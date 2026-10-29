package preview

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pinkhit/vault/backend/internal/testutil"
)

func TestXLSXValuesCoordinatesAndSheetNavigation(t *testing.T) {
	parts := testutil.XLSXParts(
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1"><f>HYPERLINK("https://example.invalid","link")</f><v>999</v></c><c r="D1" t="b"><v>1</v></c></row><row r="3"><c r="B3" t="inlineStr"><is><r><t>合同</t></r><r><t> text</t></r></is></c></row>`,
		`<row r="1"><c r="A1"><v>12345678901234567890</v></c></row>`)
	parts["xl/sharedStrings.xml"] = `<sst xmlns="` + spreadsheetNS + `"><si><r><t>&lt;script&gt;</t></r><r><t>literal&lt;/script&gt;</t></r></si></sst>`
	data := testutil.ZIP(t, parts)
	got, err := render(t, "workbook.xlsx", data, 1)
	want := [][]string{{"<script>literal</script>", "", `=HYPERLINK("https://example.invalid","link")`, "TRUE"}, {}, {"", "合同 text"}}
	if err != nil || got.Kind != "xlsx" || got.SheetName != "Sheet 1" || got.Pages != 2 || got.Page != 1 || !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("preview=%+v error=%v", got, err)
	}
	got, err = render(t, "workbook.xlsx", data, 2)
	if err != nil || got.SheetName != "Sheet 2" || got.Rows[0][0] != "12345678901234567890" {
		t.Fatal("second sheet/precision", got, err)
	}
	if _, err := render(t, "workbook.xlsx", data, 3); !errors.Is(err, ErrPage) {
		t.Fatal("invalid sheet", err)
	}
	parts["xl/workbook.xml"] = strings.Replace(parts["xl/workbook.xml"], `name="Sheet 1"`, `state="hidden" name="Sheet 1"`, 1)
	got, err = render(t, "workbook.xlsx", testutil.ZIP(t, parts), 1)
	if err != nil || got.Pages != 1 || got.SheetName != "Sheet 2" {
		t.Fatal("hidden worksheet was listed", got, err)
	}
	got, err = render(t, "empty.xlsx", testutil.ZIP(t, testutil.XLSXParts("")), 1)
	if err != nil || len(got.Rows) != 0 {
		t.Fatal("empty sheet", got, err)
	}
	if _, err := render(t, "no-sheets.xlsx", testutil.ZIP(t, testutil.XLSXParts()), 1); !errors.Is(err, ErrMalformed) {
		t.Fatal("missing sheet list", err)
	}
}

func TestXLSXLimitsAndMalformedPackages(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]string)
		want   error
	}{
		{"not a workbook", func(p map[string]string) { delete(p, "xl/workbook.xml") }, ErrMalformed},
		{"macro workbook", func(p map[string]string) {
			p["[Content_Types].xml"] = strings.ReplaceAll(p["[Content_Types].xml"], "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml", "application/vnd.ms-excel.sheet.macroEnabled.main+xml")
		}, ErrMalformed},
		{"external sheet", func(p map[string]string) {
			p["xl/_rels/workbook.xml.rels"] = strings.ReplaceAll(p["xl/_rels/workbook.xml.rels"], `Target="worksheets/sheet1.xml"`, `Target="https://example.invalid/" TargetMode="External"`)
		}, ErrMalformed},
		{"traversal", func(p map[string]string) {
			p["xl/_rels/workbook.xml.rels"] = strings.ReplaceAll(p["xl/_rels/workbook.xml.rels"], "worksheets/sheet1.xml", "../outside.xml")
		}, ErrMalformed},
		{"duplicate relationship", func(p map[string]string) {
			p["xl/_rels/workbook.xml.rels"] = strings.ReplaceAll(p["xl/_rels/workbook.xml.rels"], "</Relationships>", `<Relationship Id="rId1"/></Relationships>`)
		}, ErrMalformed},
		{"bad XML", func(p map[string]string) { p["xl/worksheets/sheet1.xml"] = "<worksheet>" }, ErrMalformed},
		{"DTD", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = `<!DOCTYPE x [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + p["xl/worksheets/sheet1.xml"]
		}, ErrMalformed},
		{"bad shared string", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], `<c r="A1"><v>42</v></c>`, `<c r="A1" t="s"><v>9999</v></c>`)
		}, ErrMalformed},
		{"duplicate cell", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], "</row>", `<c r="A1"><v>43</v></c></row>`)
		}, ErrMalformed},
		{"wrong row", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], `r="A1"`, `r="A2"`)
		}, ErrMalformed},
		{"invalid coordinate", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], `r="A1"`, `r="A+1"`)
		}, ErrMalformed},
		{"large part", func(p map[string]string) { p["xl/worksheets/sheet1.xml"] = strings.Repeat(" ", (2<<20)+1) }, ErrLimit},
		{"large shared string", func(p map[string]string) {
			p["xl/sharedStrings.xml"] = `<sst xmlns="` + spreadsheetNS + `"><si><t>` + strings.Repeat("x", 4097) + `</t></si></sst>`
		}, ErrLimit},
		{"large repeated output", func(p map[string]string) {
			p["xl/sharedStrings.xml"] = `<sst xmlns="` + spreadsheetNS + `"><si><t>` + strings.Repeat("x", 4096) + `</t></si></sst>`
			p["xl/worksheets/sheet1.xml"] = `<worksheet xmlns="` + spreadsheetNS + `"><sheetData><row>` + strings.Repeat(`<c t="s"><v>0</v></c>`, 17) + `</row></sheetData></worksheet>`
		}, ErrLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			parts := testutil.XLSXParts(`<row r="1"><c r="A1"><v>42</v></c></row>`)
			test.change(parts)
			got, err := render(t, "bad.xlsx", testutil.ZIP(t, parts), 1)
			if !errors.Is(err, test.want) || got.Rows != nil {
				t.Fatal("partial/unsafe preview", got, err)
			}
		})
	}
	parts := testutil.XLSXParts(`<row r="1"><c r="AG1"><v>42</v></c></row><row r="1048576"><c r="XFD1048576"><v>43</v></c></row>`)
	data := testutil.ZIP(t, parts)
	got, err := render(t, "sparse.xlsx", data, 1)
	if err != nil || !got.Truncated || len(got.Rows) != 0 {
		t.Fatal("sparse coordinates exceeded display bounds", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = xlsx(ctx, bytes.NewReader(data), int64(len(data)), 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	if _, err := render(t, "macro.xlsm", data, 1); !errors.Is(err, ErrUnsupported) {
		t.Fatal("macro extension accepted", err)
	}
}
