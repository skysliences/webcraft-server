package game

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func operatorApp(t *testing.T, testCase string) (*Server, *httptest.Server, Config) {
	t.Helper()
	cfg := singleWorldConfig(t, testCase)
	cfg.AllowedOrigins = []string{"http://127.0.0.1:8080"}
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
	return s, h, cfg
}

func TestOperatorsMultiplayerModesAndRestart(t *testing.T) {
	s, h, cfg := operatorApp(t, "")
	id := s.worldBinding.WorldID
	if s.ops.Settings.DefaultGameMode != "survival" || len(s.ops.Settings.Operators) != 0 {
		t.Fatal("fresh server must start in survival without an OP")
	}
	a, ac := registerTestUser(t, h.URL, "operatorone", "")
	b, bc := registerTestUser(t, h.URL, "playertwo", "")
	setTestName(t, h.URL, ac, "OperatorOne")
	setTestName(t, h.URL, bc, "PlayerTwo")
	aw, welcome := authConnect(t, h.URL, id, "ForgedClientSID", "", ac)
	bw, second := authConnect(t, h.URL, id, "ForgedClientSID", "", bc)
	for _, w := range []map[string]any{welcome, second} {
		if w["op"] != false || w["canGamemode"] != false || w["room"].(map[string]any)["mode"] != "survival" {
			t.Fatal("incorrect fresh-player privileges", w)
		}
		for _, slot := range w["st"].(map[string]any)["inv"].(map[string]any)["s"].([]any) {
			if slot != nil {
				t.Fatal("survival inventory must be empty")
			}
		}
	}
	s.mu.Lock()
	r := s.rooms[id]
	var ap, bp *Peer
	for p := range r.Peers {
		if p.sid == a {
			ap = p
		}
		if p.sid == b {
			bp = p
		}
	}
	if ap == nil || bp == nil {
		t.Fatal("players were not admitted to the same server")
	}
	ap.owner = true // Legacy room ownership must not elevate a single-world user.
	if s.executeCommand(ap, "/gamemode creative") || s.gameMode(ap) != "survival" || s.executeCommand(ap, "/op PlayerTwo") {
		t.Fatal("non-OP used a privileged command")
	}
	ap.owner = false
	item := customStack(s.id("Diamond Sword"), 1)
	bp.state.Inv.Slots[0] = copyStack(item)
	s.mu.Unlock()
	if _, err := s.Console("op OperatorOne"); err != nil {
		t.Fatal(err)
	}
	if msg := receive(t, aw, "permissions"); msg["op"] != true || msg["canGamemode"] != true {
		t.Fatal("grant did not synchronize", msg)
	}
	if msg := receive(t, bw, "permissions"); msg["op"] != false || msg["canGamemode"] != false {
		t.Fatal("other player was elevated", msg)
	}
	s.mu.Lock()
	if !s.executeCommand(ap, "/gamemode c PlayerTwo") || s.gameMode(bp) != "creative" || s.gameMode(ap) != "survival" {
		t.Fatal("OP cannot switch target independently")
	}
	if !reflect.DeepEqual(bp.state.Inv.Slots[0], item) {
		t.Fatal("mode switch changed item metadata")
	}
	if s.setPlayerGameMode(bp, "invalid", a, "command") {
		t.Fatal("invalid mode committed")
	}
	if !s.executeCommand(ap, "/gamemode survival PlayerTwo") || bp.ext().GameMode != "survival" {
		t.Fatal("explicit survival must remain persistent")
	}
	bp.state.Fly = 1
	if !s.executeCommand(ap, "/gamemode survival PlayerTwo") || bp.state.Fly != 0 {
		t.Fatal("survival kept flight")
	}
	if !s.executeCommand(ap, "/gamemode creative") {
		t.Fatal("OP cannot switch self")
	}
	s.mu.Unlock()
	if _, err := s.Console("defaultgamemode creative"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if s.gameMode(bp) != "survival" {
		t.Fatal("default mode overwrote an existing player")
	}
	s.mu.Unlock()
	c, cc := registerTestUser(t, h.URL, "playerthree", "")
	setTestName(t, h.URL, cc, "PlayerThree")
	cw, third := authConnect(t, h.URL, id, "ForgedClientSID", "", cc)
	if third["st"].(map[string]any)["ext"].(map[string]any)["gm"] != "creative" || third["op"] != false {
		t.Fatal("default mode was not applied only to new players")
	}
	if _, err := s.Console("deop OperatorOne"); err != nil {
		t.Fatal(err)
	}
	// Skip the two default-mode permission refreshes queued since the grant.
	for {
		msg := receive(t, aw, "permissions")
		if msg["op"] == false {
			if msg["canGamemode"] != false {
				t.Fatal("revoked OP kept mode permission")
			}
			break
		}
	}
	s.mu.Lock()
	if s.executeCommand(ap, "/gamemode survival") || s.gameMode(ap) != "creative" {
		t.Fatal("revoked OP still controls mode")
	}
	s.mu.Unlock()
	aw.Close()
	bw.Close()
	cw.Close()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.worldBinding.WorldID != id || restored.isOperator(a) || restored.ops.Settings.DefaultGameMode != "creative" {
		t.Fatal("settings did not persist")
	}
	if restored.rooms[id].Players[a].Ext.GameMode != "creative" || restored.rooms[id].Players[b].Ext.GameMode != "survival" || restored.rooms[id].Players[c].Ext.GameMode != "creative" || !reflect.DeepEqual(restored.rooms[id].Players[b].Inv.Slots[0], item) {
		t.Fatal("player modes or complete items did not persist")
	}
	var changed *api.Event
	for _, e := range singleWorldEvents(t, cfg.DataDir) {
		if e.Name == api.PlayerGameModeChanged && e.String("target") == b && e.String("gamemode") == "creative" {
			changed = e
		}
		if e.Name == api.PlayerOperatorChanged && e.String("target") == a && e.Data["operator"] == true && (e.Identity == nil || e.Identity.ID != a || !e.Player.Operator) {
			t.Fatal("operator event missing fixed identity or committed privileges")
		}
	}
	if changed == nil || changed.String("actor") != a || changed.String("reason") != "command" || changed.Player.AccountID != b || changed.Player.GameMode != "creative" || !reflect.DeepEqual(changed.Player.Items[0], itemSnapshot(item)) {
		t.Fatal("missing target, actor, mode or item snapshot", changed)
	}
}

func TestOperatorsCancellationSDKAndSaveFailure(t *testing.T) {
	const id = "00112233445566778899aabbccddeeff"
	for _, tc := range []string{"cancel-operator", "cancel-mode", "cancel-default", "save-failure", "sdk"} {
		t.Run(tc, func(t *testing.T) {
			// save-failure is a settings failure, not a world bootstrap failure.
			fixture := tc
			if tc == "save-failure" {
				fixture = ""
			}
			s, _, cfg := operatorApp(t, fixture)
			s.mu.Lock()
			r := s.primaryRoom()
			p := dropTestPlayer(r, 1, 10.5, 60, 10.5)
			p.sid = id
			p.name = "Tester"
			p.ext().GameMode = "survival"
			r.Peers[p] = true
			r.Players[id] = p.state
			defer func() { delete(r.Peers, p); s.mu.Unlock() }()
			s.pluginSource = "plugin:test"
			if tc == "cancel-mode" {
				if s.setPlayerGameMode(p, "creative", "test", "plugin") || s.gameMode(p) != "survival" {
					t.Fatal("cancelled mode committed")
				}
				c := &api.Context{}
				c.SetGameMode(s.event(api.PlayerJoined, r, p, nil), "creative")
				s.applyAction(c.Actions[0])
				if s.gameMode(p) != "survival" {
					t.Fatal("SDK bypassed cancellation")
				}
			} else if tc == "cancel-default" {
				next := s.copySettings()
				next.DefaultGameMode = "creative"
				if s.updateSettings(next, "test", "defaultgamemode") == nil || s.ops.Settings.DefaultGameMode != "survival" {
					t.Fatal("cancelled default committed")
				}
			} else {
				file := filepath.Join(cfg.DataDir, "server.json")
				if tc == "save-failure" {
					if err := os.Rename(file, file+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(file, 0700); err != nil {
						t.Fatal(err)
					}
					defer func() { os.Remove(file); os.Rename(file+".original", file) }()
				}
				c := &api.Context{}
				c.SetOperator(id, true)
				s.applyAction(c.Actions[0])
				if s.isOperator(id) != (tc == "sdk") {
					t.Fatal("SDK grant bypassed rejection or did not commit")
				}
				if tc == "sdk" {
					s.drainActions()
					if !s.isOperator(id) {
						t.Fatal("Monitor action revoked OP")
					}
					s.pluginSource = "plugin:test"
					c.Actions = nil
					c.SetGameMode(s.event(api.PlayerJoined, r, p, nil), "c")
					s.applyAction(c.Actions[0])
					if s.gameMode(p) != "creative" {
						t.Fatal("SDK mode alias failed")
					}
				}
			}
			for _, e := range singleWorldEvents(t, cfg.DataDir) {
				if tc != "sdk" && (e.Name == api.PlayerOperatorChanged || e.Name == api.PlayerGameModeChanged || e.Name == api.ServerDefaultGameModeChanged) {
					t.Fatal("failed operation emitted success", e.Name)
				}
				if tc == "sdk" && (e.Name == api.PlayerOperatorChanged || e.Name == api.PlayerGameModeChanged) && (e.String("actor") != "plugin:test" || e.String("reason") != "plugin" || e.String("target") != id) {
					t.Fatal("wrong plugin action context", e.Data)
				}
			}
		})
	}
}

func TestOperatorsSettingsMigrationAndLegacyPayload(t *testing.T) {
	cfg := singleWorldConfig(t, "")
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	settings := s.copySettings()
	settings.DefaultGameMode = ""
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(settings)
	file := filepath.Join(cfg.DataDir, "server.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backup, err := os.ReadFile(file + ".pre-player-modes-v1.bak")
	if err != nil || string(backup) != string(raw) {
		t.Fatal("pre-upgrade original settings were not backed up", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copySettings()
	next.DefaultGameMode = ""
	if err = s.updateSettings(next, "v1-client", "settings"); err != nil || s.ops.Settings.DefaultGameMode != "survival" {
		t.Fatal("v1 payload cleared default", err)
	}
	next = s.copySettings()
	next.DefaultGameMode = "c"
	if s.updateSettings(next, "test", "settings") == nil {
		t.Fatal("noncanonical settings accepted")
	}
}

func TestOperatorsDelegationOfflineIDAndHardcore(t *testing.T) {
	s, _, cfg := operatorApp(t, "")
	const id = "00112233445566778899aabbccddeeff"
	if _, err := s.Console("OP " + id); err != nil {
		t.Fatal("cannot promote a fixed identity before first visit", err)
	}
	if _, err := s.Console("deop " + id); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.primaryRoom()
	p := dropTestPlayer(r, 1, 10.5, 60, 10.5)
	p.sid = id
	p.ext().GameMode = "survival"
	r.Peers[p] = true
	r.Players[id] = p.state
	defer delete(r.Peers, p)
	next := s.copySettings()
	next.Permissions[id] = PermissionGrant{Allow: []string{"server.gamemode"}}
	if err := s.updateSettings(next, "test", "permission"); err != nil {
		t.Fatal(err)
	}
	if s.isOperator(id) || !s.executeCommand(p, "/gamemode creative") || s.executeCommand(p, "/op "+id) {
		t.Fatal("delegation granted too many or too few privileges")
	}
	next = s.copySettings()
	next.Permissions[id] = PermissionGrant{Allow: []string{"server.*"}, Deny: []string{"server.gamemode"}}
	if err := s.updateSettings(next, "test", "permission"); err != nil {
		t.Fatal(err)
	}
	if s.executeCommand(p, "/gamemode survival") {
		t.Fatal("explicit deny was ignored")
	}
	r.ext().Hardcore = true
	p.state.Dead = true
	if s.setPlayerGameMode(p, "survival", "test", "plugin") {
		t.Fatal("SDK bypassed hardcore death rule")
	}
	if !s.setPlayerGameMode(p, "spectator", "test", "plugin") {
		t.Fatal("hardcore spectator transition rejected")
	}
	var offline bool
	for _, e := range singleWorldEvents(t, cfg.DataDir) {
		if e.Name == api.PlayerOperatorChanged && e.String("target") == id && e.Player == nil {
			offline = true
			if e.Identity == nil || e.Identity.ID != id {
				t.Fatal("offline OP event lost immutable identity")
			}
		}
	}
	if !offline {
		t.Fatal("offline changes were not exposed")
	}
}
