package indexing

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/database"
)

var ctx = context.Background()

const documentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newQueue(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`INSERT INTO documents(id,name,size,media_type,sha256,created_at)
        VALUES (?, 'fictional.txt', 12, 'text/plain', ?, '2026-01-01T00:00:00Z')`, documentID, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func enqueue(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = Enqueue(ctx, tx, documentID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func claim(t *testing.T, db *sql.DB) Job {
	t.Helper()
	job, err := Claim(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func stored(t *testing.T, db *sql.DB) Job {
	t.Helper()
	var job Job
	err := db.QueryRow(`SELECT document_id, source_sha256, status, attempts, attempt_token,
        last_error, created_at, updated_at, available_at FROM document_index_jobs WHERE document_id = ?`, documentID).
		Scan(&job.DocumentID, &job.SourceSHA256, &job.Status, &job.Attempts, &job.AttemptToken,
			&job.LastError, &job.CreatedAt, &job.UpdatedAt, &job.AvailableAt)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestRetryLifecycle(t *testing.T) {
	db := newQueue(t)
	enqueue(t, db)
	enqueue(t, db)
	initial := stored(t, db)
	if initial.Status != Pending || initial.Attempts != 0 || initial.CreatedAt <= 0 || initial.UpdatedAt < initial.CreatedAt {
		t.Fatalf("invalid initial job: %+v", initial)
	}
	first := claim(t, db)
	if first.Status != Processing || first.Attempts != 1 || len(first.AttemptToken) != 64 {
		t.Fatalf("invalid reservation: %+v", first)
	}
	if _, err := Claim(ctx, db); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("processing job was claimable", err)
	}
	retryAt := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := Finish(ctx, db, first, Result{Status: Pending, LastError: "temporary read failure", RetryAt: retryAt}); err != nil {
		t.Fatal(err)
	}
	retry := stored(t, db)
	if retry.Status != Pending || retry.Attempts != 1 || retry.LastError != "temporary read failure" || retry.AvailableAt != retryAt.Unix() || retry.AttemptToken != "" {
		t.Fatalf("lost retry metadata: %+v", retry)
	}
	if _, err := Claim(ctx, db); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("retry ran before its deadline", err)
	}
	// Advance eligibility without sleeps or dependence on wall-clock timing.
	if _, err := db.Exec("UPDATE document_index_jobs SET available_at = 0"); err != nil {
		t.Fatal(err)
	}
	second := claim(t, db)
	if second.Attempts != 2 || second.AttemptToken == first.AttemptToken || second.LastError != retry.LastError {
		t.Fatal("retry lost attempt history or reused its token")
	}
	if err := Finish(ctx, db, first, Result{Status: Ready}); !errors.Is(err, ErrStale) {
		t.Fatal("old attempt finalized a retry", err)
	}
	if err := Finish(ctx, db, second, Result{Status: Ready}); err != nil {
		t.Fatal(err)
	}
	enqueue(t, db)
	ready := stored(t, db)
	if ready.Status != Ready || ready.Attempts != 2 || ready.LastError != "" || ready.AttemptToken != "" || ready.CreatedAt != initial.CreatedAt {
		t.Fatalf("ready job reset or retained failure: %+v", ready)
	}
	if err := Finish(ctx, db, second, Result{Status: Failed, LastError: "late failure"}); !errors.Is(err, ErrStale) {
		t.Fatal("terminal state was overwritten", err)
	}
}

func TestResultsAndConstraints(t *testing.T) {
	db := newQueue(t)
	enqueue(t, db)
	job := claim(t, db)
	for _, result := range []Result{
		{Status: Processing}, {Status: "unknown"}, {Status: Failed},
		{Status: Ready, LastError: "failure"}, {Status: Pending, LastError: "failure"},
		{Status: Pending, RetryAt: time.Now().Add(time.Hour)},
		{Status: Pending, LastError: "failure", RetryAt: time.Unix(-1, 0)},
		{Status: Unsupported, RetryAt: time.Now().Add(time.Hour)},
		{Status: Failed, LastError: strings.Repeat("x", 513)},
		{Status: Failed, LastError: string([]byte{0xff})},
	} {
		if err := Finish(ctx, db, job, result); !errors.Is(err, ErrResult) {
			t.Fatalf("invalid result accepted: %+v: %v", result, err)
		}
	}
	for _, assignment := range []string{
		"status = 'unknown'", "attempts = -1", "status = 'pending'",
		"attempt_token = ''", "source_sha256 = 'invalid'", "updated_at = 0",
		"last_error = '" + strings.Repeat("x", 513) + "'",
	} {
		if _, err := db.Exec("UPDATE document_index_jobs SET " + assignment); err == nil {
			t.Fatalf("schema accepted invalid job: %s", assignment)
		}
	}
	for _, status := range []Status{Failed, Unsupported} {
		if err := Finish(ctx, db, job, Result{Status: status, LastError: "sanitized reason"}); err != nil {
			t.Fatal(err)
		}
		if got := stored(t, db); got.Status != status || got.LastError != "sanitized reason" || got.Attempts != 1 {
			t.Fatalf("terminal result not retained: %+v", got)
		}
		if _, err := Claim(ctx, db); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("terminal job automatically retried", err)
		}
		if status == Failed {
			// Different source bytes create fresh work, without rewriting history timestamps.
			if _, err := db.Exec("UPDATE documents SET sha256 = ?", strings.Repeat("c", 64)); err != nil {
				t.Fatal(err)
			}
			enqueue(t, db)
			job = claim(t, db)
		}
	}
}

func TestRestartRecoveryFencesAbandonedAttempts(t *testing.T) {
	db := newQueue(t)
	enqueue(t, db)
	old := claim(t, db)
	var path string
	var sequence int
	var name string
	if err := db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if before := stored(t, reopened); before.Status != Processing || before.AttemptToken != old.AttemptToken {
		t.Fatal("migration/reopen silently reset durable work")
	}
	if count, err := Recover(ctx, reopened); err != nil || count != 1 {
		t.Fatal("interrupted job not recovered", count, err)
	}
	if got := stored(t, reopened); got.Attempts != 1 || got.LastError != "processing interrupted" || got.AttemptToken != "" {
		t.Fatalf("recovery lost attempt metadata: %+v", got)
	}
	current := claim(t, reopened)
	if current.Attempts != 2 || current.AttemptToken == old.AttemptToken {
		t.Fatal("recovery reused an abandoned attempt")
	}
	if err := Finish(ctx, reopened, old, Result{Status: Ready}); !errors.Is(err, ErrStale) {
		t.Fatal("abandoned attempt overwrote resumed work", err)
	}
	if err := Finish(ctx, reopened, current, Result{Status: Ready}); err != nil {
		t.Fatal(err)
	}
	if count, err := Recover(ctx, reopened); err != nil || count != 0 || stored(t, reopened).Status != Ready {
		t.Fatal("recovery reset completed work", count, err)
	}
}

func TestReplacedAndDeletedSourcesRejectStaleResults(t *testing.T) {
	db := newQueue(t)
	enqueue(t, db)
	old := claim(t, db)
	if _, err := db.Exec("UPDATE documents SET sha256 = ?", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := Finish(ctx, db, old, Result{Status: Ready}); !errors.Is(err, ErrStale) {
		t.Fatal("result accepted against changed document bytes", err)
	}
	enqueue(t, db)
	if got := stored(t, db); got.Attempts != 0 || got.SourceSHA256 == old.SourceSHA256 || got.Status != Pending {
		t.Fatal("replacement did not invalidate old work")
	}
	// Return to the original checksum: a checksum alone must not revive an old attempt.
	if _, err := db.Exec("UPDATE documents SET sha256 = ?", old.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	enqueue(t, db)
	current := claim(t, db)
	if err := Finish(ctx, db, old, Result{Status: Ready}); !errors.Is(err, ErrStale) {
		t.Fatal("old attempt revived after replacement", err)
	}
	if _, err := db.Exec("DELETE FROM documents WHERE id = ?", documentID); err != nil {
		t.Fatal(err)
	}
	if err := Finish(ctx, db, current, Result{Status: Ready}); !errors.Is(err, ErrStale) {
		t.Fatal("deleted document accepted a result", err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM document_index_jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal("document deletion left orphan jobs", count, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := Enqueue(ctx, tx, documentID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing document was enqueued", err)
	}
}

func TestTransactionsAndConcurrentClaims(t *testing.T) {
	db := newQueue(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = Enqueue(ctx, tx, documentID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = Claim(ctx, db); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("job survived a rolled-back document transaction", err)
	}
	enqueue(t, db)
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		workers.Go(func() {
			_, err := Claim(ctx, db)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	claimed := 0
	for err := range results {
		if err == nil {
			claimed++
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
	}
	if claimed != 1 {
		t.Fatalf("reserved the same job %d times", claimed)
	}
	job := stored(t, db)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = Finish(ctx, tx, job, Result{Status: Ready}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if stored(t, db).Status != Processing {
		t.Fatal("finalization escaped its caller's transaction")
	}
}
