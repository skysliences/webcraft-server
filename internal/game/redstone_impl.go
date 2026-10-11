package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// Comprehensive Redstone Physics & Automata Engine (redstone_impl.go)
// 1. Redstone Wires (0..15 BFS propagation, slope up/down crawling, signal decay)
// 2. Power Sources: Redstone Block, Levers, Buttons, Redstone Torches, Pressure Plates
// 3. Redstone Repeaters (1..4 tick delay, signal boost to 15, diode, side-locking)
// 4. Redstone Comparators (container fullness detection, compare/subtract modes)
// 5. 6-Directional Pistons & Sticky Pistons (12-block push limit, sticky pull-back)
// 6. Hoppers (container transfer, drops sucking, powered lock-down)
// ---------------------------------------------------------------------------

var facing6 = [6][3]int{
	{0, -1, 0}, // 0: Down
	{0, 1, 0},  // 1: Up
	{0, 0, -1}, // 2: North
	{0, 0, 1},  // 3: South
	{-1, 0, 0}, // 4: West
	{1, 0, 0},  // 5: East
}

var horizontal4 = [4][3]int{
	{0, 0, -1}, // 0: North
	{1, 0, 0},  // 1: East
	{0, 0, 1},  // 2: South
	{-1, 0, 0}, // 3: West
}

func pistonFacingFromPlayer(p *Peer) int {
	if p == nil || p.state == nil {
		return 1 // Up
	}
	pitch := p.state.Pitch
	if pitch > math.Pi/4 {
		return 1 // Up (looking down)
	}
	if pitch < -math.Pi/4 {
		return 0 // Down (looking up)
	}
	h := (int(math.Floor((p.state.Yaw+math.Pi/4)/(math.Pi/2))) + 2) % 4
	switch h {
	case 0:
		return 3 // South
	case 1:
		return 4 // West
	case 2:
		return 2 // North
	case 3:
		return 5 // East
	}
	return 2
}

type RepeaterBlock struct {
	Facing    int  // 0:North, 1:East, 2:South, 3:West
	Delay     int  // 1..4 (ticks = Delay * 2)
	Locked    bool // locked by side repeater
	Powered   bool
	Countdown int
}

type ComparatorBlock struct {
	Facing      int
	Subtract    bool // false: compare, true: subtract
	OutputPower int
}

type PistonBlock struct {
	Facing   int // 0..5
	Sticky   bool
	Extended bool
}

type PressurePlateBlock struct {
	Wooden  bool
	Powered bool
	Timeout int
}

type RedstoneState struct {
	Wires          map[uint64]int                 // blockKey -> power level 0..15
	Levers         map[uint64]bool                // blockKey -> on/off
	Buttons        map[uint64]int                 // blockKey -> remaining ticks
	Repeaters      map[uint64]*RepeaterBlock      // blockKey -> repeater
	Comparators    map[uint64]*ComparatorBlock    // blockKey -> comparator
	Pistons        map[uint64]*PistonBlock        // blockKey -> piston
	PressurePlates map[uint64]*PressurePlateBlock // blockKey -> plate
	Hoppers        map[uint64]int                 // blockKey -> cooldown ticks
}

func getRedstone(r *Room) *RedstoneState {
	rs := r.rt().redstone
	if rs == nil {
		rs = &RedstoneState{Wires: map[uint64]int{}, Levers: map[uint64]bool{}, Buttons: map[uint64]int{}, Repeaters: map[uint64]*RepeaterBlock{}, Comparators: map[uint64]*ComparatorBlock{}, Pistons: map[uint64]*PistonBlock{}, PressurePlates: map[uint64]*PressurePlateBlock{}, Hoppers: map[uint64]int{}}
		r.rt().redstone = rs
		if r.Ext != nil {
			for k, d := range r.Ext.Blocks {
				if d != nil && d.Redstone != nil {
					restoreRedstoneComponent(r, k, d.Redstone)
				}
			}
		}
	}
	return rs
}

