package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

type RailState struct {
	Kind    string `json:"kind"`
	Shape   string `json:"shape"`
	Powered bool   `json:"powered,omitempty"`
	Power   int    `json:"power,omitempty"`
}

func railKind(b int) string {
	switch b {
	case blkRail:
		return "rail"
	case blkPoweredRail:
		return "powered_rail"
	case blkDetectorRail:
		return "detector_rail"
	}
	return ""
}
func cloneRail(v *RailState) *RailState {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
func railState(r *Room, k uint64) *RailState {
	if r.Ext == nil || r.Ext.Blocks[k] == nil {
		return nil
	}
	return cloneRail(r.Ext.Blocks[k].Rail)
}
func validRail(v *RailState) bool {
	if v == nil {
		return true
	}
	if v.Kind != "rail" && v.Kind != "powered_rail" && v.Kind != "detector_rail" || v.Power < 0 || v.Power > 15 {
		return false
	}
	switch v.Shape {
	case "north_south", "east_west", "ascending_north", "ascending_south", "ascending_east", "ascending_west":
		return true
	case "north_east", "north_west", "south_east", "south_west":
		return v.Kind == "rail"
	}
	return false
}
func storeRail(r *Room, k uint64, v *RailState) {
	d := r.blockExt().Blocks[k]
	if d == nil {
		d = &BlockData{}
		r.Ext.Blocks[k] = d
	}
	d.Rail = cloneRail(v)
	if v != nil {
		d.Kind = v.Kind
		d.Power = v.Power
	}
	r.Dirty = true
}
func (s *Server) railData(r *Room, p *Peer, k uint64, next *RailState, reason string) map[string]any {
	x, y, z := coords(k)
	actor := s.commandActor(nil)
	var held *Stack
	slot := -1
	if p != nil {
		actor = p.sid
		held = s.heldItem(p)
		slot = p.state.Inv.Selected
	}
	return map[string]any{"reason": reason, "actor": actor, "source": s.commandActor(nil), "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "block": s.block(r, x, y, z), "railBefore": railState(r, k), "railAfter": cloneRail(next), "stack": itemSnapshot(held), "before": itemSnapshot(held), "after": itemSnapshot(held), "slot": slot}
}
func (s *Server) prepareRail(r *Room, p *Peer, k uint64, next *RailState, reason string) (func(), bool) {
	return s.prepareRailWithContext(r, p, k, next, reason, nil)
}
func (s *Server) prepareRailWithContext(r *Room, p *Peer, k uint64, next *RailState, reason string, context map[string]any) (func(), bool) {
	if !validRail(next) {
		return nil, false
	}
	data := s.railData(r, p, k, next, reason)
	for field, value := range context {
		switch field {
		case "target", "x", "y", "z", "railBefore", "railAfter", "block", "reason":
			continue
		}
		data[field] = value
	}
	if !s.allowed(api.RailChanging, r, p, data) {
		return nil, false
	}
	return func() {
		before := railState(r, k)
		storeRail(r, k, next)
		if before == nil || next == nil || before.Power != next.Power {
			r.rt().redstoneDirty = true
		}
		s.broadcastRail(r, k)
		s.emit(api.RailChanged, r, p, data)
	}, true
}
func (s *Server) changeRail(r *Room, p *Peer, k uint64, next *RailState, reason string) bool {
	if reflect.DeepEqual(railState(r, k), next) {
		return false
	}
	commit, ok := s.prepareRail(r, p, k, next, reason)
	if !ok {
		return false
	}
	commit()
	return true
}
func (s *Server) prepareRailOccupied(r *Room, k uint64, occupied bool) (func(), bool) {
	return s.prepareRailOccupiedWithContext(r, k, occupied, nil)
}
func (s *Server) prepareRailOccupiedWithContext(r *Room, k uint64, occupied bool, context map[string]any) (func(), bool) {
	v := railState(r, k)
	if v == nil || v.Kind != "detector_rail" {
		return func() {}, true
	}
	if v.Powered == occupied {
		return func() {}, true
	}
	v.Powered = occupied
	v.Power = 0
	if occupied {
		v.Power = 15
	}
	return s.prepareRailWithContext(r, nil, k, v, "rail_occupancy", context)
}
func (s *Server) setRailOccupied(r *Room, k uint64, occupied bool) bool {
	commit, ok := s.prepareRailOccupied(r, k, occupied)
	if ok {
		commit()
	}
	return ok
}
func railEndpoints(shape string) [2][3]int {
	switch shape {
	case "east_west":
		return [2][3]int{{-1, 0, 0}, {1, 0, 0}}
	case "ascending_east":
		return [2][3]int{{-1, 0, 0}, {1, 1, 0}}
	case "ascending_west":
		return [2][3]int{{-1, 1, 0}, {1, 0, 0}}
	case "ascending_north":
		return [2][3]int{{0, 1, -1}, {0, 0, 1}}
	case "ascending_south":
		return [2][3]int{{0, 0, -1}, {0, 1, 1}}
	case "north_east":
		return [2][3]int{{0, 0, -1}, {1, 0, 0}}
	case "north_west":
		return [2][3]int{{0, 0, -1}, {-1, 0, 0}}
	case "south_east":
		return [2][3]int{{0, 0, 1}, {1, 0, 0}}
	case "south_west":
		return [2][3]int{{0, 0, 1}, {-1, 0, 0}}
	}
	return [2][3]int{{0, 0, -1}, {0, 0, 1}}
}
func (s *Server) railNeighbor(r *Room, x, y, z int, d [3]int) (uint64, int, bool) {
	for _, dy := range []int{0, 1, -1} {
		ny := y + dy
		nx, nz := x+d[0], z+d[2]
		if ny < 1 || ny >= 128 || !r.inside(nx, nz) {
			continue
		}
		if !isRailBlock(s.block(r, nx, ny, nz)) {
			continue
		}
		if dy == 1 && s.mobSolid(r, x, y+1, z) || dy == -1 && s.mobSolid(r, nx, y, nz) {
			continue
		}
		return blockKey(nx, ny, nz), dy, true
	}
	return 0, 0, false
}
func (s *Server) desiredRailShape(r *Room, k uint64, v *RailState) string {
	x, y, z := coords(k)
	var yes [4]bool
	var height [4]int
	for i, d := range horizontal4 {
		_, height[i], yes[i] = s.railNeighbor(r, x, y, z, d)
	}
	ns, ew := yes[0] || yes[2], yes[1] || yes[3]
	shape := v.Shape
	if !ns && !ew {
		return shape
	}
	if ns && !ew {
		shape = "north_south"
	} else if ew && !ns {
		shape = "east_west"
	} else if v.Kind == "rail" {
		if yes[0] && yes[1] && !yes[2] && !yes[3] {
			shape = "north_east"
		}
		if yes[0] && yes[3] && !yes[2] && !yes[1] {
			shape = "north_west"
		}
		if yes[2] && yes[1] && !yes[0] && !yes[3] {
			shape = "south_east"
		}
		if yes[2] && yes[3] && !yes[0] && !yes[1] {
			shape = "south_west"
		}
	} else if shape != "east_west" && shape != "ascending_east" && shape != "ascending_west" {
		shape = "north_south"
	}
	if shape == "north_south" || shape == "ascending_north" || shape == "ascending_south" {
		shape = "north_south"
		if yes[0] && height[0] == 1 {
			shape = "ascending_north"
		} else if yes[2] && height[2] == 1 {
			shape = "ascending_south"
		}
	}
	if shape == "east_west" || shape == "ascending_east" || shape == "ascending_west" {
		shape = "east_west"
		if yes[1] && height[1] == 1 {
			shape = "ascending_east"
		} else if yes[3] && height[3] == 1 {
			shape = "ascending_west"
		}
	}
	return shape
}
func railConnects(a, b *RailState, dx, dy, dz int) bool {
	if a == nil || b == nil {
		return false
	}
	from, to := false, false
	for _, d := range railEndpoints(a.Shape) {
		if d[0] == dx && d[2] == dz && (d[1] == dy || dy == -1 && d[1] == 0) {
			from = true
		}
	}
	for _, d := range railEndpoints(b.Shape) {
		if d[0] == -dx && d[2] == -dz && (d[1] == -dy || dy == 1 && d[1] == 0) {
			to = true
		}
	}
	return from && to
}
func (s *Server) tickRails(r *Room) {
	keys := sortedMechanicKeys(r)
	for _, k := range keys {
		v := railState(r, k)
		if v == nil {
			continue
		}
		x, y, z := coords(k)
		b := s.block(r, x, y, z)
		if !isRailBlock(b) {
			continue
		}
		if !s.mechanicSupported(r, k, b) {
			s.breakUnsupportedMechanic(r, k, b)
			continue
		}
		next := cloneRail(v)
		next.Shape = s.desiredRailShape(r, k, v)
		s.changeRail(r, nil, k, next, "rail_connection")
	}
	// Powered rails carry a signal along at most eight connected rails. Detector
	// rails emit external redstone; powered rails never become global sources.
	levels := map[uint64]int{}
	queue := []uint64{}
	for _, k := range keys {
		v := railState(r, k)
		if v == nil || v.Kind != "powered_rail" {
			continue
		}
		x, y, z := coords(k)
		levels[k] = 0
		if s.blockPowered(r, x, y, z) {
			levels[k] = 15
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if levels[k] <= 8 {
			continue
		}
		v := railState(r, k)
		x, y, z := coords(k)
		for _, dir := range horizontal4 {
			nk, dy, ok := s.railNeighbor(r, x, y, z, dir)
			if !ok {
				continue
			}
			n := railState(r, nk)
			if n == nil || n.Kind != "powered_rail" || !railConnects(v, n, dir[0], dy, dir[2]) {
				continue
			}
			if levels[nk] < levels[k]-1 {
				levels[nk] = levels[k] - 1
				queue = append(queue, nk)
			}
		}
	}
	for _, k := range keys {
		v := railState(r, k)
		if v != nil && v.Kind == "powered_rail" {
			v.Power = levels[k]
			v.Powered = v.Power > 0
			s.changeRail(r, nil, k, v, "rail_power")
		}
	}
}
func (s *Server) railPacket(r *Room, k uint64) map[string]any {
	x, y, z := coords(k)
	v := railState(r, k)
	out := map[string]any{"x": x, "y": y, "z": z, "id": s.block(r, x, y, z)}
	if v != nil {
		out["kind"] = v.Kind
		out["shape"] = v.Shape
		out["power"] = v.Power
		out["powered"] = v.Powered
	}
	return out
}
func (s *Server) broadcastRail(r *Room, k uint64) {
	for p := range r.Peers {
		p.json(map[string]any{"t": "rail", "epoch": p.epoch, "worldId": r.ID, "dim": r.dimName(), "blocks": []any{s.railPacket(r, k)}})
	}
}
