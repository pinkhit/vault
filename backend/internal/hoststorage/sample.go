// Package hoststorage reads allowlisted macOS measurements from a host-written
// snapshot. The container never gets a host command endpoint or filesystem root.
package hoststorage

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const MaxAge = 90 * time.Second

type Sample struct {
	Version                            int       `json:"version"`
	CollectedAt                        time.Time `json:"collected_at"`
	TotalBytes                         int64     `json:"total_bytes"`
	FreeBytes                          int64     `json:"free_bytes"`
	AvailableIncludingReclaimableBytes int64     `json:"available_including_reclaimable_bytes"`
}

func (s Sample) Valid(now time.Time) bool {
	return s.Version == 1 && !s.CollectedAt.IsZero() &&
		now.Sub(s.CollectedAt) >= -5*time.Second && now.Sub(s.CollectedAt) <= MaxAge &&
		s.TotalBytes > 0 && s.FreeBytes >= 0 && s.FreeBytes <= s.AvailableIncludingReclaimableBytes &&
		s.AvailableIncludingReclaimableBytes <= s.TotalBytes
}

func Read(path string, now time.Time) *Sample {
	if path == "" {
		return nil
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var sample Sample
	if decoder.Decode(&sample) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || !sample.Valid(now) {
		return nil
	}
	return &sample
}

// Publish replaces a complete sample atomically. The file contains only counters
// and a timestamp; it is readable through one read-only container directory mount.
func Publish(directory string, sample *Sample) error {
	file, err := os.CreateTemp(directory, ".sample-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0644); err == nil {
		err = json.NewEncoder(file).Encode(sample)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, "storage.json"))
}
