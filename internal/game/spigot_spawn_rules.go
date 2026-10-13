package game

import (
	_ "embed"
	"encoding/json"
	"math"
)

// Data extracted from Mojang 26.3's official biome spawning attributes.
// Unsupported entities stay in the lottery and cannot masquerade as another mob.
//
//go:embed spigot_spawn_tables.json
var spigotSpawnTablesJSON []byte
var spigotSpawnTables = func() map[string]map[string][]naturalSpawnEntry {
	var out map[string]map[string][]naturalSpawnEntry
	if json.Unmarshal(spigotSpawnTablesJSON, &out) != nil {
		panic("invalid natural spawn tables")
	}
	return out
}()

type naturalSpawnEntry struct {
	Type   string `json:"type"`
	Weight int    `json:"weight"`
	Min    int    `json:"min"`
	Max    int    `json:"max"`
}

func (s *Server) naturalBiome(r *Room, x, z int) string {
	if r.dimName() == "end" {
		return "the_end"
	}
	if r.dimName() == "nether" {
		return "nether_wastes"
	}
	if !r.Infinite || legacyChunkCoord(r, chunkCoord(x, z)) {
		s.engine.use(r)
		if s.engine.mod.ExportedFunction("overworld_biome") != nil {
			id := int(int32(s.engine.call("overworld_biome", uint64(int64(x)), uint64(int64(z)))))
			names := []string{"plains", "forest", "savanna", "snowy_plains", "desert", "beach", "ocean", "windswept_hills"}
			if id >= 0 && id < len(names) {
				return names[id]
			}
		}
		return "plains"
	}
	_, biome := terrainColumn(seed(r.seedID()), x, z)
	if biome == "mountains" {
		return "windswept_hills"
	}
	return biome
}
func (s *Server) naturalFortress(r *Room, x, y, z int) bool {
	if r.dimName() != "nether" {
		return false
	}
	if r.Infinite && !legacyChunkCoord(r, chunkCoord(x, z)) {
		bx, bz := (x%128+128)%128, (z%128+128)%128
		return y >= 53 && y <= 57 && ((bz >= 62 && bz <= 66) || (bx >= 62 && bx <= 66) || (bx >= 58 && bx <= 70 && bz >= 58 && bz <= 70))
	}
	s.engine.use(r)
	return int32(s.engine.call("fortress_at", uint64(int64(x)), uint64(y), uint64(int64(z)))) >= 0
}
func (s *Server) naturalEntries(r *Room, category string, x, y, z int) []naturalSpawnEntry {
	if category == "monster" && s.naturalFortress(r, x, y, z) {
		return spigotSpawnTables["nether_fortress"][category]
	}
	return spigotSpawnTables[s.naturalBiome(r, x, z)][category]
}
func (s *Server) naturalTypeAllowed(r *Room, typ string, x, y, z int) bool {
	for _, e := range s.naturalEntries(r, mobCategory(typ), x, y, z) {
		if e.Type == typ {
			return true
		}
	}
	return false
}
func (s *Server) selectNaturalEntry(r *Room, category string, x, y, z int) (naturalSpawnEntry, bool) {
	entries := s.naturalEntries(r, category, x, y, z)
	total := 0
	for _, e := range entries {
		total += e.Weight
	}
	if total == 0 {
		return naturalSpawnEntry{}, false
	}
	choice := s.rng.Intn(total)
	for _, e := range entries {
		choice -= e.Weight
		if choice < 0 {
			_, supported := mobSpecs[e.Type]
			return e, supported
		}
	}
	return naturalSpawnEntry{}, false
}
func (s *Server) naturalCounted(m *Mob) bool {
	return m != nil && !m.Persistent && mobCategory(m.Type) != "misc"
}
func (s *Server) naturalLocalCap(r *Room, category string, x, z int) bool {
	// Vanilla's per-player cap keeps one player's full farm from excluding
	// another player's otherwise eligible chunks. Bukkit changes the global cap.
	limit := 70
	if category == "animal" {
		limit = 10
	}
	for p := range r.Peers {
		if !s.spawningPlayer(p) {
			continue
		}
		dx, dz := p.state.X-float64(floorDiv(x, 16)*16+8), p.state.Z-float64(floorDiv(z, 16)*16+8)
		if dx*dx+dz*dz >= 128*128 {
			continue
		}
		count := 0
		for _, m := range r.Mobs {
			if !s.naturalCounted(m) || mobCategory(m.Type) != category || !s.mobChunkLoaded(r, chunkCoord(int(math.Floor(m.X)), int(math.Floor(m.Z)))) {
				continue
			}
			mx, mz := float64(floorDiv(int(math.Floor(m.X)), 16)*16+8)-p.state.X, float64(floorDiv(int(math.Floor(m.Z)), 16)*16+8)-p.state.Z
			if mx*mx+mz*mz < 128*128 {
				count++
			}
		}
		if count < limit {
			return true
		}
	}
	return false
}
func (s *Server) naturalColumnTop(r *Room, x, z int) int {
	c := s.spawnLights(r)
	for y := 127; y >= 0; y-- {
		b, ok := s.spawnLightBlock(r, c, x, y, z)
		if !ok {
			return 0
		}
		if b != 0 {
			return min(126, y+1)
		}
	}
	return 0
}
func (s *Server) naturalWorldSpawnDistance(r *Room, x, y, z float64) float64 {
	if r.naturalSpawnOrigin == nil {
		sp := s.spawnPoint(r)
		r.naturalSpawnOrigin = &sp
	}
	sp := *r.naturalSpawnOrigin
	return math.Sqrt(math.Pow(x-float64(sp[0]), 2) + math.Pow(y-float64(sp[1]), 2) + math.Pow(z-float64(sp[2]), 2))
}
func (s *Server) naturalContext(r *Room, x, y, z int, limit, count, chunks int, tick uint64) map[string]any {
	light := s.naturalSpawnLight(r, x, y, z)
	return map[string]any{"categoryLimit": limit, "categoryCount": count, "eligibleChunks": chunks, "biome": s.naturalBiome(r, x, z), "skyLight": light.sky, "blockLight": light.block, "localLight": light.local, "minecraftTick": tick, "spawningRules": "spigot-26.3"}
}

