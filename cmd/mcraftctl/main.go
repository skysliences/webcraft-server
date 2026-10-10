package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mcraft/backend/internal/game"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	endpoint := flag.String("url", "http://127.0.0.1:8081", "服务器地址")
	data := flag.String("data", "data", "数据目录")
	engine := flag.String("engine", "src/client/engine.wasm", "世界引擎")
	pluginDir := flag.String("plugins", "plugins", "插件目录")
	world := flag.String("world", "", "玩法命令的世界 ID")
	player := flag.String("player", "", "目标玩家名字/账号 ID")
	confirm := flag.Bool("confirm", false, "确认恢复备份")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "mcraftctl [选项] status|backup|op <账号>|...|restore <备份.zip>")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	if args[0] == "restore" {
		if len(args) != 2 || !*confirm {
			fatal("恢复需要：mcraftctl --confirm restore <备份.zip>")
		}
		if res, err := client.Get(strings.TrimRight(*endpoint, "/") + "/healthz"); err == nil {
			res.Body.Close()
			fatal("请先停止服务端，再执行恢复")
		}
		dir, err := filepath.Abs(*data)
		if err != nil {
			fatal(err.Error())
		}
		err = game.RestoreOffline(game.Config{DataDir: dir, EngineFile: *engine, PluginDir: *pluginDir, InfiniteWorlds: true, SingleWorld: os.Getenv("LEGACY_MULTI_WORLD") != "true", WorldID: os.Getenv("WORLD_ID"), WorldName: os.Getenv("WORLD_NAME"), WorldMode: os.Getenv("WORLD_MODE"), IdentityIssuer: os.Getenv("AUTH_ISSUER_URL"), IdentityServerID: os.Getenv("AUTH_SERVER_ID"), IdentityPublicKey: os.Getenv("AUTH_PUBLIC_KEY"), IdentityCallback: os.Getenv("AUTH_CALLBACK_URL"), GamePublicURL: os.Getenv("GAME_PUBLIC_URL")}, args[1])
		if err != nil {
			fatal(err.Error())
		}
		fmt.Println("恢复完成，原数据已保留在 data/backups。现在可以启动服务端。")
		return
	}
	key, err := os.ReadFile(filepath.Join(*data, "admin.key"))
	if err != nil {
		fatal(err.Error())
	}
	body, _ := json.Marshal(map[string]string{"command": strings.Join(args, " "), "world": *world, "player": *player})
	req, err := http.NewRequest("POST", strings.TrimRight(*endpoint, "/")+"/api/admin/console", bytes.NewReader(body))
	if err != nil {
		fatal(err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		fatal(err.Error())
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		fatal(err.Error())
	}
	if res.StatusCode != 200 {
		fatal(string(raw))
	}
	var formatted bytes.Buffer
	if json.Indent(&formatted, raw, "", "  ") == nil {
		fmt.Println(formatted.String())
	} else {
		fmt.Println(string(raw))
	}
}
func fatal(msg string) { fmt.Fprintln(os.Stderr, msg); os.Exit(1) }
