package game

import (
	"fmt"
	"math"
	api "mcraft/backend/pkg/plugin"
	"sort"
)

func DefaultMobSpawningConfig() api.MobSpawningConfig {
	return api.MobSpawningConfig{MonsterLimit: 70, AnimalLimit: 10, SpawnRange: 6, MonsterInterval: 1, AnimalInterval: 400, AttemptsPerTick: 32, SpawnsPerTick: 4, TotalLimit: 1024}
}

func validateMobSpawningConfig(c api.MobSpawningConfig) error {
	if c.MonsterLimit < 0 || c.MonsterLimit > 1000 || c.AnimalLimit < 0 || c.AnimalLimit > 1000 || c.SpawnRange < 1 || c.SpawnRange > 8 || c.MonsterInterval < 0 || c.MonsterInterval > 12000 || c.AnimalInterval < 0 || c.AnimalInterval > 12000 || c.AttemptsPerTick < 1 || c.AttemptsPerTick > 128 || c.SpawnsPerTick < 1 || c.SpawnsPerTick > 32 || c.TotalLimit < 1 || c.TotalLimit > maxWorldMobs {
		return fmt.Errorf("invalid mob spawning configuration: limits 0..1000, range 1..8, intervals 0..12000, attempts 1..128, spawns 1..32, total 1..%d", maxWorldMobs)
	}
	return nil
}

func (s *Server) mobCapacity(r *Room, extra int) bool {
	return r != nil && extra >= 0 && len(r.Mobs)+extra <= s.config.MobSpawning.TotalLimit
}

func mobCategory(typ string) string {
	switch typ {
	case "cow", "pig", "sheep", "chicken", "wolf":
		return "animal"
	case "villager", "ender_dragon", "end_crystal", "wither", "shulker", "boat", "minecart", "fishing_bobber":
		return "misc"
	}
	if mobSpecs[typ].Hostile {
		return "monster"
	}
	return "misc"
}

func mobPersistent(m *Mob) bool {
	return m.Persistent || !mobSpecs[m.Type].Hostile || mobCategory(m.Type) == "misc"
}

func (s *Server) spawningPlayer(p *Peer) bool {
	return p != nil && p.state != nil && !p.closed && !p.state.Dead && !s.isSpectator(p)
}

// Only committed chunks are eligible. Spawning never generates terrain or
// loads old files merely to expand its budget. Overlapping player areas merge.
func (s *Server) mobChunkLoaded(r *Room, c ChunkCoord) bool {
	if !r.inside(c.X*16, c.Z*16) || !r.inside(c.X*16+15, c.Z*16+15) {
		return false
	}
	return !r.Infinite || r.chunks[c] != nil || legacyChunkCoord(r, c)
}

func (s *Server) mobSpawnChunks(r *Room) []ChunkCoord {
	seen := map[ChunkCoord]bool{}
	for p := range r.Peers {
		if !s.spawningPlayer(p) {
			continue
		}
		radius := min(s.config.MobSpawning.SpawnRange, max(0, p.rd))
		cx, cz := floorDiv(int(math.Floor(p.state.X)), 16), floorDiv(int(math.Floor(p.state.Z)), 16)
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				c := ChunkCoord{X: cx + dx, Z: cz + dz}
				if s.mobChunkLoaded(r, c) {
					seen[c] = true
				}
			}
		}
	}
	chunks := make([]ChunkCoord, 0, len(seen))
	for c := range seen {
		chunks = append(chunks, c)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].X == chunks[j].X {
			return chunks[i].Z < chunks[j].Z
		}
		return chunks[i].X < chunks[j].X
	})
	return chunks
}

func (s *Server) naturalMobCount(r *Room, category string, active map[ChunkCoord]bool) int {
	count := 0
	for _, m := range r.Mobs {
		if mobCategory(m.Type) != category || category == "monster" && mobPersistent(m) {
			continue
		}
		if active[chunkCoord(int(math.Floor(m.X)), int(math.Floor(m.Z)))] {
			count++
		}
	}
	return count
}

func (s *Server) nearestMobPlayer(r *Room, x, y, z float64) (*Peer, float64) {
	var nearest *Peer
	distance := math.Inf(1)
	for p := range r.Peers {
		if !s.spawningPlayer(p) {
			continue
		}
		dx, dy, dz := p.state.X-x, p.state.Y-y, p.state.Z-z
		d := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if d < distance {
			nearest, distance = p, d
		}
	}
	return nearest, distance
}

func (s *Server) mobAreaLoaded(r *Room, typ string, x, z float64) bool {
	w := mobSpecs[typ].Width / 2
	for _, dx := range []float64{-w, w} {
		for _, dz := range []float64{-w, w} {
			if !s.mobChunkLoaded(r, chunkCoord(int(math.Floor(x+dx)), int(math.Floor(z+dz)))) {
				return false
			}
		}
	}
	return true
}
func (s *Server) mobActive(r *Room, m *Mob) bool {
	if m == nil || !s.mobAreaLoaded(r, m.Type, m.X, m.Z) {
		return false
	}
	_, distance := s.nearestMobPlayer(r, m.X, m.Y, m.Z)
	return distance <= 128
}

// A small skylight model for spawn checks: roofs block daylight and leaves
// attenuate it. Client brightness settings do not affect server spawn rules.
func (s *Server) mobSkyLight(r *Room, x, y, z int) int {
	if r.dimName() != "overworld" {
		return 0
	}
	light := 15
	for by := y; by < 128; by++ {
		kind := s.kind(s.block(r, x, by, z))
		if kind == 1 || kind == 3 {
			return 0
		}
		if kind == 2 || kind == 4 || kind == 6 {
			light = max(0, light-1)
		}
	}
	if isNight(r.root().Time) {
		light = max(0, light-11)
	}
	return light
}

