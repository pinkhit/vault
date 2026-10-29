package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/extraction"
	"github.com/pinkhit/vault/backend/internal/indexing"
)

func searchPage(t *testing.T, a *API, actor *identity, query string, limit, offset int) documents.SearchPage {
	t.Helper()
	path := fmt.Sprintf("/api/search?q=%s&limit=%d&offset=%d", url.QueryEscape(query), limit, offset)
	response := jsonRequest(t, a.Handler(""), "GET", path, "", actor)
	requireStatus(t, response, 200)
	var page documents.SearchPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func searchPassages(t *testing.T, a *API, document documents.Document, passages ...extraction.Passage) {
	t.Helper()
	tx, err := a.Documents.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE document_index_jobs SET status='ready',attempts=1,attempt_token='',last_error='' WHERE document_id=?", document.ID); err != nil {
		t.Fatal(err)
	}
	for _, passage := range passages {
		if _, err = tx.Exec("INSERT INTO document_passages(document_id,source_sha256,page,section,text) VALUES (?,?,?,?,?)", document.ID, document.SHA256, passage.Page, passage.Section, passage.Text); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSearchNamesMetadataPassagesRankingAndReferences(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	nameMatch := uploadAs(t, a, fixtureIdentity(), "printer report.txt")
	contentMatch := uploadAs(t, a, fixtureIdentity(), "scan.pdf")
	searchPassages(t, a, contentMatch,
		extraction.Passage{Page: 2, Section: 1, Text: "Printer supplier invoice INV-2026-001. <script>do-not-execute</script>"},
		extraction.Passage{Page: 3, Section: 1, Text: "Printer printer printer maintenance"})
	page := searchPage(t, a, fixtureIdentity(), "printer", 20, 0)
	if len(page.Results) != 2 || page.HasMore || page.Results[0].Document.ID != nameMatch.ID || page.Results[0].Match != "filename" ||
		page.Results[1].Document.ID != contentMatch.ID || page.Results[1].Page != 3 || page.Results[1].Section != 1 {
		t.Fatalf("incorrect ranking/deduplication/references: %+v", page)
	}
	first := searchPage(t, a, fixtureIdentity(), "printer", 1, 0)
	second := searchPage(t, a, fixtureIdentity(), "printer", 1, 1)
	if !first.HasMore || second.HasMore || first.Results[0].Document.ID != nameMatch.ID || second.Results[0].Document.ID != contentMatch.ID {
		t.Fatal("pagination is not over distinct matching documents")
	}
	for _, query := range []string{`"supplier invoice"`, "INV-2026-001"} {
		got := searchPage(t, a, fixtureIdentity(), query, 20, 0)
		if len(got.Results) != 1 || got.Results[0].Page != 2 || got.Results[0].Match != "content" {
			t.Fatalf("phrase/identifier source lost: %q: %+v", query, got)
		}
	}
	if got := searchPage(t, a, fixtureIdentity(), "INV-2026-0010", 20, 0); len(got.Results) != 0 {
		t.Fatal("identifier search silently became a prefix search")
	}
	if got := searchPage(t, a, fixtureIdentity(), nameMatch.ID, 20, 0); len(got.Results) != 1 || got.Results[0].Match != "metadata" {
		t.Fatal("document metadata is not searchable")
	}
	response := jsonRequest(t, a.Handler(""), "GET", "/api/search?q=script", "", fixtureIdentity())
	requireStatus(t, response, 200)
	if strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), `\u003cscript\u003e`) {
		t.Fatal("snippet output was rendered/interpolated as HTML")
	}
}

