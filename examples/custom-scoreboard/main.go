// Build: go build -o ../plugins/custom-scoreboard/custom-scoreboard ./examples/custom-scoreboard
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type EntryTemplate struct {
	Name     string `json:"name"`
	Score    any    `json:"score"`
	Icon     string `json:"icon,omitempty"`
	Badge    string `json:"badge,omitempty"`
	Color    string `json:"color,omitempty"`
	Rank     any    `json:"rank,omitempty"`
	Progress any    `json:"progress,omitempty"`
}

type ScoreboardConfig struct {
	Enabled bool              `json:"enabled"`
	Title   string            `json:"title"`
	Entries []EntryTemplate   `json:"entries"`
	Footer  map[string]string `json:"footer"`
}

var (
	cfgMu      sync.RWMutex
	currentCfg ScoreboardConfig
	configPath string
)

func defaultScoreboardConfig() ScoreboardConfig {
	return ScoreboardConfig{
		Enabled: true,
		Title:   "⚡ CCRAFT 官方服务器 ⚡",
		Entries: []EntryTemplate{
			{
				Name:  "世界",
				Score: 0,
				Icon:  "🌐",
				Badge: "{mode}",
			},
			{
				Name:  "在线人数",
				Score: "{online}",
				Icon:  "👥",
				Badge: "{online} 人",
			},
			{
				Name:  "网络延迟",
				Score: "{ping}",
				Icon:  "⚡",
				Badge: "{ping}ms",
			},
			{
				Name:     "{player_name}",
				Score:    "{health}",
				Icon:     "❤️",
				Badge:    "YOU",
				Progress: "{health_ratio}",
			},
			{
				Name:  "游戏时刻",
				Score: 0,
				Icon:  "⏳",
				Badge: "{time}",
			},
			{
				Name:  "位置坐标",
				Score: 0,
				Icon:  "🧭",
				Badge: "{pos_x}, {pos_y}, {pos_z}",
			},
		},
		Footer: map[string]string{
			"server": "game.ccraft.app",
			"mode":   "{mode}",
			"tag":    "ccraft.app",
		},
	}
}

func loadConfig(path string) (ScoreboardConfig, error) {
	cfg := defaultScoreboardConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func getScoreboardConfig() ScoreboardConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return currentCfg
}

func setScoreboardConfig(cfg ScoreboardConfig) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	currentCfg = cfg
}

func replacePlaceholders(text string, vars map[string]string) string {
	for k, v := range vars {
		text = strings.ReplaceAll(text, "{"+k+"}", v)
	}
	return text
}

func resolveInt(val any, vars map[string]string) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		resolved := replacePlaceholders(v, vars)
		if n, err := strconv.Atoi(strings.TrimSpace(resolved)); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(resolved), 64); err == nil {
			return int(f)
		}
	}
	return 0
}

func resolveFloat(val any, vars map[string]string) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		resolved := replacePlaceholders(v, vars)
		if f, err := strconv.ParseFloat(strings.TrimSpace(resolved), 64); err == nil {
			return f
		}
	}
	return 0.0
}

