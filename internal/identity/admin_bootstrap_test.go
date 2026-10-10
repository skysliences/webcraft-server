package identity

import (
	"strings"
	"testing"
)

type collidingAdministratorStore struct{ *consoleStore }

func (s collidingAdministratorStore) ListAccounts() []ManagedAccount {
	return []ManagedAccount{*s.Account(testUserID), *s.Account(testAdminID)}
}

func TestCoreAdminBootstrapFixedIDCannotBeClaimedByUsername(t *testing.T) {
	store := newConsoleStore()
	account := store.accounts[testUserID]
	account.Username = testAdminID
	store.accounts[testUserID] = account
	s, err := New(Config{DataDir: t.TempDir(), Issuer: "http://localhost", AdminUsers: []string{testAdminID}}, collidingAdministratorStore{store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if w := consoleRequest(s, "GET", "/api/admin/me", strings.Repeat("a", 64), "", "", ""); w.Code != 200 {
		t.Fatalf("configured fixed identity lost administrator access: %d", w.Code)
	}
	if w := consoleRequest(s, "GET", "/api/admin/me", strings.Repeat("c", 64), "", "", ""); w.Code != 403 {
		t.Fatalf("username matching another user's fixed ID gained access: %d", w.Code)
	}
}
