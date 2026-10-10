package identity

import (
	"crypto/ed25519"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"net/url"
	"time"
)

// Joining a private server never transfers the website cookie. A short,
// audience-bound grant is consumed once by that server before issuing a lease.
func (s *Service) clientGrant(w http.ResponseWriter, r *http.Request) {
	u, session := s.session(r)
	if u == nil || u.Name == "" {
		fail(w, 401, "请先登录并设置玩家名字")
		return
	}
	var input struct {
		ServerID string `json:"serverId"`
	}
	if !body(w, r, &input) {
		return
	}
	s.mu.Lock()
	if latest := s.store.Current(session); latest == nil || latest.ID != u.ID || latest.Name != u.Name {
		s.mu.Unlock()
		fail(w, 401, "核心会话已变化，请重试")
		return
	}
	client, ok := s.clients[input.ServerID]
	s.mu.Unlock()
	if !ok {
		fail(w, 404, "这个游戏服务器尚未在核心账号服务注册")
		return
	}
	data := func() map[string]any {
		return map[string]any{"reason": "client_server_join", "actor": u.ID, "target": client.ID, "serverId": client.ID}
	}
	if e := s.emit(api.AccountServerAuthorizing, u, data()); e.Cancelled {
		fail(w, 403, e.Reason)
		return
	}
	current := s.store.Current(session)
	if current == nil || current.ID != u.ID || current.Name == "" {
		fail(w, 401, "核心会话已失效")
		return
	}
	u = current
	now := time.Now()
	grant := Random()
	proof, err := Sign(s.key, Claims{Issuer: s.config.Issuer, Audience: client.ID, Subject: u.ID, IssuedAt: now.Unix(), Expires: now.Add(time.Minute).Unix(), Session: session, Profile: *u, Purpose: "client_join", GrantID: grant})
	if err != nil {
		fail(w, 503, "身份签发失败")
		return
	}
	s.mu.Lock()
	if latest := s.store.Current(session); latest == nil || latest.ID != u.ID || latest.Name != u.Name {
		s.mu.Unlock()
		fail(w, 401, "核心会话已变化，请重试")
		return
	}
	for id, old := range s.clientGrants {
		if !now.Before(old.Expires) {
			delete(s.clientGrants, id)
		}
	}
	if len(s.clientGrants) >= 10000 {
		s.mu.Unlock()
		fail(w, 429, "加入请求过于频繁")
		return
	}
	s.clientGrants[Hash(grant)] = authorization{ClientID: client.ID, Session: session, Expires: now.Add(time.Minute)}
	s.mu.Unlock()
	s.emit(api.AccountServerAuthorized, u, data())
	callback, _ := url.Parse(client.CallbackURL)
	write(w, 200, map[string]any{"proof": proof, "serverId": client.ID, "issuer": s.config.Issuer, "callbackApiOrigin": callback.Scheme + "://" + callback.Host, "expiresIn": 60})
}
func (s *Service) consumeClientGrant(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Proof    string `json:"proof"`
		ClientID string `json:"client_id"`
	}
	if !body(w, r, &input) {
		return
	}
	c, err := Verify(s.key.Public().(ed25519.PublicKey), input.Proof, s.config.Issuer, input.ClientID, time.Now())
	if err != nil || c.Purpose != "client_join" || len(c.GrantID) != 64 {
		fail(w, 401, "加入凭证无效或已过期")
		return
	}
	u := s.store.Current(c.Session)
	if u == nil || u.ID != c.Subject || u.Name == "" {
		fail(w, 401, "核心会话已注销")
		return
	}
	s.mu.Lock()
	_, registered := s.clients[input.ClientID]
	grant, exists := s.clientGrants[Hash(c.GrantID)]
	if latest := s.store.Current(c.Session); latest == nil || latest.ID != u.ID || latest.Name != u.Name {
		s.mu.Unlock()
		fail(w, 401, "核心会话已变化，请重试")
		return
	}
	if !registered || !exists || grant.ClientID != input.ClientID || grant.Session != c.Session || !time.Now().Before(grant.Expires) {
		s.mu.Unlock()
		fail(w, 401, "加入凭证已使用或已过期")
		return
	}
	delete(s.clientGrants, Hash(c.GrantID))
	s.mu.Unlock()
	s.emit(api.AccountServerAuthorizationConsumed, u, map[string]any{"reason": "client_server_join", "actor": u.ID, "target": input.ClientID, "serverId": input.ClientID})
	s.proof(w, u, input.ClientID, c.Session)
}
