package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

func init() {
	// Catch eggs before container and block-use hooks: a spawn egg is an item
	// operation and never writes its ID into the world as a block.
	onPlace(-100, func(s *Server, p *Peer, m Message, target int) bool {
		_, item := s.heldStack(p)
		if item.SpawnEgg == "" {
			return false
		}
		s.useSpawnEgg(p, m, nil)
		return true
	})
}

// useSpawnEgg is shared by packets and SDK actions. All before events complete
// before either the inventory or the entity is committed. Its payload is
// deliberately read-only; plugins cancel protection events or use SDK actions.
func (s *Server) useSpawnEgg(p *Peer, m Message, expected *api.ItemStack) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Dead || p.state.Inv == nil || p.state.Inv.Selected < 0 || p.state.Inv.Selected >= len(p.state.Inv.Slots) || !s.canBuild(p) || !s.reachable(p, m) {
		return false
	}
	r := p.room
	held, item := s.heldStack(p)
	if item.SpawnEgg == "" || !s.validStack(held, s.rules.max(item.ID)) {
		return false
	}
	before := itemSnapshot(held)
	if expected != nil && !reflect.DeepEqual(before, expected) {
		return false
	}
	off := faceOffsets[m.F]
	x, y, z := float64(m.X+off[0])+.5, float64(m.Y+off[1]), float64(m.Z+off[2])+.5
	if !s.mobFits(r, item.SpawnEgg, x, y, z) || !s.mobSolid(r, m.X+off[0], m.Y+off[1]-1, m.Z+off[2]) || !s.mobCapacity(r, 1) {
		p.sys("生成蛋需要对空旷地面使用，且世界生物数量未达服务器安全上限。")
		return false
	}
	slot := p.state.Inv.Selected
	after := copyStack(held)
	consumed := 0
	if !s.isCreative(p) {
		after = sized(held, held.Count-1)
		consumed = 1
	}
	data := map[string]any{"reason": "spawn_egg", "actor": p.sid, "target": map[string]any{"x": x, "y": y, "z": z}, "type": item.SpawnEgg, "x": x, "y": y, "z": z, "slot": slot, "item": held.ID, "count": consumed, "stack": before.Clone(), "before": before.Clone(), "after": itemSnapshot(after), "clickX": m.X, "clickY": m.Y, "clickZ": m.Z, "face": m.F}
	if !s.allowed(api.ItemSpawnEggUsing, r, p, data) {
		return false
	}
	mob, err := s.prepareMob(r, p, item.SpawnEgg, x, y, z, "spawn_egg", data)
	if err != nil {
		p.sys(err.Error())
		return false
	}
	data["entityId"] = mob.ID
	data["target"] = map[string]any{"entityId": mob.ID, "x": x, "y": y, "z": z}
	if consumed != 0 && !s.allowed(api.InventoryChanging, r, p, data) {
		return false
	}
	// Compare immediately before commit as well as at action receipt. The
	// original snapshot also keeps Lore/custom data on the remaining stack.
	if p.state.Inv.Selected != slot || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), before) {
		return false
	}
	p.state.Inv.Slots[slot] = after
	r.Dirty = true
	s.publishMob(r, p, mob, "spawn_egg", data)
	if consumed != 0 {
		s.emit(api.InventoryChanged, r, p, data)
	}
	s.emit(api.ItemSpawnEggUsed, r, p, data)
	s.inventory(p, 0, 0)
	return true
}
