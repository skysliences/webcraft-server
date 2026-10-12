package game

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMobSpawnBudgetLoadedChunksOverlapAndSpectators(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("0123456789abcdef", "spawning", "survival", 0)
	p := dropTestPlayer(r, 1, 256.5, 60, 256.5)
	p.rd = 6
	r.Peers[p] = true
	if n := len(s.mobSpawnChunks(r)); n != 169 {
		t.Fatal(n)
	}
	q := dropTestPlayer(r, 2, 260.5, 60, 260.5)
	q.rd = 6
	r.Peers[q] = true
	if n := len(s.mobSpawnChunks(r)); n != 169 {
		t.Fatalf("overlap inflated cap: %d", n)
	}
	q.state.X = 768.5
	if n := len(s.mobSpawnChunks(r)); n != 338 {
		t.Fatal(n)
	}
	q.state.Ext = &PlayerExt{GameMode: "spectator"}
	if n := len(s.mobSpawnChunks(r)); n != 169 {
		t.Fatal("spectator counted", n)
	}
	p.state.Dead = true
	if n := len(s.mobSpawnChunks(r)); n != 0 {
		t.Fatal("dead player counted", n)
	}
	p.state.Dead = false
	r.Infinite = true
	r.LegacyW = 0
	r.chunks = map[ChunkCoord]*WorldChunk{}
	if n := len(s.mobSpawnChunks(r)); n != 0 || len(r.chunks) != 0 {
		t.Fatal("spawn generated terrain", n)
	}
	r.chunks[ChunkCoord{16, 16}] = &WorldChunk{}
	if n := len(s.mobSpawnChunks(r)); n != 1 || len(r.chunks) != 1 {
		t.Fatal(n)
	}
	active := map[ChunkCoord]bool{{16, 16}: true}
	r.Mobs[1] = &Mob{Type: "zombie", X: 256, Y: 60, Z: 256}
	r.Mobs[2] = &Mob{Type: "zombie", X: 256, Y: 60, Z: 256, Persistent: true}
	r.Mobs[3] = &Mob{Type: "zombie", X: 1000, Y: 60, Z: 1000}
	r.Mobs[4] = &Mob{Type: "villager", X: 256, Y: 60, Z: 256}
	if n := s.naturalMobCount(r, "monster", active); n != 1 {
		t.Fatal(n)
	}
}

func TestMobNaturalConditionsCavesDistanceAndPeaceful(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	p.state.X, p.state.Z = 100.5, 10.5
	if !s.naturalSpawnPosition(r, "cow", 12, 60, 10) {
		t.Fatal("sunlit grass")
	}
	r.Edits[blockKey(12, 63, 10)] = s.rules.byName["Stone"]
	if !s.naturalSpawnPosition(r, "zombie", 12, 60, 10) || s.canNaturalSpawn(r, "cow", 12, 60, 10) {
		t.Fatal("daylight cave")
	}
	r.Edits[blockKey(12, 60, 11)] = 82
	if s.naturalSpawnPosition(r, "zombie", 12, 60, 10) {
		t.Fatal("block light ignored")
	}
	r.Edits[blockKey(12, 60, 11)] = 0
	p.state.X = 36.5
	if s.naturalSpawnPosition(r, "zombie", 12, 60, 10) {
		t.Fatal("24-block boundary")
	}
	p.state.X = 36.6
	if !s.naturalSpawnPosition(r, "zombie", 12, 60, 10) {
		t.Fatal("outside exclusion")
	}
	p.state.X = 140.6
	if s.naturalSpawnPosition(r, "zombie", 12, 60, 10) {
		t.Fatal("outside 128")
	}
	p.state.X, p.state.Y = 12.5, 85
	if !s.naturalSpawnPosition(r, "zombie", 12, 60, 10) {
		t.Fatal("vertical distance ignored")
	}
	r.ext().Difficulty = "peaceful"
	if s.canNaturalSpawn(r, "zombie", 12, 60, 10) {
		t.Fatal("peaceful natural")
	}
	for _, reason := range []string{"spawn_egg", "plugin", "command", "spawner"} {
		if _, err := s.spawnMob(r, p, "zombie", 12.5, 60, 10.5, reason); err == nil {
			t.Fatal("peaceful bypass", reason)
		}
	}
}

