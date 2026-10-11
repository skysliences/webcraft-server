// Package plugin is the public Mcraft plugin SDK and wire protocol (API v1).
package plugin

import (
	"encoding/json"
	"strings"
)

const APIVersion = 1

type Priority int

const (
	Lowest  Priority = -100
	Low     Priority = -50
	Normal  Priority = 0
	High    Priority = 50
	Highest Priority = 100
	Monitor Priority = 200
)

type Subscription struct {
	Event           string   `json:"event"`
	Priority        Priority `json:"priority"`
	IgnoreCancelled bool     `json:"ignoreCancelled,omitempty"`
	Every           int      `json:"every,omitempty"`
}
type Command struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
	Usage       string   `json:"usage,omitempty"`
	Permission  string   `json:"permission,omitempty"`
}
type Metadata struct {
	Blocks        []BlockDefinition `json:"blocks,omitempty"`
	Items         []ItemDefinition  `json:"items,omitempty"`
	APIVersion    int               `json:"apiVersion"`
	Subscriptions []Subscription    `json:"subscriptions"`
	Commands      []Command         `json:"commands"`
}
type Player struct {
	Operator    bool                      `json:"operator,omitempty"`
	AccountID   string                    `json:"accountId,omitempty"`
	GameMode    string                    `json:"gamemode,omitempty"`
	XPLevel     int                       `json:"xpLevel,omitempty"`
	XPPoints    int                       `json:"xpPoints,omitempty"`
	XPTotal     int                       `json:"xpTotal,omitempty"`
	Effects     map[string]EffectSnapshot `json:"effects,omitempty"`
	ID          uint16                    `json:"id"`
	UID         string                    `json:"uid"`
	Name        string                    `json:"name"`
	Owner       bool                      `json:"owner"`
	X           float64                   `json:"x"`
	Y           float64                   `json:"y"`
	Z           float64                   `json:"z"`
	Health      float64                   `json:"health"`
	Food        int                       `json:"food"`
	Dead        bool                      `json:"dead"`
	Selected    int                       `json:"selected"`
	Mount       uint32                    `json:"mount,omitempty"`
	Inventory   [][]int                   `json:"inventory,omitempty"`
	Cursor      []int                     `json:"cursor,omitempty"`
	Items       []*ItemStack              `json:"items,omitempty"`
	CursorItem  *ItemStack                `json:"cursorItem,omitempty"`
	MaxHealth   float64                   `json:"maxHealth,omitempty"`
	Permissions []string                  `json:"permissions,omitempty"`
	Roles       []string                  `json:"roles,omitempty"`
}
type EffectSnapshot struct {
	Ticks     int `json:"ticks"`
	Amplifier int `json:"amplifier"`
}
type World struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Lock      int    `json:"lock"`
	Online    int    `json:"online"`
	RootID    string `json:"rootId,omitempty"`
	Dimension string `json:"dimension,omitempty"`
	Infinite  bool   `json:"infinite,omitempty"`
}
type Event struct {
	Identity    *Identity      `json:"identity,omitempty"`
	ID          uint64         `json:"id"`
	Name        string         `json:"name"`
	Time        string         `json:"time"`
	Tick        uint64         `json:"tick"`
	Source      string         `json:"source,omitempty"`
	Cancellable bool           `json:"cancellable"`
	Cancelled   bool           `json:"cancelled"`
	Reason      string         `json:"reason,omitempty"`
	World       *World         `json:"world,omitempty"`
	Player      *Player        `json:"player,omitempty"`
	Data        map[string]any `json:"data"`
	Damage      *DamageContext `json:"damage,omitempty"`
}