func init() {
	emittedPowerFn = redstoneEmittedPower

	// Register room tick hook for Redstone Automata
	onRoomTick(func(s *Server, r *Room) {
		getRedstone(r)
		if r.rt().redstoneDirty {
			s.recalculateWires(r)
		}

		s.tickRedstoneComponents(r)

		s.tickHoppers(r)
	})

	// Register block update hook for redstone wire and actuator triggers
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		s.checkActuators(r, x, y, z)
	})

	onPlace(8, func(s *Server, p *Peer, m Message, target int) bool {
		return s.handleRedstoneUse(p, m, nil, false)
	})
	onBreak(func(s *Server, p *Peer, x, y, z, old int) {
		s.clearRedstoneComponent(p.room, blockKey(x, y, z))
		s.recalculateWires(p.room)
		s.updateRedstoneAround(p.room, x, y, z)
	})

}

// Reports whether an entity (player, mob, drop) stands on a pressure plate
func (s *Server) entityOnPlate(r *Room, x, y, z int) bool {
	// Check players
	for p := range r.Peers {
		if p.state != nil && !p.state.Dead {
			if int(math.Floor(p.state.X)) == x && int(math.Floor(p.state.Z)) == z &&
				int(math.Floor(p.state.Y)) >= y && int(math.Floor(p.state.Y)) <= y+1 {
				return true
			}
		}
	}
	// Check mobs
	for _, m := range r.Mobs {
		if m != nil && m.Health > 0 {
			if int(math.Floor(m.X)) == x && int(math.Floor(m.Z)) == z &&
				int(math.Floor(m.Y)) >= y && int(math.Floor(m.Y)) <= y+1 {
				return true
			}
		}
	}
	// Check item drops
	for _, d := range r.Drops {
		if d != nil {
			if int(math.Floor(d.X)) == x && int(math.Floor(d.Z)) == z &&
				int(math.Floor(d.Y)) >= y && int(math.Floor(d.Y)) <= y+1 {
				return true
			}
		}
	}
	return false
}

// Repeater input power calculation from back
func (s *Server) isRepeaterInputPowered(r *Room, x, y, z int, facing int) bool {
	// Back offset is facing + 2 % 4
	backDir := (facing + 2) % 4
	bx := x + horizontal4[backDir][0]
	by := y
	bz := z + horizontal4[backDir][2]
	return s.emittedPower(r, bx, by, bz, -horizontal4[backDir][0], 0, -horizontal4[backDir][2]) > 0
}

// Checks if repeater is locked by another powered repeater pointing into its left/right side
func (s *Server) isRepeaterSideLocked(r *Room, x, y, z int, facing int) bool {
	rs := getRedstone(r)
	leftDir := (facing + 3) % 4
	rightDir := (facing + 1) % 4

	for _, sDir := range []int{leftDir, rightDir} {
		sx := x + horizontal4[sDir][0]
		sz := z + horizontal4[sDir][2]
		sideKey := blockKey(sx, y, sz)
		if rep := rs.Repeaters[sideKey]; rep != nil && rep.Powered {
			// Check if side repeater points towards this repeater
			opposite := (sDir + 2) % 4
			if rep.Facing == opposite {
				return true
			}
		}
	}
	return false
}

