package game

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"math"
	"math/rand"
	"mcraft/backend/internal/plugins"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	IdentityIssuer        string
	IdentityServerID      string
	IdentityPublicKey     string
	IdentityCallback      string
	GamePublicURL         string
	IdentityCheckInterval time.Duration
	DataDir               string
	EngineFile            string
	AllowedOrigins        []string
	MaxPlayers            int
	PluginDir             string
	PluginTimeout         time.Duration
	SecureCookies         bool
	InfiniteWorlds        bool
	SingleWorld           bool
	WorldID               string
	WorldName             string
	WorldMode             string
	Anticheat             AnticheatConfig
	MobSpawning           *api.MobSpawningConfig
}
type Server struct {
	coreRules            *Rules
	registry             api.RegistrySnapshot
	identity             *identityClient
	identityDone         chan struct{}
	pendingVillageSpawns []villageSpawn
	mu                   sync.Mutex
	chunkPreparer        *chunkPreparer
	config               Config
	anticheatMu          sync.RWMutex
	anticheat            AnticheatConfig
	rooms                map[string]*Room
	engine               *Engine
	rules                *Rules
	nextID               uint16
	rng                  *rand.Rand
	started              time.Time
	stop                 chan struct{}
	stopRequested        chan struct{}
	done                 chan struct{}
	once                 sync.Once
	plugins              *plugins.Manager
	actions              []plugins.Intent
	eventID              uint64
	tick                 uint64
	pluginSource         string
	closing              bool
	accounts             *accounts
	ops                  *operations
	unlockData           func()
	worldBinding         worldBinding
	worldBindingPrevious string
	worldBindingNew      bool
	closeError           error
	testEventHook        func(*api.Event)
	economyDB            *EconomyDB
	shop                 *ShopManager
	residence            *ResidenceManager
}

