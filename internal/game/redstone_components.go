package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"sort"
)

// RedstoneComponent is optional BlockData, shared by finite saves and MCC v1
// chunks. Old blocks without state receive deterministic defaults on load.
type RedstoneComponent struct {
	Kind        string `json:"kind"`
	Facing      int    `json:"facing,omitempty"`
	SupportFace int    `json:"supportFace,omitempty"`
	Delay       int    `json:"delay,omitempty"`
	Subtract    bool   `json:"subtract,omitempty"`
	Power       int    `json:"power,omitempty"`
	Powered     bool   `json:"powered,omitempty"`
	Extended    bool   `json:"extended,omitempty"`
	Sticky      bool   `json:"sticky,omitempty"`
	Countdown   int    `json:"countdown,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
	Locked      bool   `json:"locked,omitempty"`
}

var redstoneItemKinds = map[string]string{"redstone": "wire", "redstone_torch": "torch", "repeater": "repeater", "comparator": "comparator", "piston": "piston", "sticky_piston": "sticky_piston", "stone_pressure_plate": "stone_pressure_plate", "oak_pressure_plate": "oak_pressure_plate", "lever": "lever", "stone_button": "button", "oak_button": "wooden_button"}
var redstoneBlockKinds = map[int]string{108: "wire", 109: "torch", 166: "repeater", 167: "comparator", 168: "piston", 169: "sticky_piston", 174: "stone_pressure_plate", 175: "oak_pressure_plate"}

func (s *Server) redstoneKind(b int) string {
	if kind := redstoneBlockKinds[b]; kind != "" {
		return kind
	}
	if b >= 0 && b < len(s.rules.Names) {
		switch s.nameOf(b) {
		case "Lever":
			return "lever"
		case "Button", "Stone Button":
			return "button"
		case "Oak Button":
			return "wooden_button"
		}
	}
	return ""
}
func validRedstoneComponent(v *RedstoneComponent) bool {
	if v == nil {
		return true
	}
	known := false
	for _, kind := range redstoneItemKinds {
		if kind == v.Kind {
			known = true
			break
		}
	}
	if (v.Kind == "repeater" || v.Kind == "comparator") && v.Facing > 3 {
		return false
	}
	if v.Kind == "repeater" && v.Delay < 1 {
		return false
	}
	return known && v.Facing >= 0 && v.Facing <= 5 && v.SupportFace >= 0 && v.SupportFace <= 5 && v.Delay >= 0 && v.Delay <= 4 && v.Power >= 0 && v.Power <= 15 && v.Countdown >= 0 && v.Countdown <= 1000 && v.Timeout >= 0 && v.Timeout <= 1000
}
func cloneRedstone(v *RedstoneComponent) *RedstoneComponent {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func redstoneSnapshot(r *Room, k uint64) *RedstoneComponent {
	rs := getRedstone(r)
	var state *RedstoneComponent
	if r.Ext != nil && r.Ext.Blocks[k] != nil {
		state = cloneRedstone(r.Ext.Blocks[k].Redstone)
	}
	if state == nil {
		state = &RedstoneComponent{}
	}
	if value, ok := rs.Wires[k]; ok {
		state.Kind = "wire"
		state.Power = value
	}
	if value, ok := rs.Levers[k]; ok {
		state.Kind = "lever"
		state.Powered = value
	}
	if value, ok := rs.Buttons[k]; ok {
		if state.Kind == "" {
			state.Kind = "button"
		}
		state.Countdown = value
		state.Powered = value > 0
	}
	if value := rs.Repeaters[k]; value != nil {
		state.Kind = "repeater"
		state.Facing = value.Facing
		state.Delay = value.Delay
		state.Locked = value.Locked
		state.Powered = value.Powered
		state.Countdown = value.Countdown
	}
	if value := rs.Comparators[k]; value != nil {
		state.Kind = "comparator"
		state.Facing = value.Facing
		state.Subtract = value.Subtract
		state.Power = value.OutputPower
	}
	if value := rs.Pistons[k]; value != nil {
		if state.Kind != "piston" && state.Kind != "sticky_piston" {
			state.Kind = "piston"
		}
		state.Sticky = value.Sticky
		state.Facing = value.Facing
		state.Extended = value.Extended
	}
	if value := rs.PressurePlates[k]; value != nil {
		state.Kind = "stone_pressure_plate"
		if value.Wooden {
			state.Kind = "oak_pressure_plate"
		}
		state.Powered = value.Powered
		state.Timeout = value.Timeout
	}
	if state.Kind == "" {
		return nil
	}
	return state
}
func clearRuntimeRedstone(rs *RedstoneState, k uint64) {
	delete(rs.Wires, k)
	delete(rs.Levers, k)
	delete(rs.Buttons, k)
	delete(rs.Repeaters, k)
	delete(rs.Comparators, k)
	delete(rs.Pistons, k)
	delete(rs.PressurePlates, k)
}
func restoreRedstoneComponent(r *Room, k uint64, state *RedstoneComponent) {
	if state == nil || !validRedstoneComponent(state) {
		return
	}
	rs := getRedstone(r)
	clearRuntimeRedstone(rs, k)
	switch state.Kind {
	case "wire":
		rs.Wires[k] = state.Power
	case "lever":
		rs.Levers[k] = state.Powered
	case "button", "wooden_button":
		rs.Buttons[k] = state.Countdown
	case "repeater":
		rs.Repeaters[k] = &RepeaterBlock{Facing: state.Facing, Delay: max(1, state.Delay), Locked: state.Locked, Powered: state.Powered, Countdown: state.Countdown}
	case "comparator":
		rs.Comparators[k] = &ComparatorBlock{Facing: state.Facing, Subtract: state.Subtract, OutputPower: state.Power}
	case "piston", "sticky_piston":
		rs.Pistons[k] = &PistonBlock{Facing: state.Facing, Sticky: state.Kind == "sticky_piston" || state.Sticky, Extended: state.Extended}
	case "stone_pressure_plate", "oak_pressure_plate":
		rs.PressurePlates[k] = &PressurePlateBlock{Wooden: state.Kind == "oak_pressure_plate", Powered: state.Powered, Timeout: state.Timeout}
	}
}
func storeRedstoneComponent(r *Room, k uint64, state *RedstoneComponent) {
	e := r.blockExt()
	if e.Blocks == nil {
		e.Blocks = map[uint64]*BlockData{}
	}
	if e.Blocks[k] == nil {
		e.Blocks[k] = &BlockData{Kind: "redstone"}
	}
	e.Blocks[k].Redstone = cloneRedstone(state)
	if state != nil {
		e.Blocks[k].Facing = state.Facing
		e.Blocks[k].Sticky = state.Kind == "sticky_piston" || state.Sticky
		e.Blocks[k].Power = state.Power
	}
	restoreRedstoneComponent(r, k, state)
	r.Dirty = true
}
func (s *Server) clearRedstoneComponent(r *Room, k uint64) {
	clearRuntimeRedstone(getRedstone(r), k)
	if r.Ext != nil && r.Ext.Blocks[k] != nil && r.Ext.Blocks[k].Redstone != nil {
		if r.Ext.Blocks[k].Kind == "redstone" {
			delete(r.Ext.Blocks, k)
		} else {
			r.Ext.Blocks[k].Redstone = nil
		}
	}
}
func (s *Server) trackRedstoneBlock(r *Room, k uint64, b int) {
	kind := s.redstoneKind(b)
	if kind == "" {
		s.clearRedstoneComponent(r, k)
		return
	}
	if state := redstoneSnapshot(r, k); state != nil && state.Kind == kind {
		return
	}
	state := &RedstoneComponent{Kind: kind, SupportFace: 1}
	if kind == "repeater" {
		state.Delay = 1
	}
	if kind == "piston" || kind == "sticky_piston" {
		state.Facing = 1
	}
	if r.Ext != nil {
		if legacy := r.Ext.Blocks[k]; legacy != nil {
			state.Facing = legacy.Facing
			state.Power = legacy.Power
			state.Sticky = legacy.Sticky
			if (kind == "repeater" || kind == "comparator") && state.Facing > 3 {
				state.Facing = 0
			}
		}
	}
	storeRedstoneComponent(r, k, state)
}
func (s *Server) restoreLoadedRedstone(r *Room) {
	// Existing placed blocks may predate persistent component data. Restoring
	// them never consumes another item and never changes their block ID.
	for k, b := range r.Edits {
		if s.redstoneKind(b) != "" {
			s.trackRedstoneBlock(r, k, b)
		}
	}
	if len(getRedstone(r).Wires) > 0 {
		r.rt().redstoneDirty = true
	}
}
func (s *Server) persistRedstone(r *Room) {
	if r.rt().redstone == nil {
		return
	}
	keys := map[uint64]bool{}
	rs := getRedstone(r)
	for k := range rs.Wires {
		keys[k] = true
	}
	for k := range rs.Levers {
		keys[k] = true
	}
	for k := range rs.Buttons {
		keys[k] = true
	}
	for k := range rs.Repeaters {
		keys[k] = true
	}
	for k := range rs.Comparators {
		keys[k] = true
	}
	for k := range rs.Pistons {
		keys[k] = true
	}
	for k := range rs.PressurePlates {
		keys[k] = true
	}
	for k := range keys {
		x, y, z := coords(k)
		state := redstoneSnapshot(r, k)
		if state != nil && s.redstoneKind(s.block(r, x, y, z)) == state.Kind {
			storeRedstoneComponent(r, k, state)
		}
	}
}
func (s *Server) redstoneData(r *Room, p *Peer, k uint64, next *RedstoneComponent, reason string) map[string]any {
	x, y, z := coords(k)
	actor := "server"
	if p != nil {
		actor = p.sid
	}
	kind := ""
	if next != nil {
		kind = next.Kind
	} else if before := redstoneSnapshot(r, k); before != nil {
		kind = before.Kind
	}
	data := map[string]any{"reason": reason, "actor": actor, "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "component": kind, "componentBefore": cloneRedstone(redstoneSnapshot(r, k)), "componentAfter": cloneRedstone(next), "block": s.block(r, x, y, z)}
	if p != nil {
		data["stack"] = itemSnapshot(s.heldItem(p))
		data["before"] = itemSnapshot(s.heldItem(p))
		data["after"] = itemSnapshot(s.heldItem(p))
		data["slot"] = p.state.Inv.Selected
	}
	return data
}

func (s *Server) redstoneBlockChangeData(r *Room, p *Peer, k uint64, b int, reason string) map[string]any {
	x, y, z := coords(k)
	old := s.block(r, x, y, z)
	kind := s.redstoneKind(b)
	if kind == "" && s.redstoneKind(old) == "" {
		return nil
	}
	var next *RedstoneComponent
	if kind != "" {
		next = &RedstoneComponent{Kind: kind, SupportFace: 1}
		if kind == "repeater" {
			next.Delay = 1
		}
		if kind == "piston" || kind == "sticky_piston" {
			next.Facing = 1
		}
		if current := redstoneSnapshot(r, k); current != nil && current.Kind == kind {
			next = current
		}
	}
	data := s.redstoneData(r, p, k, next, reason)
	data["oldBlock"] = old
	data["block"] = b
	if p == nil {
		data["actor"] = s.commandActor(nil)
	}
	return data
}
func (s *Server) redstoneSupported(r *Room, k uint64, b int) bool {
	kind := s.redstoneKind(b)
	if kind == "" || kind == "piston" || kind == "sticky_piston" {
		return true
	}
	x, y, z := coords(k)
	return s.supportsTorch(s.block(r, x, y-1, z))
}
func (s *Server) changeRedstone(r *Room, p *Peer, k uint64, next *RedstoneComponent, reason string) bool {
	if !validRedstoneComponent(next) || reflect.DeepEqual(redstoneSnapshot(r, k), next) {
		return false
	}
	data := s.redstoneData(r, p, k, next, reason)
	if !s.allowed(api.RedstoneComponentChanging, r, p, data) {
		return false
	}
	storeRedstoneComponent(r, k, next)
	s.broadcastRedstone(r, k)
	s.emit(api.RedstoneComponentChanged, r, p, data)
	return true
}

func (s *Server) handleRedstoneUse(p *Peer, m Message, expected *api.ItemStack, compare bool) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Inv == nil {
		return false
	}
	r := p.room
	if !s.reachable(p, m) {
		return false
	}
	target := s.block(r, m.X, m.Y, m.Z)
	kind := s.redstoneKind(target)
	held, it := s.heldStack(p)
	heldKind := redstoneItemKinds[it.Key]
	if heldKind == "" {
		heldKind = s.redstoneKind(it.PlacesID)
	}
	interactive := kind == "lever" || kind == "button" || kind == "wooden_button" || kind == "repeater" || kind == "comparator"
	if heldKind == "" && (!interactive || p.flags&1 != 0) {
		return false
	}
	if p.state.Dead || !s.canBuild(p) || compare && !reflect.DeepEqual(itemSnapshot(held), expected) {
		return true
	}
	if interactive && p.flags&1 == 0 {
		k := blockKey(m.X, m.Y, m.Z)
		s.trackRedstoneBlock(r, k, target)
		next := cloneRedstone(redstoneSnapshot(r, k))
		if next == nil {
			return true
		}
		switch kind {
		case "lever":
			next.Powered = !next.Powered
		case "button", "wooden_button":
			next.Powered = true
			next.Countdown = 20
			if kind == "wooden_button" {
				next.Countdown = 30
			}
		case "repeater":
			next.Delay = next.Delay%4 + 1
		case "comparator":
			next.Subtract = !next.Subtract
		}
		if s.changeRedstone(r, p, k, next, "redstone_interact") {
			s.recalculateWires(r)
			s.updateRedstoneAround(r, m.X, m.Y, m.Z)
			soundAt(r, "block.comparator.click", float64(m.X)+.5, float64(m.Y)+.1, float64(m.Z)+.5, nil)
		}
		return true
	}
	if heldKind == "" {
		return false
	}
	if !s.validStack(held, s.rules.max(it.ID)) || it.PlacesID <= 0 || it.PlacesID >= len(s.rules.Names) || s.redstoneKind(it.PlacesID) != heldKind {
		return true
	}
	off := faceOffsets[m.F]
	x, y, z := m.X+off[0], m.Y+off[1], m.Z+off[2]
	face := m.F
	if target >= 0 && target < len(s.rules.Kinds) && s.rules.Kinds[target] == 5 && kind == "" && !isTorch(target) && s.nameOf(target) != "Fire" {
		x, y, z = m.X, m.Y, m.Z
		face = 1
	}
	if !r.inside(x, z) || y < 1 || y >= 128 {
		return true
	}
	old := s.block(r, x, y, z)
	if old != 0 && !(old < len(s.rules.Kinds) && s.rules.Kinds[old] == 5 && s.redstoneKind(old) == "" && !isTorch(old) && s.nameOf(old) != "Fire") {
		return true
	}
	next := &RedstoneComponent{Kind: heldKind, SupportFace: face}
	if heldKind == "piston" || heldKind == "sticky_piston" {
		next.Facing = pistonFacingFromPlayer(p)
	} else {
		if heldKind == "torch" || heldKind == "lever" || heldKind == "button" || heldKind == "wooden_button" {
			support := faceOffsets[face]
			if face == 0 || !s.supportsTorch(s.block(r, x-support[0], y-support[1], z-support[2])) {
				p.sys("红石组件需要完整方块支撑。")
				return true
			}
		} else {
			if face != 1 || !s.supportsTorch(s.block(r, x, y-1, z)) {
				p.sys("这个红石组件需要放在完整方块的顶面。")
				return true
			}
		}
		next.Facing = ((int(math.Floor((p.state.Yaw+math.Pi/4)/(math.Pi/2)))+2)%4 + 4) % 4
		if heldKind == "repeater" {
			next.Delay = 1
		}
	}
	if s.rules.Kinds[it.PlacesID] == 1 {
		for other := range r.Peers {
			if a := other.state; a != nil && !a.Dead && float64(x+1) > a.X-.3 && float64(x) < a.X+.3 && float64(z+1) > a.Z-.3 && float64(z) < a.Z+.3 && float64(y+1) > a.Y && float64(y) < a.Y+1.8 {
				return true
			}
		}
	}
	k := blockKey(x, y, z)
	slot := p.state.Inv.Selected
	before := itemSnapshot(held)
	after := copyStack(held)
	count := 0
	if !s.isCreative(p) {
		after = sized(held, held.Count-1)
		count = 1
	}
	data := s.redstoneData(r, p, k, next, "redstone_place")
	data["oldBlock"] = old
	data["block"] = it.PlacesID
	data["stack"] = before.Clone()
	data["before"] = before.Clone()
	data["after"] = itemSnapshot(after)
	data["item"] = held.ID
	data["count"] = count
	data["face"] = face
	if !s.allowed(api.BlockPlacing, r, p, data) || !s.allowed(api.RedstoneComponentChanging, r, p, data) || count != 0 && !s.allowed(api.InventoryChanging, r, p, data) {
		return true
	}
	if p.state.Inv.Selected != slot || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), before) {
		return true
	}
	p.state.Inv.Slots[slot] = after
	storeRedstoneComponent(r, k, next)
	s.commitPlayerBlock(p, k, it.PlacesID, data)
	s.broadcastRedstone(r, k)
	if count != 0 {
		s.emit(api.InventoryChanged, r, p, data)
	}
	s.inventory(p, 0, 0)
	s.recalculateWires(r)
	s.updateRedstoneAround(r, x, y, z)
	s.checkActuators(r, x, y, z)
	return true
}

func (s *Server) redstoneWirePacket(r *Room, k uint64) map[string]any {
	x, y, z := coords(k)
	v := redstoneSnapshot(r, k)
	out := map[string]any{"x": x, "y": y, "z": z, "id": s.block(r, x, y, z), "power": 0, "facing": 0, "delay": 1, "sub": false, "extended": false}
	if v != nil {
		power := v.Power
		if v.Powered {
			power = 15
		}
		if v.Kind == "torch" {
			// Torch output is computed by the existing inverter rules rather
			// than the wire's stored level. Send that same output to renderers.
			power = s.directPowerSource(r, x, y, z, 0, 1, 0)
		}
		out["power"] = power
		out["facing"] = v.Facing
		out["delay"] = max(1, v.Delay)
		out["sub"] = v.Subtract
		out["extended"] = v.Extended
		out["torchFace"] = v.SupportFace
	}
	return out
}
func (s *Server) redstoneEnvelope(p *Peer, blocks []any) map[string]any {
	return map[string]any{"t": "rs", "blocks": blocks, "epoch": p.epoch, "worldId": p.room.ID, "dim": p.room.dimName()}
}
func (s *Server) broadcastRedstone(r *Room, k uint64) {
	entry := s.redstoneWirePacket(r, k)
	for p := range r.Peers {
		p.json(s.redstoneEnvelope(p, []any{entry}))
	}
}
func (s *Server) syncRedstoneFor(p *Peer) {
	r := p.room
	if r.Ext == nil {
		return
	}
	keys := []uint64{}
	for k, d := range r.Ext.Blocks {
		if d != nil && d.Redstone != nil {
			x, _, z := coords(k)
			if p.knows(x, z) || !r.Infinite && r.Mode == "creative" {
				keys = append(keys, k)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	blocks := []any{}
	for _, k := range keys {
		blocks = append(blocks, s.redstoneWirePacket(r, k))
		if len(blocks) == 256 {
			p.json(s.redstoneEnvelope(p, blocks))
			blocks = []any{}
		}
	}
	if len(blocks) > 0 {
		p.json(s.redstoneEnvelope(p, blocks))
	}
}

func (s *Server) tickRedstoneComponents(r *Room) {
	rs := getRedstone(r)
	for k, ticks := range rs.Buttons {
		if ticks <= 0 {
			continue
		}
		next := redstoneSnapshot(r, k)
		next.Countdown = ticks - 1
		next.Powered = next.Countdown > 0
		if s.changeRedstone(r, nil, k, next, "redstone_button_tick") && next.Countdown == 0 {
			x, y, z := coords(k)
			s.recalculateWires(r)
			s.updateRedstoneAround(r, x, y, z)
		}
	}
	for k, pl := range rs.PressurePlates {
		x, y, z := coords(k)
		next := redstoneSnapshot(r, k)
		if s.entityOnPlate(r, x, y, z) {
			next.Timeout = 10
			next.Powered = true
		} else if pl.Powered {
			next.Timeout = max(0, pl.Timeout-1)
			if next.Timeout == 0 {
				next.Powered = false
			}
		}
		if s.changeRedstone(r, nil, k, next, "redstone_plate_tick") && next.Powered != pl.Powered {
			s.recalculateWires(r)
			s.updateRedstoneAround(r, x, y, z)
		}
	}
	for k, rep := range rs.Repeaters {
		x, y, z := coords(k)
		next := redstoneSnapshot(r, k)
		next.Locked = s.isRepeaterSideLocked(r, x, y, z, rep.Facing)
		if !next.Locked {
			input := s.isRepeaterInputPowered(r, x, y, z, rep.Facing)
			if input != rep.Powered {
				if next.Countdown <= 0 {
					next.Countdown = max(2, rep.Delay*2)
				}
				next.Countdown--
				if next.Countdown == 0 {
					next.Powered = input
				}
			} else {
				next.Countdown = 0
			}
		}
		if s.changeRedstone(r, nil, k, next, "redstone_repeater_tick") && next.Powered != rep.Powered {
			s.recalculateWires(r)
			s.updateRedstoneAround(r, x, y, z)
		}
	}
	for k, cmp := range rs.Comparators {
		x, y, z := coords(k)
		next := redstoneSnapshot(r, k)
		next.Power = s.calculateComparatorOutput(r, x, y, z, cmp)
		if s.changeRedstone(r, nil, k, next, "redstone_comparator_tick") {
			s.recalculateWires(r)
			s.updateRedstoneAround(r, x, y, z)
		}
	}
}