// Comparator output calculation (container fullness / comparison / subtraction)
func (s *Server) calculateComparatorOutput(r *Room, x, y, z int, cmp *ComparatorBlock) int {
	backDir := (cmp.Facing + 2) % 4
	bx := x + horizontal4[backDir][0]
	bz := z + horizontal4[backDir][2]

	backPower := 0

	// 1. Container measurement behind comparator
	containerKey := blockKey(bx, y, bz)
	if c := r.Containers[containerKey]; c != nil && len(c.Slots) > 0 {
		fullness, occupied := 0.0, false
		for _, sl := range c.Slots {
			if sl != nil && sl.Count > 0 {
				fullness += math.Min(1, float64(sl.Count)/float64(s.rules.max(sl.ID)))
				occupied = true
			}
		}
		if occupied {
			backPower = 1 + int(math.Floor(fullness/float64(len(c.Slots))*14))
		}
	} else {
		// Read normal redstone power from back
		backPower = s.emittedPower(r, bx, y, bz, -horizontal4[backDir][0], 0, -horizontal4[backDir][2])
	}

	// 2. Maximum side power from left & right
	leftDir := (cmp.Facing + 3) % 4
	rightDir := (cmp.Facing + 1) % 4
	sideLeft := s.emittedPower(r, x+horizontal4[leftDir][0], y, z+horizontal4[leftDir][2], -horizontal4[leftDir][0], 0, -horizontal4[leftDir][2])
	sideRight := s.emittedPower(r, x+horizontal4[rightDir][0], y, z+horizontal4[rightDir][2], -horizontal4[rightDir][0], 0, -horizontal4[rightDir][2])
	maxSide := max(sideLeft, sideRight)

	// 3. Compute mode output
	if cmp.Subtract {
		return max(0, backPower-maxSide)
	}
	if backPower >= maxSide {
		return backPower
	}
	return 0
}

// Recalculates 0..15 wire signal propagation using BFS
func (s *Server) recalculateWires(r *Room) {
	// Actual block commits invalidate connectivity; reconcile once per tick.
	// A recalculation must not invalidate itself when only signal state changes.
	r.rt().redstoneDirty = false
	rs := getRedstone(r)
	if len(rs.Wires) == 0 {
		return
	}

	// Calculate into a temporary map; a veto must retain committed power.
	levels := map[uint64]int{}
	for k := range rs.Wires {
		levels[k] = 0
	}

	type wireNode struct {
		x, y, z int
		power   int
	}
	queue := []wireNode{}

	// 1. Seed wires directly adjacent to redstone sources (15 power)
	for k := range rs.Wires {
		x, y, z := coords(k)
		maxSource := 0
		for _, d := range faceOffsets {
			nx, ny, nz := x+d[0], y+d[1], z+d[2]
			p := s.directPowerSource(r, nx, ny, nz, -d[0], -d[1], -d[2])
			if p > maxSource {
				maxSource = p
			}
		}
		if maxSource > 0 {
			levels[k] = maxSource
			queue = append(queue, wireNode{x, y, z, maxSource})
		}
	}

	// 2. BFS signal decay propagation (15 -> 14 -> ... -> 0)
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if curr.power <= 1 {
			continue
		}

		nextPower := curr.power - 1
		for _, d := range [][2]int{{0, 1}, {0, -1}, {1, 0}, {-1, 0}} {
			nx, nz := curr.x+d[0], curr.z+d[1]

			// Check flat adjacent
			checkY := []int{curr.y, curr.y + 1, curr.y - 1}
			for _, ny := range checkY {
				nk := blockKey(nx, ny, nz)
				if _, ok := rs.Wires[nk]; ok {
					// Check slope passability
					if ny == curr.y+1 && s.mobSolid(r, curr.x, curr.y+1, curr.z) {
						continue // Blocked above
					}
					if ny == curr.y-1 && s.mobSolid(r, nx, curr.y, nz) {
						continue // Blocked on edge
					}
					if levels[nk] < nextPower {
						levels[nk] = nextPower
						queue = append(queue, wireNode{nx, ny, nz, nextPower})
					}
				}
			}
		}
	}

	// 3. Commit only accepted signal changes, then notify neighbouring actuators.
	for k, power := range levels {
		next := redstoneSnapshot(r, k)
		if next != nil && next.Power != power {
			next.Power = power
			s.changeRedstone(r, nil, k, next, "redstone_signal")
		}
	}
	for k := range rs.Wires {
		x, y, z := coords(k)
		s.updateRedstoneAround(r, x, y, z)
	}
}

