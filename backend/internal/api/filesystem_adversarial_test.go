package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticSymlinksCannotExposePrivateFiles(t *testing.T) {
	for _, route := range []string{"/", "/assets/leak.txt"} {
		t.Run(route, func(t *testing.T) {
			a, _ := newApp(t, t.TempDir(), 1024)
			web := t.TempDir()
			if err := os.Mkdir(filepath.Join(web, "assets"), 0700); err != nil {
				t.Fatal(err)
			}
			private := filepath.Join(t.TempDir(), "private.txt")
			sentinel := []byte("fictional private file outside the web root")
			if err := os.WriteFile(private, sentinel, 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(web, "index.html")
			if route != "/" {
				link = filepath.Join(web, "assets", "leak.txt")
			}
			if err := os.Symlink(private, link); err != nil {
				t.Fatal(err)
			}
			response := asUser(t, a.Handler(web), "GET", route, nil, "", nil, nil)
			if response.Code == 200 || strings.Contains(response.Body.String(), string(sentinel)) {
				t.Fatal("unauthenticated static route followed an out-of-root symlink")
			}
		})
	}
}

func TestTraversalAndSQLIDsCannotReachPrivateBytes(t *testing.T) {
	dataDir := t.TempDir()
	a, _ := newApp(t, dataDir, 1024)
	owner := createMember(t, a, "path-owner")
	actor, _ := signIn(t, a, owner.Username, memberPassword, nil)
	createMember(t, a, "path-attacker")
	attacker, _ := signIn(t, a, "path-attacker", memberPassword, nil)
	private := uploadAs(t, a, actor, "private-path.txt")
	web := t.TempDir()
	if err := os.Mkdir(filepath.Join(web, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("public page"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler(web))
	defer server.Close()
	paths := []string{
		"/api/documents/" + private.ID + "/download",
		"/api/documents/" + url.PathEscape("' OR 1=1 --") + "/download",
		"/api/documents/..%2f..%2fmetadata.sqlite/download",
		"/api/documents/%252e%252e%252fmetadata.sqlite/download",
		"/api/documents/..%5c..%5cmetadata.sqlite/download",
		"/assets/../../" + strings.TrimPrefix(filepath.ToSlash(dataDir), "/") + "/objects/" + private.ID,
		"/assets/%2e%2e/%2e%2e/data/metadata.sqlite",
		"/data/objects/" + private.ID,
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest("GET", server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.AddCookie(attacker.cookie)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode < 400 || strings.Contains(string(body), "fictional private document") || strings.HasPrefix(string(body), "SQLite format 3") {
				t.Fatalf("path attack reached data or an unexpected success: %d", response.StatusCode)
			}
		})
	}
}

func TestSymlinkedObjectRejectedEvenWithMatchingExpectedHash(t *testing.T) {
	directory := t.TempDir()
	a, _ := newApp(t, directory, 1024)
	document := uploadAs(t, a, fixtureIdentity(), "object-link.txt")
	sentinel := []byte("fictional external file")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(directory, "objects", document.ID)
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, object); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(sentinel)
	// Deliberately align expected metadata so a checksum failure cannot hide
	// a broken no-follow boundary. This is test setup, not a claimed DB-write exploit.
	if _, err := a.Documents.DB.Exec("UPDATE documents SET size=?, sha256=? WHERE id=?", len(sentinel), hex.EncodeToString(hash[:]), document.ID); err != nil {
		t.Fatal(err)
	}
	response := jsonRequest(t, a.Handler(""), "GET", "/api/documents/"+document.ID+"/download", "", fixtureIdentity())
	requireStatus(t, response, 409)
	if strings.Contains(response.Body.String(), string(sentinel)) {
		t.Fatal("symlink disclosed out-of-root bytes")
	}
}
