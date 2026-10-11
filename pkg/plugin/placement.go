package plugin

// PlacementSnapshot exposes the ordinary player's placement context. Returned
// items are independent snapshots; modifying them never changes the operation.
// The existing block and inventory events remain the protection entry points.
type PlacementSnapshot struct {
	X, Y, Z, OldBlock, Block, Slot, Face, Count, Consumed int
	Actor, Reason                                         string
	Stack, Before, After                                  *ItemStack
}

func (e *Event) BlockPlacement() *PlacementSnapshot {
	if e == nil || (e.Name != BlockPlacing && e.Name != BlockPlaced && e.Name != BlockChanged) {
		return nil
	}
	return e.placementSnapshot()
}

// PlacementInventory returns the authoritative inventory transaction belonging
// to an ordinary placement, including real creative/survival consumption.
func (e *Event) PlacementInventory() *PlacementSnapshot {
	if e == nil || (e.Name != InventoryChanging && e.Name != InventoryChanged) || e.String("reason") != "place" {
		return nil
	}
	return e.placementSnapshot()
}

func (e *Event) placementSnapshot() *PlacementSnapshot {
	if e.Data["stack"] == nil || e.Data["block"] == nil || e.Number("block") <= 0 {
		return nil
	}
	actor := e.String("actor")
	if actor == "" && e.Player != nil {
		actor = e.Player.AccountID
	}
	before, after := e.Item("before"), e.Item("after")
	consumed := int(e.Number("consumed"))
	// Older component placement events already have inventory snapshots.
	if _, explicit := e.Data["consumed"]; !explicit && before != nil {
		consumed = before.Count
		if after != nil {
			consumed -= after.Count
		}
		if consumed < 0 {
			consumed = 0
		}
	}
	return &PlacementSnapshot{X: int(e.Number("x")), Y: int(e.Number("y")), Z: int(e.Number("z")), OldBlock: int(e.Number("oldBlock")), Block: int(e.Number("block")), Slot: int(e.Number("slot")), Face: int(e.Number("face")), Count: int(e.Number("count")), Consumed: consumed, Actor: actor, Reason: e.String("reason"), Stack: e.Item("stack"), Before: before, After: after}
}
