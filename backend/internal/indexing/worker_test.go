package indexing

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/database"
	"github.com/pinkhit/vault/backend/internal/extraction"
	"github.com/pinkhit/vault/backend/internal/storage"
)

func testWorker(t *testing.T) *Worker {
	t.Helper()
	w := NewWorker(newQueue(t), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.retryDelay, w.pollInterval = 0, time.Millisecond
	w.extract = func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error) {
		return []extraction.Passage{{Section: 1, Text: "synthetic indexed text"}}, nil
	}
	enqueue(t, w.db)
	return w
}

func step(t *testing.T, w *Worker) {
	t.Helper()
	if processed, err := w.step(ctx); err != nil || !processed {
		t.Fatal("job was not processed", processed, err)
	}
}

func passageCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM document_passages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not make expected progress")
		var zero T
		return zero
	}
}

func TestWorkerExtractsAndPersistsVerifiedOriginal(t *testing.T) {
	w := testWorker(t)
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	w.store, w.extract = store, extraction.Extract
	data := "Invoice REF-2026\n\nAmount: 42"
	staged, err := store.Stage(documentID, strings.NewReader(data), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Promote(documentID); err != nil {
		t.Fatal(err)
	}
	if err = store.DiscardStage(documentID); err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.Exec("UPDATE documents SET size = ?, sha256 = ?", staged.Size, staged.SHA256); err != nil {
		t.Fatal(err)
	}
	enqueue(t, w.db)
	step(t, w)
	if got := stored(t, w.db); got.Status != Ready || got.Attempts != 1 || passageCount(t, w.db) != 2 {
		t.Fatalf("incomplete indexing: %+v", got)
	}
	var text, checksum string
	var page, section int
	err = w.db.QueryRow("SELECT text,source_sha256,page,section FROM document_passages ORDER BY section LIMIT 1").Scan(&text, &checksum, &page, &section)
	if err != nil || text != "Invoice REF-2026" || checksum != staged.SHA256 || page != 0 || section != 1 {
		t.Fatal("passage references/content not preserved", err)
	}
	file, err := store.OpenVerified(documentID, staged.Size, staged.SHA256)
	if err != nil {
		t.Fatal("indexing changed original bytes", err)
	}
	file.Close()
	if processed, err := w.step(ctx); processed || err != nil {
		t.Fatal("ready work processed twice", processed, err)
	}
}

func TestWorkerFailurePolicyAndSanitizedErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status Status
		tries  int
	}{
		{"temporary", errors.New("secret path /private/example; token=private"), Failed, MaxAttempts},
		{"panic", nil, Failed, MaxAttempts},
		{"unavailable", extraction.ErrUnavailable, Failed, MaxAttempts},
		{"timeout", context.DeadlineExceeded, Failed, MaxAttempts},
		{"unsupported", extraction.ErrUnsupported, Unsupported, 1},
		{"no text", extraction.ErrNoText, Unsupported, 1},
		{"malformed", extraction.ErrMalformed, Failed, 1},
		{"limit", extraction.ErrLimit, Failed, 1},
		{"corrupt", storage.ErrIntegrity, Failed, 1},
		{"missing", os.ErrNotExist, Failed, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := testWorker(t)
			calls := 0
			w.extract = func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error) {
				calls++
				if test.name == "panic" {
					panic("secret document contents")
				}
				return nil, test.err
			}
			for attempt := 1; attempt <= test.tries; attempt++ {
				step(t, w)
				job := stored(t, w.db)
				if job.Attempts != attempt || strings.Contains(job.LastError, "secret") || job.LastError == "" || passageCount(t, w.db) != 0 {
					t.Fatalf("unsafe failure state: %+v", job)
				}
				if attempt < test.tries && job.Status != Pending {
					t.Fatal("transient failure was not scheduled for retry")
				}
			}
			if stored(t, w.db).Status != test.status || calls != test.tries {
				t.Fatal("incorrect terminal failure policy")
			}
			if processed, err := w.step(ctx); processed || err != nil {
				t.Fatal("terminal work was retried", err)
			}
		})
	}
}

func TestWorkerLateResultsCannotReplaceCurrentPassages(t *testing.T) {
	w := testWorker(t)
	old := claim(t, w.db)
	if _, err := Recover(ctx, w.db); err != nil {
		t.Fatal(err)
	}
	step(t, w)
	if err := w.publish(ctx, old, Result{Status: Ready}, []extraction.Passage{{Section: 1, Text: "stale private text"}}); !errors.Is(err, ErrStale) {
		t.Fatal("abandoned attempt overwrote completed work", err)
	}
	var text string
	if err := w.db.QueryRow("SELECT text FROM document_passages").Scan(&text); err != nil || text != "synthetic indexed text" {
		t.Fatal("stale completion changed current passages", err)
	}
	if _, err := w.db.Exec("UPDATE documents SET sha256 = ?", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	enqueue(t, w.db)
	if passageCount(t, w.db) != 0 {
		t.Fatal("replacement retained stale passages")
	}
	for _, change := range []string{"replace", "delete"} {
		t.Run(change, func(t *testing.T) {
			w := testWorker(t)
			started, release := make(chan struct{}), make(chan struct{})
			w.extract = func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error) {
				close(started)
				<-release
				return []extraction.Passage{{Section: 1, Text: "late text"}}, nil
			}
			done := make(chan error, 1)
			go func() { _, err := w.step(ctx); done <- err }()
			receive(t, started)
			if change == "replace" {
				if _, err := w.db.Exec("UPDATE documents SET sha256 = ?", strings.Repeat("c", 64)); err != nil {
					t.Fatal(err)
				}
				enqueue(t, w.db)
			} else if _, err := w.db.Exec("DELETE FROM documents"); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := receive(t, done); err != nil || passageCount(t, w.db) != 0 {
				t.Fatal("deleted/replaced document accepted stale text", err)
			}
		})
	}
}

