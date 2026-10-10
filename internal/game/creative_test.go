package game

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func TestCreativeInventoryAllRegisteredItems(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.ext().GameMode = "creative"
	for _, it := range s.rules.Items {
		raw, _ := json.Marshal(map[string]any{"t": "cset", "i": 0, "id": it.ID, "n": it.Stack})
		m, err := parseMessage(raw)
		if err != nil {
			t.Fatal(err)
		}
		s.message(p, m)
		v := p.state.Inv.Slots[0]
		if v == nil || v.ID != it.ID || v.Count != it.Stack || v.Meta != nil || v.Damage != 0 {
			t.Fatalf("registered item %s (%d) rejected: %+v", it.Name, it.ID, v)
		}
	}
	s.message(p, Message{T: "cset", ID: 276, N: 1})
	if s.attributes(p)[api.AttackDamage] != 7 {
		t.Fatal("diamond sword attributes inactive")
	}
	s.message(p, Message{T: "cset", I: 1, ID: 298, N: 1})
	s.message(p, Message{T: "sel", I: 1})
	s.message(p, Message{T: "use", S: 0})
	if p.state.Inv.Slots[38] == nil || p.state.Inv.Slots[38].ID != 298 || p.state.Inv.Slots[1] != nil {
		t.Fatal("creative armor cannot be equipped")
	}
	other := dropTestPlayer(r, 2, 11.5, 60, 10.5)
	r.Peers[other] = true
	defer delete(r.Peers, other)
	s.message(p, Message{T: "sel", I: 0})
	s.message(p, Message{T: "attack", P: 2})
	if other.state.Health != 13 {
		t.Fatalf("diamond sword attack failed: hp=%v", other.state.Health)
	}
	for len(p.send) > 0 {
		packet := <-p.send
		if packet.kind == 1 {
			var m map[string]any
			if json.Unmarshal(packet.body, &m) == nil && m["t"] == "sys" {
				t.Fatalf("valid item operation refused: %v", m)
			}
		}
	}
}

func TestCreativeInventoryRejectsInvalidAndUnauthorizedRequests(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.state.Inv.Slots[0] = customStack(276, 1)
	original := copyStack(p.state.Inv.Slots[0])
	for _, gm := range []string{"survival", "adventure", "spectator"} {
		p.ext().GameMode = gm
		if s.creativeInventory(p, Message{T: "cset", ID: 1, N: 64}) {
			t.Fatal("unauthorized creative grant", gm)
		}
		if !reflect.DeepEqual(original, p.state.Inv.Slots[0]) {
			t.Fatal("unauthorized request changed inventory")
		}
	}
	p.ext().GameMode = "creative"
	for _, m := range []Message{
		{T: "cset", ID: 65535, N: 1}, {T: "cset", ID: 276, N: 2},
		{T: "cset", ID: 1, N: 0}, {T: "cset", ID: 1, N: -1}, {T: "cset", ID: 1, N: 65},
		{T: "cset", I: -1, ID: 1, N: 1}, {T: "cset", I: 9, ID: 1, N: 1},
		{T: "cset", N: 1}, {T: "cset", Pick: 2, ID: 1, N: 1},
		{T: "cswap", I: 0, S: 38}, {T: "cswap", I: 0, S: 0},
	} {
		if s.creativeInventory(p, m) {
			t.Fatalf("invalid request accepted: %+v", m)
		}
		if !reflect.DeepEqual(original, p.state.Inv.Slots[0]) {
			t.Fatal("invalid request changed inventory")
		}
	}
	r.Infinite = true
	p.epoch = 2
	s.message(p, Message{T: "cset", ID: 1, N: 64, Epoch: 1})
	if !reflect.DeepEqual(original, p.state.Inv.Slots[0]) {
		t.Fatal("stale epoch request committed")
	}
	p.state.Dead = true
	if s.creativeInventory(p, Message{T: "cset", ID: 1, N: 64, Epoch: 2}) {
		t.Fatal("dead player obtained items")
	}
}

func TestCreativePickSwapAndPersistencePreserveMetadata(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	s.rooms[r.ID] = r
	p.ext().GameMode = "creative"
	v := customStack(276, 1)
	v.Damage = 9
	v.Ench = map[string]int{"sharpness": 2}
	p.state.Inv.Slots[0] = copyStack(v)
	if !s.creativeInventory(p, Message{T: "cset", ID: 1, N: 64, Pick: 1}) || !reflect.DeepEqual(v, p.state.Inv.Slots[9]) {
		t.Fatal("pick-block lost original metadata")
	}
	p.state.Inv.Slots[1] = copyStack(v)
	if !s.creativeInventory(p, Message{T: "cswap", I: 0, S: 1}) || !reflect.DeepEqual(v, p.state.Inv.Slots[0]) || p.state.Inv.Slots[1].ID != 1 {
		t.Fatal("swap lost metadata")
	}
	for i := 9; i < 36; i++ {
		p.state.Inv.Slots[i] = stack(1, 64, 0)
	}
	if s.creativeInventory(p, Message{T: "cset", ID: 2, N: 64, Pick: 1}) || !reflect.DeepEqual(v, p.state.Inv.Slots[0]) {
		t.Fatal("full storage pick discarded old stack")
	}
	r.Players["CreativePersist123"] = p.state
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.roomFile(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	var disk diskRoom
	if json.Unmarshal(raw, &disk) != nil || !reflect.DeepEqual(v, disk.Players["CreativePersist123"].Inv.Slots[0]) {
		t.Fatal("creative item metadata not saved")
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, loader.rooms[r.ID].Players["CreativePersist123"].Inv.Slots[0]) {
		t.Fatal("creative item not restored")
	}
}

func TestPluginCreativeInventoryTransactions(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.ext().GameMode = "creative"
	p.state.Inv.Slots[0] = customStack(276, 1)
	old := copyStack(p.state.Inv.Slots[0])
	for _, slot := range []int{1, 3, 4, 5} {
		if s.creativeInventory(p, Message{T: "cset", I: slot, ID: 339, N: 8}) || p.state.Inv.Slots[slot] != nil {
			t.Fatal("cancelled/invalid plugin transaction committed", slot)
		}
	}
	if !s.creativeInventory(p, Message{T: "cset", I: 2, ID: 339, N: 8}) || p.state.Inv.Slots[2].Count != 2 || p.state.Inv.Slots[2].ID != 339 {
		t.Fatal("mutable count or read-only fields/Monitor validation failed")
	}
	if !s.creativeInventory(p, Message{T: "cswap", I: 0, S: 6}) || !reflect.DeepEqual(old, p.state.Inv.Slots[6]) {
		t.Fatal("plugin swap failed")
	}
	if !s.creativeInventory(p, Message{T: "cset", I: 6}) || p.state.Inv.Slots[6] != nil {
		t.Fatal("creative clear failed")
	}
	events := readItemEvents(t, s)
	completed, generic := 0, 0
	for _, e := range events {
		if e.Name == api.InventoryCreativeChanged {
			completed++
			if e.Player == nil || e.World == nil || e.Player.GameMode != "creative" || e.Data["target"] == nil || e.Data["changes"] == nil {
				t.Fatal("event missing actor/target/transaction snapshots")
			}
			if e.String("reason") == "creative.clear" && !reflect.DeepEqual(e.Item("before"), itemSnapshot(old)) {
				t.Fatal("clear event lost full item snapshot")
			}
		}
		if e.Name == api.InventoryChanged {
			generic++
		}
	}
	if completed != 3 || generic != 3 {
		t.Fatalf("failed transaction emitted successful event: creative=%d generic=%d", completed, generic)
	}
}