func (e *Event) Cancel(reason string) {
	if e.Cancellable {
		e.Cancelled = true
		e.Reason = reason
	}
}
func (e *Event) String(key string) string { v, _ := e.Data[key].(string); return v }
func (e *Event) Bool(key string) bool {
	switch v := e.Data[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return false
}
func (e *Event) Number(key string) float64 {
	switch v := e.Data[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case uint64:
		return float64(v)
	}
	return 0
}

type Action struct {
	BlockKey          string              `json:"blockKey,omitempty"`
	AccountID         string              `json:"accountId,omitempty"`
	Type              string              `json:"type"`
	WorldID           string              `json:"worldId,omitempty"`
	PlayerID          uint16              `json:"playerId,omitempty"`
	Message           string              `json:"message,omitempty"`
	ItemID            int                 `json:"itemId,omitempty"`
	ItemName          string              `json:"itemName,omitempty"`
	Count             int                 `json:"count,omitempty"`
	Block             int                 `json:"block,omitempty"`
	X                 float64             `json:"x,omitempty"`
	Y                 float64             `json:"y,omitempty"`
	Z                 float64             `json:"z,omitempty"`
	Amount            float64             `json:"amount,omitempty"`
	Reason            string              `json:"reason,omitempty"`
	EntityType        string              `json:"entityType,omitempty"`
	Persistent        bool                `json:"persistent,omitempty"`
	Ticks             uint64              `json:"ticks,omitempty"`
	FuseTicks         int                 `json:"fuseTicks,omitempty"`
	Slot              int                 `json:"slot,omitempty"`
	Face              int                 `json:"face,omitempty"`
	Stack             *ItemStack          `json:"stack,omitempty"`
	Expected          *ItemStack          `json:"expected,omitempty"`
	ContainerExpected []*ItemStack        `json:"containerExpected,omitempty"`
	EntityID          uint32              `json:"entityId,omitempty"`
	Rule              string              `json:"rule,omitempty"`
	Value             bool                `json:"value,omitempty"`
	Difficulty        string              `json:"difficulty,omitempty"`
	GameMode          string              `json:"gamemode,omitempty"`
	Effect            string              `json:"effect,omitempty"`
	Duration          int                 `json:"duration,omitempty"`
	Amplifier         int                 `json:"amplifier,omitempty"`
	Rain              bool                `json:"rain,omitempty"`
	Thunder           bool                `json:"thunder,omitempty"`
	Lines             []string            `json:"lines,omitempty"`
	Blocks            []BlockChange       `json:"blocks,omitempty"`
	CustomUI          *CustomUIDefinition `json:"customUi,omitempty"`
}

// CustomUIDefinition 插件向客户端推送的自定义界面表单与弹窗定义
type CustomUIDefinition struct {
	ID      string           `json:"id"`                // 界面唯一标识
	Title   string           `json:"title"`             // 窗口标题
	Content string           `json:"content,omitempty"` // 正文提示描述
	Style   string           `json:"style,omitempty"`   // 风格: "wood" (原木风格), "stone", "dark"
	Inputs  []CustomUIInput  `json:"inputs,omitempty"`  // 输入组件列表
	Buttons []CustomUIButton `json:"buttons,omitempty"` // 按钮列表
	Data    map[string]any   `json:"data,omitempty"`    // 插件私有上下文数据
}

// CustomUIInput 界面表单输入项
type CustomUIInput struct {
	ID          string `json:"id"`                    // 字段 key
	Type        string `json:"type,omitempty"`        // "text", "password", "number"
	Label       string `json:"label,omitempty"`       // 标签名称
	Placeholder string `json:"placeholder,omitempty"` // 占位提示
	Default     string `json:"default,omitempty"`     // 默认值
	MaxLength   int    `json:"maxLength,omitempty"`   // 最大字符长度
}

// CustomUIButton 界面交互操作按钮
type CustomUIButton struct {
	ID   string `json:"id"`             // 按钮 ID
	Text string `json:"text"`           // 按钮显示文本
	Type string `json:"type,omitempty"` // "submit" (主要动作), "cancel" (取消关闭)
}
type BlockChange struct {
	X     int `json:"x"`
	Y     int `json:"y"`
	Z     int `json:"z"`
	Block int `json:"block"`
}
type BlockSnapshot struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Z     int    `json:"z"`
	Block int    `json:"block"`
	Name  string `json:"name,omitempty"`
}
type WorldSnapshot struct {
	WorldID   string          `json:"worldId"`
	Dimension string          `json:"dimension,omitempty"`
	Time      uint64          `json:"time"`
	Count     int             `json:"count"`
	Blocks    []BlockSnapshot `json:"blocks,omitempty"`
}
type Initialize struct {
	APIVersion int             `json:"apiVersion"`
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	DataDir    string          `json:"dataDir"`
	Config     json.RawMessage `json:"config"`
	TickMillis int             `json:"tickMillis"`
}
type Request struct {
	ID         uint64      `json:"id"`
	Method     string      `json:"method"`
	Initialize *Initialize `json:"initialize,omitempty"`
	Event      *Event      `json:"event,omitempty"`
	Priority   Priority    `json:"priority,omitempty"`
	Command    string      `json:"command,omitempty"`
	Args       []string    `json:"args,omitempty"`
}
type Response struct {
	ID        uint64         `json:"id"`
	Error     string         `json:"error,omitempty"`
	Metadata  *Metadata      `json:"metadata,omitempty"`
	Cancelled bool           `json:"cancelled,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Actions   []Action       `json:"actions,omitempty"`
}

const (
	ServerStarted             = "server.started"
	ServerWorldSelecting      = "server.world.select.before"
	ServerWorldSelected       = "server.world.selected"
	ServerStopping            = "server.stopping"
	ServerTick                = "server.tick"
	WorldLoaded               = "world.loaded"
	WorldCreating             = "world.create.before"
	WorldCreated              = "world.created"
	WorldSaving               = "world.save.before"
	WorldSaved                = "world.saved"
	WorldSaveFailed           = "world.save.failed"
	WorldRenaming             = "world.rename.before"
	WorldRenamed              = "world.renamed"
	WorldLockChanging         = "world.lock.before"
	WorldLockChanged          = "world.lock.changed"
	WorldResetting            = "world.reset.before"
	WorldReset                = "world.reset"
	WorldUndoing              = "world.undo.before"
	WorldUndone               = "world.undone"
	PlayerJoining             = "player.join.before"
	PlayerJoined              = "player.joined"
	PlayerQuit                = "player.quit"
	PlayerMoving              = "player.move.before"
	PlayerMoved               = "player.moved"
	PlayerChatting            = "player.chat.before"
	PlayerChat                = "player.chat"
	CommandExecuting          = "command.before"
	CommandExecuted           = "command.executed"
	PlayerKicking             = "player.kick.before"
	PlayerKicked              = "player.kicked"
	PlayerTeleporting         = "player.teleport.before"
	PlayerTeleported          = "player.teleported"
	BlockBreaking             = "block.break.before"
	BlockBroken               = "block.broken"
	BlockPlacing              = "block.place.before"
	BlockPlaced               = "block.placed"
	BlockChanged              = "block.changed"
	ItemSelecting             = "item.select.before"
	ItemSelected              = "item.selected"
	ItemConsuming             = "item.consume.before"
	ItemConsumed              = "item.consumed"
	ItemCrafting              = "item.craft.before"
	ItemCrafted               = "item.crafted"
	ItemSpawning              = "item.spawn.before"
	ItemSpawned               = "item.spawned"
	ItemPickingUp             = "item.pickup.before"
	ItemPickedUp              = "item.picked_up"
	ItemDespawning            = "item.despawn.before"
	ItemDespawned             = "item.despawned"
	InventoryChanging         = "inventory.change.before"
	InventoryChanged          = "inventory.changed"
	InventoryCreativeChanging = "inventory.creative.before"
	InventoryCreativeChanged  = "inventory.creative.changed"
	InventoryClicking         = "inventory.click.before"
	InventoryClicked          = "inventory.clicked"
	InventoryDragging         = "inventory.drag.before"
	InventoryDragged          = "inventory.dragged"
	InventoryOpening          = "inventory.open.before"
	InventoryOpened           = "inventory.opened"
	InventoryClosed           = "inventory.closed"
	FurnaceSmelting           = "furnace.smelt.before"
	FurnaceSmelted            = "furnace.smelted"
	PlayerDamaging            = "player.damage.before"
	PlayerDamaged             = "player.damaged"
	PlayerDying               = "player.death.before"
	PlayerDead                = "player.dead"
	PlayerRespawning          = "player.respawn.before"
	PlayerRespawned           = "player.respawned"
	WorldTimeChanging         = "world.time.before"
	WorldTimeChanged          = "world.time.changed"
	EntitySpawning            = "entity.spawn.before"
	EntitySpawned             = "entity.spawned"
	EntityDespawning          = "entity.despawn.before"
	EntityDespawned           = "entity.despawned"
	EntityDamaging            = "entity.damage.before"
	EntityDamaged             = "entity.damaged"
	EntityDying               = "entity.death.before"
	EntityDead                = "entity.dead"
	EntityAttacking           = "entity.attack.before"
	EntityAttacked            = "entity.attacked"
	PlayerAttacking           = "player.attack.before"
	PlayerAttacked            = "player.attacked"
	PlayerHealing             = "player.heal.before"
	PlayerHealed              = "player.healed"
	ItemMetaChanging          = "item.meta.before"
	ItemMetaChanged           = "item.meta.changed"
	ItemWearing               = "item.durability.before"
	ItemWorn                  = "item.durability.changed"
	PlayerAttributesChanged   = "player.attributes.changed"
)

type EventSpec struct {
	Fields      []string `json:"fields,omitempty"`
	Name        string   `json:"name"`
	Cancellable bool     `json:"cancellable"`
	Mutable     []string `json:"mutable,omitempty"`
}

func Events() []EventSpec {
	names := []string{ServerStarted, ServerStopping, ServerTick, WorldLoaded, WorldCreating, WorldCreated, WorldSaving, WorldSaved, WorldSaveFailed, WorldRenaming, WorldRenamed, WorldLockChanging, WorldLockChanged, WorldResetting, WorldReset, WorldUndoing, WorldUndone, PlayerJoining, PlayerJoined, PlayerQuit, PlayerMoving, PlayerMoved, PlayerChatting, PlayerChat, CommandExecuting, CommandExecuted, PlayerKicking, PlayerKicked, PlayerTeleporting, PlayerTeleported, BlockBreaking, BlockBroken, BlockPlacing, BlockPlaced, BlockChanged, ItemSelecting, ItemSelected, ItemConsuming, ItemConsumed, ItemCrafting, ItemCrafted, ItemSpawning, ItemSpawned, ItemPickingUp, ItemPickedUp, ItemDespawning, ItemDespawned, InventoryChanging, InventoryChanged, InventoryClicking, InventoryClicked, InventoryOpening, InventoryOpened, InventoryClosed, FurnaceSmelting, FurnaceSmelted, PlayerDamaging, PlayerDamaged, PlayerDying, PlayerDead, PlayerRespawning, PlayerRespawned}
	mutable := map[string][]string{ItemSpawning: {"count"}, WorldCreating: {"name"}, WorldRenaming: {"name"}, PlayerChatting: {"message"}, CommandExecuting: {"command"}, PlayerDamaging: {"amount"}, PlayerDying: {"message"}}
	names = append(names, WorldTimeChanging, WorldTimeChanged, EntitySpawning, EntitySpawned, EntityDespawning, EntityDespawned, EntityDamaging, EntityDamaged, EntityDying, EntityDead, EntityAttacking, EntityAttacked)
	mutable[EntityDamaging] = []string{"amount"}
	names = append(names, PlayerAttacking, PlayerAttacked, PlayerHealing, PlayerHealed, ItemMetaChanging, ItemMetaChanged, ItemWearing, ItemWorn, PlayerAttributesChanged)
	mutable[PlayerAttacking] = []string{"amount"}
	mutable[EntityAttacking] = []string{"amount"}
	mutable[PlayerHealing] = []string{"amount"}
	mutable[ItemWearing] = []string{"amount"}
	names = append(names, InventoryCreativeChanging, InventoryCreativeChanged)
	names = append(names, InventoryDragging, InventoryDragged)
	mutable[InventoryCreativeChanging] = []string{"count"}
	out := make([]EventSpec, 0, len(names))
	for _, name := range names {
		before := len(name) > 7 && name[len(name)-7:] == ".before"
		spec := EventSpec{Name: name, Cancellable: before && name != WorldSaving, Mutable: mutable[name]}
		if name == BlockBreaking || name == BlockBroken || name == BlockPlacing || name == BlockPlaced || name == BlockChanged || name == InventoryChanging || name == InventoryChanged {
			spec.Fields = []string{"x", "y", "z", "oldBlock", "block", "blockKey", "oldBlockKey", "reason", "actor", "source", "target", "item", "count", "consumed", "slot", "face", "stack", "before", "after"}
		}
		if name == EntitySpawning || name == EntitySpawned || name == EntityDespawning || name == EntityDespawned {
			spec.Fields = mobLifecycleFields()
		}
		out = append(out, spec)
	}
	out = append(out, identityEvents()...)
	out = append(out, EventSpec{Name: ServerWorldSelecting, Cancellable: true}, EventSpec{Name: ServerWorldSelected})
	out = append(out, registryEvents()...)
	out = append(out, extensionEvents()...)
	out = append(out, automationEvents()...)
	for i := range out {
		if out[i].Name == AnimalBreeding || out[i].Name == AnimalBred || out[i].Name == InventoryChanging || out[i].Name == InventoryChanged || out[i].Name == ItemConsuming || out[i].Name == ItemConsumed || out[i].Name == EntitySpawning || out[i].Name == EntitySpawned {
			fields := append(out[i].Fields, automationEvents()[0].Fields...)
			out[i].Fields = nil
			seen := map[string]bool{}
			for _, field := range fields {
				if !seen[field] {
					out[i].Fields = append(out[i].Fields, field)
					seen[field] = true
				}
			}
		}
	}
	return out
}

// ScoreboardEntry 表示计分板单行条目
type ScoreboardEntry struct {
	Name     string  `json:"name"`
	Score    int     `json:"score"`
	Icon     string  `json:"icon,omitempty"`
	Badge    string  `json:"badge,omitempty"`
	Color    string  `json:"color,omitempty"`
	Rank     int     `json:"rank,omitempty"`
	Progress float64 `json:"progress,omitempty"`
}
