// Package analytics reports administrator-only logical usage and filesystem capacity.
package analytics

import (
	"context"
	"database/sql"
	"math"
	"sync"
	"time"

	"github.com/pinkhit/vault/backend/internal/audit"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/hoststorage"
	"github.com/pinkhit/vault/backend/internal/storage"
)

const CapacityTTL = 30 * time.Second

type TypeUsage struct {
	MediaType    string `json:"media_type"`
	Count        int64  `json:"count"`
	LogicalBytes int64  `json:"logical_bytes"`
}

type Documents struct {
	Count        int64       `json:"count"`
	LogicalBytes int64       `json:"logical_bytes"`
	ByType       []TypeUsage `json:"by_type"`
}

type Filesystem struct {
	Source    string            `json:"source"`
	CheckedAt time.Time         `json:"checked_at"`
	Capacity  *storage.Capacity `json:"capacity"` // null on failure, never invented zero values.
}

type Report struct {
	Documents            Documents           `json:"documents"`
	Filesystem           Filesystem          `json:"filesystem"`
	HostStorage          *hoststorage.Sample `json:"host_storage"`
	GrowthAvailableBytes *int64              `json:"growth_available_bytes"`
}

type Service struct {
	HostStorageFile string // Fixed operator configuration, never request input.
	db              *sql.DB
	measure         func() (storage.Capacity, error)
	source          string
	now             func() time.Time
	mu              sync.Mutex
	cached          Filesystem
}

func New(db *sql.DB, measure func() (storage.Capacity, error), container bool) *Service {
	source := "native-filesystem"
	if container {
		source = "container-filesystem"
	}
	return &Service{db: db, measure: measure, source: source, now: time.Now}
}

func (s *Service) Overview(ctx context.Context, actor auth.User) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	logical, err := s.documents(ctx, actor)
	if err != nil {
		return Report{}, err
	}
	// The filesystem call holds no SQLite transaction. Failures are cached too.
	physical := s.filesystem()
	host := hoststorage.Read(s.HostStorageFile, s.now())
	var growth *int64
	if host != nil && physical.Capacity != nil {
		ceiling := min(host.FreeBytes, physical.Capacity.AvailableBytes)
		growth = &ceiling
	}
	// Check authority again after sampling, including for cached role snapshots.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback()
	if err = auth.RequireAdmin(ctx, tx, actor); err != nil {
		return Report{}, err
	}
	if err = audit.Record(ctx, tx, actor.ID, "storage.analytics", "", "allowed"); err != nil {
		return Report{}, err
	}
	if err = tx.Commit(); err != nil {
		return Report{}, err
	}
	return Report{Documents: logical, Filesystem: physical, HostStorage: host, GrowthAvailableBytes: growth}, nil
}

func (s *Service) documents(ctx context.Context, actor auth.User) (Documents, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Documents{}, err
	}
	defer tx.Rollback()
	if err = auth.RequireAdmin(ctx, tx, actor); err != nil {
		return Documents{}, err
	}
	rows, err := tx.QueryContext(ctx, `
        SELECT media_type, COUNT(*), SUM(size) FROM documents
        GROUP BY media_type ORDER BY SUM(size) DESC, media_type ASC`)
	if err != nil {
		return Documents{}, err
	}
	defer rows.Close()
	result := Documents{ByType: make([]TypeUsage, 0)}
	for rows.Next() {
		var item TypeUsage
		if err = rows.Scan(&item.MediaType, &item.Count, &item.LogicalBytes); err != nil {
			return Documents{}, err
		}
		if item.Count < 0 || item.LogicalBytes < 0 || result.Count > math.MaxInt64-item.Count || result.LogicalBytes > math.MaxInt64-item.LogicalBytes {
			return Documents{}, storage.ErrCapacity
		}
		result.Count += item.Count
		result.LogicalBytes += item.LogicalBytes
		result.ByType = append(result.ByType, item)
	}
	if err = rows.Err(); err != nil {
		return Documents{}, err
	}
	if err = rows.Close(); err != nil {
		return Documents{}, err
	}
	return result, tx.Commit()
}

func (s *Service) filesystem() Filesystem {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	age := now.Sub(s.cached.CheckedAt)
	if !s.cached.CheckedAt.IsZero() && age >= 0 && age < CapacityTTL {
		return s.cached
	}
	s.cached = Filesystem{Source: s.source, CheckedAt: now}
	if measured, err := s.measure(); err == nil {
		s.cached.Capacity = &measured
	}
	return s.cached
}
