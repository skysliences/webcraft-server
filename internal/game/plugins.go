package game

import (
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"math"
	"mcraft/backend/internal/plugins"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func (s *Server) event(name string, r *Room, p *Peer, data map[string]any) *api.Event {
	s.eventID++
	e := &api.Event{ID: s.eventID, Name: name, Time: time.Now().UTC().Format(time.RFC3339Nano), Tick: s.tick, Source: s.pluginSource, Data: data}
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	if damage, ok := e.Data["damage"].(*api.DamageContext); ok {
		e.Damage = damage
	}
	if r != nil {
		e.World = &api.World{ID: r.ID, Name: r.Name, Mode: r.Mode, Lock: r.Lock, Online: len(r.Peers)}
		e.World.RootID = r.root().ID
		e.World.Dimension = r.dimName()
		e.World.Infinite = r.Infinite
	}
	if p != nil {
		e.Player = &api.Player{ID: p.id, UID: p.builder, AccountID: p.sid, Name: p.name, Owner: p.owner, Operator: s.isOperator(p.sid)}
		if s.identity != nil {
			e.Identity = &api.Identity{ID: p.sid, Name: p.name, Issuer: s.identity.issuer}
		}
		e.Player.Permissions = s.permissionNodes(p.sid)
		e.Player.Roles = s.playerRoles(p.sid)
		if p.state != nil {
			e.Player.GameMode = s.gameMode(p)
			e.Player.Mount = p.state.Mount
			if ext := p.state.Ext; ext != nil {
				e.Player.XPLevel, e.Player.XPPoints, e.Player.XPTotal = ext.XPLevel, ext.XPPoints, ext.XPTotal
				e.Player.Effects = map[string]api.EffectSnapshot{}
				for name, v := range ext.Effects {
					if v != nil {
						e.Player.Effects[name] = api.EffectSnapshot{Ticks: v.Ticks, Amplifier: v.Amp}
					}
				}
			}
			e.Player.X, e.Player.Y, e.Player.Z, e.Player.Health = p.state.X, p.state.Y, p.state.Z, p.state.Health
			e.Player.Food, e.Player.Dead = p.state.Food, p.state.Dead
			e.Player.MaxHealth = s.maxHealth(p)
			e.Player.Cursor = legacyStack(p.state.Cursor)
			e.Player.CursorItem = itemSnapshot(p.state.Cursor)
			e.Player.Selected = p.state.Slot
			if p.state.Inv != nil {
				e.Player.Selected = p.state.Inv.Selected
				e.Player.Inventory = make([][]int, len(p.state.Inv.Slots))
				e.Player.Items = make([]*api.ItemStack, len(p.state.Inv.Slots))
				for i, v := range p.state.Inv.Slots {
					e.Player.Inventory[i] = legacyStack(v)
					e.Player.Items[i] = itemSnapshot(v)
				}
			}
		}
	}
	return e
}
func (s *Server) emit(name string, r *Room, p *Peer, data map[string]any) *api.Event {
	e := s.event(name, r, p, data)
	if s.testEventHook != nil {
		s.testEventHook(e)
	}
	if s.plugins != nil {
		s.queueActions(s.plugins.Emit(e))
	}
	return e
}
func (s *Server) queueActions(actions []plugins.Intent) {
	if s.closing {
		return
	}
	remaining := 256 - len(s.actions)
	if remaining <= 0 {
		return
	}
	if len(actions) > remaining {
		actions = actions[:remaining]
	}
	s.actions = append(s.actions, actions...)
}
func (s *Server) allowed(name string, r *Room, p *Peer, data map[string]any) bool {
	e := s.emit(name, r, p, data)
	if e.Cancelled && p != nil {
		reason := e.Reason
		if reason == "" {
			reason = "此操作已被插件阻止。"
		}
		p.sys(limitText(reason, 240))
	}
	return !e.Cancelled
}
func limitText(str string, n int) string {
	r := []rune(str)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
func (s *Server) blockData(r *Room, key uint64, b int) map[string]any {
	x, y, z := coords(key)
	return map[string]any{"x": x, "y": y, "z": z, "oldBlock": s.block(r, x, y, z), "block": b, "blockKey": s.registryBlockKey(b), "oldBlockKey": s.registryBlockKey(s.block(r, x, y, z)), "reason": "block_change", "actor": map[string]any{"kind": "server", "plugin": s.pluginSource}, "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}}
}
func (s *Server) playerBlockData(p *Peer, key uint64, b int) map[string]any {
	data := s.blockData(p.room, key, b)
	data["actor"], data["source"] = p.sid, "player"
	data["reason"] = "place"
	if b == 0 {
		data["reason"] = "break"
	}
	data["stack"] = itemSnapshot(s.heldItem(p))
	return data
}
func (s *Server) allowBlock(p *Peer, key uint64, b int, context ...map[string]any) bool {
	if b != blkExplosiveTNT && s.primedTNT(p.room, key) {
		return false
	}
	name := api.BlockPlacing
	if b == 0 {
		name = api.BlockBreaking
	}
	data := s.playerBlockData(p, key, b)
	component := s.redstoneBlockChangeData(p.room, p, key, b, "block_change")
	if component != nil {
		data = component
	}
	if mechanic := s.mechanicBlockChangeData(p.room, p, key, b, "block_change"); mechanic != nil {
		for field, value := range mechanic {
			data[field] = value
		}
	}
	if len(context) > 0 {
		for field, value := range context[0] {
			data[field] = value
		}
		for field, value := range data {
			context[0][field] = value
		}
		data = context[0]
	}
	return s.allowed(name, p.room, p, data) && (component == nil || s.allowed(api.RedstoneComponentChanging, p.room, p, data)) && s.mechanicBlockBefore(p.room, p, data)
}
func (s *Server) blockEvents(r *Room, p *Peer, data map[string]any) {
	name := api.BlockPlaced
	if data["block"] == 0 {
		name = api.BlockBroken
	}
	s.emit(name, r, p, data)
	s.emit(api.BlockChanged, r, p, data)
	if _, ok := data["componentAfter"]; ok {
		s.emit(api.RedstoneComponentChanged, r, p, data)
	}
	s.mechanicBlockCompleted(r, p, data)
}
func (s *Server) correctPosition(p *Peer) {
	m := map[string]any{"t": "place", "st": p.state}
	if p.room.usesSurvival() {
		p.setOrigin()
		m["sv"] = s.peerSurvivalInfo(p)
		p.known = map[int]bool{}
		p.knownDrops = map[uint32]bool{}
		p.knownMobs = map[uint32]bool{}
	}
	p.json(m)
	if p.room.usesSurvival() {
		s.stream(p)
		p.json(map[string]string{"t": "synced"})
	}
	s.syncMobsFor(p)
}
func (s *Server) teleport(p *Peer, x, y, z float64, reason string) bool {
	if p.state.Dead || !number(x, p.room.minCoord(), p.room.maxCoord()) || !number(y, 1, 127) || !number(z, p.room.minCoord(), p.room.maxCoord()) {
		return false
	}
	data := map[string]any{"x": x, "y": y, "z": z, "fromX": p.state.X, "fromY": p.state.Y, "fromZ": p.state.Z, "reason": reason}
	if !s.allowed(api.PlayerTeleporting, p.room, p, data) {
		return false
	}
	s.closeWindow(p)
	p.state.X, p.state.Y, p.state.Z = x, y, z
	p.peakY = y
	p.mining = nil
	p.useAt = time.Time{}
	p.room.Dirty = true
	s.correctPosition(p)
	s.emit(api.PlayerTeleported, p.room, p, data)
	return true
}
func (s *Server) drainActions() {
	for count := 0; len(s.actions) > 0 && count < 128; count++ {
		intent := s.actions[0]
		s.actions = s.actions[1:]
		s.pluginSource = "plugin:" + intent.PluginID
		s.applyAction(intent.Action)
	}
	if len(s.actions) > 0 {
		log.Printf("plugin actions exceeded transaction budget; discarded %d", len(s.actions))
	}
	s.actions = nil
	s.pluginSource = ""
}
func (s *Server) applyAction(a api.Action) {
	if a.Type == "set_operator" {
		if sidRE.MatchString(a.AccountID) {
			next := s.copySettings()
			if a.Value {
				next.Operators[a.AccountID] = true
			} else {
				delete(next.Operators, a.AccountID)
			}
			if err := s.updateSettings(next, s.commandActor(nil), "plugin"); err != nil {
				log.Printf("plugin operator: %v", err)
			}
		}
		return
	}
	r := s.rooms[a.WorldID]
	if r == nil {
		for _, room := range s.allRooms() {
			if room.ID == a.WorldID {
				r = room
				break
			}
		}
	}
	if r == nil {
		return
	}
	var p *Peer
	for other := range r.Peers {
		if other.id == a.PlayerID && !other.closed {
			p = other
			break
		}
	}
	if a.BlockKey != "" {
		a.Block = s.rules.byName[a.BlockKey]
		if !s.rules.validBlock(a.Block) || a.Block == 0 {
			return
		}
	}
	switch a.Type {
	case "feed_animal":
		if p == nil || p.state == nil || p.state.Inv == nil || p.state.Inv.Selected != a.Slot || a.Expected == nil {
			return
		}
		s.feedAnimal(p, r.Mobs[a.EntityID], a.Expected, true)
	case "activate_hopper":
		if !number(a.X, r.minCoord(), r.maxCoord()) || !number(a.Y, 1, 126) || !number(a.Z, r.minCoord(), r.maxCoord()) || a.X != math.Trunc(a.X) || a.Y != math.Trunc(a.Y) || a.Z != math.Trunc(a.Z) || len(a.ContainerExpected) != 5 {
			return
		}
		k := blockKey(int(a.X), int(a.Y), int(a.Z))
		c := s.automationContainer(r, k)
		if c == nil || c.Kind != "hopper" {
			return
		}
		expected := make(Stacks, 5)
		for i, item := range a.ContainerExpected {
			if item != nil {
				expected[i] = (*Stack)(item.Clone())
				if !s.validStack(expected[i], s.rules.max(item.ID)) {
					return
				}
			}
		}
		if reflect.DeepEqual(c.Slots, expected) {
			s.activateHopper(r, k)
		}
	case "dispense_block":
		if !number(a.X, r.minCoord(), r.maxCoord()) || !number(a.Y, 1, 127) || !number(a.Z, r.minCoord(), r.maxCoord()) || a.X != math.Trunc(a.X) || a.Y != math.Trunc(a.Y) || a.Z != math.Trunc(a.Z) || len(a.ContainerExpected) != 9 {
			return
		}
		k := blockKey(int(a.X), int(a.Y), int(a.Z))
		b := s.block(r, int(a.X), int(a.Y), int(a.Z))
		state := mechanicSnapshot(r, k)
		c := r.Containers[k]
		if (b != blkDispenser && b != blkDropper) || state == nil || c == nil || c.Kind != state.Kind || len(c.Slots) != 9 {
			return
		}
		expected := make(Stacks, 9)
		for i, item := range a.ContainerExpected {
			if item != nil {
				expected[i] = (*Stack)(item.Clone())
				if !s.validStack(expected[i], s.rules.max(item.ID)) {
					return
				}
			}
		}
		if !reflect.DeepEqual(c.Slots, expected) {
			return
		}
		s.dispenseMechanic(r, k, state)
	case "write_sign":
		if a.Expected == nil {
			_ = s.writeSignDirect(r, int(a.X), int(a.Y), int(a.Z), a.Lines)
			return
		}
		if p == nil || p.state == nil || p.state.Inv == nil || p.state.Inv.Selected != a.Slot || !number(a.X, r.minCoord(), r.maxCoord()) || !number(a.Y, 0, 127) || !number(a.Z, r.minCoord(), r.maxCoord()) || a.X != math.Trunc(a.X) || a.Y != math.Trunc(a.Y) || a.Z != math.Trunc(a.Z) {
			return
		}
		m := Message{T: "place", X: int(a.X), Y: int(a.Y), Z: int(a.Z), F: a.Face}
		if err := s.writeSign(p, m.X, m.Y, m.Z, a.Lines, a.Expected, true); err != nil {
			p.sys(err.Error())
		}
	case "use_mechanic", "use_vehicle":
		if p == nil || p.state == nil || p.state.Inv == nil || p.state.Inv.Selected != a.Slot || !number(a.X, r.minCoord(), r.maxCoord()) || !number(a.Y, 0, 127) || !number(a.Z, r.minCoord(), r.maxCoord()) || a.X != math.Trunc(a.X) || a.Y != math.Trunc(a.Y) || a.Z != math.Trunc(a.Z) {
			return
		}
		m := Message{T: "place", X: int(a.X), Y: int(a.Y), Z: int(a.Z), F: a.Face}
		if a.Type == "use_mechanic" {
			s.handleMechanicUse(p, m, a.Expected, true)
		}
		if a.Type == "use_vehicle" {
			s.useVehicle(p, m, a.Expected.Clone(), true)
		}
	case "custom_ui":
		if p != nil && a.CustomUI != nil {
			p.json(map[string]any{
				"t":       "custom_ui",
				"id":      a.CustomUI.ID,
				"title":   a.CustomUI.Title,
				"content": a.CustomUI.Content,
				"style":   a.CustomUI.Style,
				"inputs":  a.CustomUI.Inputs,
				"buttons": a.CustomUI.Buttons,
				"data":    a.CustomUI.Data,
			})
		}
	case "close_custom_ui":
		if p != nil {
			p.json(map[string]any{
				"t":  "close_custom_ui",
				"id": a.Reason,
			})
		}
	case "open_container":
		if p != nil && number(a.X, r.minCoord(), r.maxCoord()) && number(a.Y, 0, 127) && number(a.Z, r.minCoord(), r.maxCoord()) {
			k := blockKey(int(a.X), int(a.Y), int(a.Z))
			b := s.block(r, int(a.X), int(a.Y), int(a.Z))
			kind := "chest"
			if b == s.id("Ender Chest") {
				kind = "ender"
			}
			s.openWindow(p, k, kind, 0, "plugin_authorized")
		}
	case "interact_vehicle":
		if p != nil {
			s.interactVehicle(p, a.EntityID)
		}
	case "prime_tnt":
		if number(a.X, r.minCoord(), r.maxCoord()) && number(a.Y, 1, 127) && number(a.Z, r.minCoord(), r.maxCoord()) && a.X == math.Trunc(a.X) && a.Y == math.Trunc(a.Y) && a.Z == math.Trunc(a.Z) {
			if p != nil && p.state != nil && p.state.Inv != nil && p.state.Inv.Selected == a.Slot && reflect.DeepEqual(itemSnapshot(s.heldItem(p)), a.Expected) {
				s.primeTNT(r, p, int(a.X), int(a.Y), int(a.Z), a.FuseTicks, "plugin", nil)
			} else if a.PlayerID == 0 {
				s.primeTNT(r, nil, int(a.X), int(a.Y), int(a.Z), a.FuseTicks, "plugin", nil)
			}
		}
	case "use_redstone":
		if p != nil && p.state != nil && p.state.Inv != nil && p.state.Inv.Selected == a.Slot && number(a.X, r.minCoord(), r.maxCoord()) && number(a.Y, 1, 127) && number(a.Z, r.minCoord(), r.maxCoord()) && a.X == math.Trunc(a.X) && a.Y == math.Trunc(a.Y) && a.Z == math.Trunc(a.Z) {
			s.handleRedstoneUse(p, Message{T: "place", X: int(a.X), Y: int(a.Y), Z: int(a.Z), F: a.Face}, a.Expected, true)
		}
	case "use_spawn_egg":
		if p != nil && p.state != nil && p.state.Inv != nil && a.Slot == p.state.Inv.Selected && a.Expected != nil && number(a.X, r.minCoord(), r.maxCoord()) && number(a.Y, 1, 127) && number(a.Z, r.minCoord(), r.maxCoord()) && a.X == math.Trunc(a.X) && a.Y == math.Trunc(a.Y) && a.Z == math.Trunc(a.Z) {
			s.useSpawnEgg(p, Message{T: "place", X: int(a.X), Y: int(a.Y), Z: int(a.Z), F: a.Face}, a.Expected)
		}
	case "set_gamemode":
		if p != nil {
			s.setPlayerGameMode(p, a.GameMode, s.commandActor(nil), "plugin")
		}
	case "set_difficulty":
		s.setDifficulty(r, p, a.Difficulty, "plugin")
	case "set_rule":
		if a.Rule == "randomTickSpeed" {
			s.setRandomTickSpeed(r, p, int(a.Amount), "plugin")
		} else {
			s.setRule(r, p, a.Rule, a.Value, "plugin")
		}
	case "set_weather":
		s.setWeather(r, a.Rain, a.Thunder, a.Duration)
	case "give_xp":
		if p != nil && number(a.Amount, -1000000, 1000000) && a.Amount == math.Trunc(a.Amount) {
			s.giveXP(p, int(a.Amount))
		}
	case "apply_effect":
		if p != nil {
			s.addEffect(p, a.Effect, a.Duration, a.Amplifier)
		}
	case "clear_effects":
		if p != nil {
			s.clearEffects(p, "plugin")
		}
	case "spawn_mob":
		context := map[string]any{"persistent": a.Persistent, "actor": "plugin", "source": "plugin"}
		mob, err := s.prepareMob(r, nil, a.EntityType, a.X, a.Y, a.Z, "plugin", context)
		if err != nil {
			log.Printf("plugin mob spawn: %v", err)
		} else {
			s.publishMob(r, nil, mob, "plugin", context)
		}
	case "set_time":
		s.setWorldTime(r, nil, a.Ticks, "plugin")
	case "message":
		if p != nil {
			p.sys(limitText(a.Message, 240))
		}
	case "broadcast":
		broadcast(r, map[string]string{"t": "sys", "m": limitText(a.Message, 240)}, nil)
	case "set_item_meta":
		s.setItemMeta(p, a)
	case "give_item", "give_stack":
		if p == nil || !r.usesSurvival() || p.state.Dead {
			return
		}
		id := a.ItemID
		if a.ItemName != "" {
			id = s.rules.byName[a.ItemName]
		}
		v := stack(id, a.Count, 0)
		if a.Type == "give_stack" {
			v = (*Stack)(a.Stack.Clone())
		}
		if !s.validStack(v, 2304) {
			return
		}
		clone := &Inventory{Slots: make(Stacks, 46)}
		for i, v := range p.state.Inv.Slots {
			clone.Slots[i] = copyStack(v)
		}
		if s.add(clone, v) != nil {
			p.sys("背包已满，插件物品未发放。")
			return
		}
		data := map[string]any{"reason": "plugin.give_item", "item": v.ID, "count": v.Count, "stack": itemSnapshot(v)}
		if !s.allowed(api.InventoryChanging, r, p, data) {
			return
		}
		p.state.Inv.Slots = clone.Slots
		r.Dirty = true
		s.inventory(p, 0, 0)
		s.emit(api.InventoryChanged, r, p, data)
	case "drop_item", "drop_stack":
		if !r.usesSurvival() {
			return
		}
		id := a.ItemID
		if a.ItemName != "" {
			id = s.rules.byName[a.ItemName]
		}
		v := stack(id, a.Count, 0)
		if a.Type == "drop_stack" {
			v = (*Stack)(a.Stack.Clone())
		}
		drops, err := s.prepareDrops(r, nil, Stacks{v}, a.X, a.Y, a.Z, "plugin")
		if err != nil {
			return
		}
		s.publishDrops(r, nil, drops, "plugin")
	case "teleport":
		if p != nil {
			s.teleport(p, a.X, a.Y, a.Z, "plugin")
		}
	case "change_dimension":
		if p != nil {
			s.changePlayerDimension(nil, p, a.Dimension, "plugin")
		}
	case "heal":
		s.heal(p, a.Amount, "plugin")
	case "damage_player":
		s.damagePlayer(p, a.Amount, a.Reason, &api.DamageContext{Cause: "plugin"})
	case "damage_entity":
		if v := r.blockExt().Vehicles[a.EntityID]; v != nil {
			s.damageVehicleSource(r, nil, v, a.Amount, a.Reason, &api.DamageContext{Cause: "plugin"})
		} else if m := r.Mobs[a.EntityID]; m != nil {
			s.damageMobSource(r, nil, m, a.Amount, a.Reason, &api.DamageContext{Cause: "plugin"})
		}
	case "set_block":
		if !number(a.X, r.minCoord(), r.maxCoord()) || !number(a.Y, 1, 127) || !number(a.Z, r.minCoord(), r.maxCoord()) || a.X != math.Trunc(a.X) || a.Y != math.Trunc(a.Y) || a.Z != math.Trunc(a.Z) || !s.rules.validBlock(a.Block) || r.Mode == "creative" && !s.creativeBlock(a.Block) {
			return
		}
		key := blockKey(int(a.X), int(a.Y), int(a.Z))
		if a.Block != blkExplosiveTNT && s.primedTNT(r, key) {
			return
		}
		if !s.torchSupported(r, key, a.Block) || !s.redstoneSupported(r, key, a.Block) || !s.mechanicSupported(r, key, a.Block) {
			return
		}
		if r.Containers[key] != nil {
			return
		}
		if v := mechanicSnapshot(r, key); v != nil && v.Record != nil {
			return
		}
		data := s.blockData(r, key, a.Block)
		component := s.redstoneBlockChangeData(r, nil, key, a.Block, "plugin.redstone")
		if component != nil {
			data = component
		}
		for k, v := range s.mechanicBlockChangeData(r, nil, key, a.Block, "plugin.mechanic") {
			data[k] = v
		}
		name := api.BlockPlacing
		if a.Block == 0 {
			name = api.BlockBreaking
		}
		if !s.allowed(name, r, nil, data) {
			return
		}
		if component != nil && !s.allowed(api.RedstoneComponentChanging, r, nil, data) {
			return
		}
		if !s.mechanicBlockBefore(r, nil, data) {
			return
		}
		changes, err := s.prepareTorchChanges(r, nil, key, a.Block, 0)
		if err != nil {
			log.Printf("plugin torch physics: %v", err)
			return
		}
		oldBlock := s.block(r, int(a.X), int(a.Y), int(a.Z))
		r.Edits[key] = a.Block
		s.clearInactiveFire(r, key, a.Block)
		s.trackRedstoneBlock(r, key, a.Block)
		s.trackMechanicBlock(r, key, a.Block)
		delete(r.Authors, key)
		s.queueLeafChange(r, key, oldBlock, a.Block)
		r.Dirty = true
		r.rt().redstoneDirty = true
		r.Active = time.Now().UnixMilli()
		if r.usesSurvival() {
			s.broadcastChunk(r, key, a.Block)
		} else {
			out := binary.LittleEndian.AppendUint32([]byte{3}, toWireKey(key))
			out = append(out, byte(a.Block))
			for peer := range r.Peers {
				peer.enqueue(websocket.BinaryMessage, out)
			}
		}
		s.blockEvents(r, nil, data)
		if _, ok := data["mechanicAfter"]; ok {
			s.broadcastMechanic(r, key)
		}
		if _, ok := data["railAfter"]; ok {
			s.broadcastRail(r, key)
		}
		s.applyTorchChanges(r, nil, changes)
	case "set_blocks":
		if len(a.Blocks) == 0 || len(a.Blocks) > 1024 {
			return
		}
		var edits [][4]int
		for _, b := range a.Blocks {
			if !r.inside(b.X, b.Z) || b.Y < 1 || b.Y >= 127 || !s.rules.validBlock(b.Block) || (r.Mode == "creative" && !s.creativeBlock(b.Block)) {
				continue
			}
			key := blockKey(b.X, b.Y, b.Z)
			if r.Containers[key] != nil || (b.Block != blkExplosiveTNT && s.primedTNT(r, key)) {
				continue
			}
			edits = append(edits, [4]int{b.X, b.Y, b.Z, b.Block})
		}
		if len(edits) == 0 {
			return
		}
		data := map[string]any{"reason": "plugin.set_blocks", "count": len(edits)}
		if !s.allowed(api.BlockPlacing, r, nil, data) {
			return
		}
		s.setBlocksWithContext(r, edits, 0, "plugin", nil, data)
		s.emit(api.BlockPlaced, r, nil, data)
	default:
		log.Printf("unknown plugin action %q", a.Type)
	}
}
func (s *Server) executeCommand(p *Peer, msg string) bool {
	e := s.emit(api.CommandExecuting, p.room, p, map[string]any{"command": limitText(msg, 240)})
	if e.Cancelled {
		if e.Reason != "" {
			p.sys(limitText(e.Reason, 240))
		}
		return false
	}
	msg = strings.TrimSpace(e.String("command"))
	parts := strings.Fields(msg)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return false
	}
	name := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if h, ok := s.handleFullCommand(p, name, parts); h {
		if ok {
			s.emit(api.CommandExecuted, p.room, p, map[string]any{"command": msg, "handled": true, "success": true})
		}
		return ok
	}
	handled, success := true, true
	switch name {
	case "op", "deop", "defaultgamemode":
		if !s.canGameCommand(p, commandPermission(name)) {
			p.sys("这个命令需要 OP 或对应的服务器权限。")
			success = false
			break
		}
		result, err := s.console(s.commandActor(p), strings.Join(parts, " "), p.room.root().ID, p.name)
		if err != nil {
			p.sys(err.Error())
			success = false
		} else {
			p.sys(fmt.Sprint(result))
		}
	case "help":
		help := "命令：/help、/spawn、/plugins、/time query、/seed、/worldinfo"
		if s.canGameCommand(p, "server.gamemode") {
			help += "、/time set day|night、/summon cow|pig|sheep|chicken|zombie|spider、/gamemode、/difficulty、/gamerule、/weather、/tp、/give、/kill、/xp、/effect、/spawnpoint"
		}
		for _, status := range s.plugins.Status() {
			if status.State == "loaded" {
				for _, cmd := range status.Commands {
					if cmd.Permission != "owner" || p.owner || s.isOperator(p.sid) {
						help += "、/" + cmd.Name
					}
				}
			}
		}
		p.sys(limitText(help, 600))
	case "plugins":
		parts := []string{}
		for _, status := range s.plugins.Status() {
			parts = append(parts, status.Name+" "+status.Version+" ("+status.State+")")
		}
		p.sys(limitText("插件："+strings.Join(parts, "，"), 240))
	case "spawn":
		sp := s.engine.spawn(p.room)
		success = s.teleport(p, float64(sp[0])+0.5, float64(sp[1]), float64(sp[2])+0.5, "command")
	case "time":
		if len(parts) == 2 && parts[1] == "query" {
			p.sys(fmt.Sprintf("世界第 %d 天，时间 %d / 24000。", p.room.Time/24000+1, p.room.Time%24000))
		} else if !s.canGameCommand(p, "server.admin") {
			p.sys("修改时间需要 OP 权限。")
			success = false
		} else if len(parts) == 3 && parts[1] == "set" {
			ticks, err := strconv.ParseUint(parts[2], 10, 64)
			if n, ok := map[string]uint64{"day": 1000, "noon": 6000, "night": 13000, "midnight": 18000}[parts[2]]; ok {
				ticks = n
				err = nil
			}
			if err != nil || ticks >= 24000 {
				p.sys("用法：/time set day|noon|night|midnight|0-23999")
				success = false
			} else {
				success = s.setWorldTime(p.room, p, p.room.Time/24000*24000+ticks, "command")
				if success {
					p.sys("世界时间已更新。")
				}
			}
		} else {
			p.sys("用法：/time query 或 /time set day|night")
			success = false
		}
	case "summon":
		if !s.canGameCommand(p, "server.admin") {
			p.sys("召唤生物需要 OP 权限。")
			success = false
			break
		}
		if len(parts) != 2 && len(parts) != 5 {
			p.sys("用法：/summon cow|pig|sheep|chicken|zombie|spider [x y z]")
			success = false
			break
		}
		x, y, z := p.state.X-math.Sin(p.state.Yaw)*3, p.state.Y, p.state.Z-math.Cos(p.state.Yaw)*3
		if len(parts) == 5 {
			var err error
			x, err = strconv.ParseFloat(parts[2], 64)
			if err != nil {
				success = false
			}
			y, err = strconv.ParseFloat(parts[3], 64)
			if err != nil {
				success = false
			}
			z, err = strconv.ParseFloat(parts[4], 64)
			if err != nil {
				success = false
			}
		} else if !s.mobFits(p.room, parts[1], x, y, z) || !s.mobSolid(p.room, int(x), int(y)-1, int(z)) {
			y = float64(s.surface(p.room, int(x), int(z)))
		}
		if !success {
			p.sys("坐标格式错误。")
			break
		}
		if _, err := s.spawnMob(p.room, p, parts[1], x, y, z, "command"); err != nil {
			p.sys(err.Error())
			success = false
		} else {
			p.sys("生物已生成。")
		}
	default:
		if h, ok2 := s.extCommand(p, name, parts); h {
			success = ok2
			break
		}
		actions, ok, err := s.plugins.Execute(name, e, parts[1:])
		handled = ok
		if err != nil {
			p.sys(limitText("命令执行失败："+err.Error(), 240))
			success = false
		} else {
			s.queueActions(actions)
		}
		if !ok {
			p.sys("未知命令，输入 /help 查看帮助。")
			success = false
		}
	}
	if handled && success {
		s.emit(api.CommandExecuted, p.room, p, map[string]any{"command": msg, "handled": handled, "success": success})
	}
	return handled && success
}
