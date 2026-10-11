package game

import (
	"math"
	"reflect"
	"sort"

	"github.com/gorilla/websocket"
	api "mcraft/backend/pkg/plugin"
)

// A game tick is 100ms: four ticks are Minecraft's eight 50ms hopper ticks.
const hopperCooldown = 4
const blkHopper = 193
const itemHopper = 385

func (s *Server) automationContainer(r *Room, k uint64) *Container {
	c := r.Containers[k]
	if c == nil || c.Kind == "ender" || c.Kind == "ench" || c.Kind == "table" || windowSize(c.Kind) != len(c.Slots) {
		return nil
	}
	x, y, z := coords(k)
	if containerKinds[s.nameOf(s.block(r, x, y, z))] != c.Kind {
		return nil
	}
	return c
}

func containerTarget(k uint64, c *Container) map[string]any {
	x, y, z := coords(k)
	return map[string]any{"kind": "container", "type": c.Kind, "x": x, "y": y, "z": z}
}

func (s *Server) automationSlot(c *Container, slot, face int, v *Stack, insert bool) bool {
	if v == nil || slot < 0 || slot >= len(c.Slots) {
		return false
	}
	switch c.Kind {
	case "furnace":
		if !insert {
			return face == 0 && (slot == 2 || slot == 1 && v.ID == itemBucket)
		}
		if face == 1 {
			return slot == 0
		}
		return face != 0 && slot == 1 && s.rules.byID[v.ID].Fuel > 0
	case "brew":
		if !insert {
			return face == 0 && slot < 3
		}
		if face == 1 {
			return slot == 3 && brewIngredient(s, v.ID)
		}
		if slot == 4 {
			return s.rules.byID[v.ID].Key == "blaze_powder" || s.rules.byID[v.ID].Name == "Cinder Dust"
		}
		return slot < 3 && s.isBottle(v.ID) && v.Count == 1
	}
	return true
}

// planContainerMove never edits live slots. Equal IDs with unequal metadata
// cannot merge; inserted/remaining items are independent deep snapshots.
func (s *Server) planContainerMove(src, dst *Container, srcFace, dstFace int) (Stacks, Stacks, int, int, *Stack) {
	for i, v := range src.Slots {
		if v == nil || !s.validStack(v, s.rules.max(v.ID)) || !s.automationSlot(src, i, srcFace, v, false) {
			continue
		}
		one := sized(v, 1)
		for pass := 0; pass < 2; pass++ {
			for j, at := range dst.Slots {
				if !s.automationSlot(dst, j, dstFace, one, true) {
					continue
				}
				if pass == 0 && (at == nil || !same(at, one) || at.Count >= s.rules.max(one.ID)) {
					continue
				}
				if pass == 1 && at != nil {
					continue
				}
				a, b := cloneStacks(src.Slots), cloneStacks(dst.Slots)
				a[i] = sized(v, v.Count-1)
				if at == nil {
					b[j] = one
				} else {
					b[j] = sized(at, at.Count+1)
				}
				return a, b, i, j, one
			}
		}
	}
	return nil, nil, 0, 0, nil
}

func (s *Server) publishContainerChanges(r *Room, keys ...uint64) {
	r.Dirty = true
	r.rt().redstoneDirty = true
	s.notifyMechanicContainers(r, keys...)
}

func (s *Server) moveHopperItem(r *Room, hk, sk, dk uint64, srcFace, dstFace int, reason string) bool {
	if sk == dk {
		return false
	}
	src, dst := s.automationContainer(r, sk), s.automationContainer(r, dk)
	hc := s.automationContainer(r, hk)
	if hc == nil || hc.Kind != "hopper" {
		return false
	}
	if src == nil || dst == nil {
		return false
	}
	sb, db := cloneStacks(src.Slots), cloneStacks(dst.Slots)
	sa, da, i, j, one := s.planContainerMove(src, dst, srcFace, dstFace)
	if one == nil {
		return false
	}
	data := map[string]any{"reason": reason, "actor": "server", "source": "hopper", "origin": containerTarget(sk, src), "target": containerTarget(dk, dst), "initiator": containerTarget(hk, hc), "stack": itemSnapshot(one), "item": itemSnapshot(one), "count": 1, "sourceSlot": i, "destinationSlot": j, "sourceBefore": sb, "sourceAfter": sa, "destinationBefore": db, "destinationAfter": da, "before": itemSnapshot(sb[i]), "after": itemSnapshot(sa[i])}
	if !s.allowed(api.InventoryMoving, r, nil, data) || !s.allowed(api.InventoryChanging, r, nil, data) {
		return false
	}
	if r.Containers[sk] != src || r.Containers[dk] != dst || !reflect.DeepEqual(src.Slots, sb) || !reflect.DeepEqual(dst.Slots, db) || s.automationContainer(r, sk) != src || s.automationContainer(r, dk) != dst {
		return false
	}
	copy(src.Slots, sa)
	copy(dst.Slots, da)
	if dst.Kind == "hopper" {
		getRedstone(r).Hoppers[dk] = hopperCooldown
	}
	s.publishContainerChanges(r, sk, dk)
	s.emit(api.InventoryChanged, r, nil, data)
	s.emit(api.InventoryMoved, r, nil, data)
	return true
}