func main() {
	p := api.New()

	p.OnEnable = func(c *api.Context) error {
		exe, _ := os.Executable()
		exeDir := filepath.Dir(exe)
		configPath = filepath.Join(exeDir, "config.json")

		// 如果插件目录不存在 config.json，则写入默认配置方便服主修改
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			def := defaultScoreboardConfig()
			if raw, err := json.MarshalIndent(def, "", "  "); err == nil {
				_ = os.WriteFile(configPath, raw, 0644)
			}
		}

		loaded, err := loadConfig(configPath)
		if err != nil {
			log.Printf("[custom-scoreboard] failed to read %s: %v, using default", configPath, err)
			setScoreboardConfig(defaultScoreboardConfig())
		} else {
			setScoreboardConfig(loaded)
			log.Printf("[custom-scoreboard] loaded configuration from %s", configPath)
		}
		return nil
	}

	p.On(api.ScoreboardRendering, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		// 每次也可以根据需要检查或重载，保证服主修改后即刻生效
		cfg := getScoreboardConfig()
		if !cfg.Enabled {
			e.Data["enabled"] = false
			return nil
		}

		vars := make(map[string]string)
		vars["player_name"] = "Player"
		vars["ping"] = "0"
		vars["online"] = "1"
		vars["world_name"] = "world"
		vars["mode"] = "SURVIVAL"
		vars["health"] = "20"
		vars["max_health"] = "20"
		vars["health_ratio"] = "1.0"
		vars["food"] = "20"
		vars["pos_x"] = "0"
		vars["pos_y"] = "64"
		vars["pos_z"] = "0"
		vars["dimension"] = "overworld"
		vars["time"] = "12:00"

		if e.World != nil {
			vars["world_name"] = e.World.Name
			vars["mode"] = strings.ToUpper(e.World.Mode)
			vars["online"] = strconv.Itoa(e.World.Online)
			vars["dimension"] = e.World.Dimension
		}

		if e.Player != nil {
			vars["player_name"] = e.Player.Name
			hp := math.Ceil(e.Player.Health)
			vars["health"] = fmt.Sprintf("%.0f", hp)
			maxHP := e.Player.MaxHealth
			if maxHP <= 0 {
				maxHP = 20
			}
			vars["max_health"] = fmt.Sprintf("%.0f", maxHP)
			ratio := e.Player.Health / maxHP
			if ratio < 0 {
				ratio = 0
			} else if ratio > 1 {
				ratio = 1
			}
			vars["health_ratio"] = fmt.Sprintf("%.2f", ratio)
			vars["food"] = strconv.Itoa(e.Player.Food)
			vars["pos_x"] = fmt.Sprintf("%.0f", e.Player.X)
			vars["pos_y"] = fmt.Sprintf("%.0f", e.Player.Y)
			vars["pos_z"] = fmt.Sprintf("%.0f", e.Player.Z)
		}

		if pingVal, ok := e.Data["ping"].(float64); ok {
			vars["ping"] = strconv.Itoa(int(pingVal))
		} else if pingVal, ok := e.Data["ping"].(int); ok {
			vars["ping"] = strconv.Itoa(pingVal)
		}

		if timeStr, ok := e.Data["time"].(string); ok && timeStr != "" {
			vars["time"] = timeStr
		}

		// 替换标题
		e.Data["title"] = replacePlaceholders(cfg.Title, vars)

		// 替换条目
		entries := make([]map[string]any, 0, len(cfg.Entries))
		for _, tpl := range cfg.Entries {
			entry := map[string]any{
				"name":  replacePlaceholders(tpl.Name, vars),
				"score": resolveInt(tpl.Score, vars),
			}
			if tpl.Icon != "" {
				entry["icon"] = replacePlaceholders(tpl.Icon, vars)
			}
			if tpl.Badge != "" {
				entry["badge"] = replacePlaceholders(tpl.Badge, vars)
			}
			if tpl.Color != "" {
				entry["color"] = replacePlaceholders(tpl.Color, vars)
			}
			if tpl.Rank != nil {
				entry["rank"] = resolveInt(tpl.Rank, vars)
			}
			if tpl.Progress != nil {
				entry["progress"] = resolveFloat(tpl.Progress, vars)
			}
			entries = append(entries, entry)
		}
		e.Data["entries"] = entries

		// 替换页脚
		footer := make(map[string]any)
		for k, v := range cfg.Footer {
			footer[k] = replacePlaceholders(v, vars)
		}
		if pVal, err := strconv.Atoi(vars["ping"]); err == nil {
			footer["ping"] = pVal
		}
		e.Data["footer"] = footer
		e.Data["enabled"] = true

		return nil
	})

	// 管理员命令：重载计分板配置
	p.Command(api.Command{
		Name:        "screload",
		Aliases:     []string{"scoreboard-reload"},
		Description: "重新加载 custom-scoreboard 插件配置文件",
		Permission:  "owner",
	}, func(c *api.Context, e *api.Event, args []string) error {
		loaded, err := loadConfig(configPath)
		if err != nil {
			c.Message(e, fmt.Sprintf("计分板配置重载失败: %v", err))
			return nil
		}
		setScoreboardConfig(loaded)
		c.Message(e, "计分板配置已成功重新加载！")
		return nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
