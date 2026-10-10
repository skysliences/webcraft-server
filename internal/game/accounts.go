package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	api "mcraft/backend/pkg/plugin"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const accountCookie = "mcraft_session"
const sessionLifetime = 30 * 24 * time.Hour

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)
var dummyHashOnce sync.Once
var dummyPasswordHash []byte
var dummyHashError error

type account struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Name           string `json:"name"`
	PasswordHash   string `json:"passwordHash"`
	CreatedAt      int64  `json:"createdAt"`
	LegacySID      string `json:"legacySid,omitempty"`
	RegistrationIP string `json:"registrationIp"`
	LastLoginAt    int64  `json:"lastLoginAt"`
	LastLoginIP    string `json:"lastLoginIp"`
	Disabled       bool   `json:"disabled"`
}

func (a *account) public() map[string]string {
	return map[string]string{"id": a.ID, "username": a.Username, "name": a.Name}
}

type accountSession struct {
	Proof         string          `json:"proof,omitempty"`
	Authenticated *bool           `json:"authenticated,omitempty"`
	Prompt        string          `json:"prompt,omitempty"`
	Fields        []api.AuthField `json:"fields,omitempty"`
	UserID        string          `json:"userId"`
	Expires       int64           `json:"expires"`
}
type accountDisk struct {
	Version  int                       `json:"version"`
	Users    []*account                `json:"users"`
	Sessions map[string]accountSession `json:"sessions"`
}
type loginLimit struct {
	Since time.Time
	Count int
}
type accounts struct {
	central          bool
	issuer, serverID string
	mu               sync.Mutex
	file             string
	users            map[string]*account
	sessions         map[string]accountSession
	legacy           map[string]bool
	limits           map[string]loginLimit
	hashCost         int
	dummyHash        []byte
	metadataUpgrade  []byte
}
type accountError struct {
	status int
	text   string
}

