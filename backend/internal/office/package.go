// Package office provides bounded ZIP/XML reads for Office document formats.
package office

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
	MaxInputBytes = 25 << 20
	MaxParts      = 256
)

var (
	ErrMalformed = errors.New("invalid or unsupported Office document structure")
	ErrLimit     = errors.New("Office document exceeds resource limits")
)

type Package map[string]*zip.File

// Open validates the archive without unpacking any files.
func Open(ctx context.Context, source io.ReaderAt, size int64) (Package, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size > MaxInputBytes {
		return nil, ErrLimit
	}
	if err := checkDirectory(source, size); err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return nil, ErrMalformed
	}
	if len(archive.File) > MaxParts {
		return nil, ErrLimit
	}
	parts := make(map[string]*zip.File, len(archive.File))
	var expanded uint64
	for _, part := range archive.File {
		name := strings.TrimSuffix(part.Name, "/")
		if name == "." || name == ".." || path.Clean(name) != name || strings.HasPrefix(name, "/") ||
			strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") ||
			parts[part.Name] != nil || (!part.Mode().IsRegular() && !part.FileInfo().IsDir()) ||
			part.Flags&1 != 0 || (part.Method != zip.Store && part.Method != zip.Deflate) {
			return nil, ErrMalformed
		}
		if part.UncompressedSize64 > (16<<20)-expanded {
			return nil, ErrLimit
		}
		expanded += part.UncompressedSize64
		parts[part.Name] = part
	}
	return parts, nil
}

func (parts Package) Read(ctx context.Context, name string, limit int64) ([]byte, error) {
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

// RequireMain checks the conventional main part's content type and relationship.
func (parts Package) RequireMain(ctx context.Context, name, contentType string) error {
	contentTypes, err := parts.Read(ctx, "[Content_Types].xml", 64<<10)
	if err != nil {
		return err
	}
	validType := false
	err = Walk(ctx, contentTypes, xml.Name{Space: "http://schemas.openxmlformats.org/package/2006/content-types", Local: "Types"}, func(token xml.Token, depth int) error {
		if start, ok := token.(xml.StartElement); ok && depth == 2 && start.Name.Space == "http://schemas.openxmlformats.org/package/2006/content-types" && start.Name.Local == "Override" {
			if Attr(start, "PartName") == "/"+name {
				if validType || Attr(start, "ContentType") != contentType {
					return ErrMalformed
				}
				validType = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !validType {
		return ErrMalformed
	}
	relationships, err := parts.Read(ctx, "_rels/.rels", 64<<10)
	if err != nil {
		return err
	}
	validRelationship := false
	err = Walk(ctx, relationships, xml.Name{Space: "http://schemas.openxmlformats.org/package/2006/relationships", Local: "Relationships"}, func(token xml.Token, depth int) error {
		if start, ok := token.(xml.StartElement); ok && depth == 2 && start.Name.Space == "http://schemas.openxmlformats.org/package/2006/relationships" && start.Name.Local == "Relationship" {
			kind := Attr(start, "Type")
			if kind == "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" || kind == "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument" {
				mode := Attr(start, "TargetMode")
				if validRelationship || (Attr(start, "Target") != name && Attr(start, "Target") != "/"+name) || (mode != "" && mode != "Internal") {
					return ErrMalformed
				}
				validRelationship = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !validRelationship {
		return ErrMalformed
	}
	return nil
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
	if u16(10) > MaxParts || u32(12) > 64<<10 {
		return ErrLimit
	}
	if u32(16)+u32(12) != size-22 {
		return ErrMalformed
	}
	return nil
}

func Walk(ctx context.Context, data []byte, root xml.Name, visit func(xml.Token, int) error) error {
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
			attributes := make(map[xml.Name]bool, len(value.Attr))
			for _, attribute := range value.Attr {
				if attributes[attribute.Name] {
					return ErrMalformed
				}
				attributes[attribute.Name] = true
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
		if visit != nil {
			if err := visit(token, depth); err != nil {
				return err
			}
		}
		if _, ok := token.(xml.EndElement); ok {
			depth--
		}
	}
}

// Decode validates XML bounds before decoding a small, format-specific structure.
func (parts Package) Decode(ctx context.Context, name string, root xml.Name, limit int64, output any) error {
	data, err := parts.Read(ctx, name, limit)
	if err != nil {
		return err
	}
	if err = Walk(ctx, data, root, nil); err != nil {
		return err
	}
	if err = xml.Unmarshal(data, output); err != nil {
		return ErrMalformed
	}
	return ctx.Err()
}

func Attr(element xml.StartElement, name string) string {
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
