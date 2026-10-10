package game

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mcraft/backend/internal/plugins"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func allowedBackupPath(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || filepath.ToSlash(filepath.Clean(name)) != strings.TrimSuffix(name, "/") {
		return false
	}
	root := strings.Split(name, "/")[0]
	return root == "rooms" || root == "worlds" || root == "plugins" || name == "accounts.json" || name == "federation.json" || name == "server.json" || name == "world-server.json"
}
func extractBackup(file, stage string) error {
	archive, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer archive.Close()
	var total uint64
	seen := map[string]bool{}
	for _, f := range archive.File {
		if !allowedBackupPath(f.Name) || f.Mode()&os.ModeSymlink != 0 || seen[f.Name] {
			return errors.New("备份包含非法路径或重复条目")
		}
		seen[f.Name] = true
		total += f.UncompressedSize64
		if total > 10<<30 || f.UncompressedSize64 > 256<<20 {
			return errors.New("备份超过恢复大小限制")
		}
		path := filepath.Join(stage, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1))
		ce := out.Close()
		in.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	if (!seen["accounts.json"] && !seen["federation.json"]) || !seen["server.json"] {
		return errors.New("备份缺少账号或服务器配置")
	}
	return nil
}
func RestoreOffline(config Config, name string) error {
	s, err := New(config)
	if err != nil {
		return err
	}
	defer s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restoreBackup(name, "local-console")
}
func (s *Server) restoreBackup(name, actor string) error {
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".zip") {
		return errors.New("invalid backup name")
	}
	stage, err := os.MkdirTemp(s.config.DataDir, ".restore-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = extractBackup(filepath.Join(s.config.DataDir, "backups", name), stage); err != nil {
		return err
	}
	cfg := s.config
	cfg.DataDir = stage
	cfg.PluginDir = ""
	check, err := New(cfg)
	if err != nil {
		return fmt.Errorf("备份校验失败：%w", err)
	}
	err = check.validateChunkFiles()
	closeErr := check.Close()
	if err != nil {
		return fmt.Errorf("备份区块校验失败：%w", err)
	}
	if closeErr != nil {
		return closeErr
	}
	data := map[string]any{"actor": actor, "name": name, "reason": "restore"}
	if !s.allowed(api.BackupRestoring, nil, nil, data) {
		return errors.New("restore cancelled by plugin")
	}
	if s.config.SingleWorld {
		selected := check.rooms[check.worldBinding.WorldID]
		if !s.allowed(api.ServerWorldSelecting, selected, nil, map[string]any{"reason": "backup_restore", "actor": actor, "target": selected.ID, "worldId": selected.ID, "previousWorldId": s.worldBinding.WorldID, "newWorld": false}) {
			return errors.New("world binding restore cancelled by plugin")
		}
	}
	for _, r := range s.allRooms() {
		for p := range r.Peers {
			if !s.allowed(api.PlayerKicking, r, p, map[string]any{"actor": actor, "reason": "restore"}) {
				return errors.New("player disconnect cancelled by plugin")
			}
		}
	}
	prior, err := s.backup(actor, "pre-restore")
	if err != nil {
		return err
	}
	data["previousBackup"] = prior
	for _, r := range s.allRooms() {
		for p := range r.Peers {
			p.json(map[string]any{"t": "kicked", "reason": "世界已从备份恢复，请重新进入"})
			p.close(4009)
			s.emit(api.PlayerKicked, r, p, map[string]any{"actor": actor, "reason": "restore"})
			s.disconnect(p)
		}
	}
	s.actions = nil
	s.plugins.Close()
	rollback := filepath.Join(s.config.DataDir, "backups", "restore-files-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err = os.MkdirAll(rollback, 0700); err != nil {
		s.plugins = plugins.LoadFiltered(s.config.PluginDir, filepath.Join(s.config.DataDir, "plugins"), s.config.PluginTimeout, s.ops.Settings.DisabledPlugins)
		return err
	}
	oldRooms, oldAccounts, oldSettings := s.rooms, s.accounts, s.ops.Settings
	oldBinding := s.worldBinding
	roots := []string{"rooms", "worlds", "accounts.json", "federation.json", "server.json", "world-server.json", "plugins"}
	moved := map[string]bool{}
	installed := map[string]bool{}
	rollbackFiles := func() {
		for _, root := range roots {
			dst := filepath.Join(s.config.DataDir, root)
			if installed[root] {
				os.RemoveAll(dst)
			}
			if moved[root] {
				os.Rename(filepath.Join(rollback, root), dst)
			}
		}
		s.rooms, s.accounts, s.ops.Settings = oldRooms, oldAccounts, oldSettings
		s.worldBinding = oldBinding
	}
	for _, root := range roots {
		dst := filepath.Join(s.config.DataDir, root)
		if _, e := os.Stat(dst); e == nil {
			if err = os.Rename(dst, filepath.Join(rollback, root)); err != nil {
				break
			}
			moved[root] = true
		} else if !os.IsNotExist(e) {
			err = e
			break
		}
		src := filepath.Join(stage, root)
		if _, e := os.Stat(src); e == nil {
			if err = os.Rename(src, dst); err != nil {
				break
			}
			installed[root] = true
		} else if !os.IsNotExist(e) {
			err = e
			break
		}
	}
	if err != nil {
		rollbackFiles()
	} else {
		s.rooms = map[string]*Room{}
		s.engine.current = ""
		err = s.loadRooms()
		if err == nil {
			if s.identity != nil {
				s.accounts, err = newFederatedAccounts(s.config.DataDir, s.identity)
			} else {
				s.accounts, err = newAccounts(s.config.DataDir, map[string]bool{})
			}
		}
		if err == nil {
			raw, e := os.ReadFile(filepath.Join(s.config.DataDir, "server.json"))
			err = e
			if err == nil {
				err = json.Unmarshal(raw, &s.ops.Settings)
			}
		}
		if err != nil {
			rollbackFiles()
		}
	}
	s.plugins = plugins.LoadFiltered(s.config.PluginDir, filepath.Join(s.config.DataDir, "plugins"), s.config.PluginTimeout, s.ops.Settings.DisabledPlugins)
	if err != nil {
		s.emit(api.BackupRestoreFailed, nil, nil, map[string]any{"name": name, "actor": actor, "error": err.Error()})
		return err
	}
	s.audit(actor, "restore", name, true)
	s.emit(api.BackupRestored, nil, nil, data)
	if s.config.SingleWorld {
		s.emit(api.ServerWorldSelected, s.rooms[s.worldBinding.WorldID], nil, map[string]any{"reason": "backup_restore", "actor": actor, "target": s.worldBinding.WorldID, "worldId": s.worldBinding.WorldID, "previousWorldId": oldBinding.WorldID, "newWorld": false})
	}
	for _, r := range s.allRooms() {
		s.emit(api.WorldLoaded, r, nil, map[string]any{"reason": "restore"})
	}
	return nil
}
