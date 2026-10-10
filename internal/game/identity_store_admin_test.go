package game

import (
	"bytes"
	"encoding/json"
	"errors"
	"mcraft/backend/internal/identity"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func managedStoreForTest(t *testing.T, dir string) *identityStore {
	t.Helper()
	a, err := newAccounts(dir, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	a.hashCost = 4
	return &identityStore{accounts: a, unlock: func() {}}
}

func registerManagedForTest(t *testing.T, store *identityStore, username, ip string) (*identity.Profile, string) {
	t.Helper()
	user, token, err := store.RegisterWithIP(username, "account-store-test-password", "", ip)
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}

func TestIdentityManagementMetadataPersistenceAndPublicPrivacy(t *testing.T) {
	dir := t.TempDir()
	store := managedStoreForTest(t, dir)
	now := time.Now().Unix()
	user, token := registerManagedForTest(t, store, "metadata_user", "2001:0db8:0:0::2")
	registered := store.Account(user.ID)
	if registered.RegistrationIP != "2001:db8::2" || registered.LastLoginIP != "2001:db8::2" || registered.CreatedAt < now || registered.LastLoginAt != registered.CreatedAt || registered.SessionCount != 1 {
		t.Fatalf("registration metadata: %+v", registered)
	}
	if _, err := store.SetName(user.ID, "资料玩家"); err != nil {
		t.Fatal(err)
	}
	loggedIn, fresh, err := store.LoginWithIP("METADATA_USER", "account-store-test-password", identity.Hash(token), "192.0.2.8")
	if err != nil {
		t.Fatal(err)
	}
	current := store.Account(user.ID)
	if loggedIn.ID != user.ID || fresh == token || current.CreatedAt != registered.CreatedAt || current.RegistrationIP != registered.RegistrationIP || current.LastLoginIP != "192.0.2.8" || current.LastLoginAt < registered.LastLoginAt || current.Name != "资料玩家" || current.SessionCount != 1 {
		t.Fatalf("login did not preserve immutable identity/registration: %+v", current)
	}
	if store.Current(identity.Hash(token)) != nil || store.Current(identity.Hash(fresh)) == nil {
		t.Fatal("previous session rotation failed")
	}
	public, err := json.Marshal(store.Current(identity.Hash(fresh)))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"registrationIp", "createdAt", "lastLoginIp", "lastLoginAt", "disabled", "passwordHash", "192.0.2.8", "2001:db8::2", fresh, "account-store-test-password"} {
		if bytes.Contains(public, []byte(secret)) {
			t.Fatalf("public profile exposed %s", secret)
		}
	}
	admin, err := json.Marshal(store.ListAccounts())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"passwordHash", "sessions", "proof", fresh, identity.Hash(fresh), "account-store-test-password"} {
		if bytes.Contains(admin, []byte(secret)) {
			t.Fatalf("management DTO exposed %s", secret)
		}
	}
	// Detached DTOs must not allow readers to mutate the account or session map.
	current.RegistrationIP = "tampered"
	listed := store.ListAccounts()
	listed[0].Name = "tampered"
	restored := managedStoreForTest(t, dir)
	if got := restored.Account(user.ID); !reflect.DeepEqual(got, &identity.ManagedAccount{ID: user.ID, Username: user.Username, Name: "资料玩家", CreatedAt: registered.CreatedAt, RegistrationIP: "2001:db8::2", LastLoginAt: store.Account(user.ID).LastLoginAt, LastLoginIP: "192.0.2.8", SessionCount: 1}) {
		t.Fatalf("management metadata did not restore: %+v", got)
	}
	if restored.Current(identity.Hash(fresh)) == nil {
		t.Fatal("valid session did not restore")
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "accounts.json.pre-account-management-*.bak")); len(backups) != 0 {
		t.Fatal("new-format account unnecessarily migrated")
	}
}

