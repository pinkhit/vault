package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pinkhit/vault/backend/internal/database"
)

func TestPasswordHashing(t *testing.T) {
	password := "fictional-strong-password"
	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatal("hash lacks random salts or expected Argon2id parameters")
	}
	for _, tc := range []struct {
		password string
		valid    bool
	}{{password, true}, {"wrong", false}, {strings.Repeat("x", 1025), false}} {
		valid, err := verifyPassword(tc.password, first)
		if err != nil || valid != tc.valid {
			t.Fatalf("verification=%t error=%v", valid, err)
		}
	}
	for _, password := range []string{"short", strings.Repeat("x", 1025), string([]byte{0xff})} {
		if _, err := HashPassword(password); !errors.Is(err, ErrPassword) {
			t.Fatal("invalid password accepted")
		}
	}
	if _, err := HashPassword(strings.Repeat("合", 15)); err != nil {
		t.Fatal("Unicode password rejected", err)
	}
}

func TestBootstrapAdoptsLegacyMetadataAndRefusesSecondSetup(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO documents(id,name,size,media_type,sha256,created_at)
        VALUES (?, 'legacy.txt', 5, 'text/plain', ?, '2026-01-01T00:00:00Z')`, strings.Repeat("a", 32), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(db, 8*time.Hour, 30*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.Bootstrap(context.Background(), "ADMIN", "fictional-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || !user.IsAdmin || !user.Enabled {
		t.Fatalf("bad administrator: %+v", user)
	}
	var owner, checksum string
	var size int64
	if err = db.QueryRow("SELECT owner_id, sha256, size FROM documents").Scan(&owner, &checksum, &size); err != nil {
		t.Fatal(err)
	}
	if owner != user.ID || checksum != strings.Repeat("b", 64) || size != 5 {
		t.Fatal("legacy metadata damaged")
	}
	if _, err = service.Bootstrap(context.Background(), "other", "another-fictional-password"); !errors.Is(err, ErrBootstrapped) {
		t.Fatalf("repeated bootstrap: %v", err)
	}
	var hash string
	if err = db.QueryRow("SELECT password_hash FROM users WHERE id=?", user.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, "fictional-admin-password") {
		t.Fatal("plaintext password stored")
	}
}

func TestConcurrentLastAdministratorProtection(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := New(db, 8*time.Hour, 30*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Bootstrap(context.Background(), "first", "fictional-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateUser(context.Background(), first, "second", "fictional-admin-password", true)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	var wg sync.WaitGroup
	for _, actor := range []User{first, second} {
		wg.Add(1)
		go func(actor User) {
			defer wg.Done()
			_, err := service.UpdateUser(context.Background(), actor, actor.ID, UserUpdate{Enabled: &disabled})
			if err != nil && !errors.Is(err, ErrLastAdmin) {
				t.Errorf("disable: %v", err)
			}
		}(actor)
	}
	wg.Wait()
	var admins int
	if err = db.QueryRow("SELECT count(*) FROM users WHERE enabled=1 AND is_admin=1").Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("enabled administrators=%d", admins)
	}
}