func (s *Server) spawnChunkAnimals(r *Room, c ChunkCoord) {
	if s.config.MobSpawning == nil || s.rng == nil {
		return
	}
	if !s.config.MobSpawning.GenerateChunkAnimals || r.dimName() != "overworld" || !s.mobSpawningAllowed(r) || !s.mobCapacity(r, 1) {
		return
	}
	previous := r.rt().spawnLights
	r.rt().spawnLights = newSpawnLighting()
	defer func() { r.rt().spawnLights = previous }()
	for s.rng.Float64() < .1 {
		x, z := c.X*16+1+s.rng.Intn(14), c.Z*16+1+s.rng.Intn(14)
		y := s.surface(r, x, z)
		entry, supported := s.selectNaturalEntry(r, "animal", x, y, z)
		if !supported {
			continue
		}
		size := entry.Min + s.rng.Intn(entry.Max-entry.Min+1)
		for member := 0; member < size && s.mobCapacity(r, 1); member++ {
			for attempt := 0; attempt < 4; attempt++ {
				x = c.X*16 + min(14, max(1, x-c.X*16+s.rng.Intn(5)-s.rng.Intn(5)))
				z = c.Z*16 + min(14, max(1, z-c.Z*16+s.rng.Intn(5)-s.rng.Intn(5)))
				y = s.surface(r, x, z)
				if !s.mobAreaLoaded(r, entry.Type, float64(x)+.5, float64(z)+.5) || !s.canNaturalSpawn(r, entry.Type, x, y, z) || !s.naturalTypeAllowed(r, entry.Type, x, y, z) || !s.naturalSpawnCollisionFree(r, entry.Type, float64(x)+.5, float64(y), float64(z)+.5) {
					continue
				}
				context := s.naturalContext(r, x, y, z, 0, 0, 1, s.tick*2)
				context["source"] = "chunk_generation"
				m, err := s.prepareMob(r, nil, entry.Type, float64(x)+.5, float64(y), float64(z)+.5, "chunk_generation", context)
				if err == nil && s.canNaturalSpawn(r, entry.Type, x, y, z) && s.naturalTypeAllowed(r, entry.Type, x, y, z) && s.naturalSpawnCollisionFree(r, entry.Type, float64(x)+.5, float64(y), float64(z)+.5) {
					s.publishMob(r, nil, m, "chunk_generation", context)
					break
				}
			}
		}
	}
}
func (s *Server) naturalSpawnCollisionFree(r *Room, typ string, x, y, z float64) bool {
	spec := mobSpecs[typ]
	for _, m := range r.Mobs {
		other := mobSpecs[m.Type]
		width := (spec.Width + other.Width) / 2
		if y < m.Y+other.Height && y+spec.Height > m.Y && math.Abs(x-m.X) < width && math.Abs(z-m.Z) < width {
			return false
		}
	}
	for p := range r.Peers {
		if p.state == nil || p.state.Dead || s.isSpectator(p) {
			continue
		}
		width := (spec.Width + .6) / 2
		if y < p.state.Y+1.8 && y+spec.Height > p.state.Y && math.Abs(x-p.state.X) < width && math.Abs(z-p.state.Z) < width {
			return false
		}
	}
	return true
}
