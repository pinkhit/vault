package documents

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	for _, name := range []string{"", " ", ".", "..", "../file", "a/b", `a\b`, "a\x00b", "a\nb", strings.Repeat("x", 256), string([]byte{0xff})} {
		if ValidateName(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"invoice.txt", "合同.pdf", "invoice 2026 (1).txt", "<script>.txt"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("rejected %q: %v", name, err)
		}
	}
}

func FuzzValidateName(f *testing.F) {
	for _, name := range []string{"invoice.txt", "../x", "合同.pdf", "a\x00b"} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if ValidateName(name) == nil && (len(name) == 0 || len(name) > 255 || strings.ContainsAny(name, "/\\\x00\r\n")) {
			t.Fatalf("unsafe name %q", name)
		}
	})
}
