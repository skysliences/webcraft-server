package game

// ---------------------------------------------------------------------------
// Redstone: power queries shared by doors, TNT, lamps, pistons and hoppers.
// (Full implementation below the API section.)
// ---------------------------------------------------------------------------

// blockPowered reports whether a block position receives redstone power
// from an adjacent source (powered wire pointing at it, lever, button,
// redstone torch, or a strongly powered neighbour block).
func (s *Server) blockPowered(r *Room, x, y, z int) bool {
	return s.powerAt(r, x, y, z) > 0
}

// powerAt returns the redstone power level (0..15) delivered to a position.
func (s *Server) powerAt(r *Room, x, y, z int) int {
	return s.powerAtWithConduction(r, x, y, z, true)
}

// Torch attachments receive power from adjacent sources and wires. A powered
// solid neighbour cannot conduct through a second solid attachment block.
func (s *Server) powerAtWithConduction(r *Room, x, y, z int, includePoweredBlocks bool) int {
	k := blockKey(x, y, z)
	if r.rt().redstoneQueryDepth >= 16 || r.rt().redstoneQueries[k] {
		return 0
	}
	if r.rt().redstoneQueries == nil {
		r.rt().redstoneQueries = map[uint64]bool{}
	}
	r.rt().redstoneQueries[k] = true
	r.rt().redstoneQueryDepth++
	defer func() { r.rt().redstoneQueryDepth--; delete(r.rt().redstoneQueries, k) }()
	best := 0
	for _, d := range faceOffsets {
		nx, ny, nz := x+d[0], y+d[1], z+d[2]
		var power int
		if includePoweredBlocks {
			power = s.emittedPower(r, nx, ny, nz, -d[0], -d[1], -d[2])
		} else {
			power = max(s.directPowerSource(r, nx, ny, nz, -d[0], -d[1], -d[2]), s.wireOutput(r, nx, ny, nz, -d[0], -d[1], -d[2]))
		}
		if power > best {
			best = power
		}
	}
	return best
}

// emittedPower is the power a block at (x,y,z) sends towards direction
// (dx,dy,dz). Filled in by redstone_impl.go.
var emittedPowerFn func(s *Server, r *Room, x, y, z, dx, dy, dz int) int

func (s *Server) emittedPower(r *Room, x, y, z, dx, dy, dz int) int {
	if emittedPowerFn == nil {
		return 0
	}
	return emittedPowerFn(s, r, x, y, z, dx, dy, dz)
}
