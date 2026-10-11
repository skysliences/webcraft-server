package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"sort"
	"unicode/utf8"
)

// Optional v8 block metadata, shared by finite saves and MCC v1 chunks.
// Inventory records retain the same Stack representation and metadata rules.
type MechanicState struct {
	Kind        string    `json:"kind"`
	Facing      int       `json:"facing,omitempty"`
	SupportFace int       `json:"supportFace,omitempty"`
	Power       int       `json:"power,omitempty"`
	Powered     bool      `json:"powered,omitempty"`
	Pulse       int       `json:"pulse,omitempty"`
	Inverted    bool      `json:"inverted,omitempty"`
	Level       int       `json:"level,omitempty"`
	Lines       [4]string `json:"lines,omitempty"`
	Record      *Stack    `json:"record,omitempty"`
	Observed    string    `json:"observed,omitempty"`
}

var mechanicKinds = map[int]string{193: "hopper", blkCauldron: "cauldron", blkSign: "sign", blkJukebox: "jukebox", blkDispenser: "dispenser", blkDropper: "dropper", blkObserver: "observer", blkRedstoneLamp: "lamp", blkDaylightDetector: "daylight_detector", blkComposter: "composter"}
var mechanicItems = map[int]int{385: 193, itemRail: blkRail, itemPoweredRail: blkPoweredRail, itemDetectorRail: blkDetectorRail, itemDispenser: blkDispenser, itemDropper: blkDropper, itemObserver: blkObserver, itemRedstoneLamp: blkRedstoneLamp, itemDaylightDetector: blkDaylightDetector, itemComposter: blkComposter, itemCauldron: blkCauldron, itemOakSign: blkSign, itemJukebox: blkJukebox}

