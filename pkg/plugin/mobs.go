package plugin

import "encoding/json"

// MobSpawningConfig follows Bukkit's category limits and Minecraft tick units.
// Limits apply to natural spawning; TotalLimit is a separate host safety limit.
type MobSpawningConfig struct {
	GenerateChunkAnimals bool `json:"generateChunkAnimals"`
	MonsterLimit         int  `json:"monsterLimit"`
	AnimalLimit          int  `json:"animalLimit"`
	SpawnRange           int  `json:"spawnRange"`
	MonsterInterval      int  `json:"monsterInterval"`
	AnimalInterval       int  `json:"animalInterval"`
	AttemptsPerTick      int  `json:"attemptsPerTick"`
	SpawnsPerTick        int  `json:"spawnsPerTick"`
	TotalLimit           int  `json:"totalLimit"`
}

// MobLifecycle preserves the API v1 fields and adds read-only spawn/despawn
// policy snapshots. Persistent mobs still die normally; they resist despawning.
type MobLifecycle struct {
	X                     float64    `json:"x"`
	Y                     float64    `json:"y"`
	Z                     float64    `json:"z"`
	Health                float64    `json:"health"`
	NearestPlayerDistance float64    `json:"nearestPlayerDistance"`
	EntityID              uint32     `json:"entityId"`
	Type                  string     `json:"type"`
	SpawnReason           string     `json:"spawnReason"`
	Category              string     `json:"category"`
	Persistent            bool       `json:"persistent"`
	InactiveTicks         int        `json:"inactiveTicks"`
	Reason                string     `json:"reason"`
	Actor                 string     `json:"actor"`
	Source                string     `json:"source"`
	CategoryLimit         int        `json:"categoryLimit"`
	CategoryCount         int        `json:"categoryCount"`
	EligibleChunks        int        `json:"eligibleChunks"`
	Target                *Combatant `json:"target"`
	Biome                 string     `json:"biome,omitempty"`
	SkyLight              int        `json:"skyLight,omitempty"`
	BlockLight            int        `json:"blockLight,omitempty"`
	LocalLight            int        `json:"localLight,omitempty"`
	MinecraftTick         uint64     `json:"minecraftTick,omitempty"`
	SpawningRules         string     `json:"spawningRules,omitempty"`
}

func (e *Event) MobLifecycle() *MobLifecycle {
	if e == nil || e.Data == nil {
		return nil
	}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return nil
	}
	var snapshot MobLifecycle
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.EntityID == 0 {
		return nil
	}
	return &snapshot
}

// SpawnPersistentMob uses the same protected spawn path as SpawnMob.
func (c *Context) SpawnPersistentMob(world, kind string, x, y, z float64) {
	c.Actions = append(c.Actions, Action{Type: "spawn_mob", WorldID: world, EntityType: kind, X: x, Y: y, Z: z, Persistent: true})
}

func mobLifecycleFields() []string {
	return []string{"entityId", "type", "x", "y", "z", "health", "flags", "growthTicks", "reason", "spawnReason", "category", "persistent", "inactiveTicks", "actor", "source", "target", "categoryLimit", "categoryCount", "eligibleChunks", "nearestPlayerDistance", "biome", "skyLight", "blockLight", "localLight", "minecraftTick", "spawningRules"}
}
