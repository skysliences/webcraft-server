package game

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func TestSingleWorldPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "single-world-fixture" {
		return
	}
	p := api.New()
	var config struct {
		Case string `json:"case"`
	}
	p.OnEnable = func(c *api.Context) error { return json.Unmarshal(c.Config, &config) }
	p.On(api.ServerWorldSelecting, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if config.Case == "cancel-select" || config.Case == "cancel-restore" && e.String("reason") == "backup_restore" {
			e.Cancel("single_world.protected")
		}
		if config.Case == "save-failure" {
			return os.MkdirAll(filepath.Join(c.DataDir, "..", "..", "rooms", e.String("worldId")+".json"), 0700)
		}
		if config.Case == "binding-failure" {
			return os.MkdirAll(filepath.Join(c.DataDir, "..", "..", "world-server.json"), 0700)
		}
		e.Data["worldId"] = "ffffffffffffffff" // Not a declared mutable field.
		return nil
	})
	p.On(api.WorldCreating, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		switch config.Case {
		case "cancel-create":
			e.Cancel("single_world.create_protected")
		case "invalid-name":
			e.Data["name"] = " "
		case "rename":
			e.Data["name"] = "插件命名的主世界"
		}
		e.Data["mode"] = "invalid"
		return nil
	})
	for _, name := range []string{api.PlayerOperatorChanging, api.PlayerGameModeChanging, api.ServerDefaultGameModeChanging} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if config.Case == "cancel-operator" && e.Name == api.PlayerOperatorChanging || config.Case == "cancel-mode" && e.Name == api.PlayerGameModeChanging || config.Case == "cancel-default" && e.Name == api.ServerDefaultGameModeChanging {
				e.Cancel("operators.protected")
			}
			e.Data["gamemode"], e.Data["operator"], e.Data["target"] = "invalid", false, "forged"
			return nil
		})
	}
	for _, name := range []string{api.ServerWorldSelecting, api.ServerWorldSelected, api.WorldCreating, api.WorldCreated, api.WorldLoaded, api.ServerStarted, api.BackupRestored, api.PlayerOperatorChanging, api.PlayerOperatorChanged, api.PlayerGameModeChanging, api.PlayerGameModeChanged, api.ServerDefaultGameModeChanging, api.ServerDefaultGameModeChanged} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(append(raw, '\n'))
			e.Data["name"] = "monitor ignored"
			e.Data["worldId"] = "eeeeeeeeeeeeeeee"
			e.Cancel("monitor ignored")
			if e.Name == api.PlayerOperatorChanged {
				c.SetOperator(e.String("target"), false)
			}
			return err
		})
	}
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func singleWorldConfig(t *testing.T, testCase string) Config {
	t.Helper()
	pluginsDir, dataDir := t.TempDir(), t.TempDir()
	entry := filepath.Join(pluginsDir, "single-world")
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": "single-world", "name": "single-world", "version": "1", "apiVersion": 1, "entry": "plugin", "args": []string{"-test.run=^TestSingleWorldPluginProcess$", "single-world-fixture"}, "config": map[string]string{"case": testCase}})
	if err = os.WriteFile(filepath.Join(entry, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return Config{DataDir: dataDir, EngineFile: "../../../public/engine.wasm", PluginDir: pluginsDir, PluginTimeout: time.Second, SingleWorld: true, InfiniteWorlds: true}
}

func singleWorldEvents(t *testing.T, dataDir string) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "plugins", "single-world", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var event api.Event
		if err = json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, &event)
	}
	return events
}

