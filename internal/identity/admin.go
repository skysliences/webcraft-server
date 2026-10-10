package identity

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type adminCSRF struct {
	token   string
	expires time.Time
}
type adminState struct {
	ids  map[string]bool
	csrf map[string]adminCSRF
}
type adminRegistry struct {
	Version        int      `json:"version"`
	Administrators []string `json:"administrators"`
}
type adminAccount struct {
	ManagedAccount
	Protected bool            `json:"protected"`
	Online    bool            `json:"online"`
	Locations []AdminPresence `json:"locations"`
}
type adminAuditRecord struct {
	ID       string `json:"id"`
	Time     int64  `json:"time"`
	Actor    string `json:"actor"`
	Target   string `json:"target"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	IP       string `json:"ip"`
	Outcome  string `json:"outcome"`
	Disabled *bool  `json:"disabled,omitempty"`
	Revoked  int    `json:"revoked,omitempty"`
}

// initAdmin resolves operator names once and persists fixed account IDs.
// Console privileges are deliberately independent of game server operators.
func (s *Service) initAdmin() error {
	s.admin = &adminState{ids: map[string]bool{}, csrf: map[string]adminCSRF{}}
	store, available := s.store.(AdminStore)
	if !available && len(s.config.AdminUsers) > 0 {
		return errors.New("account store does not support administration")
	}
	path := filepath.Join(s.config.DataDir, "admins.json")
	raw, err := os.ReadFile(path)
	exists := err == nil
	if exists {
		var saved adminRegistry
		if json.Unmarshal(raw, &saved) != nil || saved.Version != 1 {
			return errors.New("invalid core administrators registry")
		}
		for _, id := range saved.Administrators {
			if !IDPattern.MatchString(id) {
				return errors.New("invalid core administrator ID")
			}
			s.admin.ids[id] = true
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	changed := false
	if available {
		accounts := store.ListAccounts()
		for _, configured := range s.config.AdminUsers {
			configured = strings.TrimSpace(configured)
			var id string
			// Resolve a fixed ID before looking at usernames: another account may
			// have chosen that ID string as its username.
			for _, account := range accounts {
				if account.ID == configured {
					id = account.ID
					break
				}
			}
			if id == "" {
				for _, account := range accounts {
					if strings.EqualFold(account.Username, configured) {
						id = account.ID
						break
					}
				}
			}
			if id == "" {
				return fmt.Errorf("configured core administrator %q does not exist", configured)
			}
			if !s.admin.ids[id] {
				s.admin.ids[id], changed = true, true
			}
		}
	}
	if !exists || changed {
		ids := make([]string, 0, len(s.admin.ids))
		for id := range s.admin.ids {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		raw, err := json.Marshal(adminRegistry{Version: 1, Administrators: ids})
		if err != nil {
			return err
		}
		if err = atomic(path, raw); err != nil {
			return err
		}
	}
	return os.Chmod(path, 0600)
}

func (s *Service) registerAdminRoutes(m *http.ServeMux) {
	for route, file := range map[string]string{"GET /admin": "admin.html", "GET /admin/": "admin.html", "GET /admin/admin.js": "admin.js", "GET /admin/admin.css": "admin.css"} {
		m.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			if file == "admin.html" && r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
				http.NotFound(w, r)
				return
			}
			raw, err := web.ReadFile("web/" + file)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			contentType := "text/html; charset=utf-8"
			if strings.HasSuffix(file, ".js") {
				contentType = "text/javascript; charset=utf-8"
			} else if strings.HasSuffix(file, ".css") {
				contentType = "text/css; charset=utf-8"
			}
			w.Header().Set("Content-Type", contentType)
			w.Write(raw)
		})
	}
	m.HandleFunc("GET /api/admin/me", s.adminMe)
	m.HandleFunc("GET /api/admin/stats", s.adminStats)
	m.HandleFunc("GET /api/admin/users", s.adminUsers)
	m.HandleFunc("GET /api/admin/users/{id}", s.adminUser)
	m.HandleFunc("GET /api/admin/online", s.adminOnline)
	m.HandleFunc("GET /api/admin/servers", s.adminServers)
	m.HandleFunc("POST /api/admin/users/{id}/status", s.adminStatus)
	m.HandleFunc("POST /api/admin/users/{id}/sessions/revoke", s.adminRevoke)
}

func (s *Service) adminSession(w http.ResponseWriter, r *http.Request) (*Profile, string, AdminStore, bool) {
	u, session := s.session(r)
	if u == nil {
		fail(w, 401, "请先登录核心账号")
		return nil, "", nil, false
	}
	s.adminMu.Lock()
	allowed := s.admin != nil && s.admin.ids[u.ID]
	s.adminMu.Unlock()
	if !allowed {
		fail(w, 403, "当前账号没有核心账号管理权限")
		return nil, "", nil, false
	}
	store, available := s.store.(AdminStore)
	if !available {
		fail(w, 503, "账号存储暂不支持管理功能")
		return nil, "", nil, false
	}
	return u, session, store, true
}

func (s *Service) adminMe(w http.ResponseWriter, r *http.Request) {
	u, session, _, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	s.adminMu.Lock()
	now := time.Now()
	entry := s.admin.csrf[session]
	if now.After(entry.expires) {
		for key, old := range s.admin.csrf {
			if now.After(old.expires) {
				delete(s.admin.csrf, key)
			}
		}
		if len(s.admin.csrf) >= 2048 {
			s.adminMu.Unlock()
			fail(w, 429, "管理会话过多，请稍后重试")
			return
		}
		entry = adminCSRF{token: Random(), expires: now.Add(8 * time.Hour)}
		s.admin.csrf[session] = entry
	}
	s.adminMu.Unlock()
	write(w, 200, map[string]any{"user": public(u), "admin": true, "csrfToken": entry.token, "issuer": s.config.Issuer})
}

// The caller holds adminMu through before/commit/after, serializing management.
func (s *Service) adminCSRFValid(w http.ResponseWriter, r *http.Request, session string) bool {
	entry := s.admin.csrf[session]
	value := r.Header.Get("X-Mcraft-CSRF")
	u := s.store.Current(session)
	if u == nil || !s.admin.ids[u.ID] || r.Header.Get("Origin") != s.config.Issuer || len(value) != 64 || time.Now().After(entry.expires) || subtle.ConstantTimeCompare([]byte(value), []byte(entry.token)) != 1 {
		fail(w, 403, "管理请求校验失败，请刷新后台后重试")
		return false
	}
	return true
}

func groupAdminPresence(presence []AdminPresence) map[string][]AdminPresence {
	byAccount := map[string][]AdminPresence{}
	for _, p := range presence {
		byAccount[p.AccountID] = append(byAccount[p.AccountID], p)
	}
	return byAccount
}

func (s *Service) managedAccount(account ManagedAccount, locations []AdminPresence) adminAccount {
	if locations == nil {
		locations = []AdminPresence{}
	}
	s.adminMu.Lock()
	protected := s.admin != nil && s.admin.ids[account.ID]
	s.adminMu.Unlock()
	return adminAccount{ManagedAccount: account, Protected: protected, Online: len(locations) > 0, Locations: locations}
}

func (s *Service) adminStats(w http.ResponseWriter, r *http.Request) {
	_, _, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	accounts := store.ListAccounts()
	active := map[string]bool{}
	disabled, sessions, recent := 0, 0, 0
	for _, a := range accounts {
		active[a.ID] = !a.Disabled
		if a.Disabled {
			disabled++
		}
		sessions += a.SessionCount
		if a.CreatedAt >= time.Now().Add(-24*time.Hour).Unix() {
			recent++
		}
	}
	online := map[string]bool{}
	for _, p := range s.adminPresence() {
		if active[p.AccountID] {
			online[p.AccountID] = true
		}
	}
	s.mu.Lock()
	servers := len(s.clients)
	s.mu.Unlock()
	write(w, 200, map[string]any{"accounts": len(accounts), "disabled": disabled, "sessions": sessions, "online": len(online), "registeredToday": recent, "servers": servers, "updatedAt": time.Now().Unix()})
}

func pageNumber(raw string, fallback, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}
func (s *Service) adminUsers(w http.ResponseWriter, r *http.Request) {
	_, _, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(q) > 256 {
		fail(w, 400, "搜索内容过长")
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "all" && status != "enabled" && status != "disabled" && status != "online" {
		fail(w, 400, "用户筛选条件无效")
		return
	}
	presence := groupAdminPresence(s.adminPresence())
	rows := []adminAccount{}
	for _, a := range store.ListAccounts() {
		if q != "" && !strings.Contains(strings.ToLower(a.ID+" "+a.Username+" "+a.Name), q) {
			continue
		}
		row := s.managedAccount(a, presence[a.ID])
		if status == "enabled" && a.Disabled || status == "disabled" && !a.Disabled || status == "online" && (!row.Online || a.Disabled) {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt == rows[j].CreatedAt {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt > rows[j].CreatedAt
	})
	page, size := pageNumber(r.URL.Query().Get("page"), 1, 1000000), pageNumber(r.URL.Query().Get("pageSize"), 25, 100)
	total, pages := len(rows), (len(rows)+size-1)/size
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	start, end := (page-1)*size, page*size
	if end > total {
		end = total
	}
	write(w, 200, map[string]any{"users": rows[start:end], "total": total, "page": page, "pageSize": size, "pages": pages})
}

func (s *Service) adminUser(w http.ResponseWriter, r *http.Request) {
	_, _, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	a := store.Account(r.PathValue("id"))
	if a == nil {
		fail(w, 404, "账号不存在")
		return
	}
	write(w, 200, map[string]any{"user": s.managedAccount(*a, groupAdminPresence(s.adminPresence())[a.ID])})
}
func (s *Service) adminOnline(w http.ResponseWriter, r *http.Request) {
	_, _, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	presence := groupAdminPresence(s.adminPresence())
	rows := []adminAccount{}
	for id, locations := range presence {
		a := store.Account(id)
		if a == nil {
			continue
		}
		row := s.managedAccount(*a, locations)
		if row.Online && !a.Disabled {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Username < rows[j].Username })
	write(w, 200, map[string]any{"users": rows, "total": len(rows), "updatedAt": time.Now().Unix()})
}
func (s *Service) adminServers(w http.ResponseWriter, r *http.Request) {
	_, _, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	type serverRow struct {
		Client
		Owner     *ManagedAccount `json:"owner,omitempty"`
		Online    int             `json:"online"`
		LastSeen  int64           `json:"lastSeenAt"`
		Connected int             `json:"connections"`
	}
	s.mu.Lock()
	clients := make([]Client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	rows := []serverRow{}
	for _, c := range clients {
		row := serverRow{Client: c}
		if c.OwnerID != "" {
			row.Owner = store.Account(c.OwnerID)
		}
		// Keep API v1 fields for consumers, without collecting private game activity.
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	write(w, 200, map[string]any{"servers": rows, "total": len(rows), "updatedAt": time.Now().Unix()})
}

func (s *Service) appendAdminAudit(record adminAuditRecord) error {
	path := filepath.Join(s.config.DataDir, "admin-audit.jsonl")
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(raw, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (s *Service) adminStatus(w http.ResponseWriter, r *http.Request) {
	u, session, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if !body(w, r, &input) {
		return
	}
	if input.Disabled == nil {
		fail(w, 400, "需要指定账号状态")
		return
	}
	s.adminMu.Lock()
	committed := false
	defer func() {
		s.adminMu.Unlock()
		if committed {
			s.expirePresence("administration_status")
		}
	}()
	if !s.adminCSRFValid(w, r, session) {
		return
	}
	target := r.PathValue("id")
	a := store.Account(target)
	if a == nil {
		fail(w, 404, "账号不存在")
		return
	}
	if *input.Disabled && s.admin.ids[target] {
		fail(w, 409, "核心管理员账号受保护，不能在后台禁用")
		return
	}
	if a.Disabled == *input.Disabled {
		write(w, 200, map[string]any{"user": a, "changed": false})
		return
	}
	reason := "admin_enable"
	if *input.Disabled {
		reason = "admin_disable"
	}
	audit := adminAuditRecord{ID: Random()[:24], Time: time.Now().Unix(), Actor: u.ID, Target: target, Action: "account_status", Reason: reason, IP: s.clientIP(r), Outcome: "attempt", Disabled: input.Disabled}
	if err := s.appendAdminAudit(audit); err != nil {
		fail(w, 503, "管理审计暂不可写，账号状态未改变")
		return
	}
	e := s.emit(api.AccountAdminStatusChanging, u, map[string]any{"actor": u.ID, "target": target, "reason": reason, "account": *a, "previous": a.Disabled, "disabled": *input.Disabled, "auditId": audit.ID})
	if e.Cancelled {
		audit.Outcome = "cancelled"
		s.appendAdminAudit(audit)
		fail(w, 403, e.Reason)
		return
	}
	updated, err := store.SetAccountDisabled(target, *input.Disabled)
	if err != nil {
		audit.Outcome = "failed"
		s.appendAdminAudit(audit)
		storeError(w, err)
		return
	}
	committed = true
	audit.Outcome = "committed"
	warning := s.appendAdminAudit(audit) != nil
	if warning {
		log.Printf("account admin audit outcome failed for %s; change committed", audit.ID)
	}
	s.emit(api.AccountAdminStatusChanged, u, map[string]any{"actor": u.ID, "target": target, "reason": reason, "before": *a, "after": *updated, "previous": a.Disabled, "disabled": updated.Disabled, "auditId": audit.ID})
	write(w, 200, map[string]any{"user": updated, "changed": true, "auditWarning": warning})
}

func (s *Service) adminRevoke(w http.ResponseWriter, r *http.Request) {
	u, session, store, ok := s.adminSession(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !body(w, r, &input) {
		return
	}
	s.adminMu.Lock()
	committed := false
	defer func() {
		s.adminMu.Unlock()
		if committed {
			s.expirePresence("core_session_revoked")
		}
	}()
	if !s.adminCSRFValid(w, r, session) {
		return
	}
	target := r.PathValue("id")
	a := store.Account(target)
	if a == nil {
		fail(w, 404, "账号不存在")
		return
	}
	if s.admin.ids[target] {
		fail(w, 409, "核心管理员账号受保护，不能在后台撤销会话")
		return
	}
	audit := adminAuditRecord{ID: Random()[:24], Time: time.Now().Unix(), Actor: u.ID, Target: target, Action: "sessions_revoke", Reason: "admin_revoke_sessions", IP: s.clientIP(r), Outcome: "attempt"}
	if err := s.appendAdminAudit(audit); err != nil {
		fail(w, 503, "管理审计暂不可写，会话未被撤销")
		return
	}
	e := s.emit(api.AccountAdminSessionsRevoking, u, map[string]any{"actor": u.ID, "target": target, "reason": audit.Reason, "account": *a, "sessionCount": a.SessionCount, "auditId": audit.ID})
	if e.Cancelled {
		audit.Outcome = "cancelled"
		s.appendAdminAudit(audit)
		fail(w, 403, e.Reason)
		return
	}
	count, err := store.RevokeAccountSessions(target)
	if err != nil {
		audit.Outcome = "failed"
		s.appendAdminAudit(audit)
		storeError(w, err)
		return
	}
	committed = true
	audit.Outcome, audit.Revoked = "committed", count
	warning := s.appendAdminAudit(audit) != nil
	if warning {
		log.Printf("account admin audit outcome failed for %s; revocation committed", audit.ID)
	}
	s.emit(api.AccountAdminSessionsRevoked, u, map[string]any{"actor": u.ID, "target": target, "reason": audit.Reason, "before": *a, "after": store.Account(target), "revoked": count, "auditId": audit.ID})
	write(w, 200, map[string]any{"revoked": count, "auditWarning": warning})
}
