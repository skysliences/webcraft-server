package game

import (
	"math"
)

// ---------------------------------------------------------------------------
// Dimensions: Nether portals (lighting, validation, travel, portal search and
// construction), End portal frames and eyes, the End spawn platform, the exit
// portal and the end credits.
// ---------------------------------------------------------------------------

const (
	blkObsidian   = 24
	blkFire       = 95
	blkPortalX    = 189
	blkPortalZ    = 190
	blkFrame      = 201
	blkFrameEye   = 202
	blkEndPortal  = 208
	blkExitPortal = 209
	blkDragonEgg  = 210
	blkBedrock    = 12
)

func isNetherPortal(b int) bool { return b == blkPortalX || b == blkPortalZ }

// ---------------------------------------------------------------------------
// End
// ---------------------------------------------------------------------------

// endSpawn is the obsidian platform players arrive on.
func endSpawn(r *Room) [3]int {
	c := r.width() / 2
	return [3]int{c + 100, 49, c}
}

// prepareEnd builds the spawn platform and the exit portal once per world.
func (s *Server) prepareEnd(c *Room) {
	if c.Edits == nil {
		c.Edits = map[uint64]int{}
	}
	s.buildEndPlatform(c)
	_, portal := s.engine.endSpikes(c)
	// 开局生成未激活的基岩祭坛框架，击败末影龙后再点亮传送门并生成龙蛋
	s.buildExitPortal(c, portal, false)
	c.Dirty = true
}

func (s *Server) buildEndPlatform(c *Room) {
	sp := endSpawn(c)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			x, z := sp[0]+dx, sp[2]+dz
			s.setBlock(c, x, sp[1]-1, z, blkObsidian, setQuiet|setNoNeighbors)
			for dy := 0; dy < 3; dy++ {
				s.setBlock(c, x, sp[1]+dy, z, 0, setQuiet|setNoNeighbors)
			}
		}
	}
}

// buildExitPortal places the bedrock fountain; portal activates and egg generates upon victory
func (s *Server) buildExitPortal(c *Room, at [3]int, egg bool) {
	cx, y, cz := at[0], at[1], at[2]
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			d := float64(dx*dx + dz*dz)
			x, z := cx+dx, cz+dz
			switch {
			case d <= 6.25:
				s.setBlock(c, x, y-1, z, blkBedrock, setQuiet|setNoNeighbors)
				if dx == 0 && dz == 0 {
					continue
				}
				if egg {
					s.setBlock(c, x, y, z, blkExitPortal, setQuiet|setNoNeighbors)
				} else {
					s.setBlock(c, x, y, z, 0, setQuiet|setNoNeighbors)
				}
			case d <= 12.25:
				s.setBlock(c, x, y-1, z, blkBedrock, setQuiet|setNoNeighbors)
				s.setBlock(c, x, y, z, blkBedrock, setQuiet|setNoNeighbors)
			}
		}
	}
	for dy := 0; dy < 4; dy++ {
		s.setBlock(c, cx, y+dy, cz, blkBedrock, setQuiet|setNoNeighbors)
	}
	if egg {
		s.setBlock(c, cx, y+4, cz, blkDragonEgg, setQuiet|setNoNeighbors)
	}
}

// ---------------------------------------------------------------------------
// Nether portal frames (port of rules/portal.js)
// ---------------------------------------------------------------------------

type portalFrame struct {
	x, y, z       int
	axisX         bool
	width, height int
}

func (s *Server) portalSize(r *Room, x, y, z int, axisX bool) *portalFrame {
	get := func(a, b, c int) int { return s.block(r, a, b, c) }
	dx, dz := 0, 1
	if axisX {
		dx, dz = 1, 0
	}
	empty := func(b int) bool { return b == 0 || b == blkFire || isNetherPortal(b) }
	if !empty(get(x, y, z)) {
		return nil
	}
	low := y
	for y > 0 && low-y < 21 && empty(get(x, y-1, z)) {
		y--
	}
	if get(x, y-1, z) != blkObsidian {
		return nil
	}
	for left := 0; left < 21 && empty(get(x-dx, y, z-dz)) && get(x-dx, y-1, z-dz) == blkObsidian; left++ {
		x -= dx
		z -= dz
	}
	if get(x-dx, y, z-dz) != blkObsidian {
		return nil
	}
	width := 0
	for width <= 21 && empty(get(x+width*dx, y, z+width*dz)) && get(x+width*dx, y-1, z+width*dz) == blkObsidian {
		width++
	}
	if width < 2 || width > 21 || get(x+width*dx, y, z+width*dz) != blkObsidian {
		return nil
	}
	height := 0
	for ; height <= 21; height++ {
		top := true
		for i := 0; i < width; i++ {
			if get(x+i*dx, y+height, z+i*dz) != blkObsidian {
				top = false
				break
			}
		}
		if top {
			break
		}
		if get(x-dx, y+height, z-dz) != blkObsidian || get(x+width*dx, y+height, z+width*dz) != blkObsidian {
			return nil
		}
		for i := 0; i < width; i++ {
			if !empty(get(x+i*dx, y+height, z+i*dz)) {
				return nil
			}
		}
	}
	if height < 3 || height > 21 {
		return nil
	}
	return &portalFrame{x, y, z, axisX, width, height}
}

