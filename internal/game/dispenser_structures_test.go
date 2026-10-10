package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"strings"
	"testing"
)

func dispenserWitherWorld(s *Server, axisX bool) (*Room, uint64, *Stack, [][4]int) {
	r, k, original := dispenserFireWorld(s, itemWitherSkeletonSkull)
	original.Count = 3
	r.Containers[k].Slots[0] = copyStack(original)
	cx, cy, cz, dx, dz := 12, 60, 10, 1, 0
	if !axisX {
		cx, cz, dx, dz = 11, 11, 0, 1
	}
	cells := [][4]int{{cx, cy - 2, cz, blkWitherSoulSand}, {cx, cy - 1, cz, blkWitherSoulSand}, {cx - dx, cy - 1, cz - dz, blkWitherSoulSand}, {cx + dx, cy - 1, cz + dz, blkWitherSoulSand}, {cx, cy, cz, blkWitherSkeletonSkull}, {cx - dx, cy, cz - dz, blkWitherSkeletonSkull}, {cx + dx, cy, cz + dz, blkWitherSkeletonSkull}}
	for _, at := range cells {
		r.Edits[blockKey(at[0], at[1], at[2])] = at[3]
	}
	r.Edits[blockKey(cx-dx, cy-2, cz-dz)] = 0
	r.Edits[blockKey(cx+dx, cy-2, cz+dz)] = 0
	r.Edits[blockKey(11, 60, 10)] = 0
	return r, k, original, cells
}
func TestDispenserWitherTrueIDsAxesSourceAndStructureAtomicity(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, axis := range []bool{true, false} {
		r, k, original, cells := dispenserWitherWorld(s, axis)
		if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || len(r.Mobs) != 1 || r.Containers[k].Slots[0].Count != 2 || !same(original, r.Containers[k].Slots[0]) || len(r.Drops) != 0 {
			t.Fatal("skull failed to atomically summon wither")
		}
		for _, at := range cells {
			if s.block(r, at[0], at[1], at[2]) != 0 {
				t.Fatal("summon left structural block")
			}
		}
		for _, mob := range r.Mobs {
			if mob.Type != "wither" || mob.Health != 300 || getMobExt(mob.ID).ChargeTicks != 100 {
				t.Fatal("summon lost boss charge/health")
			}
		}
	}
	r, k, original, cells := dispenserWitherWorld(s, true)
	for _, at := range cells {
		if at[3] == blkWitherSoulSand {
			r.Edits[blockKey(at[0], at[1], at[2])] = 88
		}
	}
	handled, _, _ := s.prepareDispenserWither(r, k, 0, original, 11, 60, 10, map[string]any{})
	if handled || len(r.Mobs) != 0 {
		t.Fatal("furnace ID incorrectly accepted as soul sand")
	}
	r, k, original, _ = dispenserWitherWorld(s, true)
	r.ext().Difficulty = "peaceful"
	handled, _, _ = s.prepareDispenserWither(r, k, 0, original, 11, 60, 10, map[string]any{})
	if handled || len(r.Mobs) != 0 {
		t.Fatal("wither spawned in peaceful mode")
	}
}
func TestDispenserWitherRealPluginMobAndStructuralProtection(t *testing.T) {
	s := dispenserFirePluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.EntitySpawning, api.BlockBreaking, api.BlockPhysicsChanging, api.BlockDispensing, api.InventoryChanging, api.MechanicChanging} {
		r, k, original, _ := dispenserWitherWorld(s, true)
		original.Meta.CustomData["test:cancel"] = event
		r.Containers[k].Slots[0] = copyStack(original)
		before := map[uint64]int{}
		for at, b := range r.Edits {
			before[at] = b
		}
		start := len(dispenserFireEvents(t, s))
		nextID := r.NextMobID
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(r.Edits, before) || !reflect.DeepEqual(r.Containers[k].Slots[0], original) || len(r.Mobs) != 0 || r.NextMobID != nextID || mechanicSnapshot(r, k).Powered {
			t.Fatalf("%s veto partly committed wither", event)
		}
		for _, e := range dispenserFireEvents(t, s)[start:] {
			if !strings.HasSuffix(e.Name, ".before") {
				t.Fatalf("%s veto emitted %s", event, e.Name)
			}
		}
	}
	r, k, original, _ := dispenserWitherWorld(s, true)
	start := len(dispenserFireEvents(t, s))
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) {
		t.Fatal("wither successful dispense failed")
	}
	spawned := 0
	broken := 0
	for _, e := range dispenserFireEvents(t, s)[start:] {
		if e.Name == api.EntitySpawned {
			spawned++
			if e.String("reason") != "dispenser_wither" || e.String("actor") != "server" || e.String("source") != "dispenser" || e.Item("stack") == nil || !reflect.DeepEqual(e.Item("stack").Meta, original.Meta) {
				t.Fatal("wither spawn lost metadata/attribution")
			}
		}
		if e.Name == api.BlockBroken {
			broken++
			if e.String("actor") != "server" || e.String("source") != "dispenser" {
				t.Fatal("structure block event lost attribution")
			}
		}
	}
	if spawned != 1 || broken != 6 {
		t.Fatalf("wither successes spawned=%d broken=%d", spawned, broken)
	}
}
func TestPlayerWitherSummonPluginVetoPreservesBuiltStructure(t *testing.T) {
	s := dispenserFirePluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _, original, cells := dispenserWitherWorld(s, true)
	r.Edits[blockKey(11, 60, 10)] = blkWitherSkeletonSkull
	p := dropTestPlayer(r, 17, 13.5, 60, 13.5)
	p.sid = "test-wither-player"
	p.state.Inv.Slots[0] = copyStack(original)
	r.Peers[p] = true
	original.Meta.CustomData["test:cancel"] = api.EntitySpawning
	start := len(dispenserFireEvents(t, s))
	s.checkWitherSpawn(r, p, 11, 60, 10, original)
	for _, at := range cells {
		if s.block(r, at[0], at[1], at[2]) != at[3] {
			t.Fatal("player summon veto deleted structure")
		}
	}
	if len(r.Mobs) != 0 {
		t.Fatal("player veto spawned wither")
	}
	for _, e := range dispenserFireEvents(t, s)[start:] {
		if !strings.HasSuffix(e.Name, ".before") {
			t.Fatal("player veto emitted success")
		}
	}
}
