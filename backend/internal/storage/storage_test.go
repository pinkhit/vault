package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type brokenReader struct{ sent bool }

func (r *brokenReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, "partial bytes"), nil
}

func TestInterruptedWriteCleansStaging(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Stage(strings.Repeat("a", 32), &brokenReader{}, 1024); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatal("partial write left behind")
	}
}

func TestExclusiveDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("second writer accepted")
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestRejectUnsafeStorage(t *testing.T) {
	for _, name := range []string{"objects", "staging", "metadata.sqlite", "metadata.sqlite-wal", "server.lock"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := t.TempDir()
			if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			s, err := Open(dir)
			if err == nil {
				s.Close()
				t.Fatal("unsafe storage accepted")
			}
		})
	}
}

func TestStorageKeysAndPermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"../outside", "", strings.Repeat("g", 32), strings.Repeat("a", 31), strings.Repeat("A", 32)} {
		if _, err = s.Stage(id, strings.NewReader("x"), 10); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	id := strings.Repeat("a", 32)
	if _, err = s.Stage(id, strings.NewReader("x"), 10); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "staging", id))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode: %v", info.Mode())
	}
	info, err = os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode: %v", info.Mode())
	}
}