func TestSingleWorldStartupEvents(t *testing.T) {
	for _, testCase := range []string{"cancel-select", "cancel-create", "invalid-name", "save-failure", "binding-failure"} {
		t.Run(testCase, func(t *testing.T) {
			cfg := singleWorldConfig(t, testCase)
			s, err := New(cfg)
			if err == nil {
				s.Close()
				t.Fatal("invalid/cancelled/failed binding started a server")
			}
			for _, event := range singleWorldEvents(t, cfg.DataDir) {
				if event.Name == api.ServerWorldSelected || event.Name == api.WorldCreated || event.Name == api.WorldLoaded || event.Name == api.ServerStarted {
					t.Fatal("failed startup emitted success", event.Name)
				}
			}
		})
	}
	cfg := singleWorldConfig(t, "rename")
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := s.worldBinding.WorldID
	if len(s.rooms) != 1 || s.rooms[id].Name != "插件命名的主世界" || s.rooms[id].Mode != "survival" || !s.rooms[id].Infinite {
		t.Fatal("mutation whitelist or single world bootstrap failed")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	events := singleWorldEvents(t, cfg.DataDir)
	if len(events) != 6 || events[0].Name != api.ServerWorldSelecting || events[1].Name != api.WorldCreating || events[2].Name != api.WorldCreated || events[3].Name != api.ServerWorldSelected || events[4].Name != api.WorldLoaded || events[5].Name != api.ServerStarted {
		t.Fatal("incorrect committed event order", events)
	}
	selected := events[3]
	if selected.String("reason") != "server_start" || selected.String("actor") != "server" || selected.String("worldId") != id || selected.World.ID != id {
		t.Fatal("missing stable selection context", selected)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.worldBinding.WorldID != id || len(s.rooms) != 1 {
		t.Fatal("restart regenerated the world")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	created := 0
	for _, e := range singleWorldEvents(t, cfg.DataDir) {
		if e.Name == api.WorldCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatal("restart emitted world.created again")
	}
}

func TestSingleWorldArchivesDimensionsAndAPI(t *testing.T) {
	cfg := singleWorldConfig(t, "")
	roomDir := filepath.Join(cfg.DataDir, "rooms")
	if err := os.MkdirAll(roomDir, 0700); err != nil {
		t.Fatal(err)
	}
	const archived = "1111111111111111"
	archiveRaw := []byte("archived bytes must not be parsed or migrated")
	if err := os.WriteFile(filepath.Join(roomDir, archived+".json"), archiveRaw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, dim := range []string{"", "nether", "end"} {
		id := DefaultRoom
		if dim != "" {
			id += "." + dim
		}
		raw, _ := json.Marshal(diskRoom{Version: saveVersionCurrent, Name: "原世界", Mode: "creative", Infinite: true, Generator: generatorVersion, W: 1024, Dim: dim, Players: map[string]*Player{"player0000000000": {Health: 17, Food: 16, X: 42, Y: 64, Z: 35}}})
		if err := os.WriteFile(filepath.Join(roomDir, id+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		h.Close()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if len(s.rooms) != 1 || len(s.allRooms()) != 3 || s.rooms[DefaultRoom].Players["player0000000000"].Health != 17 {
		t.Fatal("lost root, dimensions, or progress")
	}
	for _, path := range []string{"/api/server", "/api/servers", "/api/events"} {
		res, err := http.Get(h.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatal(path, res.StatusCode, err)
		}
		switch path {
		case "/api/server":
			if body["singleWorld"] != true || body["joinPath"] != "/r/"+DefaultRoom || body["world"].(map[string]any)["id"] != DefaultRoom {
				t.Fatal("wrong server descriptor", body)
			}
		case "/api/servers":
			if len(body["worlds"].([]any)) != 1 {
				t.Fatal("archived worlds exposed")
			}
		case "/api/events":
			found := false
			for _, e := range body["events"].([]any) {
				if e.(map[string]any)["name"] == api.ServerWorldSelecting {
					found = true
				}
			}
			if !found {
				t.Fatal("selection absent from public catalog")
			}
		}
	}
	res, err := http.Post(h.URL+"/api/rooms", "application/json", strings.NewReader(`{"name":"second world"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 409 || len(s.rooms) != 1 {
		t.Fatal("second world creation permitted")
	}
	res, err = http.Get(h.URL + "/api/rooms/" + archived)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("archived world API accessible", res.StatusCode)
	}
	res, err = http.Get(h.URL + "/ws/" + archived)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("archived world socket accessible", res.StatusCode)
	}
	s.mu.Lock()
	name, err := s.backup("test", "single-world")
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(filepath.Join(cfg.DataDir, "backups", name))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range archive.File {
		found[f.Name] = true
	}
	archive.Close()
	if !found["world-server.json"] || !found["rooms/"+archived+".json"] {
		t.Fatal("backup lost world binding or archive")
	}
	s.mu.Lock()
	s.rooms[DefaultRoom].Name, s.rooms[DefaultRoom].Dirty = "changed after backup", true
	err = s.restoreBackup(name, "test")
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if s.worldBinding.WorldID != DefaultRoom || s.rooms[DefaultRoom].Name != "原世界" || len(s.rooms) != 1 || len(s.allRooms()) != 3 {
		t.Fatal("restore lost active world or dimensions")
	}
	raw, err := os.ReadFile(filepath.Join(roomDir, archived+".json"))
	if err != nil || string(raw) != string(archiveRaw) {
		t.Fatal("archived save changed", err)
	}
	var restored bool
	for _, e := range singleWorldEvents(t, cfg.DataDir) {
		if e.Name == api.ServerWorldSelected && e.String("reason") == "backup_restore" {
			restored = true
		}
	}
	if !restored {
		t.Fatal("restore did not emit committed selection")
	}
}

func TestSingleWorldRestoreCancellationAndMissingSave(t *testing.T) {
	cfg := singleWorldConfig(t, "cancel-restore")
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := s.worldBinding.WorldID
	s.mu.Lock()
	name, err := s.backup("test", "binding-cancel")
	if err == nil {
		err = s.restoreBackup(name, "test")
	}
	s.mu.Unlock()
	if err == nil {
		t.Fatal("plugin allowed cancelled restore")
	}
	for _, e := range singleWorldEvents(t, cfg.DataDir) {
		if e.Name == api.BackupRestored || e.Name == api.ServerWorldSelected && e.String("reason") == "backup_restore" {
			t.Fatal("cancelled restore emitted success")
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(cfg.DataDir, "rooms", id+".json")); err != nil {
		t.Fatal(err)
	}
	if s, err = New(cfg); err == nil {
		s.Close()
		t.Fatal("missing bound save was silently regenerated")
	}
	if _, err = os.Stat(filepath.Join(cfg.DataDir, "rooms", id+".json")); !os.IsNotExist(err) {
		t.Fatal("missing progress overwritten")
	}
}
