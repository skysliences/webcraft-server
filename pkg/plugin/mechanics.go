package plugin

// DispenseBlock activates a dispenser/dropper through the same protection and
// atomic output rules as a redstone pulse. All nine source slots must match the
// supplied complete snapshot when the action is applied. No player is required.
func (c *Context) DispenseBlock(e *Event, x, y, z int, containerExpected []*ItemStack) {
	if e == nil || e.World == nil || len(containerExpected) != 9 {
		return
	}
	expected := make([]*ItemStack, 9)
	for i, item := range containerExpected {
		expected[i] = item.Clone()
	}
	c.Actions = append(c.Actions, Action{Type: "dispense_block", WorldID: e.World.ID, X: float64(x), Y: float64(y), Z: float64(z), ContainerExpected: expected})
}

// UseMechanic invokes a player's ordinary protected block interaction or placement.
func (c *Context) UseMechanic(e *Event, x, y, z, face int) {
	if e == nil || e.World == nil || e.Player == nil {
		return
	}
	c.Actions = append(c.Actions, Action{Type: "use_mechanic", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: e.Player.Selected, X: float64(x), Y: float64(y), Z: float64(z), Face: face, Expected: e.Player.Item(e.Player.Selected)})
}

// WriteSign copies the proposed four lines and the selected item snapshot.
func (c *Context) WriteSign(e *Event, x, y, z int, lines []string) {
	if e == nil || e.World == nil || e.Player == nil {
		return
	}
	c.Actions = append(c.Actions, Action{Type: "write_sign", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: e.Player.Selected, X: float64(x), Y: float64(y), Z: float64(z), Lines: append([]string(nil), lines...), Expected: e.Player.Item(e.Player.Selected)})
}

// UseVehicle places a minecart/boat or casts/reels a fishing rod through the
// same inventory, permission and cancellable event checks as player input.
func (c *Context) UseVehicle(e *Event, slot, x, y, z, face int) {
	if e == nil || e.World == nil || e.Player == nil || slot != e.Player.Selected || e.Player.Item(slot) == nil {
		return
	}
	c.Actions = append(c.Actions, Action{Type: "use_vehicle", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: slot, X: float64(x), Y: float64(y), Z: float64(z), Face: face, Expected: e.Player.Item(slot)})
}

// InteractVehicle enters/exits a reachable vehicle without bypassing events.
func (c *Context) InteractVehicle(e *Event, id uint32) {
	if e == nil || e.World == nil || e.Player == nil {
		return
	}
	c.Actions = append(c.Actions, Action{Type: "interact_vehicle", WorldID: e.World.ID, PlayerID: e.Player.ID, EntityID: id})
}
