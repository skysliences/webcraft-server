package game

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mcraft/backend/internal/identity"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type identityFlow struct {
	Verifier, ReturnURL string
	Expires             time.Time
}
type identityClient struct {
	issuer, serverID, callback, frontend, cookie string
	key                                          ed25519.PublicKey
	http                                         *http.Client
	mu                                           sync.Mutex
	flows                                        map[string]identityFlow
}

func newIdentityClient(c Config) (*identityClient, error) {
	issuer, err := identity.WebURL(c.IdentityIssuer)
	if err != nil || issuer.Path != "" && issuer.Path != "/" {
		return nil, errors.New("AUTH_ISSUER_URL must be an HTTPS origin (HTTP permitted on loopback)")
	}
	callback, err := identity.WebURL(c.IdentityCallback)
	if err != nil || callback.Path != "/api/auth/callback" {
		return nil, errors.New("AUTH_CALLBACK_URL must end in /api/auth/callback")
	}
	front, err := identity.WebURL(c.GamePublicURL)
	if err != nil || front.Path != "" && front.Path != "/" {
		return nil, errors.New("GAME_PUBLIC_URL must be a frontend origin")
	}
	if !identity.ServerPattern.MatchString(c.IdentityServerID) {
		return nil, errors.New("invalid AUTH_SERVER_ID")
	}
	client := &identityClient{issuer: strings.TrimRight(c.IdentityIssuer, "/"), serverID: c.IdentityServerID, callback: callback.String(), frontend: strings.TrimRight(front.String(), "/"), cookie: "mcraft_game_" + identity.Hash(c.IdentityServerID)[:16], flows: map[string]identityFlow{}, http: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if c.IdentityPublicKey != "" {
		client.key, err = base64.RawURLEncoding.DecodeString(c.IdentityPublicKey)
	} else {
		var meta struct{ Issuer, PublicKey, Protocol string }
		_, err = client.call("GET", "/.well-known/mcraft-identity", nil, &meta)
		if err == nil {
			if meta.Issuer != client.issuer || meta.Protocol != "mcraft-account-v1" {
				return nil, errors.New("core identity metadata mismatch")
			}
			client.key, err = base64.RawURLEncoding.DecodeString(meta.PublicKey)
		}
	}
	if err != nil || len(client.key) != ed25519.PublicKeySize {
		return nil, errors.New("core signing key unavailable or invalid; configure AUTH_PUBLIC_KEY or start the core account service")
	}
	return client, nil
}
func (c *identityClient) call(method, path string, body, out any) (int, error) {
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		input = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.issuer+path, input)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("核心账号服务暂时不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return resp.StatusCode, errors.New("核心身份验证失败，请重新登录")
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(out); err != nil {
		return resp.StatusCode, errors.New("核心账号响应无效")
	}
	return resp.StatusCode, nil
}
func (c *identityClient) verify(proof string) (identity.Claims, error) {
	claim, err := identity.Verify(c.key, proof, c.issuer, c.serverID, time.Now())
	if err != nil {
		return identity.Claims{}, err
	}
	if err == nil && (!usernameRE.MatchString(claim.Profile.Username) || !validPlayerName(claim.Profile.Name) || claim.Profile.LegacySID != "" && !sidRE.MatchString(claim.Profile.LegacySID)) {
		err = errors.New("invalid signed profile")
	}
	return *claim, err
}

type federatedDisk struct {
	Version  int                       `json:"version"`
	Issuer   string                    `json:"issuer"`
	ServerID string                    `json:"serverId"`
	Users    []identity.Profile        `json:"users"`
	Sessions map[string]accountSession `json:"sessions"`
}

func newFederatedAccounts(dir string, c *identityClient) (*accounts, error) {
	a := &accounts{central: true, issuer: c.issuer, serverID: c.serverID, file: filepath.Join(dir, "federation.json"), users: map[string]*account{}, sessions: map[string]accountSession{}, limits: map[string]loginLimit{}, legacy: map[string]bool{}}
	raw, err := os.ReadFile(a.file)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	var disk federatedDisk
	if json.Unmarshal(raw, &disk) != nil || disk.Version != 1 || disk.Issuer != c.issuer || disk.ServerID != c.serverID {
		return nil, errors.New("federation.json is invalid or belongs to a different authority/server; original file preserved")
	}
	for _, p := range disk.Users {
		if !sidRE.MatchString(p.ID) || !usernameRE.MatchString(p.Username) || !validPlayerName(p.Name) || a.users[p.ID] != nil {
			return nil, errors.New("invalid federated profile")
		}
		a.users[p.ID] = profileAccount(p)
	}
	for hash, session := range disk.Sessions {
		claims, err := c.verify(session.Proof)
		if err != nil || claims.Subject != session.UserID || len(hash) != 64 || session.Authenticated == nil {
			continue
		}
		if _, err = validatedAccess(map[string]any{"authenticated": *session.Authenticated, "prompt": session.Prompt, "fields": session.Fields}); err != nil {
			return nil, err
		}
		session.Expires = claims.Expires
		a.sessions[hash] = session
		a.users[claims.Subject] = profileAccount(claims.Profile)
	}
	return a, nil
}
func profileAccount(p identity.Profile) *account {
	return &account{ID: p.ID, Username: p.Username, Name: p.Name, LegacySID: p.LegacySID}
}

// Central game servers persist only public profiles and server-bound signed leases.
func (a *accounts) saveFederatedLocked() error {
	var prior federatedDisk
	// The authority binding is immutable and supplied at account construction.
	prior.Version = 1
	prior.Issuer = a.issuer
	prior.ServerID = a.serverID
	prior.Sessions = a.sessions
	for _, u := range a.users {
		prior.Users = append(prior.Users, identity.Profile{ID: u.ID, Username: u.Username, Name: u.Name, LegacySID: u.LegacySID})
	}
	raw, err := json.Marshal(prior)
	if err != nil {
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
func (s *Server) requestSessionHash(r *http.Request) string {
	if value := r.Header.Get("Authorization"); value != "" {
		if strings.HasPrefix(value, "Bearer ") && validClientToken(strings.TrimPrefix(value, "Bearer ")) {
			return identity.Hash(strings.TrimPrefix(value, "Bearer "))
		}
		return ""
	}
	if r.Header.Get("Upgrade") != "" {
		for _, value := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			value = strings.TrimSpace(value)
			if strings.HasPrefix(value, "auth.") {
				if validClientToken(value[5:]) {
					return identity.Hash(value[5:])
				}
				return ""
			}
		}
	}
	if s.identity == nil {
		return cookieHash(r)
	}
	c, err := r.Cookie(s.identity.cookie)
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	return identity.Hash(c.Value)
}
func (s *Server) federationCookie(w http.ResponseWriter, r *http.Request, name, token string, age int) {
	c := &http.Cookie{Name: name, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.config.SecureCookies || r.TLS != nil, MaxAge: age}
	if token == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}
func (s *Server) identityEvent(name string, u *account, data map[string]any) *api.Event {
	e := s.event(name, nil, nil, data)
	e.Identity = &api.Identity{ID: u.ID, Username: u.Username, Name: u.Name, Issuer: s.identity.issuer}
	if s.plugins != nil {
		s.queueActions(s.plugins.Emit(e))
		if e.Cancellable && s.plugins.Failed() > 0 {
			e.Cancel("服务器插件不可用，准入操作已阻止")
		}
	}
	return e
}

type serverAccess struct {
	Authenticated bool            `json:"authenticated"`
	Prompt        string          `json:"prompt"`
	Fields        []api.AuthField `json:"fields"`
}

var authFieldPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

func validatedAccess(data map[string]any) (serverAccess, error) {
	var a serverAccess
	var ok bool
	a.Authenticated, ok = data["authenticated"].(bool)
	if !ok {
		return a, errors.New("插件 authenticated 字段必须是布尔值")
	}
	a.Prompt, ok = data["prompt"].(string)
	if !ok || utf8.RuneCountInString(a.Prompt) > 240 {
		return a, errors.New("插件准入提示无效")
	}
	raw, err := json.Marshal(data["fields"])
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(raw, &a.Fields); err != nil || len(a.Fields) > 8 {
		return a, errors.New("插件准入字段无效")
	}
	used := map[string]bool{}
	for _, f := range a.Fields {
		if !authFieldPattern.MatchString(f.Name) || used[f.Name] || strings.TrimSpace(f.Label) == "" || utf8.RuneCountInString(f.Label) > 64 || f.Type != "text" && f.Type != "password" {
			return a, errors.New("插件准入字段名称、类型或标签无效")
		}
		used[f.Name] = true
	}
	return a, nil
}
func accessOf(session accountSession) serverAccess {
	a := serverAccess{Authenticated: session.Authenticated != nil && *session.Authenticated, Prompt: session.Prompt, Fields: session.Fields}
	if a.Fields == nil {
		a.Fields = []api.AuthField{}
	}
	return a
}
func (s *Server) accessEvent(u *account, action string, values map[string]string) (serverAccess, error) {
	reason := "core_login"
	if action != "check" {
		reason = "server_" + action
	}
	e := s.identityEvent(api.PlayerAuthenticating, u, map[string]any{"reason": reason, "action": action, "actor": u.ID, "target": u.ID, "authenticated": action == "check", "prompt": "", "fields": []api.AuthField{}, "values": values})
	if e.Cancelled {
		return serverAccess{}, &accountError{403, limitText(e.Reason, 240)}
	}
	a, err := validatedAccess(e.Data)
	if err != nil {
		return a, &accountError{422, err.Error()}
	}
	return a, nil
}
func (s *Server) accessCompleted(u *account, a serverAccess, action string) {
	name := api.PlayerAuthenticationRequired
	if a.Authenticated {
		name = api.PlayerAuthenticated
	}
	reason := "core_login"
	if action != "check" {
		reason = "server_" + action
	}
	s.identityEvent(name, u, map[string]any{"reason": reason, "action": action, "actor": u.ID, "target": u.ID, "authenticated": a.Authenticated, "prompt": a.Prompt, "fields": a.Fields})
}
func (s *Server) centralAccountRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/auth/client", s.clientIdentity)
	m.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		hash := s.requestSessionHash(r)
		u := s.accounts.sessionProfile(hash, false)
		auth := map[string]any{"mode": "central", "issuer": s.identity.issuer, "frontendURL": s.identity.frontend, "loginURL": s.identity.callback[:len(s.identity.callback)-len("callback")] + "start"}
		if u == nil {
			writeJSON(w, 200, map[string]any{"user": nil, "auth": auth})
			return
		}
		s.accounts.mu.Lock()
		a := accessOf(s.accounts.sessions[hash])
		s.accounts.mu.Unlock()
		writeJSON(w, 200, map[string]any{"user": u.public(), "auth": auth, "access": a})
	})
	m.HandleFunc("GET /api/auth/start", s.beginIdentity)
	m.HandleFunc("GET /api/auth/callback", s.finishIdentity)
	for _, route := range []string{"login", "register", "profile"} {
		m.HandleFunc("POST /api/auth/"+route, func(w http.ResponseWriter, r *http.Request) {
			failure(w, 409, "此服务器使用核心账号，请前往核心账号服务登录或修改资料。")
		})
	}
	m.HandleFunc("POST /api/auth/server-access", s.serverAccess)
	m.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if !accountJSON(w, r, &body) {
			return
		}
		hash := s.requestSessionHash(r)
		s.mu.Lock()
		defer s.mu.Unlock()
		u := s.accounts.sessionProfile(hash, false)
		if u != nil {
			e := s.identityEvent(api.PlayerLoggingOut, u, map[string]any{"reason": "server_logout", "actor": u.ID, "target": u.ID})
			if e.Cancelled {
				failure(w, 403, e.Reason)
				return
			}
		}
		if err := s.accounts.logout(hash); err != nil {
			accountFailure(w, err)
			return
		}
		s.expireIdentityPeers(hash)
		if u != nil {
			s.identityEvent(api.PlayerLoggedOut, u, map[string]any{"reason": "server_logout", "actor": u.ID, "target": u.ID})
		}
		s.drainActions()
		s.federationCookie(w, r, s.identity.cookie, "", -1)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}
