package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/analytics"
	"github.com/pinkhit/vault/backend/internal/auth"

	"github.com/pinkhit/vault/backend/internal/database"
	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/indexing"
	"github.com/pinkhit/vault/backend/internal/storage"
)

const fixtureToken = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
const fixtureCSRF = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

var fixtureActor = auth.User{ID: "cccccccccccccccccccccccccccccccc", Username: "fixture-admin", IsAdmin: true, Enabled: true}
var fixtureHash = sync.OnceValues(func() (string, error) { return auth.HashPassword("fictional-admin-password") })

func seedFixtureAccount(t *testing.T, db *sql.DB) {
	t.Helper()
	hash, err := fixtureHash()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO users(id, username, password_hash, is_admin, enabled, created_at) VALUES (?, ?, ?, 1, 1, ?)`, fixtureActor.ID, fixtureActor.Username, hash, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(fixtureToken))
	_, err = db.Exec(`INSERT OR IGNORE INTO sessions(token_hash, user_id, csrf_token, created_at, expires_at, last_seen) VALUES (?, ?, ?, ?, ?, ?)`, hex.EncodeToString(digest[:]), fixtureActor.ID, fixtureCSRF, time.Now().Unix(), time.Now().Add(8*time.Hour).Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
}

func newApp(t *testing.T, dir string, limit int64) (*API, func()) {
	t.Helper()
	store, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	seedFixtureAccount(t, db)
	authentication, err := auth.New(db, 8*time.Hour, 30*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	a := &API{Auth: authentication, Documents: &documents.Service{DB: db, Store: store, MaxBytes: limit, Log: log}, Storage: analytics.New(db, store.VolumeCapacity, false), Log: log}
	var once sync.Once
	closeApp := func() { once.Do(func() { db.Close(); store.Close() }) }
	t.Cleanup(closeApp)
	if err = a.Documents.Recover(context.Background()); err != nil {
		closeApp()
		t.Fatal(err)
	}
	return a, closeApp
}

func multipartBody(t *testing.T, name string, data []byte) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), writer.FormDataContentType()
}

func send(handler http.Handler, method, path string, body []byte, contentType, checksum string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "familyvault_session", Value: fixtureToken})
	req.Header.Set("X-CSRF-Token", fixtureCSRF)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if checksum != "" {
		req.Header.Set("X-Checksum-SHA256", checksum)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func uploadBytesAs(t *testing.T, a *API, actor *identity, name string, data []byte) documents.Document {
	t.Helper()
	body, kind := multipartBody(t, name, data)
	return documentFrom(t, asUser(t, a.Handler(""), "POST", "/api/documents", body, kind, actor, nil))
}

func documentFrom(t *testing.T, w *httptest.ResponseRecorder) documents.Document {
	t.Helper()
	if w.Code != 201 {
		t.Fatalf("upload status %d: %s", w.Code, w.Body.String())
	}
	var d documents.Document
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func count(t *testing.T, a *API) int {
	t.Helper()
	var n int
	if err := a.Documents.DB.QueryRow("SELECT count(*) FROM documents").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Uses a real HTTP listener, SQLite and filesystem, then reopens the same data directory.
func TestUploadDownloadIntegrationAndRestart(t *testing.T) {
	dir := t.TempDir()
	a, closeApp := newApp(t, dir, 1<<20)
	server := httptest.NewServer(a.Handler(""))
	data, err := os.ReadFile("../../../tests/fixtures/fictional-invoice.txt")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	checksum := hex.EncodeToString(digest[:])
	body, contentType := multipartBody(t, "fictional-invoice.txt", data)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/documents", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Checksum-SHA256", checksum)
	req.AddCookie(&http.Cookie{Name: "familyvault_session", Value: fixtureToken})
	req.Header.Set("X-CSRF-Token", fixtureCSRF)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var d documents.Document
	err = json.NewDecoder(response.Body).Decode(&d)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("upload: status=%d error=%v", response.StatusCode, err)
	}
	if d.SHA256 != checksum || d.Size != int64(len(data)) || d.Name != "fictional-invoice.txt" || d.CreatedAt.IsZero() {
		t.Fatalf("invalid metadata: %+v", d)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "objects", d.ID))
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("original bytes not persisted: %v", err)
	}
	got, err := a.Documents.Get(context.Background(), fixtureActor, d.ID)
	if err != nil || got != d {
		t.Fatalf("database metadata mismatch: %+v %v", got, err)
	}
	server.Close()
	closeApp()
	a, _ = newApp(t, dir, 1<<20)
	server = httptest.NewServer(a.Handler(""))
	defer server.Close()
	req, err = http.NewRequest("GET", server.URL+"/api/documents/"+d.ID+"/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "familyvault_session", Value: fixtureToken})
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Equal(downloaded, data) {
		t.Fatalf("download after restart: status=%d error=%v", response.StatusCode, err)
	}
	if response.Header.Get("X-Checksum-SHA256") != checksum {
		t.Fatal("checksum header mismatch")
	}
	if !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatal("unsafe download disposition")
	}
	if count(t, a) != 1 {
		t.Fatal("metadata did not persist")
	}
}

func TestRejectedUploadsLeaveNoCompletedDocuments(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		data     []byte
		checksum string
		mutate   func([]byte) []byte
		status   int
	}{
		{name: "empty", filename: "empty.txt", status: 400},
		{name: "too large", filename: "large.txt", data: bytes.Repeat([]byte("x"), 1025), status: 413},
		{name: "traversal", filename: "../invoice.txt", data: []byte("fictional"), status: 400},
		{name: "windows traversal", filename: `..\invoice.txt`, data: []byte("fictional"), status: 400},
		{name: "long filename", filename: strings.Repeat("a", 256), data: []byte("fictional"), status: 400},
		{name: "checksum mismatch", filename: "invoice.txt", data: []byte("fictional"), checksum: strings.Repeat("0", 64), status: 400},
		{name: "invalid checksum", filename: "invoice.txt", data: []byte("fictional"), checksum: "abc", status: 400},
		{name: "interrupted", filename: "invoice.txt", data: []byte("fictional"), mutate: func(b []byte) []byte { return b[:len(b)-20] }, status: 400},
		{name: "interrupted after streaming", filename: "invoice.txt", data: bytes.Repeat([]byte("x"), 800), mutate: func(b []byte) []byte { return b[:len(b)-20] }, status: 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a, _ := newApp(t, dir, 1024)
			body, ct := multipartBody(t, tc.filename, tc.data)
			if tc.mutate != nil {
				body = tc.mutate(body)
			}
			w := send(a.Handler(""), "POST", "/api/documents", body, ct, tc.checksum)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if count(t, a) != 0 {
				t.Fatal("invalid document was published")
			}
			for _, sub := range []string{"objects", "staging"} {
				entries, err := os.ReadDir(filepath.Join(dir, sub))
				if err != nil || len(entries) != 0 {
					t.Fatalf("%s not cleaned: %v %v", sub, entries, err)
				}
			}
		})
	}
}

func TestMalformedMultipartAndRequestLimits(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	h := a.Handler("")
	cases := []struct {
		name   string
		body   []byte
		ct     string
		status int
	}{
		{"missing content type", []byte("x"), "", 400},
		{"missing boundary", []byte("x"), "multipart/form-data", 400},
		{"broken body", []byte("garbage"), "multipart/form-data; boundary=abc", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := send(h, "POST", "/api/documents", tc.body, tc.ct, "")
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
	for _, kind := range []string{"extra file", "extra field", "wrong field", "transfer encoding", "oversized overhead"} {
		t.Run(kind, func(t *testing.T) {
			var b bytes.Buffer
			writer := multipart.NewWriter(&b)
			name := "file"
			if kind == "wrong field" {
				name = "other"
			}
			var part io.Writer
			var err error
			if kind == "transfer encoding" {
				header := make(textproto.MIMEHeader)
				header.Set("Content-Disposition", `form-data; name="file"; filename="invoice.txt"`)
				header.Set("Content-Transfer-Encoding", "quoted-printable")
				part, err = writer.CreatePart(header)
			} else {
				part, err = writer.CreateFormFile(name, "invoice.txt")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write([]byte("fictional"))
			switch kind {
			case "extra file":
				part, _ = writer.CreateFormFile("file", "second.txt")
				_, _ = part.Write([]byte("second"))
			case "extra field":
				_ = writer.WriteField("comment", "extra")
			}
			_ = writer.Close()
			if kind == "oversized overhead" {
				b.Write(bytes.Repeat([]byte("x"), 70<<10))
			}
			w := send(h, "POST", "/api/documents", b.Bytes(), writer.FormDataContentType(), "")
			want := 400
			if kind == "oversized overhead" {
				want = 413
			}
			if w.Code != want {
				t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
	if count(t, a) != 0 {
		t.Fatal("malformed upload published")
	}
}

func TestDuplicateNamesAndPagination(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	h := a.Handler("")
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		body, ct := multipartBody(t, "invoice.txt", []byte(fmt.Sprintf("fictional-%d", i)))
		d := documentFrom(t, send(h, "POST", "/api/documents", body, ct, ""))
		if ids[d.ID] {
			t.Fatal("duplicate ID")
		}
		ids[d.ID] = true
	}
	w := send(h, "GET", "/api/documents?limit=2", nil, "", "")
	var page struct {
		Documents []documents.Document `json:"documents"`
		HasMore   bool                 `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(page.Documents) != 2 || !page.HasMore {
		t.Fatalf("bad page: %s", w.Body.String())
	}
	if page.Documents[0].CreatedAt.Before(page.Documents[1].CreatedAt) {
		t.Fatal("incorrect ordering")
	}
	w = send(h, "GET", "/api/documents?limit=2&offset=2", nil, "", "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Documents) != 1 || page.HasMore {
		t.Fatalf("bad second page: %s", w.Body.String())
	}
}

