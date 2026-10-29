package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The trigger creates a deterministic revocation between session lookup's role
// read and the endpoint's privileged SELECT, without timing-dependent sleeps.
func TestAdministratorMetadataRechecksRevokedAuthority(t *testing.T) {
	for _, path := range []string{"/api/admin/users", "/api/admin/audit"} {
		for _, change := range []string{"is_admin = 0", "enabled = 0"} {
			t.Run(path+change, func(t *testing.T) {
				a, _ := newApp(t, t.TempDir(), 1024)
				administrator, err := a.Auth.CreateUser(context.Background(), fixtureActor, "review-admin", memberPassword, true)
				if err != nil {
					t.Fatal(err)
				}
				actor, _ := signIn(t, a, administrator.Username, memberPassword, nil)
				_, err = a.Documents.DB.Exec(`CREATE TRIGGER revoke_during_lookup
                    AFTER UPDATE OF last_seen ON sessions WHEN NEW.user_id = '` + administrator.ID + `'
                    BEGIN
                        UPDATE users SET ` + change + ` WHERE id = NEW.user_id;
                        DELETE FROM sessions WHERE user_id = NEW.user_id;
                    END`)
				if err != nil {
					t.Fatal(err)
				}
				response := jsonRequest(t, a.Handler(""), "GET", path, "", actor)
				requireStatus(t, response, http.StatusForbidden)
				if strings.Contains(response.Body.String(), fixtureActor.Username) || strings.Contains(response.Body.String(), `"events"`) {
					t.Fatal("revoked administrator received privileged metadata")
				}
			})
		}
	}
}

func TestPermissionAuditIdentifiesGranteeAndChanges(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	target := createMember(t, a, "grantee")
	document := uploadAs(t, a, fixtureIdentity(), "review.txt")
	path := "/api/documents/" + document.ID + "/permissions/" + target.ID
	handler := a.Handler("")
	requireStatus(t, jsonRequest(t, handler, "PUT", path, `{"level":"read"}`, fixtureIdentity()), 204)
	requireStatus(t, jsonRequest(t, handler, "PUT", path, `{"level":"manage"}`, fixtureIdentity()), 204)
	requireStatus(t, jsonRequest(t, handler, "DELETE", path, "", fixtureIdentity()), 204)
	response := jsonRequest(t, handler, "GET", "/api/admin/audit", "", fixtureIdentity())
	requireStatus(t, response, 200)
	var body struct {
		Events []struct {
			ObjectID  string `json:"object_id"`
			Operation string `json:"operation"`
			Details   struct {
				TargetUserID     string  `json:"target_user_id"`
				PermissionBefore *string `json:"permission_before"`
				PermissionAfter  *string `json:"permission_after"`
			} `json:"details"`
		} `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	expected := [][2]string{{"manage", ""}, {"read", "manage"}, {"", "read"}}
	var seen int
	for _, event := range body.Events {
		if event.ObjectID != document.ID || !strings.HasPrefix(event.Operation, "document.permission.") {
			continue
		}
		details := event.Details
		if seen >= len(expected) || details.TargetUserID != target.ID || details.PermissionBefore == nil || details.PermissionAfter == nil {
			t.Fatal("permission audit lacks the affected grantee or permission transition")
		}
		if *details.PermissionBefore != expected[seen][0] || *details.PermissionAfter != expected[seen][1] {
			t.Fatal("incorrect permission history")
		}
		seen++
	}
	if seen != len(expected) {
		t.Fatal("missing permission audit events")
	}
}

func TestSessionHashCSRFAndHeadersCannotAuthenticate(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	createMember(t, a, "alice")
	actor, session := signIn(t, a, "alice", memberPassword, nil)
	digest := sha256.Sum256([]byte(actor.cookie.Value))
	for _, value := range []string{hex.EncodeToString(digest[:]), actor.csrf, session.User.ID, strings.Repeat("0", 64)} {
		invalid := &identity{cookie: &http.Cookie{Name: a.Auth.CookieName(), Value: value}, csrf: actor.csrf}
		requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/documents", "", invalid), 401)
	}
	for _, header := range []string{"Authorization", "X-User-ID", "X-Session-ID"} {
		response := asUser(t, a.Handler(""), "GET", "/api/documents?session="+actor.cookie.Value, nil, "", nil, func(r *http.Request) { r.Header.Set(header, "Bearer "+actor.cookie.Value) })
		requireStatus(t, response, 401)
	}
}

func TestFixationAndFailedLoginDoNotPreserveOrReplaceIdentity(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	createMember(t, a, "alice")
	chosen := &identity{cookie: &http.Cookie{Name: a.Auth.CookieName(), Value: strings.Repeat("a", 64)}, csrf: strings.Repeat("b", 64)}
	actor, _ := signIn(t, a, "alice", memberPassword, chosen)
	if actor.cookie.Value == chosen.cookie.Value || actor.csrf == chosen.csrf {
		t.Fatal("attacker-selected session was adopted")
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/auth/session", "", chosen), 401)
	response := jsonRequest(t, a.Handler(""), "POST", "/api/auth/login", `{"username":"alice","password":"wrong"}`, actor)
	requireStatus(t, response, 401)
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("failed login replaced the session cookie")
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/auth/session", "", actor), 200)
}
