package game

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func vegetationRoom() *Room {
	r := newRoom("vegetationtest01", "vegetation", "survival", 0)
	r.Infinite, r.Generator = true, generatorVersion
	return r
}

// Surface-only fixture lets biome scatter tests cover many absolute coordinates
// cheaply; full noise generation is verified separately below.
func vegetationSurface(s *Server, r *Room, c ChunkCoord) []byte {
	base := make([]byte, chunkVolume)
	for z := c.Z * 16; z < c.Z*16+16; z++ {
		for x := c.X * 16; x < c.X*16+16; x++ {
			h, b := terrainColumn(seed(r.seedID()), x, z)
			id := s.rules.byName["Grass Block"]
			if b == "ocean" || b == "desert" {
				id = s.rules.byName["Sand"]
			}
			base[chunkIndex(x, h, z)] = byte(id)
		}
	}
	return base
}
func vegetationFixture(t *testing.T, s *Server, r *Room) (ChunkCoord, []byte, []vegetationBlock) {
	t.Helper()
	for z := -8; z <= 8; z++ {
		for x := 0; x <= 8; x++ {
			c := ChunkCoord{x, z}
			base := s.generateTerrainBase(r, c)
			plants := s.vegetationPlan(r, c, base)
			flower := false
			for _, v := range plants {
				if v.Biome == "plains" && v.Block >= 64 && v.Block <= 67 {
					flower = true
				}
			}
			if len(plants) > 8 && flower {
				return c, base, plants
			}
		}
	}
	t.Fatal("no plains vegetation fixture")
	return ChunkCoord{}, nil, nil
}
func writeOldVegetationChunk(t *testing.T, s *Server, r *Room, c ChunkCoord, base []byte) []byte {
	t.Helper()
	d := chunkData(r, c, base)
	d.VegetationVersion = 0
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err = gz.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	file := s.chunkFile(r, c)
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomicPrivateFile(file, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), b.Bytes()...)
}

func TestVegetationBiomesDeterminismAndChunkOrder(t *testing.T) {
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{rules: rules}
	r := vegetationRoom()
	seen := map[string]int{}
	for z := -12; z <= 12; z++ {
		for x := -12; x <= 12; x++ {
			c := ChunkCoord{x, z}
			base := vegetationSurface(s, r, c)
			plants := s.vegetationPlan(r, c, base)
			if !reflect.DeepEqual(plants, s.vegetationPlan(r, c, base)) {
				t.Fatal("vegetation depends on runtime state")
			}
			for _, plant := range plants {
				seen[s.rules.Names[plant.Block]]++
				if chunkCoord(plant.X, plant.Z) != c || base[chunkIndex(plant.X, plant.Y, plant.Z)] != 0 || plant.Y <= 53 {
					t.Fatal("plant outside chunk/above water constraints")
				}
				h, biome := terrainColumn(seed(r.seedID()), plant.X, plant.Z)
				if h+1 != plant.Y || biome != plant.Biome || biome == "ocean" {
					t.Fatal("plant biome/support mismatch")
				}
				if biome == "desert" && s.rules.Names[plant.Block] != "Dead Bush" {
					t.Fatal("desert has grassland vegetation")
				}
			}
		}
	}
	for _, name := range []string{"Short Grass", "Dandelion", "Poppy", "Cornflower", "Oxeye Daisy", "Fern"} {
		if seen[name] == 0 {
			t.Fatal("missing vegetation type", name)
		}
	}
	for _, c := range []ChunkCoord{{0, 0}, {-2, -3}, {2, 0}} {
		old := s.generateTerrainBase(r, c)
		full := s.generateTerrain(r, c)
		plan := s.vegetationPlan(r, c, old)
		expected := append([]byte(nil), old...)
		for _, p := range plan {
			expected[chunkIndex(p.X, p.Y, p.Z)] = byte(p.Block)
		}
		if !bytes.Equal(full, expected) {
			t.Fatal("generation changed terrain beyond decoration")
		}
		s.generateTerrain(r, ChunkCoord{c.X + 1, c.Z})
		if !bytes.Equal(full, s.generateTerrain(r, c)) {
			t.Fatal("adjacent chunk load order changed plants")
		}
	}
	c, base, _ := vegetationFixture(t, s, r)
	r.Dim = "nether"
	if len(s.vegetationPlan(r, c, base)) != 0 {
		t.Fatal("nether has overworld flora")
	}
	r.Dim = "end"
	if len(s.vegetationPlan(r, c, base)) != 0 {
		t.Fatal("end has overworld flora")
	}
	r.Dim = "overworld"
	r.LegacyW = 1024
	if len(s.vegetationPlan(r, ChunkCoord{0, 0}, vegetationSurface(s, r, ChunkCoord{}))) != 0 {
		t.Fatal("legacy terrain redecorated")
	}
	t.Log("vegetation counts", seen)
}

