package game

import "math"

type spawnLightCell struct{ x, y, z, l int }
type spawnLighting struct {
	bases   map[ChunkCoord][]byte
	columns map[[2]int][128]byte
	sources map[ChunkCoord][]spawnLightCell
}

func newSpawnLighting() *spawnLighting {
	return &spawnLighting{map[ChunkCoord][]byte{}, map[[2]int][128]byte{}, map[ChunkCoord][]spawnLightCell{}}
}
func (s *Server) spawnLights(r *Room) *spawnLighting {
	if r.rt().spawnLights != nil {
		return r.rt().spawnLights
	}
	return newSpawnLighting()
}
func (s *Server) spawnModel(b int) int {
	if model, ok := s.rules.blockModels[b]; ok {
		return model
	}
	return b
}
func (s *Server) spawnOpacity(b int) int {
	switch s.kind(s.spawnModel(b)) {
	case 1:
		return 15
	case 2, 4, 6:
		return 1
	}
	return 0
}

// Snapshot committed chunks only. copy_box reads the existing finite generator;
// infinite worlds never ensure/load extra chunks for a light query.
func (s *Server) spawnLightBlock(r *Room, c *spawnLighting, x, y, z int) (int, bool) {
	if !r.inside(x, z) || y < 0 || y >= 128 {
		return 0, false
	}
	cc := chunkCoord(x, z)
	if !s.mobChunkLoaded(r, cc) {
		return 0, false
	}
	if b, ok := r.Edits[blockKey(x, y, z)]; ok {
		return b, true
	}
	if r.Infinite && r.chunks[cc] != nil {
		base := r.chunks[cc].Base
		if len(base) != chunkVolume {
			return 0, false
		}
		return int(base[chunkIndex(x, y, z)]), true
	}
	base, ok := c.bases[cc]
	if !ok {
		if r.Infinite {
			base = s.engine.legacyChunk(r, cc)
		} else {
			s.engine.use(r)
			n := int(s.engine.call("copy_box", uint64(cc.X*16), 0, uint64(cc.Z*16), 16, 128, 16))
			if n != chunkVolume {
				return 0, false
			}
			raw, valid := s.engine.mod.Memory().Read(uint32(s.engine.call("ebuf_ptr")), uint32(n))
			if !valid {
				return 0, false
			}
			base = make([]byte, chunkVolume)
			for by := 0; by < 128; by++ {
				for bz := 0; bz < 16; bz++ {
					for bx := 0; bx < 16; bx++ {
						base[(bz*16+bx)*128+by] = raw[(by*16+bz)*16+bx]
					}
				}
			}
		}
		c.bases[cc] = base
	}
	if len(base) != chunkVolume {
		return 0, false
	}
	return int(base[chunkIndex(x, y, z)]), true
}
func (s *Server) directSpawnSky(r *Room, c *spawnLighting, x, y, z int) int {
	key := [2]int{x, z}
	col, ok := c.columns[key]
	if !ok {
		level := 15
		for by := 127; by >= 0; by-- {
			b, known := s.spawnLightBlock(r, c, x, by, z)
			if !known {
				level = 15
				col[by] = 15
				continue
			}
			opacity := s.spawnOpacity(b)
			if opacity >= 15 {
				level = 0
			} else if opacity > 0 || level < 15 {
				level = max(0, level-max(1, opacity))
			}
			col[by] = byte(level)
		}
		c.columns[key] = col
	}
	return int(col[min(127, max(0, y))])
}

