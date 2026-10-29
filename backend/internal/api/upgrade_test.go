package api

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/database"
	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/storage"
)

func TestMilestoneOneUpgradePreservesOriginalBytes(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	data := []byte("fictional document uploaded before accounts existed")
	id := strings.Repeat("a", 32)
	staged, err := store.Stage(id, bytes.NewReader(data), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Promote(id); err != nil {
		t.Fatal(err)
	}
	if err = store.DiscardStage(id); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "metadata.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	migration, err := os.ReadFile("../database/migrations/001_documents.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY) STRICT;
        INSERT INTO schema_migrations VALUES ('001_documents.sql')`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO documents(id,name,size,media_type,sha256,created_at)
        VALUES (?, 'legacy.txt', ?, 'text/plain', ?, ?)`, id, staged.Size, staged.SHA256, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := &documents.Service{DB: db, Store: store, MaxBytes: 1024, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err = service.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Get(ctx, auth.User{}, id); err != sql.ErrNoRows {
		t.Fatal("legacy document visible before account setup", err)
	}
	authentication, err := auth.New(db, 8*time.Hour, 30*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := authentication.Bootstrap(ctx, "administrator", "fictional-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	document, file, err := service.Download(ctx, administrator, id)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	original, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(original, data) || document.SHA256 != staged.SHA256 || document.OwnerID != administrator.ID {
		t.Fatal("upgrade changed bytes, checksum or ownership", err)
	}
}
