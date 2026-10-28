// Package storage stores bytes beneath an os.Root using server-generated keys.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"syscall"
)

var (
	ErrTooLarge  = errors.New("file exceeds upload limit")
	ErrEmpty     = errors.New("empty files are not supported")
	ErrIntegrity = errors.New("document integrity check failed")
	keyPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type Store struct {
	root *os.Root
	lock *os.File
}

type Staged struct {
	Size   int64
	SHA256 string
}

func ValidID(id string) bool {
	return keyPattern.MatchString(id)
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("data directory must be a real directory")
	}
	if err = os.Chmod(path, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root}
	fail := func(err error) (*Store, error) {
		s.Close()
		return nil, err
	}
	if info, err := root.Lstat("server.lock"); err == nil {
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("server.lock must be a regular file"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	lock, err := root.OpenFile("server.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail(err)
	}
	s.lock = lock
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("data directory already in use: %w", err))
	}
	for _, dir := range []string{"objects", "staging"} {
		if err = root.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return fail(err)
		}
		info, err := root.Lstat(dir)
		if err != nil {
			return fail(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail(fmt.Errorf("%s must be a real directory", dir))
		}
	}
	// Database paths are local configuration, never request input. Reject symlinked leaves.
	for _, name := range []string{"metadata.sqlite", "metadata.sqlite-wal", "metadata.sqlite-shm"} {
		info, err := root.Lstat(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
		if err == nil && !info.Mode().IsRegular() {
			return fail(fmt.Errorf("%s must be a regular file", name))
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	var err error
	if s.lock != nil {
		err = s.lock.Close()
	}
	if s.root != nil {
		err = errors.Join(err, s.root.Close())
	}
	return err
}

func (s *Store) Stage(id string, reader io.Reader, limit int64) (st Staged, err error) {
	if !ValidID(id) {
		return st, fmt.Errorf("invalid storage key")
	}
	f, err := s.root.OpenFile("staging/"+id, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return st, err
	}
	defer func() {
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			err = errors.Join(err, s.remove("staging/"+id))
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(reader, limit+1))
	if err != nil {
		return st, err
	}
	if size > limit {
		return st, ErrTooLarge
	}
	if size == 0 {
		return st, ErrEmpty
	}
	if err = f.Sync(); err != nil {
		return st, err
	}
	return Staged{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (s *Store) Promote(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid storage key")
	}
	// Link refuses to overwrite an existing object. Both directories are on the same volume.
	if err := s.root.Link("staging/"+id, "objects/"+id); err != nil {
		return err
	}
	return s.syncDir("objects")
}

func (s *Store) DiscardStage(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid storage key")
	}
	return s.remove("staging/" + id)
}

func (s *Store) OpenVerified(id string, size int64, checksum string) (*os.File, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid storage key")
	}
	info, err := s.root.Lstat("objects/" + id)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrIntegrity
	}
	f, err := s.root.Open("objects/" + id)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, f)
	if err == nil && (n != size || hex.EncodeToString(hash.Sum(nil)) != checksum) {
		err = ErrIntegrity
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Reconcile runs before serving requests while the exclusive data-directory lock is held.
// Never remove metadata: a missing/corrupt referenced object must be investigated.
func (s *Store) Reconcile(known map[string]bool) error {
	for _, dir := range []string{"staging", "objects"} {
		f, err := s.root.Open(dir)
		if err != nil {
			return err
		}
		entries, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !ValidID(entry.Name()) {
				return fmt.Errorf("unexpected entry in %s", dir)
			}
			if dir == "staging" || !known[entry.Name()] {
				if err = s.root.Remove(dir + "/" + entry.Name()); err != nil {
					return err
				}
			}
		}
		if err = s.syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) remove(path string) error {
	err := s.root.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) syncDir(path string) error {
	f, err := s.root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
