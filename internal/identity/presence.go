package identity

import (
	"errors"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"sort"
	"sync"
	"time"
)

const presenceTTL = 90 * time.Second
const presenceLimit = 20000

// Server fields remain in the v1 DTO for older clients, but website presence
// never identifies, counts or observes a private game server.
type AdminPresence struct {
	AccountID   string `json:"accountId"`
	ServerID    string `json:"serverId,omitempty"`
	ServerName  string `json:"serverName,omitempty"`
	LastSeenAt  int64  `json:"lastSeenAt"`
	Location    string `json:"location"`
	Connections int    `json:"connections"`
}
type presenceEntry struct {
	user                Profile
	connection, session string
	seen                time.Time
}
type presenceDeparture struct {
	account, session string
	seen             time.Time
}
type presenceState struct {
	// Serialize notifications with their committed changes without holding the
	// map lock while a plugin is executing.
	operations sync.Mutex
	mu         sync.Mutex
	entries    map[string]presenceEntry
	departed   map[string]presenceDeparture
	stop, done chan struct{}
	once       sync.Once
}

func (s *Service) initPresence() error {
	if s.config.GameHomeURL != "" {
		if _, err := WebURL(s.config.GameHomeURL); err != nil {
			return errors.New("invalid game launcher URL: " + err.Error())
		}
	}
	if err := s.validateTrustedProxies(); err != nil {
		return err
	}
	s.presence = &presenceState{entries: map[string]presenceEntry{}, departed: map[string]presenceDeparture{}, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.presence.done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.presence.stop:
				return
			case <-ticker.C:
				s.expirePresence("core_revoked")
			}
		}
	}()
	return nil
}
func (s *Service) closePresence() {
	if s.presence == nil {
		return
	}
	s.presence.once.Do(func() { close(s.presence.stop) })
	<-s.presence.done
}
func (s *Service) presenceEvent(name string, e presenceEntry, reason string) {
	s.emit(name, &e.user, map[string]any{"reason": reason, "actor": e.user.ID, "target": e.user.ID,
		"serverId": "", "location": "website", "lastSeenAt": e.seen.Unix(), "connectionId": e.connection})
}

// Session expiry/revocation and browser visibility are external facts, not
// cancellable game actions. Credentials, IPs and game data never enter events.
func (s *Service) expirePresence(reason string) {
	if s.presence == nil {
		return
	}
	p := s.presence
	p.operations.Lock()
	defer p.operations.Unlock()
	type removed struct {
		entry  presenceEntry
		reason string
	}
	var gone []removed
	p.mu.Lock()
	now := time.Now()
	for key, e := range p.entries {
		why := ""
		if now.Sub(e.seen) > presenceTTL {
			why = "heartbeat_expired"
		}
		if u := s.store.Current(e.session); u == nil || u.ID != e.user.ID {
			why = reason
		}
		if why != "" {
			delete(p.entries, key)
			gone = append(gone, removed{e, why})
		}
	}
	for key, d := range p.departed {
		if now.Sub(d.seen) > presenceTTL {
			delete(p.departed, key)
		}
	}
	p.mu.Unlock()
	for _, v := range gone {
		s.presenceEvent(api.AccountPresenceOffline, v.entry, v.reason)
	}
}
func (s *Service) adminPresence() []AdminPresence {
	rows := map[string]*AdminPresence{}
	if s.presence == nil {
		return []AdminPresence{}
	}
	p := s.presence
	p.mu.Lock()
	for _, e := range p.entries {
		if time.Since(e.seen) > presenceTTL {
			continue
		}
		u := s.store.Current(e.session)
		if u == nil || u.ID != e.user.ID {
			continue
		}
		row := rows[e.user.ID]
		if row == nil {
			row = &AdminPresence{AccountID: e.user.ID, Location: "website"}
			rows[e.user.ID] = row
		}
		row.Connections++
		if e.seen.Unix() > row.LastSeenAt {
			row.LastSeenAt = e.seen.Unix()
		}
	}
	p.mu.Unlock()
	out := make([]AdminPresence, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}
func (s *Service) registerPresenceRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/auth/heartbeat", s.websiteHeartbeat)
	// Old game deployments receive an explicit retirement response. Even a
	// valid game identity proof cannot be used to populate website activity.
	m.HandleFunc("POST /api/presence", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusGone, "游戏服务器在线上报已移除，请使用网页客户端心跳")
	})
}
func (s *Service) websiteHeartbeat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Connection string `json:"connectionId"`
		Online     *bool  `json:"online"`
	}
	if !body(w, r, &input) {
		return
	}
	if !connectionNonce(input.Connection) || input.Online == nil {
		fail(w, 400, "网页连接标识或在线状态无效")
		return
	}
	u, session := s.session(r)
	if u == nil {
		fail(w, 401, "请先登录核心账号")
		return
	}
	p := s.presence
	p.operations.Lock()
	defer p.operations.Unlock()
	p.mu.Lock()
	// Account administration may revoke a session between initial validation
	// and this commit. Do not create presence or a successful event afterward.
	if current := s.store.Current(session); current == nil || current.ID != u.ID {
		p.mu.Unlock()
		fail(w, 401, "核心会话已失效")
		return
	}
	old, exists := p.entries[input.Connection]
	departed, left := p.departed[input.Connection]
	if left && time.Since(departed.seen) > presenceTTL {
		delete(p.departed, input.Connection)
		left = false
	}
	if exists && (old.user.ID != u.ID || old.session != session) || left && (departed.account != u.ID || departed.session != session) {
		p.mu.Unlock()
		fail(w, 409, "网页连接标识已属于另一会话")
		return
	}
	if *input.Online && left {
		p.mu.Unlock()
		fail(w, 409, "网页连接已结束，请生成新的连接标识")
		return
	}
	if !exists && !left && len(p.entries)+len(p.departed) >= presenceLimit {
		p.mu.Unlock()
		fail(w, 429, "网页在线连接已达上限")
		return
	}
	var notification string
	e := presenceEntry{user: *u, session: session, connection: input.Connection, seen: time.Now()}
	if *input.Online {
		p.entries[input.Connection] = e
		if !exists {
			notification = api.AccountPresenceUpdated
		}
	} else {
		// Retain a bounded terminal marker so a delayed online request cannot
		// resurrect a hidden or closed page. A visible resume uses a new nonce.
		if !left {
			p.departed[input.Connection] = presenceDeparture{account: u.ID, session: session, seen: time.Now()}
		}
		if exists {
			delete(p.entries, input.Connection)
			e = old
			notification = api.AccountPresenceOffline
		}
	}
	p.mu.Unlock()
	if notification != "" {
		reason := "website_heartbeat"
		if notification == api.AccountPresenceOffline {
			reason = "website_leave"
		}
		s.presenceEvent(notification, e, reason)
	}
	write(w, 200, map[string]bool{"ok": true})
}
func connectionNonce(v string) bool {
	if len(v) != 32 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
