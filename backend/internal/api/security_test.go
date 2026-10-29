package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/documents"
)

const memberPassword = "fictional-member-password"

type identity struct {
	cookie *http.Cookie
	csrf   string
}

func asUser(t *testing.T, handler http.Handler, method, path string, body []byte, contentType string, actor *identity, change func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Origin", "http://localhost")
	if actor != nil {
		req.AddCookie(actor.cookie)
		req.Header.Set("X-CSRF-Token", actor.csrf)
	}
	if change != nil {
		change(req)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func jsonRequest(t *testing.T, handler http.Handler, method, path, body string, actor *identity) *httptest.ResponseRecorder {
	return asUser(t, handler, method, path, []byte(body), "application/json", actor, nil)
}

func fixtureIdentity() *identity {
	return &identity{cookie: &http.Cookie{Name: "familyvault_session", Value: fixtureToken}, csrf: fixtureCSRF}
}

func createMember(t *testing.T, a *API, username string) auth.User {
	t.Helper()
	user, err := a.Auth.CreateUser(context.Background(), fixtureActor, username, memberPassword, false)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func signIn(t *testing.T, a *API, username, password string, old *identity) (*identity, auth.Session) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	response := asUser(t, a.Handler(""), "POST", "/api/auth/login", body, "application/json", old, nil)
	if response.Code != 200 {
		t.Fatalf("login status %d: %s", response.Code, response.Body.String())
	}
	var session auth.Session
	if err = json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies: %v", cookies)
	}
	return &identity{cookie: cookies[0], csrf: session.CSRFToken}, session
}

func uploadAs(t *testing.T, a *API, actor *identity, name string) documents.Document {
	t.Helper()
	return uploadBytesAs(t, a, actor, name, []byte("fictional private document"))
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if response.Code != expected {
		t.Fatalf("status=%d want=%d: %s", response.Code, expected, response.Body.String())
	}
}

func TestUnauthenticatedEndpointsFailClosed(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	document := uploadAs(t, a, fixtureIdentity(), "private.txt")
	paths := []struct{ method, path string }{
		{"GET", "/api/documents"},
		{"POST", "/api/documents"},
		{"GET", "/api/documents/" + document.ID + "/download"},
		{"HEAD", "/api/documents/" + document.ID + "/download"},
		{"PATCH", "/api/documents/" + document.ID},
		{"GET", "/api/documents/" + document.ID + "/permissions"},
		{"PUT", "/api/documents/" + document.ID + "/permissions/other"},
		{"DELETE", "/api/documents/" + document.ID + "/permissions/other"},
		{"GET", "/api/admin/users"},
		{"POST", "/api/admin/users"},
		{"PATCH", "/api/admin/users/other"},
		{"GET", "/api/admin/audit"},
		{"GET", "/api/auth/session"},
		{"POST", "/api/auth/logout"},
		{"POST", "/api/auth/password"},
		{"POST", "/api/auth/revoke-sessions"},
	}
	for _, tc := range paths {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			for _, invalid := range []string{"", "forged", strings.Repeat("a", 64)} {
				var actor *identity
				if invalid != "" {
					actor = &identity{cookie: &http.Cookie{Name: "familyvault_session", Value: invalid}, csrf: fixtureCSRF}
				}
				response := asUser(t, a.Handler(""), tc.method, tc.path, nil, "", actor, nil)
				requireStatus(t, response, 401)
				if strings.Contains(response.Body.String(), "private.txt") || response.Header().Get("Content-Disposition") != "" {
					t.Fatal("private metadata leaked")
				}
			}
		})
	}
	requireStatus(t, asUser(t, a.Handler(""), "GET", "/api/health", nil, "", nil, nil), 200)
}

