package documents

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchLiteralGrammar(t *testing.T) {
	for query, want := range map[string]string{
		" printer invoice ":               `"printer" AND "invoice"`,
		`"supplier invoice" INV-2026-001`: `"supplier invoice" AND "INV-2026-001"`,
		"OR NOT AND":                      `"OR" AND "NOT" AND "AND"`,
		"合同 café O'Reilly":                `"合同" AND "café" AND "O'Reilly"`,
	} {
		got, err := searchExpression(query)
		if err != nil || got != want {
			t.Fatalf("query %q: expression %q, error %v", query, got, err)
		}
	}
	for _, query := range []string{"", " ", `"`, `""`, `"a"b`, `a"b`, "name:secret", "needle*", "a\nb", "---", strings.Repeat("x", 513), strings.Repeat("word ", 9), string([]byte{0xff})} {
		if _, err := searchExpression(query); !errors.Is(err, ErrSearchQuery) {
			t.Fatalf("invalid grammar accepted: %q: %v", query, err)
		}
	}
	trimmed := trimSnippet(strings.Repeat("合同", 500))
	if !utf8.ValidString(trimmed) || len(trimmed) > MaxSnippetBytes || !strings.HasSuffix(trimmed, "…") {
		t.Fatal("snippet truncation broke its UTF-8/byte bound")
	}
}

func FuzzSearchExpression(f *testing.F) {
	for _, query := range []string{"printer", `"supplier invoice"`, "INV-2026-001", `name:secret OR *`, `"a" OR "b"`, "合同", "a\x00b"} {
		f.Add(query)
	}
	literalsOnly := regexp.MustCompile(`^"[^"]+"( AND "[^"]+"){0,7}$`)
	f.Fuzz(func(t *testing.T, query string) {
		expression, err := searchExpression(query)
		if err == nil && (!utf8.ValidString(expression) || !literalsOnly.MatchString(expression) || len(expression) > MaxSearchBytes+64) {
			t.Fatalf("unbounded/executable search grammar: %q", expression)
		}
	})
}
