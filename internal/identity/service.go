package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mcraft/backend/internal/plugins"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var web embed.FS

const Cookie = "mcraft_identity"

type Config struct {
	DataDir, Issuer, PluginDir       string
	SecureCookies, AllowLegacyCookie bool
	Clients                          []Client
	AdminUsers, TrustedProxies       []string
	GameHomeURL                      string
}
type authorization struct {
	ClientID, Redirect, Challenge, Session string
	Expires                                time.Time
}
type Service struct {
	mu           sync.Mutex
	eventMu      sync.Mutex
	config       Config
	store        Store
	key          ed25519.PrivateKey
	clients      map[string]Client
	codes        map[string]authorization
	clientGrants map[string]authorization
	plugins      *plugins.Manager
	eventID      uint64
	adminMu      sync.Mutex
	admin        *adminState
	presence     *presenceState
}

func New(config Config, store Store) (*Service, error) {
	u, err := WebURL(config.Issuer)
	if err != nil || u.Path != "" && u.Path != "/" {
		return nil, errors.New("invalid core issuer URL")
	}
	config.Issuer = strings.TrimRight(config.Issuer, "/")
	s := &Service{config: config, store: store, clients: map[string]Client{}, codes: map[string]authorization{}, clientGrants: map[string]authorization{}}
	file := filepath.Join(config.DataDir, "signing-key.json")
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		seed := make([]byte, ed25519.SeedSize)
		if _, err = rand.Read(seed); err != nil {
			return nil, err
		}
		raw, _ = json.Marshal(map[string]string{"seed": base64.RawURLEncoding.EncodeToString(seed)})
		if err = atomic(file, raw); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var saved struct {
		Seed string `json:"seed"`
	}
	if json.Unmarshal(raw, &saved) != nil {
		return nil, errors.New("invalid signing key")
	}
	seed, err := base64.RawURLEncoding.DecodeString(saved.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid signing key")
	}
	s.key = ed25519.NewKeyFromSeed(seed)
	file = filepath.Join(config.DataDir, "servers.json")
	if raw, err = os.ReadFile(file); err == nil {
		if json.Unmarshal(raw, &s.clients) != nil || s.clients == nil {
			return nil, errors.New("invalid server registry")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for id, c := range s.clients {
		if id != c.ID || validateClient(c) != nil {
			return nil, errors.New("invalid registered server")
		}
	}
	for _, c := range config.Clients {
		if err = validateClient(c); err != nil {
			return nil, err
		}
		if old, ok := s.clients[c.ID]; ok && old != c {
			return nil, errors.New("bootstrap server conflicts with existing registry")
		}
		s.clients[c.ID] = c
	}
	if err = s.saveClients(); err != nil {
		return nil, err
	}
	s.plugins = plugins.LoadFiltered(config.PluginDir, filepath.Join(config.DataDir, "plugins"), time.Second, nil)
	if err = s.initAdmin(); err != nil {
		s.plugins.Close()
		return nil, err
	}
	if err = s.initPresence(); err != nil {
		s.plugins.Close()
		return nil, err
	}
	s.emit(api.ServerStarted, nil, map[string]any{"service": "mcraft-account", "issuer": config.Issuer, "reason": "core_start"})
	return s, nil
}
func atomic(file string, raw []byte) error {
	f, err := os.OpenFile(file+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
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
	return os.Rename(file+".tmp", file)
}
func validateClient(c Client) error {
	if !ServerPattern.MatchString(c.ID) || strings.TrimSpace(c.Name) == "" || len([]rune(c.Name)) > 64 {
		return errors.New("invalid server ID or name")
	}
	front, e := WebURL(c.FrontendURL)
	if e != nil {
		return e
	}
	callback, e := WebURL(c.CallbackURL)
	if e != nil {
		return e
	}
	if front.Path != "" && front.Path != "/" || callback.Path != "/api/auth/callback" {
		return errors.New("游戏网址需为首页，回调地址需为 /api/auth/callback")
	}
	return nil
}
func (s *Service) saveClients() error {
	raw, err := json.Marshal(s.clients)
	if err != nil {
		return err
	}
	return atomic(filepath.Join(s.config.DataDir, "servers.json"), raw)
}
func (s *Service) Close() error {
	s.closePresence()
	s.emit(api.ServerStopping, nil, map[string]any{"service": "mcraft-account", "issuer": s.config.Issuer, "reason": "core_stop"})
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	s.plugins.Close()
	return s.store.Close()
}
func (s *Service) emit(name string, user *Profile, data map[string]any) *api.Event {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	s.mu.Lock()
	s.eventID++
	id := s.eventID
	s.mu.Unlock()
	e := &api.Event{ID: id, Name: name, Time: time.Now().UTC().Format(time.RFC3339Nano), Data: data}
	if user != nil {
		if e.Data == nil {
			e.Data = map[string]any{}
		}
		if _, ok := e.Data["actor"]; !ok {
			e.Data["actor"] = user.ID
		}
		if _, ok := e.Data["target"]; !ok {
			e.Data["target"] = user.ID
		}
		e.Identity = &api.Identity{ID: user.ID, Username: user.Username, Name: user.Name, Issuer: s.config.Issuer}
	}
	s.plugins.Emit(e)
	if e.Cancellable && s.plugins.Failed() > 0 {
		e.Cancel("账号插件不可用，操作已阻止")
	}
	return e
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, text string) {
	write(w, status, map[string]string{"error": text})
}
func storeError(w http.ResponseWriter, err error) {
	var status interface{ StatusCode() int }
	if errors.As(err, &status) {
		fail(w, status.StatusCode(), err.Error())
	} else {
		fail(w, 503, "账号数据保存失败，请稍后重试")
	}
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "需要 JSON 请求")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "请求格式无效")
		return false
	}
	return true
}
func public(p *Profile) any {
	if p == nil {
		return nil
	}
	return map[string]string{"id": p.ID, "username": p.Username, "name": p.Name}
}
func (s *Service) session(r *http.Request) (*Profile, string) {
	c, err := r.Cookie(Cookie)
	if err != nil && s.config.AllowLegacyCookie {
		c, err = r.Cookie("mcraft_session")
	}
	if err != nil || len(c.Value) != 64 {
		return nil, ""
	}
	hash := Hash(c.Value)
	return s.store.Current(hash), hash
}
func (s *Service) cookie(w http.ResponseWriter, r *http.Request, token string) {
	c := &http.Cookie{Name: Cookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.config.SecureCookies || r.TLS != nil, MaxAge: 30 * 86400}
	if token == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}
