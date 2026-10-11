package plugin

const (
	InventoryMoving    = "inventory.move.before"
	InventoryMoved     = "inventory.moved"
	InventoryPickingUp = "inventory.pickup.before"
	InventoryPickedUp  = "inventory.picked_up"
	AnimalFeeding      = "animal.feed.before"
	AnimalFed          = "animal.fed"
)

func automationEvents() []EventSpec {
	fields := []string{"reason", "actor", "source", "target", "origin", "initiator", "stack", "item", "count", "slot", "sourceSlot", "destinationSlot", "before", "after", "sourceBefore", "sourceAfter", "destinationBefore", "destinationAfter", "entityId", "remaining", "mob_id", "mob_type", "baby_id", "parent", "mate", "baby", "parentBefore", "parentAfter", "mateBefore", "mateAfter", "food", "mateFood", "breeder", "mateBreeder", "consumed"}
	return []EventSpec{
		{Name: InventoryMoving, Cancellable: true, Fields: fields}, {Name: InventoryMoved, Fields: fields},
		{Name: InventoryPickingUp, Cancellable: true, Fields: fields}, {Name: InventoryPickedUp, Fields: fields},
		{Name: AnimalFeeding, Cancellable: true, Fields: fields}, {Name: AnimalFed, Fields: fields},
	}
}

// ActivateHopper attempts one ordinary push/pull cycle, respecting redstone
// locks, cooldown, sided slots and all protection events. The five-slot
// snapshot must still match; it is deeply copied, including custom data.
func (c *Context) ActivateHopper(e *Event, x, y, z int, expected []*ItemStack) {
	if e == nil || e.World == nil || len(expected) != 5 {
		return
	}
	v := make([]*ItemStack, 5)
	for i, item := range expected {
		v[i] = item.Clone()
	}
	c.Actions = append(c.Actions, Action{Type: "activate_hopper", WorldID: e.World.ID, X: float64(x), Y: float64(y), Z: float64(z), ContainerExpected: v})
}

// FeedAnimal follows the player's normal feeding/breeding transaction. A stale
// selection or food snapshot is rejected without consuming anything.
func (c *Context) FeedAnimal(e *Event, id uint32) {
	if e == nil || e.World == nil || e.Player == nil || e.Player.Item(e.Player.Selected) == nil {
		return
	}
	c.Actions = append(c.Actions, Action{Type: "feed_animal", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: e.Player.Selected, EntityID: id, Expected: e.Player.Item(e.Player.Selected)})
}
