package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func tntTestWorld(s *Server) (*Room, *Peer, uint64) {
	r, p := redstoneTestPeer(s, itemFlintAndSteel)
	p.state.Inv.Slots[0] = customStack(itemFlintAndSteel, 1)
	k := blockKey(12, 60, 10)
	r.Edits[k] = blkExplosiveTNT
	s.rooms[r.ID] = r
	r.Players[p.sid] = p.state
	return r, p, k
}
func TestTNTDefaultFuseSustainedPowerAndMiningProtection(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, k := tntTestWorld(s)
	r.Edits[blockKey(13, 60, 10)] = 109
	s.trackRedstoneBlock(r, blockKey(13, 60, 10), 109)
	for _, h := range blockUpdateHooks {
		h(s, r, 12, 60, 10, blkExplosiveTNT)
	}
	if !s.primedTNT(r, k) || r.Ext.Blocks[k].Fuse != 40 {
		t.Fatal("redstone torch did not prime TNT")
	}
	// Repeated powered-neighbour notifications do not shorten/reset the fuse.
	for i := 0; i < 50; i++ {
		for _, h := range blockUpdateHooks {
			h(s, r, 12, 60, 10, blkExplosiveTNT)
		}
	}
	if r.Ext.Blocks[k].Fuse != 40 {
		t.Fatal("neighbour update changed fuse")
	}
	if s.allowBlock(p, k, 0) || s.setBlock(r, 12, 60, 10, 0, 0) {
		t.Fatal("primed TNT can be defused by ordinary break")
	}
	s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 12, Y: 60, Z: 10, Block: 0})
	if s.block(r, 12, 60, 10) != blkExplosiveTNT {
		t.Fatal("plugin setblock defused TNT")
	}
	s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 60, Z: 10, F: 1})
	if p.mining != nil {
		p.mining.At = time.Now().Add(-time.Minute)
	}
	s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 60, Z: 10, F: 1})
	if !s.primedTNT(r, k) || len(r.Drops) != 0 {
		t.Fatal("mining produced duplicate TNT or removed fuse")
	}
	for i := 0; i < 39; i++ {
		s.tickPrimedTNT(r)
	}
	if s.block(r, 12, 60, 10) != blkExplosiveTNT || r.Ext.Blocks[k].Fuse != 1 {
		t.Fatal("TNT exploded before four seconds")
	}
	s.tickPrimedTNT(r)
	if s.block(r, 12, 60, 10) != 0 || r.Ext.Blocks[k] != nil {
		t.Fatal("sustained power prevented explosion")
	}
	for _, d := range r.Drops {
		if d.Stack.ID == blkExplosiveTNT {
			t.Fatal("primed source dropped TNT")
		}
	}
}

func TestTNTNormalTorchAndAttachmentIsolation(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, face := range []int{1, 2, 3, 4, 5} {
		r, _, k := tntTestWorld(s)
		off := faceOffsets[face]
		tx, ty, tz := 12+off[0], 60+off[1], 10+off[2]
		r.Edits[blockKey(tx, ty, tz)] = 109
		storeRedstoneComponent(r, blockKey(tx, ty, tz), &RedstoneComponent{Kind: "torch", SupportFace: face})
		if s.directPowerSource(r, tx, ty, tz, -off[0], -off[1], -off[2]) != 0 || s.blockPowered(r, 12, 60, 10) {
			t.Fatal("redtorch powered attached support", face)
		}
		for _, h := range blockUpdateHooks {
			h(s, r, 12, 60, 10, blkExplosiveTNT)
		}
		if s.primedTNT(r, k) {
			t.Fatal("attached TNT was primed by torch", face)
		}
	}
	r, _, k := tntTestWorld(s)
	r.Edits[blockKey(13, 60, 10)] = torchBlock
	for _, h := range blockUpdateHooks {
		h(s, r, 12, 60, 10, blkExplosiveTNT)
	}
	if s.primedTNT(r, k) || s.emittedPower(r, 13, 60, 10, -1, 0, 0) != 0 {
		t.Fatal("normal illumination torch powers TNT")
	}
}

