// Optional core-account policy. Install in the account service's plugin folder.
package main

import (
	"encoding/json"
	"errors"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"regexp"
)

func main() {
	p := api.New()
	protected := map[string]bool{}
	fixedID := regexp.MustCompile(`^[A-Za-z0-9]{12,40}$`)
	p.OnEnable = func(c *api.Context) error {
		var config struct {
			ProtectedAccountIDs []string `json:"protectedAccountIds"`
		}
		if len(c.Config) > 0 && string(c.Config) != "null" {
			if err := json.Unmarshal(c.Config, &config); err != nil {
				return err
			}
		}
		for _, id := range config.ProtectedAccountIDs {
			if !fixedID.MatchString(id) {
				return errors.New("invalid protected fixed account ID")
			}
			protected[id] = true
		}
		return nil
	}
	for _, name := range []string{api.AccountAdminStatusChanging, api.AccountAdminSessionsRevoking} {
		p.On(name, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
			if !protected[e.String("target")] {
				return nil
			}
			if e.Name == api.AccountAdminStatusChanging {
				disabled, _ := e.Data["disabled"].(bool)
				if !disabled {
					return nil
				}
			}
			e.Cancel("account_policy.protected_identity")
			return nil
		})
	}
	for _, name := range []string{api.AccountAdminStatusChanged, api.AccountAdminSessionsRevoked, api.AccountPresenceUpdated, api.AccountPresenceOffline, api.AccountServerAuthorized, api.AccountServerAuthorizationConsumed} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			// Keep credential-free activity records. Account DTOs can contain private
			// registration IPs; this example deliberately does not copy those fields.
			record := map[string]any{"event": e.Name, "time": e.Time, "actor": e.String("actor"), "target": e.String("target"), "reason": e.String("reason")}
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "activity.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			f.Close()
			return err
		})
	}
	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
