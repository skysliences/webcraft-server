package game

import "math"

// Coordinates are signed. Like Minecraft, a finite numerical border protects
// physics and persistence; terrain is generated lazily throughout that space.
const worldBorder = 30000000
const chunkVolume = 16 * 16 * 128
const generatorVersion = "noise-v1"

type ChunkCoord struct{ X, Z int }

func chunkCoord(x, z int) ChunkCoord { return ChunkCoord{floorDiv(x, 16), floorDiv(z, 16)} }
func floorDiv(n, d int) int {
	q := n / d
	if n%d < 0 {
		q--
	}
	return q
}
func chunkIndex(x, y, z int) int { return ((z&15)*16+(x&15))*128 + y }

func terrainHash(seed uint32, x, y, z int) uint64 {
	h := uint64(seed) ^ uint64(int64(x))*0x9e3779b185ebca87 ^ uint64(int64(y))*0xc2b2ae3d27d4eb4f ^ uint64(int64(z))*0x165667b19e3779f9
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	return h ^ (h >> 31)
}
func noise(s uint32, x, y, z float64) float64 {
	ix, iy, iz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	fade := func(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }
	fx, fy, fz := fade(x-float64(ix)), fade(y-float64(iy)), fade(z-float64(iz))
	v := func(dx, dy, dz int) float64 {
		return float64(terrainHash(s, ix+dx, iy+dy, iz+dz)>>11)/float64(uint64(1)<<53)*2 - 1
	}
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
	return lerp(lerp(lerp(v(0, 0, 0), v(1, 0, 0), fx), lerp(v(0, 1, 0), v(1, 1, 0), fx), fy), lerp(lerp(v(0, 0, 1), v(1, 0, 1), fx), lerp(v(0, 1, 1), v(1, 1, 1), fx), fy), fz)
}
func fractal(s uint32, x, z, scale float64, octaves int) float64 {
	sum, weight, amp := 0.0, 0.0, 1.0
	for i := 0; i < octaves; i++ {
		sum += noise(s+uint32(i)*7919, x/scale, 0, z/scale) * amp
		weight += amp
		scale /= 2
		amp /= 2
	}
	return sum / weight
}
func terrainColumn(s uint32, x, z int) (int, string) {
	continental := fractal(s, float64(x), float64(z), 450, 4)
	ridge := math.Abs(fractal(s+531, float64(x), float64(z), 140, 3))
	h := int(58 + continental*34 + ridge*22 + fractal(s+999, float64(x), float64(z), 42, 3)*5)
	h = max(8, min(115, h))
	if h < 51 {
		return h, "ocean"
	}
	if h > 83 {
		return h, "mountains"
	}
	heat := fractal(s+3741, float64(x), float64(z), 350, 3)
	wet := fractal(s+829, float64(x), float64(z), 210, 3)
	if heat > 0.23 && wet < 0.08 {
		return h, "desert"
	}
	if wet > 0.05 {
		return h, "forest"
	}
	return h, "plains"
}
func (s *Server) generateTerrainBase(r *Room, c ChunkCoord) []byte {
	out := make([]byte, chunkVolume)
	sd := seed(r.seedID())
	id := func(name string) byte {
		aliases := map[string]string{"Netherrack": "Underrock", "End Stone": "Void Stone"}
		if alias := aliases[name]; alias != "" {
			name = alias
		}
		return byte(s.rules.byName[name])
	}
	stone, dirt, grass, sand, water, bedrock := id("Stone"), id("Dirt"), id("Grass Block"), id("Sand"), id("Water"), id("Bedrock")
	if grass == 0 {
		grass = id("Grass")
	}
	set := func(x, y, z int, b byte) {
		if y >= 0 && y < 128 && floorDiv(x, 16) == c.X && floorDiv(z, 16) == c.Z {
			out[chunkIndex(x, y, z)] = b
		}
	}
	for z := c.Z * 16; z < c.Z*16+16; z++ {
		for x := c.X * 16; x < c.X*16+16; x++ {
			h, biome := terrainColumn(sd, x, z)
			if r.dimName() == "end" {
				d := math.Hypot(float64(x-512), float64(z-512))
				islandNoise := noise(sd, float64(x)/75, 0, float64(z)/75)
				// 主岛生成 (Main End Island, 有效半径 125，自然覆盖 x=612 黑曜石出生平台)
				if d <= 125 {
					// 顶部平缓起伏，中心高度约 64~68
					topY := 64 + int(islandNoise*6) - int(math.Pow(d/125.0, 2.5)*8)
					// 底部呈倒圆锥向中心深处收拢，中心最深可达 y=36，边缘向上变薄汇合
					bottomY := 36 + int(math.Pow(d/120.0, 1.8)*28)
					if topY >= bottomY {
						for y := max(1, bottomY); y <= min(120, topY); y++ {
							set(x, y, z, id("End Stone"))
						}
					}
				} else if d > 800 && islandNoise > 0.25 {
					// 外岛群岛 (Outer Islands)
					topY := 58 + int(islandNoise*14)
					bottomY := 45 - int(islandNoise*5)
					for y := bottomY; y <= topY; y++ {
						set(x, y, z, id("End Stone"))
					}
				}
				continue
			}
			if r.dimName() == "nether" {
				// 下界要塞生成判定 (Nether Fortress)
				bx := (x%128 + 128) % 128
				bz := (z%128 + 128) % 128
				isBridge := (bz >= 62 && bz <= 66) || (bx >= 62 && bx <= 66)
				isHall := bx >= 58 && bx <= 70 && bz >= 58 && bz <= 70
				netherBricks := id("Under Bricks")
				if netherBricks == 0 {
					netherBricks = 183
				}

				for y := 0; y < 128; y++ {
					b := id("Netherrack")
					if y == 0 || y == 127 {
						b = bedrock
					} else if y > 25 && y < 115 && noise(sd+811, float64(x)/30, float64(y)/18, float64(z)/30) > -0.12 {
						b = 0
						if y < 32 {
							b = id("Lava")
						}
					}

					// 要塞结构覆盖
					if isBridge || isHall {
						// 桥面在 y = 52
						if y == 52 {
							b = netherBricks
						} else if y > 52 && y <= 56 {
							// 走廊内部通道挖空
							b = 0
							// 桥两侧护栏
							if !isHall && (bz == 62 || bz == 66 || bx == 62 || bx == 66) && y == 53 {
								b = netherBricks
							}
							// 要塞核心大厅刷怪笼与地狱疣
							if isHall {
								if bx == 64 && bz == 64 && y == 53 {
									b = 188 // Mob Cage (Ember Wisp / Blaze)
								} else if (bx == 60 || bx == 68) && (bz == 60 || bz == 68) && y == 53 {
									b = 181 // Grave Sand / Soul Sand
								} else if (bx == 60 || bx == 68) && (bz == 60 || bz == 68) && y == 54 {
									b = 185 // Under Wart
								}
							}
						} else if y < 52 && y >= 32 {
							// 桥墩直插岩浆海 (每隔 16 格一根粗立柱)
							if (bx == 64 || bz == 64) && (bx%16 == 0 || bz%16 == 0) {
								b = netherBricks
							}
						}
					}

					set(x, y, z, b)
				}
				continue
			}
			for y := 0; y <= max(h, 52); y++ {
				b := stone
				switch {
				case y == 0:
					b = bedrock
				case y > h:
					b = water
				case y == h:
					if biome == "desert" || biome == "ocean" {
						b = sand
					} else {
						b = grass
					}
				case y > h-4:
					if biome == "desert" || biome == "ocean" {
						b = sand
					} else {
						b = dirt
					}
				default:
					// Two low-frequency density fields form connected underground caves.
					if y > 5 && y < h-5 && math.Abs(noise(sd+19, float64(x)/27, float64(y)/19, float64(z)/27)) < 0.075 && noise(sd+43, float64(x)/41, float64(y)/25, float64(z)/41) > -0.15 {
						b = 0
					}
					if b == stone {
						v := terrainHash(sd+101, x, y, z) % 1000
						switch {
						case y < 18 && v < 7:
							b = id("Diamond Ore")
						case y < 32 && v < 18:
							b = id("Gold Ore")
						case y < 62 && v < 38:
							b = id("Iron Ore")
						case v < 62:
							b = id("Coal Ore")
						}
					}
				}
				set(x, y, z, b)
			}
		}
	}
	if r.dimName() == "overworld" {
		// Roots outside this chunk contribute their canopy too, preventing seams.
		for z := c.Z*16 - 3; z < c.Z*16+19; z++ {
			for x := c.X*16 - 3; x < c.X*16+19; x++ {
				h, b := terrainColumn(sd, x, z)
				v := terrainHash(sd+330, x, 0, z)
				threshold := uint64(2)
				if b == "forest" {
					threshold = 12
				}
				if h <= 53 || h > 100 || b == "desert" || b == "ocean" || v%1000 >= threshold {
					continue
				}
				th := 4 + int((v>>16)%3)
				for y := h + 1; y <= h+th; y++ {
					set(x, y, z, id("Oak Log"))
				}
				for dz := -2; dz <= 2; dz++ {
					for dx := -2; dx <= 2; dx++ {
						for dy := -1; dy <= 1; dy++ {
							if abs(dx)+abs(dz)+abs(dy) < 5 && !(dx == 0 && dz == 0 && dy <= 0) {
								set(x+dx, h+th+dy, z+dz, id("Oak Leaves"))
							}
						}
					}
				}
			}
		}
	}
	s.maybeGenerateVillage(r, c, set, id)
	return out
}

// Pure generation is deterministic; live chunks apply the same vegetation plan
// through the cancellable population transaction before they are streamed.
func (s *Server) generateTerrain(r *Room, c ChunkCoord) []byte {
	out := s.generateTerrainBase(r, c)
	for _, plant := range s.vegetationPlan(r, c, out) {
		out[chunkIndex(plant.X, plant.Y, plant.Z)] = byte(plant.Block)
	}
	return out
}
