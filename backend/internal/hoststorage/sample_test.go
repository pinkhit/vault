package hoststorage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotValidityFreshnessAndFileBoundary(t *testing.T) {
	now := time.Now().UTC()
	sample := Sample{Version: 1, CollectedAt: now, TotalBytes: 1000, FreeBytes: 200, AvailableIncludingReclaimableBytes: 250}
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.json")
	if err := Publish(dir, &sample); err != nil {
		t.Fatal(err)
	}
	if got := Read(path, now); got == nil || *got != sample {
		t.Fatal("published sample was not readable", got)
	}
	if Read(path, now.Add(MaxAge+time.Second)) != nil || Read(path, now.Add(-time.Minute)) != nil {
		t.Fatal("stale/future sample was accepted")
	}
	for _, mutate := range []func(*Sample){
		func(s *Sample) { s.Version = 2 }, func(s *Sample) { s.FreeBytes = -1 },
		func(s *Sample) { s.AvailableIncludingReclaimableBytes = 1001 },
		func(s *Sample) { s.AvailableIncludingReclaimableBytes = 100 },
		func(s *Sample) { s.TotalBytes = 0 },
	} {
		invalid := sample
		mutate(&invalid)
		if err := Publish(dir, &invalid); err != nil {
			t.Fatal(err)
		}
		if Read(path, now) != nil {
			t.Fatal("invalid capacity was accepted", invalid)
		}
	}
	encoded, _ := json.Marshal(sample)
	for _, body := range []string{"null", "{}", string(encoded) + " {}", strings.Repeat("x", 4097), strings.Replace(string(encoded), `"version":1`, `"private_path":"/secret","version":1`, 1)} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if Read(path, now) != nil {
			t.Fatal("malformed or unallowlisted data was accepted")
		}
	}
	if err := Publish(dir, &sample); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if Read(link, now) != nil || Read("", now) != nil || Read(filepath.Join(dir, "missing"), now) != nil {
		t.Fatal("unavailable or symlinked sample was accepted")
	}
	if err := Publish(dir, nil); err != nil || Read(path, now) != nil {
		t.Fatal("failed collection retained a previous valid sample", err)
	}
}
