package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
)

func TestDocumentEndpointAuthorizationMatrix(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	ownerUser := createMember(t, a, "owner")
	owner, _ := signIn(t, a, "owner", memberPassword, nil)
	target := createMember(t, a, "target")
	document := uploadAs(t, a, owner, "private-matrix.txt")
	other := uploadAs(t, a, fixtureIdentity(), "other-document.txt")
	type subject struct {
		name                string
		user                auth.User
		identity            *identity
		read, write, manage bool
		denied              int
	}
	subjects := []subject{
		{name: "owner", user: ownerUser, identity: owner, read: true, write: true, manage: true, denied: 404},
		{name: "administrator", user: fixtureActor, identity: fixtureIdentity(), read: true, write: true, manage: true, denied: 404},
		{name: "anonymous", denied: 401},
	}
	for _, level := range []string{"read", "write", "manage", "none", "disabled"} {
		user := createMember(t, a, "matrix-"+level)
		actor, _ := signIn(t, a, user.Username, memberPassword, nil)
		if level != "none" {
			grant := level
			if level == "disabled" {
				grant = "manage"
			}
			if err := authorization.SetGrant(context.Background(), a.Documents.DB, ownerUser, document.ID, user.ID, grant); err != nil {
				t.Fatal(err)
			}
		}
		denied := 404
		if level == "disabled" {
			enabled := false
			if _, err := a.Auth.UpdateUser(context.Background(), fixtureActor, user.ID, auth.UserUpdate{Enabled: &enabled}); err != nil {
				t.Fatal(err)
			}
			denied = 401
		}
		subjects = append(subjects, subject{name: level, user: user, identity: actor, read: level == "read" || level == "write" || level == "manage", write: level == "write" || level == "manage", manage: level == "manage", denied: denied})
	}
	handler := a.Handler("")
	for _, actor := range subjects {
		t.Run(actor.name, func(t *testing.T) {
			path := "/api/documents/" + document.ID
			if actor.name != "owner" && actor.name != "administrator" {
				requireStatus(t, jsonRequest(t, handler, "DELETE", path, "", actor.identity), actor.denied)
			}
			for _, method := range []string{"GET", "HEAD"} {
				response := jsonRequest(t, handler, method, path+"/preview", "", actor.identity)
				expected := actor.denied
				if actor.read {
					expected = 200
				}
				requireStatus(t, response, expected)
				if !actor.read && (strings.Contains(response.Body.String(), document.Name) || response.Header().Get("Content-Disposition") != "") {
					t.Fatal("preview leaked restricted metadata")
				}
			}
			for _, method := range []string{"GET", "HEAD"} {
				for _, headers := range []map[string]string{
					{}, {"Range": "bytes=0-0"}, {"Range": "bytes=999999-"},
					{"If-None-Match": `"` + document.SHA256 + `"`}, {"If-Modified-Since": "Wed, 01 Jan 2031 00:00:00 GMT"},
				} {
					response := asUser(t, handler, method, path+"/download", nil, "", actor.identity, func(r *http.Request) {
						for key, value := range headers {
							r.Header.Set(key, value)
						}
					})
					if !actor.read {
						requireStatus(t, response, actor.denied)
						for _, header := range []string{"Content-Disposition", "ETag", "X-Checksum-SHA256", "Content-Range", "Last-Modified"} {
							if response.Header().Get(header) != "" {
								t.Fatalf("private %s header leaked", header)
							}
						}
						if strings.Contains(response.Body.String(), document.Name) || strings.Contains(response.Body.String(), document.SHA256) {
							t.Fatal("private document metadata leaked")
						}
					} else if response.Code != 200 && response.Code != 206 && response.Code != 304 && response.Code != 416 {
						t.Fatalf("authorized download status %d", response.Code)
					}
				}
			}
			expected := actor.denied
			if actor.write {
				expected = 204
			}
			requireStatus(t, jsonRequest(t, handler, "PATCH", path, `{"name":"private-matrix.txt"}`, actor.identity), expected)
			expected = actor.denied
			if actor.manage {
				expected = 200
			}
			requireStatus(t, jsonRequest(t, handler, "GET", path+"/permissions", "", actor.identity), expected)
			expected = actor.denied
			if actor.manage {
				expected = 204
			}
			requireStatus(t, jsonRequest(t, handler, "PUT", path+"/permissions/"+target.ID, `{"level":"read"}`, actor.identity), expected)
			requireStatus(t, jsonRequest(t, handler, "DELETE", path+"/permissions/"+target.ID, "", actor.identity), expected)
			response := jsonRequest(t, handler, "GET", "/api/documents?limit=1&offset=0", "", actor.identity)
			if actor.denied == 401 {
				requireStatus(t, response, 401)
				return
			}
			requireStatus(t, response, 200)
			var body struct {
				Documents []struct {
					ID string `json:"id"`
				}
				HasMore bool `json:"has_more"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !actor.user.IsAdmin {
				if !actor.read && (strings.Contains(response.Body.String(), document.ID) || strings.Contains(response.Body.String(), document.Name)) {
					t.Fatal("restricted document metadata leaked from listing")
				}
				if strings.Contains(response.Body.String(), other.ID) || strings.Contains(response.Body.String(), other.Name) {
					t.Fatal("unrelated metadata crossed the SQL filter")
				}
				if body.HasMore {
					t.Fatal("hidden rows influenced pagination")
				}
				if len(body.Documents) != map[bool]int{true: 1, false: 0}[actor.read] {
					t.Fatal("grant/ownership listing mismatch")
				}
			}
		})
	}
	// A fabricated cached admin flag must not override database authority.
	forged := subjects[0].user
	forged.IsAdmin = true
	if _, err := a.Documents.Get(context.Background(), forged, other.ID); err == nil {
		t.Fatal("cached admin flag bypassed SQL authorization")
	}
	if err := a.Documents.Delete(context.Background(), forged, other.ID); err == nil {
		t.Fatal("cached admin flag bypassed deletion authorization")
	}
}

func TestEveryAuthenticatedMutationRejectsInvalidCSRF(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	target := createMember(t, a, "csrf-target")
	document := uploadAs(t, a, fixtureIdentity(), "csrf-private.txt")
	second, _ := signIn(t, a, fixtureActor.Username, "fictional-admin-password", nil)
	mutations := []struct{ method, path, body string }{
		{"POST", "/api/documents", ""},
		{"PATCH", "/api/documents/" + document.ID, `{"name":"renamed.txt"}`},
		{"DELETE", "/api/documents/" + document.ID, ""},
		{"PUT", "/api/documents/" + document.ID + "/permissions/" + target.ID, `{"level":"manage"}`},
		{"DELETE", "/api/documents/" + document.ID + "/permissions/" + target.ID, ""},
		{"POST", "/api/admin/users", `{"username":"csrf-admin","password":"fictional-password","is_admin":true}`},
		{"PATCH", "/api/admin/users/" + target.ID, `{"is_admin":true}`},
		{"POST", "/api/auth/logout", ""},
		{"POST", "/api/auth/revoke-sessions", ""},
		{"POST", "/api/auth/password", `{"current_password":"fictional-admin-password","new_password":"fictional-new-password"}`},
	}
	for _, token := range []struct{ name, value string }{{"missing", ""}, {"forged", "forged"}, {"other-session", second.csrf}} {
		t.Run(token.name, func(t *testing.T) {
			actor := fixtureIdentity()
			actor.csrf = token.value
			for _, mutation := range mutations {
				t.Run(mutation.method+mutation.path, func(t *testing.T) {
					requireStatus(t, jsonRequest(t, a.Handler(""), mutation.method, mutation.path, mutation.body, actor), 403)
				})
			}
		})
	}
	var admin bool
	if err := a.Auth.DB.QueryRow("SELECT is_admin FROM users WHERE id=?", target.ID).Scan(&admin); err != nil || admin {
		t.Fatal("CSRF changed administrator authority", err)
	}
	var count int
	if err := a.Auth.DB.QueryRow("SELECT count(*) FROM document_permissions").Scan(&count); err != nil || count != 0 {
		t.Fatal("CSRF changed grants", err)
	}
}

func TestGenericAuthenticationFailureAndCanonicalThrottle(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	now := time.Now().UTC()
	a.Auth.Now = func() time.Time { return now }
	createMember(t, a, "known-user")
	disabled := createMember(t, a, "disabled-user")
	enabled := false
	if _, err := a.Auth.UpdateUser(context.Background(), fixtureActor, disabled.ID, auth.UserUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	var baseline string
	for _, username := range []string{"known-user", "unknown-user", "disabled-user", "' OR 1=1 --"} {
		response := jsonRequest(t, a.Handler(""), "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong"}`, username), nil)
		requireStatus(t, response, 401)
		if baseline == "" {
			baseline = response.Body.String()
		}
		if response.Body.String() != baseline || !strings.Contains(response.Body.String(), "invalid username or password") || len(response.Result().Cookies()) != 0 {
			t.Fatal("failure response disclosed account state")
		}
	}
	disabledLogin := jsonRequest(t, a.Handler(""), "POST", "/api/auth/login", `{"username":"disabled-user","password":"fictional-member-password"}`, nil)
	requireStatus(t, disabledLogin, 401)
	if disabledLogin.Body.String() != baseline || len(disabledLogin.Result().Cookies()) != 0 {
		t.Fatal("correct password disclosed disabled account state")
	}
	for _, username := range []string{"KNOWN-USER", " known-user ", "Known-User", "known-user"} {
		requireStatus(t, jsonRequest(t, a.Handler(""), "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong"}`, username), nil), 401)
	}
	for i := 0; i < 4; i++ {
		requireStatus(t, jsonRequest(t, a.Handler(""), "POST", "/api/auth/login", `{"username":"unknown-user","password":"wrong"}`, nil), 401)
	}
	for _, name := range []string{"known-user", "unknown-user"} {
		response := jsonRequest(t, a.Handler(""), "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"fictional-member-password"}`, name), nil)
		requireStatus(t, response, 429)
		if response.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry hint")
		}
	}
	now = now.Add(16 * time.Minute)
	signIn(t, a, "known-user", memberPassword, nil)
}
