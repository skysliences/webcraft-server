package main

import (
	"encoding/json"
	"fmt"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	SignTitle     string `json:"signTitle"`
	AllowOpBypass bool   `json:"allowOpBypass"`
}

type LockRecord struct {
	ChestKey  string `json:"chestKey"`
	SignKey   string `json:"signKey"`
	ChestPos  [3]int `json:"chestPos"`
	SignPos   [3]int `json:"signPos"`
	WorldID   string `json:"worldId"`
	OwnerUID  string `json:"ownerUid"`
	OwnerName string `json:"ownerName"`
	Password  string `json:"password"`
	CreatedAt int64  `json:"createdAt"`
}

func posKey(x, y, z int) string {
	return fmt.Sprintf("%d,%d,%d", x, y, z)
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

// Automated extraction/insertion has no authenticated human to unlock a box.
// Restrict adjacent checks to chest endpoints, protecting either half of a
// double chest without locking unrelated nearby hopper/furnace inventories.
func lockedAutomation(e *api.Event, locks map[string]*LockRecord) bool {
	if e == nil || e.World == nil {
		return false
	}
	for _, field := range []string{"origin", "target"} {
		v, ok := e.Data[field].(map[string]any)
		if !ok || v["kind"] != "container" || v["type"] != "chest" {
			continue
		}
		x, y, z := int(toFloat(v["x"])), int(toFloat(v["y"])), int(toFloat(v["z"]))
		for _, d := range [][2]int{{0, 0}, {-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
			if record := locks[posKey(x+d[0], y, z+d[1])]; record != nil && (record.WorldID == "" || record.WorldID == e.World.ID) {
				return true
			}
		}
	}
	return false
}

func main() {
	p := api.New()

	cfg := Config{
		SignTitle:     "[私有密码箱]",
		AllowOpBypass: true,
	}

	var mu sync.RWMutex
	chestLocks := make(map[string]*LockRecord) // chestKey -> LockRecord
	signLocks := make(map[string]*LockRecord)  // signKey -> LockRecord
	var dataFile string

	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			_ = json.Unmarshal(c.Config, &cfg)
		}
		_ = os.MkdirAll(c.DataDir, 0755)
		dataFile = filepath.Join(c.DataDir, "locks.json")
		raw, err := os.ReadFile(dataFile)
		if err == nil {
			var list []*LockRecord
			if err := json.Unmarshal(raw, &list); err == nil {
				mu.Lock()
				for _, r := range list {
					chestLocks[r.ChestKey] = r
					signLocks[r.SignKey] = r
				}
				mu.Unlock()
			}
		}
		log.Printf("[chest-lock] enabled with %d active locks", len(chestLocks))
		return nil
	}

	persist := func() error {
		if dataFile == "" {
			return nil
		}
		_ = os.MkdirAll(filepath.Dir(dataFile), 0755)
		mu.RLock()
		list := make([]*LockRecord, 0, len(chestLocks))
		for _, r := range chestLocks {
			list = append(list, r)
		}
		mu.RUnlock()

		raw, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return err
		}
		tmpFile := dataFile + ".tmp"
		if err := os.WriteFile(tmpFile, raw, 0644); err != nil {
			return err
		}
		return os.Rename(tmpFile, dataFile)
	}

	// 1. 玩家贴木牌到箱子上时，弹窗询问设置密码
	p.On(api.BlockPlaced, api.Subscription{Priority: api.Normal}, func(c *api.Context, e *api.Event) error {
		placedKey := e.String("blockKey")
		placedID := int(e.Number("block"))
		// 检查是否放置木牌 (Oak Sign ID 191 或 BlockKey 含 Sign)
		if placedID != 191 && !strings.Contains(placedKey, "Sign") {
			return nil
		}

		placedOnKey := e.String("placedOnBlockKey")
		placedOnID := int(e.Number("placedOnBlock"))
		placedOnName := e.String("placedOnBlockName")
		isChest := placedOnKey == "Chest" || placedOnName == "Chest" || (placedOnID >= 140 && placedOnID <= 143) || placedOnID == 54 || strings.Contains(strings.ToLower(placedOnKey), "chest") || strings.Contains(strings.ToLower(placedOnName), "chest")
		if !isChest {
			return nil
		}

		cx := int(e.Number("placedOnX"))
		cy := int(e.Number("placedOnY"))
		cz := int(e.Number("placedOnZ"))

		sx := int(e.Number("x"))
		sy := int(e.Number("y"))
		sz := int(e.Number("z"))

		ck := posKey(cx, cy, cz)
		mu.RLock()
		_, exists := chestLocks[ck]
		if !exists {
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				if _, ok := chestLocks[posKey(cx+d[0], cy, cz+d[1])]; ok {
					exists = true
					break
				}
			}
		}
		mu.RUnlock()

		if exists {
			c.Message(e, "§c[箱子锁] 该箱子（或连体大箱子）已经受到密码保护。")
			return nil
		}
		log.Printf("[chest-lock] block.placed: sign at (%d,%d,%d) placed on chest at (%d,%d,%d)", sx, sy, sz, cx, cy, cz)

		// 弹出设置密码界面
		c.OpenCustomUIFor(e, api.CustomUIDefinition{
			ID:      "chest_lock:set_password",
			Title:   "设置箱子密码",
			Content: "你正在为箱子设置密码锁。设置后，每次开启箱子均需输入此密码；只有你打掉木牌后方可解锁：",
			Inputs: []api.CustomUIInput{
				{
					ID:          "password",
					Label:       "访问密码",
					Type:        "password",
					Placeholder: "请输入 1-32 位密码...",
				},
			},
			Buttons: []api.CustomUIButton{
				{ID: "confirm", Text: "确认加锁", Type: "submit"},
				{ID: "cancel", Text: "取消放弃", Type: "cancel"},
			},
			Data: map[string]any{
				"chestX": cx, "chestY": cy, "chestZ": cz,
				"signX": sx, "signY": sy, "signZ": sz,
			},
		})
		return nil
	})

	// 2. 玩家在 Custom UI 界面提交响应
	p.On(api.PlayerCustomUISubmit, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		uiID := e.String("uiId")
		button := e.String("button")

		if uiID == "chest_lock:set_password" {
			if button != "confirm" {
				c.Message(e, "§e[箱子锁] 已放弃为箱子设置密码。")
				return nil
			}

			values, _ := e.Data["values"].(map[string]any)
			pwd := ""
			if values != nil {
				if v, ok := values["password"].(string); ok {
					pwd = strings.TrimSpace(v)
				}
			}
			if pwd == "" {
				c.Message(e, "§c[箱子锁] 密码不能为空，加锁未生效！")
				return nil
			}

			customData, _ := e.Data["customData"].(map[string]any)
			if customData == nil {
				customData, _ = e.Data["data"].(map[string]any)
			}
			if customData == nil {
				return nil
			}

			cx := int(toFloat(customData["chestX"]))
			cy := int(toFloat(customData["chestY"]))
			cz := int(toFloat(customData["chestZ"]))
			sx := int(toFloat(customData["signX"]))
			sy := int(toFloat(customData["signY"]))
			sz := int(toFloat(customData["signZ"]))

			ck := posKey(cx, cy, cz)
			sk := posKey(sx, sy, sz)

			mu.Lock()
			record := &LockRecord{
				ChestKey:  ck,
				SignKey:   sk,
				ChestPos:  [3]int{cx, cy, cz},
				SignPos:   [3]int{sx, sy, sz},
				WorldID:   e.World.ID,
				OwnerUID:  e.Player.AccountID,
				OwnerName: e.Player.Name,
				Password:  pwd,
				CreatedAt: time.Now().Unix(),
			}
			chestLocks[ck] = record
			signLocks[sk] = record
			mu.Unlock()

			// 自动更新木牌显示
			title := cfg.SignTitle
			if title == "" {
				title = "[私有密码箱]"
			}
			c.UpdateSign(e.World.ID, sx, sy, sz, []string{
				title,
				e.Player.Name,
				"右键箱子输入",
				"密码解锁",
			})

			_ = persist()
			log.Printf("[chest-lock] chest locked: chest at (%d,%d,%d) by %s (sign at %d,%d,%d)", cx, cy, cz, e.Player.Name, sx, sy, sz)
			c.Message(e, "§a[箱子锁] 密码设置成功！箱子已被木牌锁定保护。")
			return nil
		}

		if uiID == "chest_lock:unlock" {
			if button != "confirm" {
				c.Message(e, "§e[箱子锁] 已取消开箱。")
				return nil
			}

			values, _ := e.Data["values"].(map[string]any)
			pwd := ""
			if values != nil {
				if v, ok := values["password"].(string); ok {
					pwd = strings.TrimSpace(v)
				}
			}

			customData, _ := e.Data["customData"].(map[string]any)
			if customData == nil {
				customData, _ = e.Data["data"].(map[string]any)
			}
			if customData == nil {
				return nil
			}
			cx := int(toFloat(customData["chestX"]))
			cy := int(toFloat(customData["chestY"]))
			cz := int(toFloat(customData["chestZ"]))
			openX := cx
			openY := cy
			openZ := cz
			if ox, ok := customData["openX"]; ok {
				openX = int(toFloat(ox))
			}
			if oy, ok := customData["openY"]; ok {
				openY = int(toFloat(oy))
			}
			if oz, ok := customData["openZ"]; ok {
				openZ = int(toFloat(oz))
			}

			ck := posKey(cx, cy, cz)

			mu.RLock()
			record := chestLocks[ck]
			if record == nil {
				for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
					if adjRec := chestLocks[posKey(cx+d[0], cy, cz+d[1])]; adjRec != nil {
						record = adjRec
						break
					}
				}
			}
			mu.RUnlock()

			if record == nil {
				c.Message(e, "§c[箱子锁] 无法找到该箱子的锁定信息。")
				return nil
			}

			if record.Password != pwd {
				log.Printf("[chest-lock] unlock failed for player %s at (%d,%d,%d): password mismatch", e.Player.Name, cx, cy, cz)
				c.Message(e, "§c[箱子锁] 密码错误！无法打开此箱子。")
				return nil
			}

			log.Printf("[chest-lock] unlock success for player %s at (%d,%d,%d), opening (%d,%d,%d)", e.Player.Name, cx, cy, cz, openX, openY, openZ)
			c.Message(e, "§a[箱子锁] 密码正确，已为您打开箱子！")
			c.OpenContainer(e.World.ID, e.Player.ID, openX, openY, openZ)
			return nil
		}

		return nil
	})

	for _, name := range []string{api.InventoryMoving, api.InventoryPickingUp} {
		p.On(name, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
			mu.RLock()
			locked := lockedAutomation(e, chestLocks)
			mu.RUnlock()
			if locked {
				e.Cancel("chest_lock.automation_denied")
			}
			return nil
		})
	}

	// 3. 打开箱子时拦截并弹出密码输入 UI
	p.On(api.InventoryOpening, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if e.Bool("authorized") || e.String("source") == "plugin_authorized" {
			return nil
		}
		cx := int(e.Number("x"))
		cy := int(e.Number("y"))
		cz := int(e.Number("z"))
		ck := posKey(cx, cy, cz)

		mu.RLock()
		record := chestLocks[ck]
		if record == nil {
			// 检查相邻水平 4 格是否存在锁定的连体大箱子
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				adjKey := posKey(cx+d[0], cy, cz+d[1])
				adjRec := chestLocks[adjKey]
				if adjRec != nil {
					record = adjRec
					break
				}
			}
		}
		mu.RUnlock()

		if record == nil {
			return nil
		}

		// 拦截常规开箱动作（任何玩家包括房主/主人/管理员每次打开均需输入密码）
		e.Cancel("该箱子受密码保护，请输入密码。")
		log.Printf("[chest-lock] intercepted chest open at (%d,%d,%d) (record chest: %v) for player %s", cx, cy, cz, record.ChestPos, e.Player.Name)

		prompt := fmt.Sprintf("这是玩家 %s 的私有加密箱子。请输入访问密码以解锁：", record.OwnerName)
		if e.Player != nil && (e.Player.AccountID == record.OwnerUID || e.Player.Name == record.OwnerName) {
			prompt = "这是你的加密箱子。每次打开都需要验证密码，请输入访问密码以解锁："
		}

		// 弹出输入密码解锁 UI
		c.OpenCustomUIFor(e, api.CustomUIDefinition{
			ID:      "chest_lock:unlock",
			Title:   "输入箱子密码",
			Content: prompt,
			Inputs: []api.CustomUIInput{
				{
					ID:          "password",
					Label:       "箱子密码",
					Type:        "password",
					Placeholder: "请输入解锁密码...",
				},
			},
			Buttons: []api.CustomUIButton{
				{ID: "confirm", Text: "解锁并打开", Type: "submit"},
				{ID: "cancel", Text: "取消", Type: "cancel"},
			},
			Data: map[string]any{
				"chestX": record.ChestPos[0],
				"chestY": record.ChestPos[1],
				"chestZ": record.ChestPos[2],
				"openX":  cx,
				"openY":  cy,
				"openZ":  cz,
			},
		})
		return nil
	})

	// 4. 方块破坏检查：保护锁定的箱子和木牌
	p.On(api.BlockBreaking, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		bx := int(e.Number("x"))
		by := int(e.Number("y"))
		bz := int(e.Number("z"))
		bk := posKey(bx, by, bz)

		mu.RLock()
		chestLock := chestLocks[bk]
		if chestLock == nil {
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				if adjRec := chestLocks[posKey(bx+d[0], by, bz+d[1])]; adjRec != nil {
					chestLock = adjRec
					break
				}
			}
		}
		signLock := signLocks[bk]
		mu.RUnlock()

		if chestLock != nil {
			// 直接破坏被锁定的箱子是被禁止的
			e.Cancel("§c[箱子锁] 该箱子受到密码木牌保护！请先拆除锁门木牌。")
			return nil
		}

		if signLock != nil {
			// 只有放置木牌的主人（或 OP）可以打掉木牌
			isOwner := e.Player != nil && (e.Player.AccountID == signLock.OwnerUID || e.Player.Name == signLock.OwnerName)
			isOp := cfg.AllowOpBypass && e.Player != nil && e.Player.Operator
			if !isOwner && !isOp {
				e.Cancel(fmt.Sprintf("§c[箱子锁] 该木牌属于 %s，你没有权限拆除！", signLock.OwnerName))
				return nil
			}
		}

		return nil
	})

	// 5. 木牌被主人成功打掉后，解除箱子保护
	p.On(api.BlockBroken, api.Subscription{Priority: api.Normal}, func(c *api.Context, e *api.Event) error {
		bx := int(e.Number("x"))
		by := int(e.Number("y"))
		bz := int(e.Number("z"))
		bk := posKey(bx, by, bz)

		mu.Lock()
		signLock, ok := signLocks[bk]
		if ok {
			delete(chestLocks, signLock.ChestKey)
			delete(signLocks, bk)
		}
		mu.Unlock()

		if ok {
			_ = persist()
			log.Printf("[chest-lock] sign broken at (%d,%d,%d), unlocked chest at %v", bx, by, bz, signLock.ChestPos)
			c.Message(e, "§e[箱子锁] 锁门木牌已拆除，箱子已成功解除密码保护。")
		}
		return nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
