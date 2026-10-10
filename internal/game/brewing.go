package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"time"
)

// ---------------------------------------------------------------------------
// Potions and brewing. Item ids (registered in rules.json by the export
// script): 400 Glass Bottle, 401 Water Bottle, 402 Awkward Potion, 403-410
// potions. The world has no nether wart / sugar / glistering melon, so the
// recipes use the available ingredients (documented in the README).
// ---------------------------------------------------------------------------

const (
	itemGlassBottle = 400
	itemWaterBottle = 401
	itemAwkward     = 402
	itemBucket      = 306
	itemWaterBucket = 307
	itemMilkBucket  = 309
)

type potionDef struct {
	Effect string
	Ticks  int // 20 ticks per second
	Amp    int
}

var potions = map[int]potionDef{
	403: {"instant_health", 1, 0},
	404: {"regeneration", 900, 0},
	405: {"speed", 3600, 0},
	406: {"strength", 3600, 0},
	407: {"fire_resistance", 3600, 0},
	408: {"poison", 900, 0},
	409: {"instant_damage", 1, 0},
	410: {"water_breathing", 3600, 0},
}

// brewRecipes maps (input bottle, ingredient item key/name) to the result.
func (s *Server) brewResult(bottle, ingredient int) int {
	name := s.rules.byID[ingredient].Name
	switch bottle {
	case itemWaterBottle:
		if name == "Melon Slice" {
			return itemAwkward
		}
	case itemAwkward:
		switch name {
		case "Gold Nugget":
			return 403
		case "Wailer Tear":
			return 404
		case "Feather":
			return 405
		case "Cinder Dust":
			return 406
		case "Lava Gel":
			return 407
		case "Spider Eye":
			return 408
		case "Lapis Lazuli":
			return 410
		}
	case 403, 408:
		if name == "Rotten Flesh" {
			return 409
		}
	}
	return 0
}

var brewIngredientNames = map[string]bool{"Melon Slice": true, "Gold Nugget": true, "Wailer Tear": true, "Feather": true, "Cinder Dust": true, "Lava Gel": true, "Spider Eye": true, "Lapis Lazuli": true, "Rotten Flesh": true}

func brewIngredient(s *Server, id int) bool { return brewIngredientNames[s.rules.byID[id].Name] }

const brewTime = 200 // server ticks (100 ms) = 20 s

