// Package preview converts verified originals into bounded, inert display data.
// It does not authorize requests or publish original files to the browser.
package preview

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "golang.org/x/image/webp"

	"github.com/pinkhit/vault/backend/internal/docx"
)

const (
	MaxInputBytes = 25 << 20
	MaxTextBytes  = 64 << 10
	MaxImageBytes = 8 << 20
	MaxPixels     = 4_000_000
	MaxRows       = 200
	MaxColumns    = 32
	MaxPages      = 200
	Timeout       = 10 * time.Second
)

var (
	ErrUnsupported = errors.New("preview is unavailable for this file format; download the original")
	ErrMalformed   = errors.New("file cannot be previewed safely; download the original")
	ErrLimit       = errors.New("file exceeds preview limits; download the original")
	ErrPage        = errors.New("preview page is outside the document")
	ErrBusy        = errors.New("another preview is being prepared; try again shortly")
)

// One expensive preview at a time, independently of HTTP connection count.
var slot = make(chan struct{}, 1)

type Result struct {
	Kind      string     `json:"kind"`
	Text      string     `json:"text,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	Image     []byte     `json:"image,omitempty"` // JSON base64, always a generated PNG.
	Page      int        `json:"page,omitempty"`
	Pages     int        `json:"pages,omitempty"`
	Truncated bool       `json:"truncated"`
}

func Render(ctx context.Context, file *os.File, name, mediaType string, size int64, page int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	default:
		return Result{}, ErrBusy
	}
	if size <= 0 {
		return Result{}, ErrMalformed
	}
	if size > MaxInputBytes {
		return Result{}, ErrLimit
	}
	var prefix [512]byte
	n, err := file.ReadAt(prefix[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return Result{}, err
	}
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(prefix[:n]))
	recorded, _, err := mime.ParseMediaType(mediaType)
	if err != nil || recorded != detected {
		return Result{}, ErrMalformed
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".pdf" && detected == "application/pdf" {
		return pdf(ctx, file, page)
	}
	if page != 1 {
		return Result{}, ErrPage
	}
	if ext == ".docx" {
		if detected != "application/zip" {
			return Result{}, ErrMalformed
		}
		text, err := docx.Text(ctx, file, size)
		switch {
		case errors.Is(err, docx.ErrMalformed):
			return Result{}, ErrMalformed
		case errors.Is(err, docx.ErrLimit):
			return Result{}, ErrLimit
		case err != nil:
			return Result{}, err
		}
		truncated := len(text) > MaxTextBytes
		if truncated {
			end := MaxTextBytes
			for !utf8.RuneStart(text[end]) {
				end--
			}
			text = text[:end]
		}
		return Result{Kind: "text", Text: text, Truncated: truncated}, nil
	}
	imageTypes := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}
	if expected, ok := imageTypes[ext]; ok {
		if detected != expected {
			return Result{}, ErrMalformed
		}
		if size > MaxImageBytes {
			return Result{}, ErrLimit
		}
		return raster(ctx, file)
	}
	if (ext != ".txt" && ext != ".csv" && ext != ".json") || detected != "text/plain" {
		return Result{}, ErrUnsupported
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxTextBytes+1))
	if err != nil {
		return Result{}, err
	}
	truncated := len(data) > MaxTextBytes
	if truncated {
		if ext != ".txt" {
			return Result{}, ErrLimit
		}
		end := MaxTextBytes
		for end > MaxTextBytes-utf8.UTFMax && !utf8.RuneStart(data[end]) {
			end--
		}
		data = data[:end]
	}
	if !utf8.Valid(data) {
		return Result{}, ErrMalformed
	}
	for _, r := range string(data) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return Result{}, ErrMalformed
		}
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	switch ext {
	case ".csv":
		return table(data)
	case ".json":
		// Indentation of deeply nested input can amplify a small file enormously.
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		depth := 0
		for {
			token, tokenErr := decoder.Token()
			if errors.Is(tokenErr, io.EOF) {
				break
			}
			if tokenErr != nil {
				return Result{}, ErrMalformed
			}
			if delimiter, ok := token.(json.Delim); ok {
				if delimiter == '{' || delimiter == '[' {
					depth++
				} else {
					depth--
				}
				if depth > 32 {
					return Result{}, ErrLimit
				}
			}
		}
		var formatted bytes.Buffer
		if err = json.Indent(&formatted, data, "", "  "); err != nil {
			return Result{}, ErrMalformed
		}
		if formatted.Len() > MaxTextBytes*4 {
			return Result{}, ErrLimit
		}
		return Result{Kind: "json", Text: formatted.String()}, nil
	default:
		return Result{Kind: "text", Text: string(data), Truncated: truncated}, nil
	}
}

func table(data []byte) (Result, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	result := Result{Kind: "csv", Rows: make([][]string, 0)}
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return Result{}, ErrMalformed
		}
		if len(row) > MaxColumns {
			return Result{}, ErrLimit
		}
		for _, cell := range row {
			if len(cell) > 4096 {
				return Result{}, ErrLimit
			}
		}
		if len(result.Rows) == MaxRows {
			result.Truncated = true
			return result, nil
		}
		result.Rows = append(result.Rows, row)
	}
}

func raster(ctx context.Context, input io.ReadSeeker) (Result, error) {
	config, _, err := image.DecodeConfig(input)
	if err != nil {
		return Result{}, ErrMalformed
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 ||
		int64(config.Width)*int64(config.Height) > MaxPixels {
		return Result{}, ErrLimit
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return Result{}, err
	}
	decoded, _, err := image.Decode(input)
	if err != nil {
		return Result{}, ErrMalformed
	}
	output := imageOutput{ctx: ctx}
	if err = png.Encode(&output, decoded); err != nil {
		return Result{}, err
	}
	return Result{Kind: "image", Image: output.buffer.Bytes()}, nil
}

type imageOutput struct {
	ctx    context.Context
	buffer bytes.Buffer
}

func (w *imageOutput) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > MaxImageBytes-w.buffer.Len() {
		return 0, ErrLimit
	}
	return w.buffer.Write(data)
}