func TestIdentityManagementAtomicRegistrationLoginDisableAndRevoke(t *testing.T) {
	store := managedStoreForTest(t, t.TempDir())
	user, token := registerManagedForTest(t, store, "atomic_user", "192.0.2.1")
	other, otherToken := registerManagedForTest(t, store, "atomic_other", "192.0.2.2")
	before := store.Account(user.ID)
	raw, err := os.ReadFile(store.accounts.file)
	if err != nil {
		t.Fatal(err)
	}
	// The original file remains present while an unwritable temp destination
	// exercises every rollback path, including session rotation.
	if err := os.Mkdir(store.accounts.file+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if _, token, err := store.RegisterWithIP("failed_registration", "account-store-test-password", "", "198.51.100.1"); err == nil || token != "" || len(store.ListAccounts()) != 2 {
		t.Fatal("failed registration committed")
	}
	if _, token, err := store.LoginWithIP(user.Username, "account-store-test-password", identity.Hash(token), "198.51.100.2"); err == nil || token != "" {
		t.Fatal("failed login issued a session")
	}
	if _, err := store.SetAccountDisabled(user.ID, true); err == nil {
		t.Fatal("failed disable reported success")
	}
	if n, err := store.RevokeAccountSessions(user.ID); err == nil || n != 0 {
		t.Fatal("failed revocation reported success")
	}
	if !reflect.DeepEqual(before, store.Account(user.ID)) || store.Current(identity.Hash(token)) == nil || store.Current(identity.Hash(otherToken)) == nil {
		t.Fatal("save failure changed metadata or sessions")
	}
	if after, _ := os.ReadFile(store.accounts.file); !bytes.Equal(after, raw) {
		t.Fatal("save failure changed original disk")
	}
	if err := os.Remove(store.accounts.file + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if n, err := store.RevokeAccountSessions(user.ID); err != nil || n != 1 {
		t.Fatalf("revocation: %d %v", n, err)
	}
	if store.Current(identity.Hash(token)) != nil || store.Current(identity.Hash(otherToken)) == nil || store.Account(other.ID).SessionCount != 1 {
		t.Fatal("revocation affected the wrong account")
	}
	if n, err := store.RevokeAccountSessions(user.ID); err != nil || n != 0 {
		t.Fatal("repeated revocation was not idempotent")
	}
}

func TestIdentityManagementDisablePersistenceAndNoSessionRevival(t *testing.T) {
	dir := t.TempDir()
	store := managedStoreForTest(t, dir)
	user, token := registerManagedForTest(t, store, "disable_user", "192.0.2.10")
	before := store.Account(user.ID)
	if _, err := store.SetName(user.ID, "封禁测试"); err != nil {
		t.Fatal(err)
	}
	disabled, err := store.SetAccountDisabled(user.ID, true)
	if err != nil || disabled == nil || !disabled.Disabled || disabled.SessionCount != 0 {
		t.Fatalf("disable: %+v %v", disabled, err)
	}
	if store.Current(identity.Hash(token)) != nil {
		t.Fatal("disabled account retained access")
	}
	if _, _, err := store.LoginWithIP(user.Username, "account-store-test-password", "", "198.51.100.2"); err == nil {
		t.Fatal("disabled account logged in")
	} else {
		var status interface{ StatusCode() int }
		if !errors.As(err, &status) || status.StatusCode() != 403 {
			t.Fatal("disabled account error status")
		}
	}
	if _, err := store.SetName(user.ID, "不应改名"); err == nil {
		t.Fatal("disabled account changed name")
	}
	restored := managedStoreForTest(t, dir)
	if !restored.Account(user.ID).Disabled || restored.Current(identity.Hash(token)) != nil {
		t.Fatal("disabled state did not restore")
	}
	if _, err := restored.SetAccountDisabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	if restored.Current(identity.Hash(token)) != nil {
		t.Fatal("re-enable revived an old session")
	}
	login, fresh, err := restored.LoginWithIP(user.Username, "account-store-test-password", "", "198.51.100.3")
	if err != nil || login.ID != user.ID || restored.Current(identity.Hash(fresh)) == nil {
		t.Fatal("re-enabled account could not authenticate")
	}
	after := restored.Account(user.ID)
	if after.RegistrationIP != before.RegistrationIP || after.CreatedAt != before.CreatedAt || after.Name != "封禁测试" || after.LastLoginIP != "198.51.100.3" {
		t.Fatal("disable/re-enable damaged profile metadata")
	}
	if _, err := restored.SetAccountDisabled("missing_account", true); err == nil {
		t.Fatal("missing account accepted")
	}
	if _, err := restored.RevokeAccountSessions("missing_account"); err == nil {
		t.Fatal("missing account revocation accepted")
	}
	if restored.Account("missing_account") != nil {
		t.Fatal("missing account returned a DTO")
	}
}

func TestIdentityManagementOldV1BackupPreservesIdentityAndSessions(t *testing.T) {
	dir := t.TempDir()
	oldStore := managedStoreForTest(t, dir)
	user, token := registerManagedForTest(t, oldStore, "legacy_account", "")
	oldUser := *oldStore.accounts.users[user.ID]
	// Build the exact pre-management v1 schema. An old account has no historical
	// IP; the next login must not invent a registration address for it.
	raw, err := json.Marshal(map[string]any{"version": 1, "users": []any{map[string]any{"id": oldUser.ID, "username": oldUser.Username, "name": oldUser.Name, "passwordHash": oldUser.PasswordHash, "createdAt": int64(1600000000)}}, "sessions": oldStore.accounts.sessions})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldStore.accounts.file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	store := managedStoreForTest(t, dir)
	if got := store.Account(user.ID); got.CreatedAt != 1600000000 || got.RegistrationIP != "" || got.LastLoginAt != 0 || got.LastLoginIP != "" {
		t.Fatalf("old metadata fabricated: %+v", got)
	}
	if store.Current(identity.Hash(token)) == nil {
		t.Fatal("old session was lost during load")
	}
	if _, _, err := store.LoginWithIP(user.Username, "account-store-test-password", "", "198.51.100.42"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetName(user.ID, "兼容玩家"); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "accounts.json.pre-account-management-*.bak"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one migration backup, got %d: %v", len(backups), err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(backup, raw) {
		t.Fatal("migration backup did not preserve the exact original")
	}
	if mode, err := os.Stat(backups[0]); err != nil || mode.Mode().Perm() != 0600 {
		t.Fatal("account migration backup is not private")
	}
	restored := managedStoreForTest(t, dir)
	if got := restored.Account(user.ID); got.CreatedAt != 1600000000 || got.RegistrationIP != "" || got.LastLoginIP != "198.51.100.42" || got.Name != "兼容玩家" || got.SessionCount != 2 {
		t.Fatalf("migrated account damaged: %+v", got)
	}
	if got := restored.accounts.users[user.ID]; got.PasswordHash != oldUser.PasswordHash {
		t.Fatal("migration changed password hash")
	}
	if restored.Current(identity.Hash(token)) == nil {
		t.Fatal("migration lost prior session")
	}
	if len(restored.accounts.metadataUpgrade) != 0 {
		t.Fatal("new fields not recognized after restart")
	}
}

func TestIdentityManagementFailedUpgradeIsRetryable(t *testing.T) {
	dir := t.TempDir()
	store := managedStoreForTest(t, dir)
	user, token := registerManagedForTest(t, store, "backup_user", "")
	// A pending original snapshot must remain pending when its backup cannot be
	// created; no metadata/session change is allowed to escape that failure.
	raw, _ := os.ReadFile(store.accounts.file)
	store.accounts.metadataUpgrade = append([]byte(nil), raw...)
	oldFile := store.accounts.file
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	store.accounts.file = filepath.Join(blocked, "accounts.json")
	before := store.Account(user.ID)
	if _, _, err := store.LoginWithIP(user.Username, "account-store-test-password", identity.Hash(token), "192.0.2.7"); err == nil {
		t.Fatal("failed upgrade login committed")
	}
	if !bytes.Equal(store.accounts.metadataUpgrade, raw) || !reflect.DeepEqual(before, store.Account(user.ID)) || store.Current(identity.Hash(token)) == nil {
		t.Fatal("failed backup was not rolled back/retryable")
	}
	store.accounts.file = oldFile
	if _, _, err := store.LoginWithIP(user.Username, "account-store-test-password", identity.Hash(token), "192.0.2.7"); err != nil {
		t.Fatal(err)
	}
	if len(store.accounts.metadataUpgrade) != 0 {
		t.Fatal("successful backup remained pending")
	}
	backups, _ := filepath.Glob(oldFile + ".pre-account-management-*.bak")
	if len(backups) != 1 {
		t.Fatal("retry did not create one original backup")
	}
}

func TestIdentityManagementConcurrentReadersAndMutations(t *testing.T) {
	store := managedStoreForTest(t, t.TempDir())
	user, token := registerManagedForTest(t, store, "concurrent_user", "192.0.2.8")
	var wg sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 80; i++ {
				store.ListAccounts()
				store.Account(user.ID)
				store.Current(identity.Hash(token))
			}
		}()
	}
	for i := 0; i < 5; i++ {
		if _, err := store.SetAccountDisabled(user.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetAccountDisabled(user.ID, false); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.LoginWithIP(user.Username, "account-store-test-password", "", "198.51.100.5"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RevokeAccountSessions(user.ID); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if got := store.Account(user.ID); got.Disabled || got.SessionCount != 0 || got.RegistrationIP != "192.0.2.8" {
		t.Fatal("concurrent admin operations damaged final state")
	}
}

func TestIdentityManagementRejectsInvalidContextAndExcludesExpiredSessions(t *testing.T) {
	store := managedStoreForTest(t, t.TempDir())
	if _, _, err := store.RegisterWithIP("invalid_ip_user", "account-store-test-password", "", "127.0.0.1,198.51.100.1"); err == nil || len(store.ListAccounts()) != 0 {
		t.Fatal("invalid context accepted")
	}
	user, token := registerManagedForTest(t, store, "expiry_user", "192.0.2.9")
	if _, _, err := store.LoginWithIP(user.Username, "account-store-test-password", "", strings.Repeat("x", 4096)); err == nil || store.Account(user.ID).LastLoginIP != "192.0.2.9" {
		t.Fatal("invalid login IP committed")
	}
	store.accounts.mu.Lock()
	value := store.accounts.sessions[identity.Hash(token)]
	value.Expires = time.Now().Add(-time.Minute).Unix()
	store.accounts.sessions[identity.Hash(token)] = value
	store.accounts.mu.Unlock()
	if store.Account(user.ID).SessionCount != 0 {
		t.Fatal("expired session counted as active")
	}
	if n, err := store.RevokeAccountSessions(user.ID); err != nil || n != 0 || len(store.accounts.sessions) != 0 {
		t.Fatal("expired session cleanup failed")
	}
}
