// An optional per-game-server registration plugin, keyed by the core account ID.
package main

import (
	"encoding/json"
	"errors"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
)

func main() {
	p := api.New()
	registered := map[string]bool{}
	var file string
	config := struct {
		InviteCode string `json:"inviteCode"`
	}{InviteCode: "change-me"}
	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			if err := json.Unmarshal(c.Config, &config); err != nil {
				return err
			}
		}
		if config.InviteCode == "" {
			return errors.New("inviteCode is required")
		}
		file = filepath.Join(c.DataDir, "registered.json")
		raw, err := os.ReadFile(file)
		if err == nil {
			return json.Unmarshal(raw, &registered)
		}
		if !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	p.On(api.PlayerAuthenticating, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.Identity == nil {
			e.Cancel("server_auth.identity_required")
			return nil
		}
		id := e.Identity.ID
		if registered[id] {
			e.Data["authenticated"] = true
			return nil
		}
		e.Data["authenticated"] = false
		e.Data["prompt"] = "本服需要邀请码注册；核心玩家 ID 保持不变。"
		e.Data["fields"] = []api.AuthField{{Name: "invite", Label: "本服邀请码", Type: "password"}}
		if e.String("action") == "register" {
			values, _ := e.Data["values"].(map[string]any)
			code, _ := values["invite"].(string)
			if code == config.InviteCode {
				e.Data["authenticated"] = true
			} else {
				e.Cancel("server_auth.invalid_invite")
			}
		}
		return nil
	})
	p.On(api.PlayerAuthenticated, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Identity == nil || e.String("action") != "register" {
			return nil
		}
		registered[e.Identity.ID] = true
		raw, err := json.Marshal(registered)
		if err != nil {
			return err
		}
		if err = os.WriteFile(file+".tmp", raw, 0600); err != nil {
			return err
		}
		return os.Rename(file+".tmp", file)
	})
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
