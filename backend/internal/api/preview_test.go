package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/preview"
	"github.com/pinkhit/vault/backend/internal/testutil"
)

// All formats pass through Service.Preview authorization before parsing.
// Role combinations live in TestDocumentEndpointAuthorizationMatrix.
func TestPreviewHTTPContractAndRevocation(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1<<20)
	owner := createMember(t, a, "preview-owner")
	reader := createMember(t, a, "preview-reader")
	ownerID, _ := signIn(t, a, owner.Username, memberPassword, nil)
	readerID, _ := signIn(t, a, reader.Username, memberPassword, nil)
	doc := uploadBytesAs(t, a, ownerID, "private <img>.txt", []byte("Fictional <script>private-sentinel</script>"))
	path := "/api/documents/" + doc.ID + "/preview"
	handler := a.Handler("")
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", nil), 401)
	denied := jsonRequest(t, handler, "GET", path, "", readerID)
	missing := jsonRequest(t, handler, "GET", "/api/documents/"+strings.Repeat("0", 32)+"/preview", "", readerID)
	requireStatus(t, denied, 404)
	if denied.Body.String() != missing.Body.String() || strings.Contains(denied.Body.String(), "private") {
		t.Fatal("restricted preview disclosed existence/metadata")
	}
	if err := authorization.SetGrant(context.Background(), a.Documents.DB, owner, doc.ID, reader.ID, "read"); err != nil {
		t.Fatal(err)
	}
	response := asUser(t, handler, "GET", path, nil, "", readerID, func(r *http.Request) {
		r.Header.Set("Range", "bytes=0-1")
		r.Header.Set("If-None-Match", `"forged"`)
	})
	requireStatus(t, response, 200)
	if strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), `\u003cscript\u003e`) {
		t.Fatal("preview returned raw active content")
	}
	for header, expected := range map[string]string{
		"Cache-Control": "no-store", "Content-Type": "application/json; charset=utf-8",
		"Content-Disposition": "inline", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
	} {
		if response.Header().Get(header) != expected {
			t.Fatal("missing preview security header", header)
		}
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "object-src 'none'") {
		t.Fatal("preview weakened CSP")
	}
	if err := authorization.SetGrant(context.Background(), a.Documents.DB, owner, doc.ID, reader.ID, ""); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", readerID), 404)
	reader.IsAdmin = true
	if _, err := a.Documents.Preview(context.Background(), reader, doc.ID, 1); err == nil {
		t.Fatal("cached admin claim bypassed SQL authorization")
	}
	for _, suffix := range []string{"?page=0", "?page=201", "?page=x", "?page=1&page=2", "?user_id=forged", "?page=1&user_id=forged", "?page=%zz"} {
		requireStatus(t, jsonRequest(t, handler, "GET", path+suffix, "", ownerID), 400)
	}
	requireStatus(t, asUser(t, handler, "GET", path, nil, "", ownerID, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }), 403)
	requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/revoke-sessions", "", ownerID), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", ownerID), 401)
	// Expiration is checked by the same session middleware on preview requests.
	if _, err := a.Documents.DB.Exec("UPDATE sessions SET created_at=1,expires_at=2,last_seen=1"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, handler, "GET", path, "", readerID), 401)
}

