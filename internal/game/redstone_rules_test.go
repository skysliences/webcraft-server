package game

import (
	"reflect"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func TestRedstoneWireInteractionPrecedesPlacement(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []int{0, 320, 1, 339, 356, 357, 216, 383}
	for _, it := range s.rules.Items {
		if it.SpawnEgg != "" {
			items = append(items, it.ID)
			break
		}
	}
	for _, mode := range []string{"survival", "creative"} {
		for _, item := range items {
			r, p := redstoneTestPeer(s, 320)
			p.ext().GameMode = mode
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
			k := blockKey(12, 60, 10)
			p.state.Inv.Slots[0] = nil
			if item != 0 {
				p.state.Inv.Slots[0] = stack(item, 4, 0)
			}
			before := copyStack(s.heldItem(p))
			for _, dot := range []bool{true, false} {
				s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
				state := redstoneSnapshot(r, k)
				if state == nil || state.Dot != dot || !reflect.DeepEqual(before, s.heldItem(p)) || s.block(r, 12, 61, 10) != 0 {
					t.Fatal("wire interaction placed or consumed an item", mode, item, state)
				}
				if s.redstoneWirePacket(r, k)["dot"] != dot {
					t.Fatal("dot mode missing from packet")
				}
			}
			p.flags = 1
			p.state.Inv.Slots[0] = stack(320, 4, 0)
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
			if s.block(r, 12, 61, 10) != 0 || s.heldItem(p).Count != 4 {
				t.Fatal("sneaking bypassed wire support")
			}
			p.state.Inv.Slots[0] = stack(82, 4, 0)
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
			if s.block(r, 12, 60, 10) != 108 || s.block(r, 12, 61, 10) != 0 || s.heldItem(p).Count != 4 {
				t.Fatal("torch treated wire as a replaceable plant")
			}
			p.state.Inv.Slots[0] = stack(1, 4, 0)
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
			if s.block(r, 12, 61, 10) != 1 {
				t.Fatal("sneaking could not place an ordinary block above wire")
			}
		}
	}
}

func TestRedstoneFloorSupportAndSidePlacement(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range []int{320, 356, 357, 360, 361} {
		id := []int{108, 166, 167, 174, 175}[i]
		r, p := redstoneTestPeer(s, item)
		r.Edits[blockKey(12, 60, 10)] = 1
		s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 5})
		if s.block(r, 13, 60, 10) != id {
			t.Fatal("valid floor placement from a side failed", item)
		}
		p.flags = 1
		s.survivalMessage(p, Message{T: "place", X: 13, Y: 60, Z: 10, F: 1})
		if s.block(r, 13, 61, 10) != 0 || s.heldItem(p).Count != 1 {
			t.Fatal("thin component supported another component", item)
		}
	}
}

func TestRedstoneSupportLossAndLegacyRepair(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, method := range []string{"player", "plugin", "physics"} {
		for i, item := range []int{320, 356, 357, 360, 361} {
			r, p := redstoneTestPeer(s, item)
			s.rooms[r.ID] = r
			r.Edits[blockKey(12, 61, 10)] = 1
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 61, Z: 10, F: 1})
			if s.block(r, 12, 62, 10) != []int{108, 166, 167, 174, 175}[i] {
				t.Fatal("setup failed", item)
			}
			switch method {
			case "player":
				p.state.Inv.Slots[0] = nil
				s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 61, Z: 10, F: 1})
				if p.mining != nil {
					p.mining.At = p.mining.At.Add(-time.Minute)
				}
				s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 61, Z: 10, F: 1})
			case "plugin":
				s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 61, Z: 10, Block: 0})
			case "physics":
				if !s.setBlock(r, 12, 61, 10, 0, setQuiet) {
					t.Fatal("physics removal failed")
				}
			}
			if s.block(r, 12, 62, 10) != 0 || redstoneSnapshot(r, blockKey(12, 62, 10)) != nil {
				t.Fatal("support loss left component", method, item)
			}
			count := 0
			for _, drop := range r.Drops {
				if drop.Stack.ID == item {
					count += drop.Stack.Count
				}
			}
			if count != 1 {
				t.Fatal("support loss did not drop exactly one item", method, item, count)
			}
		}
	}
	r, _ := redstoneTestPeer(s, 320)
	for _, y := range []int{62, 63} {
		r.Edits[blockKey(12, y, 10)] = 108
		s.trackRedstoneBlock(r, blockKey(12, y, 10), 108)
	}
	s.tickRedstoneComponents(r)
	if s.block(r, 12, 62, 10) != 0 || s.block(r, 12, 63, 10) != 0 {
		t.Fatal("old floating wires survived repair")
	}
}

func TestRedstoneWireShapeDirectionAndPersistence(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := redstoneTestPeer(s, 320)
	k := blockKey(12, 60, 10)
	r.Edits[k] = 108
	storeRedstoneComponent(r, k, &RedstoneComponent{Kind: "wire", Power: 15, Dot: true, SupportFace: 1})
	for _, d := range horizontal4 {
		if s.emittedPower(r, 12, 60, 10, d[0], 0, d[2]) != 0 {
			t.Fatal("dot emitted sideways")
		}
	}
	if s.emittedPower(r, 12, 60, 10, 0, -1, 0) != 15 || s.emittedPower(r, 12, 60, 10, 0, 1, 0) != 0 {
		t.Fatal("wrong vertical wire output")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, engine: s.engine}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	if !redstoneSnapshot(loader.rooms[r.ID], k).Dot {
		t.Fatal("reload lost dot mode")
	}
	// A forced connection turns an isolated dot into a directional line.
	r.Edits[blockKey(11, 60, 10)] = 109
	s.trackRedstoneBlock(r, blockKey(11, 60, 10), 109)
	s.recalculateWires(r)
	if s.emittedPower(r, 12, 60, 10, 1, 0, 0) != 15 || s.emittedPower(r, 12, 60, 10, 0, 0, 1) != 0 {
		t.Fatal("wire shape and directional output disagree")
	}
}
