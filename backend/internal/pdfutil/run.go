// Package pdfutil runs Poppler against verified descriptors with Linux resource limits.
package pdfutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

var (
	ErrUnavailable = errors.New("bounded PDF processing is unavailable")
	ErrMalformed   = errors.New("PDF cannot be parsed safely")
	ErrLimit       = errors.New("PDF output exceeds its limit")
)

// Limits apply to the child only. Arguments never become shell source. No
// document paths, passwords or filenames are passed to Poppler or diagnostics.
const limits = `ulimit -c 0 && ulimit -f 0 && ulimit -t 10 && ulimit -v 262144 || exit 125
exec "$@"`

func Run(ctx context.Context, file *os.File, tool string, maxBytes int, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, ErrUnavailable
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return nil, ErrUnavailable
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	output := cappedOutput{max: maxBytes, cancel: cancel}
	arguments := append([]string{"-c", limits, "familyvault-pdf", path}, args...)
	command := exec.CommandContext(bounded, "/bin/sh", arguments...)
	command.Stdin = file
	command.Stdout = &output
	command.Stderr = io.Discard
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
	return output.buffer.Bytes(), nil
}

type cappedOutput struct {
	buffer   bytes.Buffer // Named so io.Copy cannot bypass the capped Write.
	max      int
	cancel   context.CancelFunc
	exceeded bool
}

func (w *cappedOutput) Write(data []byte) (int, error) {
	if len(data) > w.max-w.buffer.Len() {
		w.exceeded = true
		w.cancel()
		return 0, ErrLimit
	}
	return w.buffer.Write(data)
}
