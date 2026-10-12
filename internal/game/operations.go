package game

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mcraft/backend/internal/plugins"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"
)

type ServerBan struct {
	Reason string `json:"reason"`
	Until  int64  `json:"until,omitempty"`
}
type RoleGroup struct {
	ID       string   `json:"id"`
	Inherits []string `json:"inherits,omitempty"`
	Allow    []string `json:"allow,omitempty"`
	Deny     []string `json:"deny,omitempty"`
}
type PermissionGrant struct {
	Role   string   `json:"role,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Allow  []string `json:"allow,omitempty"`
	Deny   []string `json:"deny,omitempty"`
}
type ServerSettings struct {
	DefaultGameMode    string                     `json:"defaultGameMode,omitempty"`
	Revision           uint64                     `json:"revision"`
	Version            int                        `json:"version"`
	Operators          map[string]bool            `json:"operators"`
	WhitelistEnabled   bool                       `json:"whitelistEnabled"`
	Whitelist          map[string]bool            `json:"whitelist"`
	Bans               map[string]ServerBan       `json:"bans"`
	Permissions        map[string]PermissionGrant `json:"permissions"`
	Groups             map[string]RoleGroup       `json:"groups,omitempty"`
	Maintenance        bool                       `json:"maintenance"`
	MaintenanceMessage string                     `json:"maintenanceMessage"`
	AutosaveSeconds    int                        `json:"autosaveSeconds"`
	BackupMinutes      int                        `json:"backupMinutes"`
	BackupRetention    int                        `json:"backupRetention"`
	DisabledPlugins    map[string]bool            `json:"disabledPlugins"`
}
type auditEntry struct {
	Time    string `json:"time"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Detail  string `json:"detail"`
}
type operations struct {
	Settings   ServerSettings
	Key        string
	Audit      []auditEntry
	TickMillis []float64
	TickTimes  []time.Time
	LastSave   time.Time
	LastBackup time.Time
}

