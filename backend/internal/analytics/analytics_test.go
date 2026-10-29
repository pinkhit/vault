package analytics

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/storage"
)

func TestCapacityCacheExpiryFailuresAndRecovery(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	calls := 0
	fail := false
	s := New(nil, func() (storage.Capacity, error) {
		calls++
		if fail {
			return storage.Capacity{}, errors.New("private-provider-path")
		}
		return storage.Capacity{TotalBytes: 100, UsedBytes: 40, AvailableBytes: 50, ReservedBytes: 10}, nil
	}, true)
	s.now = func() time.Time { return now }
	first := s.filesystem()
	if first.Source != "container-filesystem" || first.Capacity == nil || first.CheckedAt != now {
		t.Fatal("missing measurement provenance", first)
	}
	now = now.Add(CapacityTTL - time.Second)
	if got := s.filesystem(); calls != 1 || got.CheckedAt != first.CheckedAt {
		t.Fatal("cache not reused", calls)
	}
	fail = true
	now = now.Add(time.Second)
	failed := s.filesystem()
	if calls != 2 || failed.Capacity != nil || failed.CheckedAt != now {
		t.Fatal("failed refresh retained old capacity or invented zero values", failed)
	}
	s.filesystem()
	if calls != 2 {
		t.Fatal("failure cache caused repeated sampling")
	}
	fail = false
	now = now.Add(CapacityTTL)
	if got := s.filesystem(); calls != 3 || got.Capacity == nil {
		t.Fatal("provider recovery failed", got)
	}
	now = now.Add(-time.Hour)
	s.filesystem()
	if calls != 4 {
		t.Fatal("clock rollback kept an indefinitely stale sample")
	}
}

func TestConcurrentCapacityRefreshSamplesOnce(t *testing.T) {
	var calls atomic.Int32
	s := New(nil, func() (storage.Capacity, error) {
		calls.Add(1)
		return storage.Capacity{TotalBytes: 1}, nil
	}, false)
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			if got := s.filesystem(); got.Source != "native-filesystem" || got.Capacity == nil {
				t.Error("missing concurrent snapshot")
			}
		})
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent refresh duplicated filesystem work", calls.Load())
	}
}
