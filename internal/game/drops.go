package game

import (
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"math"
	api "mcraft/backend/pkg/plugin"
	"sort"
)

const maxWorldDrops = 2048
const dropLifetimeMS = 300000
const dropPickupDelayMS = 150

// ItemDrop is authoritative and persisted with the room. Age advances while
// the world has players; restarting the process never resets its remaining life.
type ItemDrop struct {
	ID            uint32  `json:"id"`
	Stack         *Stack  `json:"stack"`
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
	Z             float64 `json:"z"`
	VX            float64 `json:"vx"`
	VY            float64 `json:"vy"`
	VZ            float64 `json:"vz"`
	AgeMS         int     `json:"ageMs"`
	PickupDelayMS int     `json:"pickupDelayMs"`
	retryAt       int
	grounded      bool
}

func (s *Server) validDrop(d *ItemDrop) bool {
	return d != nil && d.ID > 0 && d.Stack != nil && s.validStack(d.Stack, s.rules.max(d.Stack.ID)) && number(d.X, -worldBorder+.125, worldBorder-.125) && number(d.Y, -64, 129) && number(d.Z, -worldBorder+.125, worldBorder-.125) && number(d.VX, -2, 2) && number(d.VY, -4, 4) && number(d.VZ, -2, 2) && d.AgeMS >= 0 && d.AgeMS < dropLifetimeMS && d.PickupDelayMS >= 0 && d.PickupDelayMS <= dropLifetimeMS
}
func dropData(d *ItemDrop, reason string) map[string]any {
	return map[string]any{"entityId": d.ID, "item": d.Stack.ID, "count": d.Stack.Count, "damage": d.Stack.Damage, "stack": itemSnapshot(d.Stack), "x": d.X, "y": d.Y, "z": d.Z, "reason": reason}
}
func (s *Server) prepareDrops(r *Room, p *Peer, stacks Stacks, x, y, z float64, reason string) ([]*ItemDrop, error) {
	return s.prepareDropsWithContext(r, p, stacks, x, y, z, reason, nil, false)
}

func (s *Server) prepareDropsWithContext(r *Room, p *Peer, stacks Stacks, x, y, z float64, reason string, context map[string]any, strict bool) ([]*ItemDrop, error) {
	if !number(x, r.minCoord()+.125, r.maxCoord()-.125) || !number(y, 0, 127.75) || !number(z, r.minCoord()+.125, r.maxCoord()-.125) {
		return nil, fmt.Errorf("掉落位置超出世界范围。")
	}
	count := 0
	for _, stack := range stacks {
		if !s.validStack(stack, 2304) {
			return nil, fmt.Errorf("无效的掉落物品。")
		}
		count += (stack.Count + s.rules.max(stack.ID) - 1) / s.rules.max(stack.ID)
	}
	if len(r.Drops)+count > maxWorldDrops {
		return nil, fmt.Errorf("世界掉落物过多，请先拾取附近的物品。")
	}
	if r.Drops == nil {
		r.Drops = map[uint32]*ItemDrop{}
	}
	var prepared []*ItemDrop
	for _, stack := range stacks {
		for remaining := stack.Count; remaining > 0; {
			if r.NextDropID == 0 || r.NextDropID >= 0x20000000 {
				r.NextDropID = 1
			}
			for r.Drops[r.NextDropID] != nil || r.Mobs[r.NextDropID] != nil {
				r.NextDropID++
				if r.NextDropID == 0 || r.NextDropID >= 0x20000000 {
					r.NextDropID = 1
				}
			}
			n := min(remaining, s.rules.max(stack.ID))
			d := &ItemDrop{ID: r.NextDropID, Stack: sized(stack, n), X: x, Y: y, Z: z, VX: (s.rng.Float64() - .5) * .16, VY: .16, VZ: (s.rng.Float64() - .5) * .16, PickupDelayMS: dropPickupDelayMS}
			r.NextDropID++
			remaining -= n
			data := dropData(d, reason)
			for field, value := range context {
				data[field] = value
			}
			e := s.emit(api.ItemSpawning, r, p, data)
			if e.Cancelled {
				if strict {
					return nil, fmt.Errorf("插件阻止了掉落，方块未改变。")
				}
				continue
			}
			quantity := e.Number("count")
			if !number(quantity, 1, float64(s.rules.max(stack.ID))) || quantity != math.Trunc(quantity) {
				return nil, fmt.Errorf("插件返回了无效的掉落数量。")
			}
			d.Stack.Count = int(quantity)
			prepared = append(prepared, d)
		}
	}
	return prepared, nil
}
func (s *Server) publishDrops(r *Room, p *Peer, drops []*ItemDrop, reason string) {
	s.publishDropsWithContext(r, p, drops, reason, nil)
}
func (s *Server) publishDropsWithContext(r *Room, p *Peer, drops []*ItemDrop, reason string, context map[string]any) {
	for _, d := range drops {
		r.Drops[d.ID] = d
		r.Dirty = true
	}
	for _, d := range drops {
		data := dropData(d, reason)
		for field, value := range context {
			data[field] = value
		}
		s.emit(api.ItemSpawned, r, p, data)
	}
	s.syncDrops(r)
}

