package main

import (
	"bytes"
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"testing"
)

func runPlugin(t *testing.T, requests ...api.Request) []api.Response {
	t.Helper()
	var input, output bytes.Buffer
	enc := json.NewEncoder(&input)
	enc.Encode(api.Request{ID: 1, Method: "initialize", Initialize: &api.Initialize{APIVersion: api.APIVersion, DataDir: t.TempDir()}})
	for i, req := range requests {
		req.ID = uint64(i + 2)
		enc.Encode(req)
	}
	if err := newPlugin().Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&output)
	var init api.Response
	dec.Decode(&init)
	if init.Error != "" {
		t.Fatal(init.Error)
	}
	var responses []api.Response
	for range requests {
		var res api.Response
		if err := dec.Decode(&res); err != nil {
			t.Fatal(err)
		}
		if res.Error != "" {
			t.Fatal(res.Error)
		}
		responses = append(responses, res)
	}
	return responses
}
func TestLoreLifestealUsesActualDamageAndCustomRate(t *testing.T) {
	v := &api.ItemStack{ID: 276, Count: 1, Meta: &api.ItemMeta{Lore: []string{"[吸血]"}, CustomData: map[string]any{"demo:lifesteal": .25}}}
	e := &api.Event{Name: api.PlayerAttacked, World: &api.World{ID: "world"}, Player: &api.Player{ID: 3}, Damage: &api.DamageContext{Weapon: v, Amount: 30, AppliedAmount: 4}}
	request := api.Request{Method: "event", Event: e, Priority: api.Normal}
	r := runPlugin(t, request)[0]
	if len(r.Actions) != 1 || r.Actions[0].Type != "heal" || r.Actions[0].Amount != 1 || r.Actions[0].PlayerID != 3 {
		t.Fatal("lifesteal should use actual damage", r)
	}
	v.Meta.Lore = nil
	if len(runPlugin(t, request)[0].Actions) != 0 {
		t.Fatal("removing Lore did not disable lifesteal")
	}
	v.Meta.Lore = []string{"[吸血]"}
	v.Meta.CustomData["demo:lifesteal"] = .5
	if runPlugin(t, request)[0].Actions[0].Amount != 2 {
		t.Fatal("custom rate modification ignored")
	}
	e.Damage.AppliedAmount = 0
	if len(runPlugin(t, request)[0].Actions) != 0 {
		t.Fatal("cancelled/zero damage healed")
	}
}
func TestItemDemoAndEquipmentCustomProtection(t *testing.T) {
	e := &api.Event{World: &api.World{ID: "world", Mode: "survival"}, Player: &api.Player{ID: 3, Owner: true}}
	r := runPlugin(t, api.Request{Method: "command", Command: "itemdemo", Event: e})[0]
	if len(r.Actions) < 2 || r.Actions[0].Stack.Meta.DisplayName != "赤月·吸血剑" || r.Actions[1].Stack.Meta.Attributes[0].Attribute != api.MaxHealth {
		t.Fatal("demo stacks incomplete", r)
	}
	e.Name = api.PlayerDamaging
	e.Cancellable = true
	e.Damage = &api.DamageContext{Cause: "lava"}
	e.Player.Items = make([]*api.ItemStack, 46)
	e.Player.Items[38] = r.Actions[1].Stack
	if !runPlugin(t, api.Request{Method: "event", Priority: api.High, Event: e})[0].Cancelled {
		t.Fatal("equipped protection did not cancel lava")
	}
	e.Player.Items[0] = e.Player.Items[38]
	e.Player.Items[38] = nil
	if runPlugin(t, api.Request{Method: "event", Priority: api.High, Event: e})[0].Cancelled {
		t.Fatal("unequipped item activated armor effect")
	}
}
