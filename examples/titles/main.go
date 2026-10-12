package main

import (
	"encoding/json"
	"log"
	api "mcraft/backend/pkg/plugin"
)

type Config struct {
	DefaultPrefix string `json:"defaultPrefix"`
	OpPrefix      string `json:"opPrefix"`
	OpTitle       string `json:"opTitle"`
	OpIcon        string `json:"opIcon"`
	OpTitleColor  string `json:"opTitleColor"`
}

func main() {
	p := api.New()

	config := Config{
		DefaultPrefix: "§7[冒险家]§r ",
		OpPrefix:      "§c[管理员]§r ",
		OpTitle:       "★ 秩序守护者 ★",
		OpIcon:        "shield",
		OpTitleColor:  "#ff5555",
	}

	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			if err := json.Unmarshal(c.Config, &config); err != nil {
				return err
			}
		}
		log.Printf("[player-titles] enabled. opPrefix=%s opTitle=%s opIcon=%s", config.OpPrefix, config.OpTitle, config.OpIcon)
		return nil
	}

	// 监听名牌渲染事件：动态注入或覆盖称号与前缀
	p.On(api.PlayerNametagRendering, api.Subscription{Priority: api.Normal}, func(c *api.Context, e *api.Event) error {
		isOp, _ := e.Data["isOp"].(bool)
		curPrefix, _ := e.Data["prefix"].(string)
		curTitle, _ := e.Data["title"].(string)
		curIcon, _ := e.Data["titleIcon"].(string)

		if isOp {
			if curPrefix == "" && config.OpPrefix != "" {
				e.Data["prefix"] = config.OpPrefix
			}
			if curTitle == "" && config.OpTitle != "" {
				e.Data["title"] = config.OpTitle
			}
			if curIcon == "" && config.OpIcon != "" {
				e.Data["titleIcon"] = config.OpIcon
			}
			if config.OpTitleColor != "" {
				e.Data["titleColor"] = config.OpTitleColor
			}
		} else {
			if curPrefix == "" && config.DefaultPrefix != "" {
				e.Data["prefix"] = config.DefaultPrefix
			}
		}

		return nil
	})

	// 监听名牌变动通知
	p.On(api.PlayerNametagChanged, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		player, _ := e.Data["player"].(string)
		prefix, _ := e.Data["prefix"].(string)
		suffix, _ := e.Data["suffix"].(string)
		title, _ := e.Data["title"].(string)
		icon, _ := e.Data["titleIcon"].(string)
		log.Printf("[player-titles] nametag updated for %s: prefix=%q suffix=%q title=%q icon=%q", player, prefix, suffix, title, icon)
		return nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