// Power from non-wire source directly
func (s *Server) directPowerSource(r *Room, x, y, z, dx, dy, dz int) int {
	b := s.block(r, x, y, z)
	name := s.nameOf(b)
	k := blockKey(x, y, z)
	rs := getRedstone(r)

	// Redstone Block (ID 40 / Block of Spark)
	if b == 40 || name == "Block of Spark" || name == "Block of Redstone" {
		return 15
	}
	// Active Lever
	if rs.Levers[k] {
		return 15
	}
	// Active Button
	if rs.Buttons[k] > 0 {
		return 15
	}
	// Active Pressure Plate
	if pl := rs.PressurePlates[k]; pl != nil && pl.Powered {
		return 15
	}
	// Redstone Torch (Upward strong power, horizontal power)
	if b == 109 || name == "Redstone Torch" {
		face := 1
		if state := redstoneSnapshot(r, k); state != nil && state.SupportFace >= 1 && state.SupportFace <= 5 {
			face = state.SupportFace
		}
		off := faceOffsets[face]
		if dx == -off[0] && dy == -off[1] && dz == -off[2] {
			return 0
		}
		if dy == 1 || (dy == 0 && (dx != 0 || dz != 0)) {
			if !s.blockPowered(r, x-off[0], y-off[1], z-off[2]) {
				return 15
			}
		}
	}
	// Powered Repeater facing output
	if rep := rs.Repeaters[k]; rep != nil && rep.Powered {
		if dx == horizontal4[rep.Facing][0] && dz == horizontal4[rep.Facing][2] {
			return 15
		}
	}
	// Comparator output
	if cmp := rs.Comparators[k]; cmp != nil && cmp.OutputPower > 0 {
		if dx == horizontal4[cmp.Facing][0] && dz == horizontal4[cmp.Facing][2] {
			return cmp.OutputPower
		}
	}
	if power := s.mechanicsPower(r, k, dx, dy, dz); power > 0 {
		return power
	}
	return 0
}

// solidBlockPower checks if a solid block is powered by a wire above, a torch below, or a repeater/comparator pointing into it.
func (s *Server) solidBlockPower(r *Room, x, y, z int) int {
	rs := getRedstone(r)
	maxPower := 0

	// 1. Wire directly on top of the block
	aboveKey := blockKey(x, y+1, z)
	if w, ok := rs.Wires[aboveKey]; ok && w > maxPower {
		maxPower = w
	}

	// 2. Redstone torch directly below the block (upward strong power)
	if s.directPowerSource(r, x, y-1, z, 0, 1, 0) > maxPower {
		maxPower = 15
	}

	// 3. Repeater or comparator pointing into this block
	for hDir, off := range horizontal4 {
		nx, nz := x-off[0], z-off[2]
		nk := blockKey(nx, y, nz)
		if rep := rs.Repeaters[nk]; rep != nil && rep.Powered && rep.Facing == hDir {
			return 15
		}
		if cmp := rs.Comparators[nk]; cmp != nil && cmp.OutputPower > maxPower && cmp.Facing == hDir {
			maxPower = cmp.OutputPower
		}
	}
	return maxPower
}

// Calculates power emitted by block at (x,y,z) in direction (dx,dy,dz)
func redstoneEmittedPower(s *Server, r *Room, x, y, z, dx, dy, dz int) int {
	k := blockKey(x, y, z)
	rs := getRedstone(r)

	// 1. Direct sources
	if p := s.directPowerSource(r, x, y, z, dx, dy, dz); p > 0 {
		return p
	}

	// 2. Redstone Wire
	if w, ok := rs.Wires[k]; ok && w > 0 {
		return w
	}

	// 3. Powered solid cube (can power neighbouring actuators like pistons)
	b := s.block(r, x, y, z)
	if s.supportsTorch(b) && s.redstoneKind(b) == "" {
		if p := s.solidBlockPower(r, x, y, z); p > 0 {
			return p
		}
	}

	return 0
}