func TestConcurrentUploads(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	h := a.Handler("")
	body, ct := multipartBody(t, "same.txt", []byte("fictional"))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := send(h, "POST", "/api/documents", body, ct, "")
			if w.Code != 201 {
				t.Errorf("status %d: %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	if count(t, a) != 6 {
		t.Fatal("lost uploads")
	}
}

func TestDatabaseFailureAndOrphanRecovery(t *testing.T) {
	dir := t.TempDir()
	a, _ := newApp(t, dir, 1024)
	_, err := a.Documents.DB.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON documents BEGIN SELECT RAISE(FAIL, 'simulated disk failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "invoice.txt", []byte("fictional"))
	w := send(a.Handler(""), "POST", "/api/documents", body, ct, "")
	if w.Code != 500 || count(t, a) != 0 {
		t.Fatalf("failed insert became visible: %d %s", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(dir, "objects"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected invisible orphan: %v %v", entries, err)
	}
	if err = a.Documents.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(dir, "objects"))
	if err != nil || len(entries) != 0 {
		t.Fatal("orphan not reconciled")
	}
}

func TestDownloadRejectsCorruptionAndSymlink(t *testing.T) {
	for _, kind := range []string{"corrupt", "missing", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			a, _ := newApp(t, dir, 1024)
			h := a.Handler("")
			body, ct := multipartBody(t, "invoice.txt", []byte("fictional"))
			d := documentFrom(t, send(h, "POST", "/api/documents", body, ct, ""))
			path := filepath.Join(dir, "objects", d.ID)
			if kind == "corrupt" {
				if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					target := filepath.Join(t.TempDir(), "outside.txt")
					if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			w := send(h, "GET", "/api/documents/"+d.ID+"/download", nil, "", "")
			if w.Code != 409 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if err := a.Documents.Recover(context.Background()); err == nil {
				t.Fatal("recovery accepted missing/corrupt bytes")
			}
		})
	}
}

func TestEndpointsAndLocalBoundary(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	h := a.Handler("")
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/health", 200},
		{"/api/documents", 200},
		{"/api/documents?limit=0", 400},
		{"/api/documents?limit=101", 400},
		{"/api/documents?offset=-1", 400},
		{"/api/documents?limit=no", 400},
		{"/api/documents?offset=no", 400},
		{"/api/documents/bad/download", 400},
		{"/api/documents/" + strings.Repeat("0", 32) + "/download", 404},
		{"/api/missing", 404},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := send(h, "GET", tc.path, nil, "", "")
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
			if w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
		})
	}
	for _, tc := range []struct {
		name      string
		host      string
		origin    string
		fetchSite string
		status    int
	}{
		{"dns rebinding", "attacker.example", "", "", 403},
		{"foreign origin", "localhost", "https://evil.example", "", 403},
		{"origin suffix", "localhost", "http://localhost.attacker.example", "", 403},
		{"origin path", "localhost", "http://localhost/api", "", 403},
		{"null origin", "localhost", "null", "", 403},
		{"cross site", "localhost", "", "cross-site", 403},
		{"local origin", "localhost", "http://localhost", "same-origin", 200},
		{"wrong port", "localhost:8080", "http://localhost:9999", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost/api/health", nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := send(h, "DELETE", "/api/documents", nil, "", "")
	if w.Code != 405 {
		t.Fatalf("method status %d", w.Code)
	}
}

func TestProcessTerminationRecovery(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "FAMILYVAULT_CRASH_TEST_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, output)
	}
	// The helper exited without closing SQLite/files or releasing the lock itself.
	a, _ := newApp(t, dir, 1024)
	if count(t, a) != 1 {
		t.Fatal("completed document lost after termination")
	}
	var state string
	if err := a.Documents.DB.QueryRow("SELECT status FROM document_index_jobs").Scan(&state); err != nil || state != "processing" {
		t.Fatal("helper did not persist an interrupted indexing claim", state, err)
	}
	if recovered, err := indexing.Recover(context.Background(), a.Documents.DB); err != nil || recovered != 1 {
		t.Fatal("crashed claim not recovered", recovered, err)
	}
	workCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- indexing.NewWorker(a.Documents.DB, a.Documents.Store, a.Log).Run(workCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var attempts int
		if err := a.Documents.DB.QueryRow("SELECT status,attempts FROM document_index_jobs").Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if state == "ready" {
			if attempts != 2 || len(searchPage(t, a, fixtureIdentity(), `"fictional completed document"`, 20, 0).Results) != 1 {
				t.Fatal("crash recovery lost indexed content/attempt count")
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("crash recovery did not finish")
		case <-ticker.C:
		}
	}
	for _, sub := range []struct {
		dir  string
		want int
	}{{"objects", 1}, {"staging", 0}} {
		entries, err := os.ReadDir(filepath.Join(dir, sub.dir))
		if err != nil || len(entries) != sub.want {
			t.Fatalf("recovery: %s %v %v", sub.dir, entries, err)
		}
	}
}

func TestCrashHelper(t *testing.T) {
	dir := os.Getenv("FAMILYVAULT_CRASH_TEST_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	a, _ := newApp(t, dir, 1024)
	if _, err := a.Documents.Upload(context.Background(), fixtureActor, "completed.txt", strings.NewReader("fictional completed document"), ""); err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("a", 32)
	if _, err := a.Documents.Store.Stage(orphan, strings.NewReader("uncommitted orphan"), 1024); err != nil {
		t.Fatal(err)
	}
	if err := a.Documents.Store.Promote(orphan); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Documents.Store.Stage(strings.Repeat("b", 32), strings.NewReader("interrupted upload"), 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := indexing.Claim(context.Background(), a.Documents.DB); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