func TestVegetationBackfillBackupEditsAndPersistence(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := vegetationRoom()
	c, base, plants := vegetationFixture(t, s, r)
	a, b, d, e := plants[0], plants[1], plants[2], plants[3]
	r.Edits[blockKey(a.X, a.Y, a.Z)] = 0   // Player-cleared air remains cleared.
	r.Edits[blockKey(b.X, b.Y, b.Z)] = 11  // Existing construction.
	r.Edits[blockKey(d.X, d.Y-1, d.Z)] = 3 // Modified natural ground.
	key := blockKey(e.X, e.Y, e.Z)
	r.Edits[key] = 140
	item := customStack(276, 1)
	item.Ench = map[string]int{"sharpness": 2}
	r.Containers[key] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[key].Slots[0] = item
	r.Authors[key] = EditAuthor{Token: "builder", Name: "player", Before: 0, After: 140}
	r.blockExt().Blocks = map[uint64]*BlockData{key: {Facing: 2}}
	original := writeOldVegetationChunk(t, s, r, c, base)
	fresh := vegetationRoom()
	v, err := s.ensureChunk(fresh, c)
	if err != nil {
		t.Fatal(err)
	}
	if v.VegetationVersion != vegetationVersion || !fresh.Dirty {
		t.Fatal("upgrade not marked for persistence")
	}
	for _, p := range []vegetationBlock{a, b, d, e} {
		if v.Base[chunkIndex(p.X, p.Y, p.Z)] != 0 {
			t.Fatal("player modification was decorated", p)
		}
	}
	if !reflect.DeepEqual(fresh.Containers[key].Slots[0], item) || fresh.Authors[key] != r.Authors[key] || fresh.Ext.Blocks[key].Facing != 2 {
		t.Fatal("metadata lost during backfill")
	}
	count := 0
	for i, x := range v.Base {
		if x != base[i] {
			count++
		}
	}
	if count != len(plants)-4 {
		t.Fatalf("backfill changed unexpected blocks: %d want %d", count, len(plants)-4)
	}
	backup, err := os.ReadFile(s.vegetationBackupFile(r, c))
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatal("original chunk not backed up exactly")
	}
	if err = s.saveChunk(fresh, c, v); err != nil {
		t.Fatal(err)
	}
	loaded := vegetationRoom()
	w, err := s.ensureChunk(loaded, c)
	if err != nil {
		t.Fatal(err)
	}
	if w.VegetationVersion != vegetationVersion || !bytes.Equal(w.Base, v.Base) {
		t.Fatal("populated terrain not restored")
	}
	p := plants[4]
	loaded.Edits[blockKey(p.X, p.Y, p.Z)] = 0
	if err = s.saveChunk(loaded, c, w); err != nil {
		t.Fatal(err)
	}
	again := vegetationRoom()
	if _, err = s.ensureChunk(again, c); err != nil {
		t.Fatal(err)
	}
	if s.block(again, p.X, p.Y, p.Z) != 0 {
		t.Fatal("cleared vegetation regenerated after reload")
	}
	backup, _ = os.ReadFile(s.vegetationBackupFile(r, c))
	if !bytes.Equal(backup, original) {
		t.Fatal("first backup overwritten")
	}
	if !reflect.DeepEqual(again.Containers[key].Slots[0], item) {
		t.Fatal("metadata lost during save/reload")
	}
}

func TestPluginVegetationCancellationReadOnlyAndBackupFailure(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := vegetationRoom()
	c, base, plants := vegetationFixture(t, s, r)
	blocked := ChunkCoord{-1, c.Z}
	blockedBase := vegetationSurface(s, r, blocked)
	v := &WorldChunk{Base: append([]byte(nil), blockedBase...)}
	if err := s.populateVegetation(r, blocked, v, true); err != nil {
		t.Fatal(err)
	}
	if v.VegetationVersion != 0 || !bytes.Equal(v.Base, blockedBase) {
		t.Fatal("cancelled population committed")
	}
	v = &WorldChunk{Base: append([]byte(nil), base...)}
	if err := s.populateVegetation(r, c, v, true); err != nil {
		t.Fatal(err)
	}
	if v.VegetationVersion != vegetationVersion {
		t.Fatal("Monitor cancelled population")
	}
	for _, p := range plants {
		if int(v.Base[chunkIndex(p.X, p.Y, p.Z)]) != p.Block {
			t.Fatal("undeclared plugin mutation changed plants")
		}
	}
	// A failed backup preflight cannot modify terrain or emit completion.
	writeOldVegetationChunk(t, s, r, c, base)
	if err := os.MkdirAll(s.vegetationBackupFile(r, c), 0700); err != nil {
		t.Fatal(err)
	}
	v = &WorldChunk{Base: append([]byte(nil), base...)}
	if err := s.populateVegetation(r, c, v, false); err == nil {
		t.Fatal("failed backup accepted")
	}
	if v.VegetationVersion != 0 || !bytes.Equal(v.Base, base) {
		t.Fatal("failed backup modified terrain")
	}
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins/test/vegetation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid event log")
		}
		if e.Name == api.ChunkPopulated {
			completed++
			if e.Player != nil || e.World == nil || e.Data["target"] == nil || int(e.Number("count")) != len(plants) || e.String("reason") != "terrain_generation" {
				t.Fatal("population event lost read-only target/plant snapshots")
			}
		}
	}
	if completed != 1 {
		t.Fatal("cancel or backup failure emitted completed event", completed)
	}
	for _, spec := range api.Events() {
		if spec.Name == api.ChunkPopulating && (!spec.Cancellable || len(spec.Mutable) != 0) {
			t.Fatal("invalid population event catalog")
		}
	}
}

func TestVegetationVersionValidation(t *testing.T) {
	rules, _ := loadRules()
	s := &Server{rules: rules}
	for _, version := range []int{-1, vegetationVersion + 1} {
		if s.validateChunk(&diskChunk{Base: make([]byte, chunkVolume), VegetationVersion: version, CRC: crc32.ChecksumIEEE(make([]byte, chunkVolume))}) == nil {
			t.Fatal("invalid vegetation version accepted")
		}
	}
}