func TestSearchAuthorizationAndHiddenCorpusIndependence(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	alice := createMember(t, a, "search-alice")
	bob := createMember(t, a, "search-bob")
	aliceIdentity, _ := signIn(t, a, alice.Username, memberPassword, nil)
	bobIdentity, _ := signIn(t, a, bob.Username, memberPassword, nil)
	visible := uploadAs(t, a, aliceIdentity, "visible.txt")
	searchPassages(t, a, visible, extraction.Passage{Section: 1, Text: "needle public passage"})
	before, _ := json.Marshal(searchPage(t, a, aliceIdentity, "needle", 1, 0))
	var restricted documents.Document
	for i := 0; i < 8; i++ {
		body, contentType := multipartBody(t, fmt.Sprintf("needle confidential %d.txt", i), []byte(fmt.Sprintf("restricted original %d", i)))
		restricted = documentFrom(t, asUser(t, a.Handler(""), "POST", "/api/documents", body, contentType, bobIdentity, nil))
		searchPassages(t, a, restricted, extraction.Passage{Page: 7, Section: 1, Text: strings.Repeat("needle ", 200) + "private sentinel"})
	}
	after, _ := json.Marshal(searchPage(t, a, aliceIdentity, "needle", 1, 0))
	if string(before) != string(after) {
		t.Fatalf("hidden documents influenced results/ranking/pagination: %s => %s", before, after)
	}
	for _, secret := range []string{restricted.ID, restricted.Name, restricted.SHA256, "private sentinel"} {
		if strings.Contains(string(after), secret) {
			t.Fatal("restricted search metadata/content leaked")
		}
	}
	if got := searchPage(t, a, aliceIdentity, "confidential", 20, 0); len(got.Results) != 0 || got.HasMore {
		t.Fatal("restricted-only query revealed existence or counts")
	}
	for _, level := range []string{"read", "write", "manage"} {
		if err := authorization.SetGrant(context.Background(), a.Documents.DB, bob, restricted.ID, alice.ID, level); err != nil {
			t.Fatal(err)
		}
		if got := searchPage(t, a, aliceIdentity, "needle", 20, 0); len(got.Results) != 2 {
			t.Fatal("explicit grant was not honored", level)
		}
		if err := authorization.SetGrant(context.Background(), a.Documents.DB, bob, restricted.ID, alice.ID, ""); err != nil {
			t.Fatal(err)
		}
		if got := searchPage(t, a, aliceIdentity, "needle", 20, 0); len(got.Results) != 1 || got.HasMore {
			t.Fatal("revoked grant remained searchable", level)
		}
	}
	alice.IsAdmin = true
	if got, err := a.Documents.Search(context.Background(), alice, "needle", 20, 0); err != nil || len(got.Results) != 1 {
		t.Fatal("cached role bypassed search SQL", err)
	}
	if got := searchPage(t, a, fixtureIdentity(), "needle", 20, 0); len(got.Results) != 9 {
		t.Fatal("enabled administrator did not receive authorized results")
	}
	enabled := false
	if _, err := a.Auth.UpdateUser(context.Background(), fixtureActor, alice.ID, auth.UserUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/search?q=needle", "", aliceIdentity), 401)
	if got, err := a.Documents.Search(context.Background(), alice, "needle", 20, 0); err != nil || len(got.Results) != 0 {
		t.Fatal("disabled account retained SQL search visibility", err)
	}
}

func TestSearchRejectsMalformedQueriesAndIdentityParameters(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	for _, parameters := range []string{
		"", "q=", "q=%22", "q=needle*", "q=name:secret", "q=%FF", "q=%zz", "q=needle&q=secret",
		"q=needle&limit=0", "q=needle&limit=51", "q=needle&offset=-1", "q=needle&offset=10001",
		"q=needle&limit=invalid", "q=needle&offset=999999999999999999999", "q=needle&limit=1&limit=2",
		"q=needle&user_id=forged", "q=needle&is_admin=true", "q=" + strings.Repeat("x", 513),
	} {
		requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/search?"+parameters, "", fixtureIdentity()), 400)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/search?q=needle", "", nil), 401)
	uploadAs(t, a, fixtureIdentity(), "needle.txt")
	for _, injection := range []string{"needle OR restricted", `"' OR 1=1--"`, `"name:needle"`} {
		if got := searchPage(t, a, fixtureIdentity(), injection, 20, 0); len(got.Results) != 0 {
			t.Fatal("literal query became SQL/FTS syntax", injection)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Documents.Search(cancelled, fixtureActor, "needle", 20, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("search ignored cancellation", err)
	}
}

func TestSearchTracksRenamePublicationReplacementAndDeletion(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	document := uploadAs(t, a, fixtureIdentity(), "oldfilename.txt")
	searchPassages(t, a, document, extraction.Passage{Section: 1, Text: "originalpassage"})
	if _, err := a.Documents.DB.Exec(`CREATE TRIGGER fail_search_rename BEFORE INSERT ON audit_events
        BEGIN SELECT RAISE(FAIL,'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Documents.Rename(context.Background(), fixtureActor, document.ID, "uncommitted.txt"); err == nil {
		t.Fatal("rename escaped its audit transaction")
	}
	if len(searchPage(t, a, fixtureIdentity(), "oldfilename", 20, 0).Results) != 1 || len(searchPage(t, a, fixtureIdentity(), "uncommitted", 20, 0).Results) != 0 {
		t.Fatal("FTS rename update escaped rollback")
	}
	if _, err := a.Documents.DB.Exec("DROP TRIGGER fail_search_rename"); err != nil {
		t.Fatal(err)
	}
	if err := a.Documents.Rename(context.Background(), fixtureActor, document.ID, "newfilename.txt"); err != nil {
		t.Fatal(err)
	}
	if len(searchPage(t, a, fixtureIdentity(), "oldfilename", 20, 0).Results) != 0 || len(searchPage(t, a, fixtureIdentity(), "newfilename", 20, 0).Results) != 1 {
		t.Fatal("rename did not update filename search")
	}
	if _, err := a.Documents.DB.Exec("UPDATE document_index_jobs SET status='processing',attempt_token=? WHERE document_id=?", strings.Repeat("d", 64), document.ID); err != nil {
		t.Fatal(err)
	}
	if len(searchPage(t, a, fixtureIdentity(), "originalpassage", 20, 0).Results) != 0 || len(searchPage(t, a, fixtureIdentity(), "newfilename", 20, 0).Results) != 1 {
		t.Fatal("unfinished content leaked or available metadata disappeared")
	}
	if _, err := a.Documents.DB.Exec("UPDATE document_index_jobs SET status='ready',attempt_token='' WHERE document_id=?", document.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Documents.DB.Exec("UPDATE documents SET sha256=? WHERE id=?", strings.Repeat("e", 64), document.ID); err != nil {
		t.Fatal(err)
	}
	if len(searchPage(t, a, fixtureIdentity(), "originalpassage", 20, 0).Results) != 0 {
		t.Fatal("obsolete source checksum remained searchable")
	}
	// Exercise the real replacement enqueue contract without adding a replace API.
	tx, err := a.Documents.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = indexing.Enqueue(context.Background(), tx, document.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var oldHits int
	if err := a.Documents.DB.QueryRow("SELECT count(*) FROM passage_fts WHERE passage_fts MATCH 'originalpassage'").Scan(&oldHits); err != nil || oldHits != 0 {
		t.Fatal("source invalidation left old FTS postings", err)
	}
	document.SHA256 = strings.Repeat("e", 64)
	searchPassages(t, a, document, extraction.Passage{Page: 4, Section: 1, Text: "replacementpassage"})
	if got := searchPage(t, a, fixtureIdentity(), "replacementpassage", 20, 0); len(got.Results) != 1 || got.Results[0].Page != 4 {
		t.Fatal("newly published content not searchable")
	}
	if _, err := a.Documents.DB.Exec("DELETE FROM documents WHERE id=?", document.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"newfilename", "replacementpassage"} {
		if got := searchPage(t, a, fixtureIdentity(), query, 20, 0); len(got.Results) != 0 || got.HasMore {
			t.Fatal("deleted document remained searchable")
		}
	}
}

func TestSearchStableIdentitySurvivesVacuum(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	removed := uploadAs(t, a, fixtureIdentity(), "gap.txt")
	owner := createMember(t, a, "vacuum-owner")
	identity, _ := signIn(t, a, owner.Username, memberPassword, nil)
	visible := uploadAs(t, a, identity, "visible vacuum.txt")
	hidden := uploadAs(t, a, fixtureIdentity(), "hidden vacuum.txt")
	searchPassages(t, a, removed, extraction.Passage{Section: 1, Text: "removedterm"})
	searchPassages(t, a, visible, extraction.Passage{Section: 1, Text: "visibleterm"})
	searchPassages(t, a, hidden, extraction.Passage{Section: 1, Text: "hiddenterm"})
	before, _ := json.Marshal(searchPage(t, a, identity, "vacuum", 20, 0))
	if _, err := a.Documents.DB.Exec("DELETE FROM documents WHERE id=?", removed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Documents.DB.Exec("VACUUM"); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(searchPage(t, a, identity, "vacuum", 20, 0))
	if string(before) != string(after) || len(searchPage(t, a, identity, "visibleterm", 20, 0).Results) != 1 || len(searchPage(t, a, identity, "hiddenterm", 20, 0).Results) != 0 {
		t.Fatal("database maintenance changed search identity/authorization")
	}
	if _, err := a.Documents.DB.Exec("INSERT INTO passage_fts(passage_fts,rank) VALUES ('integrity-check',1)"); err != nil {
		t.Fatal("external-content FTS drifted from passages", err)
	}
}

func TestSearchFailureDoesNotLogQuery(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	var logs bytes.Buffer
	a.Log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := a.Documents.DB.Exec("DROP TABLE document_fts"); err != nil {
		t.Fatal(err)
	}
	query := "sensitive-search-marker"
	response := jsonRequest(t, a.Handler(""), "GET", "/api/search?q="+query, "", fixtureIdentity())
	requireStatus(t, response, 500)
	if strings.Contains(logs.String(), query) || strings.Contains(response.Body.String(), query) {
		t.Fatal("query text leaked through an error/log")
	}
}