func (s *Server) initOperations() error {
	o := &operations{Settings: ServerSettings{Version: 1, Operators: map[string]bool{}, Whitelist: map[string]bool{}, Bans: map[string]ServerBan{}, Permissions: map[string]PermissionGrant{}, DisabledPlugins: map[string]bool{}, AutosaveSeconds: 5, BackupMinutes: 15, BackupRetention: 7, MaintenanceMessage: "服务器维护中，请稍后再来。"}, Audit: []auditEntry{}, LastSave: time.Now(), LastBackup: time.Now()}
	file := filepath.Join(s.config.DataDir, "server.json")
	data, err := os.ReadFile(file)
	if err == nil {
		if err = json.Unmarshal(data, &o.Settings); err != nil {
			return fmt.Errorf("server settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		// Existing local world owners may administer their own installation. A new
		// remote registrant is never automatically promoted.
		if r := s.rooms[DefaultRoom]; r != nil && r.OwnerAccount != "" {
			o.Settings.Operators[r.OwnerAccount] = true
		}
	}
	if err = validateSettings(o.Settings); err != nil {
		return err
	}
	s.ops = o
	if o.Settings.DefaultGameMode == "" {
		if len(data) > 0 {
			if err := backupBeforeMigration(file, "player-modes", 1); err != nil {
				return err
			}
		}
		o.Settings.DefaultGameMode = "survival"
		if r := s.rooms[s.worldBinding.WorldID]; r != nil {
			o.Settings.DefaultGameMode = r.Mode
		}
	}
	keyFile := filepath.Join(s.config.DataDir, "admin.key")
	key, e := os.ReadFile(keyFile)
	if os.IsNotExist(e) {
		key = []byte(randomHex(32))
		e = atomicPrivateFile(keyFile, key)
	}
	if e != nil {
		return e
	}
	if len(strings.TrimSpace(string(key))) != 64 {
		return errors.New("invalid admin.key")
	}
	o.Key = strings.TrimSpace(string(key))
	os.Chmod(keyFile, 0600)
	auditFile, e := os.Open(filepath.Join(s.config.DataDir, "audit.jsonl"))
	if e == nil {
		if stat, e := auditFile.Stat(); e == nil && stat.Size() > 256<<10 {
			auditFile.Seek(-(256 << 10), io.SeekEnd)
		}
		scan := bufio.NewScanner(auditFile)
		for scan.Scan() {
			var entry auditEntry
			if json.Unmarshal(bytes.TrimSpace(scan.Bytes()), &entry) == nil {
				o.Audit = append(o.Audit, entry)
				if len(o.Audit) > 200 {
					o.Audit = o.Audit[1:]
				}
			}
		}
		auditFile.Close()
	}
	return s.saveSettings(o.Settings)
}
func validateSettings(v ServerSettings) error {
	if v.DefaultGameMode != "" && gameModes[v.DefaultGameMode] != v.DefaultGameMode {
		return errors.New("invalid default game mode")
	}
	if v.Version != 1 || v.Operators == nil || v.Whitelist == nil || v.Bans == nil || v.Permissions == nil || v.DisabledPlugins == nil {
		return errors.New("invalid server settings maps/version")
	}
	if v.AutosaveSeconds < 1 || v.AutosaveSeconds > 3600 || v.BackupMinutes < 0 || v.BackupMinutes > 10080 || v.BackupRetention < 1 || v.BackupRetention > 100 || len([]rune(v.MaintenanceMessage)) > 240 {
		return errors.New("invalid save/backup settings")
	}
	validRole := func(r string) bool {
		if r == "" || r == "player" || r == "moderator" || r == "admin" || r == "builder" {
			return true
		}
		if v.Groups != nil && v.Groups[r].ID != "" {
			return true
		}
		return false
	}
	for _, g := range v.Permissions {
		if !validRole(g.Role) {
			return errors.New("unknown permission role")
		}
		for _, grp := range g.Groups {
			if !validRole(grp) {
				return errors.New("unknown permission group")
			}
		}
		for _, p := range append(append([]string{}, g.Allow...), g.Deny...) {
			if len(p) > 100 || p == "" || strings.ContainsAny(p, " \n\r") {
				return errors.New("invalid permission node")
			}
		}
	}
	for id, grp := range v.Groups {
		if id == "" || grp.ID != id {
			return errors.New("invalid role group id")
		}
		for _, p := range append(append([]string{}, grp.Allow...), grp.Deny...) {
			if len(p) > 100 || p == "" || strings.ContainsAny(p, " \n\r") {
				return errors.New("invalid permission node in group")
			}
		}
	}
	return nil
}
func (s *Server) saveSettings(v ServerSettings) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicPrivateFile(filepath.Join(s.config.DataDir, "server.json"), data)
}

var defaultRoleGroups = map[string]RoleGroup{
	"admin": {
		ID:    "admin",
		Allow: []string{"*"},
	},
	"moderator": {
		ID:       "moderator",
		Inherits: []string{"player"},
		Allow:    []string{"server.kick", "server.ban", "server.whitelist", "server.status", "world.tp.*"},
	},
	"builder": {
		ID:       "builder",
		Inherits: []string{"player"},
		Allow:    []string{"world.build.*", "world.break.*", "world.interact.*"},
	},
	"player": {
		ID:    "player",
		Allow: []string{"player.chat", "player.move", "player.interact", "item.*"},
	},
}

func permissionMatches(node, want string) bool {
	if node == "*" || node == want {
		return true
	}
	nParts := strings.Split(node, ".")
	wParts := strings.Split(want, ".")
	for i := 0; i < len(nParts); i++ {
		if nParts[i] == "*" {
			if i == len(nParts)-1 {
				return i <= len(wParts)-1
			}
			if i >= len(wParts) {
				return false
			}
			continue
		}
		if i >= len(wParts) || nParts[i] != wParts[i] {
			return false
		}
	}
	return len(nParts) == len(wParts)
}

func (s *Server) playerRoles(sid string) []string {
	if s.ops == nil {
		return nil
	}
	if s.ops.Settings.Operators[sid] {
		return []string{"admin"}
	}
	g := s.ops.Settings.Permissions[sid]
	roles := map[string]bool{}
	if g.Role != "" {
		roles[g.Role] = true
	}
	for _, grp := range g.Groups {
		if grp != "" {
			roles[grp] = true
		}
	}
	if len(roles) == 0 {
		return []string{"player"}
	}
	out := make([]string, 0, len(roles))
	for r := range roles {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (s *Server) permissionNodes(sid string) []string {
	if s.ops == nil {
		return nil
	}
	if s.ops.Settings.Operators[sid] {
		return []string{"*"}
	}
	g := s.ops.Settings.Permissions[sid]

	initialGroups := map[string]bool{}
	if g.Role != "" {
		initialGroups[g.Role] = true
	}
	for _, grp := range g.Groups {
		if grp != "" {
			initialGroups[grp] = true
		}
	}
	if len(initialGroups) == 0 {
		initialGroups["player"] = true
	}

	allGroups := map[string]bool{}
	var resolveGroup func(name string)
	resolveGroup = func(name string) {
		if allGroups[name] {
			return
		}
		allGroups[name] = true
		var inherits []string
		if s.ops.Settings.Groups != nil {
			if grp, ok := s.ops.Settings.Groups[name]; ok {
				inherits = grp.Inherits
			}
		}
		if len(inherits) == 0 {
			if def, ok := defaultRoleGroups[name]; ok {
				inherits = def.Inherits
			}
		}
		for _, parent := range inherits {
			resolveGroup(parent)
		}
	}

	for grp := range initialGroups {
		resolveGroup(grp)
	}

	var allowNodes []string
	var denyNodes []string

	for grpName := range allGroups {
		if s.ops.Settings.Groups != nil {
			if grp, ok := s.ops.Settings.Groups[grpName]; ok {
				allowNodes = append(allowNodes, grp.Allow...)
				for _, d := range grp.Deny {
					denyNodes = append(denyNodes, "-"+d)
				}
			}
		}
		if def, ok := defaultRoleGroups[grpName]; ok {
			allowNodes = append(allowNodes, def.Allow...)
			for _, d := range def.Deny {
				denyNodes = append(denyNodes, "-"+d)
			}
		}
	}

	allowNodes = append(allowNodes, g.Allow...)
	for _, d := range g.Deny {
		denyNodes = append(denyNodes, "-"+d)
	}

	return append(allowNodes, denyNodes...)
}

func (s *Server) hasPermission(sid, node string) bool {
	nodes := s.permissionNodes(sid)
	for _, n := range nodes {
		if strings.HasPrefix(n, "-") && permissionMatches(n[1:], node) {
			return false
		}
	}
	for _, n := range nodes {
		if !strings.HasPrefix(n, "-") && permissionMatches(n, node) {
			return true
		}
	}
	return false
}
func (s *Server) audit(actor, action, detail string, success bool) {
	if s.ops == nil {
		return
	}
	e := auditEntry{time.Now().UTC().Format(time.RFC3339), actor, action, success, limitText(detail, 500)}
	s.ops.Audit = append(s.ops.Audit, e)
	if len(s.ops.Audit) > 200 {
		s.ops.Audit = s.ops.Audit[len(s.ops.Audit)-200:]
	}
	data, _ := json.Marshal(e)
	f, err := os.OpenFile(filepath.Join(s.config.DataDir, "audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		f.Write(append(data, '\n'))
		f.Close()
	}
}
func (s *Server) updateSettings(next ServerSettings, actor, reason string) error {
	if next.DefaultGameMode == "" {
		next.DefaultGameMode = s.ops.Settings.DefaultGameMode
	}
	next.Revision = s.ops.Settings.Revision + 1
	if err := validateSettings(next); err != nil {
		return err
	}
	data := map[string]any{"actor": actor, "reason": reason, "previous": s.ops.Settings, "settings": next}
	if !s.allowed(api.ServerSettingsChanging, nil, nil, data) {
		return errors.New("settings cancelled by plugin")
	}
	changes := operatorChanges(s.ops.Settings, next)
	for _, id := range changes {
		if s.operatorEvent(api.PlayerOperatorChanging, id, s.ops.Settings.Operators[id], next.Operators[id], actor, reason).Cancelled {
			return errors.New("operator change cancelled by plugin")
		}
	}
	if next.DefaultGameMode != s.ops.Settings.DefaultGameMode {
		if !s.allowed(api.ServerDefaultGameModeChanging, s.primaryRoom(), nil, map[string]any{"actor": actor, "reason": reason, "target": s.primaryWorldID(), "previous": s.ops.Settings.DefaultGameMode, "gamemode": next.DefaultGameMode}) {
			return errors.New("default game mode change cancelled by plugin")
		}
	}
	if err := s.saveSettings(next); err != nil {
		return err
	}
	previousMode := s.ops.Settings.DefaultGameMode
	s.ops.Settings = next
	for _, r := range s.allRooms() {
		for p := range r.Peers {
			s.sendPermissions(p)
		}
	}
	s.audit(actor, "settings", reason, true)
	s.emit(api.ServerSettingsChanged, nil, nil, data)
	for _, id := range changes {
		s.operatorEvent(api.PlayerOperatorChanged, id, !next.Operators[id], next.Operators[id], actor, reason)
	}
	if next.DefaultGameMode != previousMode {
		s.emit(api.ServerDefaultGameModeChanged, s.primaryRoom(), nil, map[string]any{"actor": actor, "reason": reason, "target": s.primaryWorldID(), "previous": previousMode, "gamemode": next.DefaultGameMode})
	}
	return nil
}
func (s *Server) requireOperator(req *http.Request, node string) (string, error) {
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if len(token) == 64 && subtle.ConstantTimeCompare([]byte(token), []byte(s.ops.Key)) == 1 {
		return "local-console", nil
	}
	user := s.accounts.session(s.requestSessionHash(req))
	if user == nil {
		return "", errors.New("请先登录管理员账号。")
	}
	if !s.hasPermission(user.ID, node) {
		return "", errors.New("此账号没有服务器管理权限。")
	}
	// Cookie mutations require an allowed, present origin. The local CLI uses
	// a secret bearer instead, so a third-party form cannot issue commands.
	if req.Method != "GET" && (req.Header.Get("Origin") == "" || !s.originAllowed(req.Header.Get("Origin"))) {
		return "", errors.New("管理操作来源校验失败。")
	}
	return user.ID, nil
}
func (s *Server) operationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/status", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, err := s.requireOperator(req, "server.status"); err != nil {
			failure(w, 403, err.Error())
			return
		}
		writeJSON(w, 200, s.operationStatus())
	})
	mux.HandleFunc("GET /api/admin/settings", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, err := s.requireOperator(req, "server.admin"); err != nil {
			failure(w, 403, err.Error())
			return
		}
		writeJSON(w, 200, s.ops.Settings)
	})
	mux.HandleFunc("POST /api/admin/settings", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		actor, err := s.requireOperator(req, "server.admin")
		if err != nil {
			failure(w, 403, err.Error())
			return
		}
		var next ServerSettings
		d := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10))
		d.DisallowUnknownFields()
		if err = d.Decode(&next); err != nil {
			failure(w, 400, err.Error())
			return
		}
		if next.Revision != s.ops.Settings.Revision {
			failure(w, 409, "设置已被其他管理操作更新，请刷新后重试。")
			return
		}
		if err = s.updateSettings(next, actor, "admin_api"); err != nil {
			failure(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/admin/console", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body struct {
			Command string `json:"command"`
			World   string `json:"world"`
			Player  string `json:"player"`
		}
		if !accountJSON(w, req, &body) {
			return
		}
		node := commandPermission(body.Command)
		actor, err := s.requireOperator(req, node)
		if err != nil {
			failure(w, 403, err.Error())
			return
		}
		result, err := s.console(actor, body.Command, body.World, body.Player)
		s.audit(actor, "console", body.Command, err == nil)
		if err != nil {
			failure(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "result": result})
	})
	mux.HandleFunc("GET /api/admin/backups", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, err := s.requireOperator(req, "server.backup"); err != nil {
			failure(w, 403, err.Error())
			return
		}
		writeJSON(w, 200, s.listBackups())
	})
	mux.HandleFunc("GET /api/admin/backups/{name}", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		_, err := s.requireOperator(req, "server.backup")
		s.mu.Unlock()
		if err != nil {
			failure(w, 403, err.Error())
			return
		}
		name := req.PathValue("name")
		if name != filepath.Base(name) || !strings.HasSuffix(name, ".zip") {
			failure(w, 400, "invalid backup name")
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeFile(w, req, filepath.Join(s.config.DataDir, "backups", name))
	})
	mux.HandleFunc("GET /api/admin/audit", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, err := s.requireOperator(req, "server.admin"); err != nil {
			failure(w, 403, err.Error())
			return
		}
		writeJSON(w, 200, s.ops.Audit)
	})
}
func commandPermission(cmd string) string {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(cmd), "/"))
	if len(fields) == 0 {
		return "server.admin"
	}
	switch fields[0] {
	case "op", "deop":
		return "server.op"
	case "gamemode", "gm":
		return "server.gamemode"
	case "defaultgamemode":
		return "server.defaultgamemode"
	case "status", "list":
		return "server.status"
	case "kick":
		return "server.kick"
	case "ban", "pardon":
		return "server.ban"
	case "whitelist":
		return "server.whitelist"
	case "backup", "save-all":
		return "server.backup"
	default:
		return "server.admin"
	}
}
func (s *Server) accountByName(name string) *account {
	s.accounts.mu.Lock()
	defer s.accounts.mu.Unlock()
	for _, u := range s.accounts.users {
		if strings.EqualFold(u.Username, name) || strings.EqualFold(u.Name, name) || u.ID == name {
			v := *u
			return &v
		}
	}
	return nil
}
func (s *Server) console(actor, command, world, player string) (any, error) {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(command), "/"))
	if len(fields) == 0 || len(command) > 1000 {
		return nil, errors.New("请输入有效命令")
	}
	name, args := strings.ToLower(fields[0]), fields[1:]
	fields[0] = name
	data := map[string]any{"actor": actor, "command": command, "reason": "console"}
	if !s.allowed(api.ServerCommandExecuting, nil, nil, data) {
		return nil, errors.New("command cancelled by plugin")
	}
	var next ServerSettings
	raw, _ := json.Marshal(s.ops.Settings)
	json.Unmarshal(raw, &next)
	needUser := func() (*account, error) {
		if len(args) < 1 {
			return nil, errors.New("需要账号或玩家名字")
		}
		u := s.accountByName(args[0])
		if u == nil {
			return nil, errors.New("账号不存在")
		}
		return u, nil
	}
	var result any = "完成"
	switch name {
	case "status", "list":
		result = s.operationStatus()
	case "save-all":
		for _, r := range s.allRooms() {
			r.Dirty = true
			if err := s.saveRoom(r); err != nil {
				return nil, err
			}
		}
	case "backup":
		v, err := s.backup(actor, "manual")
		if err != nil {
			return nil, err
		}
		result = v
	case "anticheat":
		if len(args) == 0 || args[0] == "status" {
			cfg := s.anticheatConfig()
			return map[string]any{
				"enabled":         cfg.Enabled,
				"reachDistance":   cfg.ReachDistance,
				"attackReach":     cfg.AttackReach,
				"maxSpeed":        cfg.MaxSpeed,
				"speedTolerance":  cfg.SpeedTolerance,
				"maxTeleportDist": cfg.MaxTeleportDist,
				"noclipCheck":     cfg.NoclipCheck,
				"flyCheck":        cfg.FlyCheck,
				"maxAirTicks":     cfg.MaxAirTicks,
			}, nil
		}
		if args[0] == "reload" {
			if err := s.reloadAnticheatConfig(); err != nil {
				return nil, err
			}
			return "反作弊配置已从 anticheat.json 重新加载并生效", nil
		}
		if args[0] == "off" || args[0] == "disable" || args[0] == "close" {
			cfg := s.anticheatConfig()
			cfg.Enabled = false
			s.setAnticheatConfig(cfg)
			return "反作弊已关闭", nil
		}
		if args[0] == "on" || args[0] == "enable" || args[0] == "open" {
			cfg := s.anticheatConfig()
			cfg.Enabled = true
			s.setAnticheatConfig(cfg)
			return "反作弊已开启", nil
		}
		if args[0] == "toggle" {
			cfg := s.anticheatConfig()
			cfg.Enabled = !cfg.Enabled
			s.setAnticheatConfig(cfg)
			return fmt.Sprintf("反作弊已切换为：%v", cfg.Enabled), nil
		}
		return nil, errors.New("用法：anticheat [status|reload|toggle|on|off]")
	case "op", "deop":
		if len(args) != 1 {
			return nil, errors.New("op/deop <玩家名字或账号 ID>")
		}
		u := s.accountByName(args[0])
		// A core account UUID may be promoted before its first visit. Names
		// require a profile already known to this game server.
		if u == nil && len(args[0]) == 32 && sidRE.MatchString(args[0]) {
			u = &account{ID: args[0]}
		}
		if u == nil {
			return nil, errors.New("账号不存在；可使用已访问玩家的名字或核心账号 ID")
		}
		if name == "op" {
			next.Operators[u.ID] = true
		} else {
			delete(next.Operators, u.ID)
		}
		if err := s.updateSettings(next, actor, name); err != nil {
			return nil, err
		}
		result = map[string]any{"accountId": u.ID, "name": u.Name, "operator": name == "op"}
	case "defaultgamemode":
		if len(args) != 1 || gameModes[strings.ToLower(args[0])] == "" {
			return nil, errors.New("defaultgamemode survival|creative|adventure|spectator")
		}
		next.DefaultGameMode = gameModes[strings.ToLower(args[0])]
		if err := s.updateSettings(next, actor, "defaultgamemode"); err != nil {
			return nil, err
		}
		result = next.DefaultGameMode
	case "stop":
		if len(args) != 0 {
			return nil, errors.New("用法：stop")
		}
		result = "server.stop.requested"
	case "whitelist":
		if len(args) < 1 {
			return nil, errors.New("whitelist on|off|add|remove|list <账号>")
		}
		switch args[0] {
		case "on":
			next.WhitelistEnabled = true
		case "off":
			next.WhitelistEnabled = false
		case "list":
			result = next.Whitelist
		case "add", "remove":
			args = args[1:]
			u, err := needUser()
			if err != nil {
				return nil, err
			}
			if fields[1] == "add" {
				next.Whitelist[u.ID] = true
			} else {
				delete(next.Whitelist, u.ID)
			}
		default:
			return nil, errors.New("invalid whitelist action")
		}
		if fields[1] != "list" {
			if err := s.updateSettings(next, actor, "whitelist"); err != nil {
				return nil, err
			}
		}
	case "ban", "pardon":
		u, err := needUser()
		if err != nil {
			return nil, err
		}
		if name == "ban" {
			reason := "被管理员封禁"
			if len(args) > 1 {
				reason = strings.Join(args[1:], " ")
			}
			next.Bans[u.ID] = ServerBan{Reason: limitText(reason, 240)}
		} else {
			delete(next.Bans, u.ID)
		}
		if err = s.updateSettings(next, actor, name); err != nil {
			return nil, err
		}
		if name == "ban" {
			for _, r := range s.allRooms() {
				for p := range r.Peers {
					if p.sid == u.ID {
						s.operatorKick(p, actor, next.Bans[u.ID].Reason)
					}
				}
			}
		}
	case "kick":
		u, err := needUser()
		if err != nil {
			return nil, err
		}
		reason := "被管理员踢出"
		if len(args) > 1 {
			reason = strings.Join(args[1:], " ")
		}
		n := 0
		for _, r := range s.allRooms() {
			for p := range r.Peers {
				if p.sid == u.ID && s.operatorKick(p, actor, reason) {
					n++
				}
			}
		}
		if n == 0 {
			return nil, errors.New("玩家不在线或被插件阻止")
		}
		result = n
	case "permission":
		u, err := needUser()
		if err != nil {
			return nil, err
		}
		if len(args) < 3 {
			return nil, errors.New("permission <账号> role|allow|deny|clear <角色或节点>")
		}
		g := next.Permissions[u.ID]
		switch args[1] {
		case "role":
			g.Role = args[2]
		case "allow":
			g.Allow = append(g.Allow, args[2])
		case "deny":
			g.Deny = append(g.Deny, args[2])
		case "clear":
			g = PermissionGrant{}
		default:
			return nil, errors.New("invalid permission action")
		}
		next.Permissions[u.ID] = g
		if err = s.updateSettings(next, actor, "permission"); err != nil {
			return nil, err
		}
	case "maintenance":
		if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
			return nil, errors.New("maintenance on|off")
		}
		next.Maintenance = args[0] == "on"
		if err := s.updateSettings(next, actor, "maintenance"); err != nil {
			return nil, err
		}
	case "plugins":
		if len(args) == 0 || args[0] == "list" {
			result = s.plugins.Status()
			break
		}
		if args[0] == "enable" || args[0] == "disable" {
			if len(args) != 2 {
				return nil, errors.New("plugins enable|disable <id>")
			}
			found := false
			for _, p := range s.plugins.Status() {
				if p.ID == args[1] {
					found = true
				}
			}
			if !found {
				return nil, errors.New("plugin not found")
			}
			if args[0] == "disable" {
				next.DisabledPlugins[args[1]] = true
			} else {
				delete(next.DisabledPlugins, args[1])
			}
			if err := s.updateSettings(next, actor, "plugins"); err != nil {
				return nil, err
			}
		} else if args[0] != "reload" {
			return nil, errors.New("plugins list|reload|enable|disable")
		}
		if err := s.reloadPlugins(actor); err != nil {
			return nil, err
		}
		result = s.plugins.Status()
	default:
		if world == "" && s.config.SingleWorld {
			world = s.worldBinding.WorldID
		}
		if player == "" && (name == "gamemode" || name == "gm" || name == "dimension" || name == "dim") && len(args) == 2 {
			player = args[1]
		}
		r := s.rooms[world]
		if r == nil {
			return nil, errors.New("玩法命令需要选择世界和在线玩家")
		}
		var p *Peer
		for _, t := range worldPeers(r) {
			if t.name == player || t.sid == player {
				p = t
				break
			}
		}
		if p == nil {
			return nil, errors.New("目标玩家不在线")
		}
		previousOwner, previousSource := p.owner, s.pluginSource
		p.owner = true
		s.pluginSource = "console:" + actor
		success := s.executeCommand(p, "/"+strings.Join(fields, " "))
		p.owner = previousOwner
		s.pluginSource = previousSource
		if !success {
			return nil, errors.New("命令被取消或校验失败，详情已发送给目标玩家")
		}
		result = "玩法命令已完成"

	}
	data["result"] = result
	s.emit(api.ServerCommandExecuted, nil, nil, data)
	s.drainActions()
	if name == "stop" {
		select {
		case s.stopRequested <- struct{}{}:
		default:
		}
	}
	return result, nil
}
func (s *Server) operatorKick(p *Peer, actor, reason string) bool {
	data := map[string]any{"actor": actor, "target": p.builder, "reason": limitText(reason, 240)}
	if !s.allowed(api.PlayerKicking, p.room, p, data) {
		return false
	}
	p.json(map[string]any{"t": "kicked", "reason": reason})
	p.close(4009)
	s.emit(api.PlayerKicked, p.room, p, data)
	s.disconnect(p)
	return true
}
func (s *Server) reloadPlugins(actor string) error {
	data := map[string]any{"actor": actor, "reason": "reload"}
	if !s.allowed(api.PluginsReloading, nil, nil, data) {
		return errors.New("reload cancelled by plugin")
	}
	s.actions = nil
	s.plugins.Close()
	s.plugins = plugins.LoadFiltered(s.config.PluginDir, filepath.Join(s.config.DataDir, "plugins"), s.config.PluginTimeout, s.ops.Settings.DisabledPlugins)
	before := s.registrySnapshot()
	if err := s.registerPluginDefinitions(); err != nil {
		s.emit(api.PluginsReloadFailed, nil, nil, map[string]any{"actor": actor, "reason": "registry_validation", "error": err.Error()})
		return err
	}
	if !reflect.DeepEqual(before, s.registry) {
		for _, r := range s.allRooms() {
			for p := range r.Peers {
				p.sys("插件注册表已更新，请重新进入服务器。")
				p.close(4009)
			}
		}
	}
	if s.plugins.Failed() > 0 {
		s.emit(api.PluginsReloadFailed, nil, nil, map[string]any{"actor": actor, "plugins": s.plugins.Status()})
		return errors.New("部分插件加载失败，查看插件状态")
	}
	s.emit(api.PluginsReloaded, nil, nil, data)
	return nil
}
func (s *Server) operationStatus() map[string]any {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	chunks, blocks := 0, 0
	players := []map[string]any{}
	worlds := []map[string]any{}
	for _, r := range s.allRooms() {
		chunks += len(r.chunks)
		blocks += len(r.Edits)
		worlds = append(worlds, r.info())
		for p := range r.Peers {
			players = append(players, map[string]any{"account": p.sid, "name": p.name, "world": r.root().ID, "dimension": r.dimName(), "x": p.state.X, "y": p.state.Y, "z": p.state.Z, "health": p.state.Health})
		}
	}
	times := append([]float64{}, s.ops.TickMillis...)
	sort.Float64s(times)
	p95 := 0.0
	if len(times) > 0 {
		p95 = times[(len(times)-1)*95/100]
	}
	tps := 10.0
	if n := len(s.ops.TickTimes); n > 1 {
		tps = mathMin(10, float64(n-1)/s.ops.TickTimes[n-1].Sub(s.ops.TickTimes[0]).Seconds())
	}
	return map[string]any{"uptimeSeconds": int(time.Since(s.started).Seconds()), "online": len(players), "maxPlayers": s.config.MaxPlayers, "tps": tps, "targetTPS": 10, "tickP95Millis": p95, "memoryBytes": mem.Alloc, "goroutines": runtime.NumGoroutine(), "loadedChunks": chunks, "loadedEdits": blocks, "players": players, "worlds": worlds, "plugins": s.plugins.Status(), "maintenance": s.ops.Settings.Maintenance, "whitelistEnabled": s.ops.Settings.WhitelistEnabled, "lastBackup": s.ops.LastBackup, "lastSave": s.ops.LastSave}
}
func mathMin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func (s *Server) operationsTick(start time.Time) {
	o := s.ops
	if o == nil {
		return
	}
	o.TickMillis = append(o.TickMillis, float64(time.Since(start).Microseconds())/1000)
	o.TickTimes = append(o.TickTimes, time.Now())
	if len(o.TickMillis) > 300 {
		o.TickMillis = o.TickMillis[1:]
		o.TickTimes = o.TickTimes[1:]
	}
	if o.Settings.BackupMinutes > 0 && time.Since(o.LastBackup) > time.Duration(o.Settings.BackupMinutes)*time.Minute {
		o.LastBackup = time.Now()
		if _, err := s.backup("scheduler", "auto"); err != nil {
			s.audit("scheduler", "backup", err.Error(), false)
		}
	}
}
func (s *Server) admission(sid string) error {
	if s.ops == nil {
		return nil
	}
	v := s.ops.Settings
	if b, ok := v.Bans[sid]; ok && (b.Until == 0 || b.Until > time.Now().Unix()) {
		return fmt.Errorf("账号已封禁：%s", b.Reason)
	}
	if s.hasPermission(sid, "server.admin") {
		return nil
	}
	if v.Maintenance {
		return errors.New(v.MaintenanceMessage)
	}
	if v.WhitelistEnabled && !v.Whitelist[sid] {
		return errors.New("此服务器已启用白名单。")
	}
	return nil
}
func (s *Server) listBackups() []map[string]any {
	out := []map[string]any{}
	entries, _ := os.ReadDir(filepath.Join(s.config.DataDir, "backups"))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zip") {
			i, err := e.Info()
			if err == nil {
				out = append(out, map[string]any{"name": e.Name(), "bytes": i.Size(), "created": i.ModTime()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) > out[j]["name"].(string) })
	return out
}
func (s *Server) backup(actor, reason string) (string, error) {
	data := map[string]any{"actor": actor, "reason": reason}
	if !s.allowed(api.BackupCreating, nil, nil, data) {
		return "", errors.New("backup cancelled by plugin")
	}
	s.accounts.mu.Lock()
	err := s.accounts.saveLocked()
	s.accounts.mu.Unlock()
	if err != nil {
		return "", err
	}
	for _, r := range s.allRooms() {
		r.Dirty = true
		if err := s.saveRoom(r); err != nil {
			return "", err
		}
	}
	name := reason + "-" + time.Now().UTC().Format("20060102T150405.000000000") + ".zip"
	dir := filepath.Join(s.config.DataDir, "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	file := filepath.Join(dir, name)
	f, err := os.OpenFile(file+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	z := zip.NewWriter(f)
	for _, root := range []string{"rooms", "worlds", "accounts.json", "federation.json", "server.json", "world-server.json", "registry.json", "plugins"} {
		err = filepath.WalkDir(filepath.Join(s.config.DataDir, root), func(path string, e os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if e.Type()&os.ModeSymlink != 0 {
				return errors.New("backup refuses symbolic links")
			}
			if e.IsDir() {
				return nil
			}
			if strings.HasSuffix(e.Name(), ".tmp") {
				return nil
			}
			rel, err := filepath.Rel(s.config.DataDir, path)
			if err != nil {
				return err
			}
			h := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate}
			h.SetMode(0600)
			w, err := z.CreateHeader(h)
			if err != nil {
				return err
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, in)
			in.Close()
			return err
		})
		if err != nil {
			break
		}
	}
	if e := z.Close(); err == nil {
		err = e
	}
	if e := f.Sync(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(file + ".tmp")
		s.emit(api.BackupFailed, nil, nil, map[string]any{"actor": actor, "error": err.Error()})
		return "", err
	}
	if err = os.Rename(file+".tmp", file); err != nil {
		return "", err
	}
	s.ops.LastBackup = time.Now()
	data["name"] = name
	s.emit(api.BackupCreated, nil, nil, data)
	s.audit(actor, "backup", name, true)
	if reason == "auto" {
		n := 0
		for _, b := range s.listBackups() {
			name := b["name"].(string)
			if strings.HasPrefix(name, "auto-") {
				n++
				if n > s.ops.Settings.BackupRetention {
					os.Remove(filepath.Join(dir, name))
				}
			}
		}
	}
	return name, nil
}
