package plugin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
)

type Context struct {
	blocks  []BlockDefinition
	items   []ItemDefinition
	Actions []Action
	DataDir string
	Config  json.RawMessage
}

func (c *Context) Message(e *Event, text string) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "message", WorldID: e.World.ID, PlayerID: e.Player.ID, Message: text})
	}
}
func (c *Context) Broadcast(world, text string) {
	c.Actions = append(c.Actions, Action{Type: "broadcast", WorldID: world, Message: text})
}
func (c *Context) GiveItem(e *Event, name string, count int) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "give_item", WorldID: e.World.ID, PlayerID: e.Player.ID, ItemName: name, Count: count})
	}
}
func (c *Context) Teleport(e *Event, x, y, z float64) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "teleport", WorldID: e.World.ID, PlayerID: e.Player.ID, X: x, Y: y, Z: z})
	}
}
func (c *Context) SetBlock(world string, x, y, z, block int) {
	c.Actions = append(c.Actions, Action{Type: "set_block", WorldID: world, X: float64(x), Y: float64(y), Z: float64(z), Block: block})
}

// SetBlocks queues a batch of block placements in a single action, drastically reducing IPC overhead.
func (c *Context) SetBlocks(world string, changes []BlockChange) {
	if len(changes) == 0 {
		return
	}
	c.Actions = append(c.Actions, Action{Type: "set_blocks", WorldID: world, Blocks: changes})
}

// QueryBlocks requests a read-only 3D cuboid block snapshot from the server REST API.
func QueryBlocks(apiURL, worldID string, minX, minY, minZ, maxX, maxY, maxZ int) (*WorldSnapshot, error) {
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	if minZ > maxZ {
		minZ, maxZ = maxZ, minZ
	}
	url := fmt.Sprintf("%s/api/rooms/%s/blocks?minX=%d&maxX=%d&minY=%d&maxY=%d&minZ=%d&maxZ=%d",
		strings.TrimRight(apiURL, "/"), worldID, minX, maxX, minY, maxY, minZ, maxZ)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query blocks failed with status %d", resp.StatusCode)
	}
	var snap WorldSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (c *Context) SpawnMob(world, kind string, x, y, z float64) {
	c.Actions = append(c.Actions, Action{Type: "spawn_mob", WorldID: world, EntityType: kind, X: x, Y: y, Z: z})
}
func (c *Context) SetTime(world string, ticks uint64) {
	c.Actions = append(c.Actions, Action{Type: "set_time", WorldID: world, Ticks: ticks})
}

// DropItem creates a persistent ground item in a survival world.
func (c *Context) DropItem(world, name string, count int, x, y, z float64) {
	c.Actions = append(c.Actions, Action{Type: "drop_item", WorldID: world, ItemName: name, Count: count, X: x, Y: y, Z: z})
}
func (c *Context) Heal(e *Event, amount float64) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "heal", WorldID: e.World.ID, PlayerID: e.Player.ID, Amount: amount})
	}
}

// OpenCustomUI 向指定世界中的玩家推送自定义表单/弹窗界面
func (c *Context) OpenCustomUI(worldID string, playerID uint16, ui CustomUIDefinition) {
	c.Actions = append(c.Actions, Action{
		Type:     "custom_ui",
		WorldID:  worldID,
		PlayerID: playerID,
		CustomUI: &ui,
	})
}

// OpenCustomUIFor 便捷方法：向事件触发者玩家推送自定义弹窗
func (c *Context) OpenCustomUIFor(e *Event, ui CustomUIDefinition) {
	if e != nil && e.World != nil && e.Player != nil {
		c.OpenCustomUI(e.World.ID, e.Player.ID, ui)
	}
}

// CloseCustomUI 关闭指定玩家当前的自定义界面
func (c *Context) CloseCustomUI(worldID string, playerID uint16, uiID string) {
	c.Actions = append(c.Actions, Action{
		Type:     "close_custom_ui",
		WorldID:  worldID,
		PlayerID: playerID,
		Reason:   uiID,
	})
}

// CloseCustomUIFor 便捷方法：关闭事件触发者玩家的自定义界面
func (c *Context) CloseCustomUIFor(e *Event, uiID string) {
	if e != nil && e.World != nil && e.Player != nil {
		c.CloseCustomUI(e.World.ID, e.Player.ID, uiID)
	}
}