func (s *Service) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		s.eventMu.Lock()
		failed := s.plugins.Failed()
		s.eventMu.Unlock()
		status, code := "ok", 200
		if failed > 0 {
			status, code = "degraded", 503
		}
		write(w, code, map[string]any{"status": status, "service": "mcraft-account", "issuer": s.config.Issuer, "failedPlugins": failed})
	})
	m.HandleFunc("GET /.well-known/mcraft-identity", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"issuer": s.config.Issuer, "publicKey": base64.RawURLEncoding.EncodeToString(s.key.Public().(ed25519.PublicKey)), "protocol": "mcraft-account-v1"})
	})
	m.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"apiVersion": api.APIVersion, "events": api.Events()})
	})
	m.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		u, _ := s.session(r)
		write(w, 200, map[string]any{"user": public(u)})
	})
	s.registerPresenceRoutes(m)
	s.registerAdminRoutes(m)
	m.HandleFunc("GET /api/launcher", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"gameHomeUrl": s.config.GameHomeURL})
	})
	for _, kind := range []string{"register", "login"} {
		m.HandleFunc("POST /api/auth/"+kind, func(w http.ResponseWriter, r *http.Request) {
			if !s.allowAccountAttempt(r) {
				fail(w, 429, "尝试过于频繁")
				return
			}
			var input struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if !body(w, r, &input) {
				return
			}
			before, after, status := api.AccountLoggingIn, api.AccountLoggedIn, 200
			if kind == "register" {
				before, after, status = api.AccountRegistering, api.AccountRegistered, 201
			}
			ip := s.clientIP(r)
			e := s.emit(before, nil, map[string]any{"username": input.Username, "ip": ip, "reason": "core_" + kind})
			if e.Cancelled {
				fail(w, 403, e.Reason)
				return
			}
			_, previous := s.session(r)
			var u *Profile
			var token string
			var err error
			if contextStore, ok := s.store.(ContextStore); ok && kind == "register" {
				u, token, err = contextStore.RegisterWithIP(input.Username, input.Password, previous, ip)
			} else if ok {
				u, token, err = contextStore.LoginWithIP(input.Username, input.Password, previous, ip)
			} else if kind == "register" {
				u, token, err = s.store.Register(input.Username, input.Password, previous)
			} else {
				u, token, err = s.store.Login(input.Username, input.Password, previous)
			}
			if err != nil {
				storeError(w, err)
				return
			}
			s.cookie(w, r, token)
			s.emit(after, u, map[string]any{"ip": ip, "reason": "core_" + kind})
			write(w, status, map[string]any{"user": public(u)})
		})
	}
	m.HandleFunc("POST /api/auth/profile", func(w http.ResponseWriter, r *http.Request) {
		u, _ := s.session(r)
		if u == nil {
			fail(w, 401, "请先登录核心账号")
			return
		}
		var input struct {
			Name string `json:"name"`
		}
		if !body(w, r, &input) {
			return
		}
		e := s.emit(api.AccountProfileChanging, u, map[string]any{"name": input.Name, "previousName": u.Name, "reason": "core_profile", "actor": u.ID})
		if e.Cancelled {
			fail(w, 403, e.Reason)
			return
		}
		updated, err := s.store.SetName(u.ID, e.String("name"))
		if err != nil {
			storeError(w, err)
			return
		}
		s.emit(api.AccountProfileChanged, updated, map[string]any{"reason": "core_profile", "actor": u.ID, "previousName": u.Name, "name": updated.Name})
		write(w, 200, map[string]any{"user": public(updated)})
	})
	m.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !body(w, r, &input) {
			return
		}
		u, hash := s.session(r)
		if u != nil {
			e := s.emit(api.AccountLoggingOut, u, map[string]any{"reason": "core_logout", "actor": u.ID})
			if e.Cancelled {
				fail(w, 403, e.Reason)
				return
			}
			if err := s.store.Logout(hash); err != nil {
				storeError(w, err)
				return
			}
			s.emit(api.AccountLoggedOut, u, map[string]any{"reason": "core_logout", "actor": u.ID})
			s.expirePresence("core_logout")
		}
		s.cookie(w, r, "")
		write(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/servers", func(w http.ResponseWriter, r *http.Request) {
		u, _ := s.session(r)
		if u == nil {
			fail(w, 401, "请先登录")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		rows := []Client{}
		for _, c := range s.clients {
			if c.OwnerID == u.ID {
				rows = append(rows, c)
			}
		}
		write(w, 200, map[string]any{"servers": rows})
	})
	m.HandleFunc("POST /api/servers", s.registerClient)
	m.HandleFunc("GET /authorize", s.authorize)
	m.HandleFunc("POST /api/token", s.token)
	m.HandleFunc("POST /api/token/status", s.status)
	m.HandleFunc("POST /api/auth/server-proof", s.clientGrant)
	m.HandleFunc("POST /api/token/client", s.consumeClientGrant)
	m.HandleFunc("GET /account.js", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := web.ReadFile("web/account.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(raw)
	})
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.page(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.config.Issuer {
			fail(w, 403, "账号操作来源无效")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Service) page(w http.ResponseWriter, r *http.Request) {
	raw, _ := web.ReadFile("web/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(raw)
}
func (s *Service) registerClient(w http.ResponseWriter, r *http.Request) {
	u, _ := s.session(r)
	if u == nil || u.Name == "" {
		fail(w, 401, "请先登录并设置名字")
		return
	}
	var c Client
	if !body(w, r, &c) {
		return
	}
	c.ID = Random()[:32]
	c.OwnerID = u.ID
	e := s.emit(api.AccountServerRegistering, u, map[string]any{"reason": "server_register", "actor": u.ID, "target": c.ID, "name": c.Name, "frontendUrl": c.FrontendURL, "callbackUrl": c.CallbackURL})
	if e.Cancelled {
		fail(w, 403, e.Reason)
		return
	}
	c.Name = e.String("name")
	if err := validateClient(c); err != nil {
		fail(w, 422, err.Error())
		return
	}
	s.mu.Lock()
	if len(s.clients) >= 2000 {
		s.mu.Unlock()
		fail(w, 409, "服务器注册已达上限")
		return
	}
	for _, old := range s.clients {
		if old.CallbackURL == c.CallbackURL {
			s.mu.Unlock()
			fail(w, 409, "这个回调地址已注册")
			return
		}
	}
	s.clients[c.ID] = c
	if err := s.saveClients(); err != nil {
		delete(s.clients, c.ID)
		s.mu.Unlock()
		fail(w, 503, "服务器注册保存失败")
		return
	}
	s.mu.Unlock()
	s.emit(api.AccountServerRegistered, u, map[string]any{"reason": "server_register", "actor": u.ID, "target": c.ID, "serverId": c.ID, "name": c.Name})
	write(w, 201, map[string]any{"server": c, "issuer": s.config.Issuer, "publicKey": base64.RawURLEncoding.EncodeToString(s.key.Public().(ed25519.PublicKey))})
}
func (s *Service) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	client, ok := s.clients[q.Get("client_id")]
	s.mu.Unlock()
	if !ok || q.Get("response_type") != "code" || q.Get("redirect_uri") != client.CallbackURL || q.Get("code_challenge_method") != "S256" || !pkcePattern.MatchString(q.Get("code_challenge")) || len(q.Get("state")) < 32 || len(q.Get("state")) > 128 {
		fail(w, 400, "无效的游戏服务器授权请求")
		return
	}
	u, session := s.session(r)
	if u == nil || u.Name == "" || q.Get("profile") == "1" {
		s.page(w, r)
		return
	}
	code := Random()
	s.mu.Lock()
	for key, c := range s.codes {
		if time.Now().After(c.Expires) {
			delete(s.codes, key)
		}
	}
	if len(s.codes) >= 10000 {
		s.mu.Unlock()
		fail(w, 429, "授权请求过多")
		return
	}
	s.codes[Hash(code)] = authorization{client.ID, client.CallbackURL, q.Get("code_challenge"), session, time.Now().Add(time.Minute)}
	s.mu.Unlock()
	target, _ := url.Parse(client.CallbackURL)
	p := target.Query()
	p.Set("code", code)
	p.Set("state", q.Get("state"))
	target.RawQuery = p.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}
func (s *Service) token(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
		ClientID string `json:"client_id"`
		Redirect string `json:"redirect_uri"`
	}
	if !body(w, r, &input) {
		return
	}
	s.mu.Lock()
	code, ok := s.codes[Hash(input.Code)]
	if ok && code.ClientID == input.ClientID && code.Redirect == input.Redirect && time.Now().Before(code.Expires) && verifierPattern.MatchString(input.Verifier) && subtle.ConstantTimeCompare([]byte(code.Challenge), []byte(Challenge(input.Verifier))) == 1 {
		delete(s.codes, Hash(input.Code))
	} else {
		s.mu.Unlock()
		fail(w, 401, "授权码无效、已过期或 PKCE 校验失败")
		return
	}
	s.mu.Unlock()
	u := s.store.Current(code.Session)
	if u == nil || u.Name == "" {
		fail(w, 401, "核心会话已失效")
		return
	}
	s.proof(w, u, input.ClientID, code.Session)
}
func (s *Service) proof(w http.ResponseWriter, u *Profile, client, session string) {
	now := time.Now()
	token, err := Sign(s.key, Claims{Issuer: s.config.Issuer, Audience: client, Subject: u.ID, IssuedAt: now.Unix(), Expires: now.Add(10 * time.Minute).Unix(), Session: session, Profile: *u})
	if err != nil {
		fail(w, 503, "身份签发失败")
		return
	}
	write(w, 200, map[string]any{"proof": token, "expiresIn": 600})
}
func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Proof    string `json:"proof"`
		ClientID string `json:"client_id"`
	}
	if !body(w, r, &input) {
		return
	}
	s.mu.Lock()
	_, registered := s.clients[input.ClientID]
	s.mu.Unlock()
	if !registered {
		fail(w, 401, "未知服务器")
		return
	}
	c, err := Verify(s.key.Public().(ed25519.PublicKey), input.Proof, s.config.Issuer, input.ClientID, time.Now())
	if err != nil || c.Purpose != "" {
		fail(w, 401, "身份凭据失效")
		return
	}
	u := s.store.Current(c.Session)
	if u == nil || u.ID != c.Subject || u.Name == "" {
		fail(w, 401, "核心会话已注销")
		return
	}
	s.proof(w, u, input.ClientID, c.Session)
}