func TestPreviewIntegrityAuditAndOriginalPreservation(t *testing.T) {
	dir := t.TempDir()
	a, _ := newApp(t, dir, 1<<20)
	doc := uploadBytesAs(t, a, fixtureIdentity(), "preview.txt", []byte("Original synthetic bytes"))
	path := "/api/documents/" + doc.ID + "/preview"
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", path, "", fixtureIdentity()), 200)
	var events int
	if err := a.Documents.DB.QueryRow("SELECT count(*) FROM audit_events WHERE operation='document.preview' AND object_id=? AND outcome='allowed' AND request_id<>'' AND details='{}'", doc.ID).Scan(&events); err != nil || events != 1 {
		t.Fatal("preview audit record absent or contains document text", err)
	}
	if _, err := a.Documents.DB.Exec(`CREATE TRIGGER fail_preview_audit BEFORE INSERT ON audit_events WHEN NEW.operation='document.preview'
        BEGIN SELECT RAISE(FAIL,'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := jsonRequest(t, a.Handler(""), "GET", path, "", fixtureIdentity())
	requireStatus(t, response, 500)
	if strings.Contains(response.Body.String(), "Original synthetic") {
		t.Fatal("content released despite audit failure")
	}
	object := filepath.Join(dir, "objects", doc.ID)
	if err := os.WriteFile(object, []byte("Changed synthetic bytes!"), 0600); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", path, "", fixtureIdentity()), 409)
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("Original synthetic bytes"), 0600)
	if err := os.Symlink(outside, object); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", path, "", fixtureIdentity()), 409)
	// A malformed/unsupported preview must leave the downloadable source intact.
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"unsupported.svg", "<svg onload='alert(1)'/>", 415},
		{"malformed.docx", "PK synthetic", 422},
		{"malformed.xlsx", "PK synthetic", 422},
		{"malformed.json", `{"broken":`, 422},
		{"oversized.json", strings.Repeat(" ", preview.MaxTextBytes) + "{}", 413},
	} {
		document := uploadBytesAs(t, a, fixtureIdentity(), test.name, []byte(test.body))
		requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/documents/"+document.ID+"/preview", "", fixtureIdentity()), test.status)
		response := jsonRequest(t, a.Handler(""), "GET", "/api/documents/"+document.ID+"/download", "", fixtureIdentity())
		requireStatus(t, response, 200)
		if response.Body.String() != test.body {
			t.Fatal("preview failure changed original bytes")
		}
	}
}

func TestPDFPreviewAPIUsesInertPageImages(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PDF resource limits require Linux")
	}
	a, _ := newApp(t, t.TempDir(), 1024*1024)
	doc := uploadBytesAs(t, a, fixtureIdentity(), "inline.pdf", testutil.PDF("Synthetic page one", "Synthetic page two"))
	response := jsonRequest(t, a.Handler(""), "GET", "/api/documents/"+doc.ID+"/preview?page=2", "", fixtureIdentity())
	requireStatus(t, response, 200)
	var result preview.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Page != 2 || result.Pages != 2 || !bytes.HasPrefix(result.Image, []byte("\x89PNG\r\n\x1a\n")) || bytes.Contains(response.Body.Bytes(), []byte("%PDF")) {
		t.Fatal("PDF source returned to browser instead of raster page", err)
	}
}

func TestPreviewRevocationDuringRenderingDiscardsThePage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PDF subprocess test requires Linux")
	}
	a, _ := newApp(t, t.TempDir(), 1<<20)
	reader := createMember(t, a, "preview-in-flight")
	actor, _ := signIn(t, a, reader.Username, memberPassword, nil)
	doc := uploadBytesAs(t, a, fixtureIdentity(), "in-flight.pdf", testutil.PDF("Private page"))
	if err := authorization.SetGrant(context.Background(), a.Documents.DB, fixtureActor, doc.ID, reader.ID, "read"); err != nil {
		t.Fatal(err)
	}
	tool, err := exec.LookPath("pdftoppm")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// FIFOs synchronize the real parser boundary without sleeps. Unlike regular
	// file writes, FIFO signaling is compatible with the child's file-size limit.
	fifos := make([]*os.File, 0, 2)
	for _, name := range []string{"started", "resume"} {
		path := filepath.Join(dir, name)
		if err = syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		fifos = append(fifos, file)
	}
	script := "#!/bin/sh\nprintf s > '" + filepath.Join(dir, "started") + "'\nread -r signal < '" + filepath.Join(dir, "resume") + "'\nexec '" + tool + "' \"$@\"\n"
	if err = os.WriteFile(filepath.Join(dir, "pdftoppm"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := exec.LookPath("pdfinfo")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(info, filepath.Join(dir, "pdfinfo")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	request := httptest.NewRequest("GET", "http://localhost/api/documents/"+doc.ID+"/preview", nil)
	request.AddCookie(actor.cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { a.Handler("").ServeHTTP(response, request); close(done) }()
	if err = fifos[0].SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err = fifos[0].Read(signal[:]); err != nil {
		t.Fatal("renderer did not reach the synchronized boundary", err)
	}
	if err = authorization.SetGrant(context.Background(), a.Documents.DB, fixtureActor, doc.ID, reader.ID, ""); err != nil {
		t.Fatal(err)
	}
	fifos[1].Write([]byte("resume\n"))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("renderer did not finish")
	}
	requireStatus(t, response, 404)
	if strings.Contains(response.Body.String(), "image") || strings.Contains(response.Body.String(), "Private page") {
		t.Fatal("page was released after access was revoked during rendering")
	}
}
