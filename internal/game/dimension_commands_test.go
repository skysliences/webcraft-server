package game

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func dimensionTestWorld(s *Server) (*Room, *Peer, *Peer) {
	r, actor := createAdvancedTestWorld(s)
	r.ID = "dimensiontest123" // Persistence uses 16-character world IDs.
	r.W = 256
	actor.sid = "DimensionAdmin123"
	actor.name = "Admin"
	s.ops.Settings.Operators[actor.sid] = true
	r.Players[actor.sid] = actor.state
	guest := dropTestPlayer(r, 2, 12.5, 61, 12.5)
	guest.name = "Guest"
	guest.sid = "DimensionGuest123"
	r.Peers[guest] = true
	r.Players[guest.sid] = guest.state
	s.rooms[r.ID] = r
	return r, actor, guest
}

func assertDimensionLanding(t *testing.T, s *Server, p *Peer) {
	t.Helper()
	x, y, z := int(p.state.X), int(p.state.Y), int(p.state.Z)
	if s.kind(s.block(p.room, x, y-1, z)) != 1 || s.block(p.room, x, y, z) != 0 || s.block(p.room, x, y+1, z) != 0 {
		t.Fatalf("unsafe %s landing: %v", p.room.dimName(), p.state)
	}
}

func TestDimensionCommandRoundTripPermissionsAndPersistence(t *testing.T) {
	for _, infinite := range []bool{false, true} {
		name := "finite"
		if infinite {
			name = "infinite"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := testApp(t, t.TempDir())
			s.mu.Lock()
			defer s.mu.Unlock()
			r, actor, guest := dimensionTestWorld(s)
			r.Infinite = infinite
			original := customStack(276, 1)
			guest.state.Inv.Slots[0] = copyStack(original)
			guest.state.Health, guest.state.Food = 13, 16
			guest.ext().XPLevel = 7
			guest.ext().Effects = map[string]*Effect{"speed": {Ticks: 80, Amp: 1}}
			if s.executeCommand(guest, "/dim end") || guest.room != r || len(r.dims) != 0 {
				t.Fatal("permission check created or moved dimension")
			}
			for _, bad := range []string{"/dim", "/dim nowhere", "/dim end MissingPlayer", "/dim end Guest extra"} {
				if s.executeCommand(actor, bad) || guest.room != r {
					t.Fatal("invalid command changed player:", bad)
				}
			}
			var transfers []*api.PlayerTransfer
			s.testEventHook = func(e *api.Event) {
				if e.Name == api.PlayerDimensionChanged {
					tr := e.PlayerTransfer()
					transfers = append(transfers, tr)
					if e.Player == nil || e.Player.AccountID != guest.sid || !reflect.DeepEqual(e.Player.Items[0], itemSnapshot(original)) {
						t.Error("completion missing target/full metadata")
					}
				}
			}
			for _, command := range []string{"/dimension end Guest", "/dim 地狱 Guest", "/dim 普通世界 Guest", "/dim minecraft:the_end Guest"} {
				if !s.executeCommand(actor, command) {
					var messages []string
					for len(guest.send) > 0 {
						out := <-guest.send
						var m struct {
							T string `json:"t"`
							M string `json:"m"`
						}
						if json.Unmarshal(out.body, &m) == nil && m.T == "sys" {
							messages = append(messages, m.M)
						}
					}
					room := s.dimRoom(r, "nether")
					pos, ok := s.dimensionCommandSpawn(room)
					t.Fatalf("failed command %s; targetClosed=%v; messages=%v; landing=%v/%v; roomInfinite=%v/rootMatch=%v", command, guest.closed, messages, pos, ok, room.Infinite, room.root() == guest.room.root())
				}
				assertDimensionLanding(t, s, guest)
				if actor.room != r || guest.room.root() != r || !reflect.DeepEqual(guest.state.Inv.Slots[0], original) || guest.state.Health != 13 || guest.state.Food != 16 || guest.ext().XPLevel != 7 || guest.ext().Effects["speed"].Ticks != 80 {
					t.Fatal("transfer lost progress or moved operator")
				}
			}
			if len(transfers) != 4 || transfers[0].Dimension != "end" || transfers[1].FromDimension != "end" || transfers[1].Dimension != "nether" || transfers[2].Dimension != "overworld" || transfers[0].Actor != actor.sid || transfers[0].TargetAccountID != guest.sid || transfers[0].Reason != "command" {
				t.Fatal(transfers)
			}
			s.testEventHook = nil
			for _, room := range []*Room{r, r.dims["end"], r.dims["nether"]} {
				if err := s.saveRoom(room); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(s.roomFile(r.ID))
			if err != nil {
				t.Fatal(err)
			}
			var saved diskRoom
			if json.Unmarshal(raw, &saved) != nil || saved.Players[guest.sid].Ext.Dim != "end" || !reflect.DeepEqual(saved.Players[guest.sid].Inv.Slots[0], original) {
				t.Fatal("dimension or metadata missing in saved root")
			}
			s.rooms = map[string]*Room{} // Require a real disk restore.
			if err = s.loadRooms(); err != nil {
				t.Fatal(err)
			}
			restored := s.rooms[r.ID]
			if restored.dims["end"] == nil || restored.dims["nether"] == nil || restored.Players[guest.sid].Ext.Dim != "end" || !reflect.DeepEqual(restored.Players[guest.sid].Inv.Slots[0], original) {
				t.Fatal("restore lost dimensions or player")
			}
		})
	}
}

