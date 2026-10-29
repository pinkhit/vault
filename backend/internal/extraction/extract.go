// Package extraction derives bounded plain-text passages from verified originals.
// It does not authorize users, mutate documents or schedule indexing work.
package extraction

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pinkhit/vault/backend/internal/storage"
)

const (
	MaxInputBytes   = 25 << 20
	MaxOutputBytes  = 2 << 20
	MaxPages        = 200
	MaxPassages     = 2048
	MaxPassageBytes = 4096
	Timeout         = 10 * time.Second
)

var (
	ErrUnsupported = errors.New("document format is not supported for extraction")
	ErrMalformed   = errors.New("document cannot be parsed safely")
	ErrLimit       = errors.New("document exceeds extraction limits")
	ErrNoText      = errors.New("document has no extractable text")
	ErrUnavailable = errors.New("bounded PDF extraction is unavailable")
)

// Source is a trusted metadata snapshot, not a caller-supplied filesystem path.
type Source struct {
	ID        string
	Name      string
	MediaType string
	Size      int64
	SHA256    string
}

type Passage struct {
	Page    int // One-based for PDF; zero for TXT.
	Section int // One-based within the page, or within the TXT document.
	Text    string
}

func Extract(ctx context.Context, store *storage.Store, source Source) ([]Passage, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.Size == 0 {
		return nil, ErrNoText
	}
	if source.Size < 0 {
		return nil, ErrMalformed
	}
	if source.Size > MaxInputBytes {
		return nil, ErrLimit
	}
	mediaType, _, err := mime.ParseMediaType(source.MediaType)
	extension := strings.ToLower(filepath.Ext(source.Name))
	if err != nil || !((extension == ".txt" && mediaType == "text/plain") ||
		(extension == ".pdf" && mediaType == "application/pdf")) {
		return nil, ErrUnsupported
	}
	file, err := store.OpenVerifiedContext(ctx, source.ID, source.Size, source.SHA256)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var prefix [512]byte
	n, err := file.ReadAt(prefix[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(prefix[:n]))
	if detected != mediaType {
		return nil, ErrMalformed
	}
	var pages []string
	if extension == ".pdf" {
		pages, err = pdfText(ctx, file)
	} else {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(file, MaxOutputBytes+1))
		if err == nil && len(data) > MaxOutputBytes {
			err = ErrLimit
		}
		pages = []string{string(data)}
	}
	if err != nil {
		return nil, err
	}
	var passages []Passage
	for i, page := range pages {
		pageNumber := 0
		if extension == ".pdf" {
			pageNumber = i + 1
		}
		if err = appendPassages(ctx, &passages, page, pageNumber); err != nil {
			return nil, err // Never return partial results as successful extraction.
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if len(passages) == 0 {
		return nil, ErrNoText
	}
	return passages, nil
}

func appendPassages(ctx context.Context, passages *[]Passage, text string, page int) error {
	if !utf8.ValidString(text) {
		return ErrMalformed
	}
	for _, r := range text {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return ErrMalformed
		}
	}
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	section := 0
	for _, paragraph := range strings.Split(strings.Join(lines, "\n"), "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		for paragraph != "" {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(*passages) >= MaxPassages {
				return ErrLimit
			}
			end := min(len(paragraph), MaxPassageBytes)
			if end < len(paragraph) {
				for !utf8.RuneStart(paragraph[end]) {
					end--
				}
				if space := strings.LastIndexByte(paragraph[:end], ' '); space >= MaxPassageBytes/2 {
					end = space
				}
			}
			section++
			*passages = append(*passages, Passage{Page: page, Section: section, Text: strings.TrimSpace(paragraph[:end])})
			paragraph = strings.TrimSpace(paragraph[end:])
		}
	}
	return nil
}
