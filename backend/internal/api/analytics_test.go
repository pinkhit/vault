package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/analytics"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/hoststorage"
	"github.com/pinkhit/vault/backend/internal/storage"
)

func storageReport(t *testing.T, a *API) analytics.Report {
	t.Helper()
	response := jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", fixtureIdentity())
	requireStatus(t, response, 200)
	var report analytics.Report
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("administrator data is cacheable")
	}
	return report
}

func TestStorageAggregatesAllOwnersWithoutReturningDocumentMetadata(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1<<20)
	empty := storageReport(t, a)
	if empty.Documents.Count != 0 || empty.Documents.LogicalBytes != 0 || empty.Documents.ByType == nil || len(empty.Documents.ByType) != 0 || empty.Filesystem.Capacity == nil {
		t.Fatal("empty storage report is incorrect", empty)
	}
	owner := createMember(t, a, "storage-owner")
	actor, _ := signIn(t, a, owner.Username, memberPassword, nil)
	one := uploadBytesAs(t, a, actor, "restricted-invoice.txt", []byte("first original"))
	two := uploadBytesAs(t, a, fixtureIdentity(), "admin-notes.txt", []byte("second original"))
	three := uploadBytesAs(t, a, actor, "private.svg", []byte("<?xml version=\"1.0\"?><svg>synthetic</svg>"))
	report := storageReport(t, a)
	if report.Documents.Count != 3 || report.Documents.LogicalBytes != one.Size+two.Size+three.Size || len(report.Documents.ByType) != 2 {
		t.Fatal("inaccurate metadata aggregation", report)
	}
	for _, item := range report.Documents.ByType {
		if item.MediaType == one.MediaType && (item.Count != 2 || item.LogicalBytes != one.Size+two.Size) {
			t.Fatal("file type aggregation is inaccurate", item)
		}
	}
	body, _ := json.Marshal(report)
	for _, private := range []string{one.ID, one.Name, one.SHA256, owner.ID, owner.Username, "first original", "metadata.sqlite", "objects/"} {
		if strings.Contains(string(body), private) {
			t.Fatal("storage report exposed document metadata or filesystem paths", private)
		}
	}
	var events int
	if err := a.Documents.DB.QueryRow("SELECT count(*) FROM audit_events WHERE operation='storage.analytics' AND request_id<>'' AND details='{}' AND object_id='' AND outcome='allowed'").Scan(&events); err != nil || events != 2 {
		t.Fatal("storage reads were not audited", events, err)
	}
}

func TestStorageRejectsNonAdminsSessionsIdentityClaimsAndPaths(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	member := createMember(t, a, "storage-member")
	actor, _ := signIn(t, a, member.Username, memberPassword, nil)
	doc := uploadAs(t, a, fixtureIdentity(), "private-storage.txt")
	for _, level := range []string{"read", "write", "manage"} {
		if err := authorization.SetGrant(context.Background(), a.Documents.DB, fixtureActor, doc.ID, member.ID, level); err != nil {
			t.Fatal(err)
		}
		response := jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", actor)
		requireStatus(t, response, 403)
		if strings.Contains(response.Body.String(), "logical_bytes") || strings.Contains(response.Body.String(), "filesystem") {
			t.Fatal("non-admin received storage metrics")
		}
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", nil), 401)
	for _, query := range []string{"path=/", "path=../../etc", "path=%2FVolumes%2Fsecret", "user_id=forged", "is_admin=true", "path=%zz"} {
		requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage?"+query, "", fixtureIdentity()), 400)
	}
	requireStatus(t, asUser(t, a.Handler(""), "GET", "/api/admin/storage", nil, "", fixtureIdentity(), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }), 403)
	forged := member
	forged.IsAdmin = true
	if _, err := a.Storage.Overview(context.Background(), forged); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("cached admin flag bypassed database authority", err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "POST", "/api/auth/revoke-sessions", "", fixtureIdentity()), 204)
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", fixtureIdentity()), 401)
	if _, err := a.Documents.DB.Exec("UPDATE sessions SET created_at=1,expires_at=2,last_seen=1"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", actor), 401)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Storage.Overview(cancelled, fixtureActor); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled analytics query continued", err)
	}
}

