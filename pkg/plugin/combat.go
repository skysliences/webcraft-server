package plugin

// Combatant uses distinct player and entity ID namespaces. Environment damage
// has no attacker. These are snapshots from before the operation commits.
type Combatant struct {
	Kind       string  `json:"kind"` // player or entity
	PlayerID   uint16  `json:"playerId,omitempty"`
	EntityID   uint32  `json:"entityId,omitempty"`
	EntityType string  `json:"entityType,omitempty"`
	Name       string  `json:"name,omitempty"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Z          float64 `json:"z"`
	Health     float64 `json:"health"`
}
type DamageContext struct {
	Cause            string     `json:"cause"`            // player_attack, mob_attack, fall, lava, drowning, void, starvation, sunlight, plugin
	Actor            string     `json:"actor,omitempty"`  // stable account ID or server; preserved by delayed damage
	Source           string     `json:"source,omitempty"` // plugin or originating system
	Attacker         *Combatant `json:"attacker,omitempty"`
	Target           *Combatant `json:"target"`
	Weapon           *ItemStack `json:"weapon,omitempty"`
	RawAmount        float64    `json:"rawAmount"`
	Armor            float64    `json:"armor"`
	Amount           float64    `json:"amount"`        // after armor and before the current listener's changes
	AppliedAmount    float64    `json:"appliedAmount"` // actual health lost, only on after events
	HealthBefore     float64    `json:"healthBefore"`
	HealthAfter      float64    `json:"healthAfter"`
	AbsorptionBefore float64    `json:"absorptionBefore,omitempty"`
	AbsorptionAfter  float64    `json:"absorptionAfter,omitempty"`
	AbsorbedAmount   float64    `json:"absorbedAmount,omitempty"`
}