func TestCrossUserAccessAndPermissionLevels(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	aliceUser := createMember(t, a, "alice")
	bobUser := createMember(t, a, "bob")
	carol := createMember(t, a, "carol")
	alice, _ := signIn(t, a, "alice", memberPassword, nil)
	bob, _ := signIn(t, a, "bob", memberPassword, nil)
	document := uploadAs(t, a, alice, "alice-private.txt")
	if document.OwnerID != aliceUser.ID {
		t.Fatal("uploader was not assigned ownership")
	}
	path := "/api/documents/" + document.ID
	handler := a.Handler("")
	for _, method := range []string{"GET", "HEAD"} {
		response := asUser(t, handler, method, path+"/download", nil, "", bob, nil)
		requireStatus(t, response, 404)
		if response.Header().Get("ETag") != "" || response.Header().Get("X-Checksum-SHA256") != "" {
			t.Fatal("checksum metadata leaked")
		}
	}
	requireStatus(t, jsonRequest(t, handler, "PATCH", path, `{"name":"stolen.txt"}`, bob), 404)
	requireStatus(t, jsonRequest(t, handler, "PUT", path+"/permissions/"+bobUser.ID, `{"level":"manage"}`, bob), 404)
	hidden := jsonRequest(t, handler, "GET", "/api/documents?limit=1", "", bob)
	requireStatus(t, hidden, 200)
	if strings.Contains(hidden.Body.String(), document.ID) || strings.Contains(hidden.Body.String(), "alice-private") {
		t.Fatal("listing leaked another user's document")
	}
	if strings.Contains(hidden.Body.String(), `"has_more":true`) {
		t.Fatal("pagination leaked hidden row count")
	}
	// Administrative authority is read from the session/database, not client headers.
	response := asUser(t, handler, "GET", path+"/download", nil, "", bob, func(r *http.Request) { r.Header.Set("X-User-ID", fixtureActor.ID); r.Header.Set("X-Is-Admin", "true") })
	requireStatus(t, response, 404)
	for _, level := range []string{"read", "write", "manage"} {
		requireStatus(t, jsonRequest(t, handler, "PUT", path+"/permissions/"+bobUser.ID, fmt.Sprintf(`{"level":%q}`, level), alice), 204)
		requireStatus(t, jsonRequest(t, handler, "GET", path+"/download", "", bob), 200)
		renameStatus, grantStatus := 404, 404
		if level != "read" {
			renameStatus = 204
		}
		if level == "manage" {
			grantStatus = 204
		}
		requireStatus(t, jsonRequest(t, handler, "PATCH", path, `{"name":"renamed.txt"}`, bob), renameStatus)
		requireStatus(t, jsonRequest(t, handler, "PUT", path+"/permissions/"+carol.ID, `{"level":"read"}`, bob), grantStatus)
		expected := 404
		if level == "manage" {
			expected = 200
		}
		requireStatus(t, jsonRequest(t, handler, "GET", path+"/permissions", "", bob), expected)
	}
	requireStatus(t, jsonRequest(t, handler, "PUT", path+"/permissions/"+aliceUser.ID, `{"level":"read"}`, bob), 400)
	requireStatus(t, jsonRequest(t, handler, "PUT", path+"/permissions/"+bobUser.ID, `{"level":"admin"}`, alice), 400)
	requireStatus(t, jsonRequest(t, handler, "DELETE", path+"/permissions/"+bobUser.ID, "", alice), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", path+"/download", "", bob), 404)
	requireStatus(t, jsonRequest(t, handler, "PATCH", path, `{"name":"revoked.txt"}`, bob), 404)
	requireStatus(t, jsonRequest(t, handler, "GET", path+"/download", "", alice), 200)
	requireStatus(t, jsonRequest(t, handler, "GET", path+"/download", "", fixtureIdentity()), 200)
}

func TestCSRFAndLoginOrigin(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	createMember(t, a, "alice")
	alice, _ := signIn(t, a, "alice", memberPassword, nil)
	second, _ := signIn(t, a, "alice", memberPassword, nil)
	document := uploadAs(t, a, alice, "csrf.txt")
	handler := a.Handler("")
	for _, token := range []string{"", "forged", second.csrf} {
		actor := &identity{cookie: alice.cookie, csrf: token}
		for _, tc := range []struct{ method, path, body string }{
			{"POST", "/api/documents", ""},
			{"POST", "/api/auth/logout", ""},
			{"POST", "/api/auth/revoke-sessions", ""},
			{"POST", "/api/auth/password", `{"current_password":"x","new_password":"x"}`},
			{"PATCH", "/api/documents/" + document.ID, `{"name":"x"}`},
			{"PUT", "/api/documents/" + document.ID + "/permissions/other", `{"level":"read"}`},
		} {
			requireStatus(t, jsonRequest(t, handler, tc.method, tc.path, tc.body, actor), 403)
		}
	}
	for _, origin := range []string{"", "null", "http://localhost:9999", "https://evil.example"} {
		response := asUser(t, handler, "POST", "/api/auth/login", []byte(`{"username":"alice","password":"fictional-member-password"}`), "application/json", nil, func(r *http.Request) { r.Header.Set("Origin", origin) })
		requireStatus(t, response, 403)
	}
	for _, body := range []string{"", `{}`, `{"username":"alice","password":"fictional-member-password","is_admin":true}`, `{} {}`, strings.Repeat("x", 17000)} {
		response := asUser(t, handler, "POST", "/api/auth/login", []byte(body), "application/json", nil, nil)
		if body == "{}" {
			requireStatus(t, response, 401)
		} else {
			requireStatus(t, response, 400)
		}
	}
	requireStatus(t, asUser(t, handler, "POST", "/api/auth/login", []byte("username=alice"), "application/x-www-form-urlencoded", nil, nil), 415)
}

func TestSessionRotationLogoutAndPersistentHashes(t *testing.T) {
	dir := t.TempDir()
	a, closeApp := newApp(t, dir, 1024)
	createMember(t, a, "alice")
	first, _ := signIn(t, a, "alice", memberPassword, nil)
	second, session := signIn(t, a, "alice", memberPassword, first)
	if first.cookie.Value == second.cookie.Value || first.csrf == second.csrf {
		t.Fatal("session did not rotate")
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/documents", "", first), 401)
	if !second.cookie.HttpOnly || second.cookie.SameSite != http.SameSiteStrictMode || second.cookie.Path != "/" || second.cookie.Domain != "" || second.cookie.MaxAge <= 0 {
		t.Fatalf("unsafe cookie: %+v", second.cookie)
	}
	digest := sha256.Sum256([]byte(second.cookie.Value))
	var stored string
	if err := a.Auth.DB.QueryRow("SELECT token_hash FROM sessions WHERE user_id = ?", session.User.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hex.EncodeToString(digest[:]) || stored == second.cookie.Value {
		t.Fatal("raw session token stored in database")
	}
	closeApp()
	a, _ = newApp(t, dir, 1024)
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/documents", "", second), 200)
	response := jsonRequest(t, a.Handler(""), "POST", "/api/auth/logout", "", second)
	requireStatus(t, response, 204)
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/documents", "", second), 401)
	a.Auth.SecureCookies = true
	cookie := a.Auth.Cookie("synthetic")
	if !cookie.Secure || !strings.HasPrefix(cookie.Name, "__Host-") || cookie.Domain != "" || cookie.Path != "/" {
		t.Fatal("secure cookie mode is not host-bound")
	}
}

func TestAbsoluteAndIdleSessionExpiry(t *testing.T) {
	for _, kind := range []string{"absolute", "idle"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := newApp(t, t.TempDir(), 1024)
			createMember(t, a, "alice")
			now := time.Now().UTC().Truncate(time.Second)
			a.Auth.Now = func() time.Time { return now }
			a.Auth.TTL = 2 * time.Minute
			a.Auth.IdleTTL = time.Minute
			actor, _ := signIn(t, a, "alice", memberPassword, nil)
			if kind == "absolute" {
				for i := 0; i < 2; i++ {
					now = now.Add(50 * time.Second)
					requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/auth/session", "", actor), 200)
				}
				now = now.Add(20 * time.Second)
			} else {
				now = now.Add(time.Minute)
			}
			requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/documents", "", actor), 401)
		})
	}
}

func TestPasswordResetDisableAndRevocation(t *testing.T) {
	for _, kind := range []string{"password change", "administrator reset", "disable", "role change", "revoke all"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := newApp(t, t.TempDir(), 1024)
			user := createMember(t, a, "alice")
			first, _ := signIn(t, a, "alice", memberPassword, nil)
			second, _ := signIn(t, a, "alice", memberPassword, nil)
			handler := a.Handler("")
			switch kind {
			case "password change":
				requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/password", `{"current_password":"wrong","new_password":"new-fictional-password"}`, first), 401)
				requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/password", `{"current_password":"fictional-member-password","new_password":"new-fictional-password"}`, first), 204)
			case "administrator reset":
				requireStatus(t, jsonRequest(t, handler, "PATCH", "/api/admin/users/"+user.ID, `{"password":"new-fictional-password"}`, fixtureIdentity()), 200)
			case "disable":
				requireStatus(t, jsonRequest(t, handler, "PATCH", "/api/admin/users/"+user.ID, `{"enabled":false}`, fixtureIdentity()), 200)
			case "role change":
				requireStatus(t, jsonRequest(t, handler, "PATCH", "/api/admin/users/"+user.ID, `{"is_admin":true}`, fixtureIdentity()), 200)
			case "revoke all":
				requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/revoke-sessions", "", first), 204)
			}
			for _, actor := range []*identity{first, second} {
				requireStatus(t, jsonRequest(t, handler, "GET", "/api/documents", "", actor), 401)
			}
			if kind == "password change" || kind == "administrator reset" {
				signIn(t, a, "alice", "new-fictional-password", nil)
			}
			if kind == "disable" {
				requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/login", `{"username":"alice","password":"fictional-member-password"}`, nil), 401)
			}
		})
	}
}

