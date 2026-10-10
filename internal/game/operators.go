package game

import (
	"encoding/json"
	"sort"
	"strings"

	api "mcraft/backend/pkg/plugin"
)

func (s *Server) isOperator(id string) bool { return s.ops != nil && s.ops.Settings.Operators[id] }
func (s *Server) canGameCommand(p *Peer, permission string) bool {
	return p != nil && (strings.HasPrefix(s.pluginSource, "console:") || s.hasPermission(p.sid, permission) || !s.config.SingleWorld && p.owner)
}
func (s *Server) commandActor(p *Peer) string {
	if strings.HasPrefix(s.pluginSource, "console:") {
		return strings.TrimPrefix(s.pluginSource, "console:")
	}
	if s.pluginSource != "" {
		return s.pluginSource
	}
	if p != nil {
		return p.sid
	}
	return "server"
}
func (s *Server) commandReason() string {
	if strings.HasPrefix(s.pluginSource, "console:") {
		return "console"
	}
	if s.pluginSource != "" {
		return "plugin"
	}
	return "command"
}
func (s *Server) primaryRoom() *Room {
	if s.config.SingleWorld {
		return s.rooms[s.worldBinding.WorldID]
	}
	return s.rooms[DefaultRoom]
}
func (s *Server) primaryWorldID() string {
	if r := s.primaryRoom(); r != nil {
		return r.ID
	}
	return ""
}
func (s *Server) defaultGameMode(r *Room) string {
	if s.config.SingleWorld && s.ops != nil && s.ops.Settings.DefaultGameMode != "" {
		return s.ops.Settings.DefaultGameMode
	}
	if r.Mode == "creative" {
		return "creative"
	}
	return "survival"
}
func (s *Server) copySettings() ServerSettings {
	var next ServerSettings
	raw, _ := json.Marshal(s.ops.Settings)
	json.Unmarshal(raw, &next)
	return next
}
func operatorChanges(previous, next ServerSettings) []string {
	ids := map[string]bool{}
	for id, value := range previous.Operators {
		if value != next.Operators[id] {
			ids[id] = true
		}
	}
	for id, value := range next.Operators {
		if value != previous.Operators[id] {
			ids[id] = true
		}
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	return list
}
func (s *Server) operatorEvent(name, id string, previous, enabled bool, actor, reason string) *api.Event {
	var peer *Peer
	r := s.primaryRoom()
	for _, room := range s.allRooms() {
		for p := range room.Peers {
			if p.sid == id {
				peer, r = p, room
				break
			}
		}
	}
	e := s.event(name, r, peer, map[string]any{"actor": actor, "target": id, "previous": previous, "operator": enabled, "reason": reason})
	e.Identity = &api.Identity{ID: id}
	if s.identity != nil {
		e.Identity.Issuer = s.identity.issuer
	}
	if u := s.accountByName(id); u != nil {
		e.Data["targetName"] = u.Name
		e.Identity = &api.Identity{ID: u.ID, Name: u.Name, Username: u.Username}
		if s.identity != nil {
			e.Identity.Issuer = s.identity.issuer
		}
	}
	if s.plugins != nil {
		s.queueActions(s.plugins.Emit(e))
	}
	return e
}
func (s *Server) sendPermissions(p *Peer) {
	// A temporary console execution context must not grant client privileges.
	can := s.hasPermission(p.sid, "server.gamemode") || !s.config.SingleWorld && p.owner
	p.json(map[string]any{"t": "permissions", "op": s.isOperator(p.sid), "permissions": s.permissionNodes(p.sid), "canGamemode": can})
}

// Console is available to the process owner. HTTP callers must still pass the
// authenticated console endpoint; chat uses its own OP/permission checks.
func (s *Server) Console(command string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.console("local-console", command, "", "")
}
func (s *Server) StopRequested() <-chan struct{} { return s.stopRequested }
