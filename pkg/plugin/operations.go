package plugin

// SetOperator uses the same cancellable and persisted path as op/deop.
func (c *Context) SetOperator(accountID string, enabled bool) {
	c.Actions = append(c.Actions, Action{Type: "set_operator", AccountID: accountID, Value: enabled})
}

// Actions are queued after the listener finishes and re-enter the server's
// validation and before/after events. They never mutate event snapshots.
func (c *Context) GiveExperience(e *Event, points int) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "give_xp", WorldID: e.World.ID, PlayerID: e.Player.ID, Amount: float64(points)})
	}
}
func (c *Context) ApplyEffect(e *Event, name string, ticks, amplifier int) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "apply_effect", WorldID: e.World.ID, PlayerID: e.Player.ID, Effect: name, Duration: ticks, Amplifier: amplifier})
	}
}
func (c *Context) ClearEffects(e *Event) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "clear_effects", WorldID: e.World.ID, PlayerID: e.Player.ID})
	}
}
func (c *Context) SetGameMode(e *Event, mode string) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "set_gamemode", WorldID: e.World.ID, PlayerID: e.Player.ID, GameMode: mode})
	}
}
func (c *Context) SetRule(world, rule string, value bool) {
	c.Actions = append(c.Actions, Action{Type: "set_rule", WorldID: world, Rule: rule, Value: value})
}
func (c *Context) SetDifficulty(world, difficulty string) {
	c.Actions = append(c.Actions, Action{Type: "set_difficulty", WorldID: world, Difficulty: difficulty})
}
func (c *Context) SetWeather(world string, rain, thunder bool, duration int) {
	c.Actions = append(c.Actions, Action{Type: "set_weather", WorldID: world, Rain: rain, Thunder: thunder, Duration: duration})
}
