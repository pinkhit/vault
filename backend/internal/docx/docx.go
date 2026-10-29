// Package docx reads a bounded, text-only subset of WordprocessingML.
// It never unpacks files, follows relationships or renders uploaded markup.
package docx

import (
	"context"
	"encoding/xml"
	"io"
	"strings"

	"github.com/pinkhit/vault/backend/internal/office"
)

const (
	MaxInputBytes       = 25 << 20
	MaxTextBytes        = 2 << 20
	maxPartBytes        = 4 << 20
	maxParts            = 256
	wordNamespace       = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	strictWordNamespace = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	mainContentType     = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)

var (
	ErrMalformed = office.ErrMalformed
	ErrLimit     = office.ErrLimit
)

// Text returns main-body paragraphs, including table text, without page numbers.
// Headers, footnotes, deleted text, field instructions and embedded objects are
// omitted. Office layout and executable content are never interpreted.
func Text(ctx context.Context, source io.ReaderAt, size int64) (string, error) {
	parts, err := office.Open(ctx, source, size)
	if err != nil {
		return "", err
	}
	if err = parts.RequireMain(ctx, "word/document.xml", mainContentType); err != nil {
		return "", err
	}
	document, err := parts.Read(ctx, "word/document.xml", maxPartBytes)
	if err != nil {
		return "", err
	}
	return bodyText(ctx, document)
}

func bodyText(ctx context.Context, data []byte) (string, error) {
	var output strings.Builder
	body, text, skipped := 0, 0, 0
	seenBody := false
	write := func(value string) error {
		if len(value) > MaxTextBytes-output.Len() {
			return ErrLimit
		}
		output.WriteString(value)
		return nil
	}
	err := office.Walk(ctx, data, xml.Name{Local: "document"}, func(token xml.Token, depth int) error {
		switch value := token.(type) {
		case xml.StartElement:
			word := value.Name.Space == wordNamespace || value.Name.Space == strictWordNamespace
			if depth == 1 && !word {
				return ErrMalformed
			}
			if !word || skipped != 0 {
				return nil
			}
			if value.Name.Local == "body" && depth == 2 {
				if seenBody {
					return ErrMalformed
				}
				body, seenBody = depth, true
			}
			if body == 0 {
				return nil
			}
			switch value.Name.Local {
			case "del", "moveFrom", "instrText":
				skipped = depth
			case "t":
				text = depth
			case "tab":
				return write("\t")
			case "br", "cr":
				return write("\n")
			}
		case xml.CharData:
			if text != 0 && skipped == 0 {
				return write(string(value))
			}
		case xml.EndElement:
			if skipped == depth {
				skipped = 0
			}
			if text == depth {
				text = 0
			}
			if body != 0 && skipped == 0 && (value.Name.Space == wordNamespace || value.Name.Space == strictWordNamespace) {
				if value.Name.Local == "p" {
					if err := write("\n\n"); err != nil {
						return err
					}
				}
			}
			if body == depth {
				body = 0
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !seenBody {
		return "", ErrMalformed
	}
	return strings.TrimSpace(output.String()), nil
}