func TestDimensionCommandCancellationTransportAndTPResult(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, actor, guest := dimensionTestWorld(s)
	// Establish destinations before testing a veto, so generation isn't part of the test.
	end := s.dimRoom(r, "end")
	before := copyStack(customStack(276, 1))
	guest.state.Inv.Slots[0] = copyStack(before)
	cart := transportTestCart(r, guest.state.X, 61, guest.state.Z)
	cart.RiderID, cart.Rider = guest.id, guest.sid
	guest.state.Mount = cart.ID
	for _, cancelEvent := range []string{api.PlayerDimensionChanging, api.PlayerTeleporting, api.VehicleExiting} {
		var success int
		seenVeto := false
		s.testEventHook = func(e *api.Event) {
			if e.Name == cancelEvent {
				e.Cancelled = true
				seenVeto = true
			}
			if e.Name == api.PlayerDimensionChanged || e.Name == api.PlayerTeleported || e.Name == api.CommandExecuted {
				success++
			}
		}
		x, y, z := guest.state.X, guest.state.Y, guest.state.Z
		if s.executeCommand(actor, "/dim end Guest") || !seenVeto || guest.room != r || !r.Peers[guest] || end.Peers[guest] || guest.state.X != x || guest.state.Y != y || guest.state.Z != z || guest.state.Mount != cart.ID || cart.RiderID != guest.id || !reflect.DeepEqual(guest.state.Inv.Slots[0], before) || success != 0 {
			t.Fatalf("veto %s committed a transfer", cancelEvent)
		}
	}
	// Cross-dimension /tp must propagate the protected transfer's failure.
	s.testEventHook = nil
	if !s.moveToDimension(actor, end, 612.5, 49, 512.5, true) {
		t.Fatal("operator setup")
	}
	var completed int
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.PlayerDimensionChanging {
			e.Cancelled = true
		}
		if e.Name == api.CommandExecuted {
			completed++
		}
	}
	if s.executeCommand(actor, "/tp Guest Admin") || guest.room != r || completed != 0 {
		t.Fatal("cancelled /tp falsely succeeded")
	}
	s.testEventHook = nil
	if !s.executeCommand(actor, "/dimension end Guest") || guest.state.Mount != 0 || cart.RiderID != 0 {
		t.Fatal("successful transfer did not dismount")
	}
	guest.state.Dead = true
	if s.executeCommand(actor, "/dim overworld Guest") || guest.room != end {
		t.Fatal("dead player teleported")
	}
}