// UpdateSign 直接修改或覆写指定世界坐标的告示牌文本内容
func (c *Context) UpdateSign(worldID string, x, y, z int, lines []string) {
	c.Actions = append(c.Actions, Action{
		Type:    "write_sign",
		WorldID: worldID,
		X:       float64(x),
		Y:       float64(y),
		Z:       float64(z),
		Lines:   lines,
	})
}

// OpenContainer 命令服务端为玩家打开指定坐标处的容器窗口
func (c *Context) OpenContainer(worldID string, playerID uint16, x, y, z int) {
	c.Actions = append(c.Actions, Action{
		Type:     "open_container",
		WorldID:  worldID,
		PlayerID: playerID,
		X:        float64(x),
		Y:        float64(y),
		Z:        float64(z),
	})
}

// SetPlayerNametag 向指定玩家设置头顶名称前缀、后缀及图片称号
func (c *Context) SetPlayerNametag(worldID string, playerID uint16, prefix, suffix, title, titleIcon string) {
	c.Actions = append(c.Actions, Action{
		Type:     "set_nametag",
		WorldID:  worldID,
		PlayerID: playerID,
		Nametag: &NametagAction{
			Prefix:    prefix,
			Suffix:    suffix,
			Title:     title,
			TitleIcon: titleIcon,
		},
	})
}

// SetPlayerNametagFor 便捷方法：为当前事件触发者玩家设置头顶名牌与称号
func (c *Context) SetPlayerNametagFor(e *Event, prefix, suffix, title, titleIcon string) {
	if e != nil && e.World != nil && e.Player != nil {
		c.SetPlayerNametag(e.World.ID, e.Player.ID, prefix, suffix, title, titleIcon)
	}
}

// SetPlayerNametagFull 完整配置玩家头顶名牌各属性（前缀、后缀、显示名、颜色、称号、图片、动画）
func (c *Context) SetPlayerNametagFull(worldID string, playerID uint16, info NametagAction) {
	c.Actions = append(c.Actions, Action{
		Type:     "set_nametag",
		WorldID:  worldID,
		PlayerID: playerID,
		Nametag:  &info,
	})
}

// Withdraw 从事件触发者玩家的经济账户扣除金币
func (c *Context) Withdraw(e *Event, amount float64, reason string) {
	if e != nil && e.Player != nil && e.World != nil && amount > 0 {
		c.Actions = append(c.Actions, Action{
			Type:      "economy_withdraw",
			WorldID:   e.World.ID,
			PlayerID:  e.Player.ID,
			AccountID: e.Player.AccountID,
			Amount:    amount,
			Reason:    reason,
		})
	}
}

// Deposit 向事件触发者玩家的经济账户存入金币
func (c *Context) Deposit(e *Event, amount float64, reason string) {
	if e != nil && e.Player != nil && e.World != nil && amount > 0 {
		c.Actions = append(c.Actions, Action{
			Type:      "economy_deposit",
			WorldID:   e.World.ID,
			PlayerID:  e.Player.ID,
			AccountID: e.Player.AccountID,
			Amount:    amount,
			Reason:    reason,
		})
	}
}

type Handler func(*Context, *Event) error
type CommandHandler func(*Context, *Event, []string) error
type listener struct {
	subscription Subscription
	handler      Handler
	removed      bool
}
type commandHandler struct {
	command Command
	handler CommandHandler
}
type Plugin struct {
	mu        sync.Mutex
	listeners []*listener
	commands  map[string]commandHandler
	OnEnable  func(*Context) error
	OnDisable func(*Context) error
	dataDir   string
	config    json.RawMessage
}

