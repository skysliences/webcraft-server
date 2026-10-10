package game

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	api "mcraft/backend/pkg/plugin"
)

const vegetationVersion = 1

type vegetationBlock struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Z        int    `json:"z"`
	Block    int    `json:"block"`
	OldBlock int    `json:"oldBlock"`
	Biome    string `json:"biome"`
}

func (s *Server) vegetationPlan(r *Room, c ChunkCoord, base []byte) []vegetationBlock {
	out := []vegetationBlock{}
	if r.dimName() != "overworld" || len(base) != chunkVolume {
		return out
	}
	// The original engine already decorated migrated terrain. Preserve it exactly.
	if r.LegacyW > 0 && c.X >= 0 && c.Z >= 0 && c.X < r.LegacyW/16 && c.Z < r.LegacyW/16 {
		return out
	}
	sd := seed(r.seedID())
	grass, sand := s.rules.byName["Grass Block"], s.rules.byName["Sand"]
	flowers := []string{"Dandelion", "Poppy", "Cornflower", "Oxeye Daisy"}
	for z := c.Z * 16; z < c.Z*16+16; z++ {
		for x := c.X * 16; x < c.X*16+16; x++ {
			h, biome := terrainColumn(sd, x, z)
			if h <= 52 || h+1 >= 128 || !r.inside(x, z) {
				continue
			}
			ground, air := blockKey(x, h, z), blockKey(x, h+1, z)
			// An explicit air edit may be a player clearing grass. Never refill it,
			// nor grow on placed/edited ground, in a container or block state.
			if _, ok := r.Edits[ground]; ok {
				continue
			}
			if _, ok := r.Edits[air]; ok {
				continue
			}
			if _, ok := r.Authors[air]; ok {
				continue
			}
			if r.Containers[air] != nil || r.Ext != nil && r.Ext.Blocks[air] != nil {
				continue
			}
			if base[chunkIndex(x, h+1, z)] != 0 {
				continue
			}
			soil := int(base[chunkIndex(x, h, z)])
			v := terrainHash(sd+7103, x, 0, z)
			// Coordinate noise produces patches, while the hash scatters plants
			// within each patch. Neither depends on chunk order or runtime RNG.
			patch := fractal(sd+9083, float64(x), float64(z), 36, 2)
			chance := int(260 + patch*160)
			name := ""
			switch biome {
			case "plains":
				if soil != grass {
					continue
				}
				if v%1000 < 36 {
					name = flowers[(v>>16)%uint64(len(flowers))]
				} else if v%1000 < uint64(chance+130) {
					name = "Short Grass"
				}
			case "forest":
				if soil != grass {
					continue
				}
				if v%1000 < 18 {
					name = flowers[(v>>16)%uint64(len(flowers))]
				} else if v%1000 < uint64(chance) {
					name = "Short Grass"
					if (v>>24)%3 == 0 {
						name = "Fern"
					}
				}
			case "mountains":
				if soil == grass && v%1000 < 90 {
					name = "Short Grass"
				}
			case "desert":
				if soil == sand && v%1000 < 14 {
					name = "Dead Bush"
				}
			}
			if name == "" {
				continue
			}
			if b := s.rules.byName[name]; b > 0 {
				out = append(out, vegetationBlock{X: x, Y: h + 1, Z: z, Block: b, Biome: biome})
			}
		}
	}
	return out
}

func (s *Server) populateVegetation(r *Room, c ChunkCoord, v *WorldChunk, generated bool) error {
	if v.VegetationVersion >= vegetationVersion {
		return nil
	}
	plants := s.vegetationPlan(r, c, v.Base)
	reason := "vegetation_backfill"
	if generated {
		reason = "terrain_generation"
	}
	counts := map[string]int{}
	for _, plant := range plants {
		counts[s.rules.Names[plant.Block]]++
	}
	data := map[string]any{"x": c.X, "z": c.Z, "reason": reason, "generator": r.Generator, "vegetationVersion": vegetationVersion, "previousVersion": v.VegetationVersion, "count": len(plants), "blocks": plants, "counts": counts, "target": map[string]any{"kind": "chunk", "x": c.X, "z": c.Z}}
	if !s.allowed(api.ChunkPopulating, r, nil, data) {
		return nil
	}
	// Before any upgrade can be committed, retain the exact previous chunk file.
	if !generated {
		if err := s.backupVegetationChunk(r, c); err != nil {
			return fmt.Errorf("vegetation backup: %w", err)
		}
	}
	for _, plant := range plants {
		v.Base[chunkIndex(plant.X, plant.Y, plant.Z)] = byte(plant.Block)
	}
	v.Terrain = nil // A successful decoration commit changes the immutable base.
	v.VegetationVersion = vegetationVersion
	r.Dirty = true
	s.emit(api.ChunkPopulated, r, nil, data)
	return nil
}

func (s *Server) vegetationBackupFile(r *Room, c ChunkCoord) string {
	rel, _ := filepath.Rel(filepath.Join(s.config.DataDir, "worlds"), s.chunkFile(r, c))
	return filepath.Join(s.config.DataDir, "backups", "vegetation-v1", rel)
}
func (s *Server) backupVegetationChunk(r *Room, c ChunkCoord) error {
	file := s.vegetationBackupFile(r, c)
	if info, err := os.Stat(file); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("backup target is not a file")
		}
		return nil // Keep the first original; never replace an earlier backup.
	} else if !os.IsNotExist(err) {
		return err
	}
	raw, err := os.ReadFile(s.chunkFile(r, c))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	return atomicPrivateFile(file, raw)
}