func (s *Server) naturalSpawnPosition(r *Room, typ string, x, y, z int) bool {
	if !s.mobSpawningAllowed(r) || mobSpecs[typ].Hostile && s.difficulty(r) == "peaceful" || !s.mobAreaLoaded(r, typ, float64(x)+.5, float64(z)+.5) {
		return false
	}
	_, distance := s.nearestMobPlayer(r, float64(x)+.5, float64(y), float64(z)+.5)
	if distance <= 24 || distance > 128 {
		return false
	}
	if !s.canNaturalSpawn(r, typ, x, y, z) {
		return false
	}
	for _, m := range r.Mobs {
		width := (mobSpecs[typ].Width + mobSpecs[m.Type].Width) / 2
		if math.Abs(m.Y-float64(y)) < mobSpecs[typ].Height && math.Hypot(m.X-float64(x)-.5, m.Z-float64(z)-.5) < width {
			return false
		}
	}
	return true
}

func mobIntervalDue(serverTick uint64, interval int) bool {
	// The host runs at 10 Hz; one host tick advances two Minecraft ticks.
	return interval > 0 && serverTick%uint64((interval+1)/2) == 0
}

func (s *Server) spawnAmbientMobs(r *Room) {
	if !s.mobSpawningAllowed(r) || !s.mobCapacity(r, 1) {
		return
	}
	cfg := s.config.MobSpawning
	animalDue := cfg.AnimalLimit > 0 && r.dimName() == "overworld" && mobIntervalDue(s.tick, cfg.AnimalInterval)
	monsterDue := cfg.MonsterLimit > 0 && s.difficulty(r) != "peaceful" && mobIntervalDue(s.tick, cfg.MonsterInterval)
	if !animalDue && !monsterDue {
		return
	}
	chunks := s.mobSpawnChunks(r)
	if len(chunks) == 0 {
		return
	}
	active := map[ChunkCoord]bool{}
	for _, c := range chunks {
		active[c] = true
	}
	if r.naturalSpawnOrigin == nil {
		spawn := s.spawnPoint(r)
		r.naturalSpawnOrigin = &spawn
	}
	worldSpawn := *r.naturalSpawnOrigin
	spawned := 0
	for _, category := range []string{"animal", "monster"} {
		interval, base := cfg.MonsterInterval, cfg.MonsterLimit
		if category == "animal" {
			interval, base = cfg.AnimalInterval, cfg.AnimalLimit
		}
		if !mobIntervalDue(s.tick, interval) || base == 0 || category == "monster" && s.difficulty(r) == "peaceful" {
			continue
		}
		limit := base * len(chunks) / 289
		count := s.naturalMobCount(r, category, active)
		if count >= limit {
			continue
		}
		types := []string{"cow", "pig", "sheep", "chicken"}
		if category == "monster" {
			switch r.dimName() {
			case "nether":
				types = []string{"blaze", "wither_skeleton"}
			case "end":
				types = []string{"enderman"}
			default:
				types = []string{"zombie", "spider", "skeleton", "creeper"}
			}
		} else if r.dimName() != "overworld" {
			continue
		}
		// Each category gets a bounded attempt budget; total successes are shared.
		for n := 0; n < cfg.AttemptsPerTick && spawned < cfg.SpawnsPerTick && count < limit && s.mobCapacity(r, 1); n++ {
			c := chunks[s.rng.Intn(len(chunks))]
			x, z := c.X*16+s.rng.Intn(16), c.Z*16+s.rng.Intn(16)
			y := 0
			if category == "monster" {
				// Sample the column rather than just its top: caves and indoor floors.
				y = 1 + s.rng.Intn(126)
			} else {
				y = s.surface(r, x, z)
			}
			typ := types[s.rng.Intn(len(types))]
			for pack := 0; pack < 4 && spawned < cfg.SpawnsPerTick && count < limit && s.mobCapacity(r, 1); pack++ {
				px, pz := x, z
				if pack > 0 {
					px += s.rng.Intn(5) - 2
					pz += s.rng.Intn(5) - 2
				}
				if !active[chunkCoord(px, pz)] || !s.naturalSpawnPosition(r, typ, px, y, pz) {
					continue
				}
				if math.Sqrt(math.Pow(float64(px)+.5-float64(worldSpawn[0]), 2)+math.Pow(float64(y-worldSpawn[1]), 2)+math.Pow(float64(pz)+.5-float64(worldSpawn[2]), 2)) <= 24 {
					continue
				}
				context := map[string]any{"categoryLimit": limit, "categoryCount": count, "eligibleChunks": len(chunks)}
				m, err := s.prepareMob(r, nil, typ, float64(px)+.5, float64(y), float64(pz)+.5, "natural", context)
				if err != nil {
					continue
				}
				s.publishMob(r, nil, m, "natural", context)
				count++
				spawned++
			}
		}
	}
}

func (s *Server) despawnMob(r *Room, m *Mob, distance float64) bool {
	if mobPersistent(m) {
		m.InactiveTicks = 0
		return false
	}
	if math.IsInf(distance, 1) {
		return false
	} // no active player: pause, never erase a world
	if distance <= 32 {
		m.InactiveTicks = 0
		return false
	}
	m.InactiveTicks = min(m.InactiveTicks+2, 1200000)
	reason := ""
	if distance > 128 {
		reason = "distance"
	} else if m.InactiveTicks > 600 && s.rng.Intn(400) == 0 {
		reason = "random_distance"
	}
	if reason == "" {
		return false
	}
	data := mobData(m, reason)
	data["nearestPlayerDistance"] = distance
	if !s.removeMobWithData(r, m, data) {
		m.InactiveTicks = 0
		return false
	}
	return true
}