func (s *Server) brewTick(r *Room, key uint64, c *Container) {
	if len(c.Slots) != 5 {
		return
	}
	ing := c.Slots[3]
	can := false
	if ing != nil {
		for i := 0; i < 3; i++ {
			if b := c.Slots[i]; b != nil && s.brewResult(b.ID, ing.ID) > 0 {
				can = true
			}
		}
	}
	if c.Burn <= 0 && can {
		if f := c.Slots[4]; f != nil && s.rules.byID[f.ID].Name == "Cinder Dust" {
			c.Burn = 20
			c.BurnTotal = 20
			c.Slots[4] = sized(f, f.Count-1)
			r.Dirty = true
		}
	}
	if !can || c.Burn <= 0 {
		if c.Progress != 0 {
			c.Progress = 0
			r.Dirty = true
		}
		return
	}
	c.Progress++
	r.Dirty = true
	if c.Progress < brewTime {
		return
	}
	x, y, z := coords(key)
	before := make(Stacks, len(c.Slots))
	for i, v := range c.Slots {
		before[i] = copyStack(v)
	}
	data := map[string]any{"x": x, "y": y, "z": z, "before": before, "reason": "brew_complete"}
	if !s.allowed(api.BrewingCompleting, r, nil, data) {
		c.Progress = brewTime
		return
	}
	c.Progress = 0
	c.Burn--
	for i := 0; i < 3; i++ {
		if b := c.Slots[i]; b != nil {
			if out := s.brewResult(b.ID, ing.ID); out > 0 {
				result := copyStack(b)
				result.ID = out
				result.Count = 1
				c.Slots[i] = result
			}
		}
	}
	c.Slots[3] = sized(ing, ing.Count-1)
	data["after"] = c.Slots
	s.emit(api.BrewingCompleted, r, nil, data)
	soundAt(r, "block.brewing_stand.brew", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
}

func (s *Server) brewWindows(r *Room) {
	for p := range r.Peers {
		if p.window == nil || p.kind() != "brew" {
			continue
		}
		c := r.Containers[p.window.Key]
		if c == nil {
			continue
		}
		p.json(map[string]any{"t": "wslot", "w": p.window.ID, "s": c.Slots})
		p.json(map[string]any{"t": "wprop", "w": p.window.ID, "brew": c.Progress, "total": brewTime, "fuel": c.Burn, "fuelMax": 20})
	}
}

// drink finishes drinking a potion or milk bucket.
func (s *Server) drink(p *Peer, held *Stack) {
	if p == nil || p.state == nil || p.state.Dead || held == nil || !isDrink(held.ID) || !reflect.DeepEqual(held, s.heldItem(p)) {
		return
	}
	result := itemGlassBottle
	if held.ID == itemMilkBucket {
		result = itemBucket
	}
	before := cloneStacks(p.state.Inv.Slots)
	after, ok := s.mechanicReplacement(p, result)
	if !ok {
		return
	}
	slot, original := p.state.Inv.Selected, copyStack(held)
	output := sized(original, 1)
	output.ID, output.Damage = result, 0
	data := map[string]any{"reason": "drink", "source": "player", "target": playerTarget(p), "slot": slot,
		"stack": itemSnapshot(original), "item": original.ID, "result": itemSnapshot(output),
		"before": itemSnapshot(original), "after": itemSnapshot(after[slot]),
		"inventoryBefore": before, "inventoryAfter": cloneStacks(after), "count": 1}
	if s.isCreative(p) {
		data["count"] = 0
	}
	if !s.allowed(api.ItemConsuming, p.room, p, data) || !reflect.DeepEqual(before, p.state.Inv.Slots) || p.state.Inv.Selected != slot || !s.allowed(api.InventoryChanging, p.room, p, data) {
		return
	}
	// Damage potions can kill the drinker. Commit the bottle before death
	// drops/clears inventory, after every effect/damage/death before event.
	commit := func() bool {
		if p.state.Dead || p.state.Inv.Selected != slot || !reflect.DeepEqual(before, p.state.Inv.Slots) {
			return false
		}
		p.state.Inv.Slots = cloneStacks(after)
		p.room.Dirty = true
		s.emit(api.InventoryChanged, p.room, p, data)
		return true
	}
	if original.ID == itemMilkBucket {
		if !s.clearEffects(p, "milk", commit) {
			return
		}
	} else if def, exists := potions[original.ID]; exists {
		if !s.addEffect(p, def.Effect, def.Ticks, def.Amp, commit) {
			return
		}
	} else if !commit() {
		return
	}
	s.emit(api.ItemConsumed, p.room, p, data)
	p.json(map[string]any{"t": "itemfx", "k": "drink", "i": original.ID})
	s.inventory(p, 0, 0)
	s.health(p)
}

func (s *Server) fillPlayerBottle(p *Peer, m Message, target int) bool {
	held := s.heldItem(p)
	if held == nil || held.ID != itemGlassBottle || !isWater(target) || !p.room.usesSurvival() {
		return false
	}
	if p.state.Dead || !s.reachable(p, m) || !s.canBuild(p) {
		return true
	}
	slot := p.state.Inv.Selected
	before := cloneStacks(p.state.Inv.Slots)
	after, ok := s.mechanicReplacement(p, itemWaterBottle)
	if !ok {
		return true
	}
	output := sized(held, 1)
	output.ID, output.Damage = itemWaterBottle, 0
	data := map[string]any{"reason": "bottle_fill", "behavior": "bottle_fill", "source": "player", "fluid": target,
		"x": m.X, "y": m.Y, "z": m.Z, "target": &api.Combatant{Kind: "block", X: float64(m.X), Y: float64(m.Y), Z: float64(m.Z)},
		"slot": slot, "stack": itemSnapshot(held), "result": itemSnapshot(output),
		"before": itemSnapshot(held), "after": itemSnapshot(after[slot]),
		"inventoryBefore": before, "inventoryAfter": cloneStacks(after)}
	if !s.allowed(api.BottleFilling, p.room, p, data) || !s.allowed(api.InventoryChanging, p.room, p, data) ||
		p.state.Inv.Selected != slot || !reflect.DeepEqual(before, p.state.Inv.Slots) || s.block(p.room, m.X, m.Y, m.Z) != target {
		return true
	}
	p.state.Inv.Slots = cloneStacks(after)
	p.room.Dirty = true
	s.emit(api.InventoryChanged, p.room, p, data)
	s.emit(api.BottleFilled, p.room, p, data)
	soundAt(p.room, "item.bottle.fill", float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, nil)
	s.inventory(p, 0, 0)
	return true
}

func isDrink(id int) bool { return id == itemMilkBucket || id >= itemWaterBottle && id <= 410 }

func init() {
	onRoomTick(func(s *Server, r *Room) {
		if !r.usesSurvival() {
			return
		}
		for key, c := range r.Containers {
			if c.Kind == "brew" {
				s.brewTick(r, key, c)
			}
		}
		if r.rt().tickCount%5 == 0 {
			s.brewWindows(r)
		}
	})
	onMessage("use", func(s *Server, p *Peer, m Message) bool {
		held := s.heldItem(p)
		if held == nil || !isDrink(held.ID) || !p.room.usesSurvival() {
			return false
		}
		switch m.S {
		case 0:
			p.useAt = time.Now()
			p.useSlot = p.state.Inv.Selected
		case 1:
			p.useAt = time.Time{}
		case 2:
			if !p.useAt.IsZero() && p.useSlot == p.state.Inv.Selected && time.Since(p.useAt) >= 1400*time.Millisecond {
				p.useAt = time.Time{}
				s.drink(p, held)
			}
		}
		return true
	})
	// Filling a glass bottle from water.
	onPlace(20, func(s *Server, p *Peer, m Message, target int) bool {
		return s.fillPlayerBottle(p, m, target)
	})
}
