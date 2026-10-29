package auth

import (
	"context"
	"database/sql"

	"github.com/pinkhit/vault/backend/internal/audit"
)

// Bootstrap is called only by the offline, operator-controlled create-admin command.
// Existing records are assigned to this first administrator without changing bytes.
func (s *Service) Bootstrap(ctx context.Context, username, password string) (User, error) {
	name, err := normalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
		return User{}, err
	}
	if count != 0 {
		return User{}, ErrBootstrapped
	}
	user := User{ID: randomToken()[:32], Username: name, IsAdmin: true, Enabled: true}
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id, username, password_hash, is_admin, enabled, created_at)
        VALUES (?, ?, ?, 1, 1, ?)`, user.ID, name, hash, s.Now().Unix()); err != nil {
		return User{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE documents SET owner_id = ? WHERE owner_id IS NULL", user.ID); err != nil {
		return User{}, err
	}
	if err = audit.Record(ctx, tx, user.ID, "user.bootstrap", user.ID, "allowed"); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func requireAdmin(ctx context.Context, tx *sql.Tx, actor User) error {
	var allowed bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND is_admin = 1 AND enabled = 1)", actor.ID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (s *Service) CreateUser(ctx context.Context, actor User, username, password string, admin bool) (User, error) {
	// Reject non-administrators before performing expensive password work.
	if !actor.IsAdmin {
		return User{}, ErrForbidden
	}
	name, err := normalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, actor); err != nil {
		return User{}, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)", name).Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrConflict
	}
	user := User{ID: randomToken()[:32], Username: name, IsAdmin: admin, Enabled: true}
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id, username, password_hash, is_admin, enabled, created_at)
        VALUES (?, ?, ?, ?, 1, ?)`, user.ID, name, hash, admin, s.Now().Unix()); err != nil {
		return User{}, err
	}
	if err = audit.Record(ctx, tx, actor.ID, "user.create", user.ID, "allowed"); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

type UserUpdate struct {
	IsAdmin  *bool   `json:"is_admin"`
	Enabled  *bool   `json:"enabled"`
	Password *string `json:"password"`
}

func (s *Service) UpdateUser(ctx context.Context, actor User, id string, update UserUpdate) (User, error) {
	if !actor.IsAdmin {
		return User{}, ErrForbidden
	}
	var hash string
	var err error
	if update.Password != nil {
		hash, err = HashPassword(*update.Password)
		if err != nil {
			return User{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, actor); err != nil {
		return User{}, err
	}
	var user User
	var oldHash string
	err = tx.QueryRowContext(ctx, "SELECT id, username, is_admin, enabled, password_hash FROM users WHERE id = ?", id).
		Scan(&user.ID, &user.Username, &user.IsAdmin, &user.Enabled, &oldHash)
	if err != nil {
		return User{}, err
	}
	if update.IsAdmin != nil {
		user.IsAdmin = *update.IsAdmin
	}
	if update.Enabled != nil {
		user.Enabled = *update.Enabled
	}
	if hash == "" {
		hash = oldHash
	}
	if _, err = tx.ExecContext(ctx, "UPDATE users SET is_admin = ?, enabled = ?, password_hash = ? WHERE id = ?", user.IsAdmin, user.Enabled, hash, id); err != nil {
		return User{}, err
	}
	var admins int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE enabled = 1 AND is_admin = 1").Scan(&admins); err != nil {
		return User{}, err
	}
	if admins == 0 {
		return User{}, ErrLastAdmin
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
		return User{}, err
	}
	if err = audit.Record(ctx, tx, actor.ID, "user.update", id, "allowed"); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Service) Users(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, username, is_admin, enabled FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err = rows.Scan(&user.ID, &user.Username, &user.IsAdmin, &user.Enabled); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Service) ChangePassword(ctx context.Context, actor User, current, next string) error {
	if err := s.consumeAttempt(ctx, "password:"+actor.ID, "password:"+actor.ID); err != nil {
		return err
	}
	var oldHash string
	if err := s.DB.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id = ? AND enabled = 1", actor.ID).Scan(&oldHash); err != nil {
		return err
	}
	valid, err := verifyPassword(current, oldHash)
	if err != nil {
		return err
	}
	if !valid {
		if err := audit.Record(ctx, s.DB, actor.ID, "user.password", actor.ID, "denied"); err != nil {
			return err
		}
		return ErrCredentials
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ? AND enabled = 1", hash, actor.ID, oldHash)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", actor.ID); err != nil {
		return err
	}
	if err = audit.Record(ctx, tx, actor.ID, "user.password", actor.ID, "allowed"); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeAll invalidates every device session, including the caller's session.
func (s *Service) RevokeAll(ctx context.Context, actor User) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", actor.ID); err != nil {
		return err
	}
	if err = audit.Record(ctx, tx, actor.ID, "auth.revoke_all", actor.ID, "allowed"); err != nil {
		return err
	}
	return tx.Commit()
}