func TestTNTInvalidRequestsCreativeAndLegacy(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, why := range []string{"dead", "adventure", "spectator", "lock", "far", "stale", "invalid_fuse"} {
		r, p, k := tntTestWorld(s)
		ctx := &api.Context{}
		ctx.PrimeTNT(s.event(api.CommandExecuting, r, p, nil), 12, 60, 10, 40)
		switch why {
		case "dead":
			p.state.Dead = true
		case "adventure", "spectator":
			p.ext().GameMode = why
		case "lock":
			r.Lock = 2
		case "far":
			p.state.X = 100
		case "stale":
			p.state.Inv.Slots[0].Meta.Lore[0] = "changed"
		case "invalid_fuse":
			ctx.Actions[0].FuseTicks = 12001
		}
		before := copyStack(s.heldItem(p))
		s.applyAction(ctx.Actions[0])
		if s.primedTNT(r, k) || !reflect.DeepEqual(before, s.heldItem(p)) {
			t.Fatal("invalid SDK TNT action committed", why)
		}
	}
	r, p, k := tntTestWorld(s)
	p.ext().GameMode = "creative"
	before := copyStack(s.heldItem(p))
	if !s.primeTNT(r, p, 12, 60, 10, maxTNTFuse, "flint_and_steel", nil) || r.Ext.Blocks[k].Fuse != maxTNTFuse || !reflect.DeepEqual(before, s.heldItem(p)) {
		t.Fatal("creative flint consumed durability or rejected maximum fuse")
	}
	r, p, k = tntTestWorld(s)
	r.blockExt().Blocks = map[uint64]*BlockData{k: {Fuse: 2}}
	s.tickPrimedTNT(r)
	if r.Ext.Blocks[k].Fuse != 1 || r.Ext.Blocks[k].TNT == nil || r.Ext.Blocks[k].TNT.Actor != "server" {
		t.Fatal("legacy TNT fuse was not continued")
	}
	s.tickPrimedTNT(r)
	if s.block(r, 12, 60, 10) != 0 {
		t.Fatal("legacy TNT never detonated")
	}
}

