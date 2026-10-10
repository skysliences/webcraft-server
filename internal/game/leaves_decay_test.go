package game

import (
	"encoding/json"
	"math/rand"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func leavesTestWorld(s *Server) (*Room, *Peer) {
	r, p := mobTestWorld(s)
	p.sid = "LeafDecayPlayer123"
	r.Players[p.sid] = p.state
	return r, p
}
func leavesTestTick(s *Server, r *Room, n int) {
	for i := 0; i < n; i++ {
		s.extTick(r)
	}
}
func countTestLeaves(s *Server, r *Room, keys []uint64) int {
	n := 0
	for _, k := range keys {
		x, y, z := coords(k)
		if s.leafBlock(s.block(r, x, y, z)) {
			n++
		}
	}
	return n
}

func TestLeavesDecayActiveCrownDistanceBudgetAndPersistent(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := leavesTestWorld(s)
	trunk := blockKey(10, 100, 10)
	r.Edits[trunk] = blkOakLog
	var crown []uint64
	for x := 9; x <= 12; x++ {
		for y := 101; y <= 102; y++ {
			for z := 9; z <= 12; z++ {
				k := blockKey(x, y, z)
				r.Edits[k] = blkOakLeaves
				crown = append(crown, k)
			}
		}
	}
	for _, k := range crown {
		x, y, z := coords(k)
		if !s.hasLogNearby(r, x, y, z) {
			t.Fatalf("supported crown lost log: %d %d %d", x, y, z)
		}
	}
	if !s.setBlocks(r, [][4]int{{10, 100, 10, 0}}, 0, "test_trunk_break") {
		t.Fatal("trunk did not break")
	}
	s.runBreakHooks(p, 10, 100, 10, blkOakLog)
	leavesTestTick(s, r, 1)
	removed := len(crown) - countTestLeaves(s, r, crown)
	if removed < 1 || removed > leafDecayTickBudget {
		t.Fatalf("per tick removal budget: %d", removed)
	}
	leavesTestTick(s, r, 30)
	if remaining := countTestLeaves(s, r, crown); remaining != 0 {
		t.Fatalf("connected crown did not decay: %d", remaining)
	}

	// Exactly six leaf/log edges support leaves; an air gap does not.
	r, p = leavesTestWorld(s)
	r.Edits[blockKey(10, 100, 10)] = blkSpruceLog
	for x := 11; x <= 17; x++ {
		r.Edits[blockKey(x, 100, 10)] = blkBirchLeaves
	}
	if !s.hasLogNearby(r, 16, 100, 10) || s.hasLogNearby(r, 17, 100, 10) {
		t.Fatal("support distance must be six connected edges")
	}
	r.Edits[blockKey(13, 100, 10)] = 0
	if s.hasLogNearby(r, 16, 100, 10) {
		t.Fatal("support crossed an air gap")
	}

	// Existing placed variants and historical natural-ID player decorations persist.
	for i, b := range []int{171, 172, 173, blkOakLeaves} {
		k := blockKey(20+i, 100, 10)
		r.Edits[k] = b
		if b == blkOakLeaves {
			r.Authors[k] = EditAuthor{Token: "builder", Before: 0, After: b}
		}
		s.queueLeafCrown(r, k)
	}
	orphan := blockKey(25, 100, 10)
	r.Edits[orphan] = blkBirchLeaves
	r.Authors[orphan] = EditAuthor{Token: "stale", After: 1}
	s.queueLeafCrown(r, orphan)
	leavesTestTick(s, r, 30)
	for i, b := range []int{171, 172, 173, blkOakLeaves} {
		if r.Edits[blockKey(20+i, 100, 10)] != b {
			t.Fatal("player decoration decayed")
		}
	}
	if r.Edits[orphan] != 0 {
		t.Fatal("a stale author incorrectly protected a natural leaf")
	}
	r, _ = leavesTestWorld(s)
	r.ext().Rules = map[string]bool{"doTileDrops": false}
	noLoot := blockKey(12, 100, 10)
	r.Edits[noLoot] = blkOakLeaves
	s.queueLeafCrown(r, noLoot)
	leavesTestTick(s, r, 10)
	if r.Edits[noLoot] != 0 || len(r.Drops) != 0 {
		t.Fatal("doTileDrops=false must remove the leaf without spawning loot")
	}

	// Future species/stripped log variants use registry names, not only three IDs.
	s.rules.Names[176] = "Stripped Acacia Log"
	s.rules.Kinds[176] = 1
	s.rules.Names[177] = "Acacia Leaves"
	s.rules.Kinds[177] = 2
	r.Edits[blockKey(30, 100, 10)] = 176
	r.Edits[blockKey(31, 100, 10)] = 177
	if !s.hasLogNearby(r, 31, 100, 10) {
		t.Fatal("registry log species was ignored")
	}
	s.setBlocks(r, [][4]int{{30, 100, 10, 0}}, 0, "test_trunk_break")
	leavesTestTick(s, r, 30)
	if r.Edits[blockKey(31, 100, 10)] != 0 {
		t.Fatal("new leaf species did not decay")
	}

	// Random loot supports all three tree species and oak-only apples.
	s.rng = rand.New(rand.NewSource(6))
	seen := map[int]bool{}
	for _, b := range []int{blkOakLeaves, blkBirchLeaves, blkSpruceLeaves} {
		for i := 0; i < 5000; i++ {
			for _, v := range s.leafDecayDrops(b) {
				seen[v.ID] = true
				if b != blkOakLeaves && v.ID == s.id("Apple") {
					t.Fatal("non-oak apple drop")
				}
			}
		}
	}
	for _, name := range []string{"Oak Sapling", "Birch Sapling", "Spruce Sapling", "Apple", "Stick"} {
		if !seen[s.id(name)] {
			t.Fatalf("missing decay drop %s", name)
		}
	}
}

func TestLeavesDecayRestartAndNegativeChunkBoundary(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := leavesTestWorld(s)
	for x := 10; x < 40; x++ {
		r.Edits[blockKey(x, 100, 10)] = blkOakLeaves
	}
	r.Edits[blockKey(9, 100, 10)] = 0
	s.queueLeafCrown(r, blockKey(9, 100, 10))
	leavesTestTick(s, r, 1)
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, engine: s.engine, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.rooms[r.ID]
	q := dropTestPlayer(loaded, 2, 10, 100, 10)
	loaded.Peers[q] = true
	leavesTestTick(loader, loaded, 100)
	for x := 10; x < 40; x++ {
		if loaded.Edits[blockKey(x, 100, 10)] != 0 {
			t.Fatal("finite restart left an orphan leaf")
		}
	}

	// Unknown neighbouring chunks cannot be treated as air or generated by decay.
	r = newRoom("feed1234feed5678", "negative leaves", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	p = dropTestPlayer(r, 1, -1, 100, 8)
	r.Peers[p] = true
	c := ChunkCoord{-1, 0}
	v := &WorldChunk{Base: make([]byte, chunkVolume)}
	v.Base[chunkIndex(-1, 100, 8)] = byte(blkBirchLeaves)
	r.chunks = map[ChunkCoord]*WorldChunk{c: v}
	s.restoreChunkLeafDecay(r, c, v)
	// Other feature hooks may explore terrain independently; exercise only
	// the decay queue to verify it does not generate an unknown chunk.
	for i := 0; i < 30; i++ {
		r.rt().tickCount++
		s.leafDecayTick(r)
	}
	if b, _ := s.leafBlockAt(r, -1, 100, 8); b != blkBirchLeaves || len(r.chunks) != 1 {
		t.Fatalf("unloaded supporting chunk caused decay/exploration: leaf=%d chunks=%d", b, len(r.chunks))
	}
	other := ChunkCoord{0, 0}
	w := &WorldChunk{Base: make([]byte, chunkVolume)}
	w.Base[chunkIndex(0, 100, 8)] = byte(blkSpruceLog)
	r.chunks[other] = w
	s.restoreChunkLeafDecay(r, other, w)
	leavesTestTick(s, r, 30)
	if !s.hasLogNearby(r, -1, 100, 8) {
		t.Fatal("cross-chunk/negative support lost")
	}
	if !s.setBlocks(r, [][4]int{{0, 100, 8, 0}}, 0, "test_trunk_break") {
		t.Fatal("negative boundary trunk mutation")
	}
	s.runBreakHooks(p, 0, 100, 8, blkSpruceLog)
	// Save before the pending crown has been consumed, then reload MCC data.
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	fresh := newRoom(r.ID, r.Name, r.Mode, 0)
	fresh.Infinite = true
	fresh.Generator = generatorVersion
	for _, at := range []ChunkCoord{c, other} {
		if _, err := s.ensureChunk(fresh, at); err != nil {
			t.Fatal(err)
		}
	}
	fresh.Peers[dropTestPlayer(fresh, 2, -1, 100, 8)] = true
	leavesTestTick(s, fresh, 60)
	if b, _ := s.leafBlockAt(fresh, -1, 100, 8); b != 0 {
		t.Fatal("MCC restart left disconnected boundary leaf")
	}
	if fresh.Edits[blockKey(0, 100, 8)] != 0 {
		t.Fatal("trunk edit did not persist")
	}
}

func TestLeavesDecayRestoreWithoutEngine(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	loader := &Server{rules: s.rules}
	r := newRoom("1234567890abcdef", "storage only", "survival", 0)
	k := blockKey(12, 100, 10)
	r.Edits[k] = blkOakLeaves
	r.Edits[blockKey(12, 99, 10)] = 0
	loader.restoreLeafDecay(r)
	if b, known := loader.leafBlockAt(r, 12, 100, 10); !known || b != blkOakLeaves {
		t.Fatal("storage-only restoration lost an authoritative edit")
	}
	if _, known := loader.leafBlockAt(r, 13, 100, 10); known {
		t.Fatal("uninitialized finite base terrain must remain unknown")
	}
	for i := 0; i < 30; i++ {
		r.rt().tickCount++
		loader.leafDecayTick(r)
	}
	if r.Edits[k] != blkOakLeaves {
		t.Fatal("storage-only restoration decayed a leaf with unknown support")
	}
}

func TestLeavesDecayPluginAndPhysicsBlockChanges(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range []string{"plugin", "physics_no_neighbors"} {
		r, _ := leavesTestWorld(s)
		s.rooms[r.ID] = r
		trunk, leaf := blockKey(12, 100, 10), blockKey(12, 101, 10)
		r.Edits[trunk], r.Edits[leaf] = blkBirchLog, blkBirchLeaves
		if entry == "plugin" {
			s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 100, Z: 10, Block: 0})
		} else if !s.setBlocksWithContext(r, [][4]int{{12, 100, 10, 0}}, setNoNeighbors, "test_physics", nil, map[string]any{"actor": "server"}) {
			t.Fatal("protected physics failed")
		}
		if r.Edits[trunk] != 0 || r.rt().leafDecay == nil {
			t.Fatalf("%s did not schedule the changed crown", entry)
		}
		leavesTestTick(s, r, 10)
		if r.Edits[leaf] != 0 {
			t.Fatalf("%s left an unsupported leaf", entry)
		}
	}
}

func TestLeavesDecayPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "leaf-decay-fixture" {
		return
	}
	p := api.New()
	names := []string{api.LeavesDecaying, api.BlockPhysicsChanging, api.BlockBreaking, api.ItemSpawning}
	for _, name := range names {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if e.String("reason") != "leaf_decay" {
				return nil
			}
			if e.Source == "plugin:cancel:"+e.Name {
				e.Cancel("leaves.protected")
			}
			if e.Name == api.ItemSpawning {
				if e.Source == "plugin:invalid_drop" {
					e.Data["count"] = 0
				}
				if e.Source == "plugin:count_drop" {
					e.Data["count"] = 2
				}
			}
			// Read-only attempts must not change the leaf or its full item metadata.
			e.Data["block"] = 1
			e.Data["actor"] = "changed"
			e.Data["stack"] = &api.ItemStack{ID: 1, Count: 64}
			return nil
		})
	}
	for _, name := range append(names, api.LeavesDecayed, api.BlockBroken, api.BlockPhysicsChanged, api.ItemSpawned) {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			if e.String("reason") != "leaf_decay" {
				return nil
			}
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
			e.Cancel("monitor must be ignored")
			e.Data["block"] = 1
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func leavesPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "leaf-decay-test")
	os.MkdirAll(entry, 0700)
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := "{\"id\":\"leaf-decay-test\",\"name\":\"leaf-decay-test\",\"version\":\"1\",\"apiVersion\":1,\"entry\":\"plugin\",\"args\":[\"-test.run=^TestLeavesDecayPluginProcess$\",\"leaf-decay-fixture\"]}"
	if err = os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestLeavesDecayProtectedTransactionsSnapshotsAndDrops(t *testing.T) {
	s := leavesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	k := blockKey(12, 100, 10)
	for _, name := range []string{api.LeavesDecaying, api.BlockPhysicsChanging, api.BlockBreaking, api.ItemSpawning} {
		r, _ := leavesTestWorld(s)
		r.Edits[k] = blkOakLeaves
		s.pluginSource = "plugin:cancel:" + name
		original := customStack(s.id("Stick"), 1)
		saved := copyStack(original)
		beforeID := r.NextDropID
		if s.decayLeafWithDrops(r, k, blkOakLeaves, Stacks{original}) || r.Edits[k] != blkOakLeaves || len(r.Drops) != 0 || r.NextDropID != beforeID || !reflect.DeepEqual(original, saved) {
			t.Fatalf("%s cancellation committed a leaf/drop change", name)
		}
	}
	for _, why := range []string{"invalid_drop", "capacity", "invalid_meta", "stale"} {
		r, _ := leavesTestWorld(s)
		r.Edits[k] = blkOakLeaves
		s.pluginSource = "plugin:" + why
		loot := customStack(s.id("Stick"), 1)
		beforeID := r.NextDropID
		switch why {
		case "capacity":
			for i := uint32(1); i <= maxWorldDrops; i++ {
				r.Drops[i] = &ItemDrop{ID: i, Stack: stack(s.id("Stick"), 1, 0), X: 12.5, Y: 100.5, Z: 10.5}
			}
		case "invalid_meta":
			loot.Meta.Lore = []string{strings.Repeat("x", 10000)}
		case "stale":
			r.Edits[k] = blkBirchLeaves
		}
		beforeCount := len(r.Drops)
		if s.decayLeafWithDrops(r, k, blkOakLeaves, Stacks{loot}) || r.Edits[k] == 0 || len(r.Drops) != beforeCount || r.NextDropID != beforeID {
			t.Fatalf("%s failure lost a leaf or published loot", why)
		}
	}
	r, _ := leavesTestWorld(s)
	r.Edits[k] = blkOakLeaves
	original := customStack(s.id("Stick"), 1)
	original.Ench = map[string]int{"luck": 1}
	original.Meta.Attributes = []api.AttributeModifier{{ID: "test:leaf_loot", Attribute: api.AttackDamage, Amount: 2, Operation: api.Add, Slot: "mainhand"}}
	original.Meta.Unbreakable = true
	original.Meta.CustomData["test:leaf_snapshot"] = map[string]any{"tree": "oak", "nested": []any{float64(1), "retained"}}
	expected := copyStack(original)
	s.pluginSource = "plugin:count_drop"
	if !s.decayLeafWithDrops(r, k, blkOakLeaves, Stacks{original}) || r.Edits[k] != 0 || len(r.Drops) != 1 {
		t.Fatal("valid leaf decay did not commit")
	}
	for _, d := range r.Drops {
		if d.Stack.Count != 2 || !same(d.Stack, expected) {
			t.Fatal("drop count mutation lost full metadata")
		}
	}
	if !reflect.DeepEqual(original, expected) {
		t.Fatal("preparation mutated original loot")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, engine: s.engine, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	for _, d := range loader.rooms[r.ID].Drops {
		if d.Stack.Count != 2 || !same(d.Stack, expected) {
			t.Fatal("leaf drop persistence lost name, Lore, attributes, custom data or enchantments")
		}
	}
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "leaf-decay-test", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	successes := map[string]int{}
	seenSnapshot := false
	seenBlockSnapshot := false
	seenDropSnapshot := false
	lastEvent := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Name == api.LeavesDecayed || e.Name == api.BlockBroken || e.Name == api.BlockPhysicsChanged || e.Name == api.ItemSpawned {
			successes[e.Source]++
		}
		if e.Name == api.LeavesDecayed && e.Source == "plugin:count_drop" {
			if e.String("actor") != "server" || e.Number("block") != 0 || e.Item("stack").ID != blkOakLeaves {
				t.Fatal("read-only or Monitor changed committed leaf event")
			}
			encoded, _ := json.Marshal(e.Data["drops"])
			var drops []*api.ItemStack
			json.Unmarshal(encoded, &drops)
			if len(drops) != 1 || drops[0].Count != 2 || !reflect.DeepEqual(drops[0].Meta, itemSnapshot(expected).Meta) {
				t.Fatal("leaf completion lacks actual full loot snapshots")
			}
			if lastEvent[e.Source] != api.BlockPhysicsChanged {
				t.Fatal("leaf success preceded block commit event")
			}
			seenSnapshot = true
		}
		if e.Source == "plugin:count_drop" && (e.Name == api.BlockBreaking || e.Name == api.BlockBroken || e.Name == api.BlockPhysicsChanging || e.Name == api.BlockPhysicsChanged) {
			if e.String("actor") != "server" || e.Number("block") != 0 || e.Number("oldBlock") != blkOakLeaves || e.Item("leaf").ID != blkOakLeaves || e.Item("stack").ID != blkOakLeaves {
				t.Fatal("protected block event lacks server leaf snapshots")
			}
			encoded, _ := json.Marshal(e.Data["drops"])
			var drops []*api.ItemStack
			json.Unmarshal(encoded, &drops)
			if len(drops) != 1 || drops[0].Count != 2 || !reflect.DeepEqual(drops[0].Meta, itemSnapshot(expected).Meta) {
				t.Fatal("protected block event lacks actual full loot snapshots")
			}
			seenBlockSnapshot = true
		}
		if e.Source == "plugin:count_drop" && (e.Name == api.ItemSpawning || e.Name == api.ItemSpawned) {
			if e.String("actor") != "server" || e.Item("leaf").ID != blkOakLeaves || !reflect.DeepEqual(e.Item("stack").Meta, itemSnapshot(expected).Meta) || e.Data["target"] == nil {
				t.Fatal("drop event lacks actor, target or full leaf/loot metadata")
			}
			seenDropSnapshot = true
		}
		lastEvent[e.Source] = e.Name
	}
	for _, name := range []string{api.LeavesDecaying, api.BlockPhysicsChanging, api.BlockBreaking, api.ItemSpawning, "invalid_drop", "capacity", "invalid_meta", "stale"} {
		source := "plugin:" + name
		if strings.Contains(name, ".") {
			source = "plugin:cancel:" + name
		}
		if successes[source] != 0 {
			t.Fatalf("cancelled %s emitted success", source)
		}
	}
	if !seenSnapshot {
		t.Fatal("missing leaf success snapshot")
	}
	if !seenBlockSnapshot {
		t.Fatal("missing protected block snapshot")
	}
	if !seenDropSnapshot {
		t.Fatal("missing drop snapshot")
	}
}
