package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGamePluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "game-plugin-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.ChunkPopulating, api.ChunkPopulated} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "vegetation.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(append(raw, '\n'))
			// Monitor changes and cancellation must not influence population.
			e.Data["count"] = 0
			e.Cancel("monitor ignored")
			return err
		})
	}
	p.On(api.ChunkPopulating, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Number("x") == -1 {
			e.Cancel("vegetation protected")
		}
		e.Data["count"] = 0 // This event declares no mutable fields.
		e.Data["blocks"] = []any{}
		return nil
	})
	p.On(api.PlayerDimensionChanging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "dimension_fixture" {
			e.Data["dimension"] = "nether"
			e.Data["x"] = -1
			e.Data["actor"] = "forged"
		}
		return nil
	})
	p.On(api.PlayerDimensionChanged, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
		e.Cancel("ignored monitor")
		e.Data["dimension"] = "nether"
		c.ChangeDimension(e, "nether")
		return nil
	})
	for _, spec := range api.Events() {
		name := spec.Name
		if !strings.Contains(name, "experience") && !strings.Contains(name, "effect") && !strings.Contains(name, "dimension") && !strings.HasPrefix(name, "server.") && !strings.HasPrefix(name, "world.rule") && !strings.HasPrefix(name, "block.physics") && !strings.HasPrefix(name, "projectile.") {
			continue
		}
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "extensions.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			f.Close()
			return err
		})
		if spec.Cancellable {
			p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
				if e.String("reason") == "protected" || e.String("effect") == "slowness" || name == api.PlayerDimensionChanging && e.String("reason") != "dimension_fixture" || name == api.PlayerExperienceChanging && e.Number("amount") == 13 || name == api.ProjectileLaunching && e.Number("damage") == 13 || name == api.BackupCreating && e.String("reason") == "blocked" {
					e.Cancel("extension protected")
				}
				if name == api.PlayerExperienceChanging && e.Number("amount") == 7 {
					e.Data["amount"] = 14
				}
				if name == api.PlayerEffectApplying && e.String("effect") == "speed" {
					e.Data["ticks"] = 40
					e.Data["amplifier"] = 2
					e.Data["undeclared"] = true
				}
				return nil
			})
		}
	}
	for _, name := range []string{api.BlockBreaking, api.BlockBroken, api.BlockPlacing, api.BlockPlaced, api.BlockChanged, api.PlayerAttacking, api.PlayerAttacked, api.PlayerDamaging, api.PlayerDamaged, api.PlayerDying, api.PlayerDead, api.EntityDamaging, api.EntityDamaged, api.EntityDying, api.EntityDead, api.ItemMetaChanging, api.ItemMetaChanged, api.ItemWearing, api.ItemWorn, api.PlayerHealing, api.PlayerHealed, api.PlayerAttributesChanged, api.InventoryCreativeChanging, api.InventoryCreativeChanged, api.InventoryChanging, api.InventoryChanged, api.InventoryClicking, api.InventoryClicked, api.InventoryDragging, api.InventoryDragged} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			closeErr := f.Close()
			if err != nil {
				return err
			}
			return closeErr
		})
	}
	for _, name := range []string{api.InventoryChanging, api.InventoryClicking, api.InventoryDragging} {
		name := name
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if drag := e.InventoryDrag(); drag != nil {
				mode, _ := drag.CursorBefore.Meta.CustomData["test:mode"].(string)
				if name == api.InventoryChanging && mode == "cancel_inventory" || name == api.InventoryClicking && mode == "cancel_click" || name == api.InventoryDragging && mode == "cancel_drag" {
					e.Cancel("drag protected")
				}
				e.Data["button"] = 1
				e.Data["cursorAfter"] = nil
				e.Data["changes"] = []any{}
			}
			return nil
		})
	}
	p.On(api.InventoryDragging, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
		e.Cancel("monitor ignored")
		e.Data["slots"] = []int{}
		return nil
	})
	p.On(api.InventoryCreativeChanging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		switch int(e.Number("slot")) {
		case 1:
			e.Cancel("creative protected")
		case 2:
			e.Data["count"] = 2
		case 3:
			e.Data["count"] = 1.5
		case 4:
			e.Data["count"] = 65
		}
		e.Data["item"] = 1 // Undeclared changes are ignored by the host.
		return nil
	})
	p.On(api.InventoryCreativeChanging, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
		e.Data["count"] = 0 // Monitor cannot modify or cancel a transaction.
		e.Cancel("monitor ignored")
		return nil
	})
	p.On(api.InventoryChanging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if strings.HasPrefix(e.String("reason"), "creative.") && e.Number("slot") == 5 {
			e.Cancel("inventory protected")
		}
		return nil
	})
	p.On(api.PlayerAttacking, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Damage != nil && e.Damage.Weapon != nil && e.Damage.Weapon.Meta != nil {
			mode, _ := e.Damage.Weapon.Meta.CustomData["test:mode"].(string)
			if mode == "cancel_attack" {
				e.Cancel("attack blocked")
			} else if mode == "scale_attack" {
				e.Data["amount"] = 4
			}
		}
		return nil
	})
	p.On(api.ItemMetaChanging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if v := e.Item("after"); v != nil && v.Meta != nil && v.Meta.DisplayName == "blocked" {
			e.Cancel("metadata blocked")
		}
		return nil
	})
	p.On(api.ItemWearing, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if v := e.Item("stack"); v != nil && v.Meta != nil && v.Meta.CustomData["test:cancel_wear"] == true {
			e.Cancel("wear blocked")
		}
		return nil
	})
	p.On(api.PlayerHealing, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "cancel_heal" {
			e.Cancel("heal blocked")
		} else if e.String("reason") == "scale_heal" {
			e.Data["amount"] = 2
		}
		return nil
	})
	for _, name := range []string{api.WorldTimeChanging, api.EntitySpawning, api.EntityDespawning, api.EntityDying, api.EntityAttacking} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if e.String("reason") == "protected" {
				e.Cancel("entity protection")
			}
			return nil
		})
	}
	p.On(api.EntityDamaging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "immune" {
			e.Cancel("immune mob")
		} else if e.String("reason") == "modify" {
			e.Data["amount"] = 2
		}
		return nil
	})
	for _, name := range []string{api.BlockBreaking, api.BlockPlacing, api.ItemCrafting, api.ItemConsuming, api.InventoryOpening, api.FurnaceSmelting, api.PlayerRespawning} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error { e.Cancel("test protection"); return nil })
	}
	p.On(api.ItemSpawning, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "cancel_spawn" {
			e.Cancel("test")
		} else if e.String("reason") == "modify_drop" {
			e.Data["count"] = 2
		}
		return nil
	})
	p.On(api.ItemPickingUp, api.Subscription{}, func(c *api.Context, e *api.Event) error { e.Cancel("protected item"); return nil })
	p.On(api.ItemDespawning, api.Subscription{}, func(c *api.Context, e *api.Event) error { e.Cancel("keep item"); return nil })
	p.On(api.PlayerChatting, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		e.Data["message"] = "plugin:" + e.String("message")
		return nil
	})
	p.On(api.PlayerDamaging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "immune" {
			e.Cancel("immune")
		} else {
			e.Data["amount"] = 2
		}
		return nil
	})
	p.On(api.PlayerDying, api.Subscription{}, func(c *api.Context, e *api.Event) error { e.Cancel("no death"); return nil })
	p.On(api.PlayerMoving, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Number("x") < 5 {
			e.Cancel("edge protected")
		}
		return nil
	})
	p.Command(api.Command{Name: "gift", Aliases: []string{"reward"}}, func(c *api.Context, e *api.Event, args []string) error {
		c.GiveItem(e, "Bread", 3)
		c.Message(e, "gift queued")
		return nil
	})
	p.Command(api.Command{Name: "admin", Permission: "owner"}, func(c *api.Context, e *api.Event, args []string) error { c.Message(e, "admin success"); return nil })
	p.Command(api.Command{Name: "warp"}, func(c *api.Context, e *api.Event, args []string) error { c.Teleport(e, 20.5, 80, 20.5); return nil })
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func pluginApp(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "test")
	os.MkdirAll(entry, 0700)
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": "test", "name": "test", "version": "1", "apiVersion": 1, "entry": "plugin", "args": []string{"-test.run=^TestGamePluginProcess$", "game-plugin-fixture"}})
	os.WriteFile(filepath.Join(entry, "plugin.json"), raw, 0600)
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: dir, PluginTimeout: time.Second, MaxPlayers: 4, AllowedOrigins: []string{"http://127.0.0.1:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.plugins.Failed() != 0 {
		t.Fatal(s.plugins.Status())
	}
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, httpServer
}
func TestPluginCancelledSurvivalTransactions(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("testworld1234567x", "test", "survival", 0)
	p := &Peer{room: r, id: 1, builder: "uid", send: make(chan packet, 256), state: &Player{X: 10.5, Y: 60, Z: 10.5, Health: 20, Food: 10, Inv: &Inventory{Slots: make(Stacks, 46)}}, known: map[int]bool{}}
	logID, plank, bread := s.rules.byName["Oak Log"], s.rules.byName["Oak Planks"], s.rules.byName["Bread"]
	p.state.Inv.Slots[0] = stack(plank, 5, 0)
	key := blockKey(12, 61, 10)
	r.Edits[key] = logID
	p.mining = &digState{X: 12, Y: 61, Z: 10, Block: logID, Held: plank, At: time.Now().Add(-time.Hour)}
	s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 61, Z: 10, F: 1})
	if r.Edits[key] != logID || p.state.Inv.Slots[0].Count != 5 {
		t.Fatal("cancelled mining mutated state")
	}
	r.Edits[blockKey(12, 59, 10)] = plank
	r.Edits[blockKey(12, 60, 10)] = 0
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
	if r.Edits[blockKey(12, 60, 10)] != 0 || p.state.Inv.Slots[0].Count != 5 {
		t.Fatal("cancelled placement consumed item")
	}
	p.state.Inv.Slots[41] = stack(logID, 1, 0)
	s.click(p, Message{S: 45, M: 1})
	if p.state.Inv.Slots[41].Count != 1 || p.state.Cursor != nil || p.state.Inv.Slots[0].Count != 5 {
		t.Fatal("cancelled craft changed inventory")
	}
	p.state.Inv.Slots[0] = stack(bread, 5, 0)
	p.useAt = time.Now().Add(-2 * time.Second)
	p.useSlot = 0
	s.survivalMessage(p, Message{T: "use", S: 2})
	if p.state.Food != 10 || p.state.Inv.Slots[0].Count != 5 {
		t.Fatal("cancelled eating changed hunger/items")
	}
	s.openWindow(p, key, "chest", 0)
	if p.window != nil || r.Containers[key] != nil {
		t.Fatal("cancelled open created container")
	}
	c := &Container{Kind: "furnace", Slots: Stacks{stack(s.rules.byName["Iron Ore"], 1, 0), nil, nil}, Burn: 10, Progress: 198}
	s.furnaceTick(r, c, key)
	if c.Slots[0].Count != 1 || c.Slots[2] != nil {
		t.Fatal("cancelled smelt consumed input")
	}
	s.damage(p, 10, "immune")
	if p.state.Health != 20 {
		t.Fatal("cancelled damage changed health")
	}
	s.damage(p, 10, "modify")
	if p.state.Health != 18 {
		t.Fatal("modified damage not applied")
	}
	p.state.Health = 1
	s.damage(p, 10, "modify")
	if p.state.Dead || p.state.Health != 1 {
		t.Fatal("cancelled death did not restore transaction")
	}
	p.state.Dead = true
	s.survivalMessage(p, Message{T: "respawn"})
	if !p.state.Dead || p.state.X != 10.5 {
		t.Fatal("cancelled respawn changed player")
	}
}
func TestPluginWebsocketCommandsChatAndActions(t *testing.T) {
	s, h := pluginApp(t)
	id, key := postRoom(t, h.URL, "survival")
	conn, welcome := connect(t, h.URL, id, "PluginPlayer1234", key)
	send(t, conn, map[string]any{"t": "chat", "m": "/reward"})
	receive(t, conn, "sinv")
	if receive(t, conn, "sys")["m"] != "gift queued" {
		t.Fatal("plugin command missing")
	}
	s.mu.Lock()
	var p *Peer
	for peer := range s.rooms[id].Peers {
		p = peer
	}
	if p.state.Inv.Slots[0].Count != 11 {
		s.mu.Unlock()
		t.Fatal("queued reward not applied")
	}
	s.mu.Unlock()
	send(t, conn, map[string]any{"t": "chat", "m": "hello"})
	if receive(t, conn, "chat")["m"] != "plugin:hello" {
		t.Fatal("chat not modified")
	}
	guest, _ := connect(t, h.URL, id, "PluginGuest1234", "")
	send(t, guest, map[string]any{"t": "chat", "m": "/admin"})
	if !strings.Contains(receive(t, guest, "sys")["m"].(string), "房主权限") {
		t.Fatal("owner permission not enforced")
	}
	send(t, conn, map[string]any{"t": "chat", "m": "/warp"})
	placed := receive(t, conn, "place")
	if placed["st"].(map[string]any)["x"] != 20.5 {
		t.Fatal("teleport action failed")
	}
	receive(t, conn, "synced")
	for _, endpoint := range []string{"plugins", "events"} {
		res, err := http.Get(h.URL + "/api/" + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatal(endpoint, res.StatusCode)
		}
		var result map[string]any
		json.NewDecoder(res.Body).Decode(&result)
		res.Body.Close()
		if result["apiVersion"] != float64(1) {
			t.Fatal(result)
		}
	}
	// Creative slash commands must reach the same server dispatcher.
	creative, owner := postRoom(t, h.URL, "creative")
	cc, _ := connect(t, h.URL, creative, "PluginCreative12", owner)
	send(t, cc, map[string]any{"t": "chat", "m": "/admin"})
	if receive(t, cc, "sys")["m"] != "admin success" {
		t.Fatal("creative command failed")
	}
	_ = welcome
}