func (s *Server) findFrame(r *Room, x, y, z int) *portalFrame {
	if f := s.portalSize(r, x, y, z, true); f != nil {
		return f
	}
	return s.portalSize(r, x, y, z, false)
}

func (s *Server) fillPortal(r *Room, f *portalFrame) {
	b, dx, dz := blkPortalZ, 0, 1
	if f.axisX {
		b, dx, dz = blkPortalX, 1, 0
	}
	for j := 0; j < f.height; j++ {
		for i := 0; i < f.width; i++ {
			s.setBlock(r, f.x+i*dx, f.y+j, f.z+i*dz, b, setNoNeighbors)
		}
	}
	soundAt(r, "block.portal.trigger", float64(f.x)+.5, float64(f.y)+1, float64(f.z)+.5, nil)
}

// tryLightPortal is called when fire appears at a position.
func (s *Server) tryLightPortal(r *Room, x, y, z int) bool {
	if !r.usesSurvival() || r.dimName() == "end" {
		return false
	}
	if f := s.findFrame(r, x, y, z); f != nil {
		s.fillPortal(r, f)
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Travel
// ---------------------------------------------------------------------------

func (s *Server) touchingBlock(p *Peer, test func(int) bool) (int, bool) {
	st := p.state
	for _, dy := range []float64{0, .9, 1.6} {
		for _, o := range [][2]float64{{0, 0}, {.29, .29}, {-.29, -.29}, {.29, -.29}, {-.29, .29}} {
			b := s.block(p.room, int(math.Floor(st.X+o[0])), int(math.Floor(st.Y+dy)), int(math.Floor(st.Z+o[1])))
			if test(b) {
				return b, true
			}
		}
	}
	return 0, false
}

func portalTick(s *Server, p *Peer) {
	if !p.room.usesSurvival() || s.isSpectator(p) {
		return
	}
	e := p.ext()
	if e.EndCredits {
		return
	}
	if e.PortalCool > 0 {
		e.PortalCool--
	}
	b, touching := s.touchingBlock(p, func(b int) bool {
		return isNetherPortal(b) || b == blkEndPortal || b == blkExitPortal
	})
	if !touching {
		if e.portalTicks > 0 {
			p.json(map[string]any{"t": "portal", "n": 0})
		}
		e.portalTicks = 0
		return
	}
	if e.portalTicks < 0 || e.PortalCool > 0 {
		return // must step out first
	}
	switch b {
	case blkEndPortal:
		e.portalTicks = -1
		s.enterEnd(p)
		return
	case blkExitPortal:
		e.portalTicks = -1
		s.leaveEnd(p)
		return
	}
	if s.isCreative(p) {
		e.portalTicks = 80
	} else {
		e.portalTicks += 2
	}
	p.json(map[string]any{"t": "portal", "n": min(80, e.portalTicks)})
	if e.portalTicks >= 80 {
		e.portalTicks = -1
		e.PortalCool = 20
		s.netherTravel(p)
	}
}

func (s *Server) netherTravel(p *Peer) {
	from := p.room
	var target *Room
	scale := 8.
	if from.dimName() == "nether" {
		target = s.dimRoom(from, "overworld")
	} else if from.dimName() == "overworld" {
		target = s.dimRoom(from, "nether")
		scale = 1. / 8
	} else {
		return
	}
	low,high:=target.minCoord()+4,target.maxCoord()-5
	tx := clampF(math.Floor(p.state.X*scale),low,high)
	tz := clampF(math.Floor(p.state.Z*scale),low,high)
	pos := s.findOrBuildPortal(target, int(tx), int(p.state.Y), int(tz))
	if !s.moveToDimension(p, target, pos[0], pos[1], pos[2], true) { return }
	p.ext().portalTicks = -1
	p.json(map[string]any{"t": "portal", "n": 0})
	soundAt(target, "block.portal.travel", pos[0], pos[1], pos[2], nil)
}

// findOrBuildPortal returns a standing position inside a portal near (x,z).
func (s *Server) findOrBuildPortal(r *Room, x, y, z int) [3]float64 {
	best, bd := uint64(0), math.MaxFloat64
	radius := 128
	if r.dimName() == "nether" {
		radius = 16
	}
	for k, b := range r.Edits {
		if !isNetherPortal(b) {
			continue
		}
		bx, by, bz := coords(k)
		if abs(bx-x) > radius || abs(bz-z) > radius {
			continue
		}
		// Prefer the lowest block of a portal column.
		if isNetherPortal(s.block(r, bx, by-1, bz)) {
			continue
		}
		d := float64((bx-x)*(bx-x) + (bz-z)*(bz-z) + (by-y)*(by-y))
		if d < bd {
			best, bd = k, d
		}
	}
	if bd < math.MaxFloat64 {
		bx, by, bz := coords(best)
		return [3]float64{float64(bx) + .5, float64(by), float64(bz) + .5}
	}
	return s.buildPortal(r, x, y, z)
}

func clampF2(v, lo, hi int) int { return min(max(v, lo), hi) }

func (s *Server) solidAt(r *Room, x, y, z int) bool {
	k := s.kind(s.block(r, x, y, z))
	return k == 1 || k == 2 || k == 3
}

// buildPortal constructs a 4x5 obsidian frame (X axis) near (x,z).
func (s *Server) buildPortal(r *Room, x, y, z int) [3]float64 {
	lo, hi := 4, 120
	if r.dimName() == "nether" {
		lo, hi = 32, 100
	}
	fits := func(x0, y0, z0 int) bool {
		for dx := -1; dx <= 2; dx++ {
			if !s.solidAt(r, x0+dx, y0-1, z0) {
				return false
			}
			for dy := 0; dy < 4; dy++ {
				for dz := -1; dz <= 1; dz++ {
					b := s.block(r, x0+dx, y0+dy, z0+dz)
					if b != 0 {
						return false
					}
				}
			}
		}
		return true
	}
	bx, by, bz, found := x, 0, z, false
search:
	for rad := 0; rad <= 12; rad += 2 {
		for dx := -rad; dx <= rad; dx += 2 {
			for dz := -rad; dz <= rad; dz += 2 {
				if max(abs(dx), abs(dz)) != rad {
					continue
				}
				cx, cz := x+dx, z+dz
				if !r.inside(cx-2, cz-2) || !r.inside(cx+3, cz+2) {
					continue
				}
				for yy := lo; yy <= hi; yy++ {
					if fits(cx, yy, cz) {
						bx, by, bz, found = cx, yy, cz, true
						break search
					}
				}
			}
		}
	}
	if !found {
		by = clampF2(y, lo, hi)
		if r.dimName() == "nether" {
			by = 64
		}
		bx = int(clampF(float64(bx),r.minCoord()+3,r.maxCoord()-4))
		bz = int(clampF(float64(bz),r.minCoord()+3,r.maxCoord()-4))
	}
	// Platform and clearance.
	for dx := -1; dx <= 2; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if !s.solidAt(r, bx+dx, by-1, bz+dz) || dz == 0 {
				s.setBlock(r, bx+dx, by-1, bz+dz, blkObsidian, setNoNeighbors)
			}
			for dy := 0; dy < 4; dy++ {
				if dz != 0 {
					s.setBlock(r, bx+dx, by+dy, bz+dz, 0, setNoNeighbors)
				}
			}
		}
	}
	for dy := 0; dy <= 3; dy++ {
		s.setBlock(r, bx-1, by+dy, bz, blkObsidian, setNoNeighbors)
		s.setBlock(r, bx+2, by+dy, bz, blkObsidian, setNoNeighbors)
	}
	for dx := -1; dx <= 2; dx++ {
		s.setBlock(r, bx+dx, by+3, bz, blkObsidian, setNoNeighbors)
	}
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy < 3; dy++ {
			s.setBlock(r, bx+dx, by+dy, bz, blkPortalX, setNoNeighbors)
		}
	}
	return [3]float64{float64(bx) + 1, float64(by), float64(bz) + .5}
}

