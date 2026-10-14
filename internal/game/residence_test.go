package game

import (
	"encoding/binary"
	"math"
	api "mcraft/backend/pkg/plugin"
	"testing"
	"time"
)

func makeMovePacket(x, y, z float64) []byte {
	buf := make([]byte, 18)
	buf[0] = 1
	binary.LittleEndian.PutUint32(buf[1:], math.Float32bits(float32(x)))
	binary.LittleEndian.PutUint32(buf[5:], math.Float32bits(float32(y)))
	binary.LittleEndian.PutUint32(buf[9:], math.Float32bits(float32(z)))
	return buf
}

func TestResidenceProtectionAndPermissions(t *testing.T) {
	s, _ := testApp(t, t.TempDir())

	s.mu.Lock()
	defer s.mu.Unlock()

	r := newRoom("testresworld12345", "TestWorld", "survival", 0)
	s.rooms[r.ID] = r

	owner := &Peer{
		room:    r,
		id:      1,
		sid:     "owner_sid_1111111111111111111111",
		name:    "Alice",
		builder: "alice_uid",
		send:    make(chan packet, 256),
		state: &Player{
			X:      100.5,
			Y:      65,
			Z:      100.5,
			Health: 20,
			Food:   20,
			Inv:    &Inventory{Slots: make(Stacks, 46)},
		},
		known: map[int]bool{},
	}
	stranger := &Peer{
		room:    r,
		id:      2,
		sid:     "guest_sid_2222222222222222222222",
		name:    "Bob",
		builder: "bob_uid",
		send:    make(chan packet, 256),
		state: &Player{
			X:      80.5,
			Y:      65,
			Z:      100.5,
			Health: 20,
			Food:   20,
			Inv:    &Inventory{Slots: make(Stacks, 46)},
		},
		known: map[int]bool{},
	}
	r.Peers[owner] = true
	r.Peers[stranger] = true

	// Give Alice enough balance to buy a 10x10 residence (cost = 10*10*10 = 1000)
	_, _ = s.ensureEconomyAccount(owner)
	_, err := s.economyDB.Deposit(owner.sid, owner.name, 5000, "test_init")
	if err != nil {
		t.Fatal(err)
	}

	ok, msg := s.residence.CreateResidence(owner, "AliceHome", 10)
	if !ok {
		t.Fatalf("CreateResidence failed: %s", msg)
	}

	res := s.residence.FindByName("AliceHome")
	if res == nil {
		t.Fatal("Residence AliceHome not found after creation")
	}

	// Place a test block and a chest inside Alice's residence at (100, 65, 100) and (101, 65, 100)
	plankID := s.rules.byName["Oak Planks"]
	chestID := s.rules.byName["Chest"]
	stoneID := s.rules.byName["Stone"]
	r.Edits[blockKey(100, 64, 100)] = stoneID
	r.Edits[blockKey(100, 65, 100)] = plankID
	r.Edits[blockKey(101, 64, 100)] = stoneID
	r.Edits[blockKey(101, 65, 100)] = chestID
	r.Edits[blockKey(102, 64, 100)] = stoneID
	r.Edits[blockKey(102, 65, 100)] = 0

	// 1. Stranger Bob tries to walk into AliceHome -> should be blocked
	s.binary(stranger, makeMovePacket(100.5, 65, 100.5))
	if stranger.state.X == 100.5 && stranger.state.Z == 100.5 {
		t.Fatal("stranger was allowed to walk into residence with enter=false")
	}

	// 2. Stranger Bob tries to teleport into AliceHome -> should be blocked
	if ok, _ := s.residence.TeleportToResidence(stranger, res.ID); ok {
		t.Fatal("stranger was allowed to teleport into residence with enter=false")
	}

	// 3. Even if Stranger Bob is near the border (within reach) and tries to break a block inside AliceHome:
	stranger.state.X, stranger.state.Y, stranger.state.Z = 98.5, 65, 100.5
	stranger.state.Inv.Slots[0] = stack(plankID, 10, 0)
	s.survivalMessage(stranger, Message{T: "dig", S: 0, X: 100, Y: 65, Z: 100, F: 1})
	if stranger.mining != nil {
		t.Fatal("stranger was allowed to start mining inside residence")
	}
	stranger.mining = &digState{X: 100, Y: 65, Z: 100, Block: plankID, Held: plankID, At: time.Now().Add(-time.Hour)}
	s.survivalMessage(stranger, Message{T: "dig", S: 2, X: 100, Y: 65, Z: 100, F: 1})
	if s.block(r, 100, 65, 100) != plankID {
		t.Fatal("stranger broke a block inside AliceHome without build permission")
	}

	// 4. Stranger Bob tries to place a block inside AliceHome -> should be blocked
	s.survivalMessage(stranger, Message{T: "place", X: 102, Y: 64, Z: 100, F: 1})
	if s.block(r, 102, 65, 100) != 0 || stranger.state.Inv.Slots[0].Count != 10 {
		t.Fatalf("stranger placed a block inside AliceHome without build permission: block=%d count=%d", s.block(r, 102, 65, 100), stranger.state.Inv.Slots[0].Count)
	}

	// 5. Stranger Bob tries to open chest inside AliceHome -> should be blocked
	s.openWindow(stranger, blockKey(101, 65, 100), "chest", 0)
	if stranger.window != nil {
		t.Fatal("stranger opened chest inside AliceHome without container permission")
	}

	// 6. PVP inside AliceHome -> should be blocked
	ctx := s.playerAttack(stranger, playerTarget(owner), 5)
	if ctx != nil {
		t.Fatal("PVP attack against player inside residence should be cancelled when pvp=false")
	}

	// 7. Monster spawning inside AliceHome -> should be blocked when monsters=false
	if _, err := s.prepareMob(r, nil, "zombie", 100.5, 65, 102.5, "natural", nil); err == nil {
		t.Fatal("hostile zombie should not be allowed to spawn inside residence when monsters=false")
	}

	// 8. Owner Alice CAN break and place blocks inside her own residence
	owner.state.Inv.Slots[0] = stack(plankID, 5, 0)
	s.survivalMessage(owner, Message{T: "place", X: 102, Y: 64, Z: 100, F: 1})
	if s.block(r, 102, 65, 100) != plankID {
		t.Fatal("owner Alice was blocked from placing a block in her own residence")
	}

	// 9. Owner Alice grants Bob enter and build permissions (case-insensitive name check: "bob")
	if ok, msg := s.residence.SetPlayerPermission(owner, res.ID, "bob", "enter", true); !ok {
		t.Fatalf("failed to grant enter to bob: %s", msg)
	}
	if ok, msg := s.residence.SetPlayerPermission(owner, res.ID, "bob", "build", true); !ok {
		t.Fatalf("failed to grant build to bob: %s", msg)
	}

	// Now Bob CAN walk into AliceHome
	s.binary(stranger, makeMovePacket(100.5, 65, 100.5))
	if stranger.state.X != 100.5 || stranger.state.Z != 100.5 {
		t.Fatal("authorized player Bob should be able to walk into AliceHome")
	}

	// And Bob CAN break the block at (102, 65, 100)
	stranger.mining = &digState{X: 102, Y: 65, Z: 100, Block: plankID, Held: plankID, At: time.Now().Add(-time.Hour)}
	s.survivalMessage(stranger, Message{T: "dig", S: 2, X: 102, Y: 65, Z: 100, F: 1})
	if s.block(r, 102, 65, 100) != 0 {
		t.Fatal("authorized player Bob should be able to break blocks in AliceHome")
	}

	// And Bob CAN place a block at (102, 65, 100)
	s.survivalMessage(stranger, Message{T: "place", X: 102, Y: 64, Z: 100, F: 1})
	if s.block(r, 102, 65, 100) != plankID {
		t.Fatal("authorized player Bob should be able to place blocks in AliceHome")
	}

	// 10. Owner Alice revokes Bob's permissions while Bob is standing inside AliceHome -> Bob is automatically ejected outside!
	if ok, msg := s.residence.RemovePlayerPermission(owner, res.ID, "Bob"); !ok {
		t.Fatalf("failed to remove Bob permissions: %s", msg)
	}
	bx, by, bz := int(math.Floor(stranger.state.X)), int(math.Floor(stranger.state.Y)), int(math.Floor(stranger.state.Z))
	if res.Contains(r.ID, bx, by, bz) {
		t.Fatalf("Bob should have been ejected outside AliceHome after losing enter permission, still at (%v, %v, %v)", stranger.state.X, stranger.state.Y, stranger.state.Z)
	}

	// Ensure explosion cannot break blocks inside AliceHome
	e := s.emit(api.BlockBreaking, r, nil, map[string]any{"x": 100, "y": 65, "z": 100, "reason": "tnt_explosion"})
	if !e.Cancelled {
		t.Fatal("explosion block breaking inside residence should be cancelled")
	}

	// 11. Public Residence: Non-admin Alice cannot set public flag or create public residence
	if ok, _ := s.residence.SetFlag(owner, res.ID, "public", true); ok {
		t.Fatal("non-admin should not be allowed to set public flag on residence")
	}
	owner.state.X, owner.state.Z = 200.5, 200.5
	if ok, _ := s.residence.CreateResidence(owner, "PublicTry", 10, true); ok {
		t.Fatal("non-admin should not be allowed to create a public residence")
	}

	// 12. Admin CAN create a public residence and toggle public flag on AliceHome
	admin := &Peer{
		room:    r,
		id:      3,
		sid:     "admin_sid_3333333333333333333333",
		name:    "Admin",
		builder: "admin_uid",
		owner:   true,
		send:    make(chan packet, 256),
		state: &Player{
			X:      300.5,
			Y:      65,
			Z:      300.5,
			Health: 20,
			Food:   20,
			Inv:    &Inventory{Slots: make(Stacks, 46)},
		},
		known: map[int]bool{},
	}
	r.Peers[admin] = true
	_, _ = s.ensureEconomyAccount(admin)
	_, _ = s.economyDB.Deposit(admin.sid, admin.name, 5000, "test_admin_init")

	if ok, msg := s.residence.CreateResidence(admin, "SpawnPlaza", 10, true); !ok {
		t.Fatalf("admin failed to create public residence: %s", msg)
	}
	pubRes := s.residence.FindByName("SpawnPlaza")
	if pubRes == nil || !pubRes.IsPublic || !pubRes.Flags["public"] || !pubRes.Flags["enter"] {
		t.Fatalf("public residence flags mismatch: %+v", pubRes)
	}

	// Admin converts AliceHome to public residence -> non-admin Alice can no longer manage it
	if ok, msg := s.residence.SetFlag(admin, res.ID, "public", true); !ok {
		t.Fatalf("admin failed to set public flag: %s", msg)
	}
	if !res.IsPublic || !res.Flags["public"] {
		t.Fatal("AliceHome should now be marked as public")
	}
	if ok, _ := s.residence.SetFlag(owner, res.ID, "pvp", true); ok {
		t.Fatal("non-admin should not be able to manage a public residence even if originally created by them")
	}
}