// Update redstone propagation around (x, y, z)
func (s *Server) updateRedstoneAround(r *Room, x, y, z int) {
	for _, d := range faceOffsets {
		nx, ny, nz := x+d[0], y+d[1], z+d[2]
		s.checkActuators(r, nx, ny, nz)
		// Quasi-connectivity (QC): changes at (nx, ny, nz) can activate piston at (nx, ny-1, nz)
		s.checkActuators(r, nx, ny-1, nz)
	}
	// QC: power source at (x, y, z) can activate piston directly below at (x, y-1, z)
	s.checkActuators(r, x, y-1, z)
}

// Check mechanical actuators (TNT, Doors, Trapdoors, Pistons, Hoppers)
func (s *Server) checkActuators(r *Room, x, y, z int) {
	b := s.block(r, x, y, z)
	if kind := s.redstoneKind(b); kind != "piston" && kind != "sticky_piston" {
		return
	}
	k := blockKey(x, y, z)
	s.trackRedstoneBlock(r, k, b)
	pst := getRedstone(r).Pistons[k]
	if pst == nil || pst.Facing < 0 || pst.Facing >= len(facing6) {
		return
	}
	// Standard power + Quasi-Connectivity (power delivered to the space above)
	powered := s.blockPowered(r, x, y, z) || s.blockPowered(r, x, y+1, z)
	if powered && !pst.Extended {
		s.pushPiston(r, x, y, z, facing6[pst.Facing], pst.Sticky)
	} else if !powered && pst.Extended {
		if pst.Sticky {
			s.pullPiston(r, x, y, z, facing6[pst.Facing])
		} else {
			next := redstoneSnapshot(r, k)
			next.Extended = false
			s.commitPistonMove(r, k, next, nil, "redstone_piston_retract")
		}
	}
}

