// Package docx reads a bounded, text-only subset of WordprocessingML.
// It never unpacks files, follows relationships or renders uploaded markup.
package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strings"
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
	ErrMalformed = errors.New("invalid or unsupported DOCX structure")
	ErrLimit     = errors.New("DOCX exceeds resource limits")
)

// Text returns main-body paragraphs, including table text, without page numbers.
// Headers, footnotes, deleted text, field instructions and embedded objects are
// omitted. Office layout and executable content are never interpreted.
func Text(ctx context.Context, source io.ReaderAt, size int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if size > MaxInputBytes {
		return "", ErrLimit
	}
	if err := checkDirectory(source, size); err != nil {
		return "", err
	}
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return "", ErrMalformed
	}
	if len(archive.File) > maxParts {
		return "", ErrLimit
	}
	parts := make(map[string]*zip.File, len(archive.File))
	var expanded uint64
	for _, part := range archive.File {
		name := strings.TrimSuffix(part.Name, "/")
		if name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") ||
			strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") ||
			parts[part.Name] != nil || (!part.Mode().IsRegular() && !part.FileInfo().IsDir()) ||
			part.Flags&1 != 0 || (part.Method != zip.Store && part.Method != zip.Deflate) {
			return "", ErrMalformed
		}
		if part.UncompressedSize64 > (16<<20)-expanded {
			return "", ErrLimit
		}
		expanded += part.UncompressedSize64
		parts[part.Name] = part
	}
	read := func(name string, limit int64) ([]byte, error) {
		part := parts[name]
		if part == nil {
			return nil, ErrMalformed
		}
		if part.UncompressedSize64 > uint64(limit) {
			return nil, ErrLimit
		}
		reader, err := part.Open()
		if err != nil {
			return nil, ErrMalformed
		}
		defer reader.Close()
		data, err := io.ReadAll(io.LimitReader(contextReader{ctx, reader}, limit+1))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(data) > int(limit) {
			return nil, ErrLimit
		}
		if err != nil {
			return nil, ErrMalformed
		} // Includes ZIP checksum failures.
		return data, nil
	}
	contentTypes, err := read("[Content_Types].xml", 64<<10)
	if err != nil {
		return "", err
	}
	validType := false
	err = walk(ctx, contentTypes, xml.Name{Space: "http://schemas.openxmlformats.org/package/2006/content-types", Local: "Types"}, func(token xml.Token, depth int) error {
		if start, ok := token.(xml.StartElement); ok && depth == 2 && start.Name.Space == "http://schemas.openxmlformats.org/package/2006/content-types" && start.Name.Local == "Override" {
			if attr(start, "PartName") == "/word/document.xml" {
				if validType || attr(start, "ContentType") != mainContentType {
					return ErrMalformed
				}
				validType = true
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !validType {
		return "", ErrMalformed
	}
	relationships, err := read("_rels/.rels", 64<<10)
	if err != nil {
		return "", err
	}
	validRelationship := false
	err = walk(ctx, relationships, xml.Name{Space: "http://schemas.openxmlformats.org/package/2006/relationships", Local: "Relationships"}, func(token xml.Token, depth int) error {
		if start, ok := token.(xml.StartElement); ok && depth == 2 && start.Name.Space == "http://schemas.openxmlformats.org/package/2006/relationships" && start.Name.Local == "Relationship" {
			kind := attr(start, "Type")
			if kind == "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" || kind == "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument" {
				mode := attr(start, "TargetMode")
				if validRelationship || (attr(start, "Target") != "word/document.xml" && attr(start, "Target") != "/word/document.xml") || (mode != "" && mode != "Internal") {
					return ErrMalformed
				}
				validRelationship = true
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !validRelationship {
		return "", ErrMalformed
	}
	document, err := read("word/document.xml", maxPartBytes)
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
	err := walk(ctx, data, xml.Name{Local: "document"}, func(token xml.Token, depth int) error {
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

// Bound the central-directory bytes before archive/zip allocates its file list.
// Even dishonest entry counts cannot fit more than 64 KiB of directory records.
// ZIP64, split archives, archive comments and trailers fail closed.
func checkDirectory(source io.ReaderAt, size int64) error {
	if size < 22 {
		return ErrMalformed
	}
	header := make([]byte, 22)
	if _, err := source.ReadAt(header, size-22); err != nil || !bytes.Equal(header[:4], []byte("PK\x05\x06")) {
		return ErrMalformed
	}
	u16 := func(at int) uint16 { return binary.LittleEndian.Uint16(header[at:]) }
	u32 := func(at int) int64 { return int64(binary.LittleEndian.Uint32(header[at:])) }
	if u16(4) != 0 || u16(6) != 0 || u16(8) != u16(10) || u16(20) != 0 {
		return ErrMalformed
	}
	if u16(10) > maxParts || u32(12) > 64<<10 {
		return ErrLimit
	}
	if u32(16)+u32(12) != size-22 {
		return ErrMalformed
	}
	return nil
}

func walk(ctx context.Context, data []byte, root xml.Name, visit func(xml.Token, int) error) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for tokens := 0; ; tokens++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tokens >= 200000 {
			return ErrLimit
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if roots != 1 || depth != 0 {
				return ErrMalformed
			}
			return nil
		}
		if err != nil {
			return ErrMalformed
		}
		switch value := token.(type) {
		case xml.Directive:
			return ErrMalformed // No DTD or entity declarations.
		case xml.StartElement:
			depth++
			if depth > 64 {
				return ErrLimit
			}
			if depth == 1 {
				roots++
				if roots != 1 || value.Name.Local != root.Local || (root.Space != "" && value.Name.Space != root.Space) {
					return ErrMalformed
				}
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return ErrMalformed
			}
		}
		if err := visit(token, depth); err != nil {
			return err
		}
		if _, ok := token.(xml.EndElement); ok {
			depth--
		}
	}
}

func attr(element xml.StartElement, name string) string {
	for _, attribute := range element.Attr {
		if attribute.Name.Space == "" && attribute.Name.Local == name {
			return attribute.Value
		}
	}
	return ""
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