func cloneMechanic(v *MechanicState) *MechanicState {
	if v == nil {
		return nil
	}
	out := *v
	out.Record = copyStack(v.Record)
	return &out
}
func mechanicSnapshot(r *Room, k uint64) *MechanicState {
	if r.Ext == nil || r.Ext.Blocks[k] == nil {
		return nil
	}
	return cloneMechanic(r.Ext.Blocks[k].Mechanic)
}
func (s *Server) validMechanic(v *MechanicState) bool {
	if v == nil {
		return true
	}
	known := false
	for _, k := range mechanicKinds {
		known = known || k == v.Kind
	}
	if !known || v.Facing < 0 || v.Facing > 5 || v.SupportFace < 0 || v.SupportFace > 5 || v.Power < 0 || v.Power > 15 || v.Pulse < 0 || v.Pulse > 20 || v.Level < 0 || v.Level > 7 || len(v.Observed) > 8192 {
		return false
	}
	if v.Kind == "hopper" && v.Facing == 1 {
		return false
	}
	if v.Kind == "cauldron" && v.Level > 3 {
		return false
	}
	if v.Record != nil && (v.Kind != "jukebox" || !s.validStack(v.Record, 1) || !s.registeredDisc(v.Record.ID)) {
		return false
	}
	return validSignLines(v.Lines[:])
}
func validSignLines(lines []string) bool {
	if len(lines) != 4 {
		return false
	}
	for _, line := range lines {
		if !utf8.ValidString(line) || utf8.RuneCountInString(line) > 80 {
			return false
		}
		for _, c := range line {
			if c < ' ' || c == 127 {
				return false
			}
		}
	}
	return true
}
func (s *Server) registeredDisc(id int) bool {
	it, ok := s.rules.byID[id]
	return ok && isMusicDisc(id) && it.ID == id
}
func (s *Server) defaultMechanic(r *Room, k uint64, b int) *MechanicState {
	kind := mechanicKinds[b]
	if kind == "" {
		return nil
	}
	v := &MechanicState{Kind: kind, Facing: 3, SupportFace: 1}
	if d := r.blockExt().Blocks[k]; d != nil && d.Kind == kind && d.Facing >= 0 && d.Facing <= 5 {
		v.Facing = d.Facing
		v.Power = d.Power
	}
	if kind == "hopper" {
		v.Facing = 0
	}
	if kind == "observer" {
		v.Observed = s.observerObservation(r, k, v.Facing)
	}
	return v
}
func storeMechanic(r *Room, k uint64, v *MechanicState) {
	d := r.blockExt().Blocks[k]
	if d == nil {
		d = &BlockData{}
		r.Ext.Blocks[k] = d
	}
	d.Mechanic = cloneMechanic(v)
	if v != nil {
		d.Kind = v.Kind
		d.Facing = v.Facing
		d.Power = v.Power
	}
	r.Dirty = true
	syncMechanicView(r, k, v)
}
func syncMechanicView(r *Room, k uint64, v *MechanicState) {
	vs := getVehicles(r)
	if v == nil {
		delete(vs.Composters, k)
		delete(vs.Signs, k)
		delete(vs.Jukeboxes, k)
		delete(vs.ObserverPulses, k)
	} else {
		vs.Composters[k] = v.Level
		vs.Signs[k] = v.Lines
		vs.ObserverPulses[k] = v.Pulse
		if v.Record != nil {
			vs.Jukeboxes[k] = v.Record.ID
		} else {
			delete(vs.Jukeboxes, k)
		}
	}
}
func (s *Server) trackMechanicBlock(r *Room, k uint64, b int) {
	if kind := containerKinds[s.nameOf(b)]; kind != "" && kind != "ender" && r.Containers[k] == nil {
		r.Containers[k] = &Container{Kind: kind, Slots: make(Stacks, windowSize(kind))}
		r.Dirty = true
	}

	kind := mechanicKinds[b]
	cur := mechanicSnapshot(r, k)
	if kind != "" {
		if cur == nil || cur.Kind != kind {
			storeMechanic(r, k, s.defaultMechanic(r, k, b))
		} else {
			syncMechanicView(r, k, cur)
		}
	}
	if kind == "" && cur != nil {
		storeMechanic(r, k, nil)
	}
	if isRailBlock(b) {
		v := railState(r, k)
		if v == nil || v.Kind != railKind(b) {
			storeRail(r, k, &RailState{Kind: railKind(b), Shape: "north_south"})
		}
	} else if railState(r, k) != nil {
		storeRail(r, k, nil)
	}
	if kind == "" && !isRailBlock(b) && r.Ext != nil {
		d := r.Ext.Blocks[k]
		if d != nil && d.Mechanic == nil && d.Rail == nil && d.Redstone == nil && d.TNT == nil && d.Fire == nil && (mechanicKindsFromName(d.Kind) || d.Kind == "rail" || d.Kind == "powered_rail" || d.Kind == "detector_rail") {
			delete(r.Ext.Blocks, k)
		}
	}
}
func mechanicKindsFromName(k string) bool {
	for _, v := range mechanicKinds {
		if v == k {
			return true
		}
	}
	return false
}
func (s *Server) restoreMechanics(r *Room) {
	for k, b := range r.Edits {
		if mechanicKinds[b] != "" || isRailBlock(b) || s.containerBlock(b) {
			s.trackMechanicBlock(r, k, b)
		}
	}
}
func sortedMechanicKeys(r *Room) []uint64 {
	var keys []uint64
	if r.Ext != nil {
		for k, d := range r.Ext.Blocks {
			if d != nil && (d.Mechanic != nil || d.Rail != nil) {
				keys = append(keys, k)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
func (s *Server) mechanicData(r *Room, p *Peer, k uint64, next *MechanicState, reason string) map[string]any {
	x, y, z := coords(k)
	actor := s.commandActor(nil)
	var held *Stack
	slot := -1
	if p != nil {
		actor = p.sid
		held = s.heldItem(p)
		slot = p.state.Inv.Selected
	}
	kind := ""
	if next != nil {
		kind = next.Kind
	} else if v := mechanicSnapshot(r, k); v != nil {
		kind = v.Kind
	}
	return map[string]any{"reason": reason, "actor": actor, "source": s.commandActor(nil), "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "block": s.block(r, x, y, z), "mechanic": kind, "mechanicBefore": mechanicSnapshot(r, k), "mechanicAfter": cloneMechanic(next), "stack": itemSnapshot(held), "before": itemSnapshot(held), "after": itemSnapshot(held), "slot": slot}
}
func (s *Server) prepareMechanic(r *Room, p *Peer, k uint64, next *MechanicState, reason string) (map[string]any, bool) {
	if !s.validMechanic(next) {
		return nil, false
	}
	data := s.mechanicData(r, p, k, next, reason)
	if !s.allowed(api.MechanicChanging, r, p, data) {
		return nil, false
	}
	return data, true
}
func (s *Server) changeMechanic(r *Room, p *Peer, k uint64, next *MechanicState, reason string) bool {
	before := mechanicSnapshot(r, k)
	if reflect.DeepEqual(before, next) {
		return false
	}
	data, ok := s.prepareMechanic(r, p, k, next, reason)
	if !ok {
		return false
	}
	storeMechanic(r, k, next)
	if before == nil || next == nil || before.Power != next.Power {
		r.rt().redstoneDirty = true
	}
	s.broadcastMechanic(r, k)
	s.emit(api.MechanicChanged, r, p, data)
	return true
}

// A directional observer senses committed block state at its front. Neighbour
// scheduling alone is not a change and must not manufacture another pulse.
func (s *Server) observerObservation(r *Room, k uint64, facing int) string {
	x, y, z := coords(k)
	d := facing6[facing]
	x += d[0]
	y += d[1]
	z += d[2]
	b := s.block(r, x, y, z)
	var state any
	if d := r.blockExt().Blocks[blockKey(x, y, z)]; d != nil {
		c := *d
		c.Kind = ""
		c.Owner = ""
		c.Fuse = 0
		c.TNT = nil
		c.Fire = nil
		// Timers and contents are not visible block state. Counting down a
		// repeater/TNT or editing a sign must not manufacture neighbour pulses.
		if c.Redstone != nil {
			v := cloneRedstone(c.Redstone)
			v.Countdown = 0
			v.Timeout = 0
			c.Redstone = v
		}
		if c.Mechanic != nil {
			v := cloneMechanic(c.Mechanic)
			v.Observed = ""
			v.Pulse = 0
			v.Lines = [4]string{}
			if v.Record != nil {
				v.Record = stack(v.Record.ID, 1, 0)
			}
			c.Mechanic = v
		}
		state = []any{&c, d.TNT != nil}
	}
	raw, _ := json.Marshal([]any{b, state})
	return string(raw)
}
func (s *Server) mechanicSupported(r *Room, k uint64, b int) bool {
	x, y, z := coords(k)
	if isRailBlock(b) || b == blkDaylightDetector {
		return s.supportsTorch(s.block(r, x, y-1, z))
	}
	if b == blkSign {
		v := mechanicSnapshot(r, k)
		face := 1
		if v != nil {
			face = v.SupportFace
		}
		if face < 1 || face > 5 {
			return false
		}
		d := faceOffsets[face]
		supportBlock := s.block(r, x-d[0], y-d[1], z-d[2])
		return s.supportsTorch(supportBlock) || baseName(s.rules.Names[supportBlock]) == "Chest"
	}
	return true
}

// Merge independent metadata into existing block events, preserving redstone
// component fields and old API v1 fields on mixed transactions.
func (s *Server) mechanicBlockChangeData(r *Room, p *Peer, k uint64, b int, reason string) map[string]any {
	x, y, z := coords(k)
	old := s.block(r, x, y, z)
	data := map[string]any{}
	if mechanicKinds[b] != "" || mechanicKinds[old] != "" {
		next := s.defaultMechanic(r, k, b)
		if mechanicKinds[b] != "" && mechanicKinds[b] == mechanicKinds[old] {
			next = mechanicSnapshot(r, k)
		}
		data = s.mechanicData(r, p, k, next, reason)
	}
	if isRailBlock(b) || isRailBlock(old) {
		var next *RailState
		if isRailBlock(b) {
			next = &RailState{Kind: railKind(b), Shape: "north_south"}
			if old == b {
				next = railState(r, k)
			}
		}
		for field, value := range s.railData(r, p, k, next, reason) {
			data[field] = value
		}
	}
	if len(data) == 0 {
		return nil
	}
	data["oldBlock"] = old
	data["block"] = b
	return data
}
func (s *Server) mechanicBlockBefore(r *Room, p *Peer, data map[string]any) bool {
	if _, ok := data["mechanicAfter"]; ok && !s.allowed(api.MechanicChanging, r, p, data) {
		return false
	}
	if _, ok := data["railAfter"]; ok && !s.allowed(api.RailChanging, r, p, data) {
		return false
	}
	return true
}
func (s *Server) mechanicBlockCompleted(r *Room, p *Peer, data map[string]any) {
	if _, ok := data["mechanicAfter"]; ok {
		s.emit(api.MechanicChanged, r, p, data)
	}
	if _, ok := data["railAfter"]; ok {
		s.emit(api.RailChanged, r, p, data)
	}
}
func (s *Server) mechanicPacket(r *Room, k uint64) map[string]any {
	x, y, z := coords(k)
	v := mechanicSnapshot(r, k)
	out := map[string]any{"x": x, "y": y, "z": z, "id": s.block(r, x, y, z)}
	if v != nil {
		out["kind"] = v.Kind
		out["facing"] = v.Facing
		out["supportFace"] = v.SupportFace
		out["power"] = v.Power
		out["powered"] = v.Powered
		out["level"] = v.Level
		out["inverted"] = v.Inverted
		out["lines"] = v.Lines
		out["record"] = itemSnapshot(v.Record)
	}
	return out
}
func (s *Server) broadcastMechanic(r *Room, k uint64) {
	for p := range r.Peers {
		p.json(map[string]any{"t": "mech", "epoch": p.epoch, "worldId": r.ID, "dim": r.dimName(), "blocks": []any{s.mechanicPacket(r, k)}})
	}
}
func (s *Server) syncMechanicsFor(p *Peer) {
	mechs, rails := []any{}, []any{}
	for _, k := range sortedMechanicKeys(p.room) {
		x, _, z := coords(k)
		if !p.knows(x, z) && !(p.room.Mode == "creative" && !p.room.Infinite) {
			continue
		}
		if mechanicSnapshot(p.room, k) != nil {
			mechs = append(mechs, s.mechanicPacket(p.room, k))
		}
		if railState(p.room, k) != nil {
			rails = append(rails, s.railPacket(p.room, k))
		}
	}
	for _, group := range []struct {
		typ    string
		blocks []any
	}{{"mech", mechs}, {"rail", rails}} {
		blocks := group.blocks
		reset := true
		for {
			n := min(256, len(blocks))
			p.json(map[string]any{"t": group.typ, "reset": reset, "complete": len(blocks) == n, "epoch": p.epoch, "worldId": p.room.ID, "dim": p.room.dimName(), "blocks": blocks[:n]})
			reset = false
			blocks = blocks[n:]
			if len(blocks) == 0 {
				break
			}
		}
	}
}
