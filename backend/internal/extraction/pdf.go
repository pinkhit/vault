package extraction

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/pinkhit/vault/backend/internal/pdfutil"
)

func pdfText(ctx context.Context, file *os.File) ([]string, error) {
	data, err := pdfutil.Run(ctx, file, "pdftotext", MaxOutputBytes,
		"-q", "-enc", "UTF-8", "-eol", "unix", "-f", "1", "-l", strconv.Itoa(MaxPages+1), "-", "-")
	switch {
	case errors.Is(err, pdfutil.ErrLimit):
		return nil, ErrLimit
	case errors.Is(err, pdfutil.ErrUnavailable):
		return nil, ErrUnavailable
	case errors.Is(err, pdfutil.ErrMalformed):
		return nil, ErrMalformed
	case err != nil:
		return nil, err
	}
	text := string(data)
	if !strings.HasSuffix(text, "\f") {
		return nil, ErrMalformed
	}
	if strings.Count(text, "\f") > MaxPages {
		return nil, ErrLimit
	}
	return strings.Split(strings.TrimSuffix(text, "\f"), "\f"), nil
}
