package game

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"github.com/gorilla/websocket"
	api "mcraft/backend/pkg/plugin"
	"testing"
	"time"
)

func TestTorchSurvivalPlacementCraftingAndDrops(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, coal := range []string{"Coal", "Charcoal"} {
		result := s.rules.craft(Stacks{stack(s.rules.byName[coal], 1, 0), nil, stack(s.rules.byName["Stick"], 1, 0), nil}, 2)
		if result == nil || result.ID != torchBlock || result.Count != 4 {
			t.Fatalf("%s + stick should craft four torches: %v", coal, result)
		}
	}
	offsets := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for face := 0; face <= 5; face++ {
		r, p := dropTestWorld(s)
		r.Edits[blockKey(12, 61, 10)] = 1
		p.state.Inv.Slots[0] = stack(torchBlock, 4, 0)
		s.survivalMessage(p, Message{T: "place", X: 12, Y: 61, Z: 10, F: face})
		if face == 0 {
			if p.state.Inv.Slots[0].Count != 4 {
				t.Fatal("ceiling placement consumed torch")
			}
			continue
		}
		off := offsets[face]
		x, y, z := 12+off[0], 61+off[1], 10+off[2]
		expected := torchForFace(face)
		if s.block(r, x, y, z) != expected || p.state.Inv.Slots[0].Count != 3 {
			t.Fatalf("face %d torch placement/count incorrect", face)
		}
		s.survivalMessage(p, Message{T: "dig", S: 0, X: x, Y: y, Z: z, F: 1})
		s.survivalMessage(p, Message{T: "dig", S: 2, X: x, Y: y, Z: z, F: 1})
		if s.block(r, x, y, z) != 0 || len(r.Drops) != 1 || p.state.Inv.Slots[0].Count != 3 {
			t.Fatal("broken torch should drop on ground, not enter inventory")
		}
		for _, drop := range r.Drops {
			if drop.Stack.ID != torchBlock || drop.Stack.Count != 1 {
				t.Fatal("wall torch dropped the wrong item")
			}
		}
	}
	r, p := dropTestWorld(s)
	p.state.Inv.Slots[0] = stack(torchBlock, 2, 0)
	// Clicking a flower replaces it with a floor torch on the stone beneath it.
	r.Edits[blockKey(12, 60, 10)] = 64
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 2})
	if s.block(r, 12, 60, 10) != torchBlock || p.state.Inv.Slots[0].Count != 1 {
		t.Fatal("torch cannot replace a plant")
	}
	r.Edits[blockKey(14, 61, 10)] = 9
	s.survivalMessage(p, Message{T: "place", X: 14, Y: 61, Z: 10, F: 1})
	if p.state.Inv.Slots[0].Count != 1 {
		t.Fatal("liquid supported a torch")
	}
}

func TestPluginAndUndoTorchPhysics(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mode := range []string{"creative", "survival"} {
		r, p := dropTestWorld(s)
		r.Mode = mode
		s.rooms[r.ID] = r
		defer func() { delete(s.rooms, r.ID) }()
		support, torch := blockKey(12, 61, 10), blockKey(12, 62, 10)
		s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 61, Z: 10, Block: 1})
		s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 62, Z: 10, Block: 82})
		if r.Edits[torch] != 82 {
			t.Fatal("plugin could not place a supported torch")
		}
		s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 14, Y: 62, Z: 10, Block: 82})
		if r.Edits[blockKey(14, 62, 10)] == 82 {
			t.Fatal("plugin placed a floating torch")
		}
		s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 61, Z: 10, Block: 0})
		if r.Edits[torch] != 0 {
			t.Fatal("plugin support removal left a floating torch")
		}
		if mode == "survival" && len(r.Drops) != 1 {
			t.Fatal("plugin support removal lost the torch drop")
		}
		if mode == "creative" {
			r.Edits[support] = 1
			r.Edits[torch] = 82
			r.Authors[support] = EditAuthor{Token: "builder", Before: 0, After: 1}
			p.owner = true
			s.ownerAction(p, Message{T: "undo", Token: "builder", Confirm: true})
			if r.Edits[support] != 0 || r.Edits[torch] != 0 {
				t.Fatal("undo left a floating torch")
			}
		}
	}
}

