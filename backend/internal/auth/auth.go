// Package auth owns password authentication and persistent, revocable sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pinkhit/vault/backend/internal/audit"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrCredentials     = errors.New("invalid username or password")
	ErrRateLimited     = errors.New("too many attempts; try again later")
	ErrForbidden       = errors.New("permission denied")
	ErrUsername        = errors.New("username must be 3–64 lowercase letters, digits, dots, underscores or hyphens")
	ErrConflict        = errors.New("username already exists")
	ErrLastAdmin       = errors.New("at least one enabled administrator is required")
	ErrBootstrapped    = errors.New("initial administrator already exists; use account management")
	usernamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)
	tokenPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	Enabled  bool   `json:"enabled"`
}

type Session struct {
	User      User      `json:"user"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Service struct {
	DB            *sql.DB
	TTL           time.Duration
	IdleTTL       time.Duration
	SecureCookies bool
	Now           func() time.Time
	dummyHash     string
	hashing       chan struct{}
}

func New(db *sql.DB, ttl, idleTTL time.Duration, secureCookies bool) (*Service, error) {
	dummy, err := HashPassword("unused-dummy-password-" + randomToken())
	if err != nil {
		return nil, err
	}
	return &Service{DB: db, TTL: ttl, IdleTTL: idleTTL, SecureCookies: secureCookies,
		Now: time.Now, dummyHash: dummy, hashing: make(chan struct{}, 2)}, nil
}

func randomToken() string {
	var bytes [32]byte
	// Go 1.26 crypto/rand.Read fills the buffer or terminates the process.
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func normalizeUsername(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !usernamePattern.MatchString(name) {
		return "", ErrUsername
	}
	return name, nil
}

func (s *Service) CookieName() string {
	if s.SecureCookies {
		return "__Host-familyvault_session"
	}
	return "familyvault_session"
}

func (s *Service) Cookie(token string) *http.Cookie {
	return &http.Cookie{Name: s.CookieName(), Value: token, Path: "/", HttpOnly: true,
		Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode,
		MaxAge: int(s.TTL.Seconds()), Expires: s.Now().UTC().Add(s.TTL)}
}

func (s *Service) ClearCookie() *http.Cookie {
	cookie := s.Cookie("")
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0)
	return cookie
}

// Login consumes a bounded, persistent rate-limit budget before password hashing.
// The caller supplies the socket peer IP, never an untrusted forwarding header.
func (s *Service) Login(ctx context.Context, username, password, peerIP, oldToken string) (Session, string, error) {
	name, err := normalizeUsername(username)
	if err != nil {
		name = ""
	}
	if err := s.consumeAttempt(ctx, name, peerIP); err != nil {
		return Session{}, "", err
	}
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	default:
		return Session{}, "", ErrRateLimited
	}

	var user User
	var hash string
	err = s.DB.QueryRowContext(ctx, `
        SELECT id, username, password_hash, is_admin, enabled FROM users WHERE username = ?`, name,
	).Scan(&user.ID, &user.Username, &hash, &user.IsAdmin, &user.Enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, "", err
	}
	if errors.Is(err, sql.ErrNoRows) {
		hash = s.dummyHash
	}
	valid, err := verifyPassword(password, hash)
	if err != nil {
		return Session{}, "", err
	}
	if !valid || !user.Enabled || user.ID == "" {
		if err := audit.Record(ctx, s.DB, "", "auth.login", "", "denied"); err != nil {
			return Session{}, "", err
		}
		return Session{}, "", ErrCredentials
	}

	now := s.Now().UTC()
	token := randomToken()
	session := Session{User: user, CSRFToken: randomToken(), ExpiresAt: now.Add(s.TTL)}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, "", err
	}
	defer tx.Rollback()
	// Recheck the account/hash after expensive work, preventing a concurrent reset
	// or disable from creating a usable session using stale credentials.
	var currentHash string
	err = tx.QueryRowContext(ctx, "SELECT password_hash, is_admin FROM users WHERE id = ? AND enabled = 1", user.ID).Scan(&currentHash, &session.User.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && currentHash != hash) {
		return Session{}, "", ErrCredentials
	}
	if err != nil {
		return Session{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ? OR expires_at <= ? OR last_seen <= ?`, tokenHash(oldToken), now.Unix(), now.Add(-s.IdleTTL).Unix()); err != nil {
		return Session{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `
        INSERT INTO sessions (token_hash, user_id, csrf_token, created_at, expires_at, last_seen)
        VALUES (?, ?, ?, ?, ?, ?)`, tokenHash(token), user.ID, session.CSRFToken, now.Unix(), session.ExpiresAt.Unix(), now.Unix()); err != nil {
		return Session{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash NOT IN
        (SELECT token_hash FROM sessions WHERE user_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 10)`, user.ID, user.ID); err != nil {
		return Session{}, "", err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_limits WHERE key = ?", "account:"+tokenHash(name)); err != nil {
		return Session{}, "", err
	}
	if err = audit.Record(ctx, tx, user.ID, "auth.login", user.ID, "allowed"); err != nil {
		return Session{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

func (s *Service) Lookup(ctx context.Context, token string) (Session, error) {
	if !tokenPattern.MatchString(token) {
		return Session{}, ErrUnauthenticated
	}
	now := s.Now().UTC()
	var session Session
	var expiry int64
	err := s.DB.QueryRowContext(ctx, `
        SELECT u.id, u.username, u.is_admin, u.enabled, s.csrf_token, s.expires_at
        FROM sessions s JOIN users u ON u.id = s.user_id
        WHERE s.token_hash = ? AND s.expires_at > ? AND s.last_seen > ? AND u.enabled = 1`,
		tokenHash(token), now.Unix(), now.Add(-s.IdleTTL).Unix(),
	).Scan(&session.User.ID, &session.User.Username, &session.User.IsAdmin, &session.User.Enabled, &session.CSRFToken, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt = time.Unix(expiry, 0).UTC()
	// A concurrent revocation must not recreate the row or authenticate this request.
	result, err := s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen = ? WHERE token_hash = ?`, now.Unix(), tokenHash(token))
	if err != nil {
		return Session{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Session{}, err
	}
	if updated != 1 {
		return Session{}, ErrUnauthenticated
	}
	return session, nil
}

func ValidCSRF(session Session, token string) bool {
	return tokenPattern.MatchString(token) && subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(token)) == 1
}

func (s *Service) Logout(ctx context.Context, token string, actor User) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash(token)); err != nil {
		return err
	}
	if err = audit.Record(ctx, tx, actor.ID, "auth.logout", actor.ID, "allowed"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) consumeAttempt(ctx context.Context, name, ip string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_limits WHERE reset_at <= ?", now); err != nil {
		return err
	}
	var keys int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM login_limits").Scan(&keys); err != nil {
		return err
	}
	if keys >= 10000 {
		return ErrRateLimited
	}
	for key, maximum := range map[string]int{"account:" + tokenHash(name): 5, "ip:" + tokenHash(ip): 50} {
		var attempts int
		err = tx.QueryRowContext(ctx, "SELECT attempts FROM login_limits WHERE key = ?", key).Scan(&attempts)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if attempts >= maximum {
			return ErrRateLimited
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO login_limits(key, attempts, reset_at) VALUES (?, 1, ?)
            ON CONFLICT(key) DO UPDATE SET attempts = attempts + 1`, key, now+900); err != nil {
			return err
		}
	}
	return tx.Commit()
}