func TestMobAmbientCapsIntervalsAndIndependentSources(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	p.state.X, p.state.Z = 256.5, 256.5
	p.rd = 6
	// A broad flat roof above the generated terrain makes surface sampling stable.
	for x := 160; x < 368; x++ {
		for z := 160; z < 368; z++ {
			r.Edits[blockKey(x, 120, z)] = s.rules.byName["Grass Block"]
			for y := 121; y < 128; y++ {
				r.Edits[blockKey(x, y, z)] = 0
			}
		}
	}
	s.config.MobSpawning.MonsterLimit = 0
	s.rng = rand.New(rand.NewSource(2))
	s.tick = 199
	s.spawnAmbientMobs(r)
	if len(r.Mobs) != 0 {
		t.Fatal("animal interval ignored")
	}
	for s.tick = 200; s.tick <= 400; s.tick += 200 {
		s.spawnAmbientMobs(r)
	}
	if len(r.Mobs) != 5 {
		t.Fatalf("169/289 animal cap expected 5, got %d", len(r.Mobs))
	}
	for _, m := range r.Mobs {
		if m.SpawnReason != "natural" || !mobPersistent(m) {
			t.Fatal(m)
		}
	}
	for i := 0; i < 30; i++ {
		if _, err := s.spawnMob(r, nil, "cow", 12.5, 60, 10.5, "command"); err != nil {
			t.Fatal("manual spawn limited by natural cap", err)
		}
	}
	if len(r.Mobs) != 35 {
		t.Fatal(len(r.Mobs))
	}
	s.config.MobSpawning.TotalLimit = 35
	if _, err := s.spawnMob(r, nil, "cow", 12.5, 60, 10.5, "command"); err == nil {
		t.Fatal("safety cap ignored")
	}
	// Monster attempts run independently at night, including a bounded pack.
	s.config.MobSpawning.TotalLimit = 1024
	s.config.MobSpawning.MonsterLimit = 70
	r.Mobs = map[uint32]*Mob{}
	r.Time = 18000
	for i := 1; i <= 200; i++ {
		s.tick = uint64(i)
		before := len(r.Mobs)
		s.spawnAmbientMobs(r)
		if len(r.Mobs)-before > 4 {
			t.Fatal("tick spawn budget exceeded")
		}
	}
	if len(r.Mobs) != 40 {
		t.Fatalf("169/289 monster cap expected 40, got %d", len(r.Mobs))
	}
	if mobIntervalDue(0, 0) || !mobIntervalDue(1, 1) || mobIntervalDue(199, 400) || !mobIntervalDue(200, 400) {
		t.Fatal("tick units")
	}
}

func TestMobDespawnPersistenceAgingAndThreeDimensions(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	for _, typ := range []string{"cow", "pig", "sheep", "chicken", "wolf", "villager", "shulker", "wither", "ender_dragon"} {
		m := &Mob{ID: mobNamespace | uint32(len(r.Mobs)+1), Type: typ, X: 12.5, Y: 60, Z: 10.5, Health: mobSpecs[typ].Health}
		r.Mobs[m.ID] = m
		if s.despawnMob(r, m, 200) || r.Mobs[m.ID] != m {
			t.Fatal("persistent removed", typ)
		}
	}
	m, _ := s.spawnMob(r, nil, "zombie", 14.5, 60, 10.5, "command")
	m.Persistent = true
	if s.despawnMob(r, m, 200) {
		t.Fatal("explicit persistent removed")
	}
	m.Persistent = false
	m.InactiveTicks = 600
	if s.despawnMob(r, m, 30) || m.InactiveTicks != 0 {
		t.Fatal("near timer reset")
	}
	if s.despawnMob(r, m, math.Inf(1)) || m.InactiveTicks != 0 {
		t.Fatal("empty world erased mob")
	}
	m.InactiveTicks = 600
	s.rng = rand.New(rand.NewSource(3))
	for i := 0; i < 10000 && r.Mobs[m.ID] != nil; i++ {
		s.despawnMob(r, m, 40)
	}
	if r.Mobs[m.ID] != nil {
		t.Fatal("random despawn never happened")
	}
	m, _ = s.spawnMob(r, nil, "zombie", 14.5, 60, 10.5, "command")
	p.state.X, p.state.Y, p.state.Z = 14.5, 126, 10.5
	_, distance := s.nearestMobPlayer(r, m.X, m.Y, m.Z)
	if distance != 66 {
		t.Fatal(distance)
	}
	if !s.despawnMob(r, m, 129) || r.Mobs[m.ID] != nil {
		t.Fatal("hard distance")
	}
}

func TestMobSpawningPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "mob-spawning-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.EntitySpawning, api.EntityDespawning} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if e.String("reason") == "protected" || e.String("reason") == "distance" {
				e.Cancel("spawning.protected")
			}
			e.Data["type"], e.Data["persistent"], e.Data["x"] = "ender_dragon", true, 99999
			return nil
		})
	}
	for _, name := range []string{api.EntitySpawning, api.EntitySpawned, api.EntityDespawning, api.EntityDespawned} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			e.Cancel("monitor ignored")
			c.SpawnPersistentMob(e.World.ID, "cow", 12.5, 60, 10.5)
			return err
		})
	}
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMobSpawningProtectionEventsAndPluginPersistence(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "mob-test")
	os.MkdirAll(entry, 0700)
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700)
	os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(`{"id":"mob-test","name":"mob-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestMobSpawningPluginProcess$","mob-spawning-fixture"]}`), 0600)
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: findTestEngineFile(), PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	p.sid = "test-actor"
	s.rooms[r.ID] = r
	if _, err = s.spawnMob(r, p, "cow", 12.5, 60, 10.5, "protected"); err == nil || len(r.Mobs) != 0 {
		t.Fatal("cancelled spawn committed")
	}
	m, err := s.spawnMob(r, p, "zombie", 12.5, 60, 10.5, "command")
	if err != nil || m.Type != "zombie" || m.Persistent || m.X != 12.5 {
		t.Fatal("readonly altered", m, err)
	}
	if s.despawnMob(r, m, 129) || r.Mobs[m.ID] != m {
		t.Fatal("cancelled despawn committed")
	}
	if !s.removeMob(r, m, "test") {
		t.Fatal("monitor cancelled removal")
	}
	s.applyAction(api.Action{Type: "spawn_mob", WorldID: r.ID, EntityType: "zombie", X: 12.5, Y: 60, Z: 10.5, Persistent: true})
	if len(r.Mobs) != 1 {
		t.Fatal("monitor action executed")
	}
	for _, mob := range r.Mobs {
		if !mob.Persistent || mob.SpawnReason != "plugin" {
			t.Fatal(mob)
		}
	}
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "mob-test", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	spawned, despawned := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Name == api.EntitySpawned {
			spawned++
			if e.String("reason") == "protected" {
				t.Fatal("success after cancellation")
			}
			if e.String("reason") == "command" && (e.String("actor") != "test-actor" || e.MobLifecycle().Target.EntityType != "zombie") {
				t.Fatal(e.Data)
			}
		}
		if e.Name == api.EntityDespawned {
			despawned++
			if e.String("reason") == "distance" {
				t.Fatal("success after cancellation")
			}
		}
	}
	if spawned != 2 || despawned != 1 {
		t.Fatal(spawned, despawned)
	}
}

