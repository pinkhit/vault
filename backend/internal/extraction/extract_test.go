package extraction

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pinkhit/vault/backend/internal/storage"
	"github.com/pinkhit/vault/backend/internal/testutil"
)

type original struct {
	store     *storage.Store
	source    Source
	directory string
}

func saved(t *testing.T, name string, data []byte) original {
	t.Helper()
	directory := t.TempDir()
	store, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	source := Source{ID: strings.Repeat("a", 32), Name: name, MediaType: http.DetectContentType(data[:min(len(data), 512)])}
	if len(data) != 0 {
		staged, err := store.Stage(source.ID, bytes.NewReader(data), MaxInputBytes)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Promote(source.ID); err != nil {
			t.Fatal(err)
		}
		if err = store.DiscardStage(source.ID); err != nil {
			t.Fatal(err)
		}
		source.Size, source.SHA256 = staged.Size, staged.SHA256
	}
	return original{store: store, source: source, directory: directory}
}

func TestTXTNormalizationAndReferences(t *testing.T) {
	fixture := saved(t, "notes.TXT", []byte("\ufeff  Invoice\tREF-2026\r\n Amount:\u00a0 42\r\n\r\n Second section.\r\n"))
	got, err := Extract(context.Background(), fixture.store, fixture.source)
	want := []Passage{
		{Page: 0, Section: 1, Text: "Invoice REF-2026\nAmount: 42"},
		{Page: 0, Section: 2, Text: "Second section."},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("passages = %+v, error = %v", got, err)
	}
	unicodeText := strings.Repeat("合同", MaxPassageBytes)
	fixture = saved(t, "unicode.txt", []byte(unicodeText))
	got, err = Extract(context.Background(), fixture.store, fixture.source)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for i, passage := range got {
		if len(passage.Text) > MaxPassageBytes || !utf8.ValidString(passage.Text) || passage.Section != i+1 {
			t.Fatal("invalid UTF-8 chunk boundary/reference")
		}
		joined.WriteString(passage.Text)
	}
	if joined.String() != unicodeText {
		t.Fatal("chunking lost Unicode text")
	}
}

func TestDOCXExtractionReferencesAndOriginalIntegrity(t *testing.T) {
	data := testutil.DOCX(t, `<w:p><w:r><w:t>Invoice 合同</w:t></w:r></w:p><w:p><w:r><w:t>REF-2026-007 &lt;script&gt;</w:t></w:r></w:p>`)
	fixture := saved(t, "invoice.DOCX", data)
	got, err := Extract(context.Background(), fixture.store, fixture.source)
	want := []Passage{{Section: 1, Text: "Invoice 合同"}, {Section: 2, Text: "REF-2026-007 <script>"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("passages=%+v error=%v", got, err)
	}
	file, err := fixture.store.OpenVerified(fixture.source.ID, fixture.source.Size, fixture.source.SHA256)
	if err != nil {
		t.Fatal("DOCX indexing changed original", err)
	}
	file.Close()
	empty := saved(t, "empty.docx", testutil.DOCX(t, `<w:p/>`))
	if _, err := Extract(context.Background(), empty.store, empty.source); !errors.Is(err, ErrNoText) {
		t.Fatal("empty body", err)
	}
}

func TestExtractionRejectionsAndBounds(t *testing.T) {
	for _, test := range []struct {
		name, filename, media string
		data                  []byte
		want                  error
	}{
		{"empty", "empty.txt", "", nil, ErrNoText},
		{"whitespace", "empty.txt", "", []byte(" \r\n\t"), ErrNoText},
		{"unknown format", "file.xlsx", "", []byte("PK\x03\x04archive"), ErrUnsupported},
		{"malformed DOCX", "file.docx", "", []byte("PK\x03\x04archive"), ErrMalformed},
		{"HTML", "file.html", "", []byte("<html><script>active()</script></html>"), ErrUnsupported},
		{"spoofed text", "file.txt", "text/plain", []byte("%PDF-1.7\nnot a text file"), ErrMalformed},
		{"spoofed PDF", "file.pdf", "application/pdf", []byte("ordinary text"), ErrMalformed},
		{"invalid UTF-8", "file.txt", "text/plain", []byte{0xff, 0xfe, 'x'}, ErrMalformed},
		{"binary text", "file.txt", "text/plain", []byte("a\x00b"), ErrMalformed},
		{"text output", "large.txt", "", bytes.Repeat([]byte("x"), MaxOutputBytes+1), ErrLimit},
		{"passage count", "sections.txt", "", []byte(strings.Repeat("x\n\n", MaxPassages+1)), ErrLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := saved(t, test.filename, test.data)
			if test.media != "" {
				fixture.source.MediaType = test.media
			}
			got, err := Extract(context.Background(), fixture.store, fixture.source)
			if !errors.Is(err, test.want) || got != nil {
				t.Fatalf("partial result or wrong failure: %+v, %v", got, err)
			}
			if len(test.data) != 0 {
				file, err := fixture.store.OpenVerified(fixture.source.ID, fixture.source.Size, fixture.source.SHA256)
				if err != nil {
					t.Fatal("failed extraction changed the original", err)
				}
				file.Close()
			}
		})
	}
	fixture := saved(t, "size.txt", []byte("small original"))
	fixture.source.Size = MaxInputBytes + 1
	if _, err := Extract(context.Background(), fixture.store, fixture.source); !errors.Is(err, ErrLimit) {
		t.Fatal("input size limit not enforced before opening", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Extract(ctx, fixture.store, fixture.source); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was ignored", err)
	}
	if file, err := fixture.store.OpenVerifiedContext(ctx, fixture.source.ID, 14, "unused"); !errors.Is(err, context.Canceled) || file != nil {
		t.Fatal("storage verification ignored cancellation", err)
	}
}

func TestExtractionUsesConfinedVerifiedOriginal(t *testing.T) {
	fixture := saved(t, "safe.txt", []byte("private original"))
	path := filepath.Join(fixture.directory, "objects", fixture.source.ID)
	if err := os.WriteFile(path, []byte("changed original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), fixture.store, fixture.source); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatal("corruption bypassed checksum verification", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), fixture.store, fixture.source); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatal("matching symlink target bypassed no-follow access", err)
	}
	fixture.source.ID = "../private.txt"
	if result, err := Extract(context.Background(), fixture.store, fixture.source); err == nil || result != nil {
		t.Fatal("traversal key was accepted")
	}
}

func requirePDF(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("PDF resource limits require Linux; run these tests in Docker")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Fatal("install poppler-utils before running PDF integration tests")
	}
}

func TestPDFPagesAndMalformedFiles(t *testing.T) {
	requirePDF(t)
	for _, test := range []struct {
		name string
		data []byte
		want []Passage
		err  error
	}{
		{"pages", testutil.PDF("Invoice REF-2026", "", "Last page"), []Passage{
			{Page: 1, Section: 1, Text: "Invoice REF-2026"},
			{Page: 3, Section: 1, Text: "Last page"},
		}, nil},
		{"malformed", []byte("%PDF-1.7\nmalformed body\n%%EOF\n"), nil, ErrMalformed},
		{"no text", testutil.PDF("", ""), nil, ErrNoText},
		{"page limit", testutil.PDF(make([]string, MaxPages+1)...), nil, ErrLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := saved(t, "untrusted;$(echo ignored).pdf", test.data)
			got, err := Extract(context.Background(), fixture.store, fixture.source)
			if !errors.Is(err, test.err) || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("passages = %+v, error = %v", got, err)
			}
		})
	}
}

