package preview

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pinkhit/vault/backend/internal/pdfutil"
	"github.com/pinkhit/vault/backend/internal/testutil"
)

func render(t *testing.T, name string, data []byte, page int) (Result, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "source-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	file.Seek(0, 0)
	return Render(context.Background(), file, name, http.DetectContentType(data[:min(512, len(data))]), int64(len(data)), page)
}

func TestTextTablesAndJSONRemainLiteralAndBounded(t *testing.T) {
	got, err := render(t, "unsafe <img onerror=alert(1)>.txt", []byte("Fictional <script>alert(1)</script>"), 1)
	if err != nil || got.Text != "Fictional <script>alert(1)</script>" {
		t.Fatal(got, err)
	}
	got, err = render(t, "large.txt", []byte(strings.Repeat("合", MaxTextBytes)), 1)
	if err != nil || !got.Truncated || len(got.Text) > MaxTextBytes || !utf8.ValidString(got.Text) {
		t.Fatal("text clipping broke UTF-8 or exceeded the limit", err)
	}
	got, err = render(t, "table.csv", []byte("Name,Value\n\"Fictional, Ltd\",\"=HYPERLINK(\"\"https://example.invalid\"\")\"\n\"multi\nline\",<script>\n"), 1)
	if err != nil || len(got.Rows) != 3 || got.Rows[1][0] != "Fictional, Ltd" || got.Rows[2][0] != "multi\nline" {
		t.Fatal("CSV values not parsed literally", got, err)
	}
	got, err = render(t, "large.csv", []byte(strings.Repeat("value,42\n", MaxRows+10)), 1)
	if err != nil || !got.Truncated || len(got.Rows) != MaxRows {
		t.Fatal("CSV row limit ignored", err)
	}
	got, err = render(t, "data.json", []byte(`{"html":"<svg onload=alert(1)>","large":12345678901234567890}`), 1)
	if err != nil || !strings.Contains(got.Text, "\n  ") || !strings.Contains(got.Text, "12345678901234567890") {
		t.Fatal("JSON formatting changed values", err)
	}
	for _, test := range []struct {
		name, body string
		want       error
	}{
		{"file.html", "<html>active</html>", ErrUnsupported},
		{"file.svg", "<svg onload='alert(1)'/>", ErrUnsupported},
		{"file.docx", "PK archive", ErrMalformed},
		{"file.xlsx", "PK archive", ErrMalformed},
		{"file.png", "not an image", ErrMalformed},
		{"file.txt", "a\x00b", ErrUnsupported},
		{"file.txt", "abc\xff", ErrMalformed},
		{"file.json", `{"broken":`, ErrMalformed},
		{"file.json", `{} {}`, ErrMalformed},
		{"file.json", strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33), ErrLimit},
		{"file.json", strings.Repeat(" ", MaxTextBytes) + "{}", ErrLimit},
		{"file.csv", "a,\"unclosed", ErrMalformed},
		{"file.csv", strings.Repeat("a,", MaxColumns) + "a", ErrLimit},
		{"file.csv", strings.Repeat("x", 4097), ErrLimit},
	} {
		t.Run(test.name+test.body[:min(8, len(test.body))], func(t *testing.T) {
			_, err := render(t, test.name, []byte(test.body), 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestDOCXTextPreviewAndClipping(t *testing.T) {
	data := testutil.DOCX(t, `<w:p><w:r><w:t>&lt;script&gt;literal&lt;/script&gt;</w:t></w:r></w:p>`)
	got, err := render(t, "invoice.docx", data, 1)
	if err != nil || got.Kind != "text" || got.Text != "<script>literal</script>" || got.Truncated {
		t.Fatal(got, err)
	}
	if _, err := render(t, "invoice.docx", data, 2); !errors.Is(err, ErrPage) {
		t.Fatal("invented DOCX pages", err)
	}
	data = testutil.DOCX(t, `<w:p><w:r><w:t>`+strings.Repeat("合", MaxTextBytes)+`</w:t></w:r></w:p>`)
	got, err = render(t, "large.docx", data, 1)
	if err != nil || !got.Truncated || len(got.Text) > MaxTextBytes || !utf8.ValidString(got.Text) {
		t.Fatal("invalid DOCX clipping", err)
	}
	if _, err := render(t, "renamed.docm", data, 1); !errors.Is(err, ErrUnsupported) {
		t.Fatal("macro-enabled extension accepted", err)
	}
}

func TestRasterValidationReencodingAndPixelBudget(t *testing.T) {
	for _, format := range []string{"png", "webp"} {
		fixture, err := os.ReadFile("../../../tests/fixtures/fictional-preview." + format)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = render(t, "fixture."+format, fixture, 1); err != nil {
			t.Fatal("image fixture rejected", format, err)
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var encoded bytes.Buffer
	for _, extension := range []string{"png", "jpg"} {
		encoded.Reset()
		if extension == "png" {
			png.Encode(&encoded, img)
		} else {
			jpeg.Encode(&encoded, img, nil)
		}
		// Trailing active text must not survive re-encoding.
		encoded.WriteString("<script>secret-metadata</script>")
		got, err := render(t, "test."+extension, encoded.Bytes(), 1)
		if err != nil || bytes.Contains(got.Image, []byte("secret-metadata")) {
			t.Fatal("image not safely re-encoded", err)
		}
		if _, err = png.Decode(bytes.NewReader(got.Image)); err != nil {
			t.Fatal("output is not PNG", err)
		}
	}
	encoded.Reset()
	png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 4097, 1)))
	if _, err := render(t, "wide.png", encoded.Bytes(), 1); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized image dimensions accepted", err)
	}
	encoded.Reset()
	png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2001, 2000)))
	if _, err := render(t, "pixels.png", encoded.Bytes(), 1); !errors.Is(err, ErrLimit) {
		t.Fatal("pixel budget ignored", err)
	}
	if _, err := render(t, "broken.png", []byte("\x89PNG\r\n\x1a\nbroken"), 1); !errors.Is(err, ErrMalformed) {
		t.Fatal("malformed image accepted", err)
	}
}

