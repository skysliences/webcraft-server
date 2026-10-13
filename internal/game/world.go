package game

import (
	"encoding/binary"
	"math"
	api "mcraft/backend/pkg/plugin"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// World foundation: world width, block-key migration, dimension sub-rooms and
// the extension hook registry used by the survival feature modules.
// ---------------------------------------------------------------------------

const saveVersionCurrent = 9

// maxWorldW is the largest world the server stores. The browser engine
// accepts up to 5120, but block keys use 12 bits per horizontal axis.
const maxWorldW = 4096

// Width used for newly created survival worlds.
const newSurvivalW = 4096

// PlayerExt is persisted with each player (survival extensions).
type PlayerExt struct {
	Dim       string  `json:"dim,omitempty"`  // "", "nether", "end"
	GameMode  string  `json:"gm,omitempty"`   // "", survival, creative, adventure, spectator
	XPLevel   int     `json:"xpl,omitempty"`  // experience level
	XPPoints  int     `json:"xpp,omitempty"`  // points into the current level
	XPTotal   int     `json:"xpt,omitempty"`  // lifetime score
	EnchSeed  int64   `json:"es,omitempty"`   // enchanting table seed
	SpawnX    int     `json:"sx,omitempty"`   // bed spawn point
	SpawnY    int     `json:"sy,omitempty"`   // (SpawnSet must be true)
	SpawnZ    int     `json:"sz,omitempty"`   //
	SpawnSet  bool    `json:"ss,omitempty"`   //
	FireTicks int     `json:"fire,omitempty"` // burning ticks remaining
	Absorb    float64 `json:"ab,omitempty"`   // absorption hearts
	Air       int     `json:"air,omitempty"`  // remaining oxygen ticks (0..300)
	// Effects: name -> remaining ticks/amplifier.
	Effects     map[string]*Effect `json:"fx,omitempty"`
	EndCredits  bool               `json:"credits,omitempty"`
	EnderChest  Stacks             `json:"ender,omitempty"`
	PortalCool  int                `json:"pcool,omitempty"`
	SeenCredits bool               `json:"seen,omitempty"`
	LastDeathXP int                `json:"-"`
	portalTicks int
}

// Effect is an active status effect.
type Effect struct {
	Ticks int `json:"t"`
	Amp   int `json:"a"`
}

// RoomExt is persisted with each root world.
type RoomExt struct {
	Vehicles      map[uint32]*Vehicle `json:"vehicles,omitempty"`
	NextVehicleID uint32              `json:"nextVehicleId,omitempty"`
	Difficulty    string              `json:"difficulty,omitempty"` // peaceful, easy, normal, hard
	Hardcore      bool                `json:"hardcore,omitempty"`
	Rules         map[string]bool     `json:"rules,omitempty"` // gamerules (bool)
	Rain          bool                `json:"rain,omitempty"`
	Thunder       bool                `json:"thunder,omitempty"`
	WeatherT      int                 `json:"weatherT,omitempty"` // ticks until next weather change
	Beaten        bool                `json:"beaten,omitempty"`
	// Per-block data: piston stickiness, hopper facing, redstone power...
	Blocks map[uint64]*BlockData `json:"blocks,omitempty"`
	// Scheduled ticks (crop growth handled by random ticks instead).
	EndPortalLit    bool `json:"endLit,omitempty"`
	RandomTickSpeed *int `json:"randomTickSpeed,omitempty"`
}

// BlockData is extra state stored for a block position.
type BlockData struct {
	Kind     string             `json:"k,omitempty"`
	Facing   int                `json:"f,omitempty"`
	Sticky   bool               `json:"s,omitempty"`
	Power    int                `json:"p,omitempty"`
	Fuse     int                `json:"fuse,omitempty"`
	Age      int                `json:"age,omitempty"`
	Owner    string             `json:"o,omitempty"`
	Redstone *RedstoneComponent `json:"redstone,omitempty"`
	Mechanic *MechanicState     `json:"mechanic,omitempty"`
	Rail     *RailState         `json:"rail,omitempty"`
	TNT      *TNTState          `json:"tnt,omitempty"`
	Fire     *TNTState          `json:"fire,omitempty"`
}

// roomRuntime is per-room state that is not persisted.
type roomRuntime struct {
	spawnLights        *spawnLighting
	vehicleView        *VehicleState
	fishing            *fishingRuntime
	pending            []uint64        // block positions queued for neighbour updates
	queued             map[uint64]bool // de-duplicates pending
	scheduled          map[uint64]int  // key -> ticks until a scheduled update
	projectiles        map[uint32]*Projectile
	orbs               map[uint32]*XPOrb
	nextEnt            uint32
	tickCount          uint64
	redstone           *RedstoneState
	redstoneDirty      bool
	redstoneQueryDepth int
	redstoneQueries    map[uint64]bool
	leafDecay          *leafDecayRuntime
}

func (r *Room) rt() *roomRuntime {
	if r.extRT == nil {
		r.extRT = &roomRuntime{queued: map[uint64]bool{}, scheduled: map[uint64]int{}, projectiles: map[uint32]*Projectile{}, orbs: map[uint32]*XPOrb{}, nextEnt: 1}
	}
	return r.extRT
}

func (r *Room) root() *Room {
	if r.parent != nil {
		return r.parent
	}
	return r
}
func (r *Room) seedID() string { return r.root().ID }
func (r *Room) ext() *RoomExt {
	root := r.root()
	if root.Ext == nil {
		root.Ext = &RoomExt{
			Rules:  map[string]bool{},
			Blocks: map[uint64]*BlockData{},
		}
	} else {
		if root.Ext.Rules == nil {
			root.Ext.Rules = map[string]bool{}
		}
		if root.Ext.Blocks == nil {
			root.Ext.Blocks = map[uint64]*BlockData{}
		}
	}
	return root.Ext
}
func (r *Room) blockExt() *RoomExt {
	if r.Ext == nil {
		r.Ext = &RoomExt{
			Rules:  map[string]bool{},
			Blocks: map[uint64]*BlockData{},
		}
	} else {
		if r.Ext.Rules == nil {
			r.Ext.Rules = map[string]bool{}
		}
		if r.Ext.Blocks == nil {
			r.Ext.Blocks = map[uint64]*BlockData{}
		}
	}
	return r.Ext
}
func (r *Room) hardcore() bool { return r.root().Ext != nil && r.root().Ext.Hardcore }
func (r *Room) online() int {
	root := r.root()
	n := len(root.Peers)
	for _, d := range root.dims {
		n += len(d.Peers)
	}
	return n
}
func (r *Room) width() int {
	if r.W < 16 || r.W > maxWorldW || r.W%16 != 0 {
		return 1024
	}
	return r.W
}
func (r *Room) cn() int { return r.width() / 16 }
func (r *Room) maxCoord() float64 {
	if r.Infinite {
		return worldBorder - 0.01
	}
	return float64(r.width()) - 0.01
}
func (r *Room) minCoord() float64 {
	if r.Infinite {
		return -worldBorder + 0.01
	}
	return 0
}
func (r *Room) inside(x, z int) bool {
	if r.Infinite {
		return x > -worldBorder && x < worldBorder && z > -worldBorder && z < worldBorder
	}
	w := r.width()
	return x >= 0 && x < w && z >= 0 && z < w
}
func (r *Room) dimName() string {
	if r.Dim == "" {
		return "overworld"
	}
	return r.Dim
}
func (r *Room) dimIndex() byte {
	switch r.dimName() {
	case "nether":
		return 1
	case "end":
		return 2
	}
	return 0
}
func (r *Room) gen() int {
	if r.Mode == "creative" {
		return 0
	}
	switch r.dimName() {
	case "nether":
		return 2
	case "end":
		return 3
	}
	return 1
}

// allRooms lists root worlds and their loaded dimensions.
func (s *Server) allRooms() []*Room {
	out := make([]*Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r)
		for _, d := range []string{"nether", "end"} {
			if c := r.dims[d]; c != nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// Old 10-bit key layout (pre-v7 saves and the creative wire protocol).
func oldKey(x, y, z int) uint32 { return uint32(x<<17 | z<<7 | y) }
func oldCoords(k uint32) (int, int, int) {
	return int(k >> 17 & 1023), int(k & 127), int(k >> 7 & 1023)
}
func fromWireKey(k uint32) uint64 { x, y, z := oldCoords(k); return blockKey(x, y, z) }
func toWireKey(k uint64) uint32 {
	x, y, z := coords(k)
	return oldKey(x&1023, y, z&1023)
}

func (s *Server) afterLoadRoom(r *Room, raw *diskRoom, dim string) {
	r.W = raw.W
	if r.W == 0 {
		r.W = 1024
	}
	r.Dim = "overworld"
	r.Infinite, r.LegacyW, r.Generator = raw.Infinite, raw.LegacyW, raw.Generator
	if dim != "" {
		r.Dim = dim
	}
	r.Ext = raw.Ext
	if raw.Version < 7 {
		edits := map[uint64]int{}
		for k, b := range r.Edits {
			edits[fromWireKey(uint32(k))] = b
		}
		r.Edits = edits
		containers := map[uint64]*Container{}
		for k, c := range r.Containers {
			containers[fromWireKey(uint32(k))] = c
		}
		r.Containers = containers
		authors := map[uint64]EditAuthor{}
		for k, a := range r.Authors {
			authors[fromWireKey(uint32(k))] = a
		}
		r.Authors = authors
		if r.Ext != nil {
			blocks := map[uint64]*BlockData{}
			for k, v := range r.Ext.Blocks {
				blocks[fromWireKey(uint32(k))] = v
			}
			r.Ext.Blocks = blocks
		}
	}
	if raw.Version == 7 {
		convert := func(k uint64) uint64 { return blockKey(int(k>>19&4095), int(k&127), int(k>>7&4095)) }
		edits := map[uint64]int{}
		for k, v := range r.Edits {
			edits[convert(k)] = v
		}
		r.Edits = edits
		containers := map[uint64]*Container{}
		for k, v := range r.Containers {
			containers[convert(k)] = v
		}
		r.Containers = containers
		authors := map[uint64]EditAuthor{}
		for k, v := range r.Authors {
			authors[convert(k)] = v
		}
		r.Authors = authors
		if r.Ext != nil {
			blocks := map[uint64]*BlockData{}
			for k, v := range r.Ext.Blocks {
				blocks[convert(k)] = v
			}
			r.Ext.Blocks = blocks
		}
	}
	if s.config.InfiniteWorlds && !r.Infinite {
		r.Infinite = true
		r.LegacyW = r.width()
		r.Generator = generatorVersion
		r.Dirty = true
	}
}

func (s *Server) attachDim(parent, c *Room) {
	if parent.dims == nil {
		parent.dims = map[string]*Room{}
	}
	c.parent = parent
	c.Infinite = parent.Infinite
	if c.Generator == "" {
		c.Generator = parent.Generator
	}
	c.Name, c.Mode, c.Lock = parent.Name, parent.Mode, parent.Lock
	c.OwnerHash, c.OwnerAccount = parent.OwnerHash, parent.OwnerAccount
	if c.Ext != nil {
		c.Ext = &RoomExt{Blocks: c.Ext.Blocks}
	}
	c.Players = map[string]*Player{}
	parent.dims[c.Dim] = c
}

// dimRoom returns (creating if needed) a dimension of a root world.
func (s *Server) dimRoom(r *Room, dim string) *Room {
	root := r.root()
	if dim == "" || dim == "overworld" {
		return root
	}
	if c := root.dims[dim]; c != nil {
		return c
	}
	c := newRoom(root.ID+"."+dim, root.Name, root.Mode, root.Lock)
	c.Dim = dim
	switch dim {
	case "nether":
		c.W = max(128, root.width()/8/16*16)
	case "end":
		c.W = 1024
	}
	s.attachDim(root, c)
	if dim == "end" {
		s.prepareEnd(c)
	}
	return c
}

// broadcastWorld sends to every player in every dimension of a world.
func broadcastWorld(r *Room, v any, except *Peer) {
	root := r.root()
	broadcast(root, v, except)
	for _, d := range root.dims {
		broadcast(d, v, except)
	}
}

// ---------------------------------------------------------------------------
// Block mutation helpers used by world physics (no acting player).
// ---------------------------------------------------------------------------

const (
	setQuiet       = 1 // suppress client break/place effects
	setNoNeighbors = 2
)

// setBlock writes a block as the world itself (fluids, growth, explosions...).
func (s *Server) setBlock(r *Room, x, y, z, b int, flags int) bool {
	return s.setBlocks(r, [][4]int{{x, y, z, b}}, flags, "world_update")
}

// Validate every member before committing a multi-block physics transaction.
// In particular a veto at the landing cell cannot delete a falling block.
func (s *Server) setBlocks(r *Room, changes [][4]int, flags int, reason string) bool {
	return s.setBlocksWithCommit(r, changes, flags, reason, nil)
}

// The callback commits state belonging to the same validated transaction,
// before any block success event is delivered.
func (s *Server) setBlocksWithCommit(r *Room, changes [][4]int, flags int, reason string, commit func()) bool {
	return s.setBlocksWithContext(r, changes, flags, reason, commit, nil)
}

func (s *Server) setBlocksWithContext(r *Room, changes [][4]int, flags int, reason string, commit func(), context map[string]any) bool {
	return s.setBlocksValidated(r, changes, flags, reason, commit, context, nil)
}

// validate runs after every protection event and before the first write.
func (s *Server) setBlocksValidated(r *Room, changes [][4]int, flags int, reason string, commit func(), context map[string]any, validate func() bool) bool {
	// Include dependent rails, sensors and signs in the same protected batch.
	dependent := map[uint64]bool{}
	explicit := map[uint64]bool{}
	for _, at := range changes {
		explicit[blockKey(at[0], at[1], at[2])] = true
	}
	planned := append([][4]int(nil), changes...)
	for _, at := range changes {
		for _, child := range s.mechanicSupportChildren(r, blockKey(at[0], at[1], at[2]), at[3]) {
			k := blockKey(child[0], child[1], child[2])
			if !explicit[k] {
				planned = append(planned, child)
				explicit[k] = true
				dependent[k] = true
			}
		}
	}
	changes = planned
	nextDropID := r.NextDropID
	committed := false
	defer func() {
		if !committed {
			r.NextDropID = nextDropID
		}
	}()
	type change struct {
		at         [4]int
		key        uint64
		old        int
		data       map[string]any
		spill      bool
		drops      []*ItemDrop
		dropReason string
	}
	prepared := []change{}
	seen := map[uint64]bool{}
	for _, at := range changes {
		x, y, z, b := at[0], at[1], at[2], at[3]
		if !r.inside(x, z) || y < 0 || y >= 128 || !s.rules.validBlock(b) {
			return false
		}
		key := blockKey(x, y, z)
		if b != blkExplosiveTNT && reason != "tnt_explosion" && s.primedTNT(r, key) {
			return false
		}
		if seen[key] {
			return false
		}
		seen[key] = true
		if s.block(r, x, y, z) == b {
			continue
		}
		c := change{at: at, key: key, old: s.block(r, x, y, z), data: s.blockData(r, key, b)}
		if reason == "tnt_explosion" && s.primedTNT(r, key) {
			c.data = s.tntData(r, key, s.tntOrigin(r, key), r.Ext.Blocks[key].Fuse)
			c.data["oldBlock"] = blkExplosiveTNT
			c.data["block"] = b
		}
		if component := s.redstoneBlockChangeData(r, nil, key, b, reason); component != nil {
			c.data = component
		}
		if mechanic := s.mechanicBlockChangeData(r, nil, key, b, reason); mechanic != nil {
			for field, value := range mechanic {
				c.data[field] = value
			}
		}
		for field, value := range context {
			c.data[field] = value
		}
		if target, ok := context["target"]; ok {
			c.data["operationTarget"] = target
		}
		// A multi-cell effect keeps its source context, but each block event
		// identifies the actual cell being protected.
		c.data["x"], c.data["y"], c.data["z"] = x, y, z
		c.data["block"], c.data["oldBlock"] = b, c.old
		c.data["target"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
		c.data["reason"] = reason
		var stacks Stacks
		if dependent[key] {
			c.spill = true
			c.dropReason = "mechanic_support_lost"
			c.data["reason"] = "mechanic_support_lost"
			if s.gameRule(r, "doTileDrops") {
				stacks = s.dropsAt(r, key, c.old, 0)
			}
		}
		if container := r.Containers[key]; container != nil && !s.containerBlock(b) {
			c.spill = true
			c.dropReason = "container_spill"
			if s.keepsContents(c.old) {
				// explosionBlock already prepares the portable box drop.
				if reason != "explosion" && s.gameRule(r, "doTileDrops") {
					stacks = append(stacks, s.boxDrop(container))
				}
			} else {
				for _, v := range container.Slots {
					if v != nil {
						stacks = append(stacks, copyStack(v))
					}
				}
			}
			c.data["contents"] = cloneStacks(container.Slots)
			if reason == "explosion" {
				c.dropReason = "explosion"
				if !s.gameRule(r, "doTileDrops") {
					stacks = nil
				}
			}
		}
		if !s.allowed(api.BlockPhysicsChanging, r, nil, c.data) {
			return false
		}
		before := api.BlockPlacing
		if b == 0 {
			before = api.BlockBreaking
		}
		if !s.allowed(before, r, nil, c.data) {
			return false
		}
		if _, ok := c.data["componentAfter"]; ok && !s.allowed(api.RedstoneComponentChanging, r, nil, c.data) {
			return false
		}
		if !s.mechanicBlockBefore(r, nil, c.data) {
			return false
		}
		if old := mechanicSnapshot(r, key); old != nil && old.Record != nil && b != c.old {
			c.spill = true
			c.dropReason = "mechanic_break"
			if reason == "explosion" {
				c.dropReason = "explosion"
			}
			if s.gameRule(r, "doTileDrops") {
				stacks = append(stacks, copyStack(old.Record))
			}
			c.data["record"] = itemSnapshot(old.Record)
		}
		if len(stacks) > 0 {
			var err error
			if reason == "explosion" {
				dropContext := map[string]any{}
				for field, value := range context {
					if field != "stack" && field != "before" && field != "after" {
						dropContext[field] = value
					}
				}
				c.drops, err = s.prepareDropsWithContext(r, nil, stacks, float64(x)+.5, float64(y)+.5, float64(z)+.5, c.dropReason, dropContext, true)
			} else {
				c.drops, err = s.prepareDropsWithContext(r, nil, stacks, float64(x)+.5, float64(y)+.5, float64(z)+.5, c.dropReason, context, true)
			}
			if err != nil {
				return false
			}
		}
		prepared = append(prepared, c)
	}
	if len(prepared) == 0 {
		return false
	}
	if validate != nil && !validate() {
		return false
	}
	for _, c := range prepared {
		r.Edits[c.key] = c.at[3]
		s.clearInactiveFire(r, c.key, c.at[3])
		s.trackRedstoneBlock(r, c.key, c.at[3])
		s.trackMechanicBlock(r, c.key, c.at[3])
		delete(r.Authors, c.key)
		s.queueLeafChange(r, c.key, c.old, c.at[3])
		if c.spill {
			delete(r.Containers, c.key)
			for _, d := range c.drops {
				r.Drops[d.ID] = d
			}
		}
	}
	r.Dirty = true
	r.rt().redstoneDirty = true
	committed = true
	if commit != nil {
		commit()
	}
	for _, c := range prepared {
		x, y, z, b := c.at[0], c.at[1], c.at[2], c.at[3]
		if r.usesSurvival() {
			s.broadcastChunkFlags(r, c.key, b, flags&setQuiet != 0)
		} else {
			for p := range r.Peers {
				sendBlockSync(p, c.key, b)
			}
		}
		if c.spill {
			for p := range r.Peers {
				if p.window != nil && p.window.Key == c.key {
					s.closeWindow(p)
					p.json(map[string]any{"t": "wclose", "w": p.nextWindow})
				}
			}
			if reason == "explosion" {
				dropContext := map[string]any{}
				for field, value := range context {
					if field != "stack" && field != "before" && field != "after" {
						dropContext[field] = value
					}
				}
				s.publishDropsWithContext(r, nil, c.drops, c.dropReason, dropContext)
			} else {
				s.publishDrops(r, nil, c.drops, c.dropReason)
			}
		}
		if flags&setNoNeighbors == 0 {
			s.queueNeighbors(r, x, y, z)
		}
		if _, ok := c.data["mechanicAfter"]; ok {
			s.broadcastMechanic(r, c.key)
		}
		if _, ok := c.data["railAfter"]; ok {
			s.broadcastRail(r, c.key)
		}
		s.blockEvents(r, nil, c.data)
		s.emit(api.BlockPhysicsChanged, r, nil, c.data)
	}
	return true
}

func (s *Server) broadcastChunkFlags(r *Room, key uint64, b int, quiet bool) {
	x, y, z := coords(key)
	if r.Infinite {
		s.broadcastInfiniteDelta(r, x, y, z, b, quiet)
		return
	}
	ci := z/16*r.cn() + x/16
	idx := ((z&15)*16+(x&15))*128 + y
	flag := byte(0)
	if quiet {
		flag = 128
	}
	for p := range r.Peers {
		if p.known[ci] {
			out := binary.LittleEndian.AppendUint32([]byte{chunkFrame(p), r.dimIndex(), 1, 0}, uint32(ci))
			out = binary.LittleEndian.AppendUint16(out, 1)
			p.enqueue(websocket.BinaryMessage, appendChunkRecord(out, p, idx, b, flag))
		}
	}
}

func (s *Server) queue(r *Room, x, y, z int) {
	if !r.inside(x, z) || y < 0 || y >= 128 {
		return
	}
	rt := r.rt()
	k := blockKey(x, y, z)
	if rt.queued[k] || len(rt.pending) > 20000 {
		return
	}
	rt.queued[k] = true
	rt.pending = append(rt.pending, k)
}
func (s *Server) queueNeighbors(r *Room, x, y, z int) {
	s.queue(r, x, y, z)
	for _, d := range faceOffsets {
		s.queue(r, x+d[0], y+d[1], z+d[2])
	}
}

// schedule asks for an update of a position after n ticks (100ms each).
func (s *Server) schedule(r *Room, x, y, z, n int) {
	if !r.inside(x, z) || y < 0 || y >= 128 {
		return
	}
	k := blockKey(x, y, z)
	rt := r.rt()
	if old, ok := rt.scheduled[k]; !ok || n < old {
		rt.scheduled[k] = n
	}
}

// faceOffsets follow the client face order: down, up, north, south, west, east.
var faceOffsets = [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// ---------------------------------------------------------------------------
// Hook registry. Feature files register in init(); core files call the run*
// helpers at well-defined points.
// ---------------------------------------------------------------------------

type messageHook func(s *Server, p *Peer, m Message) bool
type placeHook func(s *Server, p *Peer, m Message, target int) bool
type blockUpdateHook func(s *Server, r *Room, x, y, z, b int)
type roomTickHook func(s *Server, r *Room)
type peerTickHook func(s *Server, p *Peer)
type breakHook func(s *Server, p *Peer, x, y, z, old int)
type digStartHook func(s *Server, p *Peer, m Message) bool

var (
	messageHooks     = map[string][]messageHook{}
	placeHooks       []namedPlaceHook
	blockUpdateHooks []blockUpdateHook
	roomTickHooks    []roomTickHook
	peerTickHooks    []peerTickHook // every 100ms per survival player
	breakHooks       []breakHook
	digStartHooks    []digStartHook
)

type namedPlaceHook struct {
	order int
	fn    placeHook
}

func onMessage(t string, fn messageHook) { messageHooks[t] = append(messageHooks[t], fn) }
func onPlace(order int, fn placeHook) {
	placeHooks = append(placeHooks, namedPlaceHook{order, fn})
	for i := len(placeHooks) - 1; i > 0 && placeHooks[i].order < placeHooks[i-1].order; i-- {
		placeHooks[i], placeHooks[i-1] = placeHooks[i-1], placeHooks[i]
	}
}
func onBlockUpdate(fn blockUpdateHook) { blockUpdateHooks = append(blockUpdateHooks, fn) }
func onRoomTick(fn roomTickHook)       { roomTickHooks = append(roomTickHooks, fn) }
func onPeerTick(fn peerTickHook)       { peerTickHooks = append(peerTickHooks, fn) }
func onBreak(fn breakHook)             { breakHooks = append(breakHooks, fn) }
func onDigStart(fn digStartHook)       { digStartHooks = append(digStartHooks, fn) }

func (s *Server) runMessageHooks(p *Peer, m Message) bool {
	for _, h := range messageHooks[m.T] {
		if h(s, p, m) {
			return true
		}
	}
	return false
}
func (s *Server) runPlaceHooks(p *Peer, m Message, target int) bool {
	for _, h := range placeHooks {
		if h.fn(s, p, m, target) {
			return true
		}
	}
	return false
}
func (s *Server) runBreakHooks(p *Peer, x, y, z, old int) {
	for _, h := range breakHooks {
		h(s, p, x, y, z, old)
	}
	s.queueNeighbors(p.room, x, y, z)
}

// extTick runs once per server tick (100ms) for every room with players.
func (s *Server) extTick(r *Room) {
	rt := r.rt()
	rt.tickCount++
	if r.usesSurvival() {
		for p := range r.Peers {
			if p.state != nil && !p.state.Dead {
				for _, h := range peerTickHooks {
					h(s, p)
				}
			}
		}
	}
	for k, n := range rt.scheduled {
		if n <= 1 {
			delete(rt.scheduled, k)
			x, y, z := coords(k)
			s.queue(r, x, y, z)
		} else {
			rt.scheduled[k] = n - 1
		}
	}
	// Process block updates (bounded per tick to keep the server responsive).
	budget := 4000
	for budget > 0 && len(rt.pending) > 0 {
		k := rt.pending[0]
		rt.pending = rt.pending[1:]
		delete(rt.queued, k)
		budget--
		x, y, z := coords(k)
		b := s.block(r, x, y, z)
		for _, h := range blockUpdateHooks {
			h(s, r, x, y, z, b)
		}
	}
	for _, h := range roomTickHooks {
		h(s, r)
	}
}

// nameOf returns the base block name.
func (s *Server) nameOf(b int) string {
	if !s.rules.validBlock(b) {
		return ""
	}
	return baseName(s.rules.Names[b])
}
func (s *Server) fullName(b int) string {
	if !s.rules.validBlock(b) {
		return ""
	}
	return s.rules.Names[b]
}
func (s *Server) kind(b int) int {
	if b < 0 || b >= len(s.rules.Kinds) {
		return 0
	}
	return s.rules.Kinds[b]
}
func (s *Server) id(name string) int { return s.rules.byName[name] }

// soundAt sends a positional sound to players of a room (optionally except one).
func soundAt(r *Room, e string, x, y, z float64, except *Peer) {
	for p := range r.Peers {
		if p == except || p.state == nil {
			continue
		}
		if math.Abs(p.state.X-x) > 48 || math.Abs(p.state.Z-z) > 48 {
			continue
		}
		p.json(map[string]any{"t": "snd", "e": e, "x": x, "y": y, "z": z})
	}
}

func (p *Peer) ext() *PlayerExt {
	if p.state.Ext == nil {
		p.state.Ext = &PlayerExt{}
	}
	return p.state.Ext
}

var _ = time.Now
