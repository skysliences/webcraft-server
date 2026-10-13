package main

import (
	"bufio"
	"context"
	"log"
	"mcraft/backend/internal/game"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func envBool(k string, fallback bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envFloat(k string, fallback float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envInt(k string, fallback int) int {
	if v := os.Getenv(k); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func main() {
	root := env("PROJECT_ROOT", "..")
	data, _ := filepath.Abs(env("DATA_DIR", filepath.Join(root, "data")))
	engine := env("ENGINE_FILE", filepath.Join(root, "src/client/engine.wasm"))
	if _, err := os.Stat(engine); err != nil {
		engine = filepath.Join(root, "public/engine.wasm")
	}
	cap, err := strconv.Atoi(env("MAX_PLAYERS", "64"))
	if err != nil || cap < 1 || cap > 1000 {
		log.Fatal("MAX_PLAYERS must be 1–1000")
	}
	timeout, err := time.ParseDuration(env("PLUGIN_TIMEOUT", "100ms"))
	if err != nil || timeout < 10*time.Millisecond || timeout > time.Second {
		log.Fatal("PLUGIN_TIMEOUT must be 10ms–1s")
	}
	secureCookies, err := strconv.ParseBool(env("AUTH_COOKIE_SECURE", "false"))
	if err != nil {
		log.Fatal("AUTH_COOKIE_SECURE must be true or false")
	}
	config := game.Config{DataDir: data, EngineFile: engine, AllowedOrigins: strings.Split(env("ALLOWED_ORIGINS", "http://127.0.0.1:8080,http://localhost:8080"), ","), MaxPlayers: cap, PluginDir: env("PLUGIN_DIR", filepath.Join(root, "plugins")), PluginTimeout: timeout, SecureCookies: secureCookies}
	mobs := game.DefaultMobSpawningConfig()
	for _, setting := range []struct {
		name  string
		value *int
	}{
		{"MOB_MONSTER_LIMIT", &mobs.MonsterLimit}, {"MOB_ANIMAL_LIMIT", &mobs.AnimalLimit},
		{"MOB_SPAWN_RANGE", &mobs.SpawnRange}, {"MOB_MONSTER_INTERVAL", &mobs.MonsterInterval},
		{"MOB_ANIMAL_INTERVAL", &mobs.AnimalInterval}, {"MOB_SPAWN_ATTEMPTS", &mobs.AttemptsPerTick},
		{"MOB_SPAWNS_PER_TICK", &mobs.SpawnsPerTick}, {"MOB_TOTAL_LIMIT", &mobs.TotalLimit},
	} {
		if raw := os.Getenv(setting.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				log.Fatalf("%s must be an integer", setting.name)
			}
			*setting.value = value
		}
	}
	if raw := os.Getenv("MOB_GENERATE_CHUNK_ANIMALS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			log.Fatal("MOB_GENERATE_CHUNK_ANIMALS must be true or false")
		}
		mobs.GenerateChunkAnimals = value
	}
	config.MobSpawning = &mobs
	config.InfiniteWorlds = env("INFINITE_WORLDS", "true") != "false"
	legacyMultiWorld, err := strconv.ParseBool(env("LEGACY_MULTI_WORLD", "false"))
	if err != nil {
		log.Fatal("LEGACY_MULTI_WORLD must be true or false")
	}
	config.SingleWorld = !legacyMultiWorld
	config.WorldID = os.Getenv("WORLD_ID")
	config.WorldName = env("WORLD_NAME", "世界")
	config.WorldMode = env("WORLD_MODE", "survival")
	issuer := os.Getenv("AUTH_ISSUER_URL")
	if issuer == "" && (os.Getenv("AUTH_SERVER_ID") != "" || os.Getenv("AUTH_CALLBACK_URL") != "" || strings.ToLower(os.Getenv("ACCOUNT_MODE")) == "central") {
		issuer = "https://login.ccraft.app"
	}
	config.IdentityIssuer = issuer
	config.IdentityServerID = os.Getenv("AUTH_SERVER_ID")
	config.IdentityPublicKey = os.Getenv("AUTH_PUBLIC_KEY")
	config.IdentityCallback = os.Getenv("AUTH_CALLBACK_URL")
	config.GamePublicURL = os.Getenv("GAME_PUBLIC_URL")
	config.Anticheat = game.AnticheatConfig{
		Enabled:         envBool("ANTICHEAT_ENABLED", false), // 默认暂时关闭
		ReachDistance:   envFloat("ANTICHEAT_REACH_DISTANCE", 6.5),
		AttackReach:     envFloat("ANTICHEAT_ATTACK_REACH", 4.5),
		MaxSpeed:        envFloat("ANTICHEAT_MAX_SPEED", 8.5),
		SpeedTolerance:  envFloat("ANTICHEAT_SPEED_TOLERANCE", 1.8),
		MaxTeleportDist: envFloat("ANTICHEAT_MAX_TELEPORT_DIST", 10.0),
		NoclipCheck:     envBool("ANTICHEAT_NOCLIP_ENABLED", false),
		FlyCheck:        envBool("ANTICHEAT_FLY_ENABLED", false),
		MaxAirTicks:     envInt("ANTICHEAT_MAX_AIR_TICKS", 25),
	}
	app, err := game.New(config)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: net.JoinHostPort(env("BACKEND_HOST", "127.0.0.1"), env("BACKEND_PORT", "8081")), Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan os.Signal, 1)
	listenError := make(chan error, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Mcraft Go backend: http://%s (data: %s)", server.Addr, data)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			listenError <- err
		}
	}()
	if env("CONSOLE_ENABLED", "true") != "false" {
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 1024), 4096)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				result, err := app.Console(line)
				if err != nil {
					log.Printf("控制台：%v", err)
				} else {
					log.Printf("控制台：%v", result)
				}
			}
		}()
	}
	exitCode := 0
	select {
	case <-done:
	case <-app.StopRequested():
	case err := <-listenError:
		log.Printf("listen: %v", err)
		exitCode = 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	if err := app.Close(); err != nil {
		log.Printf("shutdown save: %v", err)
		os.Exit(1)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
