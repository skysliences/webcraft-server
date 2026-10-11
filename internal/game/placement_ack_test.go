package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"testing"
)

func lastPlaceAck(t *testing.T, p *Peer) map[string]any {
	t.Helper()
	var ack map[string]any
	for len(p.send) > 0 {
		raw := <-p.send
		if raw.kind != 1 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw.body, &m) == nil && m["t"] == "placeack" {
			ack = m
		}
	}
	return ack
}
func ackPlacedBlock(t *testing.T, p *Peer, q, b int) {
	t.Helper()
	a := lastPlaceAck(t, p)
	if a == nil || a["q"] != float64(q) {
		t.Fatalf("missing correlated ack %d: %v", q, a)
	}
	blocks, ok := a["blocks"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("bad canonical snapshot %v", a)
	}
	c := blocks[0].(map[string]any)
	if c["x"] != float64(12) || c["y"] != float64(60) || c["z"] != float64(10) || c["b"] != float64(b) {
		t.Fatalf("canonical block differs: %v", c)
	}
	if a["epoch"] != float64(p.epoch) || a["dim"] != p.room.dimName() {
		t.Fatal("ack missing epoch/dimension")
	}
	inv, ok := a["inventory"].(map[string]any)
	if !ok || inv["placeQ"] != float64(q) || inv["rev"] != float64(p.inventoryRevision) || inv["epoch"] != float64(p.epoch) || inv["dim"] != p.room.dimName() {
		t.Fatalf("missing correlated canonical inventory: %v", inv)
	}
	var actual Inventory
	raw, _ := json.Marshal(inv)
	if json.Unmarshal(raw, &actual) != nil || !reflect.DeepEqual(actual.Slots, p.state.Inv.Slots) || actual.Selected != p.state.Inv.Selected {
		t.Fatal("ack inventory differs from actual committed state/metadata")
	}
}
func placePredictionMessage(q int) Message {
	return Message{T: "place", X: 12, Y: 59, Z: 10, F: 1, Q: q, Preview: []BlockPosition{{X: 12, Y: 60, Z: 10}}}
}
func TestPlacementAckCommitMetadataAndEvents(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mode := range []string{"survival", "creative"} {
		r, p := redstoneTestPeer(s, 1)
		p.ext().GameMode = mode
		p.epoch = 23
		before := itemSnapshot(s.heldItem(p))
		start := len(mechanicEvents(t, s))
		s.survivalMessage(p, placePredictionMessage(11))
		ackPlacedBlock(t, p, 11, 1)
		count := 1
		if mode == "creative" {
			count = 2
		}
		if s.block(r, 12, 60, 10) != 1 || s.heldItem(p).Count != count || !reflect.DeepEqual(before.Meta, s.heldItem(p).Meta) {
			t.Fatal("placement changed metadata/count")
		}
		names := []string{}
		for _, e := range mechanicEvents(t, s)[start:] {
			if e.Name == api.BlockPlacing || e.Name == api.BlockPlaced || e.Name == api.InventoryChanging || e.Name == api.InventoryChanged {
				names = append(names, e.Name)
				if e.Player == nil || e.Player.AccountID != p.sid || e.Item("stack") == nil || !reflect.DeepEqual(before.Meta, e.Item("stack").Meta) || e.Item("before").Count != 2 || e.Item("after").Count != count || e.String("reason") != "place" || e.Number("oldBlock") != 0 || e.Number("block") != 1 || e.Data["target"] == nil {
					t.Fatalf("missing accurate placement context: %s %v", e.Name, e.Data)
				}
				if e.Name == api.BlockPlacing || e.Name == api.BlockPlaced {
					snap := e.BlockPlacement()
					if snap == nil || snap.Actor != p.sid || snap.Block != 1 || snap.Before.Count != 2 || snap.After.Count != count || snap.Consumed != 2-count {
						t.Fatalf("SDK snapshot mismatch %v", snap)
					}
				}
				if e.Name == api.InventoryChanging || e.Name == api.InventoryChanged {
					if snap := e.PlacementInventory(); snap == nil || snap.Consumed != 2-count || !reflect.DeepEqual(snap.Stack.Meta, before.Meta) {
						t.Fatal("inventory SDK snapshot differs from authoritative consumption")
					}
				}
			}
		}
		if !reflect.DeepEqual(names, []string{api.BlockPlacing, api.InventoryChanging, api.BlockPlaced, api.InventoryChanged}) {
			t.Fatalf("event order: %v", names)
		}
	}
}

