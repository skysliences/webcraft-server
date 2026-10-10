package game

import (
	"math"
	"strings"

	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// Shared helpers for the survival extension modules.
// ---------------------------------------------------------------------------

func isWater(b int) bool { return b == 9 || b >= 96 && b <= 103 }
func isLava(b int) bool  { return b == 68 || b >= 104 && b <= 107 }
func isFluid(b int) bool { return isWater(b) || isLava(b) }
func isClimbable(b int) bool {
	return b >= 128 && b <= 131
}

// worldPeers lists every connected player in every dimension of a world.
func worldPeers(r *Room) []*Peer {
	root := r.root()
	out := make([]*Peer, 0, len(root.Peers))
	for p := range root.Peers {
		out = append(out, p)
	}
	for _, d := range root.dims {
		for p := range d.Peers {
			out = append(out, p)
		}
	}
	return out
}

// containerKinds maps block base names to the window kind they open.
var containerKinds = map[string]string{"Chest": "chest", "Furnace": "furnace", "Hopper": "hopper", "Brewing Stand": "brew", "Shulker Box": "chest", "Ender Chest": "ender", "Dispenser": "dispenser", "Dropper": "dropper"}

func (s *Server) containerBlock(b int) bool {
	return containerKinds[s.nameOf(b)] != ""
}

// keepsContents: breaking keeps the inventory inside the dropped item.
func (s *Server) keepsContents(b int) bool { return s.nameOf(b) == "Shulker Box" }

// spillContainer drops a container's contents when its block disappears
// without a player breaking it (explosions, pistons, fluids...).
func (s *Server) spillContainer(r *Room, key uint64, c *Container) {
	delete(r.Containers, key)
	for other := range r.Peers {
		if other.window != nil && other.window.Key == key {
			s.closeWindow(other)
			other.json(map[string]any{"t": "wclose", "w": other.nextWindow})
		}
	}
	if !r.usesSurvival() {
		return
	}
	x, y, z := coords(key)
	var stacks Stacks
	for _, v := range c.Slots {
		if v != nil {
			stacks = append(stacks, copyStack(v))
		}
	}
	if len(stacks) == 0 {
		return
	}
	if drops, err := s.prepareDrops(r, nil, stacks, float64(x)+.5, float64(y)+.5, float64(z)+.5, "container_spill"); err == nil {
		s.publishDrops(r, nil, drops, "container_spill")
	}
}

const shulkerDataKey = "mcraft:shulker_contents"

// dropsAt computes block drops, including position-specific data.
func (s *Server) dropsAt(r *Room, key uint64, b, held int) Stacks {
	name := s.nameOf(b)
	if mechanicKinds[b] != "" || isRailBlock(b) {
		var out Stacks
		for item, block := range mechanicItems {
			if block == b && s.rules.max(item) > 0 {
				out = Stacks{stack(item, 1, 0)}
				break
			}
		}
		if s.gameRule(r, "doTileDrops") {
			return out
		}
		return nil
	}
	switch name {
	case "Shulker Box":
		return Stacks{s.boxDrop(r.Containers[key])}
	case "Oak Door":
		if strings.Contains(s.fullName(b), "Upper") {
			return nil // the lower half drops the door
		}
		return Stacks{stack(s.id("Oak Door"), 1, 0)}
	case "Bed":
		if strings.Contains(s.fullName(b), "Head") {
			return nil
		}
		return Stacks{stack(s.id("Bed"), 1, 0)}
	case "Piston", "Piston Head":
		if name == "Piston Head" {
			return nil
		}
		if d := r.blockExt().Blocks[key]; d != nil && d.Sticky {
			return Stacks{stack(s.id("Sticky Piston"), 1, 0)}
		}
		return Stacks{stack(s.id("Piston"), 1, 0)}
	case "Redstone Wire":
		return Stacks{stack(s.id("Spark Dust"), 1, 0)}
	case "Lever", "Stone Button", "Redstone Torch", "Redstone Lamp", "Hopper", "Brewing Stand", "Cactus":
		if id := s.id(name); id > 0 {
			return Stacks{stack(id, 1, 0)}
		}
	case "Ender Chest":
		return Stacks{stack(s.id("Obsidian"), 8, 0)}
	}
	if s.isCreativeRoomHolder(r) {
		return nil
	}
	return s.drops(b, held)
}

// isCreativeRoomHolder is a hook point for creative players; drops are
// suppressed by the caller for creative game mode instead.
func (s *Server) isCreativeRoomHolder(r *Room) bool { return false }

// afterPlace runs after a generic block placement by a player.
func (s *Server) afterPlace(p *Peer, x, y, z, placed int, m Message) {
	for _, h := range afterPlaceHooks {
		h(s, p, x, y, z, placed, m)
	}
}

type afterPlaceHook func(s *Server, p *Peer, x, y, z, placed int, m Message)

var afterPlaceHooks []afterPlaceHook

func onAfterPlace(fn afterPlaceHook) { afterPlaceHooks = append(afterPlaceHooks, fn) }

// canBuild reports whether the player may modify blocks (lock, mode).
func (s *Server) canBuild(p *Peer) bool {
	if p.room.Lock != 0 && !p.owner {
		p.sys("只有房主可以建造。")
		return false
	}
	gm := s.gameMode(p)
	if gm == "adventure" || gm == "spectator" {
		return false
	}
	return true
}

// heldStack returns the selected stack and its item definition.
func (s *Server) heldStack(p *Peer) (*Stack, Item) {
	v := s.heldItem(p)
	if v == nil {
		return nil, Item{}
	}
	return v, s.rules.byID[v.ID]
}

// consumeHeld removes n items from the selected slot (not in creative).
func (s *Server) consumeHeld(p *Peer, n int) {
	if s.isCreative(p) {
		return
	}
	v := s.heldItem(p)
	if v == nil {
		return
	}
	p.state.Inv.Slots[p.state.Inv.Selected] = sized(v, v.Count-n)
	p.room.Dirty = true
}

// replaceHeld swaps the selected stack (e.g. bucket -> water bucket). When
// the stack has more than one item the new item goes to the inventory.
func (s *Server) replaceHeld(p *Peer, v *Stack) {
	cur := s.heldItem(p)
	if s.isCreative(p) && cur != nil && v != nil && cur.ID != v.ID {
		if left := s.add(p.state.Inv, v); left != nil {
			p.state.Recovery = append(p.state.Recovery, left)
		}
		return
	}
	if cur != nil && cur.Count > 1 {
		p.state.Inv.Slots[p.state.Inv.Selected] = sized(cur, cur.Count-1)
		if left := s.add(p.state.Inv, v); left != nil {
			s.dropFromPlayer(p, left)
		}
	} else {
		p.state.Inv.Slots[p.state.Inv.Selected] = v
	}
	p.room.Dirty = true
}

// dropFromPlayer throws a stack in front of the player.
func (s *Server) dropFromPlayer(p *Peer, v *Stack) []*ItemDrop {
	if v == nil || !p.room.usesSurvival() {
		return nil
	}
	dropData := map[string]any{"stack": itemSnapshot(v), "count": v.Count}
	if !s.allowed(api.ItemDropping, p.room, p, dropData) {
		return nil
	}
	st := p.state
	x := min(float64(p.room.width())-.2, max(.2, st.X))
	z := min(float64(p.room.width())-.2, max(.2, st.Z))
	y := min(127.5, max(0, st.Y+1.3))
	drops, err := s.prepareDrops(p.room, p, Stacks{v}, x, y, z, "player_drop")
	if err != nil {
		p.sys(err.Error())
		return nil
	}
	for _, d := range drops {
		d.VX = -math.Sin(st.Yaw) * math.Cos(st.Pitch) * .3
		d.VZ = -math.Cos(st.Yaw) * math.Cos(st.Pitch) * .3
		d.VY = math.Sin(st.Pitch)*.3 + .1
		d.PickupDelayMS = 2000
	}
	s.publishDrops(p.room, p, drops, "player_drop")
	s.emit(api.ItemDropped, p.room, p, dropData)
	return drops
}

// lookDir returns the player's view direction.
func lookDir(st *Player) (float64, float64, float64) {
	return -math.Sin(st.Yaw) * math.Cos(st.Pitch), math.Sin(st.Pitch), -math.Cos(st.Yaw) * math.Cos(st.Pitch)
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