func TestStorageProviderFailureStillReturnsLogicalTotalsAndAuditFailureWithholdsThem(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	uploadAs(t, a, fixtureIdentity(), "persisted.txt")
	a.Storage = analytics.New(a.Documents.DB, func() (storage.Capacity, error) {
		return storage.Capacity{}, errors.New("secret-host-path-or-provider-diagnostic")
	}, true)
	report := storageReport(t, a)
	if report.Documents.Count != 1 || report.Filesystem.Capacity != nil || report.Filesystem.Source != "container-filesystem" || report.Filesystem.CheckedAt.IsZero() {
		t.Fatal("filesystem failure hid logical totals or fabricated capacity", report)
	}
	if _, err := a.Documents.DB.Exec(`CREATE TRIGGER fail_storage_audit BEFORE INSERT ON audit_events WHEN NEW.operation='storage.analytics'
        BEGIN SELECT RAISE(FAIL,'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", fixtureIdentity())
	requireStatus(t, response, 500)
	if strings.Contains(response.Body.String(), "logical_bytes") || strings.Contains(response.Body.String(), "secret-host-path") {
		t.Fatal("audit/provider failure leaked restricted diagnostics or metrics")
	}
}

func TestStorageRechecksRoleAfterSamplingAndRejectsDisabledAdmins(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	a.Storage = analytics.New(a.Documents.DB, func() (storage.Capacity, error) {
		_, err := a.Documents.DB.Exec("UPDATE users SET is_admin=0 WHERE id=?", fixtureActor.ID)
		return storage.Capacity{TotalBytes: 100}, err
	}, false)
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", fixtureIdentity()), 403)
	if _, err := a.Documents.DB.Exec("UPDATE users SET enabled=0 WHERE id=?", fixtureActor.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", fixtureIdentity()), 401)
}

func TestStorageGrowthUsesHostAndDockerLimitsAndNeverStaleHostData(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	dir := t.TempDir()
	a.Storage = analytics.New(a.Documents.DB, func() (storage.Capacity, error) {
		return storage.Capacity{TotalBytes: 1000, AvailableBytes: 800}, nil
	}, true)
	a.Storage.HostStorageFile = filepath.Join(dir, "storage.json")
	sample := hoststorage.Sample{Version: 1, CollectedAt: time.Now().UTC(), TotalBytes: 1000, FreeBytes: 200, AvailableIncludingReclaimableBytes: 300}
	if err := hoststorage.Publish(dir, &sample); err != nil {
		t.Fatal(err)
	}
	report := storageReport(t, a)
	if report.HostStorage == nil || report.GrowthAvailableBytes == nil || *report.GrowthAvailableBytes != 200 {
		t.Fatal("Docker virtual space or reclaimable space inflated growth headroom", report)
	}
	sample.FreeBytes, sample.AvailableIncludingReclaimableBytes = 900, 900
	hoststorage.Publish(dir, &sample)
	if report = storageReport(t, a); report.GrowthAvailableBytes == nil || *report.GrowthAvailableBytes != 800 {
		t.Fatal("Docker volume limit was ignored", report)
	}
	sample.CollectedAt = time.Now().Add(-2 * hoststorage.MaxAge)
	hoststorage.Publish(dir, &sample)
	if report = storageReport(t, a); report.HostStorage != nil || report.GrowthAvailableBytes != nil {
		t.Fatal("stale host data produced a growth estimate", report)
	}
	member := createMember(t, a, "host-metric-member")
	actor, _ := signIn(t, a, member.Username, memberPassword, nil)
	requireStatus(t, jsonRequest(t, a.Handler(""), "GET", "/api/admin/storage", "", actor), 403)
}
