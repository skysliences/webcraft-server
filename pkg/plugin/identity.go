package plugin

const (
	AccountRegistering                 = "account.register.before"
	AccountRegistered                  = "account.registered"
	AccountLoggingIn                   = "account.login.before"
	AccountLoggedIn                    = "account.logged_in"
	AccountProfileChanging             = "account.profile.before"
	AccountProfileChanged              = "account.profile.changed"
	AccountLoggingOut                  = "account.logout.before"
	AccountLoggedOut                   = "account.logged_out"
	AccountServerRegistering           = "account.server_register.before"
	AccountServerRegistered            = "account.server_registered"
	AccountServerAuthorizing           = "account.server_authorize.before"
	AccountServerAuthorized            = "account.server_authorized"
	AccountServerAuthorizationConsumed = "account.server_authorization.consumed"
	AccountAdminStatusChanging         = "account.admin.status.before"
	AccountAdminStatusChanged          = "account.admin.status.changed"
	AccountAdminSessionsRevoking       = "account.admin.sessions_revoke.before"
	AccountAdminSessionsRevoked        = "account.admin.sessions_revoked"
	// Presence events describe logged-in website page activity only. The
	// compatible serverId field is empty; location is "website".
	AccountPresenceUpdated       = "account.presence.updated"
	AccountPresenceOffline       = "account.presence.offline"
	PlayerIdentifying            = "player.identity.before"
	PlayerIdentified             = "player.identified"
	PlayerIdentityUpdated        = "player.identity.updated"
	PlayerIdentityExpired        = "player.identity.expired"
	PlayerLoggingOut             = "player.logout.before"
	PlayerLoggedOut              = "player.logged_out"
	PlayerAuthenticating         = "player.authenticate.before"
	PlayerAuthenticated          = "player.authenticated"
	PlayerAuthenticationRequired = "player.authentication.required"
)

// Identity is the immutable profile verified by the configured core authority.
// ID is the global account ID. The legacy Player.UID remains a builder ID.
type Identity struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Issuer   string `json:"issuer"`
}
type AuthField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

func identityEvents() []EventSpec {
	return []EventSpec{
		{Name: AccountRegistering, Cancellable: true}, {Name: AccountRegistered},
		{Name: AccountLoggingIn, Cancellable: true}, {Name: AccountLoggedIn},
		{Name: AccountProfileChanging, Cancellable: true, Mutable: []string{"name"}}, {Name: AccountProfileChanged},
		{Name: AccountLoggingOut, Cancellable: true}, {Name: AccountLoggedOut},
		{Name: AccountServerRegistering, Cancellable: true, Mutable: []string{"name"}}, {Name: AccountServerRegistered},
		{Name: AccountServerAuthorizing, Cancellable: true}, {Name: AccountServerAuthorized}, {Name: AccountServerAuthorizationConsumed},
		{Name: AccountAdminStatusChanging, Cancellable: true}, {Name: AccountAdminStatusChanged},
		{Name: AccountAdminSessionsRevoking, Cancellable: true}, {Name: AccountAdminSessionsRevoked},
		{Name: AccountPresenceUpdated}, {Name: AccountPresenceOffline},
		{Name: PlayerIdentifying, Cancellable: true}, {Name: PlayerIdentified},
		{Name: PlayerIdentityUpdated}, {Name: PlayerIdentityExpired},
		{Name: PlayerLoggingOut, Cancellable: true}, {Name: PlayerLoggedOut},
		{Name: PlayerAuthenticating, Cancellable: true, Mutable: []string{"authenticated", "prompt", "fields"}},
		{Name: PlayerAuthenticated}, {Name: PlayerAuthenticationRequired},
	}
}
