package plugin

import "encoding/json"

// PlayerTransfer is a detached, read-only snapshot of a dimension transfer.
// Event.Player contains the target player's complete inventory metadata.
type PlayerTransfer struct {
	FromWorldID     string     `json:"fromWorldId"`
	WorldID         string     `json:"worldId"`
	FromDimension   string     `json:"fromDimension"`
	Dimension       string     `json:"dimension"`
	FromX           float64    `json:"fromX"`
	FromY           float64    `json:"fromY"`
	FromZ           float64    `json:"fromZ"`
	X               float64    `json:"x"`
	Y               float64    `json:"y"`
	Z               float64    `json:"z"`
	Reason          string     `json:"reason"`
	Actor           string     `json:"actor"`
	ActorPlayer     *Combatant `json:"actorPlayer,omitempty"`
	Target          *Combatant `json:"target"`
	TargetAccountID string     `json:"targetAccountId"`
	Stack           *ItemStack `json:"stack,omitempty"`
	Source          string     `json:"source"`
}

func (e *Event) PlayerTransfer() *PlayerTransfer {
	if e == nil || e.Data == nil {
		return nil
	}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return nil
	}
	var snapshot PlayerTransfer
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil
	}
	return &snapshot
}

// ChangeDimension queues a transfer to a validated safe spawn in the same world.
// It passes through both cancellable dimension and teleport events.
func (c *Context) ChangeDimension(e *Event, dimension string) {
	if e != nil && e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "change_dimension", WorldID: e.World.ID, PlayerID: e.Player.ID, Dimension: dimension})
	}
}
func playerTransferFields() []string {
	return []string{"fromWorldId", "worldId", "fromDimension", "dimension", "fromX", "fromY", "fromZ", "x", "y", "z", "reason", "actor", "actorPlayer", "target", "targetAccountId", "stack", "source"}
}
