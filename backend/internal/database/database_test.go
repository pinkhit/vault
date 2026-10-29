package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationReopenAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	for i := 0; i < 2; i++ {
		db, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		var versions, foreignKeys int
		var journal string
		if err = db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil {
			t.Fatal(err)
		}
		if versions != 5 {
			t.Fatalf("migrations: %d", versions)
		}
		if err = db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || journal != "wal" {
			t.Fatalf("foreign_keys=%d journal=%s", foreignKeys, journal)
		}
		if _, err = db.Exec(`INSERT INTO documents(id,name,size,media_type,sha256,created_at) VALUES ('bad','x',-1,'text/plain','bad','bad')`); err == nil {
			t.Fatal("invalid metadata accepted")
		}
		db.Close()
	}
}

func TestAuditMigrationPreservesLegacyEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	legacy := legacyDatabase(t, path, "001_documents.sql", "002_authentication.sql")
	if _, err := legacy.Exec(`INSERT INTO audit_events(id,created_at,actor_id,operation,object_id,outcome,request_id)
		VALUES(7,123,NULL,'auth.login','','denied','legacy-request')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var timestamp int64
	var operation, outcome, requestID, details string
	if err = db.QueryRow("SELECT created_at, operation, outcome, request_id, details FROM audit_events WHERE id=7").Scan(&timestamp, &operation, &outcome, &requestID, &details); err != nil {
		t.Fatal(err)
	}
	if timestamp != 123 || operation != "auth.login" || outcome != "denied" || requestID != "legacy-request" || details != "{}" {
		t.Fatal("legacy audit history changed or details were invented")
	}
}

func TestIndexingMigrationPreservesMilestoneTwo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	legacy := legacyDatabase(t, path, "001_documents.sql", "002_authentication.sql", "003_audit_details.sql")
	id, checksum := strings.Repeat("a", 32), strings.Repeat("b", 64)
	if _, err := legacy.Exec(`
        INSERT INTO users VALUES ('owner','owner','synthetic-password-hash',1,1,123);
        INSERT INTO users VALUES ('reader','reader','another-synthetic-hash',0,1,124);
        INSERT INTO sessions VALUES ('synthetic-token-hash','reader','synthetic-csrf',125,9999999999,126);
        INSERT INTO documents(id,name,size,media_type,sha256,created_at,owner_id)
            VALUES (?, 'legacy.txt', 42, 'text/plain', ?, '2026-01-01T00:00:00Z', 'owner');
        INSERT INTO document_permissions VALUES (?, 'reader', 'read');`, id, checksum, id); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name, mediaType, hash, createdAt, owner string
	var size int64
	err = db.QueryRow("SELECT name,size,media_type,sha256,created_at,owner_id FROM documents WHERE id = ?", id).
		Scan(&name, &size, &mediaType, &hash, &createdAt, &owner)
	if err != nil || name != "legacy.txt" || size != 42 || mediaType != "text/plain" || hash != checksum || createdAt != "2026-01-01T00:00:00Z" || owner != "owner" {
		t.Fatal("migration changed original document metadata", err)
	}
	var preserved bool
	err = db.QueryRow(`SELECT EXISTS (
        SELECT 1 FROM users u JOIN sessions s ON s.user_id = u.id
        JOIN document_permissions p ON p.user_id = u.id
        WHERE u.id = 'reader' AND u.password_hash = 'another-synthetic-hash'
          AND u.is_admin = 0 AND u.enabled = 1 AND s.token_hash = 'synthetic-token-hash'
          AND s.csrf_token = 'synthetic-csrf' AND s.created_at = 125 AND s.expires_at = 9999999999
          AND s.last_seen = 126 AND p.document_id = ? AND p.level = 'read'
    )`, id).Scan(&preserved)
	if err != nil || !preserved {
		t.Fatal("migration changed existing account/session/grant", err)
	}
	var status, source, token, lastError string
	var attempts int
	var created, updated, available int64
	err = db.QueryRow(`SELECT status,source_sha256,attempt_token,last_error,attempts,created_at,updated_at,available_at
        FROM document_index_jobs WHERE document_id = ?`, id).
		Scan(&status, &source, &token, &lastError, &attempts, &created, &updated, &available)
	if err != nil || status != "pending" || source != checksum || token != "" || lastError != "" || attempts != 0 || created <= 0 || updated != created || available != created {
		t.Fatal("legacy document was not safely queued", err)
	}
	if _, err := db.Exec("INSERT INTO document_index_jobs(document_id,source_sha256) VALUES (?,?)", strings.Repeat("c", 32), checksum); err == nil {
		t.Fatal("schema accepted an orphan job")
	}
	if _, err := db.Exec("INSERT INTO document_index_jobs(document_id,source_sha256) VALUES (?,?)", id, checksum); err == nil {
		t.Fatal("schema accepted duplicate document jobs")
	}
}

func legacyDatabase(t *testing.T, path string, names ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec("CREATE TABLE schema_migrations (version TEXT PRIMARY KEY) STRICT"); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := applyMigration(context.Background(), db, name); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestPassageMigrationQueuesLegacyDocumentsAndPreservesFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	legacy := legacyDatabase(t, path, "001_documents.sql", "002_authentication.sql", "003_audit_details.sql", "004_document_indexing.sql")
	for _, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("d", 32)} {
		if _, err := legacy.Exec(`INSERT INTO documents(id,name,size,media_type,sha256,created_at)
            VALUES (?, 'checkpoint.txt', 12, 'text/plain', ?, '2026-01-01T00:00:00Z')`, id, strings.Repeat("c", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO document_index_jobs(document_id,source_sha256,status,attempts)
        VALUES (?, ?, 'ready', 2)`, strings.Repeat("a", 32), strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO document_index_jobs(document_id,source_sha256,status,attempts,last_error)
        VALUES (?, ?, 'failed', 3, 'previous failure')`, strings.Repeat("b", 32), strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var preserved, backfilled int
	if err := db.QueryRow("SELECT count(*) FROM document_index_jobs WHERE status='failed' AND attempts=3 AND last_error='previous failure'").Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM document_index_jobs WHERE status='pending' AND attempts=0").Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 || backfilled != 2 {
		t.Fatal("migration lost terminal failures or left unindexed legacy documents")
	}
}