func TestDimensionSafeLandingRejectsBlockedSpawnWithoutEditing(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, actor, _ := dimensionTestWorld(s)
	r.Mode = "creative"
	if s.executeCommand(actor, "/dimension end") || len(r.dims) != 0 {
		t.Fatal("legacy finite creative world accepted unsupported dimension rendering")
	}
	r.Mode = "survival"
	end := s.dimRoom(r, "end")
	sp := endSpawn(end)
	// Fill the entire bounded search; no command may destroy these player blocks.
	for x := sp[0] - 8; x <= sp[0]+8; x++ {
		for z := sp[2] - 8; z <= sp[2]+8; z++ {
			for y := 0; y < 128; y++ {
				end.Edits[blockKey(x, y, z)] = 1
			}
		}
	}
	before := len(end.Edits)
	if s.executeCommand(actor, "/dimension end") || actor.room != r || len(end.Edits) != before || s.block(end, sp[0], sp[1], sp[2]) != 1 {
		t.Fatal("blocked spawn was overwritten or accepted")
	}
}

func TestDimensionPluginActionsAndReadonlyEvents(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, actor, guest := dimensionTestWorld(s)
	original := customStack(276, 1)
	guest.state.Inv.Slots[0] = copyStack(original)
	// The fixture changes readonly destinations/actor and Monitor queues actions.
	// These must be discarded by the real process-based plugin manager.
	s.pluginSource = "plugin:dimension-fixture"
	if !s.changePlayerDimension(nil, guest, "end", "dimension_fixture") || guest.room.dimName() != "end" || !reflect.DeepEqual(guest.state.Inv.Slots[0], original) || len(s.actions) != 0 {
		t.Fatal("readonly event or Monitor changed transfer")
	}
	s.pluginSource = ""
	var completed int
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.PlayerDimensionChanged || e.Name == api.CommandExecuted {
			completed++
		}
	}
	s.pluginSource = "plugin:test"
	s.applyAction(api.Action{Type: "change_dimension", WorldID: guest.room.ID, PlayerID: guest.id, Dimension: "nether"})
	if guest.room.dimName() != "end" || completed != 0 {
		t.Fatal("plugin action bypassed protection")
	}
	s.pluginSource = ""
	if s.executeCommand(actor, "/dim nether") || actor.room != r || completed != 0 {
		t.Fatal("command bypassed real plugin cancellation")
	}
}

func TestDimensionConsoleAndNetherSeeds(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _, guest := dimensionTestWorld(s)
	var transfer *api.PlayerTransfer
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.PlayerDimensionChanged {
			transfer = e.PlayerTransfer()
		}
	}
	if _, err := s.console("dimension-console", "dimension end Guest", r.ID, ""); err != nil {
		t.Fatal(err)
	}
	if guest.room.dimName() != "end" || transfer == nil || transfer.Actor != "dimension-console" || transfer.Reason != "console" || guest.owner || s.pluginSource != "" {
		t.Fatal("console context or target incorrect", transfer)
	}
	var completed int
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.PlayerTeleporting {
			e.Cancelled = true
		}
		if e.Name == api.ServerCommandExecuted || e.Name == api.PlayerDimensionChanged {
			completed++
		}
	}
	if _, err := s.console("dimension-console", "dim overworld Guest", r.ID, ""); err == nil || guest.room.dimName() != "end" || completed != 0 {
		t.Fatal("console bypassed cancellation")
	}
	s.testEventHook = nil
	// Cover different cave distributions, including seeds with no landing in the
	// old 8-block radius. The search must still find actual dry cave/fortress floor.
	for _, id := range []string{"dimensiontest123", "dimensionseed001", "dimensionseed002", "dimensionseed003"} {
		root := newRoom(id, "seed", "survival", 0)
		root.Infinite = true
		nether := s.dimRoom(root, "nether")
		pos, ok := s.dimensionCommandSpawn(nether)
		if !ok {
			t.Fatal("normal generated Nether has no landing", id)
		}
		peer := dropTestPlayer(nether, 3, pos[0], pos[1], pos[2])
		assertDimensionLanding(t, s, peer)
	}
}
