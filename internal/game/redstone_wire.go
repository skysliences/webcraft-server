package game

// Match rules/redstone-wire.js: logical connections, visible arms and output
// directions must agree. An isolated wire defaults to a cross; Dot is saved.
func (s *Server) wireLinks(r *Room, x, y, z int) uint8 {
	var mask uint8
	for direction, d := range horizontal4 {
		nx, nz := x+d[0], z+d[2]
		id := s.block(r, nx, y, nz)
		connected := id == 108 || id == 109 || id == 174 || id == 175 || id == 218 || id == 213 || id == 40
		if id == 166 || id == 167 {
			facing := 0
			if state := redstoneSnapshot(r, blockKey(nx, y, nz)); state != nil {
				facing = state.Facing
			}
			connected = facing%2 == direction%2
		}
		if id == 216 {
			if state := mechanicSnapshot(r, blockKey(nx, y, nz)); state != nil {
				connected = state.Facing == [4]int{2, 5, 3, 4}[direction]
			}
		}
		kind := s.redstoneKind(id)
		connected = connected || kind == "lever" || kind == "button" || kind == "wooden_button"
		if connected || s.mobSolid(r, nx, y, nz) && !s.mobSolid(r, x, y+1, z) && s.block(r, nx, y+1, nz) == 108 || !s.mobSolid(r, nx, y, nz) && s.block(r, nx, y-1, nz) == 108 {
			mask |= 1 << direction
		}
	}
	return mask
}

func (s *Server) wireOutput(r *Room, x, y, z, dx, dy, dz int) int {
	power := getRedstone(r).Wires[blockKey(x, y, z)]
	if power == 0 || dy > 0 {
		return 0
	}
	if dy < 0 {
		return power
	}
	mask := s.wireLinks(r, x, y, z)
	if mask == 0 {
		if state := redstoneSnapshot(r, blockKey(x, y, z)); state != nil && state.Dot {
			return 0
		}
		mask = 15
	} else if mask&(mask-1) == 0 {
		mask |= (mask<<2 | mask>>2) & 15
	}
	for direction, d := range horizontal4 {
		if dx == d[0] && dz == d[2] && mask&(1<<direction) != 0 {
			return power
		}
	}
	return 0
}

func redstoneInteractive(kind string) bool {
	return kind == "wire" || kind == "lever" || kind == "button" || kind == "wooden_button" || kind == "repeater" || kind == "comparator"
}
