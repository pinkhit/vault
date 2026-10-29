package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/pinkhit/vault/backend/internal/testutil"
)

func parts(t *testing.T, body string) map[string]string {
	t.Helper()
	fixture := testutil.DOCX(t, body)
	archive, err := zip.NewReader(bytes.NewReader(fixture), int64(len(fixture)))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name] = string(data)
	}
	return result
}

func TestBodyTextKeepsUnicodeParagraphsAndTablesInert(t *testing.T) {
	body := `<w:p><w:r><w:t>Invoice 合同 &lt;script&gt;</w:t></w:r><w:r><w:tab/><w:t>42</w:t><w:br/><w:t>next</w:t></w:r></w:p>` +
		`<w:p><w:del><w:r><w:delText>deleted</w:delText></w:r></w:del><w:r><w:instrText>INCLUDETEXT https://example.invalid</w:instrText><w:t>Visible result</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`
	data := testutil.DOCX(t, body)
	got, err := Text(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil || got != "Invoice 合同 <script>\t42\nnext\n\nVisible result\n\ncell" {
		t.Fatalf("text=%q error=%v", got, err)
	}
	strict := parts(t, body)
	strict["word/document.xml"] = strings.ReplaceAll(strict["word/document.xml"], wordNamespace, strictWordNamespace)
	data = testutil.ZIP(t, strict)
	if text, err := Text(context.Background(), bytes.NewReader(data), int64(len(data))); err != nil || text != got {
		t.Fatal("strict namespace rejected", err)
	}
}

func TestMalformedPackagesAndExpansionLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]string)
		want   error
	}{
		{"missing body part", func(p map[string]string) { delete(p, "word/document.xml") }, ErrMalformed},
		{"traversal", func(p map[string]string) { p["../outside"] = "private" }, ErrMalformed},
		{"parent entry", func(p map[string]string) { p[".."] = "private" }, ErrMalformed},
		{"absolute path", func(p map[string]string) { p["/outside"] = "private" }, ErrMalformed},
		{"backslash", func(p map[string]string) { p[`word\outside`] = "private" }, ErrMalformed},
		{"macro type", func(p map[string]string) {
			p["[Content_Types].xml"] = strings.ReplaceAll(p["[Content_Types].xml"], mainContentType, "application/vnd.ms-word.document.macroEnabled.main+xml")
		}, ErrMalformed},
		{"external main", func(p map[string]string) {
			p["_rels/.rels"] = strings.ReplaceAll(p["_rels/.rels"], `Target="word/document.xml"`, `Target="https://example.invalid/" TargetMode="External"`)
		}, ErrMalformed},
		{"doctype", func(p map[string]string) {
			p["word/document.xml"] = `<!DOCTYPE x [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + p["word/document.xml"]
		}, ErrMalformed},
		{"entity", func(p map[string]string) {
			p["word/document.xml"] = strings.ReplaceAll(p["word/document.xml"], "safe", "&unknown;")
		}, ErrMalformed},
		{"multiple roots", func(p map[string]string) { p["word/document.xml"] += p["word/document.xml"] }, ErrMalformed},
		{"malformed XML", func(p map[string]string) {
			p["word/document.xml"] = strings.TrimSuffix(p["word/document.xml"], "</w:document>")
		}, ErrMalformed},
		{"duplicate attributes", func(p map[string]string) {
			p["word/document.xml"] = strings.Replace(p["word/document.xml"], "<w:body>", `<w:body mode="first" mode="second">`, 1)
		}, ErrMalformed},
		{"wrong namespace", func(p map[string]string) {
			p["word/document.xml"] = strings.ReplaceAll(p["word/document.xml"], wordNamespace, "urn:unknown")
		}, ErrMalformed},
		{"deep XML", func(p map[string]string) {
			p["word/document.xml"] = strings.ReplaceAll(p["word/document.xml"], "safe", strings.Repeat("<w:r>", 65)+"safe"+strings.Repeat("</w:r>", 65))
		}, ErrLimit},
		{"text expansion", func(p map[string]string) {
			p["word/document.xml"] = strings.ReplaceAll(p["word/document.xml"], "safe", strings.Repeat("x", MaxTextBytes+1))
		}, ErrLimit},
		{"XML expansion", func(p map[string]string) { p["word/document.xml"] = strings.Repeat(" ", maxPartBytes+1) }, ErrLimit},
		{"many entries", func(p map[string]string) {
			for i := 0; i < maxParts; i++ {
				p[fmt.Sprint("extra", i)] = ""
			}
		}, ErrLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := parts(t, `<w:p><w:r><w:t>safe</w:t></w:r></w:p>`)
			test.change(p)
			data := testutil.ZIP(t, p)
			text, err := Text(context.Background(), bytes.NewReader(data), int64(len(data)))
			if text != "" || !errors.Is(err, test.want) {
				t.Fatalf("partial result=%q error=%v want=%v", text, err, test.want)
			}
		})
	}
}

func TestZIPMetadataChecksumAndCancellation(t *testing.T) {
	data := testutil.DOCX(t, `<w:p><w:r><w:t>safe</w:t></w:r></w:p>`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Text(ctx, bytes.NewReader(data), int64(len(data))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Text(context.Background(), bytes.NewReader(data), MaxInputBytes+1); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	for _, mutate := range []func([]byte){
		func(d []byte) { binary.LittleEndian.PutUint16(d[len(d)-12:], 0) },           // Lie about entry count.
		func(d []byte) { at := bytes.Index(d, []byte("PK\x01\x02")); d[at+16] ^= 1 }, // CRC mismatch.
		func(d []byte) { at := bytes.Index(d, []byte("PK\x01\x02")); d[at+8] |= 1 },  // Encrypted flag.
		func(d []byte) {
			at := bytes.Index(d, []byte("PK\x01\x02"))
			d[at+5] = 3
			binary.LittleEndian.PutUint32(d[at+38:], uint32(0120777)<<16)
		}, // Symlink.
	} {
		corrupt := bytes.Clone(data)
		mutate(corrupt)
		if text, err := Text(context.Background(), bytes.NewReader(corrupt), int64(len(corrupt))); text != "" || !errors.Is(err, ErrMalformed) {
			t.Fatal("unsafe ZIP accepted", text, err)
		}
	}
	var duplicate bytes.Buffer
	writer := zip.NewWriter(&duplicate)
	for i := 0; i < 2; i++ {
		file, err := writer.Create("word/document.xml")
		if err != nil {
			t.Fatal(err)
		}
		file.Write([]byte("duplicate"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Text(context.Background(), bytes.NewReader(duplicate.Bytes()), int64(duplicate.Len())); !errors.Is(err, ErrMalformed) {
		t.Fatal("duplicate entry accepted", err)
	}
}