func TestPlacementInventorySnapshotOrderingAndSilentRefusal(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := redstoneTestPeer(s, 1)
	s.inventory(p, 0, 0)
	initial := p.inventoryRevision
	s.survivalMessage(p, placePredictionMessage(101))
	ackPlacedBlock(t, p, 101, 1)
	if p.inventoryRevision <= initial || p.inventoryPlaceQ != 101 || s.heldItem(p).Count != 1 {
		t.Fatal("accepted placement did not advance snapshot ordering")
	}
	// The occupied block is silently refused. The next ack must still retire its
	// reservation and report one remaining, rather than refunding or consuming.
	s.survivalMessage(p, placePredictionMessage(102))
	ackPlacedBlock(t, p, 102, 1)
	if s.heldItem(p).Count != 1 || s.block(r, 12, 60, 10) != 1 {
		t.Fatal("refusal changed canonical inventory/world")
	}
	old := p.inventoryRevision
	p.state.Dead = true
	s.survivalMessage(p, Message{T: "sel", I: 1, Q: 9})
	if p.inventorySelQ != 9 || p.inventoryRevision <= old || p.state.Inv.Selected != 0 {
		t.Fatal("selection refusal has no canonical response")
	}
}
func TestPlacementAckCancellationNoCommitOrSuccess(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.BlockPlacing, api.InventoryChanging} {
		r, p := redstoneTestPeer(s, 1)
		s.heldItem(p).Meta.CustomData["test:cancel"] = event
		before := itemSnapshot(s.heldItem(p))
		start := len(mechanicEvents(t, s))
		s.survivalMessage(p, placePredictionMessage(17))
		ackPlacedBlock(t, p, 17, 0)
		if s.block(r, 12, 60, 10) != 0 || !reflect.DeepEqual(before, itemSnapshot(s.heldItem(p))) {
			t.Fatal("cancelled prediction committed or consumed")
		}
		for _, e := range mechanicEvents(t, s)[start:] {
			if e.Name == api.BlockPlaced || e.Name == api.InventoryChanged {
				t.Fatal("cancelled placement emitted success")
			}
		}
	}
}
func TestPlacementAckBoundsOldClientsAndHooks(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range []string{"empty", "occupied", "dead", "locked", "collision"} {
		r, p := redstoneTestPeer(s, 1)
		if state == "empty" {
			p.state.Inv.Slots[0] = nil
		}
		if state == "occupied" {
			r.Edits[blockKey(12, 60, 10)] = 3
		}
		if state == "dead" {
			p.state.Dead = true
		}
		if state == "locked" {
			r.Lock = 1
		}
		if state == "collision" {
			p.state.X = 12.5
		}
		s.survivalMessage(p, placePredictionMessage(21))
		b := 0
		if state == "occupied" {
			b = 3
		}
		ackPlacedBlock(t, p, 21, b)
		if s.block(r, 12, 60, 10) != b {
			t.Fatal("refusal bypassed rules")
		}
	}
	for _, item := range []int{82, itemRail, 320} {
		_, p := redstoneTestPeer(s, item)
		p.ext().GameMode = "creative"
		s.survivalMessage(p, placePredictionMessage(30))
		b := item
		if item == itemRail {
			b = blkRail
		}
		if item == 320 {
			b = 108
		}
		ackPlacedBlock(t, p, 30, b)
	}
	_, p := redstoneTestPeer(s, 1)
	m := placePredictionMessage(0)
	s.survivalMessage(p, m)
	if lastPlaceAck(t, p) != nil {
		t.Fatal("legacy request received opt-in ack")
	}
	for _, m := range []Message{
		{T: "place", X: 1000, Y: 59, Z: 10, F: 1, Q: 31, Preview: []BlockPosition{{12, 60, 10}}},
		{T: "place", X: 12, Y: 59, Z: 10, F: 1, Q: 31, Preview: []BlockPosition{{1000, 60, 10}}},
		{T: "place", X: 12, Y: 59, Z: 10, F: 1, Q: 31, Preview: []BlockPosition{{12, 60, 10}, {12, 60, 10}}},
	} {
		_, p := redstoneTestPeer(s, 1)
		s.survivalMessage(p, m)
		if lastPlaceAck(t, p) != nil {
			t.Fatal("invalid preview probed canonical state")
		}
	}
}

func TestPlacementAckPersistsCanonicalStateAndMetadata(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := redstoneTestPeer(s, 1)
	before := itemSnapshot(s.heldItem(p))
	s.survivalMessage(p, placePredictionMessage(42))
	ackPlacedBlock(t, p, 42, 1)
	r.Players[p.sid] = p.state
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	restored := loader.rooms[r.ID]
	stack := restored.Players[p.sid].Inv.Slots[0]
	if loader.block(restored, 12, 60, 10) != 1 || stack.Count != 1 || !reflect.DeepEqual(before.Meta, stack.Meta) {
		t.Fatal("restored world does not match committed placement and item metadata")
	}
}
