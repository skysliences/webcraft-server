package game

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	api "mcraft/backend/pkg/plugin"
)

const (
	vehicleNamespace uint32 = 0x20000000
	fishingNamespace uint32 = 0x30000000
	transportIDMask  uint32 = 0x0fffffff
	maxWorldVehicles        = 128
)

// Vehicles have their own persisted identity and item. They never enter the
// natural-mob registry, AI, spawn limits, sunlight damage or mob loot table.
type Vehicle struct {
	ID        uint32  `json:"id"`
	Type      string  `json:"type"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	VX        float64 `json:"vx"`
	VY        float64 `json:"vy"`
	VZ        float64 `json:"vz"`
	Yaw       float64 `json:"yaw"`
	Health    float64 `json:"health"`
	Item      *Stack  `json:"item"`
	RiderID   uint16  `json:"-"`
	Rider     string  `json:"rider,omitempty"`
	forward   float64
	turn      float64
	controlAt uint64
}

func cloneVehicle(v *Vehicle) *Vehicle {
	if v == nil {
		return nil
	}
	copy := *v
	copy.Item = copyStack(v.Item)
	return &copy
}
func vehicleTarget(v *Vehicle) *api.Combatant {
	return &api.Combatant{Kind: "entity", EntityID: v.ID, EntityType: v.Type, X: v.X, Y: v.Y, Z: v.Z, Health: v.Health}
}
func transportActor(p *Peer) string {
	if p == nil {
		return "server"
	}
	return p.sid
}
func vehicleData(v *Vehicle, p *Peer, reason string) map[string]any {
	return map[string]any{"reason": reason, "actor": transportActor(p), "target": vehicleTarget(v), "entityId": v.ID, "type": v.Type, "x": v.X, "y": v.Y, "z": v.Z, "stack": itemSnapshot(v.Item), "vehicle": cloneVehicle(v)}
}
func vehiclePlayerMove(p *Peer, v *Vehicle, point [3]float64, reason string) map[string]any {
	return map[string]any{"reason": reason, "cause": "vehicle", "actor": p.sid, "target": vehicleTarget(v), "stack": itemSnapshot(v.Item), "entityId": v.ID, "fromX": p.state.X, "fromY": p.state.Y, "fromZ": p.state.Z, "x": point[0], "y": point[1], "z": point[2]}
}
func vehicleStore(r *Room) map[uint32]*Vehicle {
	e := r.blockExt()
	if e.Vehicles == nil {
		e.Vehicles = map[uint32]*Vehicle{}
	}
	return e.Vehicles
}
func (s *Server) vehicleID(r *Room) (uint32, uint32) {
	n := r.blockExt().NextVehicleID
	for attempts := 0; attempts < maxWorldVehicles+1; attempts++ {
		n = (n + 1) & transportIDMask
		if n == 0 {
			n = 1
		}
		id := vehicleNamespace | n
		if vehicleStore(r)[id] == nil && r.Drops[id] == nil && r.Mobs[id] == nil {
			return id, n
		}
	}
	return 0, 0
}
func (s *Server) validVehicle(r *Room, v *Vehicle) bool {
	if v == nil || v.ID&^transportIDMask != vehicleNamespace || v.ID&transportIDMask == 0 || (v.Type != "boat" && v.Type != "minecart") || !s.validStack(v.Item, 1) || v.Item.Count != 1 || !number(v.X, r.minCoord(), r.maxCoord()) || !number(v.Z, r.minCoord(), r.maxCoord()) || !number(v.Y, -16, 127.75) || !number(v.Yaw, -100000, 100000) || !number(v.VX, -1, 1) || !number(v.VY, -2, 2) || !number(v.VZ, -1, 1) || !number(v.Health, .01, 40) {
		return false
	}
	return v.Type == "minecart" && v.Item.ID == itemMinecart || v.Type == "boat" && v.Item.ID == itemBoat
}
func syncVehicleView(r *Room) {
	view := getVehicles(r)
	view.Minecarts, view.Boats = map[uint32]*Vehicle{}, map[uint32]*Vehicle{}
	for id, v := range vehicleStore(r) {
		if v.Type == "minecart" {
			view.Minecarts[id] = v
		} else if v.Type == "boat" {
			view.Boats[id] = v
		}
	}
}
func (s *Server) restoreVehicles(r *Room) error {
	if r.Ext != nil && (len(r.Ext.Vehicles) > maxWorldVehicles || r.Ext.NextVehicleID > transportIDMask) {
		return fmt.Errorf("invalid vehicle state in %s; save was not modified", r.ID)
	}
	for id, v := range vehicleStore(r) {
		if !s.validVehicle(r, v) || v.ID != id || r.Drops[id] != nil || r.Mobs[id] != nil {
			return fmt.Errorf("invalid or duplicate vehicle in %s; save was not modified", r.ID)
		}
		r.blockExt().NextVehicleID = max(r.Ext.NextVehicleID, id&transportIDMask)
		if v.Rider != "" || v.RiderID != 0 {
			r.Dirty = true
		}
		v.Rider, v.RiderID, v.forward, v.turn = "", 0, 0, 0
	}
	for _, p := range r.Players {
		if p != nil && p.Mount != 0 {
			p.Mount = 0
			r.Dirty = true
		}
	}
	syncVehicleView(r)
	return nil
}

// The earlier experimental module saved its runtime vehicles as mobs, without
// an item snapshot. Preserve its position/health using an ordinary item and
// leave the original JSON intact until saveRoom has written a migration backup.
func (s *Server) migrateLegacyTransport(r *Room, raw *diskRoom) error {
	natural := make([]*Mob, 0, len(raw.Mobs))
	seen := map[uint32]bool{}
	for _, m := range raw.Mobs {
		if m == nil || (m.Type != "minecart" && m.Type != "boat" && m.Type != "fishing_bobber") {
			natural = append(natural, m)
			continue
		}
		if m.ID == 0 || seen[m.ID] {
			return fmt.Errorf("invalid legacy transport identity in %s; save was not modified", r.ID)
		}
		seen[m.ID] = true
		r.transportMigrated = true
		if m.Type == "fishing_bobber" {
			continue // its disconnected owner cannot resume a transient cast
		}
		if raw.Ext == nil {
			raw.Ext = &RoomExt{}
		}
		if raw.Ext.Vehicles == nil {
			raw.Ext.Vehicles = map[uint32]*Vehicle{}
		}
		if len(raw.Ext.Vehicles) >= maxWorldVehicles || raw.Ext.NextVehicleID > transportIDMask {
			return fmt.Errorf("too many legacy vehicles in %s", r.ID)
		}
		n := raw.Ext.NextVehicleID
		var id uint32
		for i := 0; i < maxWorldVehicles+1; i++ {
			n = (n + 1) & transportIDMask
			if n == 0 {
				n = 1
			}
			id = vehicleNamespace | n
			if raw.Ext.Vehicles[id] == nil && r.Drops[id] == nil {
				break
			}
			id = 0
		}
		if id == 0 {
			return fmt.Errorf("cannot allocate legacy vehicle in %s", r.ID)
		}
		item := itemMinecart
		if m.Type == "boat" {
			item = itemBoat
		}
		v := &Vehicle{ID: id, Type: m.Type, X: m.X, Y: m.Y, Z: m.Z, Yaw: m.Yaw, Health: m.Health, Item: stack(item, 1, 0)}
		bounds := *r
		bounds.Infinite, bounds.W = raw.Infinite, raw.W
		if !s.validVehicle(&bounds, v) {
			return fmt.Errorf("invalid legacy vehicle in %s; save was not modified", r.ID)
		}
		raw.Ext.Vehicles[id], raw.Ext.NextVehicleID = v, n
	}
	raw.Mobs = natural
	return nil
}
func (s *Server) vehicleFits(r *Room, typ string, x, y, z float64) bool {
	w, h := .42, .65
	if typ == "boat" {
		w, h = .65, .55
	}
	if !number(x, r.minCoord()+w, r.maxCoord()-w) || !number(z, r.minCoord()+w, r.maxCoord()-w) || !number(y, .01, 127-h) {
		return false
	}
	var track *RailState
	var rx, ry, rz int
	if typ == "minecart" {
		_, rx, ry, rz, track = s.vehicleRail(r, &Vehicle{X: x, Y: y, Z: z})
	}
	for _, dx := range []float64{-w + .01, w - .01} {
		for _, dz := range []float64{-w + .01, w - .01} {
			bottom := y + .05
			if typ == "boat" {
				bottom = y + .25 // the hull floats below the water surface
			}
			for by := int(math.Floor(bottom)); by <= int(math.Floor(y+h-.01)); by++ {
				bx, bz := int(math.Floor(x+dx)), int(math.Floor(z+dz))
				if s.mobSolid(r, bx, by, bz) {
					// The bounding hull extends over the raised foundation before
					// its centre reaches an ascending rail's upper endpoint.
					// Only a connected raised rail can make that foundation safe.
					if track != nil && by == ry && isRailBlock(s.block(r, bx, by+1, bz)) && railConnects(track, railState(r, blockKey(bx, by+1, bz)), bx-rx, 1, bz-rz) {
						continue
					}
					return false
				}
			}
		}
	}
	return true
}

// SDK and player packets share snapshots, placement validation and protection.
func (s *Server) useVehicle(p *Peer, m Message, expected *api.ItemStack, compare bool) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || !p.room.usesSurvival() || !s.canBuild(p) {
		return false
	}
	held := s.heldItem(p)
	if held == nil || compare && !reflect.DeepEqual(itemSnapshot(held), expected) {
		return false
	}
	if held.ID == itemFishingRod {
		return s.useFishingRod(p, expected, compare)
	}
	if (held.ID != itemMinecart && held.ID != itemBoat) || !s.validStack(held, s.rules.max(held.ID)) || !s.reachable(p, m) {
		return false
	}
	r := p.room
	if len(vehicleStore(r)) >= maxWorldVehicles {
		return false
	}
	v := &Vehicle{Type: "minecart", Health: 20, Item: sized(held, 1), Yaw: p.state.Yaw}
	x, y, z := m.X, m.Y, m.Z
	if held.ID == itemMinecart {
		if !isRailBlock(s.block(r, x, y, z)) {
			return false
		}
		v.X, v.Z = float64(x)+.5, float64(z)+.5
		v.Y = float64(y) + .1
		if rail := railState(r, blockKey(x, y, z)); rail != nil {
			_, v.Y, _, _, _, _ = vehicleRailPoint(x, y, z, rail.Shape, .5)
		}
	} else {
		v.Type, v.Health = "boat", 40
		d := faceOffsets[m.F]
		x, y, z = x+d[0], y+d[1], z+d[2]
		v.X, v.Y, v.Z = float64(x)+.5, float64(y)+.1, float64(z)+.5
		if isWater(s.block(r, x, y-1, z)) {
			v.Y = float64(y-1) + .8
		} else if !isWater(s.block(r, x, y, z)) && !s.mobSolid(r, x, y-1, z) {
			return false
		}
	}
	if !s.vehicleFits(r, v.Type, v.X, v.Y, v.Z) {
		return false
	}
	for _, other := range vehicleStore(r) {
		if math.Hypot(other.X-v.X, other.Z-v.Z) < .8 && math.Abs(other.Y-v.Y) < 1 {
			return false
		}
	}
	id, next := s.vehicleID(r)
	if id == 0 {
		return false
	}
	v.ID = id
	slot, before := p.state.Inv.Selected, itemSnapshot(held)
	after, consumed := copyStack(held), 0
	if !s.isCreative(p) {
		after, consumed = sized(held, held.Count-1), 1
	}
	data := vehicleData(v, p, "vehicle_place")
	data["slot"], data["before"], data["after"], data["stack"], data["count"] = slot, before.Clone(), itemSnapshot(after), before.Clone(), consumed
	data["clickX"], data["clickY"], data["clickZ"], data["face"] = m.X, m.Y, m.Z, m.F
	if !s.allowed(api.VehiclePlacing, r, p, data) || consumed != 0 && !s.allowed(api.InventoryChanging, r, p, data) {
		return false
	}
	detectors, ok := s.prepareVehicleDetectors(r, nil, v, map[string]any{"actor": p.sid})
	if !ok || p.state.Inv.Selected != slot || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), before) || !s.vehicleFits(r, v.Type, v.X, v.Y, v.Z) {
		return false
	}
	p.state.Inv.Slots[slot] = after
	vehicleStore(r)[id], r.Ext.NextVehicleID, r.Dirty = v, next, true
	syncVehicleView(r)
	for _, commit := range detectors {
		commit()
	}
	if consumed != 0 {
		s.emit(api.InventoryChanged, r, p, data)
	}
	s.emit(api.VehiclePlaced, r, p, data)
	s.inventory(p, 0, 0)
	s.syncTransport(r)
	return true
}

func vehicleReachable(s *Server, p *Peer, v *Vehicle) bool {
	return p != nil && p.state != nil && !p.state.Dead && math.Hypot(v.X-p.state.X, v.Z-p.state.Z) <= 4.5 && math.Abs(v.Y-p.state.Y) < 3 && s.lineClear(p.room, [3]float64{p.state.X, p.state.Y + 1.3, p.state.Z}, [3]float64{v.X, v.Y + .4, v.Z})
}
func (s *Server) interactVehicle(p *Peer, id uint32) bool {
	if p == nil || p.room == nil || p.state == nil || !p.room.usesSurvival() || !s.canBuild(p) {
		return false
	}
	v := vehicleStore(p.room)[id]
	if v == nil || !vehicleReachable(s, p, v) {
		return false
	}
	if p.state.Mount == id {
		return s.exitVehicle(p, "vehicle_interact")
	}
	if v.RiderID != 0 || v.Rider != "" || p.state.Mount != 0 || !s.vehicleRiderFits(p.room, v) {
		return false
	}
	data := vehicleData(v, p, "vehicle_enter")
	data["before"], data["after"] = cloneVehicle(v), cloneVehicle(v)
	data["after"].(*Vehicle).Rider, data["after"].(*Vehicle).RiderID = p.sid, p.id
	move := vehiclePlayerMove(p, v, [3]float64{v.X, v.Y + .35, v.Z}, "vehicle_enter")
	if !s.allowed(api.VehicleEntering, p.room, p, data) || !s.allowed(api.PlayerMoving, p.room, p, move) || v.RiderID != 0 || p.state.Mount != 0 {
		return false
	}
	v.RiderID, v.Rider, p.state.Mount = p.id, p.sid, id
	p.state.X, p.state.Y, p.state.Z = v.X, v.Y+.35, v.Z
	p.peakY, p.state.Fly = p.state.Y, 0
	p.room.Dirty = true
	s.emit(api.VehicleEntered, p.room, p, data)
	s.emit(api.PlayerMoved, p.room, p, move)
	s.syncTransport(p.room)
	return true
}
func (s *Server) vehicleRiderFits(r *Room, v *Vehicle) bool {
	for _, dx := range []float64{-.28, .28} {
		for _, dz := range []float64{-.28, .28} {
			for y := int(math.Floor(v.Y + .4)); y <= int(math.Floor(v.Y+2.1)); y++ {
				if s.mobSolid(r, int(math.Floor(v.X+dx)), y, int(math.Floor(v.Z+dz))) {
					return false
				}
			}
		}
	}
	return true
}
func (s *Server) vehicleExitPoint(r *Room, v *Vehicle) ([3]float64, bool) {
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {0, 0}} {
		x, z := v.X+float64(d[0])*1.25, v.Z+float64(d[1])*1.25
		for _, dy := range []int{0, 1, -1, 2, -2} {
			y := math.Floor(v.Y) + float64(dy)
			if !number(x, r.minCoord()+.35, r.maxCoord()-.35) || !number(z, r.minCoord()+.35, r.maxCoord()-.35) || !number(y, 1, 125) {
				continue
			}
			if !s.mobSolid(r, int(math.Floor(x)), int(y)-1, int(math.Floor(z))) && !isWater(s.block(r, int(math.Floor(x)), int(y)-1, int(math.Floor(z)))) {
				continue
			}
			clear := true
			for _, dx := range []float64{-.28, .28} {
				for _, dz := range []float64{-.28, .28} {
					for sy := int(y); sy <= int(math.Floor(y+1.79)); sy++ {
						if s.mobSolid(r, int(math.Floor(x+dx)), sy, int(math.Floor(z+dz))) {
							clear = false
						}
					}
				}
			}
			if clear {
				return [3]float64{x, y, z}, true
			}
		}
	}
	return [3]float64{}, false
}
func (s *Server) prepareVehicleExit(r *Room, p *Peer, v *Vehicle, reason string) (map[string]any, [3]float64, bool) {
	point, ok := s.vehicleExitPoint(r, v)
	if !ok {
		return nil, point, false
	}
	data := vehicleData(v, p, reason)
	before, after := cloneVehicle(v), cloneVehicle(v)
	after.Rider, after.RiderID, after.forward, after.turn = "", 0, 0, 0
	data["before"], data["after"], data["position"] = before, after, point
	if !s.allowed(api.VehicleExiting, r, p, data) {
		return data, point, false
	}
	if p != nil {
		data["playerMove"] = vehiclePlayerMove(p, v, point, reason)
		if !s.allowed(api.PlayerMoving, r, p, data["playerMove"].(map[string]any)) {
			return data, point, false
		}
	}
	return data, point, true
}
func commitVehicleExit(r *Room, p *Peer, v *Vehicle, point [3]float64) {
	if p != nil && p.state != nil {
		p.state.Mount = 0
		p.state.X, p.state.Y, p.state.Z = point[0], point[1], point[2]
		p.peakY = point[1]
	} else if p := r.Players[v.Rider]; p != nil {
		p.Mount = 0
		p.X, p.Y, p.Z = point[0], point[1], point[2]
	}
	v.Rider, v.RiderID, v.forward, v.turn = "", 0, 0, 0
	r.Dirty = true
}
func (s *Server) exitVehicle(p *Peer, reason string) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Mount == 0 {
		return false
	}
	v := vehicleStore(p.room)[p.state.Mount]
	if v == nil || v.RiderID != p.id {
		return false
	}
	data, point, ok := s.prepareVehicleExit(p.room, p, v, reason)
	if !ok {
		return false
	}
	commitVehicleExit(p.room, p, v, point)
	s.emit(api.VehicleExited, p.room, p, data)
	if move, ok := data["playerMove"].(map[string]any); ok {
		s.emit(api.PlayerMoved, p.room, p, move)
	}
	s.syncTransport(p.room)
	return true
}
func (s *Server) controlVehicle(p *Peer, m Message) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Dead || !number(m.Forward, -1, 1) || !number(m.Turn, -1, 1) {
		return false
	}
	if m.Exit {
		return s.exitVehicle(p, "vehicle_dismount")
	}
	v := vehicleStore(p.room)[p.state.Mount]
	if v == nil || v.RiderID != p.id {
		return false
	}
	v.forward, v.turn, v.controlAt = m.Forward, m.Turn, p.room.rt().tickCount
	return true
}

func vehicleRailEnds(shape string) [2][3]int {
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
	case "south_east":
		return [2][3]int{{0, 0, 1}, {1, 0, 0}}
	case "south_west":
		return [2][3]int{{0, 0, 1}, {-1, 0, 0}}
	case "north_east":
		return [2][3]int{{0, 0, -1}, {1, 0, 0}}
	case "north_west":
		return [2][3]int{{0, 0, -1}, {-1, 0, 0}}
	}
	return [2][3]int{{0, 0, -1}, {0, 0, 1}}
}
func angleDifference(a, b float64) float64 { return math.Atan2(math.Sin(a-b), math.Cos(a-b)) }
func vehicleRailPoint(x, y, z int, shape string, t float64) (px, py, pz, tx, ty, tz float64) {
	e := vehicleRailEnds(shape)
	ax, az, bx, bz := float64(e[0][0])*.5, float64(e[0][2])*.5, float64(e[1][0])*.5, float64(e[1][2])*.5
	if e[0][0]*e[1][0]+e[0][2]*e[1][2] == 0 {
		cx, cz := ax+bx, az+bz
		start := math.Atan2(az-cz, ax-cx)
		delta := angleDifference(math.Atan2(bz-cz, bx-cx), start)
		a := start + delta*t
		px, pz = cx+.5*math.Cos(a), cz+.5*math.Sin(a)
		tx, tz = -.5*math.Sin(a)*delta, .5*math.Cos(a)*delta
	} else {
		px, pz, tx, tz = ax+(bx-ax)*t, az+(bz-az)*t, bx-ax, bz-az
	}
	py, ty = float64(y)+.1+float64(e[0][1])+(float64(e[1][1]-e[0][1]))*t, float64(e[1][1]-e[0][1])
	return float64(x) + .5 + px, py, float64(z) + .5 + pz, tx, ty, tz
}
func vehicleRailProgress(v *Vehicle, x, z int, shape string) float64 {
	e := vehicleRailEnds(shape)
	ax, az, bx, bz := float64(e[0][0])*.5, float64(e[0][2])*.5, float64(e[1][0])*.5, float64(e[1][2])*.5
	px, pz := v.X-float64(x)-.5, v.Z-float64(z)-.5
	if e[0][0]*e[1][0]+e[0][2]*e[1][2] == 0 {
		cx, cz := ax+bx, az+bz
		start := math.Atan2(az-cz, ax-cx)
		delta := angleDifference(math.Atan2(bz-cz, bx-cx), start)
		return clampF(angleDifference(math.Atan2(pz-cz, px-cx), start)/delta, 0, 1)
	}
	return clampF(((px-ax)*(bx-ax)+(pz-az)*(bz-az))/((bx-ax)*(bx-ax)+(bz-az)*(bz-az)), 0, 1)
}
func (s *Server) vehicleRail(r *Room, v *Vehicle) (uint64, int, int, int, *RailState) {
	x, y, z := int(math.Floor(v.X)), int(math.Floor(v.Y)), int(math.Floor(v.Z))
	for _, dy := range []int{0, -1, 1} {
		if y+dy >= 1 && y+dy < 128 && isRailBlock(s.block(r, x, y+dy, z)) {
			key := blockKey(x, y+dy, z)
			if rail := railState(r, key); rail != nil {
				return key, x, y + dy, z, rail
			}
		}
	}
	return 0, x, y, z, nil
}
func (s *Server) prepareVehicleDetectors(r *Room, old, next *Vehicle, contexts ...map[string]any) ([]func(), bool) {
	trigger := next
	if trigger == nil {
		trigger = old
	}
	context := map[string]any{}
	if trigger != nil {
		context["actor"] = transportActor(findPeerByID(r, trigger.RiderID))
		context["stack"], context["before"], context["after"], context["vehicleItem"] = itemSnapshot(trigger.Item), itemSnapshot(trigger.Item), itemSnapshot(trigger.Item), itemSnapshot(trigger.Item)
		context["vehicleBefore"], context["vehicleAfter"], context["trigger"] = cloneVehicle(old), cloneVehicle(next), vehicleTarget(trigger)
	}
	for _, extra := range contexts {
		for key, value := range extra {
			context[key] = value
		}
	}
	keys := map[uint64]bool{}
	for _, v := range []*Vehicle{old, next} {
		if v != nil && v.Type == "minecart" {
			key, x, y, z, rail := s.vehicleRail(r, v)
			if rail != nil && s.block(r, x, y, z) == blkDetectorRail {
				keys[key] = true
			}
		}
	}
	ordered := make([]uint64, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	commits := []func(){}
	for _, k := range ordered {
		occupied := false
		for _, v := range vehicleStore(r) {
			if old != nil && v.ID == old.ID || v.Type != "minecart" {
				continue
			}
			vk, _, _, _, state := s.vehicleRail(r, v)
			if state != nil && vk == k {
				occupied = true
			}
		}
		if next != nil && next.Type == "minecart" {
			vk, _, _, _, state := s.vehicleRail(r, next)
			occupied = occupied || state != nil && vk == k
		}
		commit, ok := s.prepareRailOccupiedWithContext(r, k, occupied, context)
		if !ok {
			return nil, false
		}
		if commit != nil {
			commits = append(commits, commit)
		}
	}
	return commits, true
}
func (s *Server) stepMinecart(r *Room, v *Vehicle) {
	_, x, y, z, rail := s.vehicleRail(r, v)
	if rail == nil {
		v.VX, v.VZ, v.VY = v.VX*.7, v.VZ*.7, max(-1, v.VY-.08)
		s.stepFreeVehicle(r, v)
		return
	}
	t := vehicleRailProgress(v, x, z, rail.Shape)
	_, _, _, tx, ty, tz := vehicleRailPoint(x, y, z, rail.Shape, t)
	direction, speed := 1.0, math.Hypot(v.VX, v.VZ)
	if v.VX*tx+v.VZ*tz < 0 {
		direction = -1
	}
	if speed < .005 && v.forward != 0 {
		if (-math.Sin(v.Yaw)*tx-math.Cos(v.Yaw)*tz)*v.forward < 0 {
			direction = -1
		}
	}
	if rail.Powered && s.block(r, x, y, z) == blkPoweredRail {
		speed += .06
	} else if s.block(r, x, y, z) == blkPoweredRail {
		speed *= .5
	} else {
		speed *= .98
	}
	if v.RiderID != 0 {
		speed += math.Abs(v.forward) * .025
	}
	speed = clampF(speed-direction*ty*.018, 0, .45)
	if speed < .002 {
		speed = 0
	}
	for sub := 0; sub < 4 && speed > 0; sub++ {
		_, x, y, z, rail = s.vehicleRail(r, v)
		if rail == nil {
			break
		}
		t = vehicleRailProgress(v, x, z, rail.Shape)
		_, _, _, tx, ty, tz = vehicleRailPoint(x, y, z, rail.Shape, t)
		if v.VX*tx+v.VZ*tz < 0 {
			direction = -1
		} else if math.Hypot(v.VX, v.VZ) > .001 {
			direction = 1
		}
		length := math.Sqrt(tx*tx + ty*ty + tz*tz)
		nextT := t + direction*speed*.25/length
		v.X, v.Y, v.Z, tx, ty, tz = vehicleRailPoint(x, y, z, rail.Shape, clampF(nextT, 0, 1))
		horizontal := math.Hypot(tx, tz)
		v.VX, v.VY, v.VZ = tx/horizontal*speed*direction, ty/length*speed*direction, tz/horizontal*speed*direction
		v.Yaw = math.Atan2(-v.VX, -v.VZ)
		if nextT < 0 || nextT > 1 {
			// Crossing an endpoint retains the tangent and finds the adjacent
			// flat/curved/ascending rail on the next substep.
			v.X += v.VX * .08
			v.Z += v.VZ * .08
			v.Y += v.VY * .08
		}
	}
}
func (s *Server) stepFreeVehicle(r *Room, v *Vehicle) {
	for i := 0; i < 4; i++ {
		if s.vehicleFits(r, v.Type, v.X+v.VX*.25, v.Y, v.Z+v.VZ*.25) {
			v.X += v.VX * .25
			v.Z += v.VZ * .25
		} else {
			v.VX, v.VZ = 0, 0
		}
		if s.vehicleFits(r, v.Type, v.X, v.Y+v.VY*.25, v.Z) {
			v.Y += v.VY * .25
		} else {
			v.VY = 0
		}
	}
}
func (s *Server) stepBoat(r *Room, v *Vehicle) {
	x, y, z := int(math.Floor(v.X)), int(math.Floor(v.Y)), int(math.Floor(v.Z))
	water := isWater(s.block(r, x, y, z)) || isWater(s.block(r, x, y-1, z))
	v.Yaw += v.turn * .09
	if water {
		if !isWater(s.block(r, x, y, z)) {
			y--
		}
		v.Y, v.VY = float64(y)+.8, 0
		v.VX, v.VZ = v.VX*.9-math.Sin(v.Yaw)*v.forward*.04, v.VZ*.9-math.Cos(v.Yaw)*v.forward*.04
	} else {
		v.VX, v.VZ, v.VY = v.VX*.6-math.Sin(v.Yaw)*v.forward*.008, v.VZ*.6-math.Cos(v.Yaw)*v.forward*.008, max(-1, v.VY-.08)
	}
	speed := math.Hypot(v.VX, v.VZ)
	if speed > .4 {
		v.VX *= .4 / speed
		v.VZ *= .4 / speed
	}
	s.stepFreeVehicle(r, v)
}
func (s *Server) tickVehicles(r *Room) {
	ids := make([]uint32, 0, len(vehicleStore(r)))
	for id := range vehicleStore(r) {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		v := vehicleStore(r)[id]
		rider := findPeerByID(r, v.RiderID)
		if v.RiderID != 0 && (rider == nil || rider.state.Dead || rider.state.Mount != id) {
			if data, point, ok := s.prepareVehicleExit(r, rider, v, "vehicle_rider_unavailable"); ok {
				commitVehicleExit(r, rider, v, point)
				s.emit(api.VehicleExited, r, rider, data)
				if move, ok := data["playerMove"].(map[string]any); ok {
					s.emit(api.PlayerMoved, r, rider, move)
				}
			}
		}
		before, next := cloneVehicle(v), cloneVehicle(v)
		if r.rt().tickCount > next.controlAt+5 {
			next.forward, next.turn = 0, 0
		}
		if next.Type == "minecart" {
			s.stepMinecart(r, next)
		} else {
			s.stepBoat(r, next)
		}
		if !s.validVehicle(r, next) || !s.vehicleFits(r, next.Type, next.X, next.Y, next.Z) || next.RiderID != 0 && !s.vehicleRiderFits(r, next) {
			continue
		}
		changed := before.X != next.X || before.Y != next.Y || before.Z != next.Z || before.Yaw != next.Yaw || before.VX != next.VX || before.VY != next.VY || before.VZ != next.VZ
		if !changed {
			if commits, ok := s.prepareVehicleDetectors(r, before, next); ok {
				for _, commit := range commits {
					commit()
				}
			}
			continue
		}
		data := vehicleData(before, rider, "vehicle_physics")
		data["before"], data["after"] = before, cloneVehicle(next)
		if !s.allowed(api.VehicleMoving, r, rider, data) {
			continue
		}
		var playerMove map[string]any
		if rider != nil && rider.state.Mount == id {
			playerMove = vehiclePlayerMove(rider, before, [3]float64{next.X, next.Y + .35, next.Z}, "vehicle_physics")
			if !s.allowed(api.PlayerMoving, r, rider, playerMove) {
				continue
			}
		}
		detectors, ok := s.prepareVehicleDetectors(r, before, next)
		if !ok || !reflect.DeepEqual(before, cloneVehicle(v)) {
			continue
		}
		*v = *next
		if rider != nil && rider.state.Mount == id {
			rider.state.X, rider.state.Y, rider.state.Z = v.X, v.Y+.35, v.Z
			rider.peakY, rider.state.Fly = rider.state.Y, 0
		}
		r.Dirty = true
		for _, commit := range detectors {
			commit()
		}
		s.emit(api.VehicleMoved, r, rider, data)
		if playerMove != nil {
			s.emit(api.PlayerMoved, r, rider, playerMove)
		}
	}
	s.syncTransport(r)
}

// Wear is prepared without changing the slot; callers commit it together
// with reeling/damage, so protection cannot consume only part of an action.
func (s *Server) prepareTransportWear(p *Peer, before *Stack, amount int, reason string, context map[string]any) (*Stack, map[string]any, bool) {
	after := copyStack(before)
	if before == nil || amount <= 0 || s.isCreative(p) || before.Meta != nil && before.Meta.Unbreakable {
		return after, nil, true
	}
	maxWear := s.rules.byID[before.ID].Durability
	if maxWear <= 0 {
		return after, nil, true
	}
	amount = s.unbreakingWear(p, before, amount)
	if amount <= 0 {
		return after, nil, true
	}
	data := map[string]any{"reason": reason, "actor": p.sid, "slot": p.state.Inv.Selected, "stack": itemSnapshot(before), "before": itemSnapshot(before), "amount": amount, "damageBefore": before.Damage}
	for k, v := range context {
		if k != "stack" && k != "before" && k != "after" {
			data[k] = v
		}
	}
	e := s.emit(api.ItemWearing, p.room, p, data)
	wear := e.Number("amount")
	if e.Cancelled || !number(wear, 0, 1000) || wear != math.Trunc(wear) {
		return nil, nil, false
	}
	after.Damage += int(wear)
	data["amount"], data["damageAfter"], data["broken"] = int(wear), after.Damage, after.Damage >= maxWear
	if after.Damage >= maxWear {
		after = nil
	}
	data["after"] = itemSnapshot(after)
	return after, data, true
}
func (s *Server) damageVehicleSource(r *Room, p *Peer, v *Vehicle, amount float64, reason string, ctx *api.DamageContext) bool {
	return s.damageVehicleTransaction(r, p, v, amount, reason, ctx, nil, nil)
}
func (s *Server) damageVehicleTransaction(r *Room, p *Peer, v *Vehicle, amount float64, reason string, ctx *api.DamageContext, wearAfter *Stack, wearData map[string]any) (committed bool) {
	if v == nil || vehicleStore(r)[v.ID] != v || !number(amount, .01, 1000) {
		return false
	}
	before := cloneVehicle(v)
	if ctx == nil {
		ctx = &api.DamageContext{Cause: damageCause(reason)}
	}
	ctx.Target, ctx.HealthBefore, ctx.HealthAfter = vehicleTarget(v), v.Health, v.Health
	if ctx.Actor == "" {
		ctx.Actor = transportActor(p)
	}
	if ctx.Source == "" {
		ctx.Source = s.pluginSource
	}
	if ctx.RawAmount == 0 {
		ctx.RawAmount = amount
	}
	ctx.Amount = amount
	data := vehicleData(v, p, reason)
	data["actor"], data["source"], data["weapon"], data["attacker"] = ctx.Actor, ctx.Source, ctx.Weapon.Clone(), ctx.Attacker
	for k, value := range damageData(ctx, reason) {
		data[k] = value
	}
	e := s.emit(api.EntityDamaging, r, p, data)
	amount = e.Number("amount")
	if e.Cancelled || !number(amount, .01, 1000) {
		return false
	}
	ctx.Amount = amount
	for k, value := range damageData(ctx, reason) {
		data[k] = value
	}
	lethal := amount >= v.Health
	afterSnapshot := cloneVehicle(v)
	afterSnapshot.Health = max(0, v.Health-amount)
	data["before"], data["after"] = before, afterSnapshot
	if lethal && (!s.allowed(api.VehicleDestroying, r, p, data) || !s.allowed(api.EntityDying, r, p, data)) {
		return false
	}
	previousID := r.NextDropID
	defer func() {
		if !committed {
			r.NextDropID = previousID
		}
	}()
	var drops []*ItemDrop
	var exitData map[string]any
	var point [3]float64
	var rider *Peer
	var inventoryData map[string]any
	detectors := []func(){}
	if lethal {
		if v.RiderID != 0 || v.Rider != "" {
			rider = findPeerByID(r, v.RiderID)
			var ok bool
			exitData, point, ok = s.prepareVehicleExit(r, rider, v, "vehicle_destroyed")
			if !ok {
				return false
			}
		}
		var err error
		if s.gameRule(r, "doTileDrops") && (p == nil || !s.isCreative(p)) {
			context := map[string]any{"actor": ctx.Actor, "target": vehicleTarget(v), "vehicle": before, "damage": data["damage"], "weapon": ctx.Weapon.Clone()}
			drops, err = s.prepareDropsWithContext(r, p, Stacks{copyStack(v.Item)}, v.X, v.Y+.2, v.Z, "vehicle_destroyed", context, true)
			if err != nil {
				return false
			}
		}
		var ok bool
		detectors, ok = s.prepareVehicleDetectors(r, before, nil, map[string]any{"actor": ctx.Actor, "weapon": ctx.Weapon.Clone(), "attacker": ctx.Attacker})
		if !ok {
			return false
		}
	}
	if wearData != nil && !reflect.DeepEqual(wearData["before"], wearData["after"]) {
		inventoryData = map[string]any{"reason": "vehicle_attack", "actor": ctx.Actor, "target": vehicleTarget(v), "weapon": ctx.Weapon.Clone(), "damage": data["damage"], "slot": wearData["slot"], "stack": wearData["before"], "before": wearData["before"], "after": wearData["after"]}
		if !s.allowed(api.InventoryChanging, r, p, inventoryData) {
			return false
		}
	}
	if !reflect.DeepEqual(before, cloneVehicle(v)) || wearData != nil && (p == nil || p.state.Inv.Selected != wearData["slot"] || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), wearData["before"])) {
		return false
	}
	v.Health = max(0, v.Health-amount)
	ctx.HealthAfter, ctx.AppliedAmount = v.Health, ctx.HealthBefore-v.Health
	for k, value := range damageData(ctx, reason) {
		data[k] = value
	}
	data["health"], data["before"], data["after"] = v.Health, before, cloneVehicle(v)
	actualDrops := Stacks{}
	for _, d := range drops {
		actualDrops = append(actualDrops, copyStack(d.Stack))
	}
	data["drops"] = leafDropSnapshots(actualDrops)
	if wearData != nil {
		p.state.Inv.Slots[p.state.Inv.Selected] = wearAfter
	}
	if lethal {
		if exitData != nil {
			commitVehicleExit(r, rider, v, point)
		}
		delete(vehicleStore(r), v.ID)
	}
	r.Dirty, committed = true, true
	syncVehicleView(r)
	for _, commit := range detectors {
		commit()
	}
	if lethal {
		context := map[string]any{"actor": ctx.Actor, "target": vehicleTarget(before), "vehicle": before, "damage": data["damage"], "weapon": ctx.Weapon.Clone()}
		s.publishDropsWithContext(r, p, drops, "vehicle_destroyed", context)
	}
	if wearData != nil {
		s.emit(api.ItemWorn, r, p, wearData)
		if inventoryData != nil {
			s.emit(api.InventoryChanged, r, p, inventoryData)
		}
		s.inventory(p, 0, 0)
	}
	if exitData != nil {
		s.emit(api.VehicleExited, r, rider, exitData)
		if move, ok := exitData["playerMove"].(map[string]any); ok {
			s.emit(api.PlayerMoved, r, rider, move)
		}
	}
	s.emit(api.EntityDamaged, r, p, data)
	if lethal {
		s.emit(api.EntityDead, r, p, data)
		s.emit(api.VehicleDestroyed, r, p, data)
	}
	s.syncTransport(r)
	return true
}
func (s *Server) attackVehicle(p *Peer, id uint32) bool {
	if p == nil || p.room == nil || p.state == nil || !s.canBuild(p) || time.Since(p.attackAt) < s.attackCooldown(p) {
		return false
	}
	v := vehicleStore(p.room)[id]
	if v == nil || !vehicleReachable(s, p, v) {
		return false
	}
	amount := s.attributes(p)[api.AttackDamage]
	if s.isCreative(p) {
		amount = 100
	}
	ctx := s.playerAttack(p, vehicleTarget(v), amount)
	if ctx == nil {
		return false
	}
	before := copyStack(s.heldItem(p))
	after, wearData, ok := s.prepareTransportWear(p, before, 1, "vehicle_attack", map[string]any{"actor": p.sid, "target": vehicleTarget(v), "weapon": itemSnapshot(before), "vehicle": cloneVehicle(v), "damage": damageData(ctx, "player_attack")["damage"]})
	if !ok || !s.damageVehicleTransaction(p.room, p, v, ctx.Amount, "player_attack", ctx, after, wearData) {
		return false
	}
	p.attackAt = time.Now()
	s.emit(api.PlayerAttacked, p.room, p, damageData(ctx, "player_attack"))
	return true
}
func transportVisible(p *Peer, x, z float64) bool {
	if p == nil || p.closed || p.room == nil || p.state == nil {
		return false
	}
	cx, cz := floorDiv(int(math.Floor(x)), 16), floorDiv(int(math.Floor(z)), 16)
	return p.knows(int(math.Floor(x)), int(math.Floor(z))) && math.Abs(float64(cx)-math.Floor(p.state.X/16)) <= float64(p.rd+2) && math.Abs(float64(cz)-math.Floor(p.state.Z/16)) <= float64(p.rd+2)
}
func (s *Server) syncTransportFor(p *Peer) {
	if p == nil || p.room == nil || p.state == nil || !p.room.usesSurvival() {
		return
	}
	vehicles := []map[string]any{}
	ids := []uint32{}
	for id := range vehicleStore(p.room) {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		v := vehicleStore(p.room)[id]
		if !transportVisible(p, v.X, v.Z) && p.state.Mount != id {
			continue
		}
		vehicles = append(vehicles, map[string]any{"id": v.ID, "type": v.Type, "x": v.X - float64(p.originX), "y": v.Y, "z": v.Z - float64(p.originZ), "yaw": v.Yaw, "health": v.Health, "rider": v.RiderID, "item": itemSnapshot(v.Item)})
	}
	bobbers := []map[string]any{}
	if rt := p.room.rt().fishing; rt != nil {
		for _, b := range rt.bobbers {
			if transportVisible(p, b.X, b.Z) || b.OwnerID == p.id {
				bobbers = append(bobbers, map[string]any{"id": b.ID, "owner": b.OwnerID, "x": b.X - float64(p.originX), "y": b.Y, "z": b.Z - float64(p.originZ), "bite": b.BiteTicks, "inWater": b.InWater})
			}
		}
	}
	p.json(map[string]any{"t": "vehicles", "epoch": p.epoch, "dim": p.room.dimName(), "mount": p.state.Mount, "player": map[string]any{"x": p.state.X - float64(p.originX), "y": p.state.Y, "z": p.state.Z - float64(p.originZ)}, "vehicles": vehicles, "bobbers": bobbers})
}
func (s *Server) syncTransport(r *Room) {
	for p := range r.Peers {
		s.syncTransportFor(p)
	}
}
func (s *Server) prepareTransportDeparture(p *Peer, reason string) (func(), bool) {
	if p == nil || p.room == nil || p.state == nil {
		return nil, true
	}
	r := p.room
	var v *Vehicle
	var exitData map[string]any
	var point [3]float64
	if p.state.Mount != 0 {
		v = vehicleStore(r)[p.state.Mount]
		if v != nil && v.RiderID == p.id {
			var ok bool
			exitData, point, ok = s.prepareVehicleExit(r, p, v, reason)
			if !ok {
				return nil, false
			}
		}
	}
	b := fishingState(r).bobbers[p.id]
	var reelData map[string]any
	if b != nil {
		reelData = fishingData(p, b, reason)
		reelData["caught"], reelData["drops"], reelData["experience"] = false, []*api.ItemStack{}, 0
		reelData["before"], reelData["after"] = itemSnapshot(b.Rod), itemSnapshot(b.Rod)
		if !s.allowed(api.FishingReeling, r, p, reelData) {
			return nil, false
		}
	}
	return func() {
		if exitData != nil {
			commitVehicleExit(r, p, v, point)
		}
		if reelData != nil {
			delete(fishingState(r).bobbers, p.id)
			delete(getVehicles(r).Bobbers, p.id)
		}
		if exitData != nil {
			s.emit(api.VehicleExited, r, p, exitData)
			if move, ok := exitData["playerMove"].(map[string]any); ok {
				s.emit(api.PlayerMoved, r, p, move)
			}
		}
		if reelData != nil {
			s.emit(api.FishingReeled, r, p, reelData)
		}
		s.syncTransport(r)
	}, true
}
func (s *Server) disconnectTransport(p *Peer) {
	if commit, ok := s.prepareTransportDeparture(p, "transport_disconnect"); ok && commit != nil {
		commit()
	}
}
func init() {
	onPlace(-90, func(s *Server, p *Peer, m Message, target int) bool {
		held := s.heldItem(p)
		if held == nil || (held.ID != itemMinecart && held.ID != itemBoat) {
			return false
		}
		s.useVehicle(p, m, nil, false)
		return true
	})
	onMessage("interact_entity", func(s *Server, p *Peer, m Message) bool {
		if vehicleStore(p.room)[m.E] == nil {
			return false
		}
		s.interactVehicle(p, m.E)
		return true
	})
	onMessage("attack_entity", func(s *Server, p *Peer, m Message) bool {
		if vehicleStore(p.room)[m.E] == nil {
			return false
		}
		s.attackVehicle(p, m.E)
		return true
	})
	onMessage("vctl", func(s *Server, p *Peer, m Message) bool { s.controlVehicle(p, m); return true })
	onRoomTick(func(s *Server, r *Room) {
		if r.usesSurvival() && len(r.Peers) > 0 {
			s.tickVehicles(r)
		}
	})
}
