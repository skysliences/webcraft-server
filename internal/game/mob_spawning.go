package game

import (
	"fmt"
	"math"
	api "mcraft/backend/pkg/plugin"
	"sort"
)

func DefaultMobSpawningConfig() api.MobSpawningConfig {
	return api.MobSpawningConfig{GenerateChunkAnimals: true, MonsterLimit: 70, AnimalLimit: 10, SpawnRange: 6, MonsterInterval: 1, AnimalInterval: 400, AttemptsPerTick: 0, SpawnsPerTick: 0, TotalLimit: 1024}
}

func validateMobSpawningConfig(c api.MobSpawningConfig) error {
	if c.MonsterLimit < 0 || c.MonsterLimit > 1000 || c.AnimalLimit < 0 || c.AnimalLimit > 1000 || c.SpawnRange < 1 || c.SpawnRange > 8 || c.MonsterInterval < 0 || c.MonsterInterval > 12000 || c.AnimalInterval < 0 || c.AnimalInterval > 12000 || c.AttemptsPerTick < 0 || c.AttemptsPerTick > 4096 || c.SpawnsPerTick < 0 || c.SpawnsPerTick > 4096 || c.TotalLimit < 1 || c.TotalLimit > maxWorldMobs {
		return fmt.Errorf("invalid mob spawning configuration: limits 0..1000, range 1..8, intervals 0..12000, attempts/spawns 0..4096 (0 uses the upstream pass), total 1..%d", maxWorldMobs)
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
		radius := 8 // Vanilla natural-spawn ticket radius; reduced Spigot range filters attempts only.
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
		if mobCategory(m.Type) != category || !s.naturalCounted(m) {
			continue
		}
		if s.mobChunkLoaded(r, chunkCoord(int(math.Floor(m.X)), int(math.Floor(m.Z)))) {
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
	if !s.naturalSpawnCollisionFree(r, typ, float64(x)+.5, float64(y), float64(z)+.5) {
		return false
	}
	return true
}

func mobIntervalDue(serverTick uint64, interval int) bool {
	return interval > 0 && (serverTick*2)%uint64(interval) == 0
}

func (s *Server) spawnAmbientMobs(r *Room) {
	if !s.mobSpawningAllowed(r) || !s.mobCapacity(r, 1) {
		return
	}
	r.rt().spawnLights = newSpawnLighting()
	defer func() { r.rt().spawnLights = nil }()
	chunks := s.mobSpawnChunks(r)
	if len(chunks) == 0 {
		return
	}
	cfg := s.config.MobSpawning
	spawned := 0
	for step := uint64(0); step < 2; step++ {
		logicalTick := s.tick*2 + step
		for _, category := range []string{"monster", "animal"} {
			interval, base := cfg.MonsterInterval, cfg.MonsterLimit
			if category == "animal" {
				interval, base = cfg.AnimalInterval, cfg.AnimalLimit
			}
			if interval == 0 || base == 0 || logicalTick%uint64(interval) != 0 || category == "monster" && s.difficulty(r) == "peaceful" {
				continue
			}
			limit := base * len(chunks) / 289
			count := s.naturalMobCount(r, category, nil)
			if count >= limit {
				continue
			}
			order := append([]ChunkCoord(nil), chunks...)
			s.rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			attempts := 0
			for _, c := range order {
				if count >= limit || !s.mobCapacity(r, 1) || cfg.AttemptsPerTick > 0 && attempts >= cfg.AttemptsPerTick || cfg.SpawnsPerTick > 0 && spawned >= cfg.SpawnsPerTick {
					break
				}
				// Spigot filters chunks by distance from the chunk centre, independently
				// from the 3D exclusion/despawn sphere checked at the candidate position.
				nearby := false
				for p := range r.Peers {
					if s.spawningPlayer(p) {
						radius := min(cfg.SpawnRange, min(8, p.rd)) * 16
						dx, dz := p.state.X-float64(c.X*16+8), p.state.Z-float64(c.Z*16+8)
						if dx*dx+dz*dz < float64(radius*radius) {
							nearby = true
							break
						}
					}
				}
				if !nearby {
					continue
				}
				attempts++
				x, z := c.X*16+s.rng.Intn(16), c.Z*16+s.rng.Intn(16)
				top := s.naturalColumnTop(r, x, z)
				if top < 1 {
					continue
				}
				y := s.rng.Intn(top + 1)
				if y < 1 {
					continue
				}
				groupSpawned := 0
				for group := 0; group < 3; group++ {
					px, pz := x, z
					entry, chosen := naturalSpawnEntry{}, false
					packSize := int(math.Ceil(s.rng.Float64() * 4))
					for pack := 0; pack < packSize; pack++ {
						px += s.rng.Intn(6) - s.rng.Intn(6)
						pz += s.rng.Intn(6) - s.rng.Intn(6)
						if !s.mobChunkLoaded(r, chunkCoord(px, pz)) {
							continue
						}
						if !chosen {
							var supported bool
							entry, supported = s.selectNaturalEntry(r, category, px, y, pz)
							if !supported {
								break
							}
							chosen = true
							packSize = entry.Min + s.rng.Intn(entry.Max-entry.Min+1)
						}
						if count >= limit || cfg.SpawnsPerTick > 0 && spawned >= cfg.SpawnsPerTick || groupSpawned >= 4 || !s.mobCapacity(r, 1) {
							break
						}
						if !s.naturalLocalCap(r, category, px, pz) || !s.naturalTypeAllowed(r, entry.Type, px, y, pz) || !s.naturalSpawnPosition(r, entry.Type, px, y, pz) || s.naturalWorldSpawnDistance(r, float64(px)+.5, float64(y), float64(pz)+.5) <= 24 {
							continue
						}
						light := s.naturalSpawnLight(r, px, y, pz)
						if category == "monster" && !s.naturalMonsterLightRoll(r, entry.Type, light) {
							continue
						}
						context := s.naturalContext(r, px, y, pz, limit, count, len(chunks), logicalTick)
						m, err := s.prepareMob(r, nil, entry.Type, float64(px)+.5, float64(y), float64(pz)+.5, "natural", context)
						if err != nil {
							continue
						}
						s.publishMob(r, nil, m, "natural", context)
						count++
						spawned++
						groupSpawned++
					}
					if groupSpawned >= 4 {
						break
					}
				}
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
	} else if m.InactiveTicks > 600 && (s.rng.Intn(800) == 0 || s.rng.Intn(800) == 0) {
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
