package extraction

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Limits apply only to the child, never to the Go server. No document strings are
// interpolated into this fixed script; the verified descriptor is passed as stdin.
const pdfLimits = `ulimit -c 0 && ulimit -f 0 && ulimit -t 10 && ulimit -v 262144 || exit 125
exec "$@"`

func pdfText(ctx context.Context, file *os.File) ([]string, error) {
	// Fail closed where the Linux address-space limit cannot be guaranteed.
	if runtime.GOOS != "linux" {
		return nil, ErrUnavailable
	}
	tool, err := exec.LookPath("pdftotext")
	if err != nil {
		return nil, ErrUnavailable
	}
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	output := cappedOutput{cancel: cancel}
	command := exec.CommandContext(bounded, "/bin/sh", "-c", pdfLimits, "familyvault-pdf", tool,
		"-q", "-enc", "UTF-8", "-eol", "unix", "-f", "1", "-l", strconv.Itoa(MaxPages+1), "-", "-")
	command.Stdin = file
	command.Stdout = &output
	command.Stderr = io.Discard // Parser diagnostics may contain private document text.
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/nonexistent"}
	command.WaitDelay = time.Second
	err = command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if output.exceeded {
		return nil, ErrLimit
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() != 125 {
			return nil, ErrMalformed
		}
		return nil, ErrUnavailable
	}
	text := output.buffer.String()
	if !strings.HasSuffix(text, "\f") {
		return nil, ErrMalformed
	}
	if strings.Count(text, "\f") > MaxPages {
		return nil, ErrLimit
	}
	return strings.Split(strings.TrimSuffix(text, "\f"), "\f"), nil
}

type cappedOutput struct {
	// A named buffer avoids exposing ReadFrom, which would bypass Write's cap.
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (w *cappedOutput) Write(data []byte) (int, error) {
	if len(data) > MaxOutputBytes-w.buffer.Len() {
		w.exceeded = true
		w.cancel() // Terminate the parser as soon as it exceeds the output budget.
		return 0, ErrLimit
	}
	return w.buffer.Write(data)
}
