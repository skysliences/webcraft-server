package game

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func expandStreamTerrain(t testing.TB, data []byte) []byte {
	t.Helper()
	out := make([]byte, 0, chunkVolume)
	for len(data) >= 3 {
		n, block := int(binary.LittleEndian.Uint16(data)), data[2]
		if n == 0 || len(out)+n > chunkVolume {
			t.Fatal("invalid streamed terrain run")
		}
		out = append(out, bytes.Repeat([]byte{block}, n)...)
		data = data[3:]
	}
	if len(data) != 0 || len(out) != chunkVolume {
		t.Fatal("incomplete streamed chunk")
	}
	return out
}

func TestChunkWireCacheFreshEditsAndDecoration(t *testing.T) {
	base := bytes.Repeat([]byte{1}, chunkVolume)
	v := &WorldChunk{Base: base}
	encoded := append([]byte(nil), chunkWireTerrain(v, nil)...)
	for _, edits := range []map[int]int{{0: 26, 317: 0}, {0: 2, 317: 11}, nil} {
		got := expandStreamTerrain(t, chunkWireTerrain(v, edits))
		want := append([]byte(nil), base...)
		for i, b := range edits {
			want[i] = byte(b)
		}
		if !bytes.Equal(got, want) || !bytes.Equal(v.Base, base) || !bytes.Equal(v.Terrain, encoded) {
			t.Fatal("mutable edit overlay changed cached base or missed current edits")
		}
	}
	rules, _ := loadRules()
	s := &Server{rules: rules}
	r := vegetationRoom()
	c, terrain, plants := vegetationFixture(t, s, r)
	v = &WorldChunk{Base: terrain}
	chunkWireTerrain(v, nil)
	if err := s.populateVegetation(r, c, v, true); err != nil {
		t.Fatal(err)
	}
	if v.Terrain != nil {
		t.Fatal("decoration did not invalidate base wire cache")
	}
	got := expandStreamTerrain(t, chunkWireTerrain(v, nil))
	for _, plant := range plants {
		if got[chunkIndex(plant.X, plant.Y, plant.Z)] != byte(plant.Block) {
			t.Fatal("successful vegetation commit missing from wire cache")
		}
	}
}

func streamFixture(t *testing.T) (*Server, *Room, *Peer) {
	t.Helper()
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{rules: rules, config: Config{DataDir: t.TempDir()}}
	r := vegetationRoom()
	r.chunks = map[ChunkCoord]*WorldChunk{}
	p := &Peer{room: r, state: &Player{X: -8.5, Y: 70, Z: 4.5}, rd: 2, send: make(chan packet, 256)}
	p.setOrigin()
	center := chunkCoord(-9, 4)
	for dz := -6; dz <= 6; dz++ {
		for dx := -6; dx <= 6; dx++ {
			r.chunks[ChunkCoord{center.X + dx, center.Z + dz}] = &WorldChunk{Base: make([]byte, chunkVolume), VegetationVersion: vegetationVersion}
		}
	}
	return s, r, p
}

func drainStreamPackets(t *testing.T, p *Peer) (int, map[ChunkCoord][]byte) {
	t.Helper()
	chunks := map[ChunkCoord][]byte{}
	for len(p.send) > 0 {
		pkt := <-p.send
		if len(pkt.body) < 8 || pkt.body[0] != 13 {
			continue
		}
		if binary.LittleEndian.Uint32(pkt.body[4:]) != p.epoch {
			t.Fatal("chunk emitted with stale origin epoch")
		}
		data := pkt.body[8:]
		for n := int(binary.LittleEndian.Uint16(pkt.body[2:])); n > 0; n-- {
			if len(data) < 12 {
				t.Fatal("short streamed chunk header")
			}
			c := ChunkCoord{int(int32(binary.LittleEndian.Uint32(data))), int(int32(binary.LittleEndian.Uint32(data[4:])))}
			length := int(binary.LittleEndian.Uint32(data[8:]))
			if length > len(data)-12 {
				t.Fatal("short streamed chunk body")
			}
			chunks[c] = expandStreamTerrain(t, data[12:12+length])
			data = data[12+length:]
		}
		if len(data) != 0 {
			t.Fatal("unaccounted stream bytes")
		}
	}
	return len(chunks), chunks
}