func (s *Server) pistonPushPlan(r *Room, x, y, z int, dir [3]int) ([][4]int, bool) {
	sources := [][3]int{}
	nx, ny, nz := x+dir[0], y+dir[1], z+dir[2]
	for i := 0; i < 12; i++ {
		if !r.inside(nx, nz) || ny < 1 || ny >= 128 {
			return nil, false
		}
		b := s.block(r, nx, ny, nz)
		if b == 0 {
			break
		}
		// Container/attached-component metadata cannot be silently relocated.
		name := s.nameOf(b)
		if name == "Bedrock" || name == "Obsidian" || s.containerBlock(b) || r.Containers[blockKey(nx, ny, nz)] != nil || s.redstoneKind(b) != "" || r.Ext != nil && r.Ext.Blocks[blockKey(nx, ny, nz)] != nil {
			return nil, false
		}
		sources = append(sources, [3]int{nx, ny, nz})
		nx += dir[0]
		ny += dir[1]
		nz += dir[2]
	}
	if !r.inside(nx, nz) || ny < 1 || ny >= 128 || s.block(r, nx, ny, nz) != 0 || r.Containers[blockKey(nx, ny, nz)] != nil || r.Ext != nil && r.Ext.Blocks[blockKey(nx, ny, nz)] != nil {
		return nil, false
	}
	if len(sources) == 0 {
		return nil, true
	}
	changes := [][4]int{{x + dir[0], y + dir[1], z + dir[2], 0}}
	for _, from := range sources {
		changes = append(changes, [4]int{from[0] + dir[0], from[1] + dir[1], from[2] + dir[2], s.block(r, from[0], from[1], from[2])})
	}
	return changes, true
}
func (s *Server) commitPistonMove(r *Room, k uint64, next *RedstoneComponent, changes [][4]int, reason string) bool {
	if next == nil || !validRedstoneComponent(next) {
		return false
	}
	data := s.redstoneData(r, nil, k, next, reason)
	blocks := []map[string]any{}
	for _, at := range changes {
		blocks = append(blocks, s.blockData(r, blockKey(at[0], at[1], at[2]), at[3]))
	}
	data["blocks"] = blocks
	if !s.allowed(api.RedstoneComponentChanging, r, nil, data) {
		return false
	}
	commit := func() { storeRedstoneComponent(r, k, next) }
	if len(changes) > 0 {
		if !s.setBlocksWithCommit(r, changes, setQuiet, reason, commit) {
			return false
		}
	} else {
		commit()
	}
	s.broadcastRedstone(r, k)
	s.emit(api.RedstoneComponentChanged, r, nil, data)
	x, y, z := coords(k)
	sound := "block.piston.contract"
	if next.Extended {
		sound = "block.piston.extend"
	}
	soundAt(r, sound, float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
	return true
}
func pistonDirection(dir [3]int) int {
	for i, value := range facing6 {
		if value == dir {
			return i
		}
	}
	return -1
}

// Push and pull validate a complete block batch and the component's state
// before committing either. A protected landing cell cannot delete a source.
func (s *Server) pushPiston(r *Room, x, y, z int, dir [3]int, sticky bool) bool {
	facing := pistonDirection(dir)
	if facing < 0 {
		return false
	}
	k := blockKey(x, y, z)
	kind := s.redstoneKind(s.block(r, x, y, z))
	if kind != "piston" && kind != "sticky_piston" {
		return false
	}
	s.trackRedstoneBlock(r, k, s.block(r, x, y, z))
	changes, ok := s.pistonPushPlan(r, x, y, z, dir)
	if !ok {
		return false
	}
	next := redstoneSnapshot(r, k)
	next.Facing = facing
	next.Extended = true
	return s.commitPistonMove(r, k, next, changes, "redstone_piston_extend")
}
func (s *Server) pullPiston(r *Room, x, y, z int, dir [3]int) bool {
	facing := pistonDirection(dir)
	if facing < 0 {
		return false
	}
	k := blockKey(x, y, z)
	kind := s.redstoneKind(s.block(r, x, y, z))
	if kind != "piston" && kind != "sticky_piston" {
		return false
	}
	s.trackRedstoneBlock(r, k, s.block(r, x, y, z))
	sx, sy, sz := x+dir[0]*2, y+dir[1]*2, z+dir[2]*2
	dx, dy, dz := x+dir[0], y+dir[1], z+dir[2]
	b := s.block(r, sx, sy, sz)
	changes := [][4]int{}
	name := s.nameOf(b)
	if b != 0 && name != "Bedrock" && name != "Obsidian" && !s.containerBlock(b) && r.Containers[blockKey(sx, sy, sz)] == nil && s.redstoneKind(b) == "" && (r.Ext == nil || r.Ext.Blocks[blockKey(sx, sy, sz)] == nil && r.Ext.Blocks[blockKey(dx, dy, dz)] == nil) && r.Containers[blockKey(dx, dy, dz)] == nil && s.block(r, dx, dy, dz) == 0 && r.inside(sx, sz) && sy >= 1 && sy < 128 {
		changes = [][4]int{{sx, sy, sz, 0}, {dx, dy, dz, b}}
	}
	next := redstoneSnapshot(r, k)
	next.Facing = facing
	next.Extended = false
	return s.commitPistonMove(r, k, next, changes, "redstone_piston_retract")
}

// Toggle lever
func (s *Server) toggleLever(r *Room, x, y, z int) bool {
	k := blockKey(x, y, z)
	rs := getRedstone(r)
	rs.Levers[k] = !rs.Levers[k]
	s.recalculateWires(r)
	s.updateRedstoneAround(r, x, y, z)
	soundAt(r, "block.lever.click", float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, nil)
	return rs.Levers[k]
}

// Press button (wooden=30 ticks, stone=20 ticks)
func (s *Server) pressButton(r *Room, x, y, z int, wooden bool) {
	k := blockKey(x, y, z)
	rs := getRedstone(r)
	ticks := 20
	if wooden {
		ticks = 30
	}
	rs.Buttons[k] = ticks
	s.recalculateWires(r)
	s.updateRedstoneAround(r, x, y, z)
	soundAt(r, "block.stone_button.click_on", float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, nil)
}
