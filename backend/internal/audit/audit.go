// Package audit records security decisions without document content or credentials.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// Executor allows the audit event to commit in the same transaction as a mutation.
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Record(ctx context.Context, db Executor, actor, operation, object, outcome string) error {
	return RecordDetails(ctx, db, actor, operation, object, outcome, Details{})
}

// Details is an explicit allowlist. Never accept arbitrary request bodies, hashes,
// cookies, filenames or document content as audit metadata.
type Details struct {
	TargetUserID     string  `json:"target_user_id,omitempty"`
	PermissionBefore *string `json:"permission_before,omitempty"`
	PermissionAfter  *string `json:"permission_after,omitempty"`
	AdminBefore      *bool   `json:"admin_before,omitempty"`
	AdminAfter       *bool   `json:"admin_after,omitempty"`
	EnabledBefore    *bool   `json:"enabled_before,omitempty"`
	EnabledAfter     *bool   `json:"enabled_after,omitempty"`
	PasswordChanged  bool    `json:"password_changed,omitempty"`
}

func RecordDetails(ctx context.Context, db Executor, actor, operation, object, outcome string, details Details) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	var actorID any
	if actor != "" {
		actorID = actor
	}
	_, err = db.ExecContext(ctx, `
        INSERT INTO audit_events (created_at, actor_id, operation, object_id, outcome, request_id, details)
        VALUES (?, ?, ?, ?, ?, ?, ?)`, time.Now().UTC().Unix(), actorID, operation, object, outcome, requestID, string(encoded))
	return err
}
