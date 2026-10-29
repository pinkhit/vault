// Package authorization defines a flat, explicit document permission model.
package authorization

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pinkhit/vault/backend/internal/audit"
	"github.com/pinkhit/vault/backend/internal/auth"
)

var ErrLevel = errors.New("permission level must be read, write or manage")
var ErrOwner = errors.New("owner permissions cannot be changed through grants")

// Visible is shared by listing and object lookup. SQL aliases d and u are required.
// Filtering happens before pagination; unauthorized rows never enter a response.
const Visible = `u.enabled = 1 AND (
    u.is_admin = 1 OR d.owner_id = u.id OR EXISTS (
        SELECT 1 FROM document_permissions p WHERE p.document_id = d.id AND p.user_id = u.id
    )
)`

type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Require(ctx context.Context, db Querier, userID, documentID, capability string) error {
	if capability != "read" && capability != "write" && capability != "manage" {
		return ErrLevel
	}
	var allowed bool
	err := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM documents d JOIN users u ON u.id = ?
            WHERE d.id = ? AND u.enabled = 1 AND (
                u.is_admin = 1 OR d.owner_id = u.id OR EXISTS (
                    SELECT 1 FROM document_permissions p
                    WHERE p.document_id = d.id AND p.user_id = u.id
                    AND (? = 'read' OR p.level = 'manage' OR (? = 'write' AND p.level = 'write'))
                )
            )
        )`, userID, documentID, capability, capability).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return sql.ErrNoRows
	}
	return nil
}

type Grant struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Level    string `json:"level"`
}

func Grants(ctx context.Context, db *sql.DB, actor auth.User, documentID string) ([]Grant, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = Require(ctx, tx, actor.ID, documentID, "manage"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.user_id, u.username, p.level
        FROM document_permissions p JOIN users u ON u.id = p.user_id
        WHERE p.document_id = ? ORDER BY u.username`, documentID)
	if err != nil {
		return nil, err
	}
	grants := make([]Grant, 0)
	for rows.Next() {
		var grant Grant
		if err = rows.Scan(&grant.UserID, &grant.Username, &grant.Level); err != nil {
			rows.Close()
			return nil, err
		}
		grants = append(grants, grant)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return grants, nil
}

// An empty level revokes a grant. Owners and administrators retain their access.
func SetGrant(ctx context.Context, db *sql.DB, actor auth.User, documentID, targetID, level string) error {
	if level != "" && level != "read" && level != "write" && level != "manage" {
		return ErrLevel
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = Require(ctx, tx, actor.ID, documentID, "manage"); err != nil {
		return err
	}
	var owner sql.NullString
	if err = tx.QueryRowContext(ctx, "SELECT owner_id FROM documents WHERE id = ?", documentID).Scan(&owner); err != nil {
		return err
	}
	if owner.Valid && owner.String == targetID {
		return ErrOwner
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id = ?", targetID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled && level != "" {
		return sql.ErrNoRows
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT level FROM document_permissions WHERE document_id = ? AND user_id = ?", documentID, targetID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if level == "" {
		_, err = tx.ExecContext(ctx, "DELETE FROM document_permissions WHERE document_id = ? AND user_id = ?", documentID, targetID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO document_permissions(document_id, user_id, level) VALUES (?, ?, ?)
            ON CONFLICT(document_id, user_id) DO UPDATE SET level = excluded.level`, documentID, targetID, level)
	}
	if err != nil {
		return err
	}
	operation := "document.permission.grant"
	if level == "" {
		operation = "document.permission.revoke"
	}
	details := audit.Details{
		TargetUserID:     targetID,
		PermissionBefore: &previous,
		PermissionAfter:  &level,
	}
	if err = audit.RecordDetails(ctx, tx, actor.ID, operation, documentID, "allowed", details); err != nil {
		return err
	}
	return tx.Commit()
}