func (e *accountError) Error() string { return e.text }
func accountFailure(w http.ResponseWriter, err error) {
	var e *accountError
	if errors.As(err, &e) {
		failure(w, e.status, e.text)
	} else {
		failure(w, 503, "账号数据保存失败，请检查后端存储后重试。")
	}
}
func validPlayerName(name string) bool {
	if n := utf8.RuneCountInString(name); n < 2 || n > 20 {
		return false
	}
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
func newAccounts(dir string, legacy map[string]bool) (*accounts, error) {
	a := &accounts{file: filepath.Join(dir, "accounts.json"), users: map[string]*account{}, sessions: map[string]accountSession{}, legacy: legacy, limits: map[string]loginLimit{}, hashCost: 12}
	dummyHashOnce.Do(func() {
		dummyPasswordHash, dummyHashError = bcrypt.GenerateFromPassword([]byte(randomHex(24)), 12)
	})
	a.dummyHash = dummyPasswordHash
	if dummyHashError != nil {
		return nil, dummyHashError
	}
	raw, err := os.ReadFile(a.file)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	var disk accountDisk
	if err := json.Unmarshal(raw, &disk); err != nil || disk.Version != 1 {
		return nil, fmt.Errorf("账号存档损坏或版本不支持；原文件未修改")
	}
	usernames, names := map[string]bool{}, map[string]bool{}
	for _, user := range disk.Users {
		if user == nil || !sidRE.MatchString(user.ID) || !usernameRE.MatchString(user.Username) || a.users[user.ID] != nil || usernames[strings.ToLower(user.Username)] || user.Name != "" && (!validPlayerName(user.Name) || names[strings.ToLower(user.Name)]) {
			return nil, fmt.Errorf("账号存档含无效或重复用户；原文件未修改")
		}
		if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err != nil {
			return nil, fmt.Errorf("账号密码哈希损坏；原文件未修改")
		}
		a.users[user.ID] = user
		usernames[strings.ToLower(user.Username)] = true
		if user.Name != "" {
			names[strings.ToLower(user.Name)] = true
		}
	}
	for hash, session := range disk.Sessions {
		if len(hash) != 64 || a.users[session.UserID] == nil {
			return nil, fmt.Errorf("账号会话存档无效；原文件未修改")
		}
		if session.Expires > time.Now().Unix() {
			a.sessions[hash] = session
		}
	}
	claimed := map[string]bool{}
	for _, user := range a.users {
		if user.LegacySID == "" {
			continue
		}
		if !sidRE.MatchString(user.LegacySID) || a.users[user.LegacySID] != nil || claimed[user.LegacySID] {
			return nil, fmt.Errorf("账号旧身份绑定无效或重复；原文件未修改")
		}
		claimed[user.LegacySID] = true
	}
	// Keep v1 readable by older installations. Before first writing the added
	// management fields, preserve the exact old file, including session hashes.
	var encoded struct {
		Users []map[string]json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	for _, user := range encoded.Users {
		if user["registrationIp"] == nil || user["lastLoginAt"] == nil || user["lastLoginIp"] == nil || user["disabled"] == nil {
			a.metadataUpgrade = append([]byte(nil), raw...)
			break
		}
	}
	return a, nil
}
func (a *accounts) saveLocked() error {
	if a.central {
		return a.saveFederatedLocked()
	}
	users := make([]*account, 0, len(a.users))
	for _, user := range a.users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	raw, err := json.Marshal(accountDisk{Version: 1, Users: users, Sessions: a.sessions})
	if err != nil {
		return err
	}
	if err := a.backupMetadataUpgradeLocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(a.file+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(a.file+".tmp", a.file)
}

func (a *accounts) backupMetadataUpgradeLocked() error {
	if len(a.metadataUpgrade) == 0 {
		return nil
	}
	backup := a.file + ".pre-account-management-" + time.Now().UTC().Format("20060102T150405.000000000") + ".bak"
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(a.metadataUpgrade)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(backup)
		return err
	}
	a.metadataUpgrade = nil
	return nil
}
func (a *accounts) allowAttempt(req *http.Request) bool {
	// RemoteAddr is supplied by the server; forwarded headers are not trusted.
	ip, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		ip = req.RemoteAddr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, limit := range a.limits {
		if now.Sub(limit.Since) >= 10*time.Minute {
			delete(a.limits, key)
		}
	}
	limit := a.limits[ip]
	if limit.Since.IsZero() {
		if len(a.limits) >= 10000 {
			return false
		}
		limit.Since = now
	}
	limit.Count++
	a.limits[ip] = limit
	return limit.Count <= 30
}
func (a *accounts) issueLocked(user *account, previous string) (string, map[string]accountSession) {
	old := a.sessions
	a.sessions = make(map[string]accountSession, len(old)+1)
	for hash, session := range old {
		if session.Expires > time.Now().Unix() && hash != previous {
			a.sessions[hash] = session
		}
	}
	var existing []string
	for hash, session := range a.sessions {
		if session.UserID == user.ID {
			existing = append(existing, hash)
		}
	}
	sort.Slice(existing, func(i, j int) bool { return a.sessions[existing[i]].Expires < a.sessions[existing[j]].Expires })
	for len(existing) >= 8 {
		delete(a.sessions, existing[0])
		existing = existing[1:]
	}
	token := randomHex(32)
	a.sessions[hashKey(token)] = accountSession{UserID: user.ID, Expires: time.Now().Add(sessionLifetime).Unix()}
	return token, old
}
func (a *accounts) register(username, password, legacySID, previous string) (*account, string, error) {
	return a.registerWithIP(username, password, legacySID, previous, "")
}

func (a *accounts) registerWithIP(username, password, legacySID, previous, ip string) (*account, string, error) {
	var err error
	ip, err = canonicalAccountIP(ip)
	if err != nil {
		return nil, "", err
	}
	username = strings.TrimSpace(username)
	if !usernameRE.MatchString(username) {
		return nil, "", &accountError{422, "账号需要 3–32 位英文字母、数字或下划线。"}
	}
	if utf8.RuneCountInString(password) < 8 || len(password) > 72 {
		return nil, "", &accountError{422, "密码至少 8 个字符，且不能超过 72 字节。"}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), a.hashCost)
	if err != nil {
		return nil, "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, user := range a.users {
		if strings.EqualFold(user.Username, username) {
			return nil, "", &accountError{409, "这个账号已注册，请登录或换一个账号。"}
		}
	}
	id := randomHex(16)
	// A legacy SID was the bearer identity for old progress. Adopt it once only;
	// authenticated accounts can never be claimed using another client's SID.
	claimed := false
	for _, user := range a.users {
		if user.LegacySID == legacySID {
			claimed = true
			break
		}
	}
	if sidRE.MatchString(legacySID) && a.legacy[legacySID] && a.users[legacySID] == nil && !claimed {
		id = legacySID
	}
	now := time.Now().Unix()
	user := &account{ID: id, Username: username, PasswordHash: string(hash), CreatedAt: now, RegistrationIP: ip, LastLoginAt: now, LastLoginIP: ip}
	a.users[id] = user
	token, old := a.issueLocked(user, previous)
	if err := a.saveLocked(); err != nil {
		delete(a.users, id)
		a.sessions = old
		return nil, "", err
	}
	copy := *user
	return &copy, token, nil
}
func (a *accounts) login(username, password, previous string) (*account, string, error) {
	return a.loginWithIP(username, password, previous, "")
}

func (a *accounts) loginWithIP(username, password, previous, ip string) (*account, string, error) {
	var err error
	ip, err = canonicalAccountIP(ip)
	if err != nil {
		return nil, "", err
	}
	a.mu.Lock()
	var user *account
	for _, v := range a.users {
		if strings.EqualFold(v.Username, strings.TrimSpace(username)) {
			copy := *v
			user = &copy
			break
		}
	}
	hash := a.dummyHash
	if user != nil {
		hash = []byte(user.PasswordHash)
	}
	a.mu.Unlock()
	err = bcrypt.CompareHashAndPassword(hash, []byte(password))
	if err != nil || user == nil {
		return nil, "", &accountError{401, "账号或密码不正确。"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	oldUser := a.users[user.ID]
	if oldUser == nil || oldUser.Disabled {
		return nil, "", &accountError{403, "这个账号已被停用，请联系账号服务管理员。"}
	}
	updated := *oldUser
	updated.LastLoginAt = time.Now().Unix()
	updated.LastLoginIP = ip
	user = &updated
	a.users[user.ID] = user
	token, old := a.issueLocked(user, previous)
	if err := a.saveLocked(); err != nil {
		a.users[user.ID] = oldUser
		a.sessions = old
		return nil, "", err
	}
	copy := *user
	return &copy, token, nil
}
func (a *accounts) setName(id, name string) (*account, error) {
	name = strings.TrimSpace(name)
	if !validPlayerName(name) {
		return nil, &accountError{422, "玩家名字需要 2–20 个中文、字母、数字、下划线或短横线。"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.users[id]
	if old == nil || old.Disabled {
		return nil, &accountError{401, "请先登录。"}
	}
	for _, user := range a.users {
		if user.ID != id && strings.EqualFold(user.Name, name) {
			return nil, &accountError{409, "这个玩家名字已被使用，请换一个名字。"}
		}
	}
	user := *old
	user.Name = name
	a.users[id] = &user
	if err := a.saveLocked(); err != nil {
		a.users[id] = old
		return nil, err
	}
	return &user, nil
}

// Old anonymous SIDs were bearer credentials. A signed-in account can adopt
// that old identity once, including when it already exists on another device.
// An ID already owned by an authenticated account is never transferable.
func (a *accounts) claimLegacy(id, sid string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	user := a.users[id]
	if user == nil || user.Disabled {
		return "", &accountError{401, "请先登录。"}
	}
	if a.central {
		return user.LegacySID, nil
	}
	if user.LegacySID != "" {
		return user.LegacySID, nil
	}
	if sid == id || !sidRE.MatchString(sid) || !a.legacy[sid] || a.users[sid] != nil {
		return "", nil
	}
	for _, other := range a.users {
		if other.LegacySID == sid {
			return "", nil
		}
	}
	updated := *user
	updated.LegacySID = sid
	a.users[id] = &updated
	if err := a.saveLocked(); err != nil {
		a.users[id] = user
		return "", err
	}
	return sid, nil
}
func cookieHash(req *http.Request) string {
	cookie, err := req.Cookie(accountCookie)
	if err != nil || len(cookie.Value) != 64 {
		return ""
	}
	return hashKey(cookie.Value)
}
func (a *accounts) session(hash string) *account {
	return a.sessionProfile(hash, true)
}
func (a *accounts) sessionProfile(hash string, requireAccess bool) *account {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[hash]
	if !ok || session.Expires <= time.Now().Unix() {
		return nil
	}
	if requireAccess && session.Authenticated != nil && !*session.Authenticated {
		return nil
	}
	user := a.users[session.UserID]
	if user == nil || user.Disabled {
		return nil
	}
	copy := *user
	return &copy
}

func canonicalAccountIP(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return "", &accountError{400, "账号请求来源地址无效。"}
	}
	return ip.String(), nil
}

func accountRequestIP(req *http.Request) string {
	ip, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		ip = req.RemoteAddr
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}
func (a *accounts) logout(hash string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	old, exists := a.sessions[hash]
	if !exists {
		return nil
	}
	delete(a.sessions, hash)
	if err := a.saveLocked(); err != nil {
		a.sessions[hash] = old
		return err
	}
	return nil
}
func (s *Server) setAccountCookie(w http.ResponseWriter, req *http.Request, token string) {
	cookie := &http.Cookie{Name: accountCookie, Value: token, Path: "/", HttpOnly: true, Secure: req.TLS != nil || s.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLifetime.Seconds()), Expires: time.Now().Add(sessionLifetime)}
	if token == "" {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, cookie)
}
func accountJSON(w http.ResponseWriter, req *http.Request, target any) bool {
	if strings.Split(req.Header.Get("Content-Type"), ";")[0] != "application/json" {
		failure(w, 415, "请使用 JSON 请求。")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		failure(w, 400, "请求格式错误或超过 4KB。")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		failure(w, 400, "请求中只能包含一个 JSON 对象。")
		return false
	}
	return true
}
func (s *Server) requireAccount(w http.ResponseWriter, req *http.Request, nameRequired bool) *account {
	user := s.accounts.session(s.requestSessionHash(req))
	if user == nil {
		failure(w, 401, "请先登录，再进入游戏。")
		return nil
	}
	if nameRequired && user.Name == "" {
		failure(w, 409, "请先设置你的玩家名字。")
		return nil
	}
	return user
}
func (s *Server) accountRoutes(mux *http.ServeMux) {
	if s.identity != nil {
		s.centralAccountRoutes(mux)
		return
	}
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, req *http.Request) {
		user := s.accounts.session(cookieHash(req))
		if user == nil {
			writeJSON(w, 200, map[string]any{"user": nil})
			return
		}
		writeJSON(w, 200, map[string]any{"user": user.public()})
	})
	for _, kind := range []string{"register", "login"} {
		mux.HandleFunc("POST /api/auth/"+kind, func(w http.ResponseWriter, req *http.Request) {
			if !s.accounts.allowAttempt(req) {
				w.Header().Set("Retry-After", "600")
				failure(w, 429, "尝试过于频繁，请稍后再试。")
				return
			}
			var body struct {
				Username  string `json:"username"`
				Password  string `json:"password"`
				LegacySID string `json:"legacySid,omitempty"`
			}
			if !accountJSON(w, req, &body) {
				return
			}
			var user *account
			var token string
			var err error
			status := 200
			if kind == "register" {
				status = 201
				user, token, err = s.accounts.registerWithIP(body.Username, body.Password, body.LegacySID, cookieHash(req), accountRequestIP(req))
			} else {
				user, token, err = s.accounts.loginWithIP(body.Username, body.Password, cookieHash(req), accountRequestIP(req))
			}
			if err != nil {
				accountFailure(w, err)
				return
			}
			s.setAccountCookie(w, req, token)
			writeJSON(w, status, map[string]any{"user": user.public()})
		})
	}
	mux.HandleFunc("POST /api/auth/profile", func(w http.ResponseWriter, req *http.Request) {
		user := s.requireAccount(w, req, false)
		if user == nil {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if !accountJSON(w, req, &body) {
			return
		}
		updated, err := s.accounts.setName(user.ID, body.Name)
		if err != nil {
			accountFailure(w, err)
			return
		}
		s.mu.Lock()
		for _, room := range s.rooms {
			for p := range room.Peers {
				if p.sid == updated.ID {
					p.name = updated.Name
					broadcast(room, map[string]any{"t": "who", "p": p.public()}, nil)
				}
			}
		}
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"user": updated.public()})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, req *http.Request) {
		var body struct{}
		if !accountJSON(w, req, &body) {
			return
		}
		hash := cookieHash(req)
		if err := s.accounts.logout(hash); err != nil {
			accountFailure(w, err)
			return
		}
		s.mu.Lock()
		for _, room := range s.rooms {
			for p := range room.Peers {
				if p.sessionHash == hash {
					p.json(map[string]string{"t": "authrequired"})
					p.close(4001)
					s.disconnect(p)
				}
			}
		}
		s.drainActions()
		s.mu.Unlock()
		s.setAccountCookie(w, req, "")
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}

func accountOwns(r *Room, userID, key string) bool {
	if r.OwnerAccount != "" {
		return r.OwnerAccount == userID
	}
	return owns(r, key)
}
