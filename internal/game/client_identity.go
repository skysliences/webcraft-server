package game

import (
	"mcraft/backend/internal/identity"
	api "mcraft/backend/pkg/plugin"
	"net/http"
)

func validClientToken(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (s *Server) clientIdentity(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	allowed := s.accounts.allowAttempt(r)
	s.mu.Unlock()
	if !allowed {
		failure(w, 429, "验证请求过于频繁")
		return
	}
	var input struct {
		Proof string `json:"proof"`
	}
	if !accountJSON(w, r, &input) {
		return
	}
	claims, err := s.identity.verify(input.Proof)
	if err != nil || claims.Purpose != "client_join" {
		failure(w, 401, "加入凭证无效或已过期")
		return
	}
	var result struct {
		Proof string `json:"proof"`
	}
	status, err := s.identity.call("POST", "/api/token/client", map[string]string{"proof": input.Proof, "client_id": s.identity.serverID}, &result)
	if err != nil {
		if status == 401 {
			failure(w, 401, "加入凭证已使用或核心会话已失效")
		} else {
			failure(w, 503, "无法验证核心账号，请稍后重试")
		}
		return
	}
	claims, err = s.identity.verify(result.Proof)
	if err != nil || claims.Purpose != "" {
		failure(w, 401, "核心身份签名校验失败")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, access, token, err := s.acceptCoreIdentity(r, claims, result.Proof)
	if err != nil {
		accountFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"user": user.public(), "access": access, "clientToken": token, "auth": map[string]any{"mode": "central", "issuer": s.identity.issuer, "serverId": s.identity.serverID}})
}

// Caller holds Server.mu. Both PKCE and unified browser clients enter the same
// plugin checks and transactional session persistence; failures emit no success.
func (s *Server) acceptCoreIdentity(r *http.Request, claims identity.Claims, proof string) (*account, serverAccess, string, error) {
	u := profileAccount(claims.Profile)
	e := s.identityEvent(api.PlayerIdentifying, u, map[string]any{"reason": "core_login", "actor": u.ID, "target": u.ID})
	if e.Cancelled {
		return nil, serverAccess{}, "", &accountError{403, limitText(e.Reason, 240)}
	}
	access, err := s.accessEvent(u, "check", nil)
	if err != nil {
		return nil, serverAccess{}, "", err
	}
	token := identity.Random()
	hash := identity.Hash(token)
	a := s.accounts
	a.mu.Lock()
	oldUser, oldSession, oldExists := a.users[u.ID], a.sessions[s.requestSessionHash(r)], false
	previous := s.requestSessionHash(r)
	_, oldExists = a.sessions[previous]
	a.users[u.ID] = u
	delete(a.sessions, previous)
	a.sessions[hash] = accountSession{UserID: u.ID, Expires: claims.Expires, Proof: proof, Authenticated: &access.Authenticated, Prompt: access.Prompt, Fields: access.Fields}
	if err = a.saveLocked(); err != nil {
		delete(a.sessions, hash)
		if oldUser == nil {
			delete(a.users, u.ID)
		} else {
			a.users[u.ID] = oldUser
		}
		if oldExists {
			a.sessions[previous] = oldSession
		}
		a.mu.Unlock()
		return nil, serverAccess{}, "", err
	}
	a.mu.Unlock()
	s.identityEvent(api.PlayerIdentified, u, map[string]any{"reason": "core_login", "actor": u.ID, "target": u.ID})
	s.accessCompleted(u, access, "check")
	s.drainActions()
	return u, access, token, nil
}
