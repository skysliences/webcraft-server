package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	api "mcraft/backend/pkg/plugin"
)

// The binding is separate from terrain and is included in server backups.
// A missing bound save is an error, never an invitation to replace progress.
type worldBinding struct {
	Version int    `json:"version"`
	WorldID string `json:"worldId"`
}

func (s *Server) prepareWorldBinding(entries []os.DirEntry) error {
	name := strings.TrimSpace(s.config.WorldName)
	if name == "" {
		name = "世界"
	}
	mode := s.config.WorldMode
	if mode == "" {
		mode = "survival"
	}
	if len([]rune(name)) > 32 || (mode != "creative" && mode != "survival") {
		return errors.New("WORLD_NAME 最多 32 个字符；WORLD_MODE 必须是 creative 或 survival")
	}
	s.config.WorldName, s.config.WorldMode = name, mode
	existing := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && len(n) == 21 && strings.HasSuffix(n, ".json") && roomRE.MatchString(n[:16]) {
			existing[n[:16]] = true
		}
	}
	var prior worldBinding
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "world-server.json"))
	if err == nil {
		if json.Unmarshal(raw, &prior) != nil || prior.Version != 1 || !roomRE.MatchString(prior.WorldID) {
			return errors.New("world-server.json 无效，已停止启动以保护存档")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	id := s.config.WorldID
	if id != "" && !roomRE.MatchString(id) {
		return errors.New("WORLD_ID 必须是 16 位小写字母或数字")
	}
	if id == "" {
		id = prior.WorldID
	}
	s.worldBindingNew = false
	if id != "" {
		if !existing[id] {
			return fmt.Errorf("主世界 %s 存档不存在，已停止启动；请检查 WORLD_ID 和 DATA_DIR", id)
		}
	} else if existing[DefaultRoom] {
		id = DefaultRoom
	} else if len(existing) == 1 {
		for key := range existing {
			id = key
		}
	} else if len(existing) > 1 {
		return errors.New("发现多个旧存档，请通过 WORLD_ID 指定本服务器的主世界")
	} else {
		id, s.worldBindingNew = randomHex(8), true
	}
	s.worldBindingPrevious = prior.WorldID
	s.worldBinding = worldBinding{Version: 1, WorldID: id}
	return nil
}

func (s *Server) newServerWorld() *Room {
	r := newRoom(s.worldBinding.WorldID, s.config.WorldName, s.config.WorldMode, 0)
	if s.config.InfiniteWorlds {
		r.Infinite, r.Generator = true, generatorVersion
	}
	if r.usesSurvival() {
		r.W = newSurvivalW
		r.Ext = &RoomExt{Difficulty: "normal", Rules: map[string]bool{"keepInventory": false}}
	}
	return r
}

func (s *Server) bindingData(reason, actor string) map[string]any {
	return map[string]any{"reason": reason, "actor": actor, "target": s.worldBinding.WorldID, "worldId": s.worldBinding.WorldID, "previousWorldId": s.worldBindingPrevious, "newWorld": s.worldBindingNew}
}

func (s *Server) commitWorldBinding(reason, actor string) error {
	r := s.rooms[s.worldBinding.WorldID]
	if r == nil {
		return errors.New("主世界没有加载")
	}
	if s.plugins.Failed() > 0 {
		return errors.New("插件启动失败，未提交服务器世界绑定")
	}
	if !s.allowed(api.ServerWorldSelecting, r, nil, s.bindingData(reason, actor)) {
		return errors.New("server.world.select.before 被插件取消，未启动服务器")
	}
	if s.worldBindingNew {
		e := s.emit(api.WorldCreating, r, nil, map[string]any{"name": r.Name, "mode": r.Mode, "lock": r.Lock, "reason": "server_initialize", "actor": actor, "target": r.ID})
		if e.Cancelled {
			return errors.New("world.create.before 被插件取消，未创建主世界")
		}
		r.Name = strings.TrimSpace(e.String("name"))
		if r.Name == "" || len([]rune(r.Name)) > 32 {
			return errors.New("插件返回无效世界名称，未创建主世界")
		}
	}
	for _, room := range s.allRooms() {
		if err := s.saveRoom(room); err != nil {
			return err
		}
	}
	file := filepath.Join(s.config.DataDir, "world-server.json")
	if old, err := os.ReadFile(file); err == nil && s.worldBindingPrevious != s.worldBinding.WorldID {
		// Keep the original binding before changing the selected save.
		backup := file + ".pre-single-" + randomHex(8) + ".bak"
		if err = os.WriteFile(backup, old, 0600); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(s.worldBinding, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicPrivateFile(file, raw); err != nil {
		return err
	}
	if s.worldBindingNew {
		s.emit(api.WorldCreated, r, nil, map[string]any{"reason": "server_initialize", "actor": actor, "target": r.ID})
	}
	s.emit(api.ServerWorldSelected, r, nil, s.bindingData(reason, actor))
	return nil
}

func (s *Server) serverDescriptor(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[s.worldBinding.WorldID]
	if !s.config.SingleWorld {
		r = s.rooms[DefaultRoom]
	}
	if r == nil {
		failure(w, 503, "服务器主世界暂不可用。")
		return
	}
	data := map[string]any{"service": "mcraft-go", "apiVersion": api.APIVersion, "singleWorld": s.config.SingleWorld, "online": s.online(), "cap": s.config.MaxPlayers, "world": map[string]any{"id": r.ID, "name": r.Name, "mode": r.Mode}, "joinPath": "/r/" + r.ID}
	if s.identity != nil {
		data["auth"] = map[string]any{"mode": "central", "issuer": s.identity.issuer, "serverId": s.identity.serverID}
	}
	writeJSON(w, 200, data)
}