func TestAdministrationAndLastAdministrator(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	user := createMember(t, a, "alice")
	member, _ := signIn(t, a, "alice", memberPassword, nil)
	handler := a.Handler("")
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/admin/users", ""},
		{"GET", "/api/admin/audit", ""},
		{"POST", "/api/admin/users", `{"username":"hacker","password":"fictional-password","is_admin":true}`},
		{"PATCH", "/api/admin/users/" + user.ID, `{"is_admin":true}`},
	} {
		requireStatus(t, jsonRequest(t, handler, tc.method, tc.path, tc.body, member), 403)
	}
	for _, body := range []string{`{"is_admin":false}`, `{"enabled":false}`} {
		requireStatus(t, jsonRequest(t, handler, "PATCH", "/api/admin/users/"+fixtureActor.ID, body, fixtureIdentity()), 409)
	}
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/admin/users", "", fixtureIdentity()), 200)
	requireStatus(t, jsonRequest(t, handler, "POST", "/api/admin/users", `{"username":"alice","password":"fictional-member-password"}`, fixtureIdentity()), 409)
	requireStatus(t, jsonRequest(t, handler, "POST", "/api/admin/users", `{"username":"new-user","password":"short"}`, fixtureIdentity()), 400)
	users := jsonRequest(t, handler, "GET", "/api/admin/users", "", fixtureIdentity())
	if strings.Contains(users.Body.String(), "password_hash") || strings.Contains(users.Body.String(), "argon2") {
		t.Fatal("password hash leaked")
	}
}

