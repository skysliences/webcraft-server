package plugin

import "encoding/json"

// InventorySlotChange holds detached, full-metadata snapshots of one slot.
type InventorySlotChange struct {
	Slot   int        `json:"slot"`
	Before *ItemStack `json:"before"`
	After  *ItemStack `json:"after"`
}

// InventoryDragContext describes an atomic cursor distribution. Button 0 is
// even distribution; button 1 places one item in each eligible slot. All fields
// are read-only; cancel InventoryDragging to prevent the complete transaction.
type InventoryDragContext struct {
	Reason       string                `json:"reason"`
	Actor        string                `json:"actor"`
	Target       InventoryDragTarget   `json:"target"`
	Window       int                   `json:"window"`
	Kind         string                `json:"kind"`
	Button       int                   `json:"button"`
	DragType     string                `json:"dragType"`
	Slots        []int                 `json:"slots"`
	CursorBefore *ItemStack            `json:"cursorBefore"`
	CursorAfter  *ItemStack            `json:"cursorAfter"`
	Changes      []InventorySlotChange `json:"changes"`
}

type InventoryDragTarget struct {
	Kind     string `json:"kind"`
	PlayerID uint16 `json:"playerId"`
	Window   int    `json:"window"`
	Slots    []int  `json:"slots"`
}

// InventoryDrag returns a detached context for drag.before/dragged and the
// accompanying legacy click/change events. Changes here do not alter the host.
func (e *Event) InventoryDrag() *InventoryDragContext {
	if e == nil || e.String("reason") != "quick_craft" {
		return nil
	}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return nil
	}
	var context InventoryDragContext
	if json.Unmarshal(raw, &context) != nil {
		return nil
	}
	return &context
}