func New(config Config) (*Server, error) {
	mobConfig := DefaultMobSpawningConfig()
	if config.MobSpawning != nil {
		mobConfig = *config.MobSpawning
	}
	if err := validateMobSpawningConfig(mobConfig); err != nil {
		return nil, err
	}
	config.MobSpawning = &mobConfig
	unlock, err := lockData(config.DataDir)
	if err != nil {
		return nil, err
	}
	var s *Server
	ready := false
	defer func() {
		if !ready {
			if s != nil && s.economyDB != nil {
				_ = s.economyDB.Close()
			}
			unlock()
		}
	}()
	if config.MaxPlayers < 1 {
		config.MaxPlayers = 64
	}
	rules, err := loadRules()
	if err != nil {
		return nil, err
	}
	engine, err := newEngine(config.EngineFile)
	if err != nil {
		return nil, err
	}
	s = &Server{config: config, rooms: map[string]*Room{}, engine: engine, rules: rules, nextID: 1, rng: rand.New(rand.NewSource(time.Now().UnixNano())), started: time.Now(), stop: make(chan struct{}), stopRequested: make(chan struct{}, 1), done: make(chan struct{})}
	s.coreRules = rules
	s.initAnticheat()
	engine.server = s
	econDB, err := initEconomyDB(config.DataDir)
	if err != nil {
		engine.runtime.Close(context.Background())
		return nil, fmt.Errorf("open economy db: %w", err)
	}
	s.economyDB = econDB
	s.residence = initResidenceManager(s, config.DataDir)
	if err = s.loadRegistry(); err != nil {
		engine.runtime.Close(context.Background())
		return nil, err
	}
	s.unlockData = unlock
	if err = s.loadRooms(); err != nil {
		engine.runtime.Close(context.Background())
		return nil, err
	}
	legacy := map[string]bool{}
	for _, room := range s.rooms {
		for sid := range room.Players {
			legacy[sid] = true
		}
	}
	if config.IdentityIssuer != "" {
		s.identity, err = newIdentityClient(config)
		if err == nil {
			s.accounts, err = newFederatedAccounts(config.DataDir, s.identity)
		}
	} else {
		s.accounts, err = newAccounts(config.DataDir, legacy)
	}
	if err != nil {
		engine.runtime.Close(context.Background())
		return nil, err
	}
	if !config.SingleWorld {
		for _, r := range s.rooms {
			if err = s.saveRoom(r); err != nil {
				engine.runtime.Close(context.Background())
				return nil, err
			}
		}
	}
	if err = s.initOperations(); err != nil {
		engine.runtime.Close(context.Background())
		return nil, err
	}
	s.plugins = plugins.LoadFiltered(config.PluginDir, filepath.Join(config.DataDir, "plugins"), config.PluginTimeout, s.ops.Settings.DisabledPlugins)
	if err = s.registerPluginDefinitions(); err != nil {
		s.plugins.Close()
		engine.runtime.Close(context.Background())
		return nil, err
	}
	if config.SingleWorld {
		if err = s.commitWorldBinding("server_start", "server"); err != nil {
			s.actions = nil
			s.plugins.Close()
			engine.runtime.Close(context.Background())
			return nil, err
		}
	}
	for _, r := range s.rooms {
		s.emit(api.WorldLoaded, r, nil, nil)
	}
	s.emit(api.ServerStarted, nil, nil, map[string]any{"apiVersion": api.APIVersion, "tickMillis": 100})
	s.drainActions()
	s.startChunkPreparation()
	if s.identity != nil {
		s.identityDone = make(chan struct{})
	}
	go s.loop()
	if s.identity != nil {
		go s.refreshIdentities()
	}
	ready = true
	return s, nil
}
func (s *Server) Close() error {
	s.once.Do(func() { close(s.stop) })
	<-s.done
	s.chunkPreparer.close()
	if s.identityDone != nil {
		<-s.identityDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return s.closeError
	}
	defer func() {
		if s.unlockData != nil {
			s.unlockData()
			s.unlockData = nil
		}
	}()
	var result error
	s.closing = true
	s.actions = nil
	s.emit(api.ServerStopping, nil, nil, nil)
	s.drainActions()
	for _, r := range s.allRooms() {
		for p := range r.Peers {
			s.closeWindow(p)
			p.closed = true
			if p.conn != nil {
				p.conn.Close()
			}
			s.disconnect(p)
		}
		if err := s.saveRoom(r); err != nil {
			result = errors.Join(result, err)
		}
	}
	s.drainActions()
	s.plugins.Close()
	if s.economyDB != nil {
		result = errors.Join(result, s.economyDB.Close())
	}
	result = errors.Join(result, s.engine.runtime.Close(context.Background()))
	s.closeError = result
	return result
}
func (s *Server) online() int {
	count := 0
	for _, r := range s.allRooms() {
		count += len(r.Peers)
	}
	return count
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func (s *Server) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	if s.identity != nil && origin == s.identity.frontend {
		return true
	}
	for _, v := range s.config.AllowedOrigins {
		if origin == v {
			return true
		}
	}
	return false
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.accountRoutes(mux)
	s.operationsRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		pending, failed := 0, 0
		for _, room := range s.rooms {
			if room.Dirty {
				pending++
			}
			if room.SaveError != "" {
				failed++
			}
		}
		status := 200
		pluginFailures := s.plugins.Failed()
		if failed > 0 || pluginFailures > 0 {
			status = 503
		}
		writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[failed == 0 && pluginFailures == 0], "service": "mcraft-go", "worlds": len(s.rooms), "online": s.online(), "pendingSaves": pending, "failedSaves": failed, "uptime": int(time.Since(s.started).Seconds()), "failedPlugins": pluginFailures, "plugins": len(s.plugins.Status())})
	})
	mux.HandleFunc("GET /api/registry", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s.registrySnapshot())
	})
	mux.HandleFunc("GET /api/plugins", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"apiVersion": api.APIVersion, "plugins": s.plugins.Status()})
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"apiVersion": api.APIVersion, "events": api.Events()})
	})
	mux.HandleFunc("GET /api/online", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]int{"online": s.online()})
	})
	mux.HandleFunc("GET /api/server", s.serverDescriptor)
	mux.HandleFunc("GET /api/servers", s.listRooms)
	mux.HandleFunc("POST /api/rooms", s.createRoom)
	mux.HandleFunc("GET /api/rooms/{id}", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		r := s.rooms[req.PathValue("id")]
		if r == nil {
			failure(w, 404, "世界不存在。")
			return
		}
		writeJSON(w, 200, r.info())
	})
	mux.HandleFunc("GET /api/rooms/{id}/stats", func(w http.ResponseWriter, req *http.Request) {
		user := s.requireAccount(w, req, true)
		if user == nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		r := s.rooms[req.PathValue("id")]
		if r == nil {
			failure(w, 404, "世界不存在。")
			return
		}
		if !accountOwns(r, user.ID, req.Header.Get("X-Owner-Key")) {
			failure(w, 403, "需要房主权限。")
			return
		}
		writeJSON(w, 200, map[string]any{"blocks": len(r.Edits), "players": len(r.Players), "events": map[string]any{}, "posts": []any{}})
	})
	mux.HandleFunc("GET /api/rooms/{id}/blocks", s.getRoomBlocks)
	mux.HandleFunc("GET /ws/{id}", s.upgrade)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { failure(w, 404, "接口不存在。") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if r.URL.Path == "/api/server" && (r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			if r.Method == "OPTIONS" {
				w.WriteHeader(204)
				return
			}
			mux.ServeHTTP(w, r)
			return
		}
		if !s.originAllowed(origin) {
			failure(w, 403, "不允许此来源访问后端。")
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Owner-Key, X-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) listRooms(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q")))
	show := req.URL.Query().Get("show")
	mine := map[string]bool{}
	for _, id := range strings.Split(req.URL.Query().Get("mine"), ",") {
		mine[id] = true
	}
	list := []*Room{}
	for _, r := range s.rooms {
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) && !strings.Contains(r.ID, q) {
			continue
		}
		if show == "players" && len(r.Peers) == 0 || show == "open" && r.Lock != 0 || show == "mine" && !mine[r.ID] || show == "verified" {
			continue
		}
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch req.URL.Query().Get("sort") {
		case "new":
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt > b.CreatedAt
			}
		case "built":
			if len(a.Edits) != len(b.Edits) {
				return len(a.Edits) > len(b.Edits)
			}
		case "players":
			if len(a.Peers) != len(b.Peers) {
				return len(a.Peers) > len(b.Peers)
			}
		}
		if a.Active != b.Active {
			return a.Active > b.Active
		}
		return a.ID < b.ID
	})
	worlds := []any{}
	for _, r := range list {
		worlds = append(worlds, r.info())
	}
	writeJSON(w, 200, map[string]any{"online": s.online(), "worlds": worlds, "sponsors": []any{}})
}
func (s *Server) getRoomBlocks(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[req.PathValue("id")]
	if r == nil {
		for _, room := range s.allRooms() {
			if room.ID == req.PathValue("id") {
				r = room
				break
			}
		}
	}
	if r == nil {
		failure(w, 404, "世界不存在。")
		return
	}

	q := req.URL.Query()
	parseInt := func(key, alt string, def int) int {
		if v := q.Get(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		if v := q.Get(alt); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		return def
	}

	minX := parseInt("minX", "x1", 0)
	maxX := parseInt("maxX", "x2", 0)
	minY := parseInt("minY", "y1", 0)
	maxY := parseInt("maxY", "y2", 0)
	minZ := parseInt("minZ", "z1", 0)
	maxZ := parseInt("maxZ", "z2", 0)

	if minX > maxX {
		minX, maxX = maxX, minX
	}
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	if minZ > maxZ {
		minZ, maxZ = maxZ, minZ
	}

	minY = max(0, minY)
	maxY = min(127, maxY)

	vol := (maxX - minX + 1) * (maxY - minY + 1) * (maxZ - minZ + 1)
	if vol <= 0 || vol > 4096 {
		failure(w, 400, "查询区域体积必须在 1 到 4096 个方块以内。")
		return
	}

	var blocks []api.BlockSnapshot
	for y := minY; y <= maxY; y++ {
		for z := minZ; z <= maxZ; z++ {
			for x := minX; x <= maxX; x++ {
				if r.inside(x, z) {
					b := s.block(r, x, y, z)
					if b > 0 {
						blocks = append(blocks, api.BlockSnapshot{
							X:     x,
							Y:     y,
							Z:     z,
							Block: b,
							Name:  s.nameOf(b),
						})
					}
				}
			}
		}
	}

	writeJSON(w, 200, api.WorldSnapshot{
		WorldID:   r.ID,
		Dimension: r.dimName(),
		Time:      r.root().Time,
		Count:     len(blocks),
		Blocks:    blocks,
	})
}
func (s *Server) createRoom(w http.ResponseWriter, req *http.Request) {
	if s.config.SingleWorld {
		failure(w, http.StatusConflict, "每台服务器只能运行一个主世界。创建新世界请使用独立的服务器数据目录。")
		return
	}
	user := s.requireAccount(w, req, true)
	if user == nil {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Mode     string `json:"mode"`
		Lock     int    `json:"lock"`
		Hardcore bool   `json:"hardcore"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		failure(w, 400, "请求格式错误或超过 8KB。")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		failure(w, 400, "请求中只能包含一个 JSON 对象。")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		body.Name = "新世界"
	}
	if len([]rune(body.Name)) > 32 {
		failure(w, 400, "世界名称最多 32 个字符。")
		return
	}
	if body.Mode == "" {
		body.Mode = "creative"
	}
	if body.Mode != "creative" && body.Mode != "survival" || body.Hardcore && body.Mode != "survival" {
		failure(w, 422, "当前支持创造模式、生存模式和极限模式（生存）。")
		return
	}
	if body.Lock != 0 && body.Lock != 2 {
		failure(w, 422, "当前支持所有人建造或仅房主建造。")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rooms) >= 1000 {
		failure(w, 409, "世界数量已达到上限。")
		return
	}
	id := randomHex(8)
	for s.rooms[id] != nil {
		id = randomHex(8)
	}
	e := s.emit(api.WorldCreating, nil, nil, map[string]any{"name": body.Name, "mode": body.Mode, "lock": body.Lock})
	defer s.drainActions()
	if e.Cancelled {
		failure(w, 403, limitText(e.Reason, 240))
		return
	}
	body.Name = strings.TrimSpace(e.String("name"))
	if body.Name == "" || len([]rune(body.Name)) > 32 {
		failure(w, 422, "插件返回了无效的世界名称。")
		return
	}
	key := randomHex(16)
	r := newRoom(id, body.Name, body.Mode, body.Lock)
	if s.config.InfiniteWorlds {
		r.Infinite = true
		r.Generator = generatorVersion
	}
	r.OwnerHash = hashKey(key)
	r.OwnerAccount = user.ID
	if r.usesSurvival() {
		r.W = newSurvivalW
		r.Ext = &RoomExt{Difficulty: "normal", Rules: map[string]bool{"keepInventory": false}}
		if body.Hardcore {
			r.Ext.Hardcore = true
			r.Ext.Difficulty = "hard"
		}
	}
	if err := s.saveRoom(r); err != nil {
		log.Printf("create room: %v", err)
		failure(w, 503, "存档写入失败，请检查后端存储。")
		return
	}
	s.rooms[id] = r
	s.emit(api.WorldCreated, r, nil, nil)
	writeJSON(w, 201, map[string]string{"id": id, "key": key})
}

type packet struct {
	kind int
	body []byte
}
type Peer struct {
	blockProtocol     int
	inventoryRevision uint64
	inventoryPlaceQ   int
	inventorySelQ     int
	originX, originZ  int
	epoch             uint32
	conn              *websocket.Conn
	send              chan packet
	room              *Room
	sid               string
	id                uint16
	owner             bool
	state             *Player
	position          bool
	flags             byte
	rd                int
	name              string
	builder           string
	window            *Window
	nextWindow        int
	known             map[int]bool
	chunkStreaming    bool
	chunkStreamAt     time.Time
	knownDrops        map[uint32]bool
	knownMobs         map[uint32]bool
	mining            *digState
	useAt             time.Time
	useSlot           int
	attackAt          time.Time
	peakY             float64
	lastHealth        time.Time
	air               int
	closed            bool
	lastSystem        string
	lastSystemAt      time.Time
	sessionHash       string
	attributes        map[string]float64
	ping              int
	nametag           *NametagInfo
}
type digState struct {
	X, Y, Z, Block, Held int
	At                   time.Time
}
type Message struct {
	BlockProtocol  int             `json:"blockProtocol,omitempty"`
	Preview        []BlockPosition `json:"preview,omitempty"`
	PlacementStack *Stack          `json:"-"`
	Forward        float64         `json:"forward"`
	Amount         float64         `json:"amount,omitempty"`
	Turn           float64         `json:"turn"`
	Exit           bool            `json:"exit"`
	Lines          []string        `json:"lines"`
	Epoch          uint32          `json:"epoch"`
	T              string          `json:"t"`
	V              int             `json:"v"`
	SID            string          `json:"sid"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	RD             int             `json:"rd"`
	Text           string          `json:"m"`
	M              int             `json:"-"`
	Mode           int             `json:"mode"`
	ID             uint16          `json:"id"`
	Token          string          `json:"token"`
	Confirm        bool            `json:"confirm"`
	UIID           string          `json:"uiId,omitempty"`
	Button         string          `json:"button,omitempty"`
	Values         map[string]any  `json:"values,omitempty"`
	CustomData     map[string]any  `json:"data,omitempty"`
	Hotbar         []int           `json:"hotbar"`
	Slot           int             `json:"slot"`
	I              int             `json:"i"`
	N              int             `json:"n"`
	Pick           int             `json:"pick"`
	S              int             `json:"s"`
	Item           string          `json:"item,omitempty"`
	Count          int             `json:"count,omitempty"`
	ResID          string          `json:"resId,omitempty"`
	Size           int             `json:"size,omitempty"`
	Flag           string          `json:"flag,omitempty"`
	TargetPlayer   string          `json:"targetPlayer,omitempty"`
	IsPublic       bool            `json:"isPublic,omitempty"`
	Blocking       bool            `json:"blocking"`
	State          bool            `json:"state"`
	X              int             `json:"x"`
	Y              int             `json:"y"`
	Z              int             `json:"z"`
	F              int             `json:"f"`
	W              int             `json:"w"`
	B              int             `json:"b"`
	Q              int             `json:"q"`
	D              int             `json:"d"`
	L              []int           `json:"l"`
	P              uint16          `json:"p"`
	E              uint32          `json:"e"`
	Add            []uint16        `json:"add"`
	Rm             []uint16        `json:"rm"`
	All            bool            `json:"all"`
	Part           string          `json:"part"`
	Ping           int             `json:"ping,omitempty"`
	Latency        int             `json:"latency,omitempty"`
	Sp             int             `json:"sp,omitempty"`
	Sound          string          `json:"sound,omitempty"`
}

func parseMessage(data []byte) (Message, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Message{}, err
	}
	var m Message
	var typ string
	if err := json.Unmarshal(raw["t"], &typ); err != nil {
		return m, err
	}
	if typ == "attack" {
		if rawS, ok := raw["s"]; ok {
			_ = json.Unmarshal(rawS, &m.Sound)
		}
		delete(raw, "s")
	}
	if typ == "milk" {
		// Entity ids are 32-bit; the shared "id" field is a 16-bit player id.
		raw["e"] = raw["id"]
		delete(raw, "id")
	}
	mode := raw["m"]
	delete(raw, "m")
	clean, _ := json.Marshal(raw)
	if err := json.Unmarshal(clean, &m); err != nil {
		return m, err
	}
	if m.T == "chat" {
		if err := json.Unmarshal(mode, &m.Text); err != nil {
			return m, err
		}
	} else if m.T == "click" {
		if err := json.Unmarshal(mode, &m.M); err != nil {
			return m, err
		}
	}
	return m, nil
}
func (p *Peer) enqueue(kind int, body []byte) {
	if p.closed {
		return
	}
	select {
	case p.send <- packet{kind, body}:
	default:
		p.conn.Close()
	}
}
func (p *Peer) json(v any) {
	data, err := json.Marshal(v)
	if err == nil {
		p.enqueue(websocket.TextMessage, data)
	}
}
func (p *Peer) sys(msg string) {
	if p.lastSystem == msg && time.Since(p.lastSystemAt) < 2*time.Second {
		return
	}
	p.lastSystem, p.lastSystemAt = msg, time.Now()
	p.json(map[string]string{"t": "sys", "m": msg})
}
func (p *Peer) close(code int) {
	p.enqueue(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""))
	p.closed = true
}
func (p *Peer) public() map[string]any {
	out := map[string]any{"id": p.id, "name": p.name, "h": nil, "p": nil, "owner": p.owner, "beaten": false}
	if p.nametag != nil {
		out["prefix"] = p.nametag.Prefix
		out["suffix"] = p.nametag.Suffix
		out["displayName"] = p.nametag.DisplayName
		out["nameColor"] = p.nametag.NameColor
		out["title"] = p.nametag.Title
		out["titleColor"] = p.nametag.TitleColor
		out["titleIcon"] = p.nametag.TitleIcon
		out["titleAnimation"] = p.nametag.TitleAnimation
	}
	return out
}
func broadcast(r *Room, v any, except *Peer) {
	for p := range r.Peers {
		if p != except {
			p.json(v)
		}
	}
}
func (s *Server) upgrade(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	r := s.rooms[req.PathValue("id")]
	s.mu.Unlock()
	if r == nil {
		failure(w, 404, "世界不存在。")
		return
	}
	if s.requireAccount(w, req, true) == nil {
		return
	}
	up := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{"mcraft-client"}, CheckOrigin: func(req *http.Request) bool { return s.originAllowed(req.Header.Get("Origin")) }}
	conn, err := up.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	p := &Peer{conn: conn, send: make(chan packet, 256), known: map[int]bool{}, air: 300, sessionHash: s.requestSessionHash(req)}
	conn.SetReadLimit(64 * 1024)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		defer conn.Close()
		for {
			select {
			case pkt, ok := <-p.send:
				if !ok {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if conn.WriteMessage(pkt.kind, pkt.body) != nil || pkt.kind == websocket.CloseMessage {
					return
				}
			case <-ticker.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					return
				}
			case <-s.stop:
				return
			}
		}
	}()
	defer func() {
		s.mu.Lock()
		s.disconnect(p)
		s.drainActions()
		close(p.send)
		s.mu.Unlock()
		conn.Close()
		<-writerDone
	}()
	window := time.Now()
	count := 0
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		count++
		if time.Since(window) > time.Second {
			window = time.Now()
			count = 1
		}
		if count > 500 {
			return
		}
		conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		s.mu.Lock()
		if p.closed {
			s.mu.Unlock()
			return
		}
		if s.accounts.session(p.sessionHash) == nil {
			p.json(map[string]string{"t": "authrequired"})
			p.close(4001)
			s.mu.Unlock()
			return
		}
		if kind == websocket.TextMessage {
			msgStr := string(data)
			if msgStr == "ping" || strings.HasPrefix(msgStr, "ping:") {
				p.enqueue(websocket.TextMessage, []byte("pong"))
			} else if m, err := parseMessage(data); err == nil {
				if p.room == nil {
					if m.T == "hello" {
						s.hello(p, r, m)
					}
				} else {
					s.message(p, m)
				}
			}
		} else if kind == websocket.BinaryMessage && p.room != nil {
			s.binary(p, data)
		}
		s.drainActions()
		s.mu.Unlock()
	}
}
func (s *Server) hello(p *Peer, r *Room, m Message) {
	if m.V != 4 {
		p.json(map[string]string{"t": "old"})
		p.close(4009)
		return
	}
	p.blockProtocol = m.BlockProtocol
	if len(s.registry.Entries) > 0 && p.blockProtocol != 2 {
		p.sys("服务器使用扩展方块，请刷新网页客户端后重新连接。")
		p.close(4009)
		return
	}
	user := s.accounts.session(p.sessionHash)
	if user == nil || user.Name == "" {
		p.json(map[string]string{"t": "authrequired"})
		p.close(4001)
		return
	}
	if err := s.admission(user.ID); err != nil {
		p.sys(err.Error())
		p.close(4009)
		return
	}
	legacySID, err := s.accounts.claimLegacy(user.ID, m.SID)
	if err != nil {
		p.sys("旧进度绑定失败，请检查账号存储后重新连接。")
		p.close(4009)
		return
	}
	if until := r.Bans[user.ID]; until.After(time.Now()) {
		p.json(map[string]string{"t": "kicked"})
		p.close(4009)
		return
	}
	for _, other := range worldPeers(r) {
		if other.sid == user.ID {
			other.json(map[string]string{"t": "moved"})
			other.close(4004)
			s.disconnect(other)
		}
	}
	if s.online() >= s.config.MaxPlayers {
		p.json(map[string]any{"t": "full", "cap": s.config.MaxPlayers})
		p.close(4009)
		return
	}
	sid := user.ID
	p.sid = sid
	p.room = r
	p.owner = accountOwns(r, sid, m.Key)
	p.name = user.Name
	p.builder = hashKey(sid)[:16]
	p.rd = min(12, max(2, m.RD))
	if r.Infinite {
		p.rd = min(6, p.rd)
	}
	p.id = s.nextID
	s.nextID++
	if s.nextID == 0 {
		s.nextID = 1
	}
	for {
		used := false
		for _, other := range worldPeers(r) {
			if other.id == p.id {
				used = true
				break
			}
		}
		if !used {
			break
		}
		p.id = s.nextID
		s.nextID++
		if s.nextID == 0 {
			s.nextID = 1
		}
	}
	if !s.allowed(api.PlayerJoining, r, p, map[string]any{"reason": "server_connect", "actor": p.sid, "target": r.root().ID}) {
		p.close(4009)
		p.room = nil
		return
	}
	registryData := map[string]any{"reason": "player_join", "actor": map[string]any{"kind": "player", "id": p.sid}, "target": map[string]any{"kind": "client", "id": p.id}, "registryVersion": 1}
	if !s.allowed(api.RegistrySyncing, r, p, registryData) {
		p.close(4009)
		p.room = nil
		return
	}
	if p.owner && r.OwnerAccount == "" {
		r.OwnerAccount = sid
	}
	others := []any{}
	for _, other := range worldPeers(r) {
		others = append(others, other.public())
	}
	r.Peers[p] = true
	r.Active = time.Now().UnixMilli()
	r.Dirty = true
	state := r.Players[sid]
	if state == nil && legacySID != "" && legacySID != sid && r.Players[legacySID] != nil {
		state = r.Players[legacySID]
		delete(r.Players, legacySID)
		r.Players[sid] = state
		oldBuilder := hashKey(legacySID)[:16]
		for key, author := range r.Authors {
			if author.Token == oldBuilder {
				author.Token, author.Name = p.builder, p.name
				r.Authors[key] = author
			}
		}
	}
	if state == nil {
		sp := s.engine.spawn(r)
		state = &Player{X: float64(sp[0]) + 0.5, Y: float64(sp[1]), Z: float64(sp[2]) + 0.5, Health: 20, Food: 20, Saturation: 5, Ext: &PlayerExt{GameMode: s.defaultGameMode(r)}}
		r.Players[sid] = state
	}
	if state.Ext != nil && state.Ext.Dim != "" && r.usesSurvival() {
		delete(r.Peers, p)
		r = s.dimRoom(r, state.Ext.Dim)
		r.Peers[p] = true
		p.room = r
	}
	if !number(state.X, r.minCoord(), r.maxCoord()) || !number(state.Y, -64, 512) || !number(state.Z, r.minCoord(), r.maxCoord()) {
		sp := s.spawnPoint(r)
		state.X = float64(sp[0]) + 0.5
		state.Y = float64(sp[1])
		state.Z = float64(sp[2]) + 0.5
	}
	p.state = state
	if p.ext().GameMode == "" {
		p.ext().GameMode = s.gameMode(p)
	}
	p.setOrigin()
	p.peakY = state.Y
	s.prepareStateView(p)
	s.renderPlayerNametag(p)
	welcome := map[string]any{"t": "welcome", "id": p.id, "you": p.public(), "players": others, "owner": p.owner, "creator": false, "room": r.info(), "st": state, "time": r.root().Time}
	welcome["op"], welcome["permissions"], welcome["canGamemode"] = s.isOperator(p.sid), s.permissionNodes(p.sid), s.canGameCommand(p, "server.gamemode")
	if r.usesSurvival() {
		if state.Inv == nil {
			state.Inv = &Inventory{Slots: make(Stacks, 46)}
			if s.isCreative(p) {
				for i, id := range state.Hotbar {
					if i >= 9 {
						break
					}
					if count := s.rules.max(id); count > 0 {
						state.Inv.Slots[i] = stack(id, count, 0)
					}
				}
				state.Inv.Selected = max(0, min(8, state.Slot))
			} else if !s.config.SingleWorld {
				state.Inv.Slots[0] = stack(s.rules.byName["Bread"], 8, 0)
			}
			state.Health = 20
			state.Food = 20
			state.Saturation = 5
		}
		s.sanitizeInventory(state.Inv)
		welcome["sv"] = s.peerSurvivalInfo(p)
	}
	welcome["registry"] = s.registrySnapshot()
	welcome["blockProtocol"] = p.blockProtocol
	p.json(welcome)
	s.emit(api.RegistrySynced, r, p, registryData)
	if r.usesSurvival() {
		if r.Infinite {
			// Send the player's footing first; remaining visibility is streamed
			// progressively without blocking authentication or every game tick.
			center := chunkCoord(int(math.Floor(state.X)), int(math.Floor(state.Z)))
			for dz := -1; dz <= 1; dz++ {
				for dx := -1; dx <= 1; dx++ {
					if _, err := s.ensureChunk(r, ChunkCoord{center.X + dx, center.Z + dz}); err != nil {
						r.SaveError = err.Error()
					}
				}
			}
		}
		s.stream(p)
		s.inventory(p, 0, 0)
		s.health(p)
		if state.Dead {
			s.deathMessage(p)
		}
	} else {
		s.syncCreative(p, r.Edits)
	}
	p.json(map[string]string{"t": "synced"})
	s.syncMobsFor(p)
	s.joinedExt(p)
	if bal, err := s.ensureEconomyAccount(p); err == nil {
		p.json(map[string]any{"t": "economy_balance", "balance": bal})
	}
	s.broadcastScoreboard(r)
	broadcastWorld(r, map[string]any{"t": "join", "p": p.public()}, p)
	s.emit(api.PlayerJoined, r, p, map[string]any{"reason": "server_connect", "actor": p.sid, "target": r.root().ID})
	log.Printf("%s joined %s (%s, %d online)", p.name, r.ID, r.Mode, len(r.Peers))
}
func (s *Server) disconnect(p *Peer) {
	if p.room == nil {
		return
	}
	s.cancelTrade(p, "玩家已离线")
	r := p.room
	s.closeWindow(p)
	s.disconnectTransport(p)
	delete(r.Peers, p)
	s.broadcastScoreboard(r)
	s.emit(api.PlayerQuit, r, p, nil)
	broadcastWorld(r, map[string]any{"t": "leave", "id": p.id}, nil)
	if err := s.saveRoom(r); err != nil {
		log.Printf("save: %v", err)
	}
	if r.parent != nil {
		r.parent.Dirty = true
		if err := s.saveRoom(r.parent); err != nil {
			log.Printf("save: %v", err)
		}
	}
	p.room = nil
}
func (s *Server) syncCreative(p *Peer, edits map[uint64]int) {
	kind := byte(3)
	if p.blockProtocol == 2 {
		kind = frameSync16
	}
	data := []byte{kind}
	for key, b := range edits {
		data = binary.LittleEndian.AppendUint32(data, toWireKey(key))
		if p.blockProtocol == 2 {
			data = binary.LittleEndian.AppendUint16(data, uint16(b))
		} else {
			data = append(data, byte(b))
		}
		if len(data) > 60000 {
			p.enqueue(websocket.BinaryMessage, data)
			data = []byte{kind}
		}
	}
	if len(data) > 1 {
		p.enqueue(websocket.BinaryMessage, data)
	}
}
func (s *Server) block(r *Room, x, y, z int) int {
	if r.Infinite && r.inside(x, z) && y >= 0 && y < 128 {
		if _, err := s.ensureChunk(r, chunkCoord(x, z)); err != nil {
			r.SaveError = err.Error()
			return 0
		}
	}
	if !r.inside(x, z) || y < 0 || y >= 128 {
		return s.engine.block(r, x, y, z)
	}
	if b, ok := r.Edits[blockKey(x, y, z)]; ok {
		return b
	}
	return s.engine.block(r, x, y, z)
}
func (s *Server) binary(p *Peer, data []byte) {
	if len(data) == 18 && data[0] == 1 && !p.room.Infinite || len(data) == 34 && data[0] == 14 && p.room.Infinite && binary.LittleEndian.Uint32(data[1:]) == p.epoch {
		st := p.state
		x, y, z := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[1:]))), float64(math.Float32frombits(binary.LittleEndian.Uint32(data[5:]))), float64(math.Float32frombits(binary.LittleEndian.Uint32(data[9:])))
		yaw, pitch := float64(int16(binary.LittleEndian.Uint16(data[13:])))/1e4, float64(int16(binary.LittleEndian.Uint16(data[15:])))/1e4
		flags := data[17]
		if p.room.Infinite {
			x = math.Float64frombits(binary.LittleEndian.Uint64(data[5:]))
			y = math.Float64frombits(binary.LittleEndian.Uint64(data[13:]))
			z = math.Float64frombits(binary.LittleEndian.Uint64(data[21:]))
			yaw = float64(int16(binary.LittleEndian.Uint16(data[29:]))) / 1e4
			pitch = float64(int16(binary.LittleEndian.Uint16(data[31:]))) / 1e4
			flags = data[33]
		}
		if !number(x, p.room.minCoord(), p.room.maxCoord()) || !number(y, -64, 512) || !number(z, p.room.minCoord(), p.room.maxCoord()) || !number(pitch, -1.7, 1.7) {
			return
		}
		if p.room.usesSurvival() && st.Dead && !s.isSpectator(p) {
			return
		}
		if st.Mount != 0 {
			if v := vehicleStore(p.room)[st.Mount]; v != nil && v.RiderID == p.id {
				// Input packets may turn the camera, but only the server's
				// vehicle physics may change mounted position or flying state.
				st.Yaw, st.Pitch, st.Fly = yaw, pitch, 0
				p.flags, p.position = flags&1, true
				p.room.Dirty = true
				if p.room.Infinite && (st.X-float64(p.originX) < 256 || st.X-float64(p.originX) > 768 || st.Z-float64(p.originZ) < 256 || st.Z-float64(p.originZ) > 768) {
					s.correctPosition(p)
				} else {
					s.stream(p)
				}
				return
			}
			st.Mount = 0
		}
		move := map[string]any{"x": x, "y": y, "z": z, "fromX": st.X, "fromY": st.Y, "fromZ": st.Z}
		if !s.allowed(api.PlayerMoving, p.room, p, move) {
			s.correctPosition(p)
			return
		}
		if ok, reason := s.validatePlayerMovement(p, x, y, z); !ok {
			s.emit(api.PlayerAnticheatViolation, p.room, p, map[string]any{
				"reason": reason,
				"x":      x,
				"y":      y,
				"z":      z,
				"fromX":  st.X,
				"fromY":  st.Y,
				"fromZ":  st.Z,
			})
			s.correctPosition(p)
			return
		}
		dist := math.Hypot(x-st.X, z-st.Z)
		if p.room.usesSurvival() {
			st.Exhaustion += min(dist, 5) * 0.02
		}
		st.X = x
		st.Y = y
		st.Z = z
		st.Yaw = yaw
		st.Pitch = pitch
		p.flags = flags & 7
		st.Fly = int(p.flags >> 1 & 1)
		if p.room.usesSurvival() && !s.mayFly(p) {
			st.Fly = 0
			p.flags &= ^byte(2)
		}
		p.position = true
		p.room.Active = time.Now().UnixMilli()
		p.room.Dirty = true
		s.emit(api.PlayerMoved, p.room, p, move)
		if p.room.usesSurvival() {
			if p.room.Infinite && (x-float64(p.originX) < 256 || x-float64(p.originX) > 768 || z-float64(p.originZ) < 256 || z-float64(p.originZ) > 768) {
				s.correctPosition(p)
			} else {
				s.stream(p)
			}
			s.pickupNearPlayer(p.room, p)
		}
	} else if (len(data) == 6 && data[0] == 2 || len(data) == 7 && data[0] == frameEdit16 && p.blockProtocol == 2) && p.room.Mode == "creative" && !p.room.Infinite {
		wire := binary.LittleEndian.Uint32(data[1:])
		b := int(data[5])
		if data[0] == frameEdit16 {
			b = int(binary.LittleEndian.Uint16(data[5:]))
		}
		if wire >= 1<<27 || wire&127 == 0 || !s.creativeBlock(b) {
			return
		}
		key := fromWireKey(wire)
		if p.room.Lock != 0 && !p.owner {
			old := 255
			if v, ok := p.room.Edits[key]; ok {
				old = v
			}
			s.denyBlock(p, wire, old)
			return
		}
		if !s.torchSupported(p.room, key, b) || !s.allowBlock(p, key, b) {
			old := 255
			if v, ok := p.room.Edits[key]; ok {
				old = v
			}
			s.denyBlock(p, wire, old)
			return
		}
		changes, err := s.prepareTorchChanges(p.room, p, key, b, 0)
		if err != nil {
			p.sys(err.Error())
			x, y, z := coords(key)
			old := s.block(p.room, x, y, z)
			s.denyBlock(p, wire, old)
			return
		}
		s.edit(p, key, b)
		s.applyTorchChanges(p.room, p, changes)
	}
}
func (s *Server) loop() {
	defer close(s.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			start := time.Now()
			s.mu.Lock()
			tick++
			s.tick = uint64(tick)
			s.emit(api.ServerTick, nil, nil, map[string]any{"online": s.online()})
			s.spawnPendingVillagers()
			for _, r := range s.allRooms() {
				if tick%10 == 0 {
					for p := range r.Peers {
						if p.sessionHash != "" && s.accounts.session(p.sessionHash) == nil {
							p.json(map[string]string{"t": "authrequired"})
							p.close(4001)
							s.disconnect(p)
						}
					}
				}
				if r.usesSurvival() {
					s.dropsTick(r)
					for key, c := range r.Containers {
						if c.Kind == "furnace" {
							s.furnaceTick(r, c, key)
						}
					}
					for p := range r.Peers {
						if tick%10 == 0 {
							s.life(p)
							s.windowUpdates(p)
						}
					}
				}
				s.worldTick(r)
				if tick%10 == 0 {
					for p := range r.Peers {
						p.json(map[string]any{"t": "time", "time": r.root().Time})
					}
					s.broadcastScoreboard(r)
				}
				for p := range r.Peers {
					if r.Infinite && p.chunkStreaming && !p.closed {
						s.streamInfinite(p)
					}
				}
				if r.Infinite && len(r.chunks) > 512 {
					s.trimChunksBudget(r, 2, 2*time.Millisecond)
				}
				for receiver := range r.Peers {
					var positions []*Peer
					for p := range r.Peers {
						if p != receiver && p.position {
							positions = append(positions, p)
						}
					}
					if len(positions) == 0 {
						continue
					}
					out := make([]byte, 5)
					out[0] = 6
					binary.LittleEndian.PutUint16(out[1:], uint16(len(positions)))
					for _, p := range positions {
						st := p.state
						out = binary.LittleEndian.AppendUint16(out, p.id)
						for _, v := range []float64{st.X - float64(receiver.originX), st.Y, st.Z - float64(receiver.originZ)} {
							out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v)))
						}
						out = binary.LittleEndian.AppendUint16(out, uint16(int16(st.Yaw*1e4)))
						out = binary.LittleEndian.AppendUint16(out, uint16(int16(st.Pitch*1e4)))
						out = append(out, p.flags)
					}
					receiver.enqueue(websocket.BinaryMessage, out)
				}
				if time.Since(s.ops.LastSave) >= time.Duration(s.ops.Settings.AutosaveSeconds)*time.Second {
					if r.parent != nil && r.Dirty && len(r.Peers) > 0 {
						r.parent.Dirty = true // player state is stored in the root world
					}
					if err := s.saveRoom(r); err != nil {
						log.Printf("save %s: %v", r.ID, err)
					}
				}
			}
			if time.Since(s.ops.LastSave) >= time.Duration(s.ops.Settings.AutosaveSeconds)*time.Second {
				s.ops.LastSave = time.Now()
			}
			s.operationsTick(start)
			s.drainActions()
			s.mu.Unlock()
		}
	}
}
func (s *Server) windowUpdates(p *Peer) {
	if p.window == nil {
		return
	}
	if p.kind() == "furnace" {
		c := p.room.Containers[p.window.Key]
		p.json(map[string]any{"t": "wslot", "w": p.window.ID, "s": c.Slots})
		dc := 0
		if c.Burn > 0 && c.Progress > 0 {
			dc = 1
		}
		p.json(map[string]any{"t": "wprop", "w": p.window.ID, "burn": c.Burn, "burnMax": c.BurnTotal, "dc": dc, "cook": c.Progress, "total": 200})
	}
}
func (s *Server) message(p *Peer, m Message) {
	r := p.room
	if r.Infinite && m.T != "chat" && m.T != "respawn" && !strings.HasPrefix(m.T, "trade_") && !strings.HasPrefix(m.T, "residence_") && m.T != "custom_ui_response" && m.Epoch != p.epoch {
		return
	}
	switch m.T {
	case "chat":
		msg := strings.TrimSpace(m.Text)
		if strings.HasPrefix(msg, "/") {
			s.command(p, msg)
			return
		}
		tag := s.renderPlayerNametag(p)
		prefix := ""
		suffix := ""
		displayName := p.name
		nameColor := ""
		if tag != nil {
			prefix = tag.Prefix
			suffix = tag.Suffix
			if tag.DisplayName != "" {
				displayName = tag.DisplayName
			}
			nameColor = tag.NameColor
		}
		chatData := map[string]any{
			"message":     limitText(msg, 120),
			"prefix":      prefix,
			"suffix":      suffix,
			"displayName": displayName,
			"nameColor":   nameColor,
		}
		e := s.emit(api.PlayerChatting, r, p, chatData)
		if e.Cancelled {
			if e.Reason != "" {
				p.sys(limitText(e.Reason, 240))
			}
			return
		}
		msg = e.String("message")
		if e.Data != nil {
			if pfx, ok := e.Data["prefix"].(string); ok {
				prefix = pfx
			}
			if sfx, ok := e.Data["suffix"].(string); ok {
				suffix = sfx
			}
			if dn, ok := e.Data["displayName"].(string); ok {
				displayName = dn
			}
			if nc, ok := e.Data["nameColor"].(string); ok {
				nameColor = nc
			}
		}
		v := []rune(msg)
		if len(v) > 120 {
			v = v[:120]
		}
		if len(v) > 0 {
			payload := map[string]any{
				"t":           "chat",
				"id":          p.id,
				"from":        p.name,
				"m":           string(v),
				"prefix":      prefix,
				"suffix":      suffix,
				"displayName": displayName,
				"nameColor":   nameColor,
			}
			broadcastWorld(r, payload, nil)
			s.emit(api.PlayerChat, r, p, payload)
		}
	case "latency":
		if m.Ping >= 0 {
			p.ping = m.Ping
		} else if m.Latency >= 0 {
			p.ping = m.Latency
		}
		return
	case "view":
		p.rd = min(12, max(2, m.RD))
		if r.Infinite {
			p.rd = min(6, p.rd)
		}
		if r.usesSurvival() {
			s.stream(p)
		}
	case "inv":
		if r.Mode == "creative" && len(m.Hotbar) == 9 && m.Slot >= 0 && m.Slot <= 8 {
			for _, b := range m.Hotbar {
				if !s.creativeItem(b) {
					return
				}
			}
			invData := map[string]any{"reason": "creative.hotbar", "hotbar": m.Hotbar, "slot": m.Slot}
			if !s.allowed(api.InventoryChanging, r, p, invData) {
				return
			}
			if m.Slot != p.state.Slot && !s.allowed(api.ItemSelecting, r, p, map[string]any{"slot": m.Slot, "previous": p.state.Slot}) {
				return
			}
			previous := p.state.Slot
			p.state.Hotbar = append([]int(nil), m.Hotbar...)
			p.state.Slot = m.Slot
			s.emit(api.InventoryChanged, r, p, invData)
			if previous != m.Slot {
				s.emit(api.ItemSelected, r, p, map[string]any{"slot": m.Slot, "previous": previous})
			}
			r.Dirty = true
		}
	case "attack":
		if m.E != 0 {
			if vehicleStore(r)[m.E] != nil {
				s.attackVehicle(p, m.E)
			} else {
				s.attackMob(p, m.E)
			}
		} else if r.usesSurvival() {
			s.survivalMessage(p, m)
		}
	case "rename", "lock", "kick", "reset", "undoBuilders", "undo":
		s.ownerAction(p, m)
	default:
		if r.usesSurvival() {
			s.survivalMessage(p, m)
		}
	}
}
func (s *Server) edit(p *Peer, key uint64, b int) {
	s.editReason(p, key, b, "")
}
func (s *Server) editReason(p *Peer, key uint64, b int, reason string) {
	data := s.playerBlockData(p, key, b)
	if component := s.redstoneBlockChangeData(p.room, p, key, b, reason); component != nil {
		data = component
	}
	if mechanic := s.mechanicBlockChangeData(p.room, p, key, b, reason); mechanic != nil {
		for field, value := range mechanic {
			data[field] = value
		}
	}
	if reason != "" {
		data["reason"] = reason
	}
	s.commitPlayerBlock(p, key, b, data)
}
func (s *Server) commitPlayerBlock(p *Peer, key uint64, b int, data map[string]any) {
	r := p.room
	if b != blkExplosiveTNT && s.primedTNT(r, key) {
		return
	}
	defer s.blockEvents(r, p, data)
	before := 255
	if v, ok := r.Edits[key]; ok {
		before = v
	}
	author := r.Authors[key]
	if author.Token == p.builder {
		before = author.Before
	}
	r.Edits[key] = b
	s.clearInactiveFire(r, key, b)
	s.trackRedstoneBlock(r, key, b)
	s.trackMechanicBlock(r, key, b)
	if _, ok := data["mechanicAfter"]; ok {
		s.broadcastMechanic(r, key)
	}
	if _, ok := data["railAfter"]; ok {
		s.broadcastRail(r, key)
	}
	r.Authors[key] = EditAuthor{Token: p.builder, Name: p.name, Before: before, After: b}
	r.Active = time.Now().UnixMilli()
	r.Dirty = true
	r.rt().redstoneDirty = true
	if r.usesSurvival() {
		s.broadcastChunk(r, key, b)
		x, y, z := coords(key)
		s.queueNeighbors(r, x, y, z)
	} else {
		for other := range r.Peers {
			if other != p {
				sendPlayerBlock(other, p.id, key, b)
			}
		}
	}
}
func (s *Server) ownerAction(p *Peer, m Message) {
	r := p.room
	if !p.owner {
		p.sys("需要房主权限。")
		return
	}
	switch m.T {
	case "rename":
		name := strings.TrimSpace(m.Name)
		ok := len([]rune(name)) > 0 && len([]rune(name)) <= 32
		if ok {
			e := s.emit(api.WorldRenaming, r, p, map[string]any{"name": name, "previous": r.Name})
			name = strings.TrimSpace(e.String("name"))
			ok = !e.Cancelled && name != "" && len([]rune(name)) <= 32
		}
		if ok {
			previous := r.Name
			r.Name = name
			s.emit(api.WorldRenamed, r, p, map[string]any{"name": name, "previous": previous})
			r.Dirty = true
			broadcast(r, map[string]any{"t": "room", "room": r.info()}, nil)
		}
		p.json(map[string]any{"t": "renamed", "ok": ok, "name": name, "error": "名称应为 1–32 个字符。"})
	case "lock":
		if m.Mode == 0 || m.Mode == 2 {
			data := map[string]any{"lock": m.Mode, "previous": r.Lock}
			if !s.allowed(api.WorldLockChanging, r, p, data) {
				return
			}
			r.Lock = m.Mode
			s.emit(api.WorldLockChanged, r, p, data)
			r.Dirty = true
			broadcast(r, map[string]any{"t": "room", "room": r.info()}, nil)
		}
	case "kick":
		for other := range r.Peers {
			if other.id == m.ID && other != p && !other.owner {
				data := map[string]any{"target": s.event("", r, other, nil).Player, "actor": p.id}
				if !s.allowed(api.PlayerKicking, r, other, data) {
					return
				}
				s.emit(api.PlayerKicked, r, other, data)
				r.Bans[other.sid] = time.Now().Add(5 * time.Minute)
				other.json(map[string]string{"t": "kicked"})
				other.close(4009)
				s.disconnect(other)
				break
			}
		}
	case "reset":
		if r.Mode != "creative" {
			p.sys("基础生存模式暂不支持重置世界。")
			return
		}
		if !s.allowed(api.WorldResetting, r, p, nil) {
			return
		}
		if r.Infinite {
			if err := s.resetInfinite(r, p); err != nil {
				p.sys(err.Error())
				return
			}
			s.emit(api.WorldReset, r, p, map[string]any{"reason": "owner_reset"})
			return
		}
		r.Edits = map[uint64]int{}
		r.Authors = map[uint64]EditAuthor{}
		r.Dirty = true
		broadcast(r, map[string]string{"t": "reset"}, nil)
		s.emit(api.WorldReset, r, p, nil)
	case "undoBuilders":
		if r.Mode != "creative" {
			return
		}
		if err := s.loadAdministrativeChunks(r); err != nil {
			p.sys(err.Error())
			return
		}
		type builder struct {
			Token  string `json:"token"`
			Name   string `json:"name"`
			Count  int    `json:"count"`
			Online bool   `json:"online"`
		}
		all := map[string]*builder{}
		for _, a := range r.Authors {
			b := all[a.Token]
			if b == nil {
				b = &builder{Token: a.Token, Name: a.Name}
				all[a.Token] = b
			}
			b.Count++
		}
		list := []*builder{}
		for token, b := range all {
			for peer := range r.Peers {
				if peer.builder == token {
					b.Online = true
				}
			}
			list = append(list, b)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
		more := len(list) > 64
		if more {
			list = list[:64]
		}
		p.json(map[string]any{"t": "undoBuilders", "ok": true, "builders": list, "more": more})
	case "undo":
		if r.Mode != "creative" || !m.Confirm {
			return
		}
		if !s.allowed(api.WorldUndoing, r, p, map[string]any{"builder": m.Token}) {
			return
		}
		if err := s.loadAdministrativeChunks(r); err != nil {
			p.sys(err.Error())
			return
		}
		patch := map[uint64]int{}
		previous := map[uint64]int{}
		total, skipped := 0, 0
		for key, a := range r.Authors {
			if a.Token != m.Token {
				continue
			}
			total++
			if r.Edits[key] != a.After {
				skipped++
				continue
			}
			if a.Before == 255 {
				previous[key] = r.Edits[key]
				delete(r.Edits, key)
			} else {
				previous[key] = r.Edits[key]
				r.Edits[key] = a.Before
			}
			patch[key] = a.Before
		}
		var physics []torchChange
		for key := range patch {
			x, y, z := coords(key)
			changes, err := s.prepareTorchChanges(r, nil, key, s.block(r, x, y, z), 0)
			if err != nil {
				for original, b := range previous {
					r.Edits[original] = b
				}
				p.json(map[string]any{"t": "undoResult", "ok": false, "error": err.Error()})
				return
			}
			physics = append(physics, changes...)
		}
		for key := range patch {
			delete(r.Authors, key)
		}
		for peer := range r.Peers {
			if r.Infinite {
				for key := range patch {
					x, y, z := coords(key)
					s.broadcastInfiniteDelta(r, x, y, z, s.block(r, x, y, z), true)
				}
				break
			}
			s.syncCreative(peer, patch)
		}
		s.applyTorchChanges(r, nil, physics)
		r.Dirty = true
		s.emit(api.WorldUndone, r, p, map[string]any{"builder": m.Token, "total": total, "restored": len(patch), "skipped": skipped})
		p.json(map[string]any{"t": "undoResult", "ok": true, "done": true, "total": total, "restored": len(patch), "skipped": skipped})
	}
}
func (s *Server) command(p *Peer, msg string) { s.executeCommand(p, msg) }