func TestTNTMCCPersistenceAndSnapshotReplication(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("tntchunkrestore1", "TNT区块恢复", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	k := blockKey(-17, 95, 18000)
	r.Edits[k] = blkExplosiveTNT
	c := chunkCoord(-17, 18000)
	origin := &TNTState{Actor: "5ab5ef00c7708e582a40d01669f0af7c", Source: "plugin:origin", Reason: "plugin", Slot: 0, Stack: itemSnapshot(customStack(itemFlintAndSteel, 1))}
	if !s.primeTNT(r, nil, -17, 95, 18000, 17, "plugin", origin) {
		t.Fatal("negative coordinate TNT prime failed")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	fresh := newRoom(r.ID, r.Name, r.Mode, 0)
	fresh.Infinite = true
	fresh.Generator = generatorVersion
	if _, err := s.ensureChunk(fresh, c); err != nil {
		t.Fatal(err)
	}
	if !s.primedTNT(fresh, k) || fresh.Ext.Blocks[k].Fuse != 17 || !reflect.DeepEqual(fresh.Ext.Blocks[k].TNT, origin) {
		t.Fatal("MCC restore lost fuse/metadata")
	}
	p := dropTestPlayer(fresh, 2, -17.5, 95, 18000.5)
	p.epoch = 77
	p.originX = -32
	p.originZ = 17984
	p.known = map[int]bool{}
	// The initial snapshot is independent from dynamic chunk indices.
	p.rd = 8
	p.known[p.localChunk(c)] = true
	fresh.Peers[p] = true
	s.syncTNTFor(p)
	var packet struct {
		T      string `json:"t"`
		Epoch  uint32 `json:"epoch"`
		Reset  bool   `json:"reset"`
		Blocks []struct {
			X, Y, Z, FuseTicks int
			Primed             bool
			Actor, Source      string
		} `json:"blocks"`
	}
	for len(p.send) > 0 {
		raw := <-p.send
		json.Unmarshal(raw.body, &packet)
		if packet.T == "tnt" {
			break
		}
	}
	if packet.T != "tnt" || packet.Epoch != 77 || !packet.Reset || len(packet.Blocks) != 1 || packet.Blocks[0].X != -17 || packet.Blocks[0].FuseTicks != 17 || !packet.Blocks[0].Primed || packet.Blocks[0].Actor != origin.Actor {
		t.Fatal("restored TNT did not replicate absolute coordinates/state", packet)
	}
	for _, bad := range []*TNTState{{Actor: "server", Slot: 99}, {Actor: "server", Slot: 0, Stack: &api.ItemStack{ID: 305, Count: 1, Meta: &api.ItemMeta{Lore: []string{strings.Repeat("x", 161)}}}}} {
		if s.validTNTState(bad) {
			t.Fatal("invalid TNT metadata accepted")
		}
	}
}

func TestTNTFlintFireChainAndPersistedAttribution(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, k := tntTestWorld(s)
	s.pluginSource = "plugin:igniter"
	before := itemSnapshot(s.heldItem(p))
	if !s.primeTNT(r, p, 12, 60, 10, 40, "flint_and_steel", nil) || s.heldItem(p).Damage != before.Damage+1 {
		t.Fatal("flint failed to prime / wear")
	}
	if r.Ext.Blocks[k].TNT.Actor != p.sid || r.Ext.Blocks[k].TNT.Attacker.PlayerID != p.id || !reflect.DeepEqual(r.Ext.Blocks[k].TNT.Stack, before) {
		t.Fatal("ignition did not preserve full attacker/item snapshot")
	}
	for i := 0; i < 7; i++ {
		s.tickPrimedTNT(r)
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, engine: s.engine, rng: s.rng}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	fresh := loader.rooms[r.ID]
	if fresh.Ext.Blocks[k].Fuse != 33 || !reflect.DeepEqual(fresh.Ext.Blocks[k].TNT.Stack, before) || fresh.Ext.Blocks[k].TNT.Actor != p.sid {
		t.Fatal("restart lost fuse or origin")
	}
	// No scheduler is restored: only persistent counters drive detonation.
	q := dropTestPlayer(fresh, 2, 20.5, 60, 20.5)
	fresh.Peers[q] = true
	for i := 0; i < 32; i++ {
		loader.tickPrimedTNT(fresh)
	}
	if loader.block(fresh, 12, 60, 10) != blkExplosiveTNT {
		t.Fatal("restored fuse ended early")
	}
	loader.tickPrimedTNT(fresh)
	if loader.block(fresh, 12, 60, 10) != 0 {
		t.Fatal("restored fuse never detonated")
	}
	// A chain receives the original flint metadata, account and plugin source.
	r, p, k = tntTestWorld(s)
	source := s.ignitionOrigin(p, "flint_and_steel")
	r.Edits[blockKey(14, 60, 10)] = blkExplosiveTNT
	if !s.explodeAttributed(r, 12.5, 60.5, 10.5, 4, true, source) || !s.primedTNT(r, blockKey(14, 60, 10)) {
		t.Fatal("blast did not chain-prime TNT")
	}
	chain := r.Ext.Blocks[blockKey(14, 60, 10)].TNT
	if chain.Actor != p.sid || chain.Reason != "explosion_chain" || !reflect.DeepEqual(chain.Stack, source.Stack) || chain.Source != "plugin:igniter" {
		t.Fatal("chain lost attribution")
	}
	// Fire touching TNT uses the same priming path with its igniter provenance.
	r, p, k = tntTestWorld(s)
	r.Edits[blockKey(13, 60, 10)] = blkFlintFire
	s.inheritFire(r, blockKey(13, 60, 10), s.ignitionOrigin(p, "flint_and_steel"))
	for _, h := range blockUpdateHooks {
		h(s, r, 12, 60, 10, blkExplosiveTNT)
	}
	if !s.primedTNT(r, k) || r.Ext.Blocks[k].TNT.Reason != "fire" || r.Ext.Blocks[k].TNT.Actor != p.sid {
		t.Fatal("fire lost priming origin")
	}
}

func TestTNTPluginFixtureProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "tnt-lifecycle-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.TNTPriming, api.InventoryChanging, api.ItemWearing, api.ExplosionCreating, api.BlockBreaking, api.BlockPhysicsChanging, api.ItemSpawning} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			switch e.Source {
			case "plugin:tnt-prime-cancel":
				if e.Name == api.TNTPriming {
					e.Cancel("tnt.prime_protected")
				}
			case "plugin:tnt-inv-cancel":
				if e.Name == api.InventoryChanging {
					e.Cancel("tnt.inventory_protected")
				}
			case "plugin:tnt-blast-cancel":
				if e.Name == api.ExplosionCreating {
					e.Cancel("tnt.blast_protected")
				}
			case "plugin:tnt-source-cancel":
				if e.Name == api.BlockBreaking && e.String("reason") == "tnt_explosion" {
					e.Cancel("tnt.source_protected")
				}
			case "plugin:tnt-short":
				if e.Name == api.TNTPriming {
					e.Data["fuseTicks"] = 2
				}
			case "plugin:tnt-fraction":
				if e.Name == api.TNTPriming {
					e.Data["fuseTicks"] = 1.5
				}
			case "plugin:tnt-zero":
				if e.Name == api.TNTPriming {
					e.Data["fuseTicks"] = 0
				}
			case "plugin:tnt-large":
				if e.Name == api.TNTPriming {
					e.Data["fuseTicks"] = 12001
				}
			case "plugin:tnt-drop-cancel":
				if e.Name == api.ItemSpawning {
					e.Cancel("tnt.drop_protected")
				}
			case "plugin:tnt-cell-cancel":
				if e.Name == api.BlockPhysicsChanging {
					e.Cancel("tnt.cell_protected")
				}
			case "plugin:tnt-text":
				if e.Name == api.TNTPriming {
					e.Data["fuseTicks"] = "fast"
				}
			}
			e.Data["actor"] = "forged"
			e.Data["stack"] = &api.ItemStack{ID: 1, Count: 64}
			return nil
		})
	}
	for _, name := range []string{api.TNTPriming, api.TNTPrimed, api.InventoryChanging, api.InventoryChanged, api.ItemWorn, api.ExplosionCreating, api.ExplosionCreated, api.BlockBreaking, api.BlockBroken, api.PlayerDamaging, api.PlayerDamaged, api.EntityDamaging, api.EntityDamaged, api.BlockPhysicsChanging, api.BlockPhysicsChanged, api.ItemSpawning, api.ItemSpawned} {
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
			e.Data["fuseTicks"] = 1
			c.PrimeTNT(e, 12, 60, 10, 1)
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func tntPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "tnt-test")
	os.MkdirAll(entry, 0700)
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(`{"id":"tnt-test","name":"tnt-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestTNTPluginFixtureProcess$","tnt-lifecycle-fixture"]}`), 0600)
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func tntEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "tnt-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := []*api.Event{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, &e)
	}
	return out
}
func TestTNTPluginCancellationMutableFuseSDKAndDamage(t *testing.T) {
	s := tntPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range []string{"plugin:tnt-prime-cancel", "plugin:tnt-inv-cancel", "plugin:tnt-fraction", "plugin:tnt-zero", "plugin:tnt-large", "plugin:tnt-text"} {
		r, p, k := tntTestWorld(s)
		s.pluginSource = source
		before := copyStack(s.heldItem(p))
		start := len(tntEvents(t, s))
		if s.primeTNT(r, p, 12, 60, 10, 40, "flint_and_steel", nil) || s.primedTNT(r, k) || !reflect.DeepEqual(before, s.heldItem(p)) {
			t.Fatal("cancelled/invalid prime committed", source)
		}
		for _, e := range tntEvents(t, s)[start:] {
			if e.Name == api.TNTPrimed || e.Name == api.InventoryChanged || e.Name == api.ItemWorn {
				t.Fatal("rejected prime emitted success", source, e.Name)
			}
		}
	}
	r, p, k := tntTestWorld(s)
	s.pluginSource = "plugin:tnt-short"
	start := len(tntEvents(t, s))
	ctx := &api.Context{}
	ctx.PrimeTNT(s.event(api.CommandExecuting, r, p, nil), 12, 60, 10, 40)
	s.applyAction(ctx.Actions[0])
	if !s.primedTNT(r, k) || r.Ext.Blocks[k].Fuse != 2 || s.heldItem(p).Damage != 0 {
		t.Fatal("SDK mutable fuse / monitor readonly failed")
	}
	primed := 0
	for _, e := range tntEvents(t, s)[start:] {
		if e.Name == api.TNTPrimed {
			primed++
			if e.Number("fuseTicks") != 2 || e.String("actor") != p.sid || e.Item("stack").Meta.Lore[0] != "第一行" || e.Data["target"] == nil {
				t.Fatal("TNT event lost readonly metadata", e)
			}
		}
	}
	if primed != 1 {
		t.Fatal("TNT success missing")
	}
	// The stored igniter survives later changes to the player's current tool.
	p.state.Inv.Slots[0] = stack(1, 64, 0)
	s.tickPrimedTNT(r)
	if !s.primedTNT(r, k) {
		t.Fatal("short fuse ended early")
	}
	s.tickPrimedTNT(r)
	damaged := false
	for _, e := range tntEvents(t, s)[start:] {
		if e.Name == api.PlayerDamaged && e.Damage != nil && e.Damage.Cause == "explosion" {
			damaged = true
			if e.Damage.Actor != p.sid || e.Damage.Source != "plugin:tnt-short" || e.Damage.Attacker.PlayerID != p.id || e.Damage.Weapon.Meta.Lore[0] != "第一行" || e.Damage.HealthBefore <= e.Damage.HealthAfter || e.Damage.AppliedAmount <= 0 {
				t.Fatal("explosion damage lost origin/result", e)
			}
		}
	}
	if !damaged {
		t.Fatal("attributed explosion damage missing")
	}
	for _, source := range []string{"plugin:tnt-blast-cancel", "plugin:tnt-source-cancel"} {
		r, p, k := tntTestWorld(s)
		s.pluginSource = source
		if !s.primeTNT(r, p, 12, 60, 10, 1, "plugin", nil) {
			t.Fatal("test prime failed")
		}
		start = len(tntEvents(t, s))
		health := p.state.Health
		s.tickPrimedTNT(r)
		if !s.primedTNT(r, k) || r.Ext.Blocks[k].Fuse != 10 || p.state.Health != health || len(r.Drops) != 0 {
			t.Fatal("protected explosion did not retain consistent pending TNT", source)
		}
		for _, e := range tntEvents(t, s)[start:] {
			if e.Name == api.ExplosionCreated || e.Name == api.PlayerDamaged || e.Name == api.BlockBroken {
				t.Fatal("protected explosion emitted success", source, e.Name)
			}
		}
	}
}