func (s *Server) pickupHopperDrop(r *Room, k uint64) bool {
	c := s.automationContainer(r, k)
	if c == nil || c.Kind != "hopper" {
		return false
	}
	x, y, z := coords(k)
	for _, id := range sortedDropIDs(r) {
		d := r.Drops[id]
		if d == nil || d.Stack == nil || !s.validStack(d.Stack, s.rules.max(d.Stack.ID)) || math.Abs(d.X-float64(x)-.5) >= .6 || math.Abs(d.Z-float64(z)-.5) >= .6 || d.Y < float64(y) || d.Y > float64(y)+1.2 {
			continue
		}
		old := *d
		old.Stack = copyStack(d.Stack)
		before, after := cloneStacks(c.Slots), cloneStacks(c.Slots)
		remaining := d.Stack.Count
		for pass := 0; pass < 2; pass++ {
			for i, at := range after {
				if remaining == 0 {
					break
				}
				if pass == 0 && (at == nil || !same(at, d.Stack)) || pass == 1 && at != nil {
					continue
				}
				room := s.rules.max(d.Stack.ID)
				if at != nil {
					room -= at.Count
				}
				n := min(remaining, room)
				if n <= 0 {
					continue
				}
				if at == nil {
					after[i] = sized(d.Stack, n)
				} else {
					after[i] = sized(at, at.Count+n)
				}
				remaining -= n
			}
		}
		picked := d.Stack.Count - remaining
		if picked <= 0 {
			continue
		}
		data := map[string]any{"reason": "hopper_pickup", "actor": "server", "source": "hopper", "origin": map[string]any{"kind": "item", "entityId": id, "x": d.X, "y": d.Y, "z": d.Z}, "target": containerTarget(k, c), "initiator": containerTarget(k, c), "entityId": id, "stack": itemSnapshot(sized(d.Stack, picked)), "item": itemSnapshot(sized(d.Stack, picked)), "count": picked, "remaining": remaining, "before": itemSnapshot(d.Stack), "after": itemSnapshot(sized(d.Stack, remaining)), "destinationBefore": before, "destinationAfter": after}
		if !s.allowed(api.InventoryPickingUp, r, nil, data) || !s.allowed(api.InventoryChanging, r, nil, data) {
			return false
		}
		if r.Drops[id] != d || !reflect.DeepEqual(d, &old) || r.Containers[k] != c || !reflect.DeepEqual(c.Slots, before) || s.automationContainer(r, k) != c {
			return false
		}
		copy(c.Slots, after)
		if remaining == 0 {
			delete(r.Drops, id)
		} else {
			d.Stack = sized(d.Stack, remaining)
			for p := range r.Peers {
				if p.knownDrops[id] {
					p.enqueue(websocket.BinaryMessage, entityEvent(id, 6, uint16(remaining)))
				}
			}
		}
		s.syncDrops(r)
		s.publishContainerChanges(r, k)
		s.emit(api.InventoryChanged, r, nil, data)
		s.emit(api.InventoryPickedUp, r, nil, data)
		return true
	}
	return false
}

func (s *Server) activateHopper(r *Room, k uint64) bool {
	c := s.automationContainer(r, k)
	if c == nil || c.Kind != "hopper" {
		return false
	}
	x, y, z := coords(k)
	rs := getRedstone(r)
	if rs.Hoppers[k] > 0 || s.blockPowered(r, x, y, z) {
		return false
	}
	face := 0
	if state := mechanicSnapshot(r, k); state != nil && state.Kind == "hopper" {
		face = state.Facing
	} else if r.Ext != nil {
		if d := r.Ext.Blocks[k]; d != nil && d.Kind == "hopper" && d.Facing >= 0 && d.Facing < 6 && d.Facing != 1 {
			face = d.Facing
		}
	}
	off := facing6[face]
	pushed := s.moveHopperItem(r, k, k, blockKey(x+off[0], y+off[1], z+off[2]), face, face^1, "hopper_push")
	pulled := false
	if s.automationContainer(r, blockKey(x, y+1, z)) != nil {
		pulled = s.moveHopperItem(r, k, blockKey(x, y+1, z), k, 0, 1, "hopper_pull")
	} else {
		pulled = s.pickupHopperDrop(r, k)
	}
	if pushed || pulled {
		rs.Hoppers[k] = hopperCooldown
	}
	return pushed || pulled
}

func (s *Server) tickHoppers(r *Room) {
	keys := make([]uint64, 0)
	for k, c := range r.Containers {
		if c != nil && c.Kind == "hopper" {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	rs := getRedstone(r)
	// Decrement all first: a receiving hopper's fresh cooldown cannot be erased
	// by a later iteration, preventing same-tick chains and map-order speedups.
	for _, k := range keys {
		if rs.Hoppers[k] > 0 {
			rs.Hoppers[k]--
		}
	}
	for _, k := range keys {
		s.activateHopper(r, k)
	}
}
