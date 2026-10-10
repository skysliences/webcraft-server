package game

import (
	"bytes"
	"encoding/json"
	"math"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCommandSystemAndSelectors(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ap := createAdvancedTestWorld(s)
	ap.name = "AdminPlayer"
	ap.sid = "adminuser"

	np := dropTestPlayer(r, 2, 15.5, 61, 15.5)
	np.name = "NormalPlayer"
	np.sid = "normaluser"
	r.Peers[np] = true

	adminID := ap.sid
	normalID := np.sid
	_ = normalID

	next := s.copySettings()
	if next.Operators == nil {
		next.Operators = map[string]bool{}
	}
	next.Operators[adminID] = true
	if err := s.updateSettings(next, "test", "command_fixture"); err != nil {
		t.Fatal(err)
	}
	// 1. 测试 /help 指令
	if !s.executeCommand(ap, "/help") {
		t.Fatal("/help command should be handled")
	}
	if !s.executeCommand(ap, "/help time") {
		t.Fatal("/help time command should be handled")
	}

	// 2. 普通玩家尝试使用管理员命令应被拦截
	if s.executeCommand(np, "/op NormalPlayer") {
		t.Fatal("non-operator should not be able to run /op")
	}
	if s.executeCommand(np, "/gamemode creative") {
		t.Fatal("non-operator should not be able to change gamemode")
	}

	// 3. /time 指令测试
	if !s.executeCommand(ap, "/time set day") {
		t.Fatal("/time set day failed")
	}
	if r.root().Time%24000 != 1000 {
		t.Fatalf("expected Time 1000, got %d", r.root().Time)
	}

	if !s.executeCommand(ap, "/time set night") {
		t.Fatal("/time set night failed")
	}
	if r.root().Time%24000 != 13000 {
		t.Fatalf("expected Time 13000, got %d", r.root().Time)
	}

	// 4. /heal 与 /feed 指令测试
	np.state.Health = 5
	np.state.Food = 2

	if !s.executeCommand(ap, "/heal NormalPlayer") {
		t.Fatal("/heal NormalPlayer failed")
	}
	if np.state.Health != 20 {
		t.Fatalf("expected NormalPlayer health 20, got %f", np.state.Health)
	}

	if !s.executeCommand(ap, "/feed NormalPlayer") {
		t.Fatal("/feed NormalPlayer failed")
	}
	if np.state.Food != 20 {
		t.Fatalf("expected NormalPlayer food 20, got %d", np.state.Food)
	}

	// 5. 目标选择器测试 (@s, @p, @a)
	ap.state.Health = 10
	if !s.executeCommand(ap, "/heal @s") {
		t.Fatal("/heal @s failed")
	}
	if ap.state.Health != 20 {
		t.Fatalf("expected AdminPlayer health 20 after /heal @s, got %f", ap.state.Health)
	}

	// 6. /say 指令测试
	if !s.executeCommand(ap, "/say Hello world from server") {
		t.Fatal("/say command failed")
	}

	// 7. /clear 指令测试
	np.state.Inv.Slots[0] = &Stack{ID: 1, Count: 64}
	if !s.executeCommand(ap, "/clear NormalPlayer") {
		t.Fatal("/clear NormalPlayer failed")
	}
	for i := 0; i < 36; i++ {
		if np.state.Inv.Slots[i] != nil {
			t.Fatalf("expected slot %d to be cleared", i)
		}
	}

	// 8. /seed 与 /worldinfo 测试
	if !s.executeCommand(ap, "/seed") {
		t.Fatal("/seed command failed")
	}
	if !s.executeCommand(ap, "/worldinfo") {
		t.Fatal("/worldinfo command failed")
	}

	// 9. /op 与 /deop 指令测试
	if !s.executeCommand(ap, "/op NormalPlayer") {
		t.Fatal("/op NormalPlayer failed")
	}
	if !s.ops.Settings.Operators[np.sid] {
		t.Fatal("NormalPlayer should now be OP")
	}

	if !s.executeCommand(ap, "/deop NormalPlayer") {
		t.Fatal("/deop NormalPlayer failed")
	}
	if s.ops.Settings.Operators[np.sid] {
		t.Fatal("NormalPlayer should no longer be OP")
	}
}

func TestUnderwaterAirDepletionAndDrowning(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, dp := createAdvancedTestWorld(s)
	dp.name = "Diver"
	dp.sid = "diver_sid_123"

	dp.state.Health = 20
	dp.air = 300
	dp.state.X = 10.5
	dp.state.Y = 60.0
	dp.state.Z = 10.5
	dp.peakY = 60
	dp.lastHealth = time.Now()
	r.Edits[blockKey(10, 59, 10)] = 1
	r.Edits[blockKey(10, 60, 10)] = 0

	// 在玩家眼睛所在方块放置水方块 (ID: 9)
	eyesY := int(math.Floor(dp.state.Y + 1.62))
	s.setBlock(r, 10, eyesY, 10, 9, 0)

	// 触发一次生命周期 tick (模拟 1 秒浸水)
	s.life(dp)

	if dp.air != 280 {
		t.Fatalf("expected air to decrease to 280, got %d", dp.air)
	}
	if dp.ext().Air != 280 {
		t.Fatalf("expected ext().Air to be 280, got %d", dp.ext().Air)
	}

	// 模拟氧气耗尽并受到溺水伤害
	dp.air = 10
	s.life(dp)

	if dp.air != 0 {
		t.Fatalf("expected air to be 0, got %d", dp.air)
	}
	if dp.state.Health >= 20 {
		t.Fatalf("expected health to decrease due to drowning, got %f", dp.state.Health)
	}

	// 离开水面，方块变为空气 (ID: 0)，氧气恢复
	s.setBlock(r, 10, eyesY, 10, 0, 0)
	s.life(dp)

	if dp.air <= 0 {
		t.Fatalf("expected air to replenish after leaving water, got %d", dp.air)
	}
}

func TestProtectedCommandPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "protected-command-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.CommandExecuting, api.CommandExecuted, api.PlayerFoodChanging, api.PlayerFoodChanged, api.InventoryChanging, api.InventoryChanged, api.PlayerHealing, api.PlayerHealed, api.PlayerEffectRemoving, api.PlayerEffectRemoved} {
		if strings.HasSuffix(name, ".before") {
			p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
				if e.Source == "plugin:cancel-"+e.Name {
					e.Cancel("command protected")
				}
				if e.Name == api.PlayerHealing && e.Source == "plugin:command-heal-scale" {
					e.Data["amount"] = 3
				}
				if e.Name == api.PlayerHealing && e.Source == "plugin:command-heal-invalid" {
					e.Data["amount"] = "invalid"
				}
				e.Data["foodAfter"], e.Data["actor"], e.Data["inventoryAfter"] = 999, "forged", nil
				return nil
			})
		}
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(b, '\n'))
			f.Close()
			e.Cancel("monitor ignored")
			e.Data["amount"] = 0
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func protectedCommandApp(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "protected-command")
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"protected-command","name":"protected-command","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestProtectedCommandPluginProcess$","protected-command-fixture"]}`
	if err = os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: dir, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func protectedCommandEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "protected-command", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var result []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		result = append(result, &e)
	}
	return result
}
func protectedCommandPlayers(s *Server) (*Room, *Peer, *Peer) {
	r, actor := dropTestWorld(s)
	actor.owner = true
	actor.name = "AdminPlayer"
	actor.sid = "11223344556677889900112233445566"
	target := dropTestPlayer(r, 2, 12.5, 60, 10.5)
	target.name = "NormalPlayer"
	target.sid = "22334455667788990011223344556677"
	r.Peers[target] = true
	target.state.Health = 5
	target.state.Food = 3
	target.state.Saturation = 1
	target.state.Exhaustion = 8
	target.air = 50
	target.ext().Air = 50
	target.ext().FireTicks = 100
	target.ext().Effects = map[string]*Effect{"poison": {Ticks: 100, Amp: 0}}
	target.state.Inv.Slots[0] = customStack(276, 1)
	r.Dirty = false
	return r, actor, target
}
func TestCommandsProtectionAtomicStateMetadataAndSuccess(t *testing.T) {
	s := protectedCommandApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tc := range []struct{ command, event string }{{"/heal NormalPlayer", api.CommandExecuting}, {"/heal NormalPlayer", api.PlayerFoodChanging}, {"/heal NormalPlayer", api.PlayerEffectRemoving}, {"/heal NormalPlayer", api.PlayerHealing}, {"/feed NormalPlayer", api.PlayerFoodChanging}, {"/clear NormalPlayer", api.InventoryChanging}} {
		r, actor, target := protectedCommandPlayers(s)
		target.state.Cursor = customStack(259, 1)
		target.state.Recovery = Stacks{customStack(1, 4)}
		before, _ := json.Marshal(target.state)
		s.pluginSource = "plugin:cancel-" + tc.event
		start := len(protectedCommandEvents(t, s))
		if s.executeCommand(actor, tc.command) {
			t.Fatalf("%s veto reported success", tc.event)
		}
		after, _ := json.Marshal(target.state)
		if !bytes.Equal(before, after) || r.Dirty {
			t.Fatalf("%s veto changed state/metadata/dirty", tc.event)
		}
		for _, e := range protectedCommandEvents(t, s)[start:] {
			if e.Name == api.CommandExecuted || e.Name == api.PlayerFoodChanged || e.Name == api.InventoryChanged || e.Name == api.PlayerHealed || e.Name == api.PlayerEffectRemoved {
				t.Fatalf("cancelled command emitted %s", e.Name)
			}
		}
	}
	r, actor, target := protectedCommandPlayers(s)
	original := copyStack(target.state.Inv.Slots[0])
	s.pluginSource = "plugin:command-heal-scale"
	start := len(protectedCommandEvents(t, s))
	if !s.executeCommand(actor, "/heal NormalPlayer") || target.state.Health != 8 || target.state.Food != 20 || target.state.Saturation != 5 || target.state.Exhaustion != 0 || target.air != 300 || target.ext().FireTicks != 0 || len(target.ext().Effects) != 0 || !r.Dirty || !reflect.DeepEqual(original, target.state.Inv.Slots[0]) {
		t.Fatal("protected heal ignored mutable amount or lost coupled state/meta")
	}
	food, healed, complete := 0, 0, 0
	for _, e := range protectedCommandEvents(t, s)[start:] {
		switch e.Name {
		case api.PlayerFoodChanged:
			food++
			if e.Number("healthAfter") != 8 || e.String("actor") != "plugin:command-heal-scale" || e.Data["target"] == nil || !reflect.DeepEqual(e.Item("stack").Meta, original.Meta) {
				t.Fatal("food event lost actor/target/metadata/actual health")
			}
		case api.PlayerHealed:
			healed++
			if e.Number("amount") != 3 {
				t.Fatal("wrong actual healing completion")
			}
		case api.CommandExecuted:
			complete++
		}
	}
	if food != 1 || healed != 1 || complete != 1 {
		t.Fatal("missing committed command completions")
	}
	s.pluginSource = "plugin:command-heal-invalid"
	r, actor, target = protectedCommandPlayers(s)
	before, _ := json.Marshal(target.state)
	if s.executeCommand(actor, "/heal NormalPlayer") {
		t.Fatal("invalid healing mutation succeeded")
	}
	after, _ := json.Marshal(target.state)
	if !bytes.Equal(before, after) || r.Dirty {
		t.Fatal("invalid healing mutation changed state")
	}
	s.pluginSource = ""
	r, actor, target = protectedCommandPlayers(s)
	if !s.executeCommand(actor, "/feed NormalPlayer") || target.state.Health != 5 || target.state.Food != 20 || target.state.Saturation != 5 || target.ext().FireTicks != 100 || len(target.ext().Effects) != 1 || !r.Dirty {
		t.Fatal("feed altered unrelated health/effects or failed Dirty")
	}
	r, actor, target = protectedCommandPlayers(s)
	target.state.Inv.Slots[1] = customStack(1, 12)
	target.state.Cursor = customStack(259, 1)
	original = copyStack(target.state.Inv.Slots[0])
	cursor := copyStack(target.state.Cursor)
	start = len(protectedCommandEvents(t, s))
	if !s.executeCommand(actor, "/clear NormalPlayer 1") || target.state.Inv.Slots[1] != nil || !reflect.DeepEqual(target.state.Inv.Slots[0], original) || !reflect.DeepEqual(target.state.Cursor, cursor) || !r.Dirty {
		t.Fatal("filtered clear lost metadata/cursor or failed commit")
	}
	found := false
	for _, e := range protectedCommandEvents(t, s)[start:] {
		if e.Name == api.InventoryChanged && e.String("reason") == "command_clear" {
			found = true
			if e.Number("count") != 12 || e.String("actor") != actor.sid || e.Data["target"] == nil || len(dispenserOutputSlots(e, "inventoryBefore")) != 46 || !reflect.DeepEqual(dispenserOutputSlots(e, "inventoryAfter")[0].Meta, original.Meta) {
				t.Fatal("clear completion lost complete metadata snapshots")
			}
		}
	}
	if !found {
		t.Fatal("clear omitted inventory completion")
	}
	beforeSlots := cloneStacks(target.state.Inv.Slots)
	if s.executeCommand(actor, "/clear NormalPlayer invalid_item") || !reflect.DeepEqual(beforeSlots, target.state.Inv.Slots) {
		t.Fatal("unknown clear filter deleted other items")
	}
}
