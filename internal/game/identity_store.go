package game

import (
	"mcraft/backend/internal/identity"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The standalone identity process reuses the existing account storage and
// password rules without starting a world engine or a game server.
type identityStore struct {
	accounts *accounts
	unlock   func()
}

var _ identity.ContextStore = (*identityStore)(nil)
var _ identity.AdminStore = (*identityStore)(nil)

func OpenIdentityStore(dir, importFile string) (identity.Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	unlock, err := lockData(dir)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			unlock()
		}
	}()
	file := filepath.Join(dir, "accounts.json")
	if _, err = os.Stat(file); os.IsNotExist(err) && importFile != "" {
		raw, readErr := os.ReadFile(importFile)
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		if readErr == nil {
			backup := importFile + ".pre-core-" + time.Now().UTC().Format("20060102T150405.000000000") + ".bak"
			if err = os.WriteFile(backup, raw, 0600); err != nil {
				return nil, err
			}
			if err = os.WriteFile(file, raw, 0600); err != nil {
				return nil, err
			}
		}
	}
	a, err := newAccounts(dir, map[string]bool{})
	if err != nil {
		return nil, err
	}
	ready = true
	return &identityStore{a, unlock}, nil
}
func profile(a *account) *identity.Profile {
	if a == nil {
		return nil
	}
	return &identity.Profile{ID: a.ID, Username: a.Username, Name: a.Name, LegacySID: a.LegacySID}
}
func (a *identityStore) Current(hash string) *identity.Profile {
	return profile(a.accounts.session(hash))
}
func (a *identityStore) Register(username, password, previous string) (*identity.Profile, string, error) {
	return a.RegisterWithIP(username, password, previous, "")
}
func (a *identityStore) RegisterWithIP(username, password, previous, ip string) (*identity.Profile, string, error) {
	u, t, e := a.accounts.registerWithIP(username, password, "", previous, ip)
	return profile(u), t, e
}
func (a *identityStore) Login(username, password, previous string) (*identity.Profile, string, error) {
	return a.LoginWithIP(username, password, previous, "")
}
func (a *identityStore) LoginWithIP(username, password, previous, ip string) (*identity.Profile, string, error) {
	u, t, e := a.accounts.loginWithIP(username, password, previous, ip)
	return profile(u), t, e
}
func (a *identityStore) SetName(id, name string) (*identity.Profile, error) {
	u, e := a.accounts.setName(id, name)
	return profile(u), e
}
func (a *identityStore) Logout(hash string) error   { return a.accounts.logout(hash) }
func (a *identityStore) Allow(r *http.Request) bool { return a.accounts.allowAttempt(r) }

func (a *identityStore) managedAccountLocked(user *account, now int64) identity.ManagedAccount {
	value := identity.ManagedAccount{ID: user.ID, Username: user.Username, Name: user.Name, CreatedAt: user.CreatedAt, RegistrationIP: user.RegistrationIP, LastLoginAt: user.LastLoginAt, LastLoginIP: user.LastLoginIP, Disabled: user.Disabled}
	for _, session := range a.accounts.sessions {
		if session.UserID == user.ID && session.Expires > now {
			value.SessionCount++
		}
	}
	return value
}

func (a *identityStore) ListAccounts() []identity.ManagedAccount {
	a.accounts.mu.Lock()
	defer a.accounts.mu.Unlock()
	now := time.Now().Unix()
	result := make([]identity.ManagedAccount, 0, len(a.accounts.users))
	counts := make(map[string]int, len(a.accounts.users))
	for _, session := range a.accounts.sessions {
		if session.Expires > now {
			counts[session.UserID]++
		}
	}
	for _, user := range a.accounts.users {
		value := identity.ManagedAccount{ID: user.ID, Username: user.Username, Name: user.Name, CreatedAt: user.CreatedAt, RegistrationIP: user.RegistrationIP, LastLoginAt: user.LastLoginAt, LastLoginIP: user.LastLoginIP, Disabled: user.Disabled, SessionCount: counts[user.ID]}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt != result[j].CreatedAt {
			return result[i].CreatedAt > result[j].CreatedAt
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (a *identityStore) Account(id string) *identity.ManagedAccount {
	a.accounts.mu.Lock()
	defer a.accounts.mu.Unlock()
	user := a.accounts.users[id]
	if user == nil {
		return nil
	}
	value := a.managedAccountLocked(user, time.Now().Unix())
	return &value
}

func (a *identityStore) SetAccountDisabled(id string, disabled bool) (*identity.ManagedAccount, error) {
	a.accounts.mu.Lock()
	defer a.accounts.mu.Unlock()
	old := a.accounts.users[id]
	if old == nil {
		return nil, &accountError{404, "账号不存在。"}
	}
	if old.Disabled == disabled {
		value := a.managedAccountLocked(old, time.Now().Unix())
		return &value, nil
	}
	updated := *old
	updated.Disabled = disabled
	a.accounts.users[id] = &updated
	oldSessions := a.accounts.sessions
	if disabled {
		// Re-enabling must not reactivate bearer sessions issued before the block.
		a.accounts.sessions, _ = sessionsWithoutAccount(oldSessions, id)
	}
	if err := a.accounts.saveLocked(); err != nil {
		a.accounts.users[id] = old
		a.accounts.sessions = oldSessions
		return nil, err
	}
	value := a.managedAccountLocked(&updated, time.Now().Unix())
	return &value, nil
}

func sessionsWithoutAccount(sessions map[string]accountSession, id string) (map[string]accountSession, int) {
	next := make(map[string]accountSession, len(sessions))
	now, count := time.Now().Unix(), 0
	for hash, session := range sessions {
		if session.UserID != id {
			next[hash] = session
			continue
		}
		if session.Expires > now {
			count++
		}
	}
	return next, count
}

func (a *identityStore) RevokeAccountSessions(id string) (int, error) {
	a.accounts.mu.Lock()
	defer a.accounts.mu.Unlock()
	if a.accounts.users[id] == nil {
		return 0, &accountError{404, "账号不存在。"}
	}
	old := a.accounts.sessions
	next, count := sessionsWithoutAccount(old, id)
	if len(next) == len(old) {
		return 0, nil
	}
	a.accounts.sessions = next
	if err := a.accounts.saveLocked(); err != nil {
		a.accounts.sessions = old
		return 0, err
	}
	return count, nil
}

func (a *identityStore) Close() error {
	a.accounts.mu.Lock()
	defer a.accounts.mu.Unlock()
	defer a.unlock()
	return a.accounts.saveLocked()
}
func (e *accountError) StatusCode() int { return e.status }
