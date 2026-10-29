package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/extraction"
	"github.com/pinkhit/vault/backend/internal/indexing"
)

func TestOwnerAndAdminDeleteOriginalAndDerivedData(t *testing.T) {
	for _, administrator := range []bool{false, true} {
		t.Run(map[bool]string{false: "owner", true: "administrator"}[administrator], func(t *testing.T) {
			dir := t.TempDir()
			a, _ := newApp(t, dir, 1024)
			owner := createMember(t, a, "delete-owner")
			actor, _ := signIn(t, a, owner.Username, memberPassword, nil)
			doc := uploadAs(t, a, actor, "deletionsentinel.txt")
			other := uploadAs(t, a, fixtureIdentity(), "preserved.txt")
			if err := authorization.SetGrant(context.Background(), a.Documents.DB, owner, doc.ID, fixtureActor.ID, "read"); err != nil {
				t.Fatal(err)
			}
			searchPassages(t, a, doc, extraction.Passage{Section: 1, Text: "deletedpassagesentinel"})
			if administrator {
				actor = fixtureIdentity()
			}
			path := "/api/documents/" + doc.ID
			requireStatus(t, jsonRequest(t, a.Handler(""), "DELETE", path, "", actor), 204)
			requireStatus(t, jsonRequest(t, a.Handler(""), "DELETE", path, "", actor), 404)
			for _, endpoint := range []string{"download", "preview", "indexing", "permissions"} {
				requireStatus(t, jsonRequest(t, a.Handler(""), "GET", path+"/"+endpoint, "", actor), 404)
			}
			if _, err := os.Lstat(filepath.Join(dir, "objects", doc.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("deleted original remains", err)
			}
			for _, table := range []string{"documents", "document_permissions", "document_index_jobs", "document_passages", "document_fts"} {
				column := "document_id"
				if table == "documents" {
					column = "id"
				}
				var remaining int
				if err := a.Documents.DB.QueryRow("SELECT count(*) FROM "+table+" WHERE "+column+"=?", doc.ID).Scan(&remaining); err != nil || remaining != 0 {
					t.Fatal("deletion left derived metadata", table, remaining, err)
				}
			}
			for _, query := range []string{"deletionsentinel", "deletedpassagesentinel"} {
				if page := searchPage(t, a, fixtureIdentity(), query, 1, 0); len(page.Results) != 0 || page.HasMore {
					t.Fatal("deleted data remained searchable")
				}
			}
			var postings, events int
			if err := a.Documents.DB.QueryRow("SELECT count(*) FROM passage_fts WHERE passage_fts MATCH 'deletedpassagesentinel'").Scan(&postings); err != nil || postings != 0 {
				t.Fatal("deleted passage postings remain", err)
			}
			if err := a.Documents.DB.QueryRow("SELECT count(*) FROM audit_events WHERE operation='document.delete' AND object_id=? AND actor_id=?", doc.ID, map[bool]string{false: owner.ID, true: fixtureActor.ID}[administrator]).Scan(&events); err != nil || events != 1 {
				t.Fatal("missing/duplicate deletion audit", events, err)
			}
			file, err := a.Documents.Store.OpenVerified(other.ID, other.Size, other.SHA256)
			if err != nil {
				t.Fatal("deletion damaged another original", err)
			}
			file.Close()
			if err := a.Documents.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteDuringIndexingRejectsLateCompletion(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	doc := uploadAs(t, a, fixtureIdentity(), "processing.txt")
	job, err := indexing.Claim(context.Background(), a.Documents.DB)
	if err != nil || job.DocumentID != doc.ID {
		t.Fatal("claim", err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "DELETE", "/api/documents/"+doc.ID, "", fixtureIdentity()), 204)
	if err := indexing.Finish(context.Background(), a.Documents.DB, job, indexing.Result{Status: indexing.Ready}); !errors.Is(err, indexing.ErrStale) {
		t.Fatal("deleted indexing job accepted late completion", err)
	}
}

func TestDeleteCleanupFailureIsRecoveredWithoutFollowingSymlinks(t *testing.T) {
	dir := t.TempDir()
	a, _ := newApp(t, dir, 1024)
	doc := uploadAs(t, a, fixtureIdentity(), "cleanup.txt")
	objects := filepath.Join(dir, "objects")
	moved := filepath.Join(dir, "temporarily-unavailable")
	if err := os.Rename(objects, moved); err != nil {
		t.Fatal(err)
	}
	// Metadata is already durably gone if physical cleanup fails after commit.
	requireStatus(t, jsonRequest(t, a.Handler(""), "DELETE", "/api/documents/"+doc.ID, "", fixtureIdentity()), 204)
	if err := os.Rename(moved, objects); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(objects, doc.ID)); err != nil {
		t.Fatal("fault did not retain orphan", err)
	}
	if err := a.Documents.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(objects, doc.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery retained deleted original", err)
	}

	doc = uploadAs(t, a, fixtureIdentity(), "symlink.txt")
	target := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(target, []byte("must survive"), 0600); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(objects, doc.ID)
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, object); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "DELETE", "/api/documents/"+doc.ID, "", fixtureIdentity()), 204)
	if data, err := os.ReadFile(target); err != nil || string(data) != "must survive" {
		t.Fatal("deletion followed symlink", err)
	}
	for _, id := range []string{"../outside", "", strings.Repeat("g", 32)} {
		if err := a.Documents.Store.RemoveObject(id); err == nil {
			t.Fatal("unsafe deletion key accepted", id)
		}
	}
}