func (s *Server) enterEnd(p *Peer) {
	target := s.dimRoom(p.room, "end")
	s.buildEndPlatform(target)
	sp := endSpawn(target)
	if !s.moveToDimension(p, target, float64(sp[0])+.5, float64(sp[1]), float64(sp[2])+.5, true) { return }
	p.ext().portalTicks = -1
}

func (s *Server) leaveEnd(p *Peer) {
	e := p.ext()
	p.room.ext().Beaten = true
	p.json(map[string]any{
		"t":       "bossbar",
		"name":    "末影龙",
		"percent": 0,
		"visible": false,
	})
	if !e.SeenCredits {
		e.EndCredits = true
		p.json(map[string]any{"t": "credits", "on": 1})
		p.room.Dirty = true
		return
	}
	s.returnFromEnd(p)
}

func (s *Server) returnFromEnd(p *Peer) {
	p.json(map[string]any{
		"t":       "bossbar",
		"name":    "末影龙",
		"percent": 0,
		"visible": false,
	})
	root := p.room.root()
	e := p.ext()
	sp := s.engine.spawn(root)
	pos := [3]float64{float64(sp[0]) + .5, float64(sp[1]), float64(sp[2]) + .5}
	if e.SpawnSet && s.nameOf(s.block(root, e.SpawnX, e.SpawnY, e.SpawnZ)) == "Bed" {
		if spot, ok := s.bedExit(root, e.SpawnX, e.SpawnY, e.SpawnZ); ok {
			pos = spot
		}
	}
	if !s.moveToDimension(p, root, pos[0], pos[1], pos[2], true) { return }
	e.portalTicks = -1
}

