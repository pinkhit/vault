package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/indexing"
)

func TestIndexingStatusRequiresCurrentDocumentReadPermission(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	owner := createMember(t, a, "index-owner")
	ownerIdentity, _ := signIn(t, a, owner.Username, memberPassword, nil)
	reader := createMember(t, a, "index-reader")
	readerIdentity, _ := signIn(t, a, reader.Username, memberPassword, nil)
	document := uploadAs(t, a, ownerIdentity, "private-index.txt")
	path := "/api/documents/" + document.ID + "/indexing"
	handler := a.Handler("")
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", nil), 401)
	for _, method := range []string{"GET", "HEAD"} {
		denied := jsonRequest(t, handler, method, path, "", readerIdentity)
		requireStatus(t, denied, 404)
		if strings.Contains(denied.Body.String(), "pending") || strings.Contains(denied.Body.String(), "attempts") {
			t.Fatal("restricted indexing metadata leaked")
		}
	}
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", fixtureIdentity()), 200)
	response := jsonRequest(t, handler, "GET", path, "", ownerIdentity)
	requireStatus(t, response, 200)
	var progress documents.IndexingProgress
	if err := json.Unmarshal(response.Body.Bytes(), &progress); err != nil || progress.Status != indexing.Pending || progress.Attempts != 0 {
		t.Fatal("upload was not transactionally queued", err)
	}
	workCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- indexing.NewWorker(a.Documents.DB, a.Documents.Store, a.Log).Run(workCtx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for progress.Status != indexing.Ready {
		select {
		case <-deadline.C:
			t.Fatal("uploaded TXT was not indexed")
		case <-ticker.C:
			response = jsonRequest(t, handler, "GET", path, "", ownerIdentity)
			requireStatus(t, response, 200)
			if err := json.Unmarshal(response.Body.Bytes(), &progress); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, private := range []string{"fictional private document", "attempt_token", "source_sha256", document.Name} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("status endpoint exposed text or internal metadata")
		}
	}
	grant := "/api/documents/" + document.ID + "/permissions/" + reader.ID
	requireStatus(t, jsonRequest(t, handler, "PUT", grant, `{"level":"read"}`, ownerIdentity), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", readerIdentity), 200)
	requireStatus(t, jsonRequest(t, handler, "DELETE", grant, "", ownerIdentity), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", readerIdentity), 404)
	reader.IsAdmin = true
	if _, err := a.Documents.Indexing(context.Background(), reader, document.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cached administrator flag bypassed status SQL authorization", err)
	}
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/documents/"+strings.Repeat("0", 32)+"/indexing", "", ownerIdentity), 404)
	if _, err := a.Documents.DB.Exec("UPDATE documents SET sha256 = ? WHERE id = ?", strings.Repeat("0", 64), document.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", ownerIdentity), 404)
}

func TestUploadJobAndAuditAreOneTransaction(t *testing.T) {
	for _, table := range []string{"document_index_jobs", "audit_events"} {
		t.Run(table, func(t *testing.T) {
			a, _ := newApp(t, t.TempDir(), 1024)
			if _, err := a.Documents.DB.Exec("CREATE TRIGGER fail_index_upload BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(FAIL,'synthetic failure'); END"); err != nil {
				t.Fatal(err)
			}
			body, contentType := multipartBody(t, "rollback.txt", []byte("fictional original"))
			requireStatus(t, send(a.Handler(""), "POST", "/api/documents", body, contentType, ""), 500)
			for _, query := range []string{
				"SELECT count(*) FROM documents", "SELECT count(*) FROM document_index_jobs",
				"SELECT count(*) FROM audit_events WHERE operation = 'document.upload'",
			} {
				var count int
				if err := a.Documents.DB.QueryRow(query).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed upload published metadata/job/audit", count, err)
				}
			}
		})
	}
}
