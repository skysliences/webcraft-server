package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"sort"
)

const defaultTNTFuse = 40
const maxTNTFuse = 12000

// TNTState persists the original igniter even after logout or a restart.
// BlockData.Fuse remains the compatible remaining 100ms tick counter.
type TNTState struct {
	Actor    string         `json:"actor"`
	Source   string         `json:"source,omitempty"`
	Reason   string         `json:"reason"`
	Attacker *api.Combatant `json:"attacker,omitempty"`
	Stack    *api.ItemStack `json:"stack,omitempty"`
	Slot     int            `json:"slot"`
}

func cloneTNTState(v *TNTState) *TNTState {
	if v == nil {
		return nil
	}
	o := *v
	o.Stack = v.Stack.Clone()
	if v.Attacker != nil {
		a := *v.Attacker
		o.Attacker = &a
	}
	return &o
}
func (s *Server) validTNTState(v *TNTState) bool {
	if v == nil {
		return true
	}
	if len(v.Actor) > 128 || len(v.Source) > 256 || len(v.Reason) > 64 || v.Slot < -1 || v.Slot >= 46 {
		return false
	}
	if v.Stack != nil && !s.validStack((*Stack)(v.Stack), s.rules.max(v.Stack.ID)) {
		return false
	}
	if a := v.Attacker; a != nil && (!number(a.X, -30000000, 30000000) || !number(a.Y, -1024, 4096) || !number(a.Z, -30000000, 30000000) || !number(a.Health, 0, 10000) || len(a.Name) > 128) {
		return false
	}
	return true
}
func (s *Server) primedTNT(r *Room, k uint64) bool {
	if r.Ext == nil || r.Ext.Blocks[k] == nil {
		return false
	}
	d := r.Ext.Blocks[k]
	x, y, z := coords(k)
	return (d.TNT != nil || d.Fuse > 0) && s.block(r, x, y, z) == blkExplosiveTNT
}
func (s *Server) tntOrigin(r *Room, k uint64) *TNTState {
	if r.Ext != nil && r.Ext.Blocks[k] != nil && r.Ext.Blocks[k].TNT != nil {
		return cloneTNTState(r.Ext.Blocks[k].TNT)
	}
	return &TNTState{Actor: "server", Reason: "legacy", Slot: -1}
}
func (s *Server) ignitionOrigin(p *Peer, reason string) *TNTState {
	o := &TNTState{Actor: s.commandActor(nil), Source: s.pluginSource, Reason: reason, Slot: -1}
	if p != nil {
		o.Actor = p.sid
		o.Attacker = playerTarget(p)
		o.Stack = itemSnapshot(s.heldItem(p))
		o.Slot = p.state.Inv.Selected
	}
	return o
}
func (s *Server) tntData(r *Room, k uint64, o *TNTState, fuse int) map[string]any {
	x, y, z := coords(k)
	return map[string]any{"actor": o.Actor, "source": o.Source, "reason": o.Reason, "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "block": blkExplosiveTNT, "fuseTicks": fuse, "slot": o.Slot, "stack": o.Stack.Clone(), "before": o.Stack.Clone(), "after": o.Stack.Clone(), "attacker": cloneTNTState(o).Attacker}
}

// preparedTNTPrime separates protection, mutation and publication so a
// dispenser can commit its source slot and its target in one transaction.
type preparedTNTPrime struct {
	s        *Server
	r        *Room
	p        *Peer
	key      uint64
	oldBlock int
	origin   *TNTState
	fuse     int
	data     map[string]any
}

func (v *preparedTNTPrime) valid() bool {
	x, y, z := coords(v.key)
	return v.s.block(v.r, x, y, z) == v.oldBlock && !v.s.primedTNT(v.r, v.key)
}
func (v *preparedTNTPrime) commit() {
	e := v.r.blockExt()
	if e.Blocks == nil {
		e.Blocks = map[uint64]*BlockData{}
	}
	d := e.Blocks[v.key]
	if d == nil {
		d = &BlockData{Kind: "tnt"}
		e.Blocks[v.key] = d
	}
	d.Fuse = v.fuse
	d.TNT = cloneTNTState(v.origin)
	v.r.Dirty = true
}
func (v *preparedTNTPrime) publish() {
	x, y, z := coords(v.key)
	v.s.broadcastTNT(v.r, v.key, true)
	v.s.tntSound(v.r, "entity.tnt.primed", float64(x)+.5, float64(y)+.5, float64(z)+.5)
	v.s.emit(api.TNTPrimed, v.r, v.p, v.data)
}
func (s *Server) prepareTNTPrime(r *Room, p *Peer, x, y, z, fuse int, origin *TNTState, futureBlock bool, context map[string]any) (*preparedTNTPrime, bool) {
	k := blockKey(x, y, z)
	old := s.block(r, x, y, z)
	if !r.inside(x, z) || y < 1 || y >= 128 || s.primedTNT(r, k) || (!futureBlock && old != blkExplosiveTNT) || (futureBlock && old != 0) {
		return nil, false
	}
	if fuse == 0 {
		fuse = defaultTNTFuse
	}
	if fuse < 1 || fuse > maxTNTFuse || origin == nil || !s.validTNTState(origin) {
		return nil, false
	}
	data := s.tntData(r, k, origin, fuse)
	for field, value := range context {
		data[field] = value
	}
	data["x"], data["y"], data["z"], data["block"], data["fuseTicks"] = x, y, z, blkExplosiveTNT, fuse
	data["target"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	e := s.emit(api.TNTPriming, r, p, data)
	n := e.Number("fuseTicks")
	if e.Cancelled || !number(n, 1, maxTNTFuse) || n != math.Trunc(n) {
		return nil, false
	}
	data["fuseTicks"] = int(n)
	return &preparedTNTPrime{s: s, r: r, p: p, key: k, oldBlock: old, origin: cloneTNTState(origin), fuse: int(n), data: data}, true
}

// A flint operation reserves its durability and inventory changes before any
// TNT state is committed. Lore and custom data are copied with both snapshots.
func (s *Server) primeTNT(r *Room, p *Peer, x, y, z, fuse int, reason string, inherited *TNTState) bool {
	k := blockKey(x, y, z)
	if !r.inside(x, z) || y < 1 || y >= 128 || s.block(r, x, y, z) != blkExplosiveTNT || s.primedTNT(r, k) {
		return false
	}
	if p != nil && (p.state == nil || p.state.Inv == nil || p.state.Dead || !s.canBuild(p) || !s.reachable(p, Message{X: x, Y: y, Z: z, F: 1})) {
		return false
	}
	if fuse == 0 {
		fuse = defaultTNTFuse
	}
	if fuse < 1 || fuse > maxTNTFuse {
		return false
	}
	o := s.ignitionOrigin(p, reason)
	if inherited != nil {
		o = cloneTNTState(inherited)
		o.Reason = reason
	}
	plan, ok := s.prepareTNTPrime(r, p, x, y, z, fuse, o, false, nil)
	if !ok {
		return false
	}
	data := plan.data
	var after *Stack
	var wearData map[string]any
	changed := false
	if p != nil && reason == "flint_and_steel" {
		held := s.heldItem(p)
		if held == nil || held.ID != itemFlintAndSteel || !s.validStack(held, s.rules.max(held.ID)) {
			return false
		}
		after = copyStack(held)
		if !s.isCreative(p) && (held.Meta == nil || !held.Meta.Unbreakable) {
			wear := s.unbreakingWear(p, held, 1)
			if wear > 0 {
				wearData = map[string]any{"reason": reason, "actor": o.Actor, "target": data["target"], "slot": o.Slot, "stack": o.Stack.Clone(), "amount": wear, "damageBefore": held.Damage}
				we := s.emit(api.ItemWearing, r, p, wearData)
				amount := we.Number("amount")
				if we.Cancelled || !dispenserNumericField(we, "amount") || !number(amount, 0, 1000) || amount != math.Trunc(amount) {
					return false
				}
				if amount > 0 {
					after.Damage += int(amount)
					if after.Damage >= s.rules.byID[held.ID].Durability {
						after = nil
					}
					changed = true
					wearData["amount"] = int(amount)
					wearData["damageAfter"] = held.Damage + int(amount)
					wearData["broken"] = after == nil
				} else {
					wearData = nil
				}
			}
		}
		data["after"] = itemSnapshot(after)
		if changed && !s.allowed(api.InventoryChanging, r, p, data) {
			return false
		}
	}
	if p != nil && (p.state.Inv.Selected != o.Slot || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), o.Stack)) {
		return false
	}
	if !plan.valid() {
		return false
	}
	plan.commit()
	if changed {
		p.state.Inv.Slots[o.Slot] = after
	}
	if changed {
		if wearData != nil {
			wearData["before"] = o.Stack.Clone()
			wearData["after"] = itemSnapshot(after)
			s.emit(api.ItemWorn, r, p, wearData)
		}
		s.emit(api.InventoryChanged, r, p, data)
		s.inventory(p, 0, 0)
	}
	plan.publish()
	return true
}
func (s *Server) tntPacket(r *Room, k uint64, primed bool) map[string]any {
	x, y, z := coords(k)
	fuse := 0
	o := s.tntOrigin(r, k)
	if primed && r.Ext != nil && r.Ext.Blocks[k] != nil {
		fuse = r.Ext.Blocks[k].Fuse
	}
	return map[string]any{"x": x, "y": y, "z": z, "primed": primed, "fuseTicks": fuse, "actor": o.Actor, "source": o.Source, "reason": o.Reason}
}
func (s *Server) tntEnvelope(p *Peer, blocks []any, reset bool) map[string]any {
	return map[string]any{"t": "tnt", "epoch": p.epoch, "worldId": p.room.ID, "dim": p.room.dimName(), "reset": reset, "blocks": blocks}
}
func (s *Server) broadcastTNT(r *Room, k uint64, primed bool, origins ...*TNTState) {
	packet := s.tntPacket(r, k, primed)
	if len(origins) > 0 && origins[0] != nil {
		packet["actor"] = origins[0].Actor
		packet["source"] = origins[0].Source
		packet["reason"] = origins[0].Reason
	}
	for p := range r.Peers {
		p.json(s.tntEnvelope(p, []any{packet}, false))
	}
}
func (s *Server) syncTNTFor(p *Peer) {
	blocks := []any{}
	r := p.room
	if r.Ext != nil {
		for k := range r.Ext.Blocks {
			if s.primedTNT(r, k) {
				x, _, z := coords(k)
				if p.knows(x, z) || !r.Infinite && r.Mode == "creative" {
					blocks = append(blocks, s.tntPacket(r, k, true))
				}
			}
		}
	}
	p.json(s.tntEnvelope(p, blocks, true))
}
func (s *Server) tntSound(r *Room, name string, x, y, z float64) {
	for p := range r.Peers {
		if p.state != nil && math.Abs(p.state.X-x) <= 48 && math.Abs(p.state.Z-z) <= 48 {
			p.json(map[string]any{"t": "snd", "e": name, "x": x, "y": y, "z": z, "epoch": p.epoch, "worldId": r.ID, "dim": r.dimName()})
		}
	}
}
func (s *Server) tickPrimedTNT(r *Room) {
	if r.Ext == nil {
		return
	}
	keys := []uint64{}
	for k := range r.Ext.Blocks {
		if s.primedTNT(r, k) {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		if !s.primedTNT(r, k) {
			continue
		}
		d := r.Ext.Blocks[k]
		if d.TNT == nil {
			d.TNT = s.tntOrigin(r, k)
		}
		if d.Fuse > 0 {
			d.Fuse--
			r.Dirty = true
		}
		if d.Fuse > 0 {
			s.broadcastTNT(r, k, true)
			continue
		}
		x, y, z := coords(k)
		if !s.explodeAttributed(r, float64(x)+.5, float64(y)+.5, float64(z)+.5, 4, true, d.TNT, [3]int{x, y, z}) {
			d.Fuse = 10
			r.Dirty = true
			s.broadcastTNT(r, k, true)
		}
	}
}
func (s *Server) fireOrigin(r *Room, k uint64) *TNTState {
	if r.Ext != nil && r.Ext.Blocks[k] != nil {
		return cloneTNTState(r.Ext.Blocks[k].Fire)
	}
	return nil
}
func (s *Server) inheritFire(r *Room, k uint64, o *TNTState) {
	if o == nil {
		return
	}
	e := r.blockExt()
	if e.Blocks == nil {
		e.Blocks = map[uint64]*BlockData{}
	}
	if e.Blocks[k] == nil {
		e.Blocks[k] = &BlockData{Kind: "fire"}
	}
	e.Blocks[k].Fire = cloneTNTState(o)
	r.Dirty = true
}
func (s *Server) clearInactiveFire(r *Room, k uint64, b int) {
	if b == blkFlintFire || r.Ext == nil || r.Ext.Blocks[k] == nil || r.Ext.Blocks[k].Fire == nil {
		return
	}
	d := r.Ext.Blocks[k]
	d.Fire = nil
	if d.Kind == "fire" && d.TNT == nil && d.Redstone == nil {
		delete(r.Ext.Blocks, k)
	}
}

// Per-cell explosions commit the block and its reserved drops together. A
// drop/physics veto cannot consume IDs, remove the cell, or publish loot.
func (s *Server) explosionBlock(r *Room, x, y, z, b int, stacks Stacks, o *TNTState) (committed bool) {
	if o == nil {
		o = s.ignitionOrigin(nil, "explosion")
	}
	if s.block(r, x, y, z) != b || b == 0 || b == blkExplosiveTNT {
		return false
	}
	if !s.gameRule(r, "doTileDrops") {
		stacks = nil
	}
	previousID := r.NextDropID
	defer func() {
		if !committed {
			r.NextDropID = previousID
		}
	}()
	origin := map[string]any{"actor": o.Actor, "source": o.Source, "origin": cloneTNTState(o), "attacker": cloneTNTState(o).Attacker, "weapon": o.Stack.Clone(), "cause": "explosion", "ignitionReason": o.Reason}
	prepared, err := s.prepareDropsWithContext(r, nil, stacks, float64(x)+.5, float64(y)+.5, float64(z)+.5, "explosion", origin, true)
	if err != nil || s.block(r, x, y, z) != b {
		return false
	}
	context := map[string]any{}
	for field, value := range origin {
		context[field] = value
	}
	context["target"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	context["stack"] = o.Stack.Clone()
	context["before"] = o.Stack.Clone()
	context["after"] = o.Stack.Clone()
	if !s.setBlocksWithContext(r, [][4]int{{x, y, z, 0}}, 0, "explosion", func() { s.publishDropsWithContext(r, nil, prepared, "explosion", origin) }, context) {
		return false
	}
	committed = true
	return true
}
func init() { onRoomTick(func(s *Server, r *Room) { s.tickPrimedTNT(r) }) }
