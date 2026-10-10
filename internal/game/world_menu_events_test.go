package game

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	api "mcraft/backend/pkg/plugin"
)

func TestWorldMenuPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "world-menu-fixture" {
		return
	}
	p := api.New()
	p.On(api.WorldCreating, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		switch e.String("name") {
		case "protected":
			e.Cancel("world_menu.protected")
		case "rename":
			e.Data["name"] = "插件命名的世界"
		case "invalid":
			e.Data["name"] = " "
		}
		e.Data["mode"] = "invalid" // Undeclared mutations must be ignored.
		return nil
	})
	for _, name := range []string{api.WorldCreating, api.WorldCreated} {
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
			e.Cancel("monitor ignored")
			return err
		})
	}
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestWorldMenuCreationEvents(t *testing.T) {
	pluginDir, dataDir := t.TempDir(), t.TempDir()
	entry := filepath.Join(pluginDir, "world-menu")
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
	manifest := `{"id":"world-menu","name":"world-menu","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestWorldMenuPluginProcess$","world-menu-fixture"]}`
	if err = os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: dataDir, EngineFile: "../../../public/engine.wasm", PluginDir: pluginDir, PluginTimeout: time.Second, InfiniteWorlds: true})
	if err != nil {
		t.Fatal(err)
	}
	s.accounts.hashCost = bcrypt.MinCost
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		h.Close()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	user := accountFixture(t, h.URL, "WorldMenuTester")
	events := func() []*api.Event {
		raw, err := os.ReadFile(filepath.Join(dataDir, "plugins", "world-menu", "events.jsonl"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		var out []*api.Event
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var event api.Event
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			out = append(out, &event)
		}
		return out
	}
	post := func(body string, status int) map[string]string {
		t.Helper()
		req, _ := http.NewRequest("POST", h.URL+"/api/rooms", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(user.Cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]string
		if err = json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("status=%d want=%d result=%v", res.StatusCode, status, result)
		}
		return result
	}
	post(`{"mode":"unknown"}`, 422)
	if len(events()) != 0 {
		t.Fatal("invalid request emitted creation events")
	}
	post(`{"name":"protected"}`, 403)
	post(`{"name":"invalid"}`, 422)
	if len(events()) != 2 {
		t.Fatal("cancelled or invalid plugin mutation emitted success")
	}
	result := post(`{"name":"rename","mode":"creative"}`, 201)
	log := events()
	if len(log) != 4 || log[2].Name != api.WorldCreating || log[3].Name != api.WorldCreated || log[3].World.ID != result["id"] {
		t.Fatal("missing before / committed event pair", log)
	}
	var saved Room
	raw, err := os.ReadFile(filepath.Join(dataDir, "rooms", result["id"]+".json"))
	if err != nil {
		t.Fatal("success emitted without persisted world", err)
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Name != "插件命名的世界" || saved.Mode != "creative" || saved.OwnerAccount != user.ID || !saved.Infinite {
		t.Fatal("creation lost mode, owner, infinite generation or mutable name", saved.Name, saved.Mode)
	}
	rooms := filepath.Join(dataDir, "rooms")
	if err = os.Rename(rooms, rooms+".saved"); err != nil {
		t.Fatal(err)
	}
	restore := func() { os.Remove(rooms); os.Rename(rooms+".saved", rooms) }
	defer restore()
	if err = os.WriteFile(rooms, []byte("simulate unavailable storage"), 0600); err != nil {
		t.Fatal(err)
	}
	post(`{"name":"save-failure"}`, 503)
	if len(events()) != 5 || events()[4].Name != api.WorldCreating {
		t.Fatal("save failure emitted success")
	}
}