// Reuse the client's entity protocol: spawn=7, move=8, gone=9, event=10.
func entityPosition(out []byte, d *ItemDrop) []byte {
	x, z := uint32(math.Round(d.X*256)), uint32(math.Round(d.Z*256))
	out = append(out, byte(x), byte(x>>8), byte(x>>16))
	out = binary.LittleEndian.AppendUint16(out, uint16(math.Round((d.Y+64)*128)))
	return append(out, byte(z), byte(z>>8), byte(z>>16), 0, 0, 0)
}
func dropSpawn(out []byte, d *ItemDrop) []byte {
	out = binary.LittleEndian.AppendUint32(out, d.ID)
	out = append(out, 1)
	out = entityPosition(out, d)
	out = append(out, 0)
	out = binary.LittleEndian.AppendUint16(out, uint16(d.Stack.ID))
	return append(out, byte(d.Stack.Count))
}
func entityEvent(id uint32, event byte, arg uint16) []byte {
	out := binary.LittleEndian.AppendUint32([]byte{10, 1, 0}, id)
	out = append(out, event)
	return binary.LittleEndian.AppendUint16(out, arg)
}
func sortedDropIDs(r *Room) []uint32 {
	ids := make([]uint32, 0, len(r.Drops))
	for id := range r.Drops {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
func dropVisible(p *Peer, d *ItemDrop) bool {
	x, z := floorDiv(int(math.Floor(d.X)), 16), floorDiv(int(math.Floor(d.Z)), 16)
	return p.room.usesSurvival() && p.knows(int(math.Floor(d.X)), int(math.Floor(d.Z))) && math.Abs(float64(x)-math.Floor(p.state.X/16)) <= float64(p.rd+2) && math.Abs(float64(z)-math.Floor(p.state.Z/16)) <= float64(p.rd+2)
}
func (s *Server) syncDrops(r *Room) {
	for p := range r.Peers {
		s.syncDropsFor(p)
	}
}
func (s *Server) syncDropsFor(p *Peer) {
	if p.closed || !p.room.usesSurvival() {
		return
	}
	if p.knownDrops == nil {
		p.knownDrops = map[uint32]bool{}
	}
	spawn, gone := []byte{7, 0, 0}, []byte{9, 0, 0}
	ns, ng := 0, 0
	var metadata []map[string]any
	for _, id := range sortedDropIDs(p.room) {
		d := p.room.Drops[id]
		if dropVisible(p, d) {
			if !p.knownDrops[id] {
				copy := *d
				copy.X -= float64(p.originX)
				copy.Z -= float64(p.originZ)
				spawn = dropSpawn(spawn, &copy)
				ns++
				p.knownDrops[id] = true
				if d.Stack.Meta != nil {
					metadata = append(metadata, map[string]any{"id": id, "stack": copyStack(d.Stack)})
				}
			}
		}
	}
	for id := range p.knownDrops {
		d := p.room.Drops[id]
		if d == nil || !dropVisible(p, d) {
			gone = binary.LittleEndian.AppendUint32(gone, id)
			ng++
			delete(p.knownDrops, id)
		}
	}
	if ng > 0 {
		binary.LittleEndian.PutUint16(gone[1:], uint16(ng))
		p.enqueue(websocket.BinaryMessage, gone)
	}
	if ns > 0 {
		binary.LittleEndian.PutUint16(spawn[1:], uint16(ns))
		p.enqueue(websocket.BinaryMessage, spawn)
		if len(metadata) > 0 {
			p.json(map[string]any{"t": "item_meta", "items": metadata})
		}
	}
}
func (s *Server) pickupNotice(r *Room, d *ItemDrop, player uint16, picked, remaining int) {
	var out []byte
	if remaining > 0 {
		out = append(out, entityEvent(d.ID, 6, uint16(picked))...)
	}
	out = append(out, entityEvent(d.ID, 5, player)...)
	if remaining > 0 {
		out = append(out, entityEvent(d.ID, 6, uint16(remaining))...)
	}
	for p := range r.Peers {
		if p.knownDrops[d.ID] {
			p.enqueue(websocket.BinaryMessage, out)
		}
	}
}
func (s *Server) removeDrop(r *Room, d *ItemDrop, reason string) {
	delete(r.Drops, d.ID)
	r.Dirty = true
	out := binary.LittleEndian.AppendUint32([]byte{9, 1, 0}, d.ID)
	for p := range r.Peers {
		if p.knownDrops[d.ID] {
			p.enqueue(websocket.BinaryMessage, out)
			delete(p.knownDrops, d.ID)
		}
	}
	s.emit(api.ItemDespawned, r, nil, dropData(d, reason))
}

func (s *Server) solid(r *Room, x, y, z float64) bool {
	if y < 0 || y >= 128 {
		return false
	}
	b := s.block(r, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
	return b >= 0 && b < len(s.rules.Kinds) && (s.rules.Kinds[b] == 1 || s.rules.Kinds[b] == 2 || s.rules.Kinds[b] == 3)
}
func (s *Server) dropBlocked(r *Room, x, y, z float64) bool {
	for _, dx := range []float64{-.124, .124} {
		for _, dz := range []float64{-.124, .124} {
			if s.solid(r, x+dx, y+.001, z+dz) || s.solid(r, x+dx, y+.249, z+dz) {
				return true
			}
		}
	}
	return false
}
func (s *Server) moveDrop(r *Room, d *ItemDrop) {
	// A player may place a new solid block around a resting item. Push it into
	// free space above only if it cannot fall downwards.
	if s.dropBlocked(r, d.X, d.Y, d.Z) {
		if !s.solid(r, d.X, d.Y-0.5, d.Z) {
			d.VY = math.Min(0, d.VY)
		} else {
			for y := math.Floor(d.Y) + 1; y < math.Min(129, math.Floor(d.Y)+7); y++ {
				if !s.dropBlocked(r, d.X, y, d.Z) {
					d.Y = y
					d.VY = 0
					break
				}
			}
		}
	}
	// Small swept steps prevent a fast fall from skipping a solid floor.
	for i := 0; i < 4; i++ {
		const dt = .5
		d.VY = max(-1.5, d.VY-.04*dt)
		dy := d.VY * dt
		if s.dropBlocked(r, d.X, d.Y+dy, d.Z) {
			lo, hi := 0., 1.
			for j := 0; j < 10; j++ {
				mid := (lo + hi) / 2
				if s.dropBlocked(r, d.X, d.Y+dy*mid, d.Z) {
					hi = mid
				} else {
					lo = mid
				}
			}
			d.Y += dy * lo
			d.grounded = dy < 0
			d.VY = 0
		} else {
			d.Y += dy
			d.grounded = false
		}
		x := clampF(d.X+d.VX*dt, r.minCoord()+.125, r.maxCoord()-.125)
		if !s.dropBlocked(r, x, d.Y, d.Z) {
			d.X = x
		} else {
			d.VX = 0
		}
		z := clampF(d.Z+d.VZ*dt, r.minCoord()+.125, r.maxCoord()-.125)
		if !s.dropBlocked(r, d.X, d.Y, z) {
			d.Z = z
		} else {
			d.VZ = 0
		}
		friction := .98
		if d.grounded {
			friction = .588
		}
		d.VX *= math.Pow(friction, dt)
		d.VZ *= math.Pow(friction, dt)
		d.VY *= math.Pow(.98, dt)
	}
	if math.Abs(d.VX) < .001 {
		d.VX = 0
	}
	if math.Abs(d.VZ) < .001 {
		d.VZ = 0
	}
}
func (s *Server) pickupReachable(p *Peer, d *ItemDrop) bool {
	if p.closed || p.state.Dead || p.state.Inv == nil || !p.knownDrops[d.ID] || math.Hypot(p.state.X-d.X, p.state.Z-d.Z) > 2.0 || d.Y < p.state.Y-.75 || d.Y > p.state.Y+2.2 {
		return false
	}
	dx, dy, dz := d.X-p.state.X, d.Y+.25-p.state.Y-1.2, d.Z-p.state.Z
	n := max(1, int(math.Ceil(math.Sqrt(dx*dx+dy*dy+dz*dz)/.2)))
	for i := 1; i < n; i++ {
		t := float64(i) / float64(n)
		if s.solid(p.room, p.state.X+dx*t, p.state.Y+1.2+dy*t, p.state.Z+dz*t) {
			return false
		}
	}
	return true
}
func (s *Server) pickupNearPlayer(r *Room, p *Peer) {
	if r == nil || p == nil || p.state == nil || p.state.Dead || p.state.Inv == nil {
		return
	}
	for _, d := range r.Drops {
		if d != nil && d.PickupDelayMS <= 0 && d.AgeMS >= d.retryAt {
			if math.Abs(p.state.X-d.X) <= 2.2 && math.Abs(p.state.Z-d.Z) <= 2.2 {
				s.pickupDrop(r, d)
			}
		}
	}
}
func (s *Server) pickupDrop(r *Room, d *ItemDrop) {
	if d.PickupDelayMS > 0 || d.AgeMS < d.retryAt {
		return
	}
	var candidates []*Peer
	for p := range r.Peers {
		if s.pickupReachable(p, d) {
			candidates = append(candidates, p)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		da, db := math.Hypot(a.state.X-d.X, a.state.Z-d.Z), math.Hypot(b.state.X-d.X, b.state.Z-d.Z)
		if da == db {
			return a.id < b.id
		}
		return da < db
	})
	for _, p := range candidates {
		clone := &Inventory{Slots: make(Stacks, 46)}
		for i, v := range p.state.Inv.Slots {
			clone.Slots[i] = copyStack(v)
		}
		left := s.add(clone, copyStack(d.Stack))
		remaining := 0
		if left != nil {
			remaining = left.Count
		}
		picked := d.Stack.Count - remaining
		if picked == 0 {
			continue
		}
		data := dropData(d, "pickup")
		data["count"] = picked
		data["remaining"] = remaining
		if !s.allowed(api.ItemPickingUp, r, p, data) {
			continue
		}
		data["reason"] = "pickup"
		if !s.allowed(api.InventoryChanging, r, p, data) {
			continue
		}
		p.state.Inv.Slots = clone.Slots
		r.Dirty = true
		s.inventory(p, 0, 0)
		s.pickupNotice(r, d, p.id, picked, remaining)
		s.emit(api.ItemPickedUp, r, p, data)
		s.emit(api.InventoryChanged, r, p, data)
		if remaining == 0 {
			delete(r.Drops, d.ID)
			out := binary.LittleEndian.AppendUint32([]byte{9, 1, 0}, d.ID)
			for other := range r.Peers {
				if other.knownDrops[d.ID] {
					other.enqueue(websocket.BinaryMessage, out)
					delete(other.knownDrops, d.ID)
				}
			}
			return
		}
		d.Stack.Count = remaining
	}
	if len(candidates) > 0 {
		d.retryAt = d.AgeMS + 500
	}
}
func (s *Server) dropsTick(r *Room) {
	if len(r.Peers) == 0 || len(r.Drops) == 0 {
		return
	}
	ids := sortedDropIDs(r)
	moved := map[uint32]bool{}
	for _, id := range ids {
		d := r.Drops[id]
		d.AgeMS += 100
		d.PickupDelayMS = max(0, d.PickupDelayMS-100)
		r.Dirty = true
		if d.AgeMS >= dropLifetimeMS || d.Y < -20 {
			reason := "expired"
			if d.Y < -20 {
				reason = "void"
			}
			if s.allowed(api.ItemDespawning, r, nil, dropData(d, reason)) {
				s.removeDrop(r, d, reason)
				continue
			}
			d.AgeMS = min(d.AgeMS, dropLifetimeMS-100)
			if d.Y < -20 {
				d.Y = -19.75
				d.VY = 0
			}
		}
		before := [3]float64{d.X, d.Y, d.Z}
		s.moveDrop(r, d)
		if before != [3]float64{d.X, d.Y, d.Z} {
			moved[id] = true
		}
	}
	// 合并相近且相同的掉落物
	for i := 0; i < len(ids); i++ {
		d1 := r.Drops[ids[i]]
		if d1 == nil || d1.Stack == nil {
			continue
		}
		maxStack := s.rules.max(d1.Stack.ID)
		if d1.Stack.Count >= maxStack {
			continue
		}
		for j := i + 1; j < len(ids); j++ {
			d2 := r.Drops[ids[j]]
			if d2 == nil || d2.Stack == nil {
				continue
			}
			if same(d1.Stack, d2.Stack) && math.Hypot(d1.X-d2.X, d1.Z-d2.Z) < 1.0 && math.Abs(d1.Y-d2.Y) < 0.5 {
				n := min(d2.Stack.Count, maxStack-d1.Stack.Count)
				if n > 0 {
					mergeData := map[string]any{"source": d2.ID, "target": d1.ID, "count": n}
					if !s.allowed(api.ItemMerging, r, nil, mergeData) {
						continue
					}
					d1.Stack.Count += n
					d2.Stack.Count -= n
					r.Dirty = true
					s.emit(api.ItemMerged, r, nil, map[string]any{"target": d1.ID, "total": d1.Stack.Count})
					if d2.Stack.Count <= 0 {
						s.removeDrop(r, d2, "merge")
					} else {
						notice := entityEvent(d2.ID, 6, uint16(d2.Stack.Count))
						for p := range r.Peers {
							if p.knownDrops[d2.ID] {
								p.enqueue(websocket.BinaryMessage, notice)
							}
						}
					}
					notice1 := entityEvent(d1.ID, 6, uint16(d1.Stack.Count))
					for p := range r.Peers {
						if p.knownDrops[d1.ID] {
							p.enqueue(websocket.BinaryMessage, notice1)
						}
					}
				}
			}
		}
	}

	s.syncDrops(r)
	for _, id := range ids {
		if d := r.Drops[id]; d != nil {
			s.pickupDrop(r, d)
		}
	}
	for p := range r.Peers {
		out := []byte{8, 0, 0}
		n := 0
		for _, id := range ids {
			if d := r.Drops[id]; d != nil && moved[id] && p.knownDrops[id] {
				out = binary.LittleEndian.AppendUint32(out, id)
				copy := *d
				copy.X -= float64(p.originX)
				copy.Z -= float64(p.originZ)
				out = entityPosition(out, &copy)
				n++
			}
		}
		if n > 0 {
			binary.LittleEndian.PutUint16(out[1:], uint16(n))
			p.enqueue(websocket.BinaryMessage, out)
		}
	}
}
