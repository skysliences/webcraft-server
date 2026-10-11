package plugin

// Registry IDs share one inventory namespace. Existing IDs never move; zero
// requests a persistent allocation. Register during OnEnable, before players join.
type BlockDefinition struct {
	Key        string  `json:"key"`
	ID         int     `json:"id,omitempty"`
	Name       string  `json:"name"`
	ModelBlock int     `json:"modelBlock"`
	Hardness   float64 `json:"hardness"`
	Stack      int     `json:"stack,omitempty"`
}
type ItemDefinition struct {
	Key        string `json:"key"`
	ID         int    `json:"id,omitempty"`
	Name       string `json:"name"`
	ModelItem  int    `json:"modelItem"`
	Stack      int    `json:"stack,omitempty"`
	Durability int    `json:"durability,omitempty"`
	PlacesKey  string `json:"placesKey,omitempty"`
}
type RegistryEntry struct {
	PluginID string           `json:"pluginId"`
	Block    *BlockDefinition `json:"block,omitempty"`
	Item     *ItemDefinition  `json:"item,omitempty"`
}
type RegistrySnapshot struct {
	Version int             `json:"version"`
	Entries []RegistryEntry `json:"entries"`
}

const (
	RegistryRegistering = "registry.register.before"
	RegistryRegistered  = "registry.registered"
	RegistryRejected    = "registry.register.rejected"
	RegistrySyncing     = "registry.sync.before"
	RegistrySynced      = "registry.synced"
)

func (c *Context) RegisterBlock(d BlockDefinition) { c.blocks = append(c.blocks, d) }
func (c *Context) RegisterItem(d ItemDefinition)   { c.items = append(c.items, d) }
func (c *Context) SetBlockKey(world string, x, y, z int, key string) {
	c.Actions = append(c.Actions, Action{Type: "set_block", WorldID: world, X: float64(x), Y: float64(y), Z: float64(z), BlockKey: key})
}
func registryEvents() []EventSpec {
	fields := []string{"reason", "actor", "target", "entry", "registryVersion"}
	return []EventSpec{
		{Name: RegistryRegistering, Cancellable: true, Fields: fields},
		{Name: RegistryRegistered, Fields: fields}, {Name: RegistryRejected, Fields: append(append([]string(nil), fields...), "error")},
		{Name: RegistrySyncing, Cancellable: true, Fields: fields}, {Name: RegistrySynced, Fields: fields},
	}
}
