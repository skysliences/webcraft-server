package game

import (
	api "mcraft/backend/pkg/plugin"
	"testing"
	"time"
)

func TestRedstoneTorchSupportLoss(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, method := range []string{"player", "plugin"} {
		for face := 1; face <= 5; face++ {
			r, p := redstoneTestPeer(s, 355)
			s.rooms[r.ID] = r
			r.Edits[blockKey(12, 61, 10)] = 1
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 61, Z: 10, F: face})
			off := faceOffsets[face]
			x, y, z := 12+off[0], 61+off[1], 10+off[2]
			k := blockKey(x, y, z)
			if s.block(r, x, y, z) != 109 || !s.redstoneSupported(r, k, 109) {
				t.Fatal("supported torch placement failed", face)
			}
			// Replacing the attachment with another solid block keeps the torch.
			s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 61, Z: 10, Block: 2})
			if s.block(r, x, y, z) != 109 {
				t.Fatal("solid replacement removed torch", face)
			}
			if method == "plugin" {
				s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 61, Z: 10, Block: 0})
			} else {
				p.state.Inv.Slots[0] = nil
				s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 61, Z: 10, F: 1})
				if p.mining != nil {
					p.mining.At = p.mining.At.Add(-time.Minute)
				}
				s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 61, Z: 10, F: 1})
			}
			if s.block(r, x, y, z) != 0 || redstoneSnapshot(r, k) != nil {
				t.Fatal("support removal left floating torch or stale power state", method, face)
			}
			count := 0
			for _, drop := range r.Drops {
				if drop.Stack.ID == 355 {
					count += drop.Stack.Count
				}
			}
			if count != 1 {
				t.Fatal("support removal must drop one redstone torch item", method, face, count)
			}
		}
	}
}

func TestRedstoneTorchSupportLossPreservesAtomicDrops(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := redstoneTestPeer(s, 355)
	s.rooms[r.ID] = r
	r.Edits[blockKey(12, 61, 10)] = 1
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 61, Z: 10, F: 5})
	for i := 0; i < maxWorldDrops; i++ {
		r.Drops[uint32(i+1)] = &ItemDrop{ID: uint32(i + 1), Stack: stack(1, 1, 0)}
	}
	s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 61, Z: 10, Block: 0})
	if s.block(r, 12, 61, 10) != 1 || s.block(r, 13, 61, 10) != 109 || redstoneSnapshot(r, blockKey(13, 61, 10)) == nil {
		t.Fatal("full drop budget must retain support and torch together")
	}
}

func TestRedstoneTorchSteadyWirePower(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := dropTestWorld(s)
	wires := [][3]int{{11, 60, 10}, {12, 60, 10}, {12, 60, 11}, {12, 60, 12}}
	place := func(x, y, z, id int) {
		r.Edits[blockKey(x, y, z)] = id
		s.trackRedstoneBlock(r, blockKey(x, y, z), id)
	}
	place(10, 59, 10, 1)
	place(10, 60, 10, 109)
	for _, at := range wires {
		place(at[0], at[1]-1, at[2], 1)
		place(at[0], at[1], at[2], 108)
	}
	for tick := 0; tick < 80; tick++ {
		s.recalculateWires(r)
		for i, at := range wires {
			if got := getRedstone(r).Wires[blockKey(at[0], at[1], at[2])]; got != 15-i {
				t.Fatalf("tick %d: wire %v power = %d, want %d", tick, at, got, 15-i)
			}
		}
		if got := s.redstoneWirePacket(r, blockKey(10, 60, 10))["power"]; got != 15 {
			t.Fatalf("tick %d: torch display power = %v, want 15", tick, got)
		}
		if s.powerAt(r, 11, 59, 11) != 15 {
			t.Fatal("powered ground stopped activating adjacent mechanisms")
		}
	}
	// Restore committed signals as a saved room would, then recalculate.
	s.persistRedstone(r)
	r.rt().redstone = nil
	s.restoreLoadedRedstone(r)
	s.recalculateWires(r)
	if getRedstone(r).Wires[blockKey(11, 60, 10)] != 15 {
		t.Fatal("restored circuit turned itself off")
	}
	for _, source := range []int{0, 109} {
		place(10, 60, 10, source)
		for tick := 0; tick < 4; tick++ {
			s.recalculateWires(r)
			for i, at := range wires {
				want := 0
				if source == 109 {
					want = 15 - i
				}
				if got := getRedstone(r).Wires[blockKey(at[0], at[1], at[2])]; got != want {
					t.Fatalf("source %d tick %d: wire %v power = %d, want %d", source, tick, at, got, want)
				}
			}
		}
	}
}

func TestRedstoneTorchAttachmentStillInverts(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for face := 1; face <= 5; face++ {
		r, _ := dropTestWorld(s)
		off := faceOffsets[face]
		sx, sy, sz := 10-off[0], 60-off[1], 10-off[2]
		ix, iy, iz := sx-off[0], sy-off[1], sz-off[2]
		r.Edits[blockKey(sx, sy, sz)] = 1
		r.Edits[blockKey(ix, iy, iz)] = 0
		r.Edits[blockKey(10, 60, 10)] = 109
		storeRedstoneComponent(r, blockKey(10, 60, 10), &RedstoneComponent{Kind: "torch", SupportFace: face})
		if s.directPowerSource(r, 10, 60, 10, -off[0], -off[1], -off[2]) != 0 {
			t.Fatal("torch powered its own attachment", face)
		}
		for _, source := range []int{40, 108} {
			r.Edits[blockKey(ix, iy, iz)] = source
			if source == 108 {
				storeRedstoneComponent(r, blockKey(ix, iy, iz), &RedstoneComponent{Kind: "wire", Power: 15})
			}
			want := 0
			// Dust does not power the block above it, unlike a redstone block.
			if source == 108 && face == 1 {
				want = 15
			}
			if s.directPowerSource(r, 10, 60, 10, 0, 1, 0) != want {
				t.Fatal("powered attachment failed to switch torch off", face, source)
			}
			r.Edits[blockKey(ix, iy, iz)] = 0
			s.clearRedstoneComponent(r, blockKey(ix, iy, iz))
			if s.directPowerSource(r, 10, 60, 10, 0, 1, 0) != 15 {
				t.Fatal("removing input failed to switch torch on", face, source)
			}
		}
	}
}
