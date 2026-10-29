package preview

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/pinkhit/vault/backend/internal/pdfutil"
)

func pdf(ctx context.Context, file *os.File, page int) (Result, error) {
	info, err := pdfutil.Run(ctx, file, "pdfinfo", 64<<10, "-")
	if err != nil {
		return Result{}, err
	}
	pages := 0
	for _, line := range strings.Split(string(info), "\n") {
		if value, ok := strings.CutPrefix(line, "Pages:"); ok {
			pages, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	if pages < 1 {
		return Result{}, ErrMalformed
	}
	if pages > MaxPages {
		return Result{}, ErrLimit
	}
	if page < 1 || page > pages {
		return Result{}, ErrPage
	}
	number := strconv.Itoa(page)
	data, err := pdfutil.Run(ctx, file, "pdftoppm", MaxImageBytes,
		"-q", "-f", number, "-l", number, "-singlefile", "-scale-to", "1600", "-png", "-")
	if err != nil {
		return Result{}, err
	}
	result, err := raster(ctx, bytes.NewReader(data))
	result.Kind, result.Page, result.Pages = "pdf", page, pages
	return result, err
}
