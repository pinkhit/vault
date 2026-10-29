// Package indexing manages durable job state. Extraction and worker scheduling
// are deliberately separate from these short database operations.
package indexing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"
)

type Status string

const (
	Pending     Status = "pending"
	Processing  Status = "processing"
	Ready       Status = "ready"
	Failed      Status = "failed"
	Unsupported Status = "unsupported"
	MaxAttempts        = 3
)

var (
	ErrStale  = errors.New("indexing attempt is no longer current")
	ErrResult = errors.New("invalid indexing result")
)

type Job struct {
	DocumentID   string
	SourceSHA256 string
	Status       Status
	Attempts     int
	AttemptToken string
	LastError    string
	CreatedAt    int64
	UpdatedAt    int64
	AvailableAt  int64
}

type Result struct {
	Status    Status
	LastError string // Sanitized operational message; never document text or credentials.
	RetryAt   time.Time
}

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Enqueue must share the document insert/replacement transaction. Re-enqueueing
// identical bytes preserves current work; a different checksum invalidates it.
func Enqueue(ctx context.Context, tx *sql.Tx, documentID string) error {
	var checksum string
	if err := tx.QueryRowContext(ctx, "SELECT sha256 FROM documents WHERE id = ?", documentID).Scan(&checksum); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
        INSERT INTO document_index_jobs(document_id, source_sha256) VALUES (?, ?)
        ON CONFLICT(document_id) DO UPDATE SET
            source_sha256 = excluded.source_sha256, status = 'pending',
            attempts = 0, attempt_token = '', last_error = '',
            updated_at = unixepoch(), available_at = unixepoch()
        WHERE document_index_jobs.source_sha256 <> excluded.source_sha256`, documentID, checksum)
	return err
}

// Claim atomically reserves one due job. sql.ErrNoRows means no work is ready.
// Extraction must happen after this statement releases the database connection.
func Claim(ctx context.Context, db *sql.DB) (Job, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Job{}, err
	}
	var job Job
	err := db.QueryRowContext(ctx, `
        UPDATE document_index_jobs
        SET status = 'processing', attempts = attempts + 1,
            attempt_token = ?, updated_at = unixepoch()
        WHERE document_id = (
            SELECT j.document_id FROM document_index_jobs j
            JOIN documents d ON d.id = j.document_id AND d.sha256 = j.source_sha256
            WHERE j.status = 'pending' AND j.available_at <= unixepoch() AND j.attempts < ?
            ORDER BY j.available_at, j.created_at, j.document_id LIMIT 1
        )
        RETURNING document_id, source_sha256, status, attempts, attempt_token,
                  last_error, created_at, updated_at, available_at`, hex.EncodeToString(token[:]), MaxAttempts,
	).Scan(&job.DocumentID, &job.SourceSHA256, &job.Status, &job.Attempts,
		&job.AttemptToken, &job.LastError, &job.CreatedAt, &job.UpdatedAt, &job.AvailableAt)
	return job, err
}

// Finish accepts only the current processing attempt against the current bytes.
// Pending schedules a retry; all other accepted results are terminal.
// Passage writes share this transaction and roll back on ErrStale.
func Finish(ctx context.Context, db Executor, job Job, result Result) error {
	if !utf8.ValidString(result.LastError) || len(result.LastError) > 512 {
		return ErrResult
	}
	switch result.Status {
	case Ready:
		if result.LastError != "" {
			return ErrResult
		}
	case Failed:
		if result.LastError == "" {
			return ErrResult
		}
	case Unsupported:
	case Pending:
		if job.Attempts >= MaxAttempts || result.LastError == "" || result.RetryAt.IsZero() || result.RetryAt.Unix() < 0 {
			return ErrResult
		}
	default:
		return ErrResult
	}
	if result.Status != Pending && !result.RetryAt.IsZero() {
		return ErrResult
	}
	availableAt := int64(0)
	if result.Status == Pending {
		availableAt = result.RetryAt.Unix()
	}
	updated, err := db.ExecContext(ctx, `
        UPDATE document_index_jobs
        SET status = ?, last_error = ?, attempt_token = '', updated_at = unixepoch(), available_at = ?
        WHERE document_id = ? AND source_sha256 = ? AND status = 'processing' AND attempt_token = ?
          AND EXISTS (
              SELECT 1 FROM documents d
              WHERE d.id = document_index_jobs.document_id AND d.sha256 = document_index_jobs.source_sha256
          )`,
		result.Status, result.LastError, availableAt, job.DocumentID, job.SourceSHA256, job.AttemptToken)
	if err != nil {
		return err
	}
	count, err := updated.RowsAffected()
	if err == nil && count != 1 {
		return ErrStale
	}
	return err
}

// Recover requeues interrupted attempts, retaining their attempt counts. Call
// only at startup under the existing exclusive data lock, before starting workers.
// Clearing tokens also fences late completions from abandoned attempts.
func Recover(ctx context.Context, db *sql.DB) (int64, error) {
	result, err := db.ExecContext(ctx, `
        UPDATE document_index_jobs
        SET status = CASE WHEN attempts >= ? THEN 'failed' ELSE 'pending' END,
            attempt_token = '',
            last_error = CASE WHEN attempts >= ? THEN 'indexing retry limit reached' ELSE 'processing interrupted' END,
            updated_at = unixepoch(), available_at = unixepoch()
        WHERE status = 'processing' OR (status = 'pending' AND attempts >= ?)`, MaxAttempts, MaxAttempts, MaxAttempts)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
