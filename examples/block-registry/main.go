// Optional example. Installing this plugin adds stable IDs, but never edits the
// world until an OP runs /registrydemo or a player places the received blocks.
package main

import (
	"log"
	api "mcraft/backend/pkg/plugin"
)

func main() {
	p := api.New()
	p.OnEnable = func(c *api.Context) error {
		c.RegisterBlock(api.BlockDefinition{Key: "block-registry:example_block", Name: "示例自定义方块", ModelBlock: 21, Hardness: 1.5, Stack: 64})
		c.RegisterItem(api.ItemDefinition{Key: "block-registry:example_item", Name: "示例自定义物品", ModelItem: 276, Stack: 1, Durability: 200})
		return nil
	}
	p.Command(api.Command{Name: "registrydemo", Permission: "server.gamemode", Description: "领取插件注册的方块和物品"}, func(c *api.Context, e *api.Event, args []string) error {
		c.GiveItem(e, "block-registry:example_block", 16)
		c.GiveItem(e, "block-registry:example_item", 1)
		c.Message(e, "已请求发放自定义方块和物品；使用现有放置、破坏和物品事件扩展行为。")
		return nil
	})
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