func TestLoginThrottlingAndGenericFailures(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	createMember(t, a, "alice")
	now := time.Now().UTC()
	a.Auth.Now = func() time.Time { return now }
	handler := a.Handler("")
	for _, name := range []string{"alice", "unknown-account"} {
		for i := 0; i < 5; i++ {
			response := jsonRequest(t, handler, "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong"}`, name), nil)
			requireStatus(t, response, 401)
			if !strings.Contains(response.Body.String(), "invalid username or password") {
				t.Fatal("account existence disclosed")
			}
		}
		response := jsonRequest(t, handler, "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong"}`, name), nil)
		requireStatus(t, response, 429)
		if response.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry hint")
		}
	}
	now = now.Add(16 * time.Minute)
	signIn(t, a, "alice", memberPassword, nil)
}

func TestAuditFailureRollsBackUploadAndGrants(t *testing.T) {
	dir := t.TempDir()
	a, _ := newApp(t, dir, 1024)
	bob := createMember(t, a, "bob")
	document := uploadAs(t, a, fixtureIdentity(), "existing.txt")
	_, err := a.Documents.DB.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL,'synthetic audit failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "uncommitted.txt", []byte("fictional"))
	requireStatus(t, asUser(t, a.Handler(""), "POST", "/api/documents", body, ct, fixtureIdentity(), nil), 500)
	if count(t, a) != 1 {
		t.Fatal("upload published without audit event")
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "PUT", "/api/documents/"+document.ID+"/permissions/"+bob.ID, `{"level":"read"}`, fixtureIdentity()), 500)
	var grants int
	if err = a.Documents.DB.QueryRow("SELECT count(*) FROM document_permissions").Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("grant committed without audit: %d %v", grants, err)
	}
}

