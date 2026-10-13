package game

import (
	"encoding/json"
	"fmt"
	"math"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

var registryKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}:[a-z][a-z0-9_./-]{0,127}$`)

func entryID(e api.RegistryEntry) int {
	if e.Block != nil {
		return e.Block.ID
	}
	if e.Item != nil {
		return e.Item.ID
	}
	return 0
}
func entryKey(e api.RegistryEntry) string {
	if e.Block != nil {
		return e.Block.Key
	}
	if e.Item != nil {
		return e.Item.Key
	}
	return ""
}
func entryName(e api.RegistryEntry) string {
	if e.Block != nil {
		return e.Block.Name
	}
	if e.Item != nil {
		return e.Item.Name
	}
	return ""
}
func cloneEntry(e api.RegistryEntry) api.RegistryEntry {
	raw, _ := json.Marshal(e)
	var out api.RegistryEntry
	json.Unmarshal(raw, &out)
	return out
}
func (s *Server) registryPath() string { return filepath.Join(s.config.DataDir, "registry.json") }
func (s *Server) loadRegistry() error {
	s.registry = api.RegistrySnapshot{Version: 1, Entries: []api.RegistryEntry{}}
	raw, err := os.ReadFile(s.registryPath())
	if os.IsNotExist(err) {
		return s.applyRegistry()
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &s.registry); err != nil {
		return fmt.Errorf("registry: %w", err)
	}
	if s.registry.Version != 1 {
		return fmt.Errorf("unsupported registry version")
	}
	seen := map[string]bool{}
	ids := map[int]bool{}
	names := map[string]bool{}
	for _, e := range s.registry.Entries {
		if err = s.validateRegistryEntry(e); err != nil {
			return fmt.Errorf("saved registry: %w", err)
		}
		if seen[entryKey(e)] || ids[entryID(e)] || names[entryName(e)] {
			return fmt.Errorf("duplicate saved registry entry")
		}
		seen[entryKey(e)] = true
		ids[entryID(e)] = true
		names[entryName(e)] = true
	}
	return s.applyRegistry()
}
func (s *Server) validateRegistryEntry(e api.RegistryEntry) error {
	if (e.Block == nil) == (e.Item == nil) {
		return fmt.Errorf("one block or item definition is required")
	}
	key := entryKey(e)
	id := entryID(e)
	name := entryName(e)
	if !registryKeyPattern.MatchString(key) || !strings.HasPrefix(key, e.PluginID+":") || strings.Contains(key, "..") {
		return fmt.Errorf("invalid plugin namespace")
	}
	if id < 220 || id > 65535 || id == 255 {
		return fmt.Errorf("ID outside available u16 namespace")
	}
	core := s.coreRules
	if core == nil {
		core, _ = loadRules()
	}
	if id < len(core.Names) || core.max(id) > 0 {
		return fmt.Errorf("reserved core ID")
	}
	_, coreName := core.byName[name]
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\n\r\x00") || strings.Contains(name, " (") || coreName {
		return fmt.Errorf("invalid or reserved name")
	}
	if e.Block != nil {
		d := e.Block
		if d.ModelBlock < 1 || d.ModelBlock >= len(core.Names) || core.Names[d.ModelBlock] == "" || core.Kinds[d.ModelBlock] == 0 || math.IsNaN(d.Hardness) || math.IsInf(d.Hardness, 0) || d.Hardness < 0 || d.Hardness > 100000 || d.Stack < 1 || d.Stack > 64 {
			return fmt.Errorf("invalid block model, hardness or stack")
		}
	} else {
		d := e.Item
		if core.max(d.ModelItem) == 0 || d.Stack < 1 || d.Stack > 64 || d.Durability < 0 || d.Durability > 1000000 || d.Durability > 0 && d.Stack != 1 {
			return fmt.Errorf("invalid item model, durability or stack")
		}
		if d.PlacesKey != "" && !registryKeyPattern.MatchString(d.PlacesKey) {
			return fmt.Errorf("invalid placesKey")
		}
	}
	return nil
}
func (s *Server) registerPluginDefinitions() error {
	next := api.RegistrySnapshot{Version: 1, Entries: append([]api.RegistryEntry(nil), s.registry.Entries...)}
	ids := map[int]bool{}
	keys := map[string]int{}
	names := map[string]string{}
	for i, e := range next.Entries {
		ids[entryID(e)] = true
		keys[entryKey(e)] = i
		names[entryName(e)] = entryKey(e)
	}
	changed := false
	var committed []api.RegistryEntry
	for _, original := range s.plugins.Registrations() {
		e := cloneEntry(original)
		key := entryKey(e)
		if e.Block != nil && e.Block.Stack == 0 {
			e.Block.Stack = 64
		}
		if e.Item != nil && e.Item.Stack == 0 {
			e.Item.Stack = 64
		}
		old, indexed := keys[key]
		if indexed {
			id := entryID(next.Entries[old])
			if entryID(e) == 0 {
				if e.Block != nil {
					e.Block.ID = id
				} else if e.Item != nil {
					e.Item.ID = id
				}
			}
			if !reflect.DeepEqual(e, next.Entries[old]) {
				return fmt.Errorf("registry %s already has a persistent definition; migrate explicitly instead of reassigning saved blocks", key)
			}
			continue
		}
		if entryID(e) == 0 {
			id := 1024
			for id <= 65535 && ids[id] {
				id++
			}
			if e.Block != nil {
				e.Block.ID = id
			} else if e.Item != nil {
				e.Item.ID = id
			}
		}
		err := s.validateRegistryEntry(e)
		if ids[entryID(e)] || names[entryName(e)] != "" {
			err = fmt.Errorf("duplicate registry ID or name")
		}
		data := map[string]any{"reason": "plugin_enable", "actor": map[string]any{"kind": "plugin", "id": e.PluginID}, "target": map[string]any{"kind": "registry", "key": key}, "entry": cloneEntry(e), "registryVersion": 1}
		if err != nil {
			data["error"] = err.Error()
			s.emit(api.RegistryRejected, nil, nil, data)
			return fmt.Errorf("registry %s: %w", key, err)
		}
		if !s.allowed(api.RegistryRegistering, nil, nil, data) {
			data["reason"] = "plugin_cancelled"
			s.emit(api.RegistryRejected, nil, nil, data)
			continue
		}
		// Definitions and IDs are read-only event fields. Validate the actual snapshot again.
		if err = s.validateRegistryEntry(e); err != nil {
			return err
		}
		ids[entryID(e)] = true
		keys[key] = len(next.Entries)
		names[entryName(e)] = key
		next.Entries = append(next.Entries, e)
		committed = append(committed, e)
		changed = true
	}
	previous := s.registry
	previousRules := s.rules
	s.registry = next
	if err := s.applyRegistry(); err != nil {
		s.registry = previous
		s.rules = previousRules
		return err
	}
	if changed {
		raw, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return err
		}
		if err = atomicPrivateFile(s.registryPath(), append(raw, '\n')); err != nil {
			s.registry = previous
			s.rules = previousRules
			return err
		}
	}
	for _, e := range committed {
		s.emit(api.RegistryRegistered, nil, nil, map[string]any{"reason": "plugin_enable", "actor": map[string]any{"kind": "plugin", "id": e.PluginID}, "target": map[string]any{"kind": "registry", "key": entryKey(e)}, "entry": cloneEntry(e), "registryVersion": 1})
	}
	return nil
}
func (s *Server) applyRegistry() error {
	if len(s.registry.Entries) > 0 && s.engine != nil && s.engine.mod.ExportedFunction("register_block16") == nil {
		return fmt.Errorf("plugin blocks/items require the current 16-bit WASM engine")
	}
	core, err := loadRules()
	if err != nil {
		return err
	}
	n := len(core.Names)
	for _, e := range s.registry.Entries {
		if e.Block != nil {
			n = max(n, e.Block.ID+1)
		}
	}
	core.Names = append(core.Names, make([]string, n-len(core.Names))...)
	core.Kinds = append(core.Kinds, make([]int, n-len(core.Kinds))...)
	core.Mining = append(core.Mining, make([]map[string]MiningRule, n-len(core.Mining))...)
	core.itemModels = map[int]int{}
	core.blockModels = map[int]int{}
	for _, e := range s.registry.Entries {
		if d := e.Block; d != nil {
			core.Names[d.ID] = d.Name
			core.Kinds[d.ID] = core.Kinds[d.ModelBlock]
			core.blockModels[d.ID] = d.ModelBlock
			core.Mining[d.ID] = map[string]MiningRule{"0": {Ticks: int(math.Ceil(d.Hardness * 30)), Harvest: true}}
			it := Item{ID: d.ID, Key: d.Key, Name: d.Name, Stack: d.Stack, PlacesID: d.ID}
			core.byID[d.ID] = it
			core.byName[d.Key] = d.ID
			core.byName[d.Name] = d.ID
			core.Items = append(core.Items, it)
			if s.engine != nil && s.engine.mod.ExportedFunction("register_block16") != nil && int32(s.engine.call("register_block16", uint64(d.ID), uint64(d.ModelBlock))) != 0 {
				return fmt.Errorf("WASM rejects registry %s", d.Key)
			}
		}
	}
	for _, e := range s.registry.Entries {
		if d := e.Item; d != nil {
			core.itemModels[d.ID] = d.ModelItem
			it := core.byID[d.ModelItem]
			it.ID = d.ID
			it.Key = d.Key
			it.Name = d.Name
			it.Stack = d.Stack
			it.Durability = d.Durability
			it.PlacesID = 0
			if d.PlacesKey != "" {
				id := core.byName[d.PlacesKey]
				if id == 0 || id >= len(core.Names) || core.Names[id] == "" {
					return fmt.Errorf("unknown placesKey %s", d.PlacesKey)
				}
				it.PlacesID = id
			}
			core.byID[d.ID] = it
			core.byName[d.Key] = d.ID
			core.byName[d.Name] = d.ID
			core.Items = append(core.Items, it)
		}
	}
	s.rules = core
	return nil
}
func (r *Rules) validBlock(id int) bool {
	return id >= 0 && id < len(r.Names) && (id == 0 || r.Names[id] != "")
}
func (s *Server) registrySnapshot() api.RegistrySnapshot {
	out := api.RegistrySnapshot{Version: 1, Entries: make([]api.RegistryEntry, len(s.registry.Entries))}
	for i, e := range s.registry.Entries {
		out.Entries[i] = cloneEntry(e)
	}
	return out
}

func (s *Server) registryBlockKey(b int) string {
	for _, e := range s.registry.Entries {
		if e.Block != nil && e.Block.ID == b {
			return e.Block.Key
		}
	}
	return ""
}
