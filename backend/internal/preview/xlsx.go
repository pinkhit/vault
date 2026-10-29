package preview

import (
	"context"
	"encoding/xml"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/pinkhit/vault/backend/internal/office"
)

const spreadsheetNS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"

type sheet struct {
	Name  string `xml:"name,attr"`
	ID    string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	State string `xml:"state,attr"`
}

type cellText struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (text cellText) String() string {
	var result strings.Builder
	result.WriteString(text.Text)
	for _, run := range text.Runs {
		result.WriteString(run.Text)
	}
	return result.String()
}

type cell struct {
	Reference string   `xml:"r,attr"`
	Type      string   `xml:"t,attr"`
	Value     string   `xml:"v"`
	Formula   *string  `xml:"f"`
	Inline    cellText `xml:"is"`
}

func xlsx(ctx context.Context, source io.ReaderAt, size int64, page int) (Result, error) {
	parts, err := office.Open(ctx, source, size)
	if err != nil {
		return Result{}, err
	}
	if err = parts.RequireMain(ctx, "xl/workbook.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"); err != nil {
		return Result{}, err
	}
	var workbook struct {
		Sheets []sheet `xml:"sheets>sheet"`
	}
	if err = parts.Decode(ctx, "xl/workbook.xml", xml.Name{Space: spreadsheetNS, Local: "workbook"}, 64<<10, &workbook); err != nil {
		return Result{}, err
	}
	if len(workbook.Sheets) == 0 {
		return Result{}, office.ErrMalformed
	}
	if len(workbook.Sheets) > 32 {
		return Result{}, office.ErrLimit
	}
	visible := make([]sheet, 0, len(workbook.Sheets))
	seen := map[string]bool{}
	for _, sheet := range workbook.Sheets {
		if sheet.ID == "" || seen[sheet.ID] || sheet.Name == "" || len(sheet.Name) > 128 {
			return Result{}, office.ErrMalformed
		}
		seen[sheet.ID] = true
		switch sheet.State {
		case "", "visible":
			visible = append(visible, sheet)
		case "hidden", "veryHidden":
		default:
			return Result{}, office.ErrMalformed
		}
	}
	if page < 1 || page > len(visible) {
		return Result{}, ErrPage
	}
	selected := visible[page-1]
	var relationships struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err = parts.Decode(ctx, "xl/_rels/workbook.xml.rels", xml.Name{Space: "http://schemas.openxmlformats.org/package/2006/relationships", Local: "Relationships"}, 64<<10, &relationships); err != nil {
		return Result{}, err
	}
	target := ""
	seen = map[string]bool{}
	for _, relationship := range relationships.Items {
		if relationship.ID == "" || seen[relationship.ID] {
			return Result{}, office.ErrMalformed
		}
		seen[relationship.ID] = true
		if relationship.ID != selected.ID {
			continue
		}
		if relationship.Type != "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" || (relationship.Mode != "" && relationship.Mode != "Internal") {
			return Result{}, office.ErrMalformed
		}
		target = relationship.Target
	}
	if target == "" || path.Clean(target) != target || strings.ContainsAny(target, "\\:\x00") {
		return Result{}, office.ErrMalformed
	}
	if strings.HasPrefix(target, "/") {
		target = strings.TrimPrefix(target, "/")
	} else {
		target = "xl/" + target
	}
	if !strings.HasPrefix(target, "xl/worksheets/") || path.Ext(target) != ".xml" {
		return Result{}, office.ErrMalformed
	}
	var shared struct {
		Items []cellText `xml:"si"`
	}
	if parts["xl/sharedStrings.xml"] != nil {
		if err = parts.Decode(ctx, "xl/sharedStrings.xml", xml.Name{Space: spreadsheetNS, Local: "sst"}, 2<<20, &shared); err != nil {
			return Result{}, err
		}
	}
	stringsTable := make([]string, len(shared.Items))
	for i, item := range shared.Items {
		stringsTable[i] = item.String()
		if len(stringsTable[i]) > 4096 {
			return Result{}, office.ErrLimit
		}
	}
	var worksheet struct {
		Rows []struct {
			Number int    `xml:"r,attr"`
			Cells  []cell `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err = parts.Decode(ctx, target, xml.Name{Space: spreadsheetNS, Local: "worksheet"}, 2<<20, &worksheet); err != nil {
		return Result{}, err
	}
	result := Result{Kind: "xlsx", SheetName: selected.Name, Page: page, Pages: len(visible), Rows: make([][]string, 0)}
	previousRow, outputBytes := 0, 0
	for _, row := range worksheet.Rows {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		number := row.Number
		if number == 0 {
			number = previousRow + 1
			if len(row.Cells) != 0 && row.Cells[0].Reference != "" {
				cellRow, _, valid := coordinates(row.Cells[0].Reference)
				if !valid {
					return Result{}, office.ErrMalformed
				}
				number = cellRow
			}
		}
		if number <= previousRow || number > 1048576 {
			return Result{}, office.ErrMalformed
		}
		previousRow = number
		if number > MaxRows {
			result.Truncated = true
		}
		previousColumn := 0
		for _, cell := range row.Cells {
			column := previousColumn + 1
			if cell.Reference != "" {
				cellRow, cellColumn, valid := coordinates(cell.Reference)
				if !valid || cellRow != number {
					return Result{}, office.ErrMalformed
				}
				column = cellColumn
			}
			if column <= previousColumn || column > 16384 {
				return Result{}, office.ErrMalformed
			}
			previousColumn = column
			value, err := cell.value(stringsTable)
			if err != nil {
				return Result{}, err
			}
			if len(value) > 4096 {
				return Result{}, office.ErrLimit
			}
			if number > MaxRows || column > MaxColumns {
				result.Truncated = true
				continue
			}
			outputBytes += len(value)
			if outputBytes > MaxTextBytes {
				return Result{}, office.ErrLimit
			}
			for len(result.Rows) < number {
				result.Rows = append(result.Rows, []string{})
			}
			for len(result.Rows[number-1]) < column {
				result.Rows[number-1] = append(result.Rows[number-1], "")
			}
			result.Rows[number-1][column-1] = value
		}
	}
	return result, nil
}

func (c cell) value(shared []string) (string, error) {
	if c.Formula != nil {
		if *c.Formula == "" {
			return "[Formula]", nil
		}
		return "=" + *c.Formula, nil
	}
	switch c.Type {
	case "s":
		index, err := strconv.Atoi(c.Value)
		if err != nil || index < 0 || index >= len(shared) {
			return "", office.ErrMalformed
		}
		return shared[index], nil
	case "inlineStr":
		return c.Inline.String(), nil
	case "b":
		if c.Value == "0" {
			return "FALSE", nil
		}
		if c.Value == "1" {
			return "TRUE", nil
		}
		return "", office.ErrMalformed
	case "", "n", "str", "e", "d":
		return c.Value, nil
	default:
		return "", office.ErrMalformed
	}
}

func coordinates(reference string) (row, column int, valid bool) {
	i := 0
	for i < len(reference) && reference[i] >= 'A' && reference[i] <= 'Z' {
		column = column*26 + int(reference[i]-'A'+1)
		if column > 16384 {
			return 0, 0, false
		}
		i++
	}
	if i == 0 || i == len(reference) {
		return 0, 0, false
	}
	for _, digit := range reference[i:] {
		if digit < '0' || digit > '9' {
			return 0, 0, false
		}
	}
	row, err := strconv.Atoi(reference[i:])
	return row, column, err == nil && row > 0 && row <= 1048576
}