func TestChunkStreamBoundedFairnessEpochAndFreshEdits(t *testing.T) {
	s, r, p := streamFixture(t)
	q := &Peer{room: r, state: &Player{X: p.state.X, Y: p.state.Y, Z: p.state.Z}, rd: p.rd, send: make(chan packet, 256)}
	q.setOrigin()
	key := blockKey(-9, 71, 4)
	r.Edits[key] = 26
	for _, peer := range []*Peer{p, q} {
		s.streamInfinite(peer)
		n, chunks := drainStreamPackets(t, peer)
		if n == 0 || n > chunkStreamBatch || !peer.chunkStreaming {
			t.Fatalf("unbounded/starved peer batch %d", n)
		}
		if chunk := chunks[chunkCoord(-9, 4)]; chunk == nil || chunk[chunkIndex(-9, 71, 4)] != 26 {
			t.Fatal("nearest standing chunk or edit missing")
		}
	}
	// Completing a view does not require the user to continue moving.
	for attempts := 0; p.chunkStreaming && attempts < 100; attempts++ {
		p.chunkStreamAt = time.Time{}
		s.streamInfinite(p)
		n, _ := drainStreamPackets(t, p)
		if n > chunkStreamBatch {
			t.Fatal("continuation exceeded per-player budget")
		}
	}
	if p.chunkStreaming || len(p.known) != 81 {
		t.Fatal("stationary view did not finish progressively", len(p.known))
	}
	// A render-origin reset must discard known state and use fresh metadata,
	// even if this chunk was encoded and sent before the edit changed.
	old := p.epoch
	r.Edits[key] = 0
	p.setOrigin()
	s.streamInfinite(p)
	_, chunks := drainStreamPackets(t, p)
	if p.epoch == old || chunks[chunkCoord(-9, 4)][chunkIndex(-9, 71, 4)] != 0 {
		t.Fatal("origin reset reused stale terrain edits")
	}
}

func TestChunkStreamBackpressureKeepsUnknownChunksRetryable(t *testing.T) {
	s, _, p := streamFixture(t)
	for len(p.send) < cap(p.send)-1 {
		p.send <- packet{body: []byte{0}}
	}
	s.streamInfinite(p)
	if len(p.known) != 0 || !p.chunkStreaming || !p.chunkStreamAt.IsZero() {
		t.Fatal("busy output queue advanced chunk visibility or lost continuation")
	}
	for len(p.send) > 0 {
		<-p.send
	}
	s.streamInfinite(p)
	n, _ := drainStreamPackets(t, p)
	if n == 0 {
		t.Fatal("stalled chunks were not retried after output drained")
	}
}

func TestChunkStreamingPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "chunk-stream-plugin" {
		return
	}
	p := api.New()
	p.On(api.ChunkPopulating, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Number("x") < 0 {
			e.Cancel("chunk.test_protected")
		}
		e.Data["blocks"] = []any{}
		return nil
	})
	p.On(api.EntitySpawning, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "village" && e.World.Name == "blocked village" {
			e.Cancel("village.test_protected")
		}
		return nil
	})
	for _, name := range []string{api.ChunkPopulating, api.ChunkPopulated, api.ChunkGenerated, api.ChunkLoaded, api.ChunkSaved, api.EntitySpawning, api.EntitySpawned} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			f.Close()
			e.Cancel("monitor ignored")
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func chunkStreamingApp(t *testing.T) *Server {
	t.Helper()
	parent, dir := t.TempDir(), t.TempDir()
	root := filepath.Join(parent, "chunk-stream")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"chunk-stream","name":"chunk-stream","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestChunkStreamingPluginProcess$","chunk-stream-plugin"]}`
	if err = os.WriteFile(filepath.Join(root, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: dir, EngineFile: "../../../public/engine.wasm", PluginDir: parent, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.plugins.Status()) != 1 || s.plugins.Failed() != 0 {
		s.Close()
		t.Fatal("chunk event fixture plugin did not start", s.plugins.Status())
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func awaitPreparedChunk(t *testing.T, s *Server, r *Room, c ChunkCoord) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.chunks[c] != nil || s.preparedChunkReady(r, c) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("chunk preparation did not finish")
}

func TestChunkPreparationEventsCancellationAndStopRestore(t *testing.T) {
	s := chunkStreamingApp(t)
	s.mu.Lock()
	r := vegetationRoom()
	s.rooms[r.ID] = r
	r.Dirty = false
	startEvent := s.eventID
	c := ChunkCoord{-1, 0}
	s.requestChunkPreparation(r, c)
	awaitPreparedChunk(t, s, r, c)
	if r.chunks[c] != nil || r.Dirty || s.eventID != startEvent || len(r.Edits) != 0 {
		s.mu.Unlock()
		t.Fatal("background preparation mutated the world or dispatched events")
	}
	v, err := s.ensureChunk(r, c)
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	if v.VegetationVersion != 0 || !r.Dirty {
		s.mu.Unlock()
		t.Fatal("cancelled vegetation committed or generated chunk unpublished")
	}
	key := blockKey(-7, 73, 6)
	r.Edits[key] = 26
	item := customStack(276, 1)
	r.Containers[key] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[key].Slots[0] = item
	// Stop while another prepared snapshot is still uncommitted. Only committed
	// chunks should enter the world save, and workers must finish before unlock.
	s.requestChunkPreparation(r, ChunkCoord{4, 7})
	s.mu.Unlock()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	fresh := vegetationRoom()
	restorer := &Server{rules: s.rules, config: s.config}
	if _, err = restorer.ensureChunk(fresh, c); err != nil || fresh.Edits[key] != 26 || !reflect.DeepEqual(fresh.Containers[key].Slots[0], item) {
		t.Fatal("stopping lost committed terrain or rich container metadata", err)
	}
	if _, err = os.Stat(s.chunkFile(r, ChunkCoord{4, 7})); !os.IsNotExist(err) {
		t.Fatal("uncommitted preparation created a saved world chunk", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "chunk-stream", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid chunk event audit")
		}
		if e.World != nil && e.World.ID == r.ID {
			counts[e.Name]++
		}
	}
	if counts[api.ChunkPopulating] != 1 || counts[api.ChunkPopulated] != 0 || counts[api.ChunkGenerated] != 1 || counts[api.ChunkLoaded] != 1 || counts[api.ChunkSaved] != 1 {
		t.Fatal("preparation/cancellation/commit lifecycle events incorrect", counts)
	}
}

func TestBackgroundVillageMetadataAndProtectedSpawnCommit(t *testing.T) {
	s := chunkStreamingApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := vegetationRoom()
	s.rooms[r.ID] = r
	var c ChunkCoord
	found := false
	for rx := 0; rx < 8 && !found; rx++ {
		for rz := 0; rz < 8; rz++ {
			vHash := terrainHash(seed(r.seedID())+777, rx, 0, rz)
			c = ChunkCoord{rx*8 + int(vHash%6) + 1, rz*8 + int((vHash>>8)%6) + 1}
			snapshot, err := s.generateChunkSnapshot(r, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Containers) > 0 && len(snapshot.VillageSpawns) > 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("no deterministic village fixture")
	}
	r.Dirty = false
	start := s.eventID
	s.requestChunkPreparation(r, c)
	awaitPreparedChunk(t, s, r, c)
	if len(r.Containers) != 0 || len(r.Mobs) != 0 || len(s.pendingVillageSpawns) != 0 || r.Dirty || s.eventID != start {
		t.Fatal("background structure generation published live containers/entities/events")
	}
	v, err := s.ensureChunk(r, c)
	if err != nil || len(r.Containers) == 0 || len(s.pendingVillageSpawns) == 0 {
		t.Fatal("committed prepared village lost loot or villager intents", err)
	}
	for _, spawn := range s.pendingVillageSpawns {
		if spawn.Room != r {
			t.Fatal("prepared entity retained detached room identity")
		}
	}
	s.spawnPendingVillagers()
	if len(r.Mobs) == 0 {
		t.Fatal("prepared village did not use authoritative entity spawn path")
	}
	if err = s.saveChunk(r, c, v); err != nil {
		t.Fatal(err)
	}
	fresh := vegetationRoom()
	if _, err = s.ensureChunk(fresh, c); err != nil || !reflect.DeepEqual(fresh.Containers, r.Containers) {
		t.Fatal("prepared village loot did not survive MCC save/restore", err)
	}
	// Replaying the same intents into an explicitly protected world must obey
	// existing entity.spawn.before cancellation, with no entity.spawned event.
	r.Name = "blocked village"
	r.Mobs = map[uint32]*Mob{}
	snapshot, _ := s.generateChunkSnapshot(r, c)
	for _, spawn := range snapshot.VillageSpawns {
		spawn.Room = r
		s.pendingVillageSpawns = append(s.pendingVillageSpawns, spawn)
	}
	s.spawnPendingVillagers()
	if len(r.Mobs) != 0 {
		t.Fatal("village generation bypassed protected entity spawn path")
	}
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "chunk-stream", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	spawned, cancelled := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid village event audit")
		}
		if e.World != nil && e.World.ID == r.ID && e.String("reason") == "village" {
			if e.Name == api.EntitySpawning && e.Cancelled {
				cancelled++
			}
			if e.Name == api.EntitySpawned {
				if e.World.Name == "blocked village" {
					t.Fatal("cancelled village emitted successful entity spawn")
				}
				spawned++
			}
		}
	}
	if spawned == 0 || cancelled == 0 {
		t.Fatal("missing authoritative village spawn/cancellation events", spawned, cancelled)
	}
}

func TestChunkPreparationQueueBoundedAndStaleSnapshotDiscarded(t *testing.T) {
	rules, _ := loadRules()
	s := &Server{rules: rules, config: Config{DataDir: t.TempDir()}}
	s.startChunkPreparation()
	defer s.chunkPreparer.close()
	r := vegetationRoom()
	for n := 0; n < 200; n++ {
		s.requestChunkPreparation(r, ChunkCoord{n, 90})
	}
	p := s.chunkPreparer
	p.mu.Lock()
	jobs, queued := len(p.jobs), len(p.work)
	p.mu.Unlock()
	if jobs > chunkPrepareLimit || queued > chunkPrepareLimit || r.chunks != nil {
		t.Fatal("unbounded preparation or worker publication", jobs, queued)
	}
	// A synchronous access invalidates the in-flight token; the worker cannot
	// later re-publish a stale read after this chunk is edited and saved.
	c := ChunkCoord{0, 90}
	v, err := s.ensureChunk(r, c)
	if err != nil {
		t.Fatal(err)
	}
	key := chunkKey(c, 777)
	r.Edits[key] = 11
	if err = s.saveChunk(r, c, v); err != nil {
		t.Fatal(err)
	}
	p.close()
	p.mu.Lock()
	stale := p.jobs[chunkPrepareKey{r, c}]
	p.mu.Unlock()
	if stale != nil {
		t.Fatal("stale completion survived synchronous save")
	}
	delete(r.chunks, c)
	if _, err = s.ensureChunk(r, c); err != nil || r.Edits[key] != 11 {
		t.Fatal("stale snapshot replaced the newly saved chunk", err)
	}
}

func TestPreparedChunkDoesNotOverwriteLiveMetadata(t *testing.T) {
	rules, _ := loadRules()
	s := &Server{rules: rules, config: Config{DataDir: t.TempDir()}}
	s.startChunkPreparation()
	defer s.chunkPreparer.close()
	r := vegetationRoom()
	c := ChunkCoord{9, -8}
	v, err := s.ensureChunk(r, c)
	if err != nil {
		t.Fatal(err)
	}
	k := chunkKey(c, 555)
	r.Edits[k] = 26
	r.Containers[k] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[k].Slots[0] = customStack(276, 1)
	r.Authors[k] = EditAuthor{Token: "saved", Before: 0, After: 26}
	r.blockExt().Blocks[k] = &BlockData{Facing: 2}
	if err = s.saveChunk(r, c, v); err != nil {
		t.Fatal(err)
	}
	live := vegetationRoom()
	s.requestChunkPreparation(live, c)
	awaitPreparedChunk(t, s, live, c)
	// Compatibility/migration state already present in the live room wins over
	// an older disk snapshot, including complete item data and authorship.
	live.Edits[k] = 26
	item := customStack(276, 1)
	item.Meta.DisplayName = "current item"
	live.Containers[k] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	live.Containers[k].Slots[0] = item
	live.Authors[k] = EditAuthor{Token: "current", Before: 0, After: 26}
	live.blockExt().Blocks[k] = &BlockData{Facing: 5}
	loaded, err := s.ensureChunk(live, c)
	if err != nil || !reflect.DeepEqual(live.Containers[k].Slots[0], item) || live.Authors[k].Token != "current" || live.Ext.Blocks[k].Facing != 5 {
		t.Fatal("prepared disk state replaced live metadata", err)
	}
	if err = s.saveChunk(live, c, loaded); err != nil {
		t.Fatal(err)
	}
	again := vegetationRoom()
	if _, err = s.ensureChunk(again, c); err != nil || !reflect.DeepEqual(again.Containers[k].Slots[0], item) || again.Authors[k].Token != "current" || again.Ext.Blocks[k].Facing != 5 {
		t.Fatal("latest metadata did not survive prepared chunk save/reload", err)
	}
}

func TestPreparedCorruptChunkPreservesFileAndEmitsNoSuccess(t *testing.T) {
	rules, _ := loadRules()
	s := &Server{rules: rules, config: Config{DataDir: t.TempDir()}}
	s.startChunkPreparation()
	defer s.chunkPreparer.close()
	r, c := vegetationRoom(), ChunkCoord{200, -100}
	file := s.chunkFile(r, c)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("corrupt terrain"), 0600); err != nil {
		t.Fatal(err)
	}
	s.requestChunkPreparation(r, c)
	awaitPreparedChunk(t, s, r, c)
	start := s.eventID
	if _, err := s.ensureChunk(r, c); err == nil || r.chunks[c] != nil || s.eventID != start {
		t.Fatal("corrupt preparation committed or emitted lifecycle success")
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != "corrupt terrain" {
		t.Fatal("corrupt disk chunk was overwritten")
	}
}

func TestChunkTrimBudgetPersistenceAndFailure(t *testing.T) {
	rules, _ := loadRules()
	s := &Server{rules: rules, config: Config{DataDir: t.TempDir()}}
	r := vegetationRoom()
	r.chunks = map[ChunkCoord]*WorldChunk{}
	for n := 0; n < 520; n++ {
		r.chunks[ChunkCoord{100 + n, 0}] = &WorldChunk{Base: make([]byte, chunkVolume), VegetationVersion: vegetationVersion, Used: uint64(n)}
	}
	c := ChunkCoord{100, 0}
	k := chunkKey(c, 40)
	item := customStack(276, 1)
	r.Edits[k] = 26
	r.Containers[k] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[k].Slots[0] = item
	r.Authors[k] = EditAuthor{Token: "owner", Before: 0, After: 26}
	s.trimChunksBudget(r, 1, 0)
	if len(r.chunks) != 519 || r.chunks[c] != nil || r.Containers[k] != nil {
		t.Fatal("bounded eviction did not retire exactly one saved chunk", len(r.chunks))
	}
	fresh := vegetationRoom()
	if _, err := s.ensureChunk(fresh, c); err != nil || fresh.Edits[k] != 26 || fresh.Authors[k].Token != "owner" || !reflect.DeepEqual(fresh.Containers[k].Slots[0], item) {
		t.Fatal("bounded eviction lost rich saved state", err)
	}
	// Fail the next save by occupying its region directory with a regular file.
	c = ChunkCoord{101, 0}
	badDir := filepath.Dir(s.chunkFile(r, c))
	if err := os.RemoveAll(badDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badDir, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	beforeEvent := s.eventID
	s.trimChunksBudget(r, 2, 0)
	if len(r.chunks) != 519 || r.chunks[c] == nil || r.SaveError == "" || s.eventID != beforeEvent {
		t.Fatal("failed save evicted a chunk or emitted successful unload")
	}
}

var benchmarkChunkTerrain []byte
var benchmarkChunkSnapshot *diskChunk

func BenchmarkChunkTerrainStreaming(b *testing.B) {
	rules, _ := loadRules()
	s, r := &Server{rules: rules}, vegetationRoom()
	base := s.generateTerrain(r, ChunkCoord{200, -190})
	b.Run("previous_full_volume_scan", func(b *testing.B) {
		edits := map[uint64]int{}
		c := ChunkCoord{200, -190}
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			copy := append([]byte(nil), base...)
			for i := range copy {
				if v, ok := edits[chunkKey(c, i)]; ok {
					copy[i] = byte(v)
				}
			}
			benchmarkChunkTerrain = encodeTerrain(copy)
		}
	})
	b.Run("cached_immutable_terrain", func(b *testing.B) {
		v := &WorldChunk{Base: base}
		chunkWireTerrain(v, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			benchmarkChunkTerrain = chunkWireTerrain(v, nil)
		}
	})
	b.Run("detached_generation_snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			benchmarkChunkSnapshot, _ = s.generateChunkSnapshot(r, ChunkCoord{n, 90})
		}
	})
}
