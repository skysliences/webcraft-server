package game

import (
	"fmt"
	"math/rand"
	api "mcraft/backend/pkg/plugin"
	"testing"
)

func TestSpigotLightContract(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := mobTestWorld(s)
	r.Time = 6000
	for _, roof := range []int{0, 10, 7, 1} {
		r.Edits[blockKey(12, 63, 10)] = roof
		if l := s.mobSkyLight(r, 12, 60, 10); l < 14 {
			t.Fatal("open roof lost side/transparent light", roof, l)
		}
		for _, typ := range []string{"skeleton", "zombie", "spider", "creeper", "enderman"} {
			if s.canNaturalSpawn(r, typ, 12, 60, 10) {
				t.Fatal("sunlit hostile spawn", roof, typ)
			}
		}
	}
	for x := 0; x <= 40; x++ {
		for z := 0; z <= 30; z++ {
			r.Edits[blockKey(x, 63, z)] = 1
		}
	}
	if s.mobSkyLight(r, 12, 60, 10) != 0 || !s.canNaturalSpawn(r, "skeleton", 12, 60, 10) {
		t.Fatal("real dark cave must spawn during daytime")
	}
	r.Edits[blockKey(12, 61, 10)] = 10
	if s.canNaturalSpawn(r, "skeleton", 12, 60, 10) {
		t.Fatal("transparent collision ignored")
	}
}
func TestSpigotGeneratedAndDynamicLighting(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("lightfixture0001", "light", "survival", 0)
	r.Infinite = true
	base := make([]byte, chunkVolume)
	base[chunkIndex(8, 60, 8)] = 82
	r.chunks = map[ChunkCoord]*WorldChunk{{0, 0}: {Base: base}}
	if got := s.blockLight(r, 9, 60, 8); got != 13 || len(r.chunks) != 1 {
		t.Fatal("base torch missing or loaded neighbour", got)
	}
	r.Edits[blockKey(8, 60, 8)] = 0
	if s.blockLight(r, 9, 60, 8) != 0 {
		t.Fatal("removed source left light")
	}
	r.Edits[blockKey(8, 60, 8)] = blkRedstoneLamp
	storeMechanic(r, blockKey(8, 60, 8), &MechanicState{Kind: "lamp", Powered: true, Power: 15})
	if s.blockLight(r, 9, 60, 8) != 14 {
		t.Fatal("powered lamp omitted")
	}
	storeMechanic(r, blockKey(8, 60, 8), &MechanicState{Kind: "lamp"})
	if s.blockLight(r, 9, 60, 8) != 0 {
		t.Fatal("unpowered lamp emitting")
	}
}
func TestSpigotRandomLightingAndCaps(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	s.rng = rand.New(rand.NewSource(9))
	for i := 0; i < 1000; i++ {
		if s.naturalMonsterLightRoll(r, "skeleton", naturalLight{15, 0, 15, 7}) {
			t.Fatal("sunlight random gate passed")
		}
	}
	accepted := 0
	for i := 0; i < 10000; i++ {
		if s.naturalMonsterLightRoll(r, "skeleton", naturalLight{15, 0, 4, 7}) {
			accepted++
		}
	}
	if accepted < 2400 || accepted > 2900 {
		t.Fatal("wrong upstream random distribution", accepted)
	}
	r.Time = 18000
	r.Edits[blockKey(12, 60, 11)] = 109
	if s.canNaturalSpawn(r, "skeleton", 12, 60, 10) {
		t.Fatal("Overworld block light limit")
	}
	r.Dim = "nether"
	if !s.canNaturalSpawn(r, "wither_skeleton", 12, 60, 10) {
		t.Fatal("Nether block light limit")
	}
	r.Dim = "end"
	if s.canNaturalSpawn(r, "enderman", 12, 60, 10) {
		t.Fatal("End block light limit")
	}
	r.Dim = ""
	p.rd = 6
	p.state.X, p.state.Z = 256.5, 256.5
	s.config.MobSpawning.SpawnRange = 1
	if len(s.mobSpawnChunks(r)) != 289 {
		t.Fatal("reduced spawn range changed ticket accounting")
	}
	for i := 0; i < 70; i++ {
		r.Mobs[uint32(i+1)] = &Mob{ID: uint32(i + 1), Type: "zombie", X: 256.5, Y: 60, Z: 256.5, Health: 20}
	}
	if s.naturalLocalCap(r, "monster", 256, 256) {
		t.Fatal("local cap ignored")
	}
	q := dropTestPlayer(r, 2, 768.5, 60, 768.5)
	r.Peers[q] = true
	if !s.naturalLocalCap(r, "monster", 768, 768) {
		t.Fatal("another player's farm blocks this player")
	}
}
func TestSpigotNaturalCommitValidationAndAudit(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	p.state.X = 100.5
	r.Time = 18000
	for x := 0; x <= 40; x++ {
		for z := 0; z <= 30; z++ {
			r.Edits[blockKey(x, 63, z)] = 1
		}
	}
	r.rt().spawnLights = newSpawnLighting() // Exercise the cache shared by a natural spawning pass.
	defer func() { r.rt().spawnLights = nil }()
	before := r.NextMobID
	success := 0
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.EntitySpawning {
			r.Edits[blockKey(12, 60, 11)] = 82
		}
		if e.Name == api.EntitySpawned {
			success++
		}
	}
	if _, err := s.spawnMob(r, nil, "skeleton", 12.5, 60, 10.5, "natural"); err == nil || success != 0 || len(r.Mobs) != 0 || before != r.NextMobID {
		t.Fatal("changed light validation committed")
	}
	s.testEventHook = nil
	r.Edits[blockKey(12, 60, 11)] = 0
	r.rt().spawnLights = newSpawnLighting() // Start the next pass after changing the fixture terrain.
	ctx := s.naturalContext(r, 12, 60, 10, 70, 0, 289, 600)
	ctx["entityId"] = uint32(1)
	ctx["type"] = "skeleton"
	tr := (&api.Event{Data: ctx}).MobLifecycle()
	if tr == nil || tr.SpawningRules != "spigot-26.3" || tr.SkyLight != 0 || tr.BlockLight != 0 {
		t.Fatal(tr)
	}
}
func TestSpigotInitialAnimalsCancelSaveAndReload(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.MobSpawning.GenerateChunkAnimals = true
	s.config.MobSpawning.AnimalLimit = 0
	s.config.MobSpawning.TotalLimit = 12
	r := newRoom("chunkanimal00001", "population", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	c := ChunkCoord{16, 16}
	for n := 0; n < 1000; n++ {
		r.ID = fmt.Sprintf("chunkanimal%05d", n)
		if s.naturalBiome(r, 256, 256) == "plains" {
			break
		}
	}
	base := make([]byte, chunkVolume)
	for x := 256; x < 272; x++ {
		for z := 256; z < 272; z++ {
			base[chunkIndex(x, 80, z)] = 2
		}
	}
	r.chunks = map[ChunkCoord]*WorldChunk{c: {Base: base, VegetationVersion: vegetationVersion}}
	s.rooms[r.ID] = r
	s.rng = rand.New(rand.NewSource(18))
	before := r.NextMobID
	vetoes, success := 0, 0
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.EntitySpawning && e.String("reason") == "chunk_generation" {
			vetoes++
			e.Cancelled = true
		}
		if e.Name == api.EntitySpawned {
			success++
		}
	}
	for i := 0; i < 200; i++ {
		s.spawnChunkAnimals(r, c)
	}
	if vetoes == 0 || success != 0 || len(r.Mobs) != 0 || r.NextMobID != before {
		t.Fatal("veto committed", vetoes, success)
	}
	s.testEventHook = nil
	for i := 0; i < 300 && len(r.Mobs) < 12; i++ {
		s.spawnChunkAnimals(r, c)
	}
	if len(r.Mobs) == 0 || len(r.Mobs) > 12 || len(r.chunks) != 1 {
		t.Fatal("initial population", len(r.Mobs))
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	count := len(r.Mobs)
	s.rooms = map[string]*Room{}
	if err := s.loadRooms(); err != nil {
		t.Fatal(err)
	}
	loaded := s.rooms[r.ID]
	if len(loaded.Mobs) != count {
		t.Fatal("lost population")
	}
	if _, err := s.ensureChunk(loaded, c); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Mobs) != count {
		t.Fatal("old chunk repopulated")
	}
}

func TestSpigotAnimalFloorTags(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := mobTestWorld(s)
	r.Time = 6000
	for _, floor := range []string{"Grass Block", "Snow Block", "Coarse Dirt"} {
		r.Edits[blockKey(12, 59, 10)] = s.rules.byName[floor]
		if !s.canNaturalSpawn(r, "wolf", 12, 60, 10) {
			t.Fatalf("wolf cannot spawn on upstream floor %s", floor)
		}
		if floor != "Grass Block" && s.canNaturalSpawn(r, "cow", 12, 60, 10) {
			t.Fatalf("generic animal used wolf floor %s", floor)
		}
	}
}