func TestPDFRasterPagesAndFailures(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PDF resource limits require Linux; verified in Docker")
	}
	data := testutil.PDF("First synthetic page", "Second synthetic page")
	first, err := render(t, "untrusted;$(ignored).pdf", data, 1)
	if err != nil || first.Kind != "pdf" || first.Page != 1 || first.Pages != 2 {
		t.Fatal(first, err)
	}
	second, err := render(t, "document.pdf", data, 2)
	if err != nil || second.Page != 2 || bytes.Equal(first.Image, second.Image) {
		t.Fatal("PDF page selection ignored", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(second.Image))
	if err != nil || config.Width > 1600 || config.Height > 1600 {
		t.Fatal("PDF raster dimensions unbounded", err)
	}
	if _, err = render(t, "document.pdf", data, 3); !errors.Is(err, ErrPage) {
		t.Fatal("out-of-range PDF page accepted", err)
	}
	if _, err = render(t, "broken.pdf", []byte("%PDF-1.7\nbroken"), 1); !errors.Is(err, pdfutil.ErrMalformed) {
		t.Fatal("malformed PDF accepted", err)
	}
	if _, err = render(t, "large.pdf", testutil.PDF(make([]string, MaxPages+1)...), 1); !errors.Is(err, ErrLimit) {
		t.Fatal("PDF page budget ignored", err)
	}
}

func TestCancellationConcurrencyAndParserLimits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render(ctx, nil, "ignored", "", 1, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled preview continued", err)
	}
	slot <- struct{}{}
	_, err := Render(context.Background(), nil, "ignored", "", 1, 1)
	<-slot
	if !errors.Is(err, ErrBusy) {
		t.Fatal("preview concurrency not bounded", err)
	}
	if runtime.GOOS != "linux" {
		return
	}
	for _, test := range []struct {
		script string
		want   error
	}{
		{"while :; do :; done", context.DeadlineExceeded},
		{"while :; do printf '%4096s' x; done", pdfutil.ErrLimit},
		{"printf 'private-parser-diagnostic' >&2; exit 1", pdfutil.ErrMalformed},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pdfinfo"), []byte("#!/bin/sh\n"+test.script), 0700)
		t.Setenv("PATH", dir)
		file, _ := os.CreateTemp(t.TempDir(), "pdf-")
		file.Write(testutil.PDF("test"))
		bounded, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_, err = pdf(bounded, file, 1)
		stop()
		file.Close()
		if !errors.Is(err, test.want) || strings.Contains(err.Error(), "private-parser-diagnostic") {
			t.Fatal("parser limits/diagnostic privacy failed", err)
		}
	}
}
