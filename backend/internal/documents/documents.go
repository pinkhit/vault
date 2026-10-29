// Package documents coordinates metadata and filesystem storage.
package documents

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pinkhit/vault/backend/internal/audit"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/storage"
)

var (
	ErrName     = errors.New("filename must be 1–255 UTF-8 bytes with no path separators or control characters")
	ErrChecksum = errors.New("SHA-256 checksum does not match uploaded bytes")
)

type Document struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	MediaType string    `json:"media_type"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
	OwnerID   string    `json:"owner_id"`
}

type Service struct {
	DB       *sql.DB
	Store    *storage.Store
	MaxBytes int64
	Log      *slog.Logger
}

func ValidateName(name string) error {
	if !utf8.ValidString(name) || len(name) > 255 || strings.TrimSpace(name) == "" {
		return ErrName
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return ErrName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrName
		}
	}
	return nil
}

func (s *Service) Upload(ctx context.Context, actor auth.User, name string, reader io.Reader, expected string) (Document, error) {
	if actor.ID == "" {
		return Document{}, auth.ErrUnauthenticated
	}
	if err := ValidateName(name); err != nil {
		return Document{}, err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Document{}, err
	}
	id := hex.EncodeToString(token[:])
	buffered := bufio.NewReader(reader)
	prefix, err := buffered.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return Document{}, err
	}
	mediaType := http.DetectContentType(prefix)
	staged, err := s.Store.Stage(id, buffered, s.MaxBytes)
	if err != nil {
		return Document{}, err
	}
	defer func() {
		if err := s.Store.DiscardStage(id); err != nil {
			s.Log.Error("staging cleanup failed", "document_id", id, "error", err)
		}
	}()
	if expected != "" && expected != staged.SHA256 {
		return Document{}, ErrChecksum
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if err = s.Store.Promote(id); err != nil {
		return Document{}, err
	}
	document := Document{
		ID:        id,
		Name:      name,
		Size:      staged.Size,
		MediaType: mediaType,
		SHA256:    staged.SHA256,
		CreatedAt: time.Now().UTC(),
		OwnerID:   actor.ID,
	}
	// Once promotion succeeds, never delete the object on an ambiguous database error.
	// A crash or failed insert leaves an invisible orphan for startup reconciliation.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO documents (id, name, size, media_type, sha256, created_at, owner_id)
		SELECT ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ? AND enabled = 1)`,
		document.ID,
		document.Name,
		document.Size,
		document.MediaType,
		document.SHA256,
		document.CreatedAt.Format("2006-01-02T15:04:05.000000000Z"),
		actor.ID,
		actor.ID,
	)
	if err != nil {
		return Document{}, fmt.Errorf("persist document: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Document{}, err
	}
	if inserted != 1 {
		return Document{}, auth.ErrUnauthenticated
	}
	if err = audit.Record(ctx, tx, actor.ID, "document.upload", document.ID, "allowed"); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func scan(scanner interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var timestamp string
	var owner sql.NullString
	err := scanner.Scan(&d.ID, &d.Name, &d.Size, &d.MediaType, &d.SHA256, &timestamp, &owner)
	if err != nil {
		return d, err
	}
	d.CreatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	d.OwnerID = owner.String
	return d, err
}

func (s *Service) List(ctx context.Context, actor auth.User, limit, offset int) ([]Document, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT d.id, d.name, d.size, d.media_type, d.sha256, d.created_at, d.owner_id
		FROM documents d JOIN users u ON u.id = ?
		WHERE `+authorization.Visible+`
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT ? OFFSET ?`, actor.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := make([]Document, 0)
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (s *Service) Get(ctx context.Context, actor auth.User, id string) (Document, error) {
	return scan(s.DB.QueryRowContext(ctx, `
		SELECT d.id, d.name, d.size, d.media_type, d.sha256, d.created_at, d.owner_id
		FROM documents d JOIN users u ON u.id = ?
		WHERE d.id = ? AND `+authorization.Visible, actor.ID, id))
}

func (s *Service) Download(ctx context.Context, actor auth.User, id string) (Document, *os.File, error) {
	d, err := s.Get(ctx, actor, id)
	if err != nil {
		return d, nil, err
	}
	f, err := s.Store.OpenVerified(id, d.Size, d.SHA256)
	if err == nil {
		if err = audit.Record(ctx, s.DB, actor.ID, "document.download", id, "allowed"); err != nil {
			f.Close()
			return d, nil, err
		}
	}
	return d, f, err
}

func (s *Service) Rename(ctx context.Context, actor auth.User, id, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = authorization.Require(ctx, tx, actor.ID, id, "write"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE documents SET name = ? WHERE id = ?", name, id); err != nil {
		return err
	}
	if err = audit.Record(ctx, tx, actor.ID, "document.rename", id, "allowed"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Recover(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, size, media_type, sha256, created_at, owner_id
		FROM documents`)
	if err != nil {
		return err
	}
	defer rows.Close()

	known := map[string]bool{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return err
		}
		f, err := s.Store.OpenVerified(d.ID, d.Size, d.SHA256)
		if err != nil {
			return fmt.Errorf("stored document %s is missing or corrupt: %w", d.ID, err)
		}
		f.Close()
		known[d.ID] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.Store.Reconcile(known)
}
