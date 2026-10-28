// Package database owns SQLite connection configuration and embedded migrations.
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	// One connection keeps pragmas consistent and serializes this single-host MVP's writes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY
		) STRICT`)
	if err != nil {
		return err
	}

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := applyMigration(ctx, db, entry.Name()); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, name string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var applied bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = ?)", name,
	).Scan(&applied)
	if err != nil {
		return err
	}
	if applied {
		return tx.Commit()
	}

	migration, err := migrations.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, string(migration)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", name); err != nil {
		return err
	}
	return tx.Commit()
}
