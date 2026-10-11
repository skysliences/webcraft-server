package game

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func TestInventoryDragOutsideSentinelAndPersistence(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	p.sid = "DragPersist12345"
	defer delete(r.Peers, p)
	v := customStack(s.rules.byName["Oak Log"], 10)
	p.state.Cursor = copyStack(v)
	m, err := parseMessage([]byte(`{"t":"click","w":0,"s":-999,"b":0,"m":5,"l":[41,42,43,44],"q":7}`))
	if err != nil {
		t.Fatal(err)
	}
	s.message(p, m) // Includes registered extension dispatch and the actual sentinel.
	for _, slot := range m.L {
		if !reflect.DeepEqual(p.get(slot), sized(v, 2)) {
			t.Fatalf("grid slot %d not evenly split: %+v", slot, p.get(slot))
		}
	}
	if !reflect.DeepEqual(p.state.Cursor, sized(v, 2)) || len(r.Drops) != 0 {
		t.Fatal("drag became a drop or lost remainder")
	}
	// Metadata copies must not share mutable Lore/custom-data backing storage.
	p.get(41).Meta.Lore[0] = "changed only in this slot"
	if reflect.DeepEqual(p.get(41).Meta.Lore, p.get(42).Meta.Lore) || !reflect.DeepEqual(p.state.Cursor.Meta, v.Meta) {
		t.Fatal("drag shared metadata")
	}
	r.Players[p.sid] = p.state
	s.rooms[r.ID] = r
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.roomFile(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	var saved diskRoom
	if json.Unmarshal(raw, &saved) != nil || !reflect.DeepEqual(saved.Players[p.sid].Cursor, p.state.Cursor) {
		t.Fatal("saved cursor metadata lost")
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loader.rooms[r.ID].Players[p.sid].Inv.Slots[42], p.get(42)) {
		t.Fatal("dragged stack metadata not restored")
	}
}

func TestInventoryDragEligibilityAndHostileRequests(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	id := s.rules.byName["Oak Log"]
	p.state.Cursor = stack(id, 8, 0)
	p.put(0, stack(id, 64, 0)) // Full slots must not dilute the distribution.
	p.put(1, stack(id, 63, 0))
	p.put(2, customStack(id, 2)) // Different metadata must not merge.
	if !s.dragInventory(p, Message{M: 5, S: -999, L: []int{0, 1, 2, 3, 39, 45}}) {
		t.Fatal("valid mixed slots refused")
	}
	if p.get(0).Count != 64 || p.get(1).Count != 64 || p.get(2).Count != 2 || p.get(3).Count != 4 || p.state.Cursor.Count != 3 || p.get(39) != nil {
		t.Fatal("eligibility/cap/remainder differs from preview")
	}
	before, cursor := copyStack(p.get(3)), copyStack(p.state.Cursor)
	for _, m := range []Message{
		{M: 5, S: -999, L: []int{3, 3}}, {M: 5, S: -999, L: []int{3, -999}}, {M: 5, S: -999, L: []int{3, 800}},
		{M: 5, S: -999, B: 2, L: []int{3}}, {M: 5, S: -999, W: 99, L: []int{3}}, {M: 5, S: -999, L: []int{}},
	} {
		if s.dragInventory(p, m) || !reflect.DeepEqual(before, p.get(3)) || !reflect.DeepEqual(cursor, p.state.Cursor) {
			t.Fatalf("invalid request committed %+v", m)
		}
	}
	p.state.Cursor = stack(id, 2, 0)
	if !s.dragInventory(p, Message{M: 5, S: -999, B: 1, L: []int{4, 5, 6}}) || p.get(4).Count != 1 || p.get(5).Count != 1 || p.get(6) != nil || p.state.Cursor != nil {
		t.Fatal("right drag over more slots than items duplicated/lost items")
	}
	p.state.Cursor = stack(id, 2, 0)
	if s.dragInventory(p, Message{M: 5, S: -999, L: []int{0, 39, 45}}) {
		t.Fatal("no-op drag reported success")
	}
}

func TestInventoryDragLargeContainerAndViewerSync(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.window = &Window{ID: 1, Kind: "chest", Key: 3, Slots: make(Stacks, 27)}
	other := dropTestPlayer(r, 2, 10.5, 60, 10.5)
	other.window = &Window{ID: 2, Kind: "chest", Key: 3, Slots: p.window.Slots}
	r.Peers[other] = true
	defer delete(r.Peers, other)
	p.state.Cursor = stack(s.rules.byName["Oak Log"], 64, 0)
	var slots []int
	for i := 0; i < 36; i++ {
		slots = append(slots, i)
	}
	for i := 46; i < 73; i++ {
		slots = append(slots, i)
	}
	if !s.dragInventory(p, Message{M: 5, W: 1, S: -999, L: slots}) {
		t.Fatal("63-slot chest distribution refused")
	}
	for _, slot := range slots {
		if p.get(slot) == nil || p.get(slot).Count != 1 {
			t.Fatal("container slot not filled", slot)
		}
	}
	if p.state.Cursor.Count != 1 || other.window.Slots[26].Count != 1 || len(other.send) == 0 {
		t.Fatal("container remainder or viewer synchronization lost")
	}
}

func TestPluginInventoryDragAtomicProtectionAndSnapshots(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.sid = "DragPersist12345"
	v := customStack(s.rules.byName["Oak Log"], 8)
	for _, mode := range []string{"cancel_drag", "cancel_inventory", "cancel_click"} {
		p.state.Cursor = copyStack(v)
		p.state.Cursor.Meta.CustomData["test:mode"] = mode
		before := copyStack(p.state.Cursor)
		if s.dragInventory(p, Message{M: 5, S: -999, L: []int{41, 42}}) || p.get(41) != nil || p.get(42) != nil || !reflect.DeepEqual(before, p.state.Cursor) {
			t.Fatal("plugin cancellation partially committed", mode)
		}
	}
	p.state.Cursor = copyStack(v)
	if !s.dragInventory(p, Message{M: 5, S: -999, L: []int{41, 42}}) {
		t.Fatal("read-only mutation/Monitor blocked valid drag")
	}
	if p.get(41).Count != 4 || p.get(42).Count != 4 || p.state.Cursor != nil {
		t.Fatal("plugin altered read-only distribution")
	}
	completed := 0
	for _, e := range readItemEvents(t, s) {
		if e.Name == api.InventoryDragged {
			completed++
			d := e.InventoryDrag()
			if d == nil || d.Actor != p.sid || d.DragType != "even" || d.CursorBefore.Count != 8 || d.CursorAfter != nil || len(d.Changes) != 2 || d.Changes[0].Slot != 41 || !reflect.DeepEqual(d.Changes[0].After, itemSnapshot(sized(v, 4))) {
				t.Fatal("drag event missing complete authoritative snapshots")
			}
			d.Changes[0].After.Meta.Lore[0] = "SDK clone"
			if p.get(41).Meta.Lore[0] == "SDK clone" {
				t.Fatal("SDK context changed host item")
			}
		}
		if e.Name == api.InventoryChanged && e.String("reason") == "quick_craft" && e.Player.CursorItem != nil {
			t.Fatal("success emitted before cursor commit")
		}
	}
	if completed != 1 {
		t.Fatal("cancelled drag emitted success", completed)
	}
	for _, name := range []string{api.InventoryDragging, api.InventoryDragged} {
		found := false
		for _, spec := range api.Events() {
			if spec.Name == name {
				found = true
				if len(spec.Mutable) != 0 || spec.Cancellable != (name == api.InventoryDragging) {
					t.Fatal("drag catalog mutation contract")
				}
			}
		}
		if !found {
			t.Fatal("drag event absent from catalog")
		}
	}
}
