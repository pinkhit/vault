package database

import (
	"context"
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
		if versions != 2 {
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