var spawnLightDirections = [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

func (s *Server) rawSpawnSky(r *Room, c *spawnLighting, x, y, z int) int {
	if r.dimName() != "overworld" {
		return 0
	}
	best := s.directSpawnSky(r, c, x, y, z)
	if best == 15 {
		return best
	}
	queue := []spawnLightCell{{x, y, z, 0}}
	seen := map[uint64]int{blockKey(x, y, z): 0}
	for i := 0; i < len(queue); i++ {
		p := queue[i]
		best = max(best, s.directSpawnSky(r, c, p.x, p.y, p.z)-p.l)
		b, known := s.spawnLightBlock(r, c, p.x, p.y, p.z)
		if !known || s.spawnOpacity(b) >= 15 {
			continue
		}
		loss := p.l + max(1, s.spawnOpacity(b))
		if 15-loss <= best {
			continue
		}
		for _, d := range spawnLightDirections {
			nx, ny, nz := p.x+d[0], p.y+d[1], p.z+d[2]
			if !r.inside(nx, nz) || ny < 0 || ny >= 128 {
				continue
			}
			k := blockKey(nx, ny, nz)
			old, exists := seen[k]
			if exists && old <= loss {
				continue
			}
			seen[k] = loss
			queue = append(queue, spawnLightCell{nx, ny, nz, loss})
		}
	}
	return best
}
func (s *Server) spawnEmission(r *Room, b, x, y, z int) int {
	model := s.spawnModel(b)
	if isTorch(model) {
		return 14
	}
	switch model {
	case 41, 68, 95, 104, 105, 106, 107, 208, 209:
		return 15
	case 91, 92, 93, 94:
		return 13
	case 184:
		return 3
	case 189, 190:
		return 11
	case blkRedstoneLamp:
		if m := mechanicSnapshot(r, blockKey(x, y, z)); m != nil && m.Powered {
			return 15
		}
	case 109:
		v := redstoneSnapshot(r, blockKey(x, y, z))
		if v == nil || v.Powered {
			return 7
		}
	}
	return 0
}
func (s *Server) spawnChunkSources(r *Room, c *spawnLighting, cc ChunkCoord) []spawnLightCell {
	if v, ok := c.sources[cc]; ok {
		return v
	}
	out := []spawnLightCell{}
	if s.mobChunkLoaded(r, cc) {
		for z := cc.Z * 16; z < cc.Z*16+16; z++ {
			for x := cc.X * 16; x < cc.X*16+16; x++ {
				for y := 0; y < 128; y++ {
					b, known := s.spawnLightBlock(r, c, x, y, z)
					if !known {
						continue
					}
					if light := s.spawnEmission(r, b, x, y, z); light > 0 {
						out = append(out, spawnLightCell{x, y, z, light})
					}
				}
			}
		}
	}
	c.sources[cc] = out
	return out
}
func (s *Server) spawnBlockLight(r *Room, c *spawnLighting, x, y, z int) int {
	queue := []spawnLightCell{}
	for cz := floorDiv(z-15, 16); cz <= floorDiv(z+15, 16); cz++ {
		for cx := floorDiv(x-15, 16); cx <= floorDiv(x+15, 16); cx++ {
			for _, p := range s.spawnChunkSources(r, c, ChunkCoord{cx, cz}) {
				if abs(p.x-x)+abs(p.y-y)+abs(p.z-z) < p.l {
					queue = append(queue, p)
				}
			}
		}
	}
	seen := map[uint64]int{}
	best := 0
	for i := 0; i < len(queue); i++ {
		p := queue[i]
		k := blockKey(p.x, p.y, p.z)
		if seen[k] >= p.l {
			continue
		}
		seen[k] = p.l
		if p.x == x && p.y == y && p.z == z {
			best = max(best, p.l)
			continue
		}
		for _, d := range spawnLightDirections {
			nx, ny, nz := p.x+d[0], p.y+d[1], p.z+d[2]
			if abs(nx-x)+abs(ny-y)+abs(nz-z) >= p.l-1 {
				continue
			}
			b, known := s.spawnLightBlock(r, c, nx, ny, nz)
			if !known {
				continue
			}
			level := p.l - max(1, s.spawnOpacity(b))
			if level > best {
				queue = append(queue, spawnLightCell{nx, ny, nz, level})
			}
		}
	}
	return best
}
func (s *Server) spawnSkyDarken(r *Room) int {
	f := float64(r.root().Time%24000)/24000 - .25
	f -= math.Floor(f)
	angle := (f*2 + (1 - (math.Cos(f*math.Pi)+1)/2)) / 3
	daylight := clampF(math.Cos(angle*math.Pi*2)*2+.5, 0, 1)
	if s.raining(r) {
		daylight *= 1 - 5.0/16
	}
	if r.root().ext().Thunder {
		daylight *= 1 - 5.0/16
	}
	return int((1 - daylight) * 11)
}
func (s *Server) mobSkyLight(r *Room, x, y, z int) int {
	return max(0, s.rawSpawnSky(r, s.spawnLights(r), x, y, z)-s.spawnSkyDarken(r))
}
func (s *Server) blockLight(r *Room, x, y, z int) int {
	return s.spawnBlockLight(r, s.spawnLights(r), x, y, z)
}

type naturalLight struct{ sky, block, local, threshold int }

func (s *Server) naturalSpawnLight(r *Room, x, y, z int) naturalLight {
	c := s.spawnLights(r)
	sky := s.rawSpawnSky(r, c, x, y, z)
	darken := s.spawnSkyDarken(r)
	if r.root().ext().Thunder {
		darken = 10
	}
	block := s.spawnBlockLight(r, c, x, y, z)
	threshold := 7
	if r.dimName() == "end" {
		threshold = 15
	}
	return naturalLight{sky, block, max(block, max(0, sky-darken)), threshold}
}
func (s *Server) naturalMonsterLightRoll(r *Room, typ string, l naturalLight) bool {
	if typ == "blaze" {
		return true
	}
	if l.sky > s.rng.Intn(32) {
		return false
	}
	threshold := l.threshold
	if r.dimName() == "overworld" {
		threshold = s.rng.Intn(8)
	}
	return l.local <= threshold
}
