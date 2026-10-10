// Package plugins hosts versioned executable plugins. It is deliberately
// independent of game state: responses contain intents applied by the game.
package plugins

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Manifest struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	APIVersion int             `json:"apiVersion"`
	Entry      string          `json:"entry"`
	Args       []string        `json:"args"`
	Enabled    *bool           `json:"enabled"`
	Config     json.RawMessage `json:"config"`
}
type Status struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Version       string             `json:"version"`
	State         string             `json:"state"`
	Error         string             `json:"error,omitempty"`
	Subscriptions []api.Subscription `json:"subscriptions"`
	Commands      []api.Command      `json:"commands"`
}
type Intent struct {
	PluginID string
	Action   api.Action
}
type process struct {
	status    Status
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	responses chan api.Response
	exited    chan struct{}
	sequence  uint64
	timeout   time.Duration
}
type registration struct {
	process *process
	command api.Command
}
type Manager struct {
	processes []*process
	commands  map[string]registration
	specs     map[string]api.EventSpec
	budget    time.Duration
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var permissionNode = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)

func Load(root, dataDir string, timeout time.Duration) *Manager {
	return LoadFiltered(root, dataDir, timeout, nil)
}
func LoadFiltered(root, dataDir string, timeout time.Duration, disabled map[string]bool) *Manager {
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	m := &Manager{commands: map[string]registration{}, specs: map[string]api.EventSpec{}, budget: timeout}
	for _, spec := range api.Events() {
		m.specs[spec.Name] = spec
	}
	if root == "" {
		return m
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return m
	}
	if err != nil {
		m.processes = append(m.processes, &process{status: Status{ID: "loader", State: "failed", Error: err.Error()}})
		return m
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		p := &process{status: Status{ID: entry.Name(), State: "failed"}, timeout: timeout}
		m.processes = append(m.processes, p)
		var manifest Manifest
		if err == nil {
			err = json.Unmarshal(raw, &manifest)
		}
		if err != nil {
			p.status.Error = err.Error()
			continue
		}
		p.status.ID, p.status.Name, p.status.Version = manifest.ID, manifest.Name, manifest.Version
		if disabled[manifest.ID] || manifest.Enabled != nil && !*manifest.Enabled {
			p.status.State = "disabled"
			continue
		}
		if !identifier.MatchString(manifest.ID) || seen[manifest.ID] || manifest.APIVersion != api.APIVersion {
			p.status.Error = "invalid/duplicate id or unsupported apiVersion"
			continue
		}
		seen[manifest.ID] = true
		if manifest.Name == "" || manifest.Version == "" || manifest.Entry == "" || filepath.IsAbs(manifest.Entry) {
			p.status.Error = "name, version and relative entry are required"
			continue
		}
		absolute, _ := filepath.Abs(dir)
		absolute, err = filepath.EvalSymlinks(absolute)
		if err != nil {
			p.status.Error = err.Error()
			continue
		}
		executable, err := filepath.EvalSymlinks(filepath.Join(absolute, manifest.Entry))
		if err != nil {
			p.status.Error = err.Error()
			continue
		}
		relative, err := filepath.Rel(absolute, executable)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			p.status.Error = "entry must be inside the plugin directory"
			continue
		}
		pluginData, _ := filepath.Abs(filepath.Join(dataDir, manifest.ID))
		if err = os.MkdirAll(pluginData, 0700); err != nil {
			p.status.Error = err.Error()
			continue
		}
		p.cmd = exec.Command(executable, manifest.Args...)
		isolatePluginSignals(p.cmd)
		p.cmd.Dir = absolute
		p.cmd.Stderr = &logWriter{id: manifest.ID}
		p.stdin, err = p.cmd.StdinPipe()
		if err != nil {
			p.status.Error = err.Error()
			continue
		}
		stdout, err := p.cmd.StdoutPipe()
		if err != nil {
			p.stdin.Close()
			p.status.Error = err.Error()
			continue
		}
		p.responses = make(chan api.Response, 1)
		p.exited = make(chan struct{})
		if err = p.cmd.Start(); err != nil {
			p.stdin.Close()
			stdout.Close()
			p.status.Error = err.Error()
			continue
		}
		go p.read(stdout)
		p.status.State = "loaded"
		response, err := p.call(api.Request{Method: "initialize", Initialize: &api.Initialize{APIVersion: api.APIVersion, ID: manifest.ID, Version: manifest.Version, DataDir: pluginData, Config: manifest.Config, TickMillis: 100}}, 5*time.Second)
		if err == nil {
			err = m.register(p, response.Metadata)
		}
		if err != nil {
			p.fail(err)
			continue
		}
		log.Printf("plugin %s %s loaded (%d events, %d commands)", manifest.ID, manifest.Version, len(p.status.Subscriptions), len(p.status.Commands))
	}
	return m
}
func (m *Manager) register(p *process, metadata *api.Metadata) error {
	if metadata == nil || metadata.APIVersion != api.APIVersion {
		return errors.New("invalid API handshake")
	}
	if len(metadata.Subscriptions) > 128 || len(metadata.Commands) > 64 {
		return errors.New("registration limit exceeded")
	}
	for i, sub := range metadata.Subscriptions {
		if _, ok := m.specs[sub.Event]; !ok {
			return fmt.Errorf("unknown event %s", sub.Event)
		}
		switch sub.Priority {
		case api.Lowest, api.Low, api.Normal, api.High, api.Highest, api.Monitor:
		default:
			return errors.New("invalid priority")
		}
		if sub.Every < 1 {
			sub.Every = 1
		}
		if sub.Every > 36000 {
			return errors.New("invalid tick interval")
		}
		metadata.Subscriptions[i] = sub
	}
	names := map[string]bool{}
	for i, command := range metadata.Commands {
		if command.Permission == "" {
			command.Permission = "everyone"
		}
		if command.Permission != "everyone" && command.Permission != "owner" && !permissionNode.MatchString(command.Permission) {
			return errors.New("invalid command permission")
		}
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			if !identifier.MatchString(name) || names[name] || name == "help" || name == "spawn" || name == "plugins" || name == "time" || name == "summon" {
				return fmt.Errorf("invalid/reserved command %s", name)
			}
			if _, exists := m.commands[name]; exists {
				return fmt.Errorf("duplicate command %s", name)
			}
			names[name] = true
		}
		metadata.Commands[i] = command
	}
	p.status.Subscriptions = metadata.Subscriptions
	p.status.Commands = metadata.Commands
	for _, command := range metadata.Commands {
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			m.commands[name] = registration{p, command}
		}
	}
	return nil
}
func (p *process) read(stdout io.ReadCloser) {
	defer stdout.Close()
	defer close(p.responses)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 64<<20) // Full bounded portable-inventory snapshots.