func (s *Server) beginIdentity(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	allowed := s.accounts.allowAttempt(r)
	s.mu.Unlock()
	if !allowed {
		failure(w, 429, "登录请求过于频繁")
		return
	}
	target, err := url.Parse(r.URL.Query().Get("return_to"))
	front, _ := url.Parse(s.identity.frontend)
	if err != nil {
		failure(w, 400, "返回地址无效")
		return
	}
	if target.String() == "" {
		target = front
	}
	if !target.IsAbs() {
		target = front.ResolveReference(target)
	}
	if target.Path == "" {
		target.Path = "/"
	}
	if target.Scheme != front.Scheme || target.Host != front.Host || target.User != nil || len(target.String()) > 2048 || target.Path != "/" && !regexp.MustCompile(`^/(r|play)/[A-Za-z0-9_-]+/?$`).MatchString(target.Path) && !regexp.MustCompile(`^/local/[a-z0-9]{16}/?$`).MatchString(target.Path) {
		failure(w, 400, "返回地址必须属于这个游戏服务器")
		return
	}
	state, verifier := identity.Random(), identity.Random()
	c := s.identity
	c.mu.Lock()
	for key, flow := range c.flows {
		if time.Now().After(flow.Expires) {
			delete(c.flows, key)
		}
	}
	if len(c.flows) >= 10000 {
		c.mu.Unlock()
		failure(w, 429, "登录请求过多")
		return
	}
	c.flows[identity.Hash(state)] = identityFlow{verifier, target.String(), time.Now().Add(5 * time.Minute)}
	c.mu.Unlock()
	s.federationCookie(w, r, c.cookie+"_oauth", state, 300)
	q := url.Values{"client_id": {c.serverID}, "redirect_uri": {c.callback}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {identity.Challenge(verifier)}, "state": {state}}
	if r.URL.Query().Get("profile") == "1" {
		q.Set("profile", "1")
	}
	http.Redirect(w, r, c.issuer+"/authorize?"+q.Encode(), http.StatusFound)
}
func (s *Server) finishIdentity(w http.ResponseWriter, r *http.Request) {
	c := s.identity
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(c.cookie + "_oauth")
	if err != nil || len(state) != 64 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		failure(w, 403, "登录状态校验失败，请重新开始登录")
		return
	}
	c.mu.Lock()
	flow, ok := c.flows[identity.Hash(state)]
	delete(c.flows, identity.Hash(state))
	c.mu.Unlock()
	s.federationCookie(w, r, c.cookie+"_oauth", "", -1)
	if !ok || time.Now().After(flow.Expires) {
		failure(w, 401, "登录流程已过期，请重新登录")
		return
	}
	var result struct {
		Proof string `json:"proof"`
	}
	_, err = c.call("POST", "/api/token", map[string]string{"code": r.URL.Query().Get("code"), "code_verifier": flow.Verifier, "client_id": c.serverID, "redirect_uri": c.callback}, &result)
	if err != nil {
		failure(w, 503, err.Error())
		return
	}
	claims, err := c.verify(result.Proof)
	if err != nil {
		failure(w, 401, "核心身份签名校验失败")
		return
	}
	s.mu.Lock()
	_, _, token, err := s.acceptCoreIdentity(r, claims, result.Proof)
	s.mu.Unlock()
	if err != nil {
		accountFailure(w, err)
		return
	}
	s.federationCookie(w, r, c.cookie, token, int(sessionLifetime.Seconds()))
	http.Redirect(w, r, flow.ReturnURL, http.StatusFound)
}
func (s *Server) serverAccess(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	attemptAllowed := s.accounts.allowAttempt(r)
	s.mu.Unlock()
	if !attemptAllowed {
		failure(w, 429, "验证请求过于频繁")
		return
	}
	var input struct {
		Action string            `json:"action"`
		Values map[string]string `json:"values"`
	}
	if !accountJSON(w, r, &input) {
		return
	}
	if input.Action != "register" && input.Action != "login" {
		failure(w, 422, "只能执行本服注册或登录")
		return
	}
	hash := s.requestSessionHash(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.accounts
	u := a.sessionProfile(hash, false)
	if u == nil {
		failure(w, 401, "请先登录核心账号")
		return
	}
	a.mu.Lock()
	old := a.sessions[hash]
	a.mu.Unlock()
	if old.Authenticated != nil && *old.Authenticated {
		failure(w, 409, "已经通过本服验证")
		return
	}
	allowed := map[string]bool{}
	for _, f := range old.Fields {
		allowed[f.Name] = true
	}
	for name, v := range input.Values {
		if !allowed[name] || utf8.RuneCountInString(v) > 256 {
			failure(w, 422, "本服验证字段无效")
			return
		}
	}
	access, err := s.accessEvent(u, input.Action, input.Values)
	if err != nil {
		accountFailure(w, err)
		return
	}
	a.mu.Lock()
	current, exists := a.sessions[hash]
	if !exists || current.Proof != old.Proof || current.Expires <= time.Now().Unix() {
		a.mu.Unlock()
		failure(w, 409, "会话已变化，请重新登录")
		return
	}
	current.Authenticated = &access.Authenticated
	current.Prompt = access.Prompt
	current.Fields = access.Fields
	a.sessions[hash] = current
	if err = a.saveLocked(); err != nil {
		a.sessions[hash] = old
		a.mu.Unlock()
		accountFailure(w, err)
		return
	}
	a.mu.Unlock()
	s.accessCompleted(u, access, input.Action)
	s.drainActions()
	writeJSON(w, 200, map[string]any{"user": u.public(), "access": access})
}
func (s *Server) expireIdentityPeers(hash string) {
	for _, room := range s.allRooms() {
		for p := range room.Peers {
			if p.sessionHash == hash {
				p.json(map[string]string{"t": "authrequired"})
				p.close(4001)
				s.disconnect(p)
			}
		}
	}
}
func (s *Server) refreshIdentities() {
	defer close(s.identityDone)
	interval := s.config.IdentityCheckInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		a := s.accounts
		s.mu.Unlock()
		a.mu.Lock()
		sessions := make(map[string]accountSession, len(a.sessions))
		for hash, v := range a.sessions {
			sessions[hash] = v
		}
		a.mu.Unlock()
		for hash, old := range sessions {
			select {
			case <-s.stop:
				return
			default:
			}
			var response struct {
				Proof string `json:"proof"`
			}
			status, err := s.identity.call("POST", "/api/token/status", map[string]string{"proof": old.Proof, "client_id": s.identity.serverID}, &response)
			expired := old.Expires <= time.Now().Unix() || status == 401
			if err != nil && !expired {
				continue
			}
			var claims identity.Claims
			if !expired {
				claims, err = s.identity.verify(response.Proof)
				if err != nil || claims.Subject != old.UserID {
					continue
				}
			}
			s.mu.Lock()
			if s.accounts != a {
				s.mu.Unlock()
				continue
			}
			a.mu.Lock()
			current, ok := a.sessions[hash]
			if !ok || current.Proof != old.Proof {
				a.mu.Unlock()
				s.mu.Unlock()
				continue
			}
			oldUser := a.users[old.UserID]
			if expired {
				delete(a.sessions, hash)
			} else {
				current.Proof = response.Proof
				current.Expires = claims.Expires
				a.sessions[hash] = current
				a.users[old.UserID] = profileAccount(claims.Profile)
			}
			if err = a.saveLocked(); err != nil {
				a.sessions[hash] = old
				a.users[old.UserID] = oldUser
				a.mu.Unlock()
				s.mu.Unlock()
				continue
			}
			a.mu.Unlock()
			if expired {
				if oldUser != nil {
					reason := "core_expired"
					if status == 401 {
						reason = "core_revoked"
					}
					s.identityEvent(api.PlayerIdentityExpired, oldUser, map[string]any{"reason": reason, "actor": oldUser.ID, "target": oldUser.ID})
				}
				s.expireIdentityPeers(hash)
			} else {
				if oldUser != nil && oldUser.Name != claims.Profile.Name {
					s.identityEvent(api.PlayerIdentityUpdated, profileAccount(claims.Profile), map[string]any{"reason": "core_profile", "actor": old.UserID, "target": old.UserID, "previousName": oldUser.Name, "name": claims.Profile.Name})
				}
				for _, room := range s.allRooms() {
					for p := range room.Peers {
						if p.sid == claims.Subject && p.name != claims.Profile.Name {
							p.name = claims.Profile.Name
							broadcast(room, map[string]any{"t": "who", "p": p.public()}, nil)
						}
					}
				}
			}
			s.drainActions()
			s.mu.Unlock()
		}
	}
}
