// Optional plugin: protects tagged items against cursor distribution. This is
// plugin policy, not a core Lore rule. Enable by packaging this executable with
// an API v1 plugin.json in a server's plugin directory.
package main

import (
	"log"
	api "mcraft/backend/pkg/plugin"
)

func main() {
	p := api.New()
	p.On(api.BlockPlacing, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if placed := e.BlockPlacement(); placed != nil && placed.Stack != nil && placed.Stack.Meta != nil && placed.Stack.Meta.CustomData["example:locked"] == true {
			e.Cancel("这件物品被插件锁定，不能放置。")
		}
		return nil
	})
	p.On(api.InventoryDragging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		drag := e.InventoryDrag()
		if drag != nil && drag.CursorBefore != nil && drag.CursorBefore.Meta != nil && drag.CursorBefore.Meta.CustomData["example:locked"] == true {
			e.Cancel("这件物品被插件锁定，不能拖拽分配。")
		}
		return nil
	})
	p.On(api.InventoryChanged, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
		if placed := e.PlacementInventory(); placed != nil {
			log.Printf("placement inventory committed actor=%s slot=%d consumed=%d before=%v after=%v", placed.Actor, placed.Slot, placed.Consumed, placed.Before, placed.After)
		}
		return nil
	})
	p.On(api.InventoryDragged, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
		if drag := e.InventoryDrag(); drag != nil {
			log.Printf("inventory drag committed actor=%s window=%d type=%s slots=%v", drag.Actor, drag.Window, drag.DragType, drag.Slots)
		}
		return nil
	})
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
