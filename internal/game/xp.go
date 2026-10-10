package game

import (
	"encoding/binary"
	"math"
	api "mcraft/backend/pkg/plugin"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Runtime (non-persisted) entities shared by XP orbs and projectiles, plus the
// experience system: levels, orbs, and XP sources (mining, kills, smelting).
// ---------------------------------------------------------------------------

// Runtime entity ids live in their own namespace below the mob namespace.
const rtEntNamespace uint32 = 0x40000000

// entView is the network view of a runtime entity.
type entView struct {
	ID         uint32
	Kind       byte // ENT_KIND: 2 xp, 3 arrow, 13 pearl
	X, Y, Z    float64
	Yaw, Pitch float64
	Flags      byte
	Data       uint16
	Count      byte
	seen       map[*Peer]bool
}

func (s *Server) newEntID(r *Room) uint32 {
	rt := r.rt()
	rt.nextEnt++
	if rt.nextEnt >= 0x3fffffff {
		rt.nextEnt = 1
	}
	return rtEntNamespace | rt.nextEnt
}

func (e *entView) pos(out []byte) []byte {
	x, z := uint32(math.Round(clampF(e.X, 0, 65535)*256)), uint32(math.Round(clampF(e.Z, 0, 65535)*256))
	out = append(out, byte(x), byte(x>>8), byte(x>>16))
	out = binary.LittleEndian.AppendUint16(out, uint16(math.Round((clampF(e.Y, -64, 400)+64)*128)))
	out = append(out, byte(z), byte(z>>8), byte(z>>16))
	yaw := byte(int(math.Round(e.Yaw*256/(2*math.Pi))) & 255)
	pitch := byte(int8(clampF(math.Round(e.Pitch*256/(2*math.Pi)), -127, 127)))
	return append(out, yaw, yaw, pitch)
}

func (e *entView) visible(p *Peer) bool {
	if p.closed || p.state == nil || !p.room.usesSurvival() {
		return false
	}
	cx, cz := floorDiv(int(math.Floor(e.X)), 16), floorDiv(int(math.Floor(e.Z)), 16)
	if !p.knows(int(math.Floor(e.X)), int(math.Floor(e.Z))) {
		return false
	}
	return math.Abs(float64(cx)-math.Floor(p.state.X/16)) <= float64(p.rd+2) && math.Abs(float64(cz)-math.Floor(p.state.Z/16)) <= float64(p.rd+2)
}

// syncEnt sends spawn/move/gone frames for one runtime entity.
func (s *Server) syncEnt(r *Room, e *entView, moved bool) {
	if e.seen == nil {
		e.seen = map[*Peer]bool{}
	}
	for p := range r.Peers {
		vis := e.visible(p)
		switch {
		case vis && !e.seen[p]:
			out := binary.LittleEndian.AppendUint32([]byte{7, 1, 0}, e.ID)
			out = append(out, e.Kind)
			view := *e
			view.X -= float64(p.originX)
			view.Z -= float64(p.originZ)
			out = view.pos(out)
			out = append(out, e.Flags)
			out = binary.LittleEndian.AppendUint16(out, e.Data)
			out = append(out, e.Count)
			p.enqueue(websocket.BinaryMessage, out)
			e.seen[p] = true
		case vis && moved:
			out := binary.LittleEndian.AppendUint32([]byte{8, 1, 0}, e.ID)
			view := *e
			view.X -= float64(p.originX)
			view.Z -= float64(p.originZ)
			p.enqueue(websocket.BinaryMessage, view.pos(out))
		case !vis && e.seen[p]:
			p.enqueue(websocket.BinaryMessage, binary.LittleEndian.AppendUint32([]byte{9, 1, 0}, e.ID))
			delete(e.seen, p)
		}
	}
	for p := range e.seen {
		if p.room != r || p.closed {
			delete(e.seen, p)
		}
	}
}

func (s *Server) entEvent(e *entView, ev byte, arg uint16) {
	out := entityEvent(e.ID, ev, arg)
	for p := range e.seen {
		p.enqueue(websocket.BinaryMessage, out)
	}
}

func (s *Server) killEnt(e *entView) {
	out := binary.LittleEndian.AppendUint32([]byte{9, 1, 0}, e.ID)
	for p := range e.seen {
		p.enqueue(websocket.BinaryMessage, out)
	}
	e.seen = nil
}

// ---------------------------------------------------------------------------
// Experience levels
// ---------------------------------------------------------------------------

func xpForLevel(l int) int {
	switch {
	case l >= 30:
		return 9*l - 158
	case l >= 15:
		return 5*l - 38
	}
	return 2*l + 7
}

func (s *Server) sendXP(p *Peer) {
	e := p.ext()
	p.json(map[string]any{"t": "xp", "l": e.XPLevel, "p": float64(e.XPPoints) / float64(xpForLevel(e.XPLevel)), "n": e.XPTotal})
}

// giveXP adds (or removes) experience points.
func (s *Server) giveXP(p *Peer, n int) bool {
	c := s.prepareExperience(p, n, false, "gain")
	if c == nil {
		return false
	}
	s.commitExperience(p, c)
	return true
}
func (s *Server) spendLevels(p *Peer, n int) bool {
	if n < 0 {
		return false
	}
	c := s.prepareExperience(p, n, true, "enchant_cost")
	if c == nil {
		return false
	}
	s.commitExperience(p, c)
	return true
}

// ---------------------------------------------------------------------------
// Orbs
// ---------------------------------------------------------------------------

type XPOrb struct {
	entView
	Value      int
	VX, VY, VZ float64
	Age        int // 100ms ticks
}

func orbSize(v int) int {
	for _, n := range []int{2477, 1237, 617, 307, 149, 73, 37, 17, 7, 3} {
		if v >= n {
			return n
		}
	}
	return 1
}

// spawnOrbs splits an amount into orbs at a position.
func (s *Server) spawnOrbs(r *Room, x, y, z float64, amount int) {
	if !r.usesSurvival() || amount <= 0 {
		return
	}
	rt := r.rt()
	for amount > 0 && len(rt.orbs) < 512 {
		v := orbSize(amount)
		amount -= v
		o := &XPOrb{Value: v, VX: (s.rng.Float64() - .5) * .2, VY: .2 + s.rng.Float64()*.1, VZ: (s.rng.Float64() - .5) * .2}
		o.ID, o.Kind, o.X, o.Y, o.Z, o.Data, o.Count = s.newEntID(r), 2, x, y, z, uint16(min(v, 65535)), 1
		rt.orbs[o.ID] = o
		s.syncEnt(r, &o.entView, false)
	}
}

func (s *Server) orbsTick(r *Room) {
	rt := r.rt()
	for id, o := range rt.orbs {
		o.Age++
		if o.Age > 3000 || o.Y < -20 {
			s.killEnt(&o.entView)
			delete(rt.orbs, id)
			continue
		}
		// Seek the nearest living survival player within 8 blocks.
		var best *Peer
		bd := 64.
		for p := range r.Peers {
			if p.state == nil || p.state.Dead || s.isSpectator(p) {
				continue
			}
			dx, dy, dz := p.state.X-o.X, p.state.Y+.9-o.Y, p.state.Z-o.Z
			if d := dx*dx + dy*dy + dz*dz; d < bd {
				best, bd = p, d
			}
		}
		if best != nil && o.Age > 5 {
			d := math.Sqrt(bd)
			if d < 1.2 {
				if !s.mendWithXP(best, o.Value) {
					continue
				}
				s.entEvent(&o.entView, 5, best.id)
				s.killEnt(&o.entView)
				delete(rt.orbs, id)
				continue
			}
			f := (1 - d/8) * (1 - d/8) * .35
			o.VX += (best.state.X - o.X) / d * f
			o.VY += (best.state.Y + .9 - o.Y) / d * f
			o.VZ += (best.state.Z - o.Z) / d * f
		}
		o.VY -= .06
		nx, ny, nz := o.X+o.VX, o.Y+o.VY, o.Z+o.VZ
		if s.solid(r, nx, ny, nz) {
			if !s.solid(r, o.X, ny, o.Z) {
				o.Y = ny
			} else {
				o.VY = 0
				o.VX *= .5
				o.VZ *= .5
			}
		} else {
			o.X, o.Y, o.Z = clampF(nx, r.minCoord()+.1, r.maxCoord()-.1), ny, clampF(nz, r.minCoord()+.1, r.maxCoord()-.1)
		}
		o.VX *= .9
		o.VZ *= .9
		o.VY *= .95
		s.syncEnt(r, &o.entView, true)
	}
}

// mendWithXP repairs a Mending item first, then grants the rest.
func (s *Server) mendWithXP(p *Peer, v int) bool {
	inv := p.state.Inv
	original := v
	repairSlot := -1
	var repaired *Stack
	var data map[string]any
	if inv != nil {
		for _, slot := range []int{p.state.Inv.Selected, 40, 39, 38, 37, 36} {
			if slot < 0 || slot >= len(inv.Slots) {
				continue
			}
			st := inv.Slots[slot]
			if st == nil || st.Damage <= 0 || st.Ench["mending"] == 0 {
				continue
			}
			fix := min(st.Damage, v*2)
			repaired = copyStack(st)
			repaired.Damage -= fix
			repairSlot = slot
			v -= (fix + 1) / 2
			data = map[string]any{"slot": slot, "before": itemSnapshot(st), "after": itemSnapshot(repaired), "reason": "mending"}
			if !s.allowed(api.InventoryChanging, p.room, p, data) {
				return false
			}
			break
		}
	}
	change := s.prepareExperience(p, v, false, "orb_pickup")
	if change == nil {
		return false
	}
	change.Data["orbValue"] = original
	if repairSlot >= 0 {
		inv.Slots[repairSlot] = repaired
		s.inventory(p, 0, 0)
		s.emit(api.InventoryChanged, p.room, p, data)
	}
	s.commitExperience(p, change)
	return true
}

// ---------------------------------------------------------------------------
// XP sources
// ---------------------------------------------------------------------------

func (s *Server) mobKilledExt(r *Room, p *Peer, m *Mob) {
	if p == nil || !r.usesSurvival() {
		return
	}
	spec := mobSpecs[m.Type]
	v := 1 + s.rng.Intn(3)
	if spec.Hostile {
		v = 5
	}
	switch m.Type {
	case "blaze", "wither_skeleton":
		v = 10
	case "ender_dragon":
		v = 12000
	}
	s.spawnOrbs(r, m.X, m.Y+.5, m.Z, v)
}

func (s *Server) oreXP(name string) int {
	switch name {
	case "Coal Ore":
		return s.rng.Intn(3)
	case "Diamond Ore", "Emerald Ore":
		return 3 + s.rng.Intn(5)
	case "Lapis Lazuli Ore", "Under Quartz Ore":
		return 2 + s.rng.Intn(4)
	case "Spark Ore", "Spark Ore (Lit)":
		return 1 + s.rng.Intn(5)
	}
	return 0
}

// smeltXP is the experience stored per smelted item (by output item name).
func smeltXP(name string) float64 {
	switch name {
	case "Iron Ingot":
		return .7
	case "Gold Ingot", "Diamond":
		return 1
	case "Glass", "Stone", "Charcoal", "Brick":
		return .1
	case "Cooked Beef", "Cooked Porkchop", "Cooked Chicken", "Cooked Mutton", "Steak", "Baked Potato":
		return .35
	}
	return .1
}

func init() {
	onBreak(func(s *Server, p *Peer, x, y, z, old int) {
		if p == nil || !p.room.usesSurvival() || s.isCreative(p) {
			return
		}
		held := s.heldItem(p)
		if held != nil && held.Ench["silk_touch"] > 0 {
			return
		}
		if v := s.oreXP(s.fullName(old)); v > 0 {
			s.spawnOrbs(p.room, float64(x)+.5, float64(y)+.5, float64(z)+.5, v)
		}
	})
	onRoomTick(func(s *Server, r *Room) {
		if r.usesSurvival() {
			s.orbsTick(r)
		}
	})
}