func TestSetSpawnCommandAndNewPlayerSpawn(t *testing.T) {
	s, _ := testApp(t, t.TempDir())

	s.mu.Lock()
	defer s.mu.Unlock()

	r := newRoom("testspawnworld123", "SpawnWorld", "survival", 0)
	s.rooms[r.ID] = r

	normalPlayer := &Peer{
		room:    r,
		id:      1,
		sid:     "user_sid_1111111111111111111111",
		name:    "User1",
		builder: "user1_uid",
		send:    make(chan packet, 256),
		state: &Player{
			X:      120.5,
			Y:      68,
			Z:      130.5,
			Health: 20,
			Food:   20,
			Inv:    &Inventory{Slots: make(Stacks, 46)},
		},
		known: map[int]bool{},
	}
	adminPlayer := &Peer{
		room:    r,
		id:      2,
		sid:     "admin_sid_2222222222222222222222",
		name:    "AdminUser",
		builder: "admin_uid",
		owner:   true,
		send:    make(chan packet, 256),
		state: &Player{
			X:      240.5,
			Y:      70,
			Z:      250.5,
			Yaw:    1.25,
			Pitch:  -0.15,
			Health: 20,
			Food:   20,
			Inv:    &Inventory{Slots: make(Stacks, 46)},
		},
		known: map[int]bool{},
	}
	r.Peers[normalPlayer] = true
	r.Peers[adminPlayer] = true

	// 1. Non-admin cannot execute /setspawn
	if s.executeCommand(normalPlayer, "/setspawn") {
		t.Fatal("non-admin should not be able to run /setspawn")
	}

	// 2. Admin executes /setspawn at current position (240, 70, 250)
	if !s.executeCommand(adminPlayer, "/setspawn") {
		t.Fatal("admin /setspawn failed")
	}
	sp := s.spawnPoint(r)
	if sp != [3]int{240, 70, 250} {
		t.Fatalf("expected spawnPoint [240, 70, 250], got %v", sp)
	}
	if r.Ext == nil || !r.Ext.SpawnSet || r.Ext.SpawnYaw != 1.25 || r.Ext.SpawnPitch != -0.15 {
		t.Fatalf("expected custom spawn yaw/pitch persisted in RoomExt, got %+v", r.Ext)
	}

	// 3. Normal player runs /spawn -> teleports to (240.5, 70, 250.5)
	if !s.executeCommand(normalPlayer, "/spawn") {
		t.Fatal("/spawn command failed")
	}
	if normalPlayer.state.X != 240.5 || normalPlayer.state.Y != 70 || normalPlayer.state.Z != 250.5 {
		t.Fatalf("expected /spawn to teleport player to (240.5, 70, 250.5), got (%v, %v, %v)", normalPlayer.state.X, normalPlayer.state.Y, normalPlayer.state.Z)
	}

	// 4. Admin executes /setspawn with explicit coordinates: /setspawn 310 72 320
	if !s.executeCommand(adminPlayer, "/setspawn 310 72 320") {
		t.Fatal("admin /setspawn 310 72 320 failed")
	}
	if got := s.spawnPoint(r); got != [3]int{310, 72, 320} {
		t.Fatalf("expected spawnPoint [310, 72, 320], got %v", got)
	}
}

