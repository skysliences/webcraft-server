package identity

// ContextStore records the trusted request context in the same durable write
// as the account/session change. Existing Store implementations remain valid.
type ContextStore interface {
	RegisterWithIP(username, password, previous, ip string) (*Profile, string, error)
	LoginWithIP(username, password, previous, ip string) (*Profile, string, error)
}

// ManagedAccount is only returned by the separately protected account console.
// It deliberately contains no password hashes or session credentials.
type ManagedAccount struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Name           string `json:"name"`
	CreatedAt      int64  `json:"createdAt"`
	RegistrationIP string `json:"registrationIp"`
	LastLoginAt    int64  `json:"lastLoginAt"`
	LastLoginIP    string `json:"lastLoginIp"`
	Disabled       bool   `json:"disabled"`
	SessionCount   int    `json:"sessionCount"`
}

// AdminStore exposes durable management operations without coupling core
// identity administrators to the operator privileges of any game server.
type AdminStore interface {
	ListAccounts() []ManagedAccount
	Account(id string) *ManagedAccount
	SetAccountDisabled(id string, disabled bool) (*ManagedAccount, error)
	RevokeAccountSessions(id string) (int, error)
}