// ---------------------------------------------------------------------------
// End portal frames
// ---------------------------------------------------------------------------

func (s *Server) checkEndPortal(r *Room, x, y, z int) {
	for cx := x - 4; cx <= x+4; cx++ {
		for cz := z - 4; cz <= z+4; cz++ {
			ok := true
			for i := -1; i <= 1 && ok; i++ {
				for _, q := range [][2]int{{cx + i, cz - 2}, {cx + i, cz + 2}, {cx - 2, cz + i}, {cx + 2, cz + i}} {
					if s.block(r, q[0], y, q[1]) != blkFrameEye {
						ok = false
						break
					}
				}
			}
			if !ok {
				continue
			}
			for dx := -1; dx <= 1; dx++ {
				for dz := -1; dz <= 1; dz++ {
					s.setBlock(r, cx+dx, y, cz+dz, blkEndPortal, setNoNeighbors)
				}
			}
			r.ext().EndPortalLit = true
			r.Dirty = true
			for p := range r.Peers {
				p.json(map[string]any{"t": "snd", "e": "block.end_portal.spawn", "x": float64(cx) + .5, "y": float64(y), "z": float64(cz) + .5})
			}
			return
		}
	}
}

func init() {
	onPeerTick(portalTick)
	// Fire next to an obsidian frame lights the portal; broken frames collapse.
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if !r.usesSurvival() {
			return
		}
		if b == blkFire {
			s.tryLightPortal(r, x, y, z)
			return
		}
		if isNetherPortal(b) {
			f := s.portalSize(r, x, y, z, b == blkPortalX)
			if f == nil {
				s.setBlock(r, x, y, z, 0, 0)
			}
		}
	})
	// Placing an eye of ender into an End portal frame.
	onPlace(5, func(s *Server, p *Peer, m Message, target int) bool {
		held := s.heldItem(p)
		if held == nil || held.ID != 768 || target != blkFrame || !p.room.usesSurvival() {
			return false
		}
		if !s.canBuild(p) {
			return true
		}
		s.setBlock(p.room, m.X, m.Y, m.Z, blkFrameEye, setQuiet)
		s.consumeHeld(p, 1)
		s.inventory(p, 0, 0)
		soundAt(p.room, "block.end_portal_frame.fill", float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, nil)
		s.checkEndPortal(p.room, m.X, m.Y, m.Z)
		return true
	})
	onMessage("endseen", func(s *Server, p *Peer, m Message) bool {
		e := p.ext()
		if !e.EndCredits {
			p.json(map[string]any{"t": "credits", "on": 0})
			return true
		}
		e.EndCredits = false
		e.SeenCredits = true
		p.json(map[string]any{"t": "credits", "on": 0})
		if p.room.dimName() == "end" {
			s.returnFromEnd(p)
		}
		p.room.Dirty = true
		return true
	})
}
