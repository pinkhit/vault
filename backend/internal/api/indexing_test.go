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
	"github.com/pinkhit/vault/backend/internal/testutil"
)

func TestIndexingStatusRequiresCurrentDocumentReadPermission(t *testing.T) {
	for name, data := range map[string][]byte{
		"private-index.txt":  []byte("fictional private document"),
		"private-index.docx": testutil.DOCX(t, `<w:p><w:r><w:t>fictional private document</w:t></w:r></w:p>`),
	} {
		t.Run(name, func(t *testing.T) { checkIndexingReadPermission(t, name, data) })
	}
}

func checkIndexingReadPermission(t *testing.T, name string, data []byte) {
	t.Helper()
	a, _ := newApp(t, t.TempDir(), 2048)
	owner := createMember(t, a, "index-owner")
	ownerIdentity, _ := signIn(t, a, owner.Username, memberPassword, nil)
	reader := createMember(t, a, "index-reader")
	readerIdentity, _ := signIn(t, a, reader.Username, memberPassword, nil)
	document := uploadBytesAs(t, a, ownerIdentity, name, data)
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
			t.Fatal("uploaded document was not indexed")
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
	if got := searchPage(t, a, ownerIdentity, `"fictional private document"`, 20, 0); len(got.Results) != 1 || got.Results[0].Match != "content" || got.Results[0].Section != 1 {
		t.Fatal("worker did not publish searchable content with section references")
	}
	grant := "/api/documents/" + document.ID + "/permissions/" + reader.ID
	requireStatus(t, jsonRequest(t, handler, "PUT", grant, `{"level":"read"}`, ownerIdentity), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", readerIdentity), 200)
	if got := searchPage(t, a, readerIdentity, `"fictional private document"`, 20, 0); len(got.Results) != 1 {
		t.Fatal("read grant did not permit content search")
	}
	requireStatus(t, jsonRequest(t, handler, "DELETE", grant, "", ownerIdentity), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", readerIdentity), 404)
	if got := searchPage(t, a, readerIdentity, `"fictional private document"`, 20, 0); len(got.Results) != 0 {
		t.Fatal("revoked indexing grant leaked search content")
	}
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
			requireStatus(t, send(t, a.Handler(""), "POST", "/api/documents", body, contentType, ""), 500)
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

func TestDocumentAndSearchStatusProjectionUsesCurrentSource(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	document := uploadAs(t, a, fixtureIdentity(), "status-projection.txt")
	if document.IndexingStatus != indexing.Pending {
		t.Fatal("upload did not expose its queued state")
	}
	for _, status := range []indexing.Status{indexing.Processing, indexing.Ready, indexing.Unsupported} {
		token := ""
		if status == indexing.Processing {
			token = strings.Repeat("a", 64)
		}
		if _, err := a.Documents.DB.Exec("UPDATE document_index_jobs SET status=?,attempts=1,attempt_token=? WHERE document_id=?", status, token, document.ID); err != nil {
			t.Fatal(err)
		}
		listed, err := a.Documents.List(context.Background(), fixtureActor, 50, 0)
		if err != nil || len(listed) != 1 || listed[0].IndexingStatus != status {
			t.Fatal("listing omitted current indexing state", err)
		}
		results := searchPage(t, a, fixtureIdentity(), "status-projection", 20, 0)
		if len(results.Results) != 1 || results.Results[0].Document.IndexingStatus != status {
			t.Fatal("search omitted current indexing state")
		}
	}
	if _, err := a.Documents.DB.Exec("UPDATE documents SET sha256=? WHERE id=?", strings.Repeat("0", 64), document.ID); err != nil {
		t.Fatal(err)
	}
	got, err := a.Documents.Get(context.Background(), fixtureActor, document.ID)
	if err != nil || got.IndexingStatus != "" {
		t.Fatal("obsolete job state was attached to current metadata", err)
	}
	results := searchPage(t, a, fixtureIdentity(), "status-projection", 20, 0)
	if len(results.Results) != 1 || results.Results[0].Document.IndexingStatus != "" {
		t.Fatal("search attached obsolete indexing state to the current source")
	}
}