func TestMobSpawningSaveMigrationAndLoweredCapacity(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := mobTestWorld(s)
	s.rooms[r.ID] = r
	for i := uint32(1); i <= 40; i++ {
		r.Mobs[mobNamespace|i] = &Mob{ID: mobNamespace | i, Type: "cow", X: 12.5, Y: 60, Z: 10.5, Health: 10}
	}
	r.Mobs[mobNamespace|1].Persistent = true
	r.Mobs[mobNamespace|1].SpawnReason = "plugin"
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	file := s.roomFile(r.ID)
	raw, _ := os.ReadFile(file)
	var data map[string]any
	json.Unmarshal(raw, &data)
	data["version"] = 8
	old, _ := json.Marshal(data)
	os.WriteFile(file, old, 0600)
	s.config.MobSpawning.TotalLimit = 1
	if err := s.loadRooms(); err != nil {
		t.Fatal("lowered cap deleted/rejected save", err)
	}
	loaded := s.rooms[r.ID]
	if len(loaded.Mobs) != 40 || !loaded.Mobs[mobNamespace|1].Persistent || loaded.Mobs[mobNamespace|1].SpawnReason != "plugin" {
		t.Fatal("mob state lost")
	}
	if err := s.saveRoom(loaded); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(file + ".pre-mob-spawning-v8.bak")
	if err != nil || !bytes.Equal(backup, old) {
		t.Fatal("original not backed up", err)
	}
	if !mobPersistent(loaded.Mobs[mobNamespace|2]) {
		t.Fatal("old animal despawns")
	}
	if s.mobCapacity(loaded, 1) {
		t.Fatal("lowered safety cap ignored")
	}
	cfg := DefaultMobSpawningConfig()
	cfg.SpawnRange = 9
	if validateMobSpawningConfig(cfg) == nil {
		t.Fatal("invalid range")
	}
}

func TestEndermanOverworldSpawningDisabled(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	p.state.X, p.state.Z = 100.5, 10.5

	// 1. 在主世界中（黑暗洞穴），验证普通怪物可以自然生成，但末影人严禁自然生成
	r.Edits[blockKey(12, 63, 10)] = s.rules.byName["Stone"]
	if !s.canNaturalSpawn(r, "zombie", 12, 60, 10) {
		t.Fatal("zombie should naturally spawn in dark cave")
	}
	if s.canNaturalSpawn(r, "enderman", 12, 60, 10) {
		t.Fatal("enderman should NOT naturally spawn in overworld")
	}

	// 2. 验证在末地（Dim == "end"）中，末影人依然可以自然生成
	r.Dim = "end"
	if !s.canNaturalSpawn(r, "enderman", 12, 60, 10) {
		t.Fatal("enderman should naturally spawn in the End")
	}
	r.Dim = "overworld"

	// 3. 验证主世界已存在的非持久末影人在 mobsTick 中会被自动清除
	r.Mobs[101] = &Mob{ID: 101, Type: "enderman", X: 12.5, Y: 60, Z: 10.5, SpawnReason: "natural"}
	// 持久末影人（如命名牌或显式标记）保留
	r.Mobs[102] = &Mob{ID: 102, Type: "enderman", X: 12.5, Y: 60, Z: 10.5, Persistent: true}
	// 普通怪物不被此规则清除
	r.Mobs[103] = &Mob{ID: 103, Type: "zombie", X: 12.5, Y: 60, Z: 10.5}

	s.mobsTick(r)

	if r.Mobs[101] != nil {
		t.Fatal("non-persistent overworld enderman should be removed by mobsTick")
	}
	if r.Mobs[102] == nil {
		t.Fatal("persistent enderman should be preserved")
	}
	if r.Mobs[103] == nil {
		t.Fatal("regular monster should not be removed by enderman restriction")
	}

	// 4. 验证主世界自然怪物刷怪循环中绝不生成末影人
	r.Mobs = map[uint32]*Mob{}
	r.Time = 18000
	s.rng = rand.New(rand.NewSource(42))
	for i := 1; i <= 200; i++ {
		s.tick = uint64(i)
		s.spawnAmbientMobs(r)
	}
	for _, m := range r.Mobs {
		if m.Type == "enderman" {
			t.Fatal("ambient spawning produced an enderman in overworld")
		}
	}
}