func TestPDFProcessCancellationAndOutputLimit(t *testing.T) {
	requirePDF(t)
	fixture := saved(t, "bounded.pdf", testutil.PDF("synthetic"))
	for _, test := range []struct {
		name, script string
		want         error
	}{
		{"deadline", "while :; do :; done", context.DeadlineExceeded},
		{"output", "while :; do printf '%4096s' x; done", ErrLimit},
		{"setup failure", "exit 125", ErrUnavailable},
		{"private diagnostics", "printf 'secret-document-content' >&2; exit 1", ErrMalformed},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "pdftotext"), []byte("#!/bin/sh\n"+test.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory)
			deadline := Timeout
			if test.name == "deadline" {
				deadline = 500 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			got, err := Extract(ctx, fixture.store, fixture.source)
			if !errors.Is(err, test.want) || got != nil || strings.Contains(fmt.Sprint(err), "secret-document-content") {
				t.Fatalf("unbounded process or diagnostic disclosure: %+v, %v", got, err)
			}
		})
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := Extract(context.Background(), fixture.store, fixture.source); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing parser did not fail closed", err)
	}
}

func TestPDFResourceLimitsAndEnvironment(t *testing.T) {
	requirePDF(t)
	fixture := saved(t, "limits.pdf", testutil.PDF("synthetic"))
	directory := t.TempDir()
	script := `#!/bin/sh
test "$(ulimit -v)" = 262144 || exit 125
test "$(ulimit -t)" = 10 || exit 125
test "$(ulimit -c)" = 0 || exit 125
test "$(ulimit -f)" = 0 || exit 125
test -z "${FAMILYVAULT_TEST_SECRET+x}" || exit 125
read -r header
test "$header" = '%PDF-1.4' || exit 125
printf 'limits verified\f'
`
	if err := os.WriteFile(filepath.Join(directory, "pdftotext"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("FAMILYVAULT_TEST_SECRET", "synthetic-value")
	got, err := Extract(context.Background(), fixture.store, fixture.source)
	if err != nil || !reflect.DeepEqual(got, []Passage{{Page: 1, Section: 1, Text: "limits verified"}}) {
		t.Fatalf("child limits/environment/descriptor were not applied: %+v, %v", got, err)
	}
}
