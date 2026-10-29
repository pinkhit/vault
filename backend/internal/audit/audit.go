// Package audit records security decisions without document content or credentials.
package audit

import (
	"context"
	"database/sql"
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
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	var actorID any
	if actor != "" {
		actorID = actor
	}
	_, err := db.ExecContext(ctx, `
        INSERT INTO audit_events (created_at, actor_id, operation, object_id, outcome, request_id)
        VALUES (?, ?, ?, ?, ?, ?)`, time.Now().UTC().Unix(), actorID, operation, object, outcome, requestID)
	return err
}
