package game

import (
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

// Administrative restoration uses the same protected healing/effect paths as
// gameplay. All coupled state changes commit after their before events.
func (s *Server) restoreCommandPlayer(actor, target *Peer, heal bool) bool {
	if target == nil || target.state == nil || target.state.Dead {
		return false
	}
	st, ext := target.state, target.ext()
	hp, food, sat, exhaustion, air, fire := st.Health, st.Food, st.Saturation, st.Exhaustion, target.air, ext.FireTicks
	inv := cloneStacks(st.Inv.Slots)
	effects := map[string]*Effect{}
	for name, value := range ext.Effects {
		if value != nil {
			clone := *value
			effects[name] = &clone
		} else {
			effects[name] = nil
		}
	}
	if ext.Effects == nil {
		effects = nil
	}
	reason := "command_feed"
	nextExhaustion := exhaustion
	if heal {
		reason = "command_heal"
		nextExhaustion = 0
	}
	data := map[string]any{"reason": reason, "actor": s.commandActor(actor), "actorPlayer": playerTarget(actor), "target": playerTarget(target), "stack": itemSnapshot(s.heldItem(target)), "foodBefore": food, "foodAfter": 20, "saturationBefore": sat, "saturationAfter": 5.0, "exhaustionBefore": exhaustion, "exhaustionAfter": nextExhaustion, "healthBefore": hp, "healthAfter": hp}
	if !s.allowed(api.PlayerFoodChanging, target.room, target, data) {
		return false
	}
	valid := func() bool {
		return !st.Dead && st.Health == hp && st.Food == food && st.Saturation == sat && st.Exhaustion == exhaustion && target.air == air && ext.FireTicks == fire && reflect.DeepEqual(ext.Effects, effects) && reflect.DeepEqual(st.Inv.Slots, inv)
	}
	commit := func() bool {
		if !valid() {
			return false
		}
		st.Food, st.Saturation, st.Exhaustion = 20, 5, nextExhaustion
		if heal {
			ext.FireTicks = 0
			target.air = 300
			ext.Air = 300
		}
		target.room.Dirty = true
		target.room.root().Dirty = true
		return true
	}
	if heal {
		healCommit := func() bool {
			amount := s.maxHealth(target) - st.Health
			if amount > 0 {
				return s.heal(target, amount, "command", commit)
			}
			return commit()
		}
		if !s.clearEffects(target, "command", healCommit) {
			return false
		}
	} else if !commit() {
		return false
	}
	data["healthAfter"], data["foodAfter"], data["saturationAfter"], data["exhaustionAfter"] = st.Health, st.Food, st.Saturation, st.Exhaustion
	s.emit(api.PlayerFoodChanged, target.room, target, data)
	s.health(target)
	if heal {
		soundAt(target.room, "entity.player.levelup", st.X, st.Y, st.Z, nil)
	}
	return true
}

// Clear all targets only after every inventory protection check succeeds.
// Snapshot the whole inventory, cursor and recovery queue for the final CAS;
// only the selected inventory stacks are removed, keeping other item metadata.
func (s *Server) clearCommandInventories(actor *Peer, targets []*Peer, filter int) (int, bool) {
	type plan struct {
		p                       *Peer
		before, after, recovery Stacks
		cursor                  *Stack
		data                    map[string]any
	}
	plans := make([]plan, 0, len(targets))
	total := 0
	for _, target := range targets {
		if target == nil || target.state == nil || target.state.Inv == nil {
			return 0, false
		}
		before := cloneStacks(target.state.Inv.Slots)
		cursor := copyStack(target.state.Cursor)
		var recovery Stacks
		if target.state.Recovery != nil {
			recovery = cloneStacks(target.state.Recovery)
		}
		after := cloneStacks(before)
		removed := Stacks{}
		count := 0
		for i, stk := range after {
			if stk != nil && (filter == 0 || stk.ID == filter) {
				count += stk.Count
				removed = append(removed, copyStack(stk))
				after[i] = nil
			}
		}
		data := map[string]any{"reason": "command_clear", "actor": s.commandActor(actor), "actorPlayer": playerTarget(actor), "target": playerTarget(target), "item": filter, "count": count, "before": cloneStacks(before), "after": cloneStacks(after), "inventoryBefore": cloneStacks(before), "inventoryAfter": cloneStacks(after), "removed": removed, "cursor": itemSnapshot(cursor), "recovery": cloneStacks(recovery)}
		if !s.allowed(api.InventoryChanging, target.room, target, data) {
			return 0, false
		}
		plans = append(plans, plan{target, before, after, recovery, cursor, data})
		total += count
	}
	for _, plan := range plans {
		if !reflect.DeepEqual(plan.p.state.Inv.Slots, plan.before) || !reflect.DeepEqual(plan.p.state.Cursor, plan.cursor) || !reflect.DeepEqual(plan.p.state.Recovery, plan.recovery) {
			return 0, false
		}
	}
	for _, plan := range plans {
		plan.p.state.Inv.Slots = cloneStacks(plan.after)
		plan.p.room.Dirty = true
		plan.p.room.root().Dirty = true
	}
	for _, plan := range plans {
		s.emit(api.InventoryChanged, plan.p.room, plan.p, plan.data)
		s.inventory(plan.p, 0, 0)
	}
	return total, true
}

func commandRestoreFailure(p *Peer, name string) {
	p.sys(fmt.Sprintf("/%s 被保护规则阻止，目标状态未更改。", name))
}