func TestTorchSupportBreakIsAtomic(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, full := range []bool{false, true} {
		r, p := dropTestWorld(s)
		support := blockKey(12, 61, 10)
		r.Edits[support] = s.rules.byName["Oak Log"]
		for id := torchBlock; id <= 86; id++ {
			off := torchOffsets[id-torchBlock]
			r.Edits[blockKey(12-off[0], 61-off[1], 10-off[2])] = id
		}
		if full {
			for i := uint32(1); i < maxWorldDrops; i++ {
				r.Drops[i] = &ItemDrop{ID: i}
			}
		}
		s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 61, Z: 10, F: 1})
		p.mining.At = time.Now().Add(-time.Minute)
		s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 61, Z: 10, F: 1})
		if full {
			if r.Edits[support] == 0 {
				t.Fatal("support was broken without enough capacity for torch drops")
			}
			continue
		}
		if r.Edits[support] != 0 || len(r.Drops) != 6 {
			t.Fatalf("support break should drop one log and five torches: %d", len(r.Drops))
		}
		torches := 0
		for _, drop := range r.Drops {
			if drop.Stack.ID == torchBlock {
				torches += drop.Stack.Count
			}
		}
		if torches != 5 {
			t.Fatal("detached torches were lost or duplicated")
		}
		for _, off := range torchOffsets {
			if s.block(r, 12-off[0], 61-off[1], 10-off[2]) != 0 {
				t.Fatal("unsupported torch remained in world")
			}
		}
	}
}

func TestCreativeTorchReplicationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, h := testApp(t, dir)
	id, key := postRoom(t, h.URL, "creative")
	owner, _ := connect(t, h.URL, id, "TorchOwner123", key)
	other, _ := connect(t, h.URL, id, "TorchObserver123", "")
	support, torch := blockKey(500, 90, 500), blockKey(500, 91, 500)
	write := func(k uint64, b byte) {
		t.Helper()
		if err := owner.WriteMessage(websocket.BinaryMessage, editPacket(k, b)); err != nil {
			t.Fatal(err)
		}
	}
	write(support, 1)
	receiveBinary(t, other, 5)
	write(torch, torchBlock)
	frame := receiveBinary(t, other, 5)
	if binary.LittleEndian.Uint32(frame[7:]) != toWireKey(torch) || frame[11] != torchBlock {
		t.Fatal("placed torch not broadcast to another player")
	}
	send(t, owner, map[string]any{"t": "inv", "hotbar": []int{82, 1, 2, 3, 4, 5, 6, 10, 11}, "slot": 0})
	send(t, owner, map[string]any{"t": "chat", "m": "bar saved"})
	receive(t, owner, "chat")
	s.mu.Lock()
	room := s.rooms[id]
	if room.Players[fixtureID(h.URL, "TorchOwner123")].Hotbar[0] != 82 {
		s.mu.Unlock()
		t.Fatal("creative hotbar rejected torch")
	}
	if err := s.saveRoom(room); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	restoreDir:=t.TempDir();os.MkdirAll(filepath.Join(restoreDir,"rooms"),0700)
	raw,err:=os.ReadFile(s.roomFile(id));if err!=nil {t.Fatal(err)}
	if err=os.WriteFile(filepath.Join(restoreDir,"rooms",id+".json"),raw,0600);err!=nil {t.Fatal(err)}
	restored, err := New(Config{DataDir: restoreDir, EngineFile: "../../../public/engine.wasm"})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.rooms[id].Edits[torch] != 82 {
		t.Fatal("restart lost placed torch")
	}
	write(support, 0)
	correction := receiveBinary(t, owner, 3)
	if len(correction) != 6 || binary.LittleEndian.Uint32(correction[1:]) != toWireKey(torch) || correction[5] != 0 {
		t.Fatal("actor did not receive automatic torch removal")
	}
	receiveBinary(t, other, 5)
	removed := receiveBinary(t, other, 5)
	if binary.LittleEndian.Uint32(removed[7:]) != toWireKey(torch) || removed[11] != 0 {
		t.Fatal("detached torch not replicated")
	}
	write(blockKey(510, 90, 510), 82)
	deny := receiveBinary(t, owner, 4)
	if deny[5] == 82 {
		t.Fatal("floating torch accepted")
	}
}

func TestTorchProtectionCancelsWholeUndo(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	r.Mode = "creative"
	p.owner = true
	s.rooms[r.ID] = r
	defer func() { delete(s.rooms, r.ID) }()
	support, torch := blockKey(12, 61, 10), blockKey(12, 62, 10)
	r.Edits[support] = 1
	r.Edits[torch] = 82
	r.Authors[support] = EditAuthor{Token: "builder", Before: 0, After: 1}
	s.ownerAction(p, Message{T: "undo", Token: "builder", Confirm: true})
	if r.Edits[support] != 1 || r.Edits[torch] != 82 || r.Authors[support].Token != "builder" {
		t.Fatal("cancelled torch physics partially committed an undo")
	}
}
