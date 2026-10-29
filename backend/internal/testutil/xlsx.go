package testutil

import (
	"fmt"
	"strings"
)

// XLSXParts builds a conventional synthetic workbook from worksheet row markup.
func XLSXParts(worksheets ...string) map[string]string {
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="main" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
	}
	var sheets, relationships strings.Builder
	for i, rows := range worksheets {
		override := fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
		parts["[Content_Types].xml"] = strings.Replace(parts["[Content_Types].xml"], "</Types>", override+"</Types>", 1)
		fmt.Fprintf(&sheets, `<sheet name="Sheet %d" sheetId="%d" r:id="rId%d"/>`, i+1, i+1, i+1)
		fmt.Fprintf(&relationships, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
		parts[fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)] = `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` + rows + `</sheetData></worksheet>`
	}
	parts["xl/workbook.xml"] = `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` + sheets.String() + `</sheets></workbook>`
	parts["xl/_rels/workbook.xml.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + relationships.String() + `</Relationships>`
	return parts
}
