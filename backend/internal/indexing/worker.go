package indexing

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pinkhit/vault/backend/internal/extraction"
	"github.com/pinkhit/vault/backend/internal/storage"
)

var errWorkerRunning = errors.New("indexing worker is already running")

type Worker struct {
	db           *sql.DB
	store        *storage.Store
	log          *slog.Logger
	extract      func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error)
	pollInterval time.Duration
	retryDelay   time.Duration
	running      sync.Mutex
}

func NewWorker(db *sql.DB, store *storage.Store, log *slog.Logger) *Worker {
	return &Worker{db: db, store: store, log: log, extract: extraction.Extract,
		pollInterval: 500 * time.Millisecond, retryDelay: 10 * time.Second}
}

// Run processes one job at a time. Recover must run under the storage lock first.
// The caller cancels and joins Run before closing SQLite or storage.
func (w *Worker) Run(ctx context.Context) error {
	if !w.running.TryLock() {
		return errWorkerRunning
	}
	defer w.running.Unlock()
	for ctx.Err() == nil {
		processed, err := w.step(ctx)
		if err != nil && ctx.Err() == nil {
			w.log.Error("indexing queue operation failed") // Never log parser errors/text.
		}
		if !processed || err != nil {
			wait(ctx, w.pollInterval)
		}
	}
	return nil
}

func (w *Worker) step(ctx context.Context) (bool, error) {
	job, err := Claim(ctx, w.db)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var source extraction.Source
	err = w.db.QueryRowContext(ctx, `SELECT id,name,media_type,size,sha256 FROM documents
        WHERE id = ? AND sha256 = ?`, job.DocumentID, job.SourceSHA256).
		Scan(&source.ID, &source.Name, &source.MediaType, &source.Size, &source.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil // Deleted/replaced work must not resurrect metadata.
	}
	if err != nil {
		return true, w.complete(ctx, job, w.retry(job, "document metadata unavailable"), nil)
	}
	// No database connection/transaction is held during extraction.
	passages, err := w.safeExtract(ctx, source)
	if ctx.Err() != nil {
		return true, ctx.Err() // Durable processing state is recovered on restart.
	}
	if err == nil && !validPassages(passages) {
		err = extraction.ErrMalformed
	}
	result := w.outcome(job, err)
	if result.Status != Ready {
		passages = nil
	}
	return true, w.complete(ctx, job, result, passages)
}

func (w *Worker) safeExtract(ctx context.Context, source extraction.Source) (passages []extraction.Passage, err error) {
	defer func() {
		if recover() != nil {
			passages, err = nil, errors.New("extractor panicked")
		}
	}()
	return w.extract(ctx, w.store, source)
}

func (w *Worker) outcome(job Job, err error) Result {
	switch {
	case err == nil:
		return Result{Status: Ready}
	case errors.Is(err, extraction.ErrUnsupported):
		return Result{Status: Unsupported, LastError: "unsupported format"}
	case errors.Is(err, extraction.ErrNoText):
		return Result{Status: Unsupported, LastError: "no extractable text"}
	case errors.Is(err, extraction.ErrLimit):
		return Result{Status: Failed, LastError: "extraction limit exceeded"}
	case errors.Is(err, extraction.ErrMalformed):
		return Result{Status: Failed, LastError: "document could not be parsed"}
	case errors.Is(err, storage.ErrIntegrity):
		return Result{Status: Failed, LastError: "original failed integrity check"}
	case errors.Is(err, os.ErrNotExist):
		return Result{Status: Failed, LastError: "original is missing"}
	case errors.Is(err, context.DeadlineExceeded):
		return w.retry(job, "extraction timed out")
	case errors.Is(err, extraction.ErrUnavailable):
		return w.retry(job, "extractor unavailable")
	default:
		return w.retry(job, "temporary extraction failure")
	}
}

func (w *Worker) retry(job Job, message string) Result {
	if job.Attempts >= MaxAttempts {
		return Result{Status: Failed, LastError: message}
	}
	return Result{Status: Pending, LastError: message,
		RetryAt: time.Now().Add(w.retryDelay * time.Duration(1<<(job.Attempts-1)))}
}

func (w *Worker) complete(ctx context.Context, job Job, result Result, passages []extraction.Passage) error {
	logged := false
	for ctx.Err() == nil {
		err := w.publish(ctx, job, result, passages)
		if err == nil || errors.Is(err, ErrStale) {
			return nil
		}
		if !logged {
			w.log.Error("indexing result could not be persisted", "document_id", job.DocumentID)
			logged = true
		}
		// Keep the claimed bounded result until storage recovers. A DB outage must
		// neither publish partial passages nor strand this job until a restart.
		wait(ctx, w.pollInterval)
	}
	return ctx.Err()
}

func (w *Worker) publish(ctx context.Context, job Job, result Result, passages []extraction.Passage) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = Finish(ctx, tx, job, result); err != nil {
		return err // Check current source/attempt before touching derived text.
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM document_passages WHERE document_id = ?", job.DocumentID); err != nil {
		return err
	}
	if len(passages) != 0 {
		statement, err := tx.PrepareContext(ctx, `INSERT INTO document_passages(document_id,source_sha256,page,section,text) VALUES (?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer statement.Close()
		for _, passage := range passages {
			if _, err = statement.ExecContext(ctx, job.DocumentID, job.SourceSHA256, passage.Page, passage.Section, passage.Text); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func validPassages(passages []extraction.Passage) bool {
	if len(passages) == 0 || len(passages) > extraction.MaxPassages {
		return false
	}
	page, section, total := -1, 0, 0
	for _, passage := range passages {
		if passage.Page != page {
			if passage.Page < page || passage.Page > extraction.MaxPages {
				return false
			}
			page, section = passage.Page, 0
		}
		total += len(passage.Text)
		if page < 0 || passage.Section != section+1 || strings.TrimSpace(passage.Text) == "" ||
			!utf8.ValidString(passage.Text) || len(passage.Text) > extraction.MaxPassageBytes || total > extraction.MaxOutputBytes {
			return false
		}
		section++
	}
	return true
}

func wait(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
