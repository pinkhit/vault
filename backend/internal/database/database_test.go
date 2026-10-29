package database

import (
	"context"
	"database/sql"
	"path/filepath"
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
		if versions != 3 {
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
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err = legacy.Exec("CREATE TABLE schema_migrations (version TEXT PRIMARY KEY) STRICT"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_documents.sql", "002_authentication.sql"} {
		data, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec(string(data)); err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES (?)", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec(`INSERT INTO audit_events(id,created_at,actor_id,operation,object_id,outcome,request_id)
		VALUES(7,123,NULL,'auth.login','','denied','legacy-request')`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
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