func New() *Plugin { return &Plugin{commands: map[string]commandHandler{}} }
func (p *Plugin) On(name string, options Subscription, handler Handler) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	options.Event = name
	if options.Every < 1 {
		options.Every = 1
	}
	l := &listener{subscription: options, handler: handler}
	p.listeners = append(p.listeners, l)
	return func() { p.mu.Lock(); l.removed = true; p.mu.Unlock() }
}
func (p *Plugin) Every(ticks int, handler Handler) func() {
	return p.On(ServerTick, Subscription{Every: ticks}, handler)
}
func (p *Plugin) Command(command Command, handler CommandHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands[command.Name] = commandHandler{command, handler}
}
func (p *Plugin) metadata() *Metadata {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := &Metadata{APIVersion: APIVersion, Subscriptions: []Subscription{}, Commands: []Command{}}
	seen := map[Subscription]bool{}
	for _, l := range p.listeners {
		if !l.removed && !seen[l.subscription] {
			seen[l.subscription] = true
			m.Subscriptions = append(m.Subscriptions, l.subscription)
		}
	}
	for _, c := range p.commands {
		m.Commands = append(m.Commands, c.command)
	}
	sort.Slice(m.Commands, func(i, j int) bool { return m.Commands[i].Name < m.Commands[j].Name })
	return m
}
func (p *Plugin) handle(req Request) (response Response) {
	response.ID = req.ID
	defer func() {
		if recovered := recover(); recovered != nil {
			response = Response{ID: req.ID, Error: fmt.Sprintf("plugin panic: %v", recovered)}
		}
	}()
	ctx := &Context{DataDir: p.dataDir, Config: p.config}
	var err error
	switch req.Method {
	case "initialize":
		if req.Initialize == nil || req.Initialize.APIVersion != APIVersion {
			response.Error = "unsupported API version"
			return
		}
		p.dataDir = req.Initialize.DataDir
		p.config = req.Initialize.Config
		ctx.DataDir = p.dataDir
		ctx.Config = p.config
		if p.OnEnable != nil {
			err = p.OnEnable(ctx)
		}
		if err == nil {
			response.Metadata = p.metadata()
			response.Metadata.Blocks = ctx.blocks
			response.Metadata.Items = ctx.items
		}
	case "event":
		if req.Event == nil {
			response.Error = "missing event"
			return
		}
		p.mu.Lock()
		var handlers []*listener
		for _, l := range p.listeners {
			if !l.removed && l.subscription.Event == req.Event.Name && l.subscription.Priority == req.Priority && (!l.subscription.IgnoreCancelled || !req.Event.Cancelled) && (req.Event.Name != ServerTick || req.Event.Tick%uint64(l.subscription.Every) == 0) {
				handlers = append(handlers, l)
			}
		}
		p.mu.Unlock()
		for _, listener := range handlers {
			p.mu.Lock()
			removed := listener.removed
			p.mu.Unlock()
			if removed || listener.subscription.IgnoreCancelled && req.Event.Cancelled {
				continue
			}
			cancelled, reason := req.Event.Cancelled, req.Event.Reason
			err = listener.handler(ctx, req.Event)
			if cancelled {
				req.Event.Cancelled = true
				if req.Event.Reason == "" {
					req.Event.Reason = reason
				}
			}
			if err != nil {
				break
			}
		}
		response.Cancelled = req.Event.Cancelled
		response.Reason = req.Event.Reason
		response.Data = req.Event.Data
	case "command":
		if req.Event == nil {
			response.Error = "missing command event"
			return
		}
		p.mu.Lock()
		command, ok := p.commands[req.Command]
		p.mu.Unlock()
		if !ok {
			err = fmt.Errorf("unknown command %s", req.Command)
		} else {
			err = command.handler(ctx, req.Event, req.Args)
		}
	case "shutdown":
		if p.OnDisable != nil {
			err = p.OnDisable(ctx)
		}
	default:
		err = fmt.Errorf("unknown method %s", req.Method)
	}
	if err != nil {
		response.Error = err.Error()
		return
	}
	response.Actions = ctx.Actions
	return
}

// Serve reserves stdout for newline-delimited JSON. Log to stderr. Handlers run
// sequentially; the host applies actions after completing the game operation.
func (p *Plugin) Serve(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64<<20) // Full bounded portable-inventory snapshots.
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return err
		}
		response := p.handle(req)
		if err := encoder.Encode(response); err != nil {
			return err
		}
		if req.Method == "shutdown" {
			return nil
		}
	}
	return scanner.Err()
}
func (p *Plugin) Run() error { return p.Serve(os.Stdin, os.Stdout) }