readLoop:
	for scanner.Scan() {
		var response api.Response
		if json.Unmarshal(scanner.Bytes(), &response) != nil {
			break
		}
		select {
		case p.responses <- response:
		default:
			break readLoop
		}
	}
	p.cmd.Process.Kill()
	p.cmd.Wait()
	close(p.exited)
}
func (p *process) fail(err error) {
	p.status.State = "failed"
	p.status.Error = err.Error()
	log.Printf("plugin %s failed: %v", p.status.ID, err)
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	if p.stdin != nil {
		p.stdin.Close()
	}
}
func (p *process) call(request api.Request, timeout time.Duration) (api.Response, error) {
	if p.status.State != "loaded" {
		return api.Response{}, fmt.Errorf("plugin unavailable: %s", p.status.Error)
	}
	p.sequence++
	request.ID = p.sequence
	bytes, err := json.Marshal(request)
	if err != nil {
		return api.Response{}, err
	}
	// Writing and reading share one deadline, including a plugin which stops reading stdin.
	result := make(chan error, 1)
	go func() { _, err := p.stdin.Write(append(bytes, '\n')); result <- err }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err = <-result:
		if err != nil {
			return api.Response{}, err
		}
	case <-timer.C:
		return api.Response{}, errors.New("plugin request timed out writing stdin")
	}
	select {
	case response, ok := <-p.responses:
		if !ok {
			return response, errors.New("plugin exited or emitted invalid JSON")
		}
		if response.ID != request.ID {
			return response, errors.New("plugin response id mismatch")
		}
		if response.Error != "" {
			return response, errors.New(response.Error)
		}
		if len(response.Actions) > 128 {
			return response, errors.New("action limit exceeded")
		}
		return response, nil
	case <-timer.C:
		return api.Response{}, errors.New("plugin request timed out")
	}
}
func (m *Manager) refresh() {
	for _, p := range m.processes {
		if p.status.State == "loaded" {
			select {
			case <-p.exited:
				p.fail(errors.New("plugin process exited"))
			default:
			}
		}
	}
}
func (m *Manager) Status() []Status {
	m.refresh()
	out := make([]Status, 0, len(m.processes))
	for _, p := range m.processes {
		out = append(out, p.status)
	}
	return out
}
func (m *Manager) Failed() int {
	m.refresh()
	n := 0
	for _, p := range m.processes {
		if p.status.State == "failed" {
			n++
		}
	}
	return n
}
func (m *Manager) Emit(event *api.Event) []Intent {
	spec, ok := m.specs[event.Name]
	if !ok {
		return nil
	}
	event.Cancellable = spec.Cancellable
	type hook struct {
		p        *process
		priority api.Priority
	}
	var hooks []hook
	for _, p := range m.processes {
		priorities := map[api.Priority]bool{}
		for _, sub := range p.status.Subscriptions {
			if sub.Event == event.Name && (!sub.IgnoreCancelled || !event.Cancelled) && (event.Name != api.ServerTick || event.Tick%uint64(sub.Every) == 0) {
				priorities[sub.Priority] = true
			}
		}
		for priority := range priorities {
			hooks = append(hooks, hook{p, priority})
		}
	}
	sort.SliceStable(hooks, func(i, j int) bool {
		if hooks[i].priority != hooks[j].priority {
			return hooks[i].priority < hooks[j].priority
		}
		return hooks[i].p.status.ID < hooks[j].p.status.ID
	})
	var intents []Intent
	deadline := time.Now().Add(m.budget)
	for _, hook := range hooks {
		p := hook.p
		run := false
		for _, sub := range p.status.Subscriptions {
			if sub.Event == event.Name && sub.Priority == hook.priority && (!sub.IgnoreCancelled || !event.Cancelled) && (event.Name != api.ServerTick || event.Tick%uint64(sub.Every) == 0) {
				run = true
				break
			}
		}
		if !run {
			continue
		}
		remaining := time.Until(deadline)
		var response api.Response
		var err error
		if remaining <= 0 {
			err = errors.New("event dispatch budget exceeded")
		} else {
			response, err = p.call(api.Request{Method: "event", Event: event, Priority: hook.priority}, remaining)
		}
		if err != nil {
			if p.status.State == "loaded" {
				p.fail(err)
			}
			if event.Cancellable && hook.priority != api.Monitor {
				event.Cancelled = true
				event.Reason = "插件 " + p.status.ID + " 不可用，操作已阻止。"
			}
			continue
		}
		if hook.priority == api.Monitor {
			continue
		}
		if event.Cancellable && response.Cancelled {
			event.Cancelled = true
			if response.Reason != "" {
				event.Reason = response.Reason
			}
		}
		if event.Data == nil {
			event.Data = map[string]any{}
		}
		for _, key := range spec.Mutable {
			if value, ok := response.Data[key]; ok {
				switch key {
				case "authenticated", "fields", "fuseTicks":
					// The identity/priming gates validate these typed fields before commit.
					event.Data[key] = value
				case "amount", "count", "ticks", "amplifier", "duration", "damage", "radius":
					if number, ok := value.(float64); ok {
						event.Data[key] = number
					} else if event.Cancellable {
						event.Cancelled = true
						event.Reason = "plugin.invalid_mutation: " + key
					}
				default:
					if str, ok := value.(string); ok {
						event.Data[key] = str
					}
				}
			}
		}
		for _, action := range response.Actions {
			intents = append(intents, Intent{p.status.ID, action})
		}
		if event.Damage != nil {
			event.Damage.Amount = event.Number("amount")
		}
	}
	return intents
}
func (m *Manager) Execute(name string, event *api.Event, args []string) ([]Intent, bool, error) {
	reg, ok := m.commands[name]
	if !ok {
		return nil, false, nil
	}
	if reg.command.Permission == "owner" && (event.Player == nil || !event.Player.Owner && !event.Player.Operator) {
		return nil, true, errors.New("需要 OP 或房主权限。")
	}
	if reg.command.Permission != "everyone" && reg.command.Permission != "owner" {
		allowed := false
		if event.Player != nil {
			for _, node := range event.Player.Permissions {
				if node == "*" || node == reg.command.Permission || strings.HasSuffix(node, ".*") && strings.HasPrefix(reg.command.Permission, strings.TrimSuffix(node, "*")) {
					allowed = true
				}
			}
			for _, node := range event.Player.Permissions {
				if strings.HasPrefix(node, "-") {
					node = node[1:]
					if node == "*" || node == reg.command.Permission || strings.HasSuffix(node, ".*") && strings.HasPrefix(reg.command.Permission, strings.TrimSuffix(node, "*")) {
						allowed = false
						break
					}
				}
			}
		}
		if !allowed {
			return nil, true, errors.New("缺少插件命令权限：" + reg.command.Permission)
		}
	}
	response, err := reg.process.call(api.Request{Method: "command", Event: event, Command: reg.command.Name, Args: args}, m.budget)
	if err != nil {
		if reg.process.status.State == "loaded" {
			reg.process.fail(err)
		}
		return nil, true, err
	}
	var intents []Intent
	for _, action := range response.Actions {
		intents = append(intents, Intent{reg.process.status.ID, action})
	}
	return intents, true, nil
}
func (m *Manager) Close() {
	for _, p := range m.processes {
		if p.status.State == "loaded" {
			_, err := p.call(api.Request{Method: "shutdown"}, p.timeout)
			if err != nil {
				p.fail(err)
			} else {
				p.stdin.Close()
			}
		}
		if p.cmd != nil && p.cmd.Process != nil {
			select {
			case <-p.exited:
			case <-time.After(200 * time.Millisecond):
				p.cmd.Process.Kill()
				<-p.exited
			}
		}
	}
}

// Keep stderr line oriented and bounded even when a plugin logs binary garbage.
type logWriter struct {
	id      string
	mu      sync.Mutex
	pending []byte
}

func (w *logWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(b)
	w.pending = append(w.pending, b...)
	for {
		i := strings.IndexByte(string(w.pending), '\n')
		if i < 0 && len(w.pending) < 4096 {
			break
		}
		if i < 0 {
			i = 4096
		}
		log.Printf("plugin[%s]: %s", w.id, string(w.pending[:i]))
		if i < len(w.pending) {
			i++
		}
		w.pending = w.pending[i:]
	}
	return n, nil
}