func TestAuditEventsAreRestrictedAndContainNoCredentials(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	createMember(t, a, "alice")
	actor, _ := signIn(t, a, "alice", memberPassword, nil)
	document := uploadAs(t, a, actor, "private-name.txt")
	response := jsonRequest(t, a.Handler(""), "GET", "/api/documents/"+document.ID+"/download", "", actor)
	requireStatus(t, response, 200)
	var requestID string
	if err := a.Documents.DB.QueryRow("SELECT request_id FROM audit_events WHERE operation='document.download' AND object_id=?", document.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if requestID == "" || requestID != response.Header().Get("X-Request-ID") {
		t.Fatal("missing audit correlation")
	}
	response = jsonRequest(t, a.Handler(""), "GET", "/api/admin/audit", "", fixtureIdentity())
	requireStatus(t, response, 200)
	for _, secret := range []string{memberPassword, actor.cookie.Value, actor.csrf, "private-name.txt", "$argon2id$"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("audit contains sensitive data")
		}
	}
}

func TestIPLoginLimitIgnoresForwardingHeaders(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	handler := a.Handler("")
	for i := 0; i < 51; i++ {
		body := []byte(fmt.Sprintf(`{"username":"unknown-%d","password":"wrong"}`, i))
		response := asUser(t, handler, "POST", "/api/auth/login", body, "application/json", nil, func(r *http.Request) { r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i)) })
		status := 401
		if i == 50 {
			status = 429
		}
		requireStatus(t, response, status)
	}
}

func TestListingFiltersBeforePaginationAndAfterRevocation(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	createMember(t, a, "alice")
	bobUser := createMember(t, a, "bob")
	alice, _ := signIn(t, a, "alice", memberPassword, nil)
	bob, _ := signIn(t, a, "bob", memberPassword, nil)
	shared := uploadAs(t, a, alice, "shared.txt")
	hidden := uploadAs(t, a, alice, "hidden.txt")
	for i := 0; i < 2; i++ {
		uploadAs(t, a, bob, fmt.Sprintf("bob-%d.txt", i))
	}
	handler := a.Handler("")
	path := "/api/documents/" + shared.ID + "/permissions/" + bobUser.ID
	requireStatus(t, jsonRequest(t, handler, "PUT", path, `{"level":"read"}`, alice), 204)
	seen := map[string]bool{}
	for offset := 0; offset < 4; offset++ {
		response := jsonRequest(t, handler, "GET", fmt.Sprintf("/api/documents?limit=1&offset=%d", offset), "", bob)
		requireStatus(t, response, 200)
		var page struct {
			Documents []documents.Document `json:"documents"`
			HasMore   bool                 `json:"has_more"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if offset < 3 && len(page.Documents) != 1 {
			t.Fatal("authorized row skipped by pagination")
		}
		if page.HasMore != (offset < 2) {
			t.Fatal("incorrect authorized pagination boundary")
		}
		for _, document := range page.Documents {
			if document.ID == hidden.ID || seen[document.ID] {
				t.Fatal("hidden or duplicate document returned")
			}
			seen[document.ID] = true
		}
	}
	if !seen[shared.ID] || len(seen) != 3 {
		t.Fatal("granted document missing from list")
	}
	requireStatus(t, jsonRequest(t, handler, "DELETE", path, "", alice), 204)
	response := jsonRequest(t, handler, "GET", "/api/documents", "", bob)
	if strings.Contains(response.Body.String(), shared.ID) || strings.Contains(response.Body.String(), hidden.ID) {
		t.Fatal("revoked metadata remains visible")
	}
}
