package game

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func transportTestWorld(s *Server) (*Room, *Peer) {
	r, p := mobTestWorld(s)
	p.sid = "TransportPlayer123"
	r.Players[p.sid] = p.state
	s.rooms[r.ID] = r
	return r, p
}
func transportTestRail(r *Room, x, y, z, b int, shape string, powered bool) {
	k := blockKey(x, y, z)
	r.Edits[k] = b
	storeRail(r, k, &RailState{Kind: railKind(b), Shape: shape, Powered: powered})
}
func transportTestCart(r *Room, x, y, z float64) *Vehicle {
	v := &Vehicle{ID: vehicleNamespace | 1, Type: "minecart", X: x, Y: y, Z: z, Health: 20, Item: customStack(itemMinecart, 1)}
	vehicleStore(r)[v.ID] = v
	r.Ext.NextVehicleID = 1
	syncVehicleView(r)
	return v
}
func TestVehicleControlsVisibilityDamageAndRestart(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := transportTestWorld(s)
	for x := 12; x <= 24; x++ {
		transportTestRail(r, x, 60, 10, blkRail, "east_west", false)
	}
	p.state.Inv.Slots[0] = customStack(itemMinecart, 1)
	original := copyStack(p.state.Inv.Slots[0])
	if !s.useVehicle(p, Message{X: 12, Y: 60, Z: 10, F: 1}, itemSnapshot(original), true) || len(vehicleStore(r)) != 1 || s.heldItem(p) != nil || len(r.Mobs) != 0 {
		t.Fatal("cart placement did not consume/commit independently of mobs")
	}
	var v *Vehicle
	for _, v = range vehicleStore(r) {
	}
	if !same(v.Item, original) {
		t.Fatal("placed cart lost full item metadata")
	}
	q := dropTestPlayer(r, 2, 12.5, 60, 13)
	q.sid = "TransportObserver123"
	r.Peers[q] = true
	s.syncTransport(r)
	for _, peer := range []*Peer{p, q} {
		seen := false
		for len(peer.send) > 0 {
			out := <-peer.send
			var m struct {
				T        string `json:"t"`
				Vehicles []struct {
					ID   uint32         `json:"id"`
					Item *api.ItemStack `json:"item"`
				} `json:"vehicles"`
			}
			if json.Unmarshal(out.body, &m) == nil && m.T == "vehicles" && len(m.Vehicles) == 1 {
				seen = m.Vehicles[0].ID == v.ID && reflect.DeepEqual(m.Vehicles[0].Item.Meta, itemSnapshot(original).Meta)
			}
		}
		if !seen {
			t.Fatal("placement was not visible with metadata to both players")
		}
	}
	if !s.interactVehicle(p, v.ID) || p.state.Mount != v.ID || v.RiderID != p.id {
		t.Fatal("mount failed")
	}
	before := [3]float64{p.state.X, p.state.Y, p.state.Z}
	packet := make([]byte, 18)
	packet[0] = 1
	for i, n := range []float32{200, 120, 200} {
		binary.LittleEndian.PutUint32(packet[1+i*4:], math.Float32bits(n))
	}
	packet[17] = 3
	s.binary(p, packet)
	if [3]float64{p.state.X, p.state.Y, p.state.Z} != before || p.state.Fly != 0 {
		t.Fatal("mounted client position/flying bypassed server authority")
	}
	for i := 0; i < 15; i++ {
		r.rt().tickCount++
		s.controlVehicle(p, Message{Forward: 1})
		s.tickVehicles(r)
	}
	if v.X <= 13 || p.state.X != v.X || p.state.Y != v.Y+.35 || p.state.Z != v.Z {
		t.Fatal("rider input did not move along rails authoritatively")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	restored := loader.rooms[r.ID]
	if got := vehicleStore(restored)[v.ID]; got == nil || !same(got.Item, original) || got.X != v.X || got.RiderID != 0 || got.Rider != "" || restored.Players[p.sid].Mount != 0 {
		t.Fatal("vehicle restart lost metadata/position or restored a stale rider")
	}
	if !s.exitVehicle(p, "test_exit") || v.RiderID != 0 || p.state.Mount != 0 {
		t.Fatal("safe dismount failed")
	}
	weapon := customStack(s.id("Diamond Sword"), 1)
	ctx := &api.DamageContext{Cause: "plugin", Actor: "tester", Weapon: itemSnapshot(weapon)}
	if !s.damageVehicleSource(r, nil, v, 7, "test_damage", ctx) || v.Health != 13 || ctx.AppliedAmount != 7 {
		t.Fatal("vehicle damage settlement failed")
	}
	if !s.damageVehicleSource(r, nil, v, 100, "test_damage", ctx) || vehicleStore(r)[v.ID] != nil || len(r.Drops) != 1 {
		t.Fatal("destroyed vehicle did not become its original item")
	}
	for _, d := range r.Drops {
		if !same(d.Stack, original) {
			t.Fatal("destroyed vehicle loot lost metadata")
		}
	}
	s.syncTransportFor(q)
	seenGone := false
	for len(q.send) > 0 {
		out := <-q.send
		var m struct {
			T        string `json:"t"`
			Vehicles []any  `json:"vehicles"`
		}
		if json.Unmarshal(out.body, &m) == nil && m.T == "vehicles" && len(m.Vehicles) == 0 {
			seenGone = true
		}
	}
	if !seenGone {
		t.Fatal("destroyed vehicle remains visible to another player")
	}
}
func TestVehicleCurvesSlopesDetectorAndBoatInput(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := transportTestWorld(s)
	transportTestRail(r, 12, 60, 9, blkRail, "north_south", false)
	transportTestRail(r, 12, 60, 10, blkRail, "north_east", false)
	for x := 13; x <= 20; x++ {
		transportTestRail(r, x, 60, 10, blkRail, "east_west", false)
	}
	v := transportTestCart(r, 12.5, 60.1, 9.7)
	v.VZ = .22
	for i := 0; i < 18; i++ {
		r.rt().tickCount++
		s.tickVehicles(r)
	}
	if v.X < 13.4 || math.Abs(v.Z-10.5) > .07 || v.VX <= 0 || math.Abs(v.VZ) > .03 {
		t.Fatalf("cart failed curved rail: %+v", v)
	}
	r, p = transportTestWorld(s)
	transportTestRail(r, 12, 60, 10, blkRail, "ascending_east", false)
	for x := 13; x <= 20; x++ {
		r.Edits[blockKey(x, 60, 10)] = 1
		transportTestRail(r, x, 61, 10, blkRail, "east_west", false)
	}
	v = transportTestCart(r, 12.2, 60.3, 10.5)
	v.VX = .3
	for i := 0; i < 10; i++ {
		r.rt().tickCount++
		s.tickVehicles(r)
	}
	if v.X <= 13 || v.Y < 61.05 {
		t.Fatalf("cart failed ascending rail: %+v", v)
	}
	r, p = transportTestWorld(s)
	for x := 12; x <= 20; x++ {
		transportTestRail(r, x, 60, 10, blkRail, "east_west", false)
	}
	transportTestRail(r, 13, 60, 10, blkPoweredRail, "east_west", true)
	transportTestRail(r, 14, 60, 10, blkDetectorRail, "east_west", false)
	v = transportTestCart(r, 13.2, 60.1, 10.5)
	v.VX = .1
	r.rt().tickCount++
	s.tickVehicles(r)
	if v.VX <= .1 {
		t.Fatal("powered rail did not accelerate")
	}
	for i := 0; i < 12; i++ {
		r.rt().tickCount++
		s.tickVehicles(r)
		if int(v.X) == 14 && railState(r, blockKey(14, 60, 10)).Power != 15 {
			t.Fatal("occupied detector rail did not emit power")
		}
	}
	if v.X <= 15 || railState(r, blockKey(14, 60, 10)).Power != 0 {
		t.Fatal("vacated detector rail kept power")
	}
	r, p = transportTestWorld(s)
	for x := 10; x <= 20; x++ {
		for z := 8; z <= 14; z++ {
			r.Edits[blockKey(x, 59, z)] = blkWaterSource
		}
	}
	p.state.Inv.Slots[0] = customStack(itemBoat, 1)
	if !s.useVehicle(p, Message{X: 12, Y: 59, Z: 10, F: 1}, nil, false) {
		t.Fatal("boat placement failed")
	}
	for _, v = range vehicleStore(r) {
	}
	p.state.X, p.state.Z = 12.5, 10.5
	if !s.interactVehicle(p, v.ID) {
		t.Fatal("boat mount failed")
	}
	x, z := v.X, v.Z
	for i := 0; i < 15; i++ {
		r.rt().tickCount++
		s.controlVehicle(p, Message{Forward: 1, Turn: .5})
		s.tickVehicles(r)
	}
	if math.Hypot(v.X-x, v.Z-z) < .2 || v.Yaw == 0 || p.state.Mount != v.ID {
		t.Fatal("boat input/turning did not move its rider")
	}
}
func TestTransportLegacyMigrationBackupAndInvalidMetadata(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "fedcba9876543210"
	raw := diskRoom{Version: 8, Mode: "survival", W: 64, Name: "legacy", Mobs: []*Mob{{ID: rtEntNamespace | 7, Type: "minecart", X: 10.5, Y: 60.1, Z: 10.5, Health: 13}, {ID: rtEntNamespace | 8, Type: "boat", X: 12.5, Y: 60.8, Z: 10.5, Health: 30}, {ID: rtEntNamespace | 9, Type: "fishing_bobber", X: 12, Y: 61, Z: 10, Health: 1}}}
	encoded, _ := json.Marshal(raw)
	if err := os.WriteFile(s.roomFile(id), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	r := loader.rooms[id]
	if len(r.Mobs) != 0 || len(vehicleStore(r)) != 2 || !r.transportMigrated {
		t.Fatal("legacy transports did not migrate separately")
	}
	onDisk, _ := os.ReadFile(s.roomFile(id))
	if string(onDisk) != string(encoded) {
		t.Fatal("load modified the original legacy save")
	}
	if err := loader.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(s.roomFile(id) + ".pre-vehicles-v8.bak")
	if err != nil || string(backup) != string(encoded) {
		t.Fatal("legacy transport migration lacks the untouched original backup")
	}
	for _, v := range vehicleStore(r) {
		if v.Item.Meta != nil {
			t.Fatal("migration fabricated missing legacy metadata")
		}
	}
	v := transportTestCart(r, 10.5, 60.1, 10.5)
	v.Item.Meta.Lore = []string{strings.Repeat("x", 10000)}
	r.Dirty = true
	if err := loader.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	invalid, _ := os.ReadFile(s.roomFile(id))
	loader.rooms = map[string]*Room{}
	if err := loader.loadRooms(); err == nil {
		t.Fatal("invalid vehicle metadata was accepted")
	}
	unchanged, _ := os.ReadFile(s.roomFile(id))
	if string(unchanged) != string(invalid) {
		t.Fatal("invalid vehicle restoration changed the save")
	}
}

func TestTransportPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "transport-fixture" {
		return
	}
	p := api.New()
	names := []string{api.VehiclePlacing, api.VehicleEntering, api.VehicleExiting, api.VehicleMoving, api.VehicleDestroying, api.FishingCasting, api.FishingBiting, api.FishingReeling, api.ItemSpawning, api.InventoryChanging, api.ItemWearing, api.PlayerExperienceChanging, api.PlayerLevelChanging, api.EntityDamaging, api.EntityDying, api.PlayerAttacking, api.PlayerMoving, api.RailChanging}
	for _, name := range names {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if e.Source == "plugin:cancel:"+e.Name {
				e.Cancel("transport.protected")
			}
			if e.Name == api.ItemSpawning && e.Source == "plugin:fishing-success" {
				e.Data["count"] = 2
			}
			if e.Name == api.ItemWearing && e.Source == "plugin:fishing-success" {
				e.Data["amount"] = 2
			}
			if e.Name == api.PlayerExperienceChanging && e.Source == "plugin:fishing-success" {
				e.Data["amount"] = 2
			}
			if e.Name == api.EntityDamaging && e.Source == "plugin:invalid-damage" {
				e.Data["amount"] = -1
			}
			if e.Name == api.ItemSpawning && e.Source == "plugin:invalid-fishing-count" {
				e.Data["count"] = 1000
			}
			if e.Name == api.PlayerExperienceChanging && e.Source == "plugin:invalid-fishing-xp" {
				e.Data["amount"] = 0.5
			}
			if e.Name == api.ItemWearing && e.Source == "plugin:invalid-fishing-wear" {
				e.Data["amount"] = -1
			}
			e.Data["actor"] = "changed"
			e.Data["type"] = "zombie"
			e.Data["stack"] = &api.ItemStack{ID: 1, Count: 64}
			return nil
		})
	}
	names = append(names, api.VehiclePlaced, api.VehicleEntered, api.VehicleExited, api.VehicleMoved, api.VehicleDestroyed, api.FishingCast, api.FishingBit, api.FishingReeled, api.ItemSpawned, api.InventoryChanged, api.ItemWorn, api.PlayerExperienceChanged, api.PlayerLevelChanged, api.EntityDamaged, api.EntityDead, api.PlayerAttacked, api.PlayerMoved, api.RailChanged)
	for _, name := range names {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			f.Close()
			e.Cancel("monitor must be ignored")
			e.Data["actor"] = "monitor"
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func transportPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "transport-test")
	os.MkdirAll(dir, 0700)
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"transport-test","name":"transport-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestTransportPluginProcess$","transport-fixture"]}`
	if err = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestTransportPluginProtectionAtomicityAndSnapshots(t *testing.T) {
	s := transportPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{api.VehiclePlacing, api.InventoryChanging, api.RailChanging} {
		r, p := transportTestWorld(s)
		transportTestRail(r, 12, 60, 10, blkDetectorRail, "east_west", false)
		p.state.Inv.Slots[0] = customStack(itemMinecart, 1)
		before := copyStack(s.heldItem(p))
		s.pluginSource = "plugin:cancel:" + name
		if s.useVehicle(p, Message{X: 12, Y: 60, Z: 10, F: 1}, nil, false) || len(vehicleStore(r)) != 0 || !reflect.DeepEqual(before, s.heldItem(p)) || r.Ext.NextVehicleID != 0 || railState(r, blockKey(12, 60, 10)).Powered {
			t.Fatalf("placement %s cancellation partially committed", name)
		}
	}
	for _, name := range []string{api.VehicleEntering, api.PlayerMoving} {
		r, p := transportTestWorld(s)
		transportTestRail(r, 12, 60, 10, blkRail, "east_west", false)
		v := transportTestCart(r, 12.5, 60.1, 10.5)
		s.pluginSource = "plugin:cancel:" + name
		if s.interactVehicle(p, v.ID) || v.RiderID != 0 || p.state.Mount != 0 {
			t.Fatalf("enter %s cancellation partially committed", name)
		}
	}
	for _, name := range []string{api.VehicleExiting, api.PlayerMoving} {
		r, p := transportTestWorld(s)
		v := transportTestCart(r, 12.5, 60.1, 10.5)
		v.RiderID, v.Rider, p.state.Mount = p.id, p.sid, v.ID
		s.pluginSource = "plugin:cancel:" + name
		if s.exitVehicle(p, "test_exit") || v.RiderID != p.id || p.state.Mount != v.ID {
			t.Fatalf("exit %s cancellation partially committed", name)
		}
	}
	for _, name := range []string{api.VehicleMoving, api.PlayerMoving, api.RailChanging} {
		r, p := transportTestWorld(s)
		transportTestRail(r, 12, 60, 10, blkRail, "east_west", false)
		transportTestRail(r, 13, 60, 10, blkDetectorRail, "east_west", false)
		v := transportTestCart(r, 12.95, 60.1, 10.5)
		v.VX = .3
		v.RiderID, v.Rider, p.state.Mount = p.id, p.sid, v.ID
		p.state.X, p.state.Y, p.state.Z = v.X, v.Y+.35, v.Z
		s.pluginSource = "plugin:cancel:" + name
		before := cloneVehicle(v)
		point := [3]float64{p.state.X, p.state.Y, p.state.Z}
		s.tickVehicles(r)
		if !reflect.DeepEqual(before, cloneVehicle(v)) || [3]float64{p.state.X, p.state.Y, p.state.Z} != point || railState(r, blockKey(13, 60, 10)).Power != 0 {
			t.Fatalf("move %s cancellation partially committed", name)
		}
	}
	for _, name := range []string{api.VehicleDestroying, api.EntityDamaging, api.EntityDying, api.ItemSpawning, api.VehicleExiting, api.RailChanging} {
		r, p := transportTestWorld(s)
		transportTestRail(r, 12, 60, 10, blkDetectorRail, "east_west", true)
		r.Ext.Blocks[blockKey(12, 60, 10)].Rail.Power = 15
		v := transportTestCart(r, 12.5, 60.1, 10.5)
		v.RiderID, v.Rider, p.state.Mount = p.id, p.sid, v.ID
		s.pluginSource = "plugin:cancel:" + name
		beforeID := r.NextDropID
		if s.damageVehicleSource(r, nil, v, 100, "vehicle_test", &api.DamageContext{Cause: "plugin"}) || vehicleStore(r)[v.ID] != v || v.Health != 20 || len(r.Drops) != 0 || r.NextDropID != beforeID || p.state.Mount != v.ID || railState(r, blockKey(12, 60, 10)).Power != 15 {
			t.Fatalf("destroy %s cancellation partially committed", name)
		}
	}
	for _, name := range []string{api.FishingCasting} {
		r, p := transportTestWorld(s)
		p.state.Inv.Slots[0] = customStack(itemFishingRod, 1)
		s.pluginSource = "plugin:cancel:" + name
		before := r.rt().nextEnt
		if s.useFishingRod(p, nil, false) || len(fishingState(r).bobbers) != 0 || r.rt().nextEnt != before {
			t.Fatal("cast cancellation allocated a bobber")
		}
	}
	for _, name := range []string{api.PlayerAttacking, api.ItemWearing, api.InventoryChanging, api.EntityDamaging} {
		r, p := transportTestWorld(s)
		v := transportTestCart(r, 12.5, 60.1, 10.5)
		p.state.Inv.Slots[0] = customStack(s.id("Diamond Sword"), 1)
		weapon := copyStack(s.heldItem(p))
		s.pluginSource = "plugin:cancel:" + name
		if s.attackVehicle(p, v.ID) || v.Health != 20 || !same(weapon, s.heldItem(p)) || !p.attackAt.IsZero() {
			t.Fatalf("attack %s cancellation consumed wear or damage", name)
		}
	}
	{
		r, p := transportTestWorld(s)
		v := transportTestCart(r, 12.5, 60.1, 10.5)
		s.pluginSource = "plugin:invalid-damage"
		if s.damageVehicleSource(r, p, v, 1, "vehicle_test", nil) || v.Health != 20 {
			t.Fatal("invalid plugin damage was committed")
		}
	}
	for _, name := range []string{api.FishingReeling, api.ItemSpawning, api.PlayerExperienceChanging, api.PlayerLevelChanging, api.ItemWearing, api.InventoryChanging} {
		r, p := transportTestWorld(s)
		p.state.Inv.Slots[0] = customStack(itemFishingRod, 1)
		p.ext().XPPoints = 6
		s.pluginSource = ""
		if !s.useFishingRod(p, nil, false) {
			t.Fatal("cast setup")
		}
		b := fishingState(r).bobbers[p.id]
		b.InWater, b.BiteTicks = true, 40
		s.pluginSource = "plugin:cancel:" + name
		rod := copyStack(s.heldItem(p))
		beforeID := r.NextDropID
		if s.reelFishing(p, b) || fishingState(r).bobbers[p.id] != b || !reflect.DeepEqual(rod, s.heldItem(p)) || p.ext().XPPoints != 6 || p.ext().XPTotal != 0 || len(r.Drops) != 0 || r.NextDropID != beforeID {
			t.Fatalf("reel %s cancellation partially committed", name)
		}
	}
	for _, source := range []string{"invalid-fishing-count", "invalid-fishing-xp", "invalid-fishing-wear"} {
		r, p := transportTestWorld(s)
		p.state.Inv.Slots[0] = customStack(itemFishingRod, 1)
		s.pluginSource = ""
		if !s.useFishingRod(p, nil, false) {
			t.Fatal("cast setup")
		}
		b := fishingState(r).bobbers[p.id]
		b.InWater, b.BiteTicks = true, 40
		s.pluginSource = "plugin:" + source
		rod, beforeID := copyStack(s.heldItem(p)), r.NextDropID
		if s.reelFishing(p, b) || fishingState(r).bobbers[p.id] != b || !same(rod, s.heldItem(p)) || p.ext().XPTotal != 0 || len(r.Drops) != 0 || r.NextDropID != beforeID {
			t.Fatalf("invalid mutable %s partially committed", source)
		}
	}
	r, p := transportTestWorld(s)
	p.state.Inv.Slots[0] = customStack(itemFishingRod, 1)
	s.pluginSource = ""
	if !s.useFishingRod(p, nil, false) {
		t.Fatal("cast setup")
	}
	b := fishingState(r).bobbers[p.id]
	b.X, b.Y, b.Z = 12.5, 60.8, 10.5
	r.Edits[blockKey(12, 60, 10)] = blkWaterSource
	b.WaitTicks = 1
	s.pluginSource = "plugin:cancel:" + api.FishingBiting
	s.tickFishing(r)
	if b.BiteTicks != 0 {
		t.Fatal("cancelled bite began a bite window")
	}
	s.pluginSource = "plugin:fishing-success"
	b.InWater, b.BiteTicks = true, 40
	rod := copyStack(s.heldItem(p))
	if !s.reelFishing(p, b) || len(r.Drops) != 1 || s.heldItem(p).Damage != rod.Damage+2 || p.ext().XPTotal != 2 || !same(sized(s.heldItem(p), 1), &Stack{ID: rod.ID, Count: 1, Damage: rod.Damage + 2, Ench: rod.Ench, Meta: rod.Meta}) {
		t.Fatal("fishing mutable XP/count/wear or metadata settlement failed")
	}
	for _, d := range r.Drops {
		if d.Stack.Count != 2 {
			t.Fatal("fishing ignored validated mutable drop count")
		}
	}
	log, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "transport-test", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	successNames := map[string]bool{api.VehiclePlaced: true, api.VehicleEntered: true, api.VehicleExited: true, api.VehicleMoved: true, api.VehicleDestroyed: true, api.FishingCast: true, api.FishingBit: true, api.FishingReeled: true, api.ItemSpawned: true, api.ItemWorn: true, api.InventoryChanged: true, api.PlayerExperienceChanged: true, api.PlayerLevelChanged: true, api.EntityDamaged: true, api.EntityDead: true, api.PlayerMoved: true, api.PlayerAttacked: true, api.RailChanged: true}
	successSnapshot := false
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid monitored transport event")
		}
		if strings.HasPrefix(e.Source, "plugin:cancel:") && successNames[e.Name] {
			t.Fatalf("cancelled action emitted success %s for %s", e.Name, e.Source)
		}
		if strings.HasPrefix(e.Source, "plugin:invalid-") && successNames[e.Name] {
			t.Fatalf("invalid mutable field emitted success %s", e.Name)
		}
		if e.Source == "plugin:fishing-success" && (e.Name == api.PlayerExperienceChanging || e.Name == api.PlayerExperienceChanged) {
			if e.String("actor") != p.sid || e.Item("rod") == nil || !reflect.DeepEqual(e.Item("rod").Meta, itemSnapshot(rod).Meta) || e.Data["target"] == nil {
				t.Fatal("fishing XP lost full rod/source snapshots")
			}
		}
		if e.Source == "plugin:fishing-success" && e.Name == api.FishingReeled {
			if e.String("actor") != p.sid || !reflect.DeepEqual(e.Item("stack").Meta, itemSnapshot(rod).Meta) || e.Item("after").Damage != 2 || e.Number("experience") != 2 || e.Data["target"] == nil {
				t.Fatal("readonly or Monitor changed reel snapshots")
			}
			successSnapshot = true
		}
	}
	if !successSnapshot {
		t.Fatal("missing monitored fishing completion")
	}
}
