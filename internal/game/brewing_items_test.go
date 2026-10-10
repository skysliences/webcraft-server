package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBrewingItemPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "brewing-items-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.BottleFilling, api.BottleFilled, api.ItemConsuming, api.ItemConsumed, api.InventoryChanging, api.InventoryChanged, api.PlayerEffectApplying, api.PlayerEffectApplied, api.PlayerEffectRemoving, api.PlayerEffectRemoved, api.PlayerHealing, api.PlayerHealed, api.PlayerDamaging, api.PlayerDamaged, api.PlayerDying, api.PlayerDead} {
		if strings.HasSuffix(name, ".before") {
			p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
				if e.Source == "plugin:cancel-"+e.Name {
					e.Cancel("protected")
				}
				if e.Name == api.PlayerEffectApplying && e.Source == "plugin:modify-drink" {
					e.Data["ticks"] = 40
					e.Data["amplifier"] = 2
				}
				if e.Name == api.PlayerEffectApplying && e.Source == "plugin:invalid-drink" {
					e.Data["ticks"] = "invalid"
				}
				e.Data["stack"] = map[string]any{"id": 1, "count": 64}
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
			e.Data["ticks"] = 1
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func brewingItemPluginApp(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "brewing-items-test")
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
	manifest := `{"id":"brewing-items-test","name":"brewing-items-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestBrewingItemPluginProcess$","brewing-items-fixture"]}`
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
func brewingItemEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "brewing-items-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var result []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid event log")
		}
		result = append(result, &e)
	}
	return result
}
func TestPlayerBottleFillMetadataCancellationAndCapacity(t *testing.T) {
	s := brewingItemPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range []string{"", api.BottleFilling, api.InventoryChanging} {
		r, p := dropTestWorld(s)
		p.state.Inv.Slots[0] = customStack(itemGlassBottle, 3)
		before := cloneStacks(p.state.Inv.Slots)
		r.Edits[blockKey(12, 60, 10)] = blkWaterSource
		s.pluginSource = ""
		if cancel != "" {
			s.pluginSource = "plugin:cancel-" + cancel
		}
		start := len(brewingItemEvents(t, s))
		s.fillPlayerBottle(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1}, blkWaterSource)
		if cancel != "" {
			if !reflect.DeepEqual(before, p.state.Inv.Slots) {
				t.Fatal("protected fill changed inventory")
			}
		} else {
			if p.state.Inv.Slots[0].Count != 2 || p.state.Inv.Slots[1] == nil || p.state.Inv.Slots[1].ID != itemWaterBottle || !reflect.DeepEqual(p.state.Inv.Slots[1].Meta, before[0].Meta) || s.block(r, 12, 60, 10) != blkWaterSource {
				t.Fatal("fill split lost metadata/source water")
			}
		}
		completed := 0
		for _, e := range brewingItemEvents(t, s)[start:] {
			if e.Name == api.BottleFilled {
				completed++
				if e.Item("stack").ID != itemGlassBottle || !reflect.DeepEqual(e.Item("result").Meta, before[0].Meta) || e.Data["target"] == nil || len(dispenserOutputSlots(e, "inventoryAfter")) != 46 {
					t.Fatal("fill event lost complete metadata/inventory")
				}
			}
		}
		if cancel != "" && completed != 0 || cancel == "" && completed != 1 {
			t.Fatal("wrong bottle fill success count")
		}
	}
	s.pluginSource = ""
	r, p := dropTestWorld(s)
	r.Edits[blockKey(12, 60, 10)] = blkWaterSource
	p.state.Inv.Slots[0] = customStack(itemGlassBottle, 3)
	for i := 1; i < 36; i++ {
		p.state.Inv.Slots[i] = stack(1, 64, 0)
	}
	before := cloneStacks(p.state.Inv.Slots)
	s.fillPlayerBottle(p, Message{X: 12, Y: 60, Z: 10}, blkWaterSource)
	if !reflect.DeepEqual(before, p.state.Inv.Slots) {
		t.Fatal("full inventory lost bottle")
	}
}
func TestPlayerDrinkRegisteredItemsMetadataAndEvents(t *testing.T) {
	s := brewingItemPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range []int{401, 402, 403, 404, 405, 406, 407, 408, 409, 410, itemMilkBucket} {
		r, p := dropTestWorld(s)
		p.state.Health = 10
		p.state.Inv.Slots[0] = customStack(id, 1)
		original := copyStack(p.state.Inv.Slots[0])
		if id == itemMilkBucket {
			p.ext().Effects = map[string]*Effect{"speed": {Ticks: 100, Amp: 0}}
		}
		if !s.validStack(original, s.rules.max(id)) {
			t.Fatalf("drink id %d remains unregistered", id)
		}
		start := len(brewingItemEvents(t, s))
		s.drink(p, original)
		expected := itemGlassBottle
		if id == itemMilkBucket {
			expected = itemBucket
		}
		v := p.state.Inv.Slots[0]
		if v == nil || v.ID != expected || !reflect.DeepEqual(v.Meta, original.Meta) {
			t.Fatalf("drink %d lost return item metadata", id)
		}
		found := false
		for _, e := range brewingItemEvents(t, s)[start:] {
			if e.Name == api.ItemConsumed {
				found = true
				if e.Item("stack").ID != id || e.Item("result").ID != expected || !reflect.DeepEqual(e.Item("stack").Meta, original.Meta) || len(dispenserOutputSlots(e, "inventoryBefore")) != 46 || len(dispenserOutputSlots(e, "inventoryAfter")) != 46 {
					t.Fatal("drink event lacks snapshots")
				}
			}
		}
		if !found {
			t.Fatal("missing successful consume")
		}
		_ = r
	}
}
func TestPlayerDrinkProtectionEffectsAndFatalPotionAtomic(t *testing.T) {
	s := brewingItemPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tc := range []struct {
		id    int
		event string
	}{{405, api.ItemConsuming}, {405, api.InventoryChanging}, {405, api.PlayerEffectApplying}, {403, api.PlayerHealing}, {409, api.PlayerDamaging}, {409, api.PlayerDying}, {itemMilkBucket, api.PlayerEffectRemoving}} {
		_, p := dropTestWorld(s)
		p.state.Health = 4
		p.state.Inv.Slots[0] = customStack(tc.id, 1)
		if tc.id == itemMilkBucket {
			p.ext().Effects = map[string]*Effect{"speed": {Ticks: 100, Amp: 0}}
		}
		before := cloneStacks(p.state.Inv.Slots)
		s.pluginSource = "plugin:cancel-" + tc.event
		start := len(brewingItemEvents(t, s))
		s.drink(p, s.heldItem(p))
		if !reflect.DeepEqual(before, p.state.Inv.Slots) || p.state.Health != 4 || p.state.Dead {
			t.Fatalf("%s veto committed drink/effect", tc.event)
		}
		for _, e := range brewingItemEvents(t, s)[start:] {
			if e.Name == api.ItemConsumed || e.Name == api.InventoryChanged || e.Name == api.PlayerEffectApplied || e.Name == api.PlayerEffectRemoved || e.Name == api.PlayerDamaged || e.Name == api.PlayerHealed {
				t.Fatalf("%s veto published success %s", tc.event, e.Name)
			}
		}
	}
	s.pluginSource = "plugin:modify-drink"
	_, p := dropTestWorld(s)
	p.state.Inv.Slots[0] = customStack(405, 1)
	s.drink(p, s.heldItem(p))
	if p.ext().Effects["speed"].Ticks != 40 || p.ext().Effects["speed"].Amp != 2 {
		t.Fatal("mutable potion effect ignored")
	}
	s.pluginSource = "plugin:invalid-drink"
	_, p = dropTestWorld(s)
	p.state.Inv.Slots[0] = customStack(405, 1)
	before := cloneStacks(p.state.Inv.Slots)
	s.drink(p, s.heldItem(p))
	if !reflect.DeepEqual(before, p.state.Inv.Slots) || s.hasEffect(p, "speed") {
		t.Fatal("invalid effect mutation committed")
	}
	s.pluginSource = ""
	r, p := dropTestWorld(s)
	r.ext().Rules = map[string]bool{"keepInventory": false}
	p.state.Health = 4
	p.state.Inv.Slots[0] = customStack(409, 1)
	original := copyStack(p.state.Inv.Slots[0])
	s.drink(p, s.heldItem(p))
	if !p.state.Dead || p.state.Health != 0 || stackCount(p, 409) != 0 || stackCount(p, itemGlassBottle) != 0 {
		t.Fatal("fatal drink revived/duplicated inventory")
	}
	glass, potion := 0, 0
	for _, d := range r.Drops {
		if d.Stack.ID == itemGlassBottle {
			glass += d.Stack.Count
			if !reflect.DeepEqual(d.Stack.Meta, original.Meta) {
				t.Fatal("fatal returned bottle lost metadata")
			}
		}
		if d.Stack.ID == 409 {
			potion += d.Stack.Count
		}
	}
	if glass != 1 || potion != 0 {
		t.Fatalf("fatal drink dropped glass=%d potion=%d", glass, potion)
	}
}