func TestTNTExplosionDropAndPhysicsProtectionAtomic(t *testing.T) {
	s := tntPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range []string{"plugin:tnt-drop-cancel", "plugin:tnt-cell-cancel"} {
		r, p, k := tntTestWorld(s)
		r.Edits[k] = 1
		s.pluginSource = source
		origin := s.ignitionOrigin(p, "flint_and_steel")
		id := r.NextDropID
		start := len(tntEvents(t, s))
		if s.explosionBlock(r, 12, 60, 10, 1, Stacks{customStack(1, 2)}, origin) || s.block(r, 12, 60, 10) != 1 || len(r.Drops) != 0 || r.NextDropID != id {
			t.Fatal("protected explosion cell committed/reserved IDs", source)
		}
		for _, e := range tntEvents(t, s)[start:] {
			if e.Name == api.BlockBroken || e.Name == api.BlockPhysicsChanged || e.Name == api.ItemSpawned {
				t.Fatal("protected explosion cell emitted success", source, e.Name)
			}
		}
	}
	r, p, k := tntTestWorld(s)
	r.Edits[k] = 1
	s.pluginSource = "plugin:tnt-cell-success"
	origin := s.ignitionOrigin(p, "flint_and_steel")
	start := len(tntEvents(t, s))
	if !s.explosionBlock(r, 12, 60, 10, 1, Stacks{customStack(1, 2)}, origin) || s.block(r, 12, 60, 10) != 0 || len(r.Drops) != 1 {
		t.Fatal("explosion cell did not commit block/drop")
	}
	seenDrop, seenBlock := false, false
	for _, e := range tntEvents(t, s)[start:] {
		if e.Name == api.ItemSpawned {
			seenDrop = true
			if e.String("reason") != "explosion" || e.String("actor") != p.sid || e.Item("stack").ID != 1 || e.Item("stack").Meta.Lore[0] != "第一行" || e.Item("weapon").ID != 305 || e.Data["origin"] == nil {
				t.Fatal("drop lost origin/metadata", e)
			}
		}
		if e.Name == api.BlockPhysicsChanged {
			seenBlock = true
			if e.String("actor") != p.sid || e.String("reason") != "explosion" || e.Item("stack").ID != 305 || e.Data["target"] == nil {
				t.Fatal("physics lost actor/item/target", e)
			}
		}
	}
	if !seenDrop || !seenBlock {
		t.Fatal("explosion cell completion missing")
	}
	r, p, k = tntTestWorld(s)
	r.Edits[k] = 1
	r.ext().Rules = map[string]bool{"doTileDrops": false}
	origin = s.ignitionOrigin(p, "flint_and_steel")
	if !s.explosionBlock(r, 12, 60, 10, 1, Stacks{customStack(1, 2)}, origin) || len(r.Drops) != 0 {
		t.Fatal("doTileDrops=false created loot or prevented explosion")
	}
	for _, dropsEnabled := range []bool{false, true} {
		r, p, k = tntTestWorld(s)
		r.Edits[k] = 26
		r.Containers[k] = &Container{Kind: "chest", Slots: Stacks{customStack(305, 1)}}
		origin = s.ignitionOrigin(p, "flint_and_steel")
		r.ext().Rules = map[string]bool{"doTileDrops": dropsEnabled}
		s.pluginSource = "plugin:tnt-drop-cancel"
		ok := s.explosionBlock(r, 12, 60, 10, 26, nil, origin)
		if dropsEnabled {
			if ok || s.block(r, 12, 60, 10) != 26 || r.Containers[k] == nil || len(r.Drops) != 0 {
				t.Fatal("cancelled container contents were destroyed")
			}
		} else {
			if !ok || s.block(r, 12, 60, 10) != 0 || r.Containers[k] != nil || len(r.Drops) != 0 {
				t.Fatal("tileDrops=false leaked container contents")
			}
		}
	}
}
