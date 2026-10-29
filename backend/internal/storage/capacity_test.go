package storage

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCapacityAccountingReservedSpaceAndWarnings(t *testing.T) {
	got, err := capacity(4096, 1000, 300, 250)
	if err != nil || got.TotalBytes != 4096000 || got.UsedBytes != 2867200 || got.AvailableBytes != 1024000 || got.ReservedBytes != 204800 || !got.LowSpace {
		t.Fatal("invalid block accounting", got, err)
	}
	for _, test := range []struct {
		total, free uint64
		low         bool
	}{
		{100 << 30, 12 << 30, false}, // Plenty of space.
		{100 << 30, 2 << 30, true},   // Under 10%, even above 1 GiB.
		{4 << 30, 800 << 20, true},   // Under 1 GiB, even above 10%.
		{10 << 30, 1 << 30, false},   // Exact thresholds are not below them.
		{10 << 30, 0, true},
	} {
		got, err := capacity(1, test.total, test.free, test.free)
		if err != nil || got.LowSpace != test.low || got.TotalBytes != got.UsedBytes+got.AvailableBytes+got.ReservedBytes {
			t.Fatal("incorrect warning/accounting", test, got, err)
		}
	}
	for _, values := range [][4]uint64{
		{0, 100, 30, 20}, {4096, 0, 0, 0}, {4096, 100, 101, 20}, {4096, 100, 30, 31},
		{4096, math.MaxUint64, 0, 0}, {math.MaxUint64, 1, 0, 0},
	} {
		if _, err := capacity(values[0], values[1], values[2], values[3]); err == nil {
			t.Fatal("invalid/overflowing filesystem figures accepted", values)
		}
	}
}

func TestVolumeCapacityUsesTheConfinedObjectDirectory(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.VolumeCapacity()
	if err != nil || got.TotalBytes <= 0 || got.TotalBytes != got.UsedBytes+got.AvailableBytes+got.ReservedBytes {
		t.Fatal("native filesystem measurement failed", got, err)
	}
	store.Close()
	if _, err = store.VolumeCapacity(); err == nil {
		t.Fatal("closed storage unexpectedly returned a measurement")
	}
}

func TestVolumeCapacityRejectsAReplacedDirectorySymlink(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = os.Rename(filepath.Join(dir, "objects"), filepath.Join(dir, "saved-objects")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(dir, "objects")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.VolumeCapacity(); err == nil {
		t.Fatal("a substituted symlink selected an unintended filesystem")
	}
}
