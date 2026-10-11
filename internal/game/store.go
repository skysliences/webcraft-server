package game

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultRoom = "ti9bwes4ryguhkra"

var roomRE = regexp.MustCompile(`^[a-z0-9]{16}$`)
var sidRE = regexp.MustCompile(`^[A-Za-z0-9]{12,40}$`)

type Inventory struct {
	Slots    Stacks `json:"s"`
	Selected int    `json:"sel"`
}
type Player struct {
	Mount      uint32     `json:"mount,omitempty"`
	X          float64    `json:"x"`
	Y          float64    `json:"y"`
	Z          float64    `json:"z"`
	Yaw        float64    `json:"yaw"`
	Pitch      float64    `json:"pitch"`
	Fly        int        `json:"fly"`
	Hotbar     []int      `json:"hotbar,omitempty"`
	Slot       int        `json:"slot"`
	Inv        *Inventory `json:"inv,omitempty"`
	Health     float64    `json:"health"`
	Food       int        `json:"food"`
	Saturation float64    `json:"saturation"`
	Exhaustion float64    `json:"exhaustion"`
	Dead       bool       `json:"dead"`
	Cursor     *Stack     `json:"cursor,omitempty"`
	Recovery   Stacks     `json:"recovery,omitempty"`
	// Ext holds survival extensions (xp, effects, spawn point, dimension...).
	Ext *PlayerExt `json:"ext,omitempty"`
	// GM/EndCredits mirror Ext for the client's st object (gm=creative flag).
	GM         bool `json:"gm,omitempty"`
	EndCredits bool `json:"endCredits,omitempty"`
}
type Container struct {
	Item      *Stack `json:"item,omitempty"` // Placed portable container metadata.
	Kind      string `json:"kind"`
	Slots     Stacks `json:"slots"`
	Burn      int    `json:"burn"`
	BurnTotal int    `json:"burnTotal"`
	Progress  int    `json:"progress"`
}
type EditAuthor struct {
	Token  string `json:"token"`
	Name   string `json:"name"`
	Before int    `json:"before"`
	After  int    `json:"after"`
}
type Room struct {
	ID                string
	Name              string
	Mode              string
	Lock              int
	OwnerHash         string
	OwnerAccount      string
	CreatedAt         int64
	Active            int64
	Time              uint64
	Edits             map[uint64]int
	Players           map[string]*Player
	Containers        map[uint64]*Container
	Authors           map[uint64]EditAuthor
	Peers             map[*Peer]bool
	Bans              map[string]time.Time
	Dirty             bool
	SaveError         string
	Drops             map[uint32]*ItemDrop
	NextDropID        uint32
	Mobs              map[uint32]*Mob
	NextMobID         uint32
	saveVersion       int
	transportMigrated bool
	// W is the world width in blocks (multiple of 16, <= 4096). Dim is
	// "overworld", "nether" or "end"; non-overworld rooms are sub-rooms of
	// parent and saved as <id>.<dim>.json.
	Infinite   bool
	LegacyW    int
	Generator  string
	chunks     map[ChunkCoord]*WorldChunk
	W          int
	Dim        string
	Ext        *RoomExt
	parent     *Room
	dims       map[string]*Room
	extRT      *roomRuntime
	scoreboard *Scoreboard
}
type diskRoom struct {
	Infinite     bool                  `json:"infinite,omitempty"`
	LegacyW      int                   `json:"legacyW,omitempty"`
	Generator    string                `json:"generator,omitempty"`
	Version      int                   `json:"version"`
	Name         string                `json:"name"`
	Mode         string                `json:"mode"`
	Lock         int                   `json:"lock"`
	OwnerHash    string                `json:"ownerHash,omitempty"`
	OwnerAccount string                `json:"ownerAccount,omitempty"`
	CreatedAt    int64                 `json:"createdAt"`
	Active       int64                 `json:"active"`
	SavedAt      string                `json:"savedAt"`
	Time         uint64                `json:"time"`
	Edits        [][2]int              `json:"edits"`
	Players      map[string]*Player    `json:"players"`
	Containers   map[uint64]*Container `json:"containers,omitempty"`
	Authors      map[uint64]EditAuthor `json:"authors,omitempty"`
	Drops        []*ItemDrop           `json:"drops,omitempty"`
	NextDropID   uint32                `json:"nextDropId,omitempty"`
	Mobs         []*Mob                `json:"mobs,omitempty"`
	NextMobID    uint32                `json:"nextMobId,omitempty"`
	W            int                   `json:"w,omitempty"`
	Dim          string                `json:"dim,omitempty"`
	Ext          *RoomExt              `json:"ext,omitempty"`
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hashKey(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
func owns(r *Room, key string) bool {
	return key != "" && r.OwnerHash != "" && subtle.ConstantTimeCompare([]byte(hashKey(key)), []byte(r.OwnerHash)) == 1
}
func newRoom(id, name, mode string, lock int) *Room {
	now := time.Now().UnixMilli()
	r := &Room{ID: id, Name: name, Mode: mode, Lock: lock, CreatedAt: now, Active: now, Time: 6000, Edits: map[uint64]int{}, Players: map[string]*Player{}, Containers: map[uint64]*Container{}, Authors: map[uint64]EditAuthor{}, Peers: map[*Peer]bool{}, Bans: map[string]time.Time{}, Dirty: true, Drops: map[uint32]*ItemDrop{}, NextDropID: 1, Mobs: map[uint32]*Mob{}, NextMobID: 1, saveVersion: saveVersionCurrent, W: 1024, Dim: "overworld"}
	r.scoreboard = newScoreboard(r)
	return r
}
func (s *Server) roomFile(id string) string {
	return filepath.Join(s.config.DataDir, "rooms", id+".json")
}
func (s *Server) loadRooms() error {
	dir := filepath.Join(s.config.DataDir, "rooms")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if s.config.SingleWorld {
		if err = s.prepareWorldBinding(entries); err != nil {
			return err
		}
	}
	children := []*Room{}
	for _, entry := range entries {
		id := entry.Name()
		fileID, dim := id, ""
		if len(id) > 22 && roomRE.MatchString(id[:16]) && id[16] == '.' && filepath.Ext(id) == ".json" {
			dim = strings.TrimSuffix(id[17:], ".json")
			if dim != "nether" && dim != "end" {
				continue
			}
			fileID = id[:16] + "." + dim
		} else if len(id) != 21 || !roomRE.MatchString(id[:16]) || filepath.Ext(id) != ".json" {
			continue
		} else {
			fileID = id[:16]
		}
		id = fileID
		// Archived worlds are not parsed, migrated, or exposed by this server.
		if s.config.SingleWorld && id[:16] != s.worldBinding.WorldID {
			continue
		}
		data, err := os.ReadFile(s.roomFile(id))
		if err != nil {
			return err
		}
		var raw diskRoom
		if err = json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("存档 %s 损坏，已停止加载以保护数据: %w", id, err)
		}
		if raw.Version > saveVersionCurrent {
			return fmt.Errorf("unsupported save version %d", raw.Version)
		}
		if raw.Ext != nil {
			for _, d := range raw.Ext.Blocks {
				if d != nil && (!validRedstoneComponent(d.Redstone) || !s.validMechanic(d.Mechanic) || !validRail(d.Rail) || !s.validTNTState(d.TNT) || !s.validTNTState(d.Fire) || d.TNT != nil && (d.Fuse < 0 || d.Fuse > maxTNTFuse)) {
					return fmt.Errorf("invalid redstone state in %s; save was not modified", id)
				}
			}
		}
		mode := raw.Mode
		if mode == "" {
			mode = "creative"
		}
		if mode != "creative" && mode != "survival" {
			return fmt.Errorf("invalid mode in %s", id)
		}
		r := newRoom(id, raw.Name, mode, raw.Lock)
		r.saveVersion = max(1, raw.Version)
		r.OwnerHash = raw.OwnerHash
		r.OwnerAccount = raw.OwnerAccount
		if r.Lock != 0 && r.Lock != 2 {
			r.Lock = 2
		}
		if raw.CreatedAt > 0 {
			r.CreatedAt = raw.CreatedAt
		}
		if raw.Active > 0 {
			r.Active = raw.Active
		}
		if raw.Version >= 2 {
			r.Time = raw.Time
		}
		for _, v := range raw.Edits {
			if v[1] >= 220 && !s.rules.validBlock(v[1]) {
				return fmt.Errorf("world %s contains unregistered block %d; restore registry.json before loading; save was not modified", id, v[1])
			}
			if v[0] >= 0 && v[0] < 1<<60 && s.rules.validBlock(v[1]) {
				r.Edits[uint64(v[0])] = v[1]
			}
		}
		for sid, p := range raw.Players {
			if sidRE.MatchString(sid) && p != nil {
				if p.Inv != nil {
					for _, v := range p.Inv.Slots {
						if v != nil && !validMeta(v.Meta) {
							return fmt.Errorf("invalid item metadata in %s; save was not modified", id)
						}
					}
				}
				for _, v := range append(append(Stacks{}, p.Recovery...), p.Cursor) {
					if v != nil && !validMeta(v.Meta) {
						return fmt.Errorf("invalid item metadata in %s; save was not modified", id)
					}
				}
				if p.Inv != nil {
					s.sanitizeInventory(p.Inv)
				}
				r.Players[sid] = p
			}
		}
		if raw.Containers != nil {
			for _, c := range raw.Containers {
				if c != nil {
					if c.Item != nil && (!s.validStack(c.Item, 1) || c.Item.ID != itemShulkerBox && c.Item.ID != blkShulkerBox) {
						return fmt.Errorf("invalid portable container metadata in %s; save was not modified", id)
					}
					for _, v := range c.Slots {
						if v != nil && !validMeta(v.Meta) {
							return fmt.Errorf("invalid container item metadata in %s; save was not modified", id)
						}
					}
				}
			}
			r.Containers = raw.Containers
		}
		if raw.Authors != nil {
			r.Authors = raw.Authors
		}
		if len(raw.Drops) > maxWorldDrops {
			return fmt.Errorf("too many item drops in %s", id)
		}
		r.NextDropID = raw.NextDropID
		for _, d := range raw.Drops {
			if !s.validDrop(d) || r.Drops[d.ID] != nil {
				return fmt.Errorf("invalid or duplicate item drop in %s; save was not modified", id)
			}
			r.Drops[d.ID] = d
			if d.ID >= r.NextDropID {
				r.NextDropID = d.ID + 1
			}
		}
		if r.NextDropID == 0 {
			r.NextDropID = 1
		}
		if err := s.migrateLegacyTransport(r, &raw); err != nil {
			return err
		}
		if len(raw.Mobs) > maxWorldMobs {
			return fmt.Errorf("too many mobs in %s", id)
		}
		for _, m := range raw.Mobs {
			if !validMob(m) || r.Mobs[m.ID] != nil || r.Drops[m.ID] != nil {
				return fmt.Errorf("invalid or duplicate mob in %s; save was not modified", id)
			}
			r.Mobs[m.ID] = m
			r.NextMobID = max(r.NextMobID, (m.ID&0x7fffffff)+1)
		}
		r.NextMobID = max(r.NextMobID, raw.NextMobID)
		s.afterLoadRoom(r, &raw, dim)
		r.Dirty = raw.Version < saveVersionCurrent || raw.Infinite != r.Infinite || r.transportMigrated
		if err := s.restoreVehicles(r); err != nil {
			return err
		}
		s.restoreLoadedRedstone(r)
		s.restoreMechanics(r)
		s.restoreLeafDecay(r)
		if dim != "" {
			children = append(children, r)
			continue
		}
		s.rooms[id] = r
	}
	for _, c := range children {
		parent := s.rooms[c.ID[:16]]
		if parent == nil {
			continue
		}
		s.attachDim(parent, c)
	}
	if s.config.SingleWorld {
		if s.rooms[s.worldBinding.WorldID] == nil {
			if !s.worldBindingNew {
				return fmt.Errorf("主世界 %s 存档不存在，已停止启动以保护进度", s.worldBinding.WorldID)
			}
			s.rooms[s.worldBinding.WorldID] = s.newServerWorld()
		}
	} else if s.rooms[DefaultRoom] == nil {
		s.rooms[DefaultRoom] = newRoom(DefaultRoom, "本地创造世界", "creative", 0)
		if s.config.InfiniteWorlds {
			r := s.rooms[DefaultRoom]
			r.Infinite = true
			r.LegacyW = 1024
			r.Generator = generatorVersion
		}
	}
	return nil
}
func (s *Server) saveRoom(r *Room) error {
	if !r.Dirty {
		return nil
	}
	if r.transportMigrated {
		if err := backupBeforeMigration(s.roomFile(r.ID), "vehicles", r.saveVersion); err != nil {
			return err
		}
	}
	s.persistRedstone(r)
	if r.Infinite {
		if err := s.saveChunks(r); err != nil {
			r.SaveError = err.Error()
			s.emit(api.WorldSaveFailed, r, nil, map[string]any{"error": err.Error()})
			return err
		}
	}
	s.emit(api.WorldSaving, r, nil, map[string]any{"blocks": len(r.Edits), "players": len(r.Players)})
	edits := make([][2]int, 0, len(r.Edits))
	for k, b := range r.Edits {
		edits = append(edits, [2]int{int(k), b})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i][0] < edits[j][0] })
	drops := make([]*ItemDrop, 0, len(r.Drops))
	for _, id := range sortedDropIDs(r) {
		drops = append(drops, r.Drops[id])
	}
	mobs := make([]*Mob, 0, len(r.Mobs))
	for _, id := range sortedMobIDs(r) {
		mobs = append(mobs, r.Mobs[id])
	}
	raw := diskRoom{Infinite: r.Infinite, LegacyW: r.LegacyW, Generator: r.Generator, Version: saveVersionCurrent, W: r.W, Dim: r.Dim, Ext: r.Ext, Mobs: mobs, NextMobID: r.NextMobID, OwnerAccount: r.OwnerAccount, Drops: drops, NextDropID: r.NextDropID, Name: r.Name, Mode: r.Mode, Lock: r.Lock, OwnerHash: r.OwnerHash, CreatedAt: r.CreatedAt, Active: r.Active, SavedAt: time.Now().UTC().Format(time.RFC3339), Time: r.Time, Edits: edits, Players: r.Players, Containers: r.Containers, Authors: r.Authors}
	if r.Infinite {
		raw.Edits = nil
		raw.Containers = nil
		raw.Authors = nil
		if raw.Ext != nil {
			e := *raw.Ext
			e.Blocks = nil
			raw.Ext = &e
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	file := s.roomFile(r.ID)
	if r.saveVersion < 3 {
		if err := backupBeforeDrops(file, r.saveVersion); err != nil {
			r.SaveError = err.Error()
			s.emit(api.WorldSaveFailed, r, nil, map[string]any{"error": err.Error()})
			return err
		}
	}
	if r.saveVersion < 4 {
		if err := backupBeforeMigration(file, "accounts", r.saveVersion); err != nil {
			r.SaveError = err.Error()
			s.emit(api.WorldSaveFailed, r, nil, map[string]any{"error": err.Error()})
			return err
		}
	}
	if r.saveVersion < 5 {
		if err := backupBeforeMigration(file, "mobs", r.saveVersion); err != nil {
			r.SaveError = err.Error()
			return err
		}
	}
	if r.saveVersion < 6 {
		if err := backupBeforeMigration(file, "item-meta", r.saveVersion); err != nil {
			r.SaveError = err.Error()
			return err
		}
	}
	if r.saveVersion < 7 {
		if err := backupBeforeMigration(file, "wide-keys", r.saveVersion); err != nil {
			r.SaveError = err.Error()
			return err
		}
	}
	if r.saveVersion < 8 {
		if err := backupBeforeMigration(file, "chunks", r.saveVersion); err != nil {
			return err
		}
	}
	tmp := file + ".tmp"
	// Flush a complete temporary file before atomically replacing the last save.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err == nil {
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		r.SaveError = err.Error()
		s.emit(api.WorldSaveFailed, r, nil, map[string]any{"error": err.Error()})
		return err
	}
	r.Dirty = false
	r.SaveError = ""
	r.saveVersion = saveVersionCurrent
	r.transportMigrated = false
	s.emit(api.WorldSaved, r, nil, map[string]any{"blocks": len(r.Edits), "players": len(r.Players)})
	return nil
}
func (r *Room) info() map[string]any {
	root := r.root()
	beaten := root.Ext != nil && root.Ext.Beaten
	return map[string]any{"id": root.ID, "name": root.Name, "mode": root.Mode, "infinite": root.Infinite, "generator": root.Generator, "loadedChunks": len(r.chunks), "pending": false, "lock": root.Lock, "hardcore": root.hardcore(), "beaten": beaten, "host": nil, "online": root.online(), "blocks": len(root.Edits), "active": root.Active, "created": root.CreatedAt, "pfp": nil}
}

// Block keys: x (12 bits) << 19 | z (12 bits) << 7 | y (7 bits). Saves before
// version 7 and the creative wire protocol use the 10-bit layout x<<17|z<<7|y.
func blockKey(x, y, z int) uint64 {
	return uint64(x&0x3ffffff)<<33 | uint64(z&0x3ffffff)<<7 | uint64(y&127)
}
func coords(k uint64) (int, int, int) {
	x, z := int(k>>33&0x3ffffff), int(k>>7&0x3ffffff)
	if x&0x2000000 != 0 {
		x -= 0x4000000
	}
	if z&0x2000000 != 0 {
		z -= 0x4000000
	}
	return x, int(k & 127), z
}

// Keep the last pre-migration save for a rollback to a server without entities.
func backupBeforeDrops(file string, version int) error {
	return backupBeforeMigration(file, "drops", version)
}
func backupBeforeMigration(file, feature string, version int) error {
	name := fmt.Sprintf("%s.pre-%s-v%d.bak", file, feature, version)
	if _, err := os.Stat(name); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		os.Remove(name)
	}
	return writeErr
}
