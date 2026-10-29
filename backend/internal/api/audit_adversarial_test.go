package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAuditFailureCannotPublishSecurityMutations(t *testing.T) {
	for _, operation := range []string{"create-user", "promote", "reset-password", "change-password", "rename", "logout", "revoke-all", "rotate-login", "download"} {
		t.Run(operation, func(t *testing.T) {
			a, _ := newApp(t, t.TempDir(), 1024)
			user := createMember(t, a, "audit-subject")
			actor, _ := signIn(t, a, user.Username, memberPassword, nil)
			document := uploadAs(t, a, actor, "audit-private.txt")
			var oldHash string
			if err := a.Auth.DB.QueryRow("SELECT password_hash FROM users WHERE id=?", user.ID).Scan(&oldHash); err != nil {
				t.Fatal(err)
			}
			var oldSessions, oldUsers int
			if err := a.Auth.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&oldSessions); err != nil {
				t.Fatal(err)
			}
			if err := a.Auth.DB.QueryRow("SELECT count(*) FROM users").Scan(&oldUsers); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Auth.DB.Exec(`CREATE TRIGGER fail_security_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL,'synthetic audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			handler := a.Handler("")
			method, path, body, who := "POST", "/api/auth/logout", "", actor
			switch operation {
			case "create-user":
				path = "/api/admin/users"
				body = `{"username":"uncommitted-admin","password":"fictional-admin-password","is_admin":true}`
				who = fixtureIdentity()
			case "promote":
				method = "PATCH"
				path = "/api/admin/users/" + user.ID
				body = `{"is_admin":true}`
				who = fixtureIdentity()
			case "reset-password":
				method = "PATCH"
				path = "/api/admin/users/" + user.ID
				body = `{"password":"new-fictional-password"}`
				who = fixtureIdentity()
			case "change-password":
				path = "/api/auth/password"
				body = `{"current_password":"fictional-member-password","new_password":"new-fictional-password"}`
			case "rename":
				method = "PATCH"
				path = "/api/documents/" + document.ID
				body = `{"name":"uncommitted-name.txt"}`
			case "revoke-all":
				path = "/api/auth/revoke-sessions"
			case "rotate-login":
				path = "/api/auth/login"
				body = `{"username":"audit-subject","password":"fictional-member-password"}`
			case "download":
				method = "GET"
				path = "/api/documents/" + document.ID + "/download"
			}
			response := jsonRequest(t, handler, method, path, body, who)
			requireStatus(t, response, 500)
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("failed audited operation issued/cleared a cookie")
			}
			if response.Header().Get("Content-Disposition") != "" || strings.Contains(response.Body.String(), "fictional private document") {
				t.Fatal("audit failure released document bytes")
			}
			var hash, name string
			var admin bool
			var sessions, users int
			if err := a.Auth.DB.QueryRow("SELECT password_hash,is_admin FROM users WHERE id=?", user.ID).Scan(&hash, &admin); err != nil {
				t.Fatal(err)
			}
			if err := a.Auth.DB.QueryRow("SELECT name FROM documents WHERE id=?", document.ID).Scan(&name); err != nil {
				t.Fatal(err)
			}
			if err := a.Auth.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if err := a.Auth.DB.QueryRow("SELECT count(*) FROM users").Scan(&users); err != nil {
				t.Fatal(err)
			}
			if hash != oldHash || admin || name != document.Name || sessions != oldSessions || users != oldUsers {
				t.Fatal("security state changed without its audit event")
			}
			requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", actor), 200)
		})
	}
}

func TestAuditHistoryIsAppendOnlyAndRoleChangesAreDetailed(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	target := createMember(t, a, "role-target")
	response := jsonRequest(t, a.Handler(""), "PATCH", "/api/admin/users/"+target.ID, `{"is_admin":true,"password":"changed-fictional-password"}`, fixtureIdentity())
	requireStatus(t, response, 200)
	response = jsonRequest(t, a.Handler(""), "GET", "/api/admin/audit", "", fixtureIdentity())
	requireStatus(t, response, 200)
	var body struct {
		Events []struct {
			ObjectID  string `json:"object_id"`
			Operation string `json:"operation"`
			Details   struct {
				TargetUserID    string `json:"target_user_id"`
				Before          *bool  `json:"admin_before"`
				After           *bool  `json:"admin_after"`
				PasswordChanged bool   `json:"password_changed"`
			} `json:"details"`
		} `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range body.Events {
		if event.Operation == "user.update" && event.ObjectID == target.ID {
			details := event.Details
			if details.TargetUserID != target.ID || details.Before == nil || *details.Before || details.After == nil || !*details.After || !details.PasswordChanged {
				t.Fatal("role/password transition not auditable")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing role-change event")
	}
	for _, statement := range []string{"DELETE FROM audit_events", "UPDATE audit_events SET operation='forged'", "UPDATE audit_events SET details='{}'"} {
		if _, err := a.Auth.DB.Exec(statement); err == nil {
			t.Fatal("audit history rewrite accepted")
		}
	}
	if _, err := a.Auth.DB.Exec(`INSERT INTO audit_events(created_at,actor_id,operation,object_id,outcome,request_id,details) VALUES (0,NULL,'test','','denied','','invalid JSON')`); err == nil {
		t.Fatal("malformed audit metadata accepted")
	}
}

func TestLogoutIsolationAndSessionCap(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	aliceUser := createMember(t, a, "cap-alice")
	createMember(t, a, "cap-bob")
	first, _ := signIn(t, a, "cap-alice", memberPassword, nil)
	second, _ := signIn(t, a, "cap-alice", memberPassword, nil)
	bob, _ := signIn(t, a, "cap-bob", memberPassword, nil)
	handler := a.Handler("")
	requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/logout", "", first), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", first), 401)
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", second), 200)
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", bob), 200)
	requireStatus(t, jsonRequest(t, handler, "POST", "/api/auth/revoke-sessions", "", second), 204)
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", second), 401)
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", bob), 200)
	var oldest, latest *identity
	for i := 0; i < 11; i++ {
		actor, _ := signIn(t, a, "cap-alice", memberPassword, nil)
		if i == 0 {
			oldest = actor
		}
		latest = actor
	}
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", oldest), 401)
	requireStatus(t, jsonRequest(t, handler, "GET", "/api/auth/session", "", latest), 200)
	var count int
	if err := a.Auth.DB.QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", aliceUser.ID).Scan(&count); err != nil || count != 10 {
		t.Fatalf("session cap=%d error=%v", count, err)
	}
	if latest.cookie.HttpOnly != true || latest.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie defenses regressed")
	}
}
