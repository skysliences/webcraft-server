package game

import "mcraft/backend/internal/worldgen"

// Surface terrain remains compatible. Themes use the existing climate fields;
// cold forests stand in for taiga until the terrain has a dedicated taiga biome.
func structureBiome(sd uint32, x, z int) int {
	h, b := terrainColumn(sd, x, z)
	return structureTheme(sd, x, z, h, b)
}

func structureTheme(sd uint32, x, z, h int, b string) int {
	if h < 54 || h > 100 || b == "ocean" || b == "mountains" {
		return -1
	}
	if b == "desert" {
		return 1
	}
	heat := fractal(sd+3741, float64(x), float64(z), 350, 3)
	wet := fractal(sd+829, float64(x), float64(z), 210, 3)
	if heat < -0.2 {
		if wet > 0.05 {
			return 3
		}
		return 4
	}
	if heat > 0.16 && wet < 0.15 {
		return 2
	}
	if b == "forest" {
		return 3
	}
	return 0
}

func (s *Server) generateStructures(r *Room, c ChunkCoord, base []byte) error {
	if r.dimName() != "overworld" || legacyChunkCoord(r, c) {
		return nil
	}
	sd := seed(r.seedID())
	type column struct{ height, theme int }
	cache := map[[2]int]column{}
	sample := func(x, z int) column {
		key := [2]int{x, z}
		if v, ok := cache[key]; ok {
			return v
		}
		v := column{-1, -1}
		if r.inside(x, z) {
			h, b := terrainColumn(sd, x, z)
			v = column{h, structureTheme(sd, x, z, h, b)}
		}
		cache[key] = v
		return v
	}
	events, err := worldgen.Generate(sd, c.X, c.Z, worldgen.Terrain{
		Height: func(x, z int) int {
			return sample(x, z).height
		},
		Biome: func(x, z int) int {
			return sample(x, z).theme
		},
		Write: func(x, y, z, b int) {
			if y > 0 && y < 127 && chunkCoord(x, z) == c {
				base[chunkIndex(x, y, z)] = byte(b)
			}
		},
	})
	if err != nil {
		return err
	}
	for _, e := range events {
		if e.Kind == 4 {
			if base[chunkIndex(e.X, e.Y, e.Z)] == blkRail {
				shape := "north_south"
				if e.Style == 1 {
					shape = "east_west"
				}
				storeRail(r, blockKey(e.X, e.Y, e.Z), &RailState{Kind: "rail", Shape: shape})
			}
			continue
		}
		if e.Kind == 3 {
			if base[chunkIndex(e.X, e.Y, e.Z)] == 0 {
				s.pendingVillageSpawns = append(s.pendingVillageSpawns, villageSpawn{r, float64(e.X) + .5, float64(e.Y), float64(e.Z) + .5})
			}
			continue
		}
		if r.Containers == nil || int(base[chunkIndex(e.X, e.Y, e.Z)]) != 140 {
			continue
		}
		k := blockKey(e.X, e.Y, e.Z)
		if r.Containers[k] != nil {
			continue
		}
		slots := make(Stacks, 27)
		// Deterministic loot, initialized once in the owning chunk snapshot.
		slots[0] = &Stack{ID: 315, Count: 2 + int(e.Seed%5)}
		slots[1] = &Stack{ID: itemBread, Count: 2 + int((e.Seed>>4)%5)}
		if e.Kind == 2 {
			slots[2] = &Stack{ID: itemRail, Count: 8 + int((e.Seed>>8)%13)}
			slots[3] = &Stack{ID: 82, Count: 4 + int((e.Seed>>16)%9)}
		} else {
			slots[2] = &Stack{ID: itemApple, Count: 2 + int((e.Seed>>8)%4)}
		}
		r.Containers[k] = &Container{Kind: "chest", Slots: slots}
	}
	return nil
}
