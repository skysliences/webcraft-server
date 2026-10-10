// Build: go build -o ../plugins/welcome/welcome ./examples/welcome
package main

import (
	"encoding/json"
	"fmt"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	p := api.New()
	config := struct {
		Greeting               string  `json:"greeting"`
		BlockedWord            string  `json:"blockedWord"`
		ChatPrefix             string  `json:"chatPrefix"`
		CenterProtectionRadius float64 `json:"centerProtectionRadius"`
	}{Greeting: "欢迎来到 Mcraft！输入 /hello、/where、/plugins 体验插件。"}
	var dataFile string
	var visits uint64
	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			if err := json.Unmarshal(c.Config, &config); err != nil {
				return err
			}
		}
		dataFile = filepath.Join(c.DataDir, "visits.json")
		raw, err := os.ReadFile(dataFile)
		if err == nil {
			if err = json.Unmarshal(raw, &visits); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		log.Print("welcome enabled")
		return nil
	}
	persist := func() error {
		raw, _ := json.Marshal(visits)
		if err := os.WriteFile(dataFile+".tmp", raw, 0600); err != nil {
			return err
		}
		return os.Rename(dataFile+".tmp", dataFile)
	}
	p.On(api.PlayerJoined, api.Subscription{}, func(c *api.Context, e *api.Event) error { visits++; c.Message(e, config.Greeting); return persist() })
	p.On(api.PlayerChatting, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		message := e.String("message")
		if config.BlockedWord != "" && strings.Contains(strings.ToLower(message), strings.ToLower(config.BlockedWord)) {
			e.Cancel("示例插件：消息包含禁止词。")
			return nil
		}
		if config.ChatPrefix != "" {
			e.Data["message"] = config.ChatPrefix + message
		}
		return nil
	})
	protect := func(c *api.Context, e *api.Event) error {
		if config.CenterProtectionRadius > 0 && e.Player != nil && !e.Player.Owner {
			dx, dz := e.Number("x")-512, e.Number("z")-512
			if dx*dx+dz*dz < config.CenterProtectionRadius*config.CenterProtectionRadius {
				e.Cancel("示例插件：中心保护区内只有房主可以建造。")
			}
		}
		return nil
	}
	p.On(api.BlockBreaking, api.Subscription{IgnoreCancelled: true}, protect)
	p.On(api.BlockPlacing, api.Subscription{IgnoreCancelled: true}, protect)
	p.Command(api.Command{Name: "hello", Aliases: []string{"hi"}, Description: "插件问候", Usage: "/hello [名字]"}, func(c *api.Context, e *api.Event, args []string) error {
		name := e.Player.Name
		if len(args) > 0 {
			name = strings.Join(args, " ")
		}
		c.Message(e, fmt.Sprintf("你好，%s！welcome 插件已运行；累计加入 %d 次。", name, visits))
		return nil
	})
	p.Command(api.Command{Name: "where", Description: "查看当前坐标"}, func(c *api.Context, e *api.Event, args []string) error {
		c.Message(e, fmt.Sprintf("世界 %s：X %.1f / Y %.1f / Z %.1f", e.World.Name, e.Player.X, e.Player.Y, e.Player.Z))
		return nil
	})
	p.Command(api.Command{Name: "starter", Permission: "owner", Description: "房主领取 3 个面包（生存模式）"}, func(c *api.Context, e *api.Event, args []string) error {
		if e.World.Mode != "survival" {
			c.Message(e, "/starter 需要生存模式。")
			return nil
		}
		c.GiveItem(e, "Bread", 3)
		return nil
	})
	p.Every(100, func(c *api.Context, e *api.Event) error { return persist() })
	p.OnDisable = func(c *api.Context) error { log.Print("welcome disabled"); return persist() }
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
