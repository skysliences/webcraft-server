package main

import (
	"context"
	"log"
	"mcraft/backend/internal/game"
	"mcraft/backend/internal/identity"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
func listEnv(k string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(k), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func main() {
	root := env("PROJECT_ROOT", "..")
	dir := env("IDENTITY_DATA_DIR", filepath.Join(root, "data", "account-core"))
	store, err := game.OpenIdentityStore(dir, env("IDENTITY_IMPORT_FILE", ""))
	if err != nil {
		log.Fatal(err)
	}
	issuer := env("IDENTITY_PUBLIC_URL", "https://login.ccraft.app")
	clients := []identity.Client{}
	if id := env("IDENTITY_BOOTSTRAP_SERVER_ID", ""); id != "" {
		clients = append(clients, identity.Client{ID: id, Name: env("IDENTITY_BOOTSTRAP_SERVER_NAME", "本地服务器"), FrontendURL: env("GAME_PUBLIC_URL", "http://127.0.0.1:8080"), CallbackURL: env("AUTH_CALLBACK_URL", "http://127.0.0.1:8081/api/auth/callback")})
	}
	app, err := identity.New(identity.Config{DataDir: dir, Issuer: issuer, PluginDir: env("IDENTITY_PLUGIN_DIR", filepath.Join(root, "account-plugins")), SecureCookies: env("AUTH_COOKIE_SECURE", "false") == "true", AllowLegacyCookie: env("IDENTITY_ALLOW_LEGACY_COOKIE", "false") == "true", Clients: clients, AdminUsers: listEnv("IDENTITY_ADMIN_USERS"), TrustedProxies: listEnv("IDENTITY_TRUSTED_PROXIES"), GameHomeURL: env("IDENTITY_GAME_HOME_URL", "")}, store)
	if err != nil {
		store.Close()
		log.Fatal(err)
	}
	server := &http.Server{Addr: net.JoinHostPort(env("IDENTITY_HOST", "127.0.0.1"), env("IDENTITY_PORT", "8082")), Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	failed := make(chan error, 1)
	go func() {
		log.Printf("Mcraft core account service: %s (data: %s)", issuer, dir)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			failed <- err
		}
	}()
	select {
	case <-stop:
	case err := <-failed:
		log.Printf("identity listen: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	if err = app.Close(); err != nil {
		log.Fatal(err)
	}
}