type eventLog chan string

func (events eventLog) Write(data []byte) (int, error) {
	select {
	case events <- string(data):
	default:
	}
	return len(data), nil
}

func TestWorkerRetriesAtomicPublicationAfterDatabaseFailure(t *testing.T) {
	w := testWorker(t)
	events := make(eventLog, 1)
	w.log = slog.New(slog.NewTextHandler(events, nil))
	calls := 0
	w.extract = func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error) {
		calls++
		return []extraction.Passage{{Section: 1, Text: "first"}, {Section: 2, Text: "second"}}, nil
	}
	if _, err := w.db.Exec(`CREATE TRIGGER fail_second_passage BEFORE INSERT ON document_passages
        WHEN NEW.section = 2 BEGIN SELECT RAISE(FAIL,'synthetic disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := w.step(workCtx); done <- err }()
	receive(t, (<-chan string)(events))
	if stored(t, w.db).Status != Processing || passageCount(t, w.db) != 0 {
		t.Fatal("database failure published partial text or ready status")
	}
	if _, err := w.db.Exec("DROP TRIGGER fail_second_passage"); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, done); err != nil || stored(t, w.db).Status != Ready || passageCount(t, w.db) != 2 || calls != 1 {
		t.Fatal("publication did not recover without re-extraction", err)
	}
}

func TestWorkerShutdownLeavesRecoverableWorkAndDatabaseAvailable(t *testing.T) {
	w := testWorker(t)
	started := make(chan struct{})
	w.extract = func(workCtx context.Context, _ *storage.Store, _ extraction.Source) ([]extraction.Passage, error) {
		close(started)
		<-workCtx.Done()
		return nil, workCtx.Err()
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(workCtx) }()
	receive(t, started)
	if err := w.Run(workCtx); !errors.Is(err, errWorkerRunning) {
		t.Fatal("same worker ran concurrently", err)
	}
	queryCtx, cancelQuery := context.WithTimeout(ctx, time.Second)
	defer cancelQuery()
	if _, err := w.db.ExecContext(queryCtx, "UPDATE documents SET name='renamed.txt'"); err != nil {
		t.Fatal("extraction held the database connection", err)
	}
	cancel()
	if err := receive(t, done); err != nil || stored(t, w.db).Status != Processing || passageCount(t, w.db) != 0 {
		t.Fatal("shutdown did not preserve unfinished work", err)
	}
	var sequence int
	var name, path string
	if err := w.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(ctx, filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := Recover(ctx, reopened); err != nil {
		t.Fatal(err)
	}
	w.db = reopened
	w.extract = func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error) {
		return []extraction.Passage{{Section: 1, Text: "resumed"}}, nil
	}
	step(t, w)
	if got := stored(t, reopened); got.Status != Ready || got.Attempts != 2 {
		t.Fatalf("restart lost/repeated the wrong work: %+v", got)
	}
}

func TestRecoveryEnforcesAttemptBudget(t *testing.T) {
	w := testWorker(t)
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		claim(t, w.db)
		if _, err := Recover(ctx, w.db); err != nil {
			t.Fatal(err)
		}
	}
	if got := stored(t, w.db); got.Status != Failed || got.Attempts != MaxAttempts || got.AttemptToken != "" {
		t.Fatalf("interrupted work bypassed retry limit: %+v", got)
	}
	if processed, err := w.step(ctx); processed || err != nil {
		t.Fatal("exhausted work was claimed", err)
	}
}

func TestWorkerShutdownDuringPublicationFailure(t *testing.T) {
	w := testWorker(t)
	events := make(eventLog, 1)
	w.log = slog.New(slog.NewTextHandler(events, nil))
	if _, err := w.db.Exec(`CREATE TRIGGER block_publication BEFORE INSERT ON document_passages
        BEGIN SELECT RAISE(FAIL,'synthetic disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(workCtx) }()
	receive(t, (<-chan string)(events))
	cancel()
	if err := receive(t, done); err != nil || stored(t, w.db).Status != Processing || passageCount(t, w.db) != 0 {
		t.Fatal("shutdown published partial data or lost claimed work", err)
	}
	if _, err := w.db.Exec("DROP TRIGGER block_publication"); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(ctx, w.db); err != nil {
		t.Fatal(err)
	}
	step(t, w)
	if got := stored(t, w.db); got.Status != Ready || got.Attempts != 2 || passageCount(t, w.db) != 1 {
		t.Fatal("publication failure did not recover on restart", got)
	}
}

func TestWorkerRejectsInvalidExtractorOutput(t *testing.T) {
	for _, passages := range [][]extraction.Passage{
		nil, {{Section: 1, Text: " "}}, {{Page: -1, Section: 1, Text: "bad"}},
		{{Section: 2, Text: "missing reference"}}, {{Section: 1, Text: string([]byte{0xff})}},
		{{Section: 1, Text: strings.Repeat("x", extraction.MaxPassageBytes+1)}},
	} {
		w := testWorker(t)
		w.extract = func(context.Context, *storage.Store, extraction.Source) ([]extraction.Passage, error) {
			return passages, nil
		}
		step(t, w)
		if stored(t, w.db).Status != Failed || passageCount(t, w.db) != 0 {
			t.Fatal("invalid output was persisted")
		}
	}
}
