package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"

	"github.com/pinkhit/vault/backend/internal/audit"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/storage"
)

type sessionKey struct{}

func sessionFrom(r *http.Request) auth.Session {
	session, _ := r.Context().Value(sessionKey{}).(auth.Session)
	return session
}

func (a *API) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(a.Auth.CookieName())
		if err != nil {
			a.securityError(w, r, auth.ErrUnauthenticated)
			return
		}
		session, err := a.Auth.Lookup(r.Context(), cookie.Value)
		if err != nil {
			a.securityError(w, r, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !auth.ValidCSRF(session, r.Header.Get("X-CSRF-Token")) {
			if err = audit.Record(r.Context(), a.Documents.DB, session.User.ID, "request.csrf", "", "denied"); err != nil {
				a.internal(w, err)
				return
			}
			problem(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, session)
		next(w, r.WithContext(ctx))
	}
}

func (a *API) admin(next http.HandlerFunc) http.HandlerFunc {
	return a.protect(func(w http.ResponseWriter, r *http.Request) {
		if !sessionFrom(r).User.IsAdmin {
			a.securityError(w, r, auth.ErrForbidden)
			return
		}
		next(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		problem(w, http.StatusUnsupportedMediaType, "expected application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	err = decoder.Decode(new(any))
	if !errors.Is(err, io.EOF) {
		problem(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	// Login has no authenticated CSRF token yet. Requiring the exact browser
	// Origin (also for command-line clients) prevents login CSRF.
	if r.Header.Get("Origin") != "http://"+r.Host {
		problem(w, http.StatusForbidden, "login requires a same-origin Origin header")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	peerIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peerIP = r.RemoteAddr
	}
	var oldToken string
	if cookie, err := r.Cookie(a.Auth.CookieName()); err == nil {
		oldToken = cookie.Value
	}
	session, token, err := a.Auth.Login(r.Context(), input.Username, input.Password, peerIP, oldToken)
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	http.SetCookie(w, a.Auth.Cookie(token))
	writeJSON(w, http.StatusOK, session)
}

func (a *API) currentSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sessionFrom(r))
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(a.Auth.CookieName())
	if err := a.Auth.Logout(r.Context(), cookie.Value, sessionFrom(r).User); err != nil {
		a.securityError(w, r, err)
		return
	}
	http.SetCookie(w, a.Auth.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) revokeSessions(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.RevokeAll(r.Context(), sessionFrom(r).User); err != nil {
		a.securityError(w, r, err)
		return
	}
	http.SetCookie(w, a.Auth.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := a.Auth.ChangePassword(r.Context(), sessionFrom(r).User, input.Current, input.Next); err != nil {
		a.securityError(w, r, err)
		return
	}
	http.SetCookie(w, a.Auth.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) users(w http.ResponseWriter, r *http.Request) {
	users, err := a.Auth.Users(r.Context())
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Admin    bool   `json:"is_admin"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := a.Auth.CreateUser(r.Context(), sessionFrom(r).User, input.Username, input.Password, input.Admin)
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	var input auth.UserUpdate
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.IsAdmin == nil && input.Enabled == nil && input.Password == nil {
		problem(w, http.StatusBadRequest, "provide is_admin, enabled or password")
		return
	}
	user, err := a.Auth.UpdateUser(r.Context(), sessionFrom(r).User, r.PathValue("id"), input)
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (a *API) rename(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := a.Documents.Rename(r.Context(), sessionFrom(r).User, r.PathValue("id"), input.Name); err != nil {
		a.securityError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) grants(w http.ResponseWriter, r *http.Request) {
	grants, err := authorization.Grants(r.Context(), a.Documents.DB, sessionFrom(r).User, r.PathValue("id"))
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": grants})
}

func (a *API) setGrant(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Level string `json:"level"`
	}
	if r.Method == http.MethodPut {
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.Level == "" {
			a.securityError(w, r, authorization.ErrLevel)
			return
		}
	}
	err := authorization.SetGrant(r.Context(), a.Documents.DB, sessionFrom(r).User, r.PathValue("id"), r.PathValue("userID"), input.Level)
	if err != nil {
		a.securityError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) auditEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Documents.DB.QueryContext(r.Context(), `
        SELECT id, created_at, COALESCE(actor_id, ''), operation, object_id, outcome, request_id
        FROM audit_events ORDER BY id DESC LIMIT 100`)
	if err != nil {
		a.internal(w, err)
		return
	}
	defer rows.Close()
	type event struct {
		ID        int64  `json:"id"`
		CreatedAt int64  `json:"created_at"`
		ActorID   string `json:"actor_id"`
		Operation string `json:"operation"`
		ObjectID  string `json:"object_id"`
		Outcome   string `json:"outcome"`
		RequestID string `json:"request_id"`
	}
	events := make([]event, 0)
	for rows.Next() {
		var item event
		if err = rows.Scan(&item.ID, &item.CreatedAt, &item.ActorID, &item.Operation, &item.ObjectID, &item.Outcome, &item.RequestID); err != nil {
			a.internal(w, err)
			return
		}
		events = append(events, item)
	}
	if err = rows.Err(); err != nil {
		a.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *API) securityError(w http.ResponseWriter, r *http.Request, err error) {
	objectID := r.PathValue("id")
	if !storage.ValidID(objectID) {
		objectID = ""
	}
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		if auditErr := audit.Record(r.Context(), a.Documents.DB, "", "auth.required", objectID, "denied"); auditErr != nil {
			a.internal(w, auditErr)
			return
		}
		problem(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, auth.ErrCredentials):
		problem(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, auth.ErrRateLimited):
		if auditErr := audit.Record(r.Context(), a.Documents.DB, sessionFrom(r).User.ID, "auth.throttled", "", "denied"); auditErr != nil {
			a.internal(w, auditErr)
			return
		}
		w.Header().Set("Retry-After", "900")
		problem(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, sql.ErrNoRows):
		if auditErr := audit.Record(r.Context(), a.Documents.DB, sessionFrom(r).User.ID, "access.check", objectID, "denied"); auditErr != nil {
			a.internal(w, auditErr)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, http.StatusNotFound, "resource not found")
		} else {
			problem(w, http.StatusForbidden, "permission denied")
		}
	case errors.Is(err, auth.ErrUsername), errors.Is(err, auth.ErrPassword), errors.Is(err, documents.ErrName), errors.Is(err, authorization.ErrLevel), errors.Is(err, authorization.ErrOwner):
		problem(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, auth.ErrConflict), errors.Is(err, auth.ErrLastAdmin):
		problem(w, http.StatusConflict, err.Error())
	case errors.Is(err, storage.ErrIntegrity):
		problem(w, http.StatusConflict, "stored document is missing or corrupt")
	default:
		a.internal(w, err)
	}
}
