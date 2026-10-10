package plugins

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The actual test executable also acts as a separate plugin process.
func TestPluginProcess(t *testing.T) {
	mode := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(mode, "fixture:") {
		return
	}
	mode = strings.TrimPrefix(mode, "fixture:")
	p := api.New()
	switch mode {
	case "low":
		p.On(api.PlayerChatting, api.Subscription{Priority: api.Low}, func(c *api.Context, e *api.Event) error {
			e.Data["message"] = e.String("message") + "L"
			e.Data["injected"] = true
			return nil
		})
	case "high":
		p.On(api.PlayerChatting, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
			e.Data["message"] = e.String("message") + "H"
			e.Cancel("denied")
			return nil
		})
	case "monitor":
		p.On(api.PlayerChatting, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			e.Data["message"] = "bad"
			e.Cancel("bad")
			c.Broadcast("world", "bad")
			return nil
		})
	case "timeout":
		p.On(api.BlockPlacing, api.Subscription{}, func(c *api.Context, e *api.Event) error { time.Sleep(time.Second); return nil })
	case "duplicate":
		p.Command(api.Command{Name: "hello"}, func(c *api.Context, e *api.Event, args []string) error { return nil })
	case "commands":
		p.Command(api.Command{Name: "hello", Aliases: []string{"hi"}, Permission: "owner"}, func(c *api.Context, e *api.Event, args []string) error {
			c.Message(e, strings.Join(args, " "))
			return nil
		})
	case "malformed":
		os.Stdout.WriteString("not json\n")
		os.Exit(0)
	case "crash":
		p.Command(api.Command{Name: "crash"}, func(c *api.Context, e *api.Event, args []string) error { os.Exit(0); return nil })
	}
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func fixture(t *testing.T, root, id, mode string, version int) {
	t.Helper()
	dir := filepath.Join(root, id)
	os.MkdirAll(dir, 0700)
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: id, Name: id, Version: "1.0", APIVersion: version, Entry: "plugin", Args: []string{"-test.run=^TestPluginProcess$", "fixture:" + mode}}
	raw, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600)
}
func TestPriorityWhitelistMonitorAndPermissions(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "a_high", "high", 1)
	fixture(t, root, "z_low", "low", 1)
	fixture(t, root, "monitor", "monitor", 1)
	fixture(t, root, "commands", "commands", 1)
	m := Load(root, t.TempDir(), 500*time.Millisecond)
	defer m.Close()
	if m.Failed() != 0 {
		t.Fatal(m.Status())
	}
	e := &api.Event{Name: api.PlayerChatting, Data: map[string]any{"message": "x"}}
	actions := m.Emit(e)
	if e.String("message") != "xLH" || !e.Cancelled || e.Reason != "denied" || e.Data["injected"] != nil || len(actions) != 0 {
		t.Fatalf("event %+v, actions %+v", e, actions)
	}
	event := &api.Event{World: &api.World{ID: "world"}, Player: &api.Player{ID: 1}}
	if _, ok, err := m.Execute("hi", event, nil); !ok || err == nil {
		t.Fatal("guest permission accepted")
	}
	event.Player.Owner = true
	intents, ok, err := m.Execute("hi", event, []string{"hello", "world"})
	if !ok || err != nil || len(intents) != 1 || intents[0].Action.Message != "hello world" {
		t.Fatalf("command %v %v %+v", ok, err, intents)
	}
	event.Player.Owner, event.Player.Operator = false, true
	if _, ok, err := m.Execute("hi", event, nil); !ok || err != nil {
		t.Fatal("OP cannot use legacy owner-permission plugin command", err)
	}
}
func TestTimeoutFailureRetainsProtectionAndBadManifests(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "slow", "timeout", 1)
	fixture(t, root, "bad_api", "low", 2)
	fixture(t, root, "malformed", "malformed", 1)
	m := Load(root, t.TempDir(), 60*time.Millisecond)
	defer m.Close()
	start := time.Now()
	e := &api.Event{Name: api.BlockPlacing}
	m.Emit(e)
	if !e.Cancelled || time.Since(start) > 500*time.Millisecond || m.Failed() != 3 {
		t.Fatalf("timeout %+v %+v", e, m.Status())
	}
	e = &api.Event{Name: api.BlockPlacing}
	m.Emit(e)
	if !e.Cancelled {
		t.Fatal("failed protection silently removed")
	}
}
func TestDuplicateCommandAndCrash(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "commands", "commands", 1)
	fixture(t, root, "duplicate", "duplicate", 1)
	fixture(t, root, "crash", "crash", 1)
	m := Load(root, t.TempDir(), 500*time.Millisecond)
	defer m.Close()
	if m.Failed() != 1 {
		t.Fatal(m.Status())
	}
	if _, ok, err := m.Execute("crash", &api.Event{}, nil); !ok || err == nil {
		t.Fatal("crash not isolated")
	}
	if m.Failed() != 2 {
		t.Fatal(m.Status())
	}
}
