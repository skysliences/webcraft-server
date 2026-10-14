# Mcraft 服务端插件开发（API v1）

Go 后端通过事件总线、命令注册和受校验的动作接口提供扩展。完整事件目录由 `GET /api/events` 返回，包含玩法、区块与运营事件。插件是独立可执行程序，通过 stdin/stdout 的 JSON Lines 协议连接服务端；Go 开发者可直接使用 `pkg/plugin` SDK，其他语言可实现相同协议。插件与前端分离，无须修改浏览器或重新编译服务端。

## 核心账号与本服准入

核心账号服务注册后产生固定 ID，独立游戏服验证签名身份。`e.Identity.ID` 是不可修改的核心身份，游戏玩家快照新增 `e.Player.AccountID`；旧 `Player.UID` 保留原派生值。世界进度与权限绑定固定 ID，插件本服注册不能重新分配玩家 ID。

新增核心的 `account.register.before/registered`、`account.login.before/logged_in`、`account.profile.before/changed`、`account.logout.before/logged_out`、`account.server_register.before/server_registered`，以及游戏服的 `player.identity.before/identified`、`player.authenticate.before`、`player.authenticated`、`player.authentication.required`、`player.logout.before/logged_out`、`player.identity.updated` 和 `player.identity.expired`。准确名称及字段以 SDK 和 `/api/events` 为准。

准入插件监听 `player.authenticate.before`，通过 `authenticated`、`prompt`、`fields` 定义额外注册/登录表单；固定身份只读。`values` 仅在本服 register/login 前事件传入，不进入完成事件。修改值再次校验；取消、校验失败或保存失败不发完成事件；Monitor 只读。未通过本服准入的会话不能进入世界、创建世界或管理服务器。账号服务只提供账号事件，世界动作仍在游戏服执行。

可选示例源码在 `examples/server-auth/`，构建到 `bin/examples/server-auth/`，手动安装后才生效。完整事件表、验证限制、核心部署、旧账号迁移与多服务器配置见 [IDENTITY.md](IDENTITY.md)。

统一网站客户端加入私人 Go 游戏服时，核心 `POST /api/auth/server-proof` 签发绑定目标服的一次性 60 秒凭据。本服 `POST /api/auth/client` 通过核心 `POST /api/token/client` 消耗凭据，再使用原身份与本服插件准入路径；核心密码与 Cookie 不发送给私人服。本服令牌按目标 origin / ID 隔离存储在客户端 `sessionStorage`，REST 使用 Bearer，WebSocket 使用令牌子协议。

| 事件 | 行为 | 只读数据 |
| --- | --- | --- |
| `account.server_authorize.before` / `account.server_authorized` | before 可取消，无可修改字段；短期加入凭据成功登记后才发成功事件 | 固定 `actor=账号ID`、`target/serverId=目标服ID`、`reason=client_server_join` |
| `account.server_authorization.consumed` | 目标服成功一次性兑换后通知，不可取消 | 同上；不包含签名凭据、会话或令牌 |

Monitor 对这些事件保持只读。未登记服务器、无效核心会话、插件取消、签发或登记失败均不发送授权成功事件；过期、重复或错误 audience 的凭据不能成功兑换。游戏服依然触发身份与本服准入事件，插件不能通过修改固定身份绕过校验。

### 独立核心账号管理与在线事件

账号服务自身提供 `/admin` 管理页面及 `/api/admin/*` 接口，无需部署游戏世界。核心管理员由账号服务的启动配置解析账号名或固定 ID，并在私有 `admins.json` 中只保存固定 ID；游戏服 OP、房主和运营密钥均不能授权该后台。未登录返回 401，普通核心账号返回 403。管理写入需要核心会话、同源 Origin 和 `/api/admin/me` 签发的会话绑定 CSRF 令牌。所有已配置核心管理员都不能通过后台禁用或撤销会话。

| 事件 | 行为 | 只读数据 |
| --- | --- | --- |
| `account.admin.status.before` / `account.admin.status.changed` | before 可取消；禁用或恢复账号 | `actor` 管理员固定 ID、`target` 目标固定 ID、`reason=admin_disable/admin_enable`、`previous`、`disabled`、`auditId`；before 包含 `account`，成功包含 `before/after` 管理账号快照 |
| `account.admin.sessions_revoke.before` / `account.admin.sessions_revoked` | before 可取消；撤销目标全部核心会话 | `actor`、`target`、`reason=admin_revoke_sessions`、`auditId`；before 包含 `account/sessionCount`，成功包含 `before/after/revoked` |
| `account.presence.updated` / `account.presence.offline` | 已登录用户的网站页面活动、离开、心跳过期或会话失效后的事实事件，不可取消 | 固定 `actor/target`、兼容空 `serverId`、`location=website`、`lastSeenAt`、网站活动连接标识、`reason=website_heartbeat/website_leave/heartbeat_expired` 或会话失效原因 |

管理与网站在线事件没有可修改字段；两个管理 before 和 `account.server_authorize.before` 可以取消，事实通知不能取消。Monitor 的取消、改值和动作忽略。`Identity` 是管理动作的操作者或在线活动的账号，不能修改。管理快照为 `id/username/name/createdAt/registrationIp/lastLoginAt/lastLoginIp/disabled/sessionCount`，不含密码、哈希、令牌或旧 SID。历史未记录的 IP 为空，时间为 0；不推测或补造注册来源。

禁用账号在一次账号存储提交中撤销其全部核心会话。恢复不会复活旧会话；固定 ID、昵称及私人游戏服存档仍保留。撤销会话后网站在线快照立刻清理，游戏服在身份校验时移除失效身份。有效登录会话数量与网站在线人数分开：只接收已登录用户可见页面的活动，首页、本地游戏、线上游戏与账号/管理页面使用同一网站活动入口，每页面独立随机连接标识。多个活动页面按固定账号 ID 汇总；约 90 秒无心跳自动过期，重启不会恢复陈旧状态。私人游戏服不向核心上报玩家在线，身份租约检查也不计入网站活动。

管理审计保存在私有 `admin-audit.jsonl`：先同步保存 `attempt`，再提交账号存储，最后记录 `committed/cancelled/failed`。取消或账号保存失败没有成功事件；审计 attempt 无法写入时直接阻止操作。若账号已经提交、但审计结果追加失败，完成事件仍准确表示已提交的状态，接口返回 `auditWarning=true`，日志记录关联 `auditId`，原 attempt 可用于核查。审计不记密码、会话哈希或 CSRF 令牌。

可选 `examples/account-policy/` 演示按 `protectedAccountIds` 取消账号禁用或会话撤销，并用 Monitor 记录不含凭据及注册 IP 的成功管理、网站在线活动和私服授权事实。该插件应安装在**账号服务**的插件目录，按固定 ID 配置保护名单；不会自动安装到任何游戏服。

## OP、玩家模式与服务器默认模式

独立游戏服的新玩家默认生存，模式保存于玩家存档。OP 绑定核心账号 ID，房主字段保留用于旧 API v1；单世界服的房主身份不授予核心管理命令。`Player.Operator`、`Player.AccountID`、`Player.GameMode` 以及完整 `Items` 提供插件快照；旧 Owner、Inventory 字段保留。旧插件命令 `Permission:"owner"` 接受 OP 或原房主；新插件应使用 `server.gamemode` 等明确权限节点。

| 事件 | 取消 | 数据与限制 |
| --- | --- | --- |
| `player.operator.before` / `player.operator.changed` | before 可取消 | `actor`、固定账号 `target`、`previous`、新 `operator`、`reason` 为只读；目标在线时附完整玩家和物品快照，离线也有 Identity.ID |
| `player.gamemode.before` / `player.gamemode.changed` | before 可取消 | `actor`、固定账号 `target`、`previous`、新 `gamemode`、`reason` 为只读；Player 是目标玩家完整快照 |
| `server.default_gamemode.before` / `server.default_gamemode.changed` | before 可取消 | `actor`、主世界 `target`、`previous`、`gamemode`、`reason` 为只读 |

模式原因是 `command`、`console`、`plugin`；OP 配置原因是 `op`、`deop`、`plugin` 或原配置更新原因；默认模式命令为 `defaultgamemode`。事件 source 沿用现有插件来源。按 [Spigot 模式事件](https://hub.spigotmc.org/javadocs/spigot/org/bukkit/event/player/PlayerGameModeChangeEvent.html) 的只读目标模式设计：插件可取消，不能偷偷替换账号或新模式；Monitor 不能取消、修改或发动作。校验失败、取消、OP/默认配置保存失败均不发完成事件。批量配置变更任一 before 被取消则整批不提交。

SDK 新增 `Context.SetOperator(accountID, enabled)`；`Context.SetGameMode(event, mode)` 沿用原接口。两者都进入同一校验、before 和完成事件路径，不绕过插件保护。模式别名在核心标准化后再发事件；极限死亡限制也适用于插件。成功授予/撤销 OP 会立即向在线玩家同步权限；修改默认模式不覆盖已有玩家模式或背包。名称、Lore、属性、附魔、自定义数据在模式切换和存档恢复中保留。

`examples/world-policy` 在 Monitor 审计成功的 OP、玩家模式、默认模式事件。独立部署和控制台用法见 [SERVER_DISTRIBUTION.md](SERVER_DISTRIBUTION.md)。

## 主世界与运行示例

游戏服默认只运行一个主世界；下界、末地是它的维度。`server.world.select.before`（SDK `ServerWorldSelecting`）在启动绑定和备份恢复提交之前发送，插件可以取消，无可修改字段；`server.world.selected`（`ServerWorldSelected`）只在存档及 `world-server.json` 成功提交后发送。两者包含完整世界快照，以及 `data.actor`、`target`、`worldId`、`previousWorldId`、`newWorld`、稳定原因 `server_start` 或 `backup_restore`。失败启动不发送 selected/started/loaded；恢复取消或回滚不发送 selected/restored。

空数据目录自动创建唯一主世界，依次经过 select.before、`world.create.before`（仅名称 `name` 可修改，原因 `server_initialize`）、保存、`world.created`、selected。已有世界不会重复发送 created。网页 `POST /api/rooms` 在单世界模式返回 409，不触发创建事件。Monitor 修改与动作忽略；普通插件修改名称后再次校验。玩家连接仍经过 `player.join.before` / `player.joined`，新增只读 `reason=server_connect`、`actor=核心账号 ID`、`target=主世界 ID`。核心身份不会因服务器或维度而改变。

`examples/world-policy` 新增可选配置 `allowedPrimaryWorldId`：拒绝绑定其他主世界，并在 Monitor 审计成功绑定。默认不限制；不可在事件中改变世界 ID 绕过运营选择。部署、旧存档迁移和备份说明见 [SINGLE_WORLD.md](SINGLE_WORLD.md)。

在项目根目录执行：

```sh
npm run plugins:build
npm start
```

进入任意世界，在聊天框输入 `/plugins`、`/hello 开发者`、`/hi` 或 `/where`。生存世界房主可用 `/starter` 领取 3 个面包。`/help` 自动列出当前身份可用的插件命令。

示例源码在 `backend/examples/welcome/main.go`，配置在 `plugins/welcome/plugin.json`：

| 配置 | 行为 |
| --- | --- |
| `greeting` | 加入世界后的欢迎消息 |
| `blockedWord` | 聊天内容包含此文本时取消发送；空字符串禁用。默认 `[blocked]` |
| `chatPrefix` | 在聊天内容前加文本；默认空字符串 |
| `centerProtectionRadius` | 以 X=512、Z=512 为中心的保护半径；保护区内只有房主能采集/放置。默认 0 禁用 |

插件会把累计加入次数保存到 `data/plugins/welcome/visits.json`。它同时演示事件监听、优先级、配置、独立数据目录、命令别名、权限和每 100 tick 的定时回调。

## 插件目录与生命周期

```text
plugins/
  welcome/
    plugin.json
    welcome          # Windows 为 welcome.exe
data/
  plugins/
    welcome/         # 服务端为插件创建的数据目录
```

```json
{
  "id": "myplugin",
  "name": "我的插件",
  "version": "1.0.0",
  "apiVersion": 1,
  "entry": "myplugin",
  "args": [],
  "enabled": true,
  "config": {"greeting": "你好"}
}
```

`id` 必须唯一，格式为小写字母开头，后接小写字母、数字、`_` 或 `-`，最多 64 字符。`entry` 是插件目录内的相对路径，直接执行，不通过 shell；可指定一个编译后的程序或有可执行权限和 shebang 的脚本。不同平台需要各自的可执行文件。`enabled: false` 禁用插件。目录内没有 `plugin.json` 的文件夹不会加载。

服务端启动时按目录名排序发现插件，并发送初始化请求。SDK 在 `OnEnable` 中读取配置和数据；初始化结束后返回事件、命令注册表。随后发送 `world.loaded` 和 `server.started`。退出时先停止游戏循环，发送 `server.stopping`、玩家离开和最终存档事件，再执行插件 `OnDisable`。关闭阶段的动作不执行，避免最终存档后再修改世界。

添加、升级、修改配置或重新启用插件后，需要重启 Go 后端。当前没有热加载和插件依赖解析；监听和命令应在 `Run()` 前或 `OnEnable` 中注册。`On` 返回的函数可停止 SDK 内该监听回调，服务端的注册表在此次进程生命周期内保留。

可配置 `PLUGIN_DIR`（默认项目 `plugins/`）与 `PLUGIN_TIMEOUT`（默认 `100ms`，允许 `10ms`–`1s`）。初始化最多 5 秒；普通事件总分发预算、命令和关闭回调使用 `PLUGIN_TIMEOUT`。服务端 tick 为 100ms，即每秒 10 tick；`Every(100, ...)` 约每 10 秒触发，不是 Minecraft 的 20 TPS。

## 开发自己的 Go 插件

可在 `backend/examples/myplugin/` 中开发，直接导入：

```go
package main

import (
    "log"
    api "mcraft/backend/pkg/plugin"
)

func main() {
    p := api.New()
    p.On(api.PlayerJoined, api.Subscription{}, func(c *api.Context, e *api.Event) error {
        c.Message(e, "欢迎，" + e.Player.Name)
        return nil
    })
    p.On(api.BlockBreaking, api.Subscription{Priority: api.High, IgnoreCancelled: true},
        func(c *api.Context, e *api.Event) error {
            if e.Player != nil && !e.Player.Owner && e.Number("y") < 10 {
                e.Cancel("这个区域禁止采集。")
            }
            return nil
        })
    p.Command(api.Command{Name: "snack", Permission: "owner", Description: "领取面包"},
        func(c *api.Context, e *api.Event, args []string) error {
            c.GiveItem(e, "Bread", 1)
            return nil
        })
    if err := p.Run(); err != nil { log.Fatal(err) }
}
```

从 `backend/` 编译：

```sh
mkdir -p ../plugins/myplugin
go build -o ../plugins/myplugin/myplugin ./examples/myplugin
```

再把前面的 manifest 保存为 `plugins/myplugin/plugin.json`。插件源码也可以放在独立仓库；在独立 Go module 内配置本地 SDK：

```sh
go mod init example.com/myplugin
go mod edit -require=mcraft/backend@v0.0.0
go mod edit -replace=mcraft/backend=/absolute/path/to/WebCraft/backend
go mod tidy
go build -o /absolute/path/to/WebCraft/plugins/myplugin/myplugin .
```

API v1 是本项目的插件协议，不加载 Bukkit/Spigot 的 Java JAR 或 Go `.so`。安装的程序具有当前系统用户的进程权限；只安装可信插件。进程隔离用于故障处理，并非操作系统权限沙箱。

## 监听顺序与取消

事件首先执行最低优先级，再依次执行 `Lowest(-100)`、`Low(-50)`、`Normal(0)`、`High(50)`、`Highest(100)`、`Monitor(200)`。同优先级按插件 ID 排序，同一插件内按注册顺序执行。

`e.Cancel("原因")` 仅对可取消事件有效；后续监听不能解除已经发生的取消。`IgnoreCancelled: true` 跳过已取消的事件。`Monitor` 用于观察最终事件，服务端忽略它的取消、字段修改和动作。

`e.Data` 中只有事件目录明确标出的字段可修改，其余修改会被服务端忽略。聊天最多 120 字符、世界名称 1–32 字符、伤害必须为 0–1000 的有限数值；命令和死亡消息也会限制长度。取消采集、放置、合成、进食、伤害或重生会阻止对应事务。`player.death.before` 取消时保留受到致命伤害前的血量。

熔炉的取消点在产物生成前：原料和产物不变，燃烧计时仍继续。Shift 合成每次点击只执行一次 `item.craft.before`，批量过程中每生成一份配方触发一次 `item.crafted`。

## 事件结构与目录

每个事件带有 `id`、`name`、UTC `time`、`tick`、`source`、`cancellable`、`cancelled`、`reason`、`data`，以及适用时的 `world` 和 `player` 快照。`source` 为空表示核心游戏操作，`plugin:<id>` 表示插件动作。玩家快照包含本次连接 ID、由固定账号 ID 派生的匿名稳定 UID、自定义玩家名字、房主身份、坐标、血量、食物、死亡状态、选择槽和生存背包；不包含账号密码、会话令牌、浏览器 SID 或房主密钥。改名后 UID 保持不变，后续事件使用新名字。快照可读，修改快照不会修改游戏。加入前的新玩家尚无已初始化的状态；可在 `player.joined` 获取完整状态。服务器级事件没有世界/玩家，世界级事件没有玩家，插件修改方块时也可没有玩家，监听必须检查 nil。

火把失去支撑时会发出 `block.breaking`、`block.broken`、`block.changed`，`data.reason` 为 `support_lost`；相应的物品生成事件也带此原因。取消支撑移除涉及的火把 `block.breaking` 会拒绝整次支撑修改，避免一半提交。插件 `SetBlock` 可以放置火把及墙面朝向，仍须有效支撑；插件移除支撑同样触发火把物理与生存掉落。

下表的“前事件”均可取消，存档前事件除外。`GET /api/events` 返回机器可读的完整名称、可取消标志和可修改字段。

| 场景 | 前事件 | 完成/观察事件 | `data` 主要字段；可修改字段用粗体 |
| --- | --- | --- | --- |
| 服务端 | — | `server.started`, `server.stopping`, `server.tick` | 启动：`apiVersion`, `tickMillis`；tick：`online` |
| 加载世界 | — | `world.loaded` | 世界快照 |
| 创建世界 | `world.create.before` | `world.created` | **`name`**, `mode`, `lock`；创建前尚无 world |
| 保存世界 | `world.save.before`（不可取消） | `world.saved`, `world.save.failed` | `blocks`, `players`；失败：`error` |
| 重命名 | `world.rename.before` | `world.renamed` | **`name`**, `previous` |
| 建造权限 | `world.lock.before` | `world.lock.changed` | `lock`, `previous` |
| 重置世界 | `world.reset.before` | `world.reset` | 世界快照；创造模式 |
| 撤销建造 | `world.undo.before` | `world.undone` | `builder`；完成：`total`, `restored`, `skipped` |
| 加入/离开 | `player.join.before` | `player.joined`, `player.quit` | 玩家快照 |
| 移动 | `player.move.before` | `player.moved` | `x`, `y`, `z`, `fromX`, `fromY`, `fromZ` |
| 聊天 | `player.chat.before` | `player.chat` | **`message`** |
| 命令 | `command.before` | `command.executed` | **`command`**；完成：`handled`, `success` |
| 踢出 | `player.kick.before` | `player.kicked` | 玩家快照为目标，`actor` 为操作者连接 ID，`target` 为目标快照 |
| 传送 | `player.teleport.before` | `player.teleported` | `x`, `y`, `z`, `fromX`, `fromY`, `fromZ`, `reason` |
| 采集 | `block.break.before` | `block.broken` | `x`, `y`, `z`, `oldBlock`, `block=0` |
| 放置/替换 | `block.place.before` | `block.placed` | `x`, `y`, `z`, `oldBlock`, `block` |
| 单方块变更 | — | `block.changed` | 上述方块字段；包括插件动作；批量 reset/undo 使用世界事件 |
| 选择物品 | `item.select.before` | `item.selected` | `slot`, `previous` |
| 创造物品栏 | `inventory.creative.before` | `inventory.creative.changed` | **`count`**（领取时）；`reason`：`creative.take`, `creative.clear`, `creative.pick`, `creative.swap`；`slot`, `item`, `before`, `after`, `stack`, `target`, `changes`；交换还有 `otherSlot` |
| 进食 | `item.consume.before` | `item.consumed` | `item`, `count`, `slot` |
| 合成 | `item.craft.before` | `item.crafted` | `result` 堆叠 `[id,count,damage]`, `kind`；前事件 `batch` |
| 生成掉落 | `item.spawn.before` | `item.spawned` | `entityId`, `item`, **`count`**, `damage`, `x`, `y`, `z`, `reason`：`block_break` 或 `plugin` |
| 拾取掉落 | `item.pickup.before` | `item.picked_up` | 同上；`count` 为此次实际拾取数，`remaining` 为剩余数，`reason=pickup`；玩家为拾取者 |
| 掉落消失 | `item.despawn.before` | `item.despawned` | 同上；`reason`：`expired` 或 `void`，无玩家快照 |
| 背包操作 | `inventory.change.before` | `inventory.changed` | `reason`：`creative.hotbar`, `mine.tool`, `pickup`, `place`, `click`, `consume`, `equip`, `plugin.give_item`；对应物品/槽数据 |
| 点击物品栏 | `inventory.click.before` | `inventory.clicked` | `window`, `kind`, `slot`, `mode`, `button`, `stack`, `cursor`, `slots` |
| 拖拽分配物品 | `inventory.drag.before` | `inventory.dragged` | 所有字段只读：`reason=quick_craft`、`actor` 固定账号 ID、`target`、`window/kind`、`button`、`dragType=even/single`、`slots`、`cursorBefore/cursorAfter` 和每格 `changes` 的完整元数据快照 |
| 打开容器/工作台 | `inventory.open.before` | `inventory.opened` | `kind`, `x`, `y`, `z`；打开后 `window` |
| 关闭容器/工作台 | — | `inventory.closed` | `window`, `kind`；不可取消，保证游标与合成材料回收 |

拖拽以一次事务提交：左键（`button=0`, `dragType=even`）在可接受的格子均分，右键（`button=1`, `dragType=single`）每格一个；余数留在鼠标上。满格、输出格、不能放入的装备/容器格和不同元数据的堆叠会跳过，重复或越界槽请求整体拒绝。协议保留 API v1 的 `mode=5, slot=-999, slots=[...]`；此处 `-999` 是提交标记，不表示丢弃。依次执行 `inventory.change.before`、`inventory.click.before`、`inventory.drag.before`，任一取消都不写入任何格子，也不发送完成事件。完成后才依次发送 `inventory.dragged`、`inventory.clicked`、`inventory.changed`。无实际变化的请求不发送成功事件。

`changes` 按槽号排序，包含所有实际目标格（包括合成格和容器格）的 `slot/before/after`；各物品快照带完整名称、Lore、属性、自定义数据、附魔和耐久。`cursor` 保留旧字段，代表提交前游标；`cursorBefore/cursorAfter` 描述结算前后。SDK 的 `e.InventoryDrag()` 返回独立的 `InventoryDragContext`，修改该对象不能改变背包。所有拖拽字段只读，Monitor 也不能取消；需要禁止特定物品分配时监听 before 并取消。参考可选示例 `backend/examples/inventory-guard`，其自定义标签检查仅属于示例插件，不写入核心规则。该事务设计参考 [Spigot InventoryDragEvent](https://hub.spigotmc.org/javadocs/spigot/org/bukkit/event/inventory/InventoryDragEvent.html)。
| 熔炼产物 | `furnace.smelt.before` | `furnace.smelted` | `x`, `y`, `z`, `input`, `result`（物品 ID）, `count` |
| 玩家攻击 | `player.attack.before` | `player.attacked` | **`amount`**；`damage` 完整攻击者、目标和武器快照 |
| 伤害 | `player.damage.before` | `player.damaged` | **`amount`**, `reason`, `cause`；`damage` 伤害上下文，完成：`health`, `appliedAmount` |
| 回血 | `player.heal.before` | `player.healed` | **`amount`**, `reason`, `healthBefore`, `maxHealth`；完成：`healthAfter` |
| 物品元数据 | `item.meta.before` | `item.meta.changed` | `slot`, `before`, `after`, `reason`；支持取消 |
| 工具耐久 | `item.durability.before` | `item.durability.changed` | **`amount`**（非负整数）, `slot`, `stack`, `damageBefore`；完成：`damageAfter`, `broken` |
| 装备属性 | — | `player.attributes.changed` | 已提交装备变化的聚合属性 `before`, `after` |
| 死亡 | `player.death.before` | `player.dead` | **`message`**；前事件 `amount` |
| 重生 | `player.respawn.before` | `player.respawned` | 玩家快照 |
| 世界时间 | `world.time.before` | `world.time.changed` | `time`, `reason`；手动修改带 `previous`；自动推进每秒观察通知，不发可取消前事件 |
| 生物生成 | `entity.spawn.before` | `entity.spawned` | `entityId`, `type`, `x`, `y`, `z`, `health`, `reason` |
| 生物清除 | `entity.despawn.before` | `entity.despawned` | 同上，`reason=distance` |
| 生物受伤 | `entity.damage.before` | `entity.damaged` | 同上，加 **`amount`**；伤害后 `health` |
| 生物死亡 | `entity.death.before` | `entity.dead` | 同上；取消死亡不产生掉落 |
| 生物攻击 | `entity.attack.before` | `entity.attacked` | 同上，加 **`amount`**, `targetId`, `damage`；玩家快照为受攻击者，`reason=melee` |
| 耕种与作物 | `farming.till.before`, `crop.grow.before`, `crop.fertilize.before`, `crop.harvest.before` | `farming.tilled`, `crop.grown`, `crop.fertilized`, `crop.harvested` | `x`, `y`, `z`, `block`, `next_block`；骨粉带 `item`；收获带 `drops` |
| 丢弃与合并 | `item.drop.before`, `item.merge.before` | `item.dropped`, `item.merged` | 丢弃：**`count`**, `item`；合并：`target_id`, `source_id`, `item`, `count` |
| 流体与桶 | `fluid.flow.before`, `bucket.fill.before`, `bucket.empty.before` | `fluid.flowed`, `bucket.filled`, `bucket.emptied` | 流体：`from_x/y/z`, `to_x/y/z`, `block`；桶：`x`, `y`, `z`, `liquid`, `bucket` |
| 门与床睡眠 | `door.toggle.before`, `player.sleep.before`, `player.wake.before` | `door.toggled`, `player.slept`, `player.woke`, `world.night_skip` | 门：`x`, `y`, `z`, `block`；床：`x`, `y`, `z`；跳夜：`time` |
| 铁砧修理/重命名 | `anvil.use.before` | `anvil.used`, `anvil.damaged` | 修理：`left`, `right`, `result`, `cost`, **`name`**；损坏：`x`, `y`, `z`, `old_block`, `new_block`, `fall_distance` |
| 动物互动 | `animal.breed.before`, `animal.tame.before`, `animal.shear.before` | `animal.bred`, `animal.tamed`, `animal.sheared` | `mob_id`, `mob_type`；繁殖/驯服带 `item`；剪毛带 `wool_count` |
| 反作弊违规 | — | `player.anticheat.violation` | `reason`, `x`, `y`, `z`, `fromX`, `fromY`, `fromZ` |
| 自定义界面交互 | — | `player.custom_ui.submit` | `uiId`, `button`, `values`（表单各输入项键值对）, `customData`（插件透传上下文）, `epoch` |

生存采集事件发生在采集时间、距离、工具和容器内容校验通过后，背包满时仍可采集。`item.spawn.before` 在破坏方块前执行，取消只抑制该次掉落，不阻止方块破坏；`count` 只能改为该物品单个堆叠上限内的正整数。需要保护方块时取消 `block.break.before`。`block.broken` 后发布 `item.spawned`，随后按重力移动，并在 600ms 后尝试附近拾取。

拾取先检查背包可容纳的数量，再执行 `item.pickup.before` 和 `inventory.change.before`（`reason=pickup`），任一取消时背包和掉落数量均不变；完成后触发两者的完成事件。完全拾取通过拾取事件和客户端实体移除表示，不再触发消失事件；消失事件用于寿命到期或掉入虚空。取消寿命消失时继续保留物品，并在后续 tick 再尝试。`entityId` 只在对应世界内唯一，需结合 `world.id` 使用。

创造模式直接修改方块也走采集/放置事件，但不产生采集掉落。背包事件针对表中列出的玩家操作；自动耐久、死亡关闭窗口/回收等核心维护使用其专属事件或快照，不提供可取消的每槽写入钩子。

创造物品栏允许当前处于创造模式的玩家领取全部已登记物品，包括方块、武器、工具、护甲、食物和材料。服务端根据物品登记表校验 ID 与堆叠数量；生存、冒险、旁观玩家不能伪造领取请求。先发送 `inventory.creative.before`，再发送 `inventory.change.before`；任一取消、数量修改无效、背包无法保存选块时的原物品或请求没有实际变化，都不发送完成事件。领取数量可修改为该物品堆叠上限内的正整数；清空和交换的 `count` 必须保持 0。`before`、`after`、`stack` 和 `changes` 中的物品均是包含名称、Lore、属性、自定义数据、附魔和耐久的完整快照，属于只读字段。`changes` 按槽序列出事务全部写入。创造领取替换指定快捷栏；选块把原物品完整保存到空背包槽；快捷栏交换通过单个事务移动原物品，保留全部元数据。客户端不能借领取接口写入自定义元数据。完成事件可用于插件统计或后续 `SetItemMeta` 动作；Monitor 只读。

## 插件动作与命令

SDK 的 `Context` 提供以下动作。动作在当前游戏操作完成后，由 Go 服务端依次校验并应用，不直接修改共享游戏内存。

| Go SDK | 协议 `type` | 规则 |
| --- | --- | --- |
| `c.Message(e, text)` | `message` | 向事件玩家发送系统消息，最多 240 字符 |
| `c.Broadcast(worldID, text)` | `broadcast` | 世界内广播系统消息 |
| `c.SpawnMob(worldID, "cow", x, y, z)` | `spawn_mob` | cow/pig/sheep/chicken/zombie/spider；校验位置、地面及世界 32 只上限 |
| `c.SetTime(worldID, ticks)` | `set_time` | 绝对世界 tick，0–2^48；触发可取消时间前事件并广播 |
| `c.OpenCustomUI(worldID, playerID, ui)` / `c.OpenCustomUIFor(e, ui)` | `custom_ui` | 向玩家推送自定义弹窗表单（输入框/多按钮/透传数据），客户端呈现原汁原味 Minecraft 浮雕面板 |
| `c.CloseCustomUI(worldID, playerID, uiID)` / `c.CloseCustomUIFor(e, uiID)` | `close_custom_ui` | 主动关闭玩家当前打开的指定 ID 自定义弹窗 |
| `c.UpdateSign(worldID, x, y, z, lines)` | `write_sign` | 免物品与手持校验直接覆写指定木牌 4 行文字并广播 |
| `c.OpenContainer(worldID, playerID, x, y, z)` | `open_container` | 授权为玩家主动打开指定坐标处的箱子/末影箱容器窗口 |
| `c.GiveItem(e, "Bread", 3)` | `give_item` | 生存物品内部英文名或协议 `itemId`；1–2304 个；背包放不下时整次拒绝 |
| `c.DropItem(worldID, "Oak Log", 3, x, y, z)` | `drop_item` | 生存世界生成地面物品，1–2304 个，按堆叠上限拆分；X/Z 0.125–1023.875，Y 0–127.75；触发掉落事件 |
| `c.SetBlock(worldID, x, y, z, blockID)` | `set_block` | X/Z 0–1023，Y 1–127；创造方块 0–67，生存使用现有规则 ID；不覆盖已创建的容器 |
| `c.Teleport(e, x, y, z)` | `teleport` | 在线且存活，坐标在世界范围内；触发传送事件并重新同步客户端 |
| `c.Heal(e, amount)` | `heal` | 在线且存活，有限数值 0–20；血量上限 20 |

可直接追加 `api.Action` 到 `c.Actions` 来指定其他在线玩家的 `worldId`、`playerId`。跨玩家操作也在宿主校验；连接 ID 应与世界 ID 一起使用，离线 UID 不能用于动作。插件动作的变更会保存到世界存档。

例如在采集完成后额外掉落一个面包：

```go
p.On(api.BlockBroken, api.Subscription{}, func(c *api.Context, e *api.Event) error {
    if e.World != nil && e.World.Mode == "survival" && e.Source == "" {
        c.DropItem(e.World.ID, "Bread", 1,
            e.Number("x") + .5, e.Number("y") + .35, e.Number("z") + .5)
    }
    return nil
})
```

每次响应最多 32 个动作，一个游戏事务最多缓存 256 个、执行 128 个动作，超出部分丢弃并记录日志。修改动作会触发相应游戏事件；监听中发起同类动作时，应使用 `e.Source` 过滤自己的动作，避免递归。`Context` 是单次回调的动作缓冲，不能保留它在异步 goroutine 中追加动作；需要周期工作时用 `Every` 在下次回调执行。

命令 `Name`、`Aliases` 使用与插件 ID 相同的字符规则。重复名称/别名、覆盖 `/help`、`/spawn`、`/plugins`、`/time`、`/summon` 或无效权限会导致插件初始化失败。权限为 `everyone`（默认）或 `owner`，宿主在调用插件前检查。参数用空白分隔，当前不支持引号转义解析。自定义命令在创造、生存模式都生效。

## 故障与状态

`GET /api/plugins` 查看插件版本、状态、事件和命令；状态包括 `loaded`、`disabled`、`failed`。插件响应超时、崩溃、无效 JSON、ID 不匹配、超限动作或回调错误时，宿主记录原因并终止该插件。`GET /healthz` 在插件失败时返回 503 和 `failedPlugins`。

已注册的可取消监听在插件失败后仍阻止相关操作，直到修复并重启；观察事件的失败不会撤销已经提交的操作。命令返回执行失败提示。初始化失败的插件没有有效注册表，无法提供保护，应通过健康接口检查后再开放服务。

回调运行在服务端顺序调度中，普通插件不能长时间阻塞。远程网络请求应提前在插件自己的后台完成，把缓存结果用于快速回调；不要在回调中同步请求本服务端 HTTP API，否则它等待游戏锁，插件会超时。stdout 只能输出协议 JSON，日志用 stderr（Go `log` 默认写 stderr）。

## JSON Lines 协议

每行一个 UTF-8 JSON 对象，最大响应 1 MiB；一请求对应一响应，必须回传相同的数字 `id`。SDK 的所有协议类型位于 `pkg/plugin/types.go`。

初始化请求：

```json
{"id":1,"method":"initialize","initialize":{"apiVersion":1,"id":"myplugin","version":"1.0.0","dataDir":"/absolute/data/plugins/myplugin","config":{},"tickMillis":100}}
```

初始化响应：

```json
{"id":1,"metadata":{"apiVersion":1,"subscriptions":[{"event":"player.chat.before","priority":50,"ignoreCancelled":true,"every":1}],"commands":[{"name":"hello","description":"问候","permission":"everyone"}]}}
```

事件请求的 `method` 是 `event`，附带 `priority` 和完整 `event`；同一插件在不同优先级分别收到请求。事件响应：

```json
{"id":2,"cancelled":true,"reason":"此消息被过滤","data":{"message":"修改后的内容"},"actions":[]}
```

命令请求的 `method` 是 `command`，附带规范主命令名 `command`、参数数组 `args`、玩家/世界事件快照。响应可包含动作：

```json
{"id":3,"actions":[{"type":"message","worldId":"世界ID","playerId":7,"message":"你好"}]}
```

退出请求是 `{"id":4,"method":"shutdown"}`，插件清理资源后回复 `{"id":4}` 并退出。任何响应包含非空 `error` 时会触发宿主的插件故障处理。

## 验证

```sh
npm run backend:test
npm run plugins:build
```

测试使用临时世界和独立插件进程，覆盖优先级与 Monitor、可修改字段白名单、取消和取消后跳过、定时任务与取消监听、命令别名/权限/冲突、错误协议、超时与崩溃、真实 WebSocket 命令/聊天/物品发放/传送，以及取消采集、放置、掉落/拾取/消失、合成、进食、熔炼、伤害、死亡和重生的数据一致性。

使用支持全局 WebSocket 的 Node.js 22+，在后端与示例已编译后执行 `npm run plugins:smoke`，可对原样编译的 Welcome 插件进行隔离冒烟测试，包括重启后的插件数据和玩家物品恢复。

## 物品与战斗扩展

完整 ItemStack、Lore、属性、自定义数据、伤害上下文、SDK 方法和 custom-items 示例见 [ITEMS.md](ITEMS.md)。`npm run plugins:build` 同时构建两个示例，生存房主可用 `/itemdemo`、`/itemlore`、`/itemattr` 和 `/itemdata`。


## 运营、区块与生存扩展事件

新增字段保持 API v1 兼容。`World` 包含 `rootId`、`dimension`、`infinite`；维度世界 ID 可直接用于 SDK 动作。坐标始终为真实世界坐标，支持负数和远距离。`Player` 增加权限节点、游戏模式、经验级别/进度和状态效果快照。旧 Inventory 数组字段保留，完整物品使用 Items / ItemStack。

| 事件组 | before / 完成或通知 | 可修改字段 |
| --- | --- | --- |
| 区块 | chunk.generated / loaded / saving / saved / save.failed / unloaded | 无，生命周期通知只读 |
| 自然植被 | chunk.populate.before / chunk.populated | 无，仅取消；x/z 是区块坐标，blocks 是真实坐标与原/新方块快照，count/counts、target、vegetationVersion、previousVersion、generator、reason |
| 设置 | server.settings.before / changed | 无，仅取消 |
| 控制台 | server.command.before / executed | 无，仅取消；command、actor、reason |
| 插件 | server.plugins.reload.before / reloaded / reload.failed | 无，仅取消 |
| 备份 | server.backup.before / created / failed | 无，仅取消 |
| 恢复 | server.restore.before / restored / restore.failed | 无，仅取消 |
| 模式 | player.gamemode.before / changed | 无，仅取消 |
| 难度 | world.difficulty.before / changed | 无，仅取消 |
| 规则 | world.rule.before / changed | 无，仅取消；rule、previous、value |
| 天气 | world.weather.before / changed | duration，整数游戏刻，1–2,000,000 |
| 维度 | player.dimension.before / changed | 无，仅取消；fromDimension、dimension、x/y/z |
| 经验 | player.experience.before / changed | amount，整数 -1,000,000–1,000,000；结算前后级别、点数、总计 |
| 等级 | player.level.before / changed | 无，仅取消 |
| 效果 | player.effect.apply.before / applied | ticks 1–2,000,000；amplifier 0–9，均为整数 |
| 效果移除 | player.effect.remove.before / removed | 无，仅取消 |
| 着火 | player.ignite.before / ignited | ticks，整数 1–2,000,000 |
| 附魔 | item.enchant.before / enchanted | 无，仅取消；before/result、消耗及完整物品 |
| 酿造 | brewing.complete.before / completed | 无，仅取消；beforeSlots/afterSlots 完整物品 |
| 投射物 | projectile.launch.before / launched | damage，0–1000 |
| 投射物命中 | projectile.hit.before / hit / removed | 无，仅取消命中；武器、攻击者、目标和伤害来源保留 |
| 方块物理 | block.physics.before / changed | 无，仅取消；reason、原方块/新方块、坐标、容器物品 |
| 树叶衰减 | leaves.decay.before / leaves.decayed | 无，仅取消；reason=leaf_decay、actor、target、leaf、stack、blockData、supportDistance、drops 完整快照 |
| TNT 点火 | tnt.prime.before / tnt.primed | fuseTicks，整数 1–12000；actor、source、target、reason、attacker、slot、stack/before/after |
| 红石组件 | redstone.component.before / changed | 无，仅取消；component、componentBefore/componentAfter、actor、target、reason；玩家操作附完整物品快照 |
| 爆炸 | world.explosion.before / created | radius，0.1–16；breakBlocks、坐标 |

所有 `.before` 可取消，except 原有 world.save.before 仍为只读保存通知。未声明字段的修改会被忽略，数值重新校验；Monitor 不接受修改、取消或动作。等级花费触发等级事件；死亡沿用 player.death.before/dead 的事务。经验球拾取及经验修补在经验、背包保护均接受后才提交；取消时经验球保留。方块下落批量预校验，任一目标取消则整次下落不提交。

操作成功后才发完成事件，取消或校验失败没有表示成功的事件。存档失败使用失败通知。插件可以调用 `GiveExperience`、`ApplyEffect`、`ClearEffects`、`SetGameMode`、`SetRule`、`SetDifficulty`、`SetWeather`，这些动作再次进入同样的规则和事件入口。监听完成事件后重复发起同一动作可能形成循环，服务端有单事务动作预算，插件应检查 Source 和变化值。

`examples/world-policy` 演示持久化爆炸保护、owner 权限命令与 Monitor 观察规则/备份/重载。安装后 `/worldpolicy protect` 启用当前世界爆炸保护，`/worldpolicy open` 关闭；默认关闭，不改变原有玩法。执行 `npm run plugins:build` 构建。运营命令与恢复步骤见 [OPERATIONS.md](OPERATIONS.md)。

## 树叶衰减与 TNT

天然树叶通过相连树叶检查六格内的树干支撑。移除树干、破坏相邻树叶及恢复存档后，服务端分批检查树冠，失去支撑的树叶自动消失；有其他树干支撑的部分保留。玩家放置的树叶（包括旧存档中的放置记录）不自动衰减。无限世界只检查已加载区块；未加载边界视为未知支撑，避免误删。无人在线时暂停树叶衰减，重新进入后继续。

衰减先发送 `leaves.decay.before`，准备掉落的 `item.spawn.before`，再进入 `block.physics.before` 方块事务；成功后才发布掉落和 `leaves.decayed`。任何取消、非法掉落或容量不足都会保留树叶且不发送成功事件，稍后重新检查。`leaf` / `stack` 为树叶快照，`drops` 为完整掉落物快照，成功事件反映实际掉落；橡木可掉苹果，树叶可掉树苗和木棍。`doTileDrops=false` 仅移除树叶。该事件的 `actor=server`，玩家为空，所有字段只读。

TNT 可由红石供电、打火石、火焰及其他 TNT 爆炸点燃。普通照明火把不供电；红石火把不向其附着的支撑方块供电，测试时应将火把放在 TNT 旁边的独立支撑上。点火默认 `fuseTicks=40`，本服务每刻 100ms，即四秒；连续供电、移除电源或邻居更新不会重置引信。点燃后的 TNT 不可通过普通采集或 `SetBlock` 收回，避免取消引信或重复掉落。连锁爆炸默认使用十刻短引信。

`tnt.prime.before` 仅允许修改整数 `fuseTicks`（1–12000）。取消或非法数值不点燃、不磨损打火石、不发送 `tnt.primed`；生存打火石使用还经过耐久和库存 before，创造模式不磨损。事件 `reason` 为 `redstone`、`flint_and_steel`、`fire`、`explosion_chain` 或 `plugin`，`actor` 为固定账号 ID 或 `server`；插件动作的 `source=plugin:<id>`。`attacker` 和完整物品 `stack/before/after` 保留点火快照，自动红石没有手持物品。`world.explosion.before` / `created` 另包含点火来源 `ignitionReason`，伤害沿用玩家/生物伤害事件，其 `damage` 包含原攻击者、武器、来源、前后生命及实际扣血。

引信剩余刻数及来源保存在兼容的可选 `BlockData.tnt` / `fire` 字段中，不变更 MCC v1 或 API v1。重启、区块卸载及多人重连会恢复引信状态和来源。若爆炸或源方块移除被取消，保留已点燃的 TNT，十刻后重试；没有爆炸成功事件。前端只显示服务端已提交的闪烁、烟雾、倒计时和爆炸。

SDK 常量为 `LeavesDecaying`、`LeavesDecayed`、`TNTPriming`、`TNTPrimed`。`Context.PrimeTNT(event, x, y, z, fuseTicks)`（零为默认引信）记录选中槽和完整期望物品；执行时比较快照，并重新检查权限、模式、生命、距离、目标和点火事件。插件不能用该动作绕过保护。`world-policy` 示例配置 `preserveNaturalLeaves=true` 可取消自然衰减，`blockedTntReasons` 可按原因拒绝点火，`tntFuseTicks` 可设定引信；原有世界爆炸保护同时拒绝点火。所有配置默认保持正常玩法，Monitor 只审计成功事件。

验证包括相关 Go 树叶/TNT 测试、独立插件进程的取消与字段修改、旧存档和负坐标 MCC 恢复，以及 `npm run tnt:verify` 的实际 WASM/GL、多人坐标与世界切换检查。`scripts/preview-world-physics.mjs` 提供临时数据目录中的真实游戏验收场景。

## 红石组件、插件保护与恢复

红石由 Go 服务端结算，浏览器发送操作并显示结果。目前可放置的组件为红石粉（物品 320 → 方块 108）、红石火把（355 → 109）、中继器（356 → 166）、比较器（357 → 167）、活塞（358 → 168）、黏性活塞（359 → 169）、石质压力板（360 → 174）和橡木压力板（361 → 175）。使用对应方块 ID 的旧快捷栏也进入同一放置入口。红石粉、中继器、比较器和压力板需要完整方块顶面支撑；火把可放在顶面或侧面；活塞可朝六个方向放置。右键中继器轮换 1–4 档延迟，右键比较器切换比较/减法模式。

线路在同层的东南西北四邻之间连通，并沿横邻上/下一级传播，每格功率减少 1。上坡要求较低线路上方没有实心阻挡，下坡要求相邻边缘同层没有实心阻挡（当前实心判定为方块 kind 1/2/3）。中继器从背面输入、向 `facing` 正面输出；比较器另接受两侧输入参与比较/减法，但仍只向正面输出。火把、压力板和红石块可作为四向电源，活塞是受电器。客户端连接形状用于显示，实际功率以服务端 `rs` 消息为准。已提交的方块变更（含普通电源、坡上阻挡及 SDK `SetBlock`）标记线路需要刷新，下一游戏刻合并重算一次；取消的变更不标记已提交，也不发送成功事件。线路功率变化继续使用只读 `redstone_signal` 组件 before/changed 事件。

| 事件 | 取消与可修改字段 | 数据 |
| --- | --- | --- |
| `redstone.component.before` / `redstone.component.changed` | before 可取消；所有字段只读 | `component`、完整 `componentBefore/componentAfter`、`reason`、`actor`、方块 `target`、真实坐标 `x/y/z`、`block`；放置还包括 `oldBlock`、`face`、`item`、实际消耗 `count` |
| `block.place.before` / `block.placed`、`block.changed` | before 可取消；红石放置字段只读 | 同一组件与物品上下文；组件与方块成功提交后才发送完成通知 |
| `inventory.change.before` / `inventory.changed` | 生存消耗 before 可取消；字段只读 | `slot` 和完整 `stack/before/after`；创造放置不消耗，因此不发送扣除库存事件 |

组件快照包含 `kind`、`facing`、`supportFace`、`delay`、`subtract`、`power`、`powered`、`extended`、`sticky`、`countdown`、`timeout`、`locked`；未设置的零值字段可能省略。`component` 是稳定字符串：本轮八种组件分别为 `wire`、`torch`、`repeater`、`comparator`、`piston`、`sticky_piston`、`stone_pressure_plate`、`oak_pressure_plate`。插件应按字符串判断，不能通过修改快照替换组件或状态。玩家放置/交互的 `actor` 为固定账号 ID，附完整玩家和选中物品的名称、Lore、属性、附魔、自定义数据快照；自动信号与计时变化的 `actor=server`，没有玩家或手持物品。插件动作另由事件 `source=plugin:<id>` 标识。

稳定原因与对应操作如下：

| `reason` | 操作 |
| --- | --- |
| `redstone_place` | 使用选中物品放置组件 |
| `redstone_interact` | 玩家或 `UseRedstone` 调整组件 |
| `redstone_signal` | 红石线路信号变化 |
| `redstone_button_tick` | 按钮计时变化 |
| `redstone_plate_tick` | 压力板受压/释放 |
| `redstone_repeater_tick` | 中继器延迟、锁定和输出变化 |
| `redstone_comparator_tick` | 比较器输出变化 |
| `redstone_piston_extend` / `redstone_piston_retract` | 活塞伸出/收回 |
| `block_change` | 其他玩家方块操作涉及组件，例如破坏 |
| `plugin.redstone` | 插件 `SetBlock` 放置、替换或清除组件 |

放置依次校验方块 before、组件 before、生存库存 before。全部通过，并再次确认选中槽与完整物品快照后，才一起提交方块、组件状态与库存；随后发送方块、组件和库存完成通知。取消、无效值、权限拒绝、快照变化或回滚都不会扣物品或发送成功事件。交互与自动状态变化也先发送组件 before，成功后才广播和发送 changed；Monitor 的取消、修改与动作被忽略。事件目录通过 `GET /api/events` 发布，SDK 常量为 `RedstoneComponentChanging`、`RedstoneComponentChanged`。

SDK `Context.UseRedstone(event, blockX, blockY, blockZ, face)` 使用事件中玩家的选中槽，记录完整期望物品快照；执行时比较当前选中槽和物品，再进入与右键操作相同的校验与事件入口。它受玩家建造权限、模式、死亡状态、距离、支撑、放置空位和实体占位约束，不能代替玩家绕过保护。它也能调整目标中继器/比较器，交互不消耗物品。`SetBlock` 涉及红石时同样进入组件和方块保护事件。不要在本次 before 或同一 changed 回调中无条件再次发起相同动作，避免循环。

活塞最多推动 12 个普通方块，先验证完整移动批次及所有目标，再统一提交移动与活塞状态。任一方块物理/破坏/放置保护或组件 before 取消时，整次操作不提交，不留下被清空的源格。基岩、黑曜石不可推动；当前也不移动容器、任何带 `BlockData` 的方块或红石组件，以保留它们的物品和元数据。黏性回收采用同样保护规则；元数据搬迁暂未实现。

区块仍使用 MCC v1，在 `BlockData` 中新增可选 `Redstone`（JSON `redstone`）对象，有限世界存档使用相同组件数据。保存保留朝向、档位、模式、功率与计时状态，加载后恢复服务端运行状态。没有组件对象的旧裸红石方块按默认值恢复，保留原方块 ID，不再次扣道具；中继器默认 1 档，活塞默认朝上，并兼容已有旧朝向/功率字段。格式没有升版，不删除或重建旧区块。

状态网络消息为 `{"t":"rs","epoch":窗口代次,"worldId":"维度世界ID","dim":"维度","blocks":[...]}`，每个方块的 `x/z` 是可为负数的绝对世界坐标，附 `id`、`power`、`facing`、`delay`、`sub`、`extended`，火把另有 `torchFace`。前端按当前原点转换坐标，拒绝旧 epoch；切换世界/维度或窗口时清除旧显示缓存，再接收当前已加载组件同步。状态随放置、交互、信号变化以及重连同步，不由客户端猜测电路结果。

`examples/world-policy` 增加可选配置 `blockedRedstoneComponents`，默认不限制。例如在现有 `plugin.json` 的 `config` 中加入 `"blockedRedstoneComponents":["piston","sticky_piston"]`，可在组件 before 取消这两类变更，稳定拒绝原因是 `world_policy.redstone_component_denied`；此列表同时拦截玩家、插件和自动状态变化，不仅拦截放置。成功 changed 在 Monitor 写入插件数据目录的 `audit.jsonl`，被取消的操作不会写成成功记录。配置修改后按现有运营流程重启插件/服务端。

验证命令为 `npm run redstone:verify`（真实 WASM 注册、放置网络包、原版资源、渲染和坐标同步）、相关 Go 红石测试及 `npm run plugins:smoke`（独立临时存档和插件进程）。

## 生物生成蛋

生成蛋 ID 420–430 依次为 `zombie`、`skeleton`、`creeper`、`spider`、`enderman`、`cow`、`pig`、`sheep`、`chicken`、`wolf`、`villager`，每组最多 64 个。手持蛋对地面的顶面使用，在相邻空位生成对应生物。服务端检查玩家生命、建造锁、游戏模式、交互距离、地面、实体体积、库存及可配置的单维度生物安全上限（默认 1024）；冒险和旁观模式不能使用。生存成功消耗一个，创造不消耗。自然生成规则（时间、光照与 `doMobSpawning`）不阻止手动生成蛋；和平难度仍阻止普通敌对生物生成，方便在白天测试怪物；生成后仍遵循生物伤害、燃烧等现有规则。

| 事件 | 取消与可修改字段 | 数据 |
| --- | --- | --- |
| `item.spawn_egg.before` / `item.spawn_egg.used` | before 可取消，所有数据只读 | `reason=spawn_egg`、固定账号 `actor`、`target` 坐标与成功实体 ID、`type`、`x/y/z`、`clickX/clickY/clickZ`、`face`、`slot`、物品 `item`、实际消耗 `count`，完整 `stack/before/after` |
| `entity.spawn.before` / `entity.spawned` | before 可取消，所有数据只读 | 生成蛋操作附同一物品/操作者上下文，并有 `entityId`、生物 `type`、坐标与生命值 |
| `inventory.change.before` / `inventory.changed` | 生存扣蛋 before 可取消，所有数据只读 | 同一生成蛋上下文及完整扣除前后快照；创造不修改库存，因此不发送扣蛋事件 |

顺序为生成蛋 before、实体生成 before、生存库存 before；全部通过后同时提交库存与实体，再发送实体完成、生存库存完成、生成蛋完成。取消、无效请求或旧快照均不扣蛋、不生成实体、不发送成功事件。物品名称、Lore、属性、附魔、自定义数据以完整快照传递，剩余蛋和存档恢复保留元数据；Lore 只负责显示，不改变生物类型。事件目录通过 `GET /api/events` 发布，Monitor 的取消、修改及动作都被忽略。

SDK `Context.UseSpawnEgg(event, slot, blockX, blockY, blockZ, face)` 仅操作玩家当前选中槽位，记录完整期望物品快照，延迟执行时再次比较并进入与玩家相同的校验和事件入口。`event.Source` 保留插件来源，`actor` 仍是物品所属玩家的固定账号 ID。普通 `SpawnMob` 动作仍经过实体生成事件，适合无需库存的插件生成。`examples/world-policy` 增加 `blockedSpawnEggTypes` 配置，例如 `["creeper"]` 可以禁止苦力怕蛋，并在 Monitor 审计成功用蛋事件。

## 无限世界的自然植被

区块加载采用有界后台读取与纯地形准备，按距离分批提交到主线程并发送给玩家。后台准备不写世界状态、不保存文件、不调用插件；未被实际访问的准备结果不会发送 `chunk.generated` 或 `chunk.loaded`。真正提交仍经过原有的 `chunk.populate.before`，成功后依次发 `chunk.populated`（若提交植被）、`chunk.generated`（若首次生成）、`chunk.loaded`；取消植被不会发 `chunk.populated`。生命周期字段及 SDK 常量保持 API v1。缓存只复用未编辑的基础地形编码，放置、破坏、插件修改和恢复的最新编辑在发送时叠加；停服先终止后台任务，再保存已提交的世界数据。探索回收按刻分批保存/卸载，保存失败保留区块及元数据，不发送 `chunk.unloaded`。

噪声生成器按世界种子与绝对坐标生成植被：草原有短草、蒲公英、虞美人、矢车菊和滨菊，森林有草、蕨与少量花，山地有稀疏草丛，沙漠有枯死灌木。散布与斑块不会随区块加载顺序改变。只有合适地表上方的空气格允许生成，水下和不同维度不生成草原植被。

每个区块的植被事务先发送 `chunk.populate.before`，提交后发送 `chunk.populated`，不逐株发送玩家放置事件。玩家字段为空；`world` 与 `target.kind=chunk` 标识目标，`blocks` 按真实坐标列出 `x/y/z`、`oldBlock=0`、`block` 和 `biome`。新地形的 `reason=terrain_generation`；旧无限区块补生成的 `reason=vegetation_backfill`。所有字段只读，插件可取消整个区块的植被事务。取消时不改植被或版本标记、不发完成事件，下次从磁盘重新加载时可再次尝试。Monitor 只读。world-policy 示例把已提交的植被事件写入审计记录。

区块格式仍为 MCC v1，新增兼容字段 `vegetationVersion`。缺失标记的旧区块在首次加载时补齐植被；已被玩家修改的地表、显式清空的格子、建筑、容器和已有植物会跳过，旧引擎迁移范围保留原地形。升级前把原始区块文件逐字节保存到 `data/backups/vegetation-v1/`，备份失败则不提交补生成。标记随区块保存，使玩家之后清除的草丛不会因重启而重新生成；地形高度、矿石、树木、账号和物品元数据保持原数据。

## 铁路、运输与机械方块（API v1）

原生引擎的实际注册是唯一 ID 来源；`scripts/export-rules.mjs` 读取运行中的 WASM names/kinds，再导出 Go 规则。旧 `update-rules-json.mjs` 入口也委托该流程，不再写入引擎没有注册的方块。既有 ID 不迁移：179 炼药锅、191 橡木告示牌、192 唱片机、211 普通铁轨、212 动力铁轨、213 探测铁轨、214 发射器、215 投掷器、216 侦测器、217 红石灯、218 阳光探测器、219 堆肥桶；对应物品 ID 为 382、383、384、365、366、367、376、377、378、379、380、381。唱片 13 是真实物品 500。

| 可取消前置事件 | 仅成功提交后事件 | 使用场景 |
| --- | --- | --- |
| `block.mechanic.before` | `block.mechanic.changed` | 放置、移除、桶/堆肥/唱片交互、信号和侦测脉冲 |
| `rail.state.before` | `rail.state.changed` | 放置、相邻连接、坡道、供电及矿车占用 |
| `block.dispense.before` | `block.dispensed` | 一次红石上升沿发射/投掷一件物品 |
| `sign.text.before` | `sign.text.changed` | 四行告示牌文本提交 |
| `vehicle.place.before` | `vehicle.placed` | 矿车、船放置 |
| `vehicle.enter.before` | `vehicle.entered` | 进入车辆 |
| `vehicle.exit.before` | `vehicle.exited` | 下车、断线等退出路径 |
| `vehicle.move.before` | `vehicle.moved` | 服务端每次车辆位移 |
| `vehicle.destroy.before` | `vehicle.destroyed` | 车辆销毁及掉落 |
| `fishing.cast.before` | `fishing.cast` | 抛竿 |
| `fishing.bite.before` | `fishing.bit` | 入水等待后咬钩 |
| `fishing.reel.before` | `fishing.reeled` | 收竿、鱼获、经验和耐久结算 |

上述新事件的数据字段均为只读，before 只允许取消。Monitor 只读且不能发动作。不得把修改嵌套 state/stack 视为修改世界的办法；使用受保护的 SDK 动作。机械与铁轨事件带 `reason`、`actor`、`source`、块坐标 `target`、完整 `stack/before/after`、`mechanicBefore/mechanicAfter` 或 `railBefore/railAfter`；告示牌另带 `linesBefore/linesAfter`。物品流转同时经过已有背包、容器、掉落、投射物、桶、经验、耐久事件，任一必要 before 拒绝时不会消费输入或发送相应成功事件。

机械状态中 `facing` 为 0 下、1 上、2 北、3 南、4 西、5 东；侦测器观察 facing 方向并向背面输出短脉冲。普通铁轨可转弯，动力和探测铁轨只直行或爬坡。`shape` 包括 `north_south`、`east_west`、`ascending_north/south/east/west`、`north_east/west`、`south_east/west`。发射器/投掷器提供九格窗口；保持高电平不会连续消耗库存，输出失败或插件取消保留原物品完整元数据。

车辆事件提供 `entityId/type`、车辆状态、操作者与原始车辆物品快照。车辆采用独立服务端实体，不参与怪物自然生成和 AI。受攻击沿用 `entity.damage.before/entity.damaged` 的完整 DamageContext（攻击者、目标、武器、来源、结算前后生命与实际扣血量）；销毁掉落沿用物品事件。矿车根据服务器铁轨连接与坡道移动，动力铁轨加速/制动，探测铁轨由真实矿车占用驱动。玩家右键上车，W/S 前进后退、A/D 转向，Shift 下车；客户端不自行确定车辆位置。

SDK 提供 `Context.UseMechanic(e,x,y,z,face)`、`WriteSign(e,x,y,z,lines)`、`UseVehicle(e,slot,x,y,z,face)`、`InteractVehicle(e,entityID)`。对应 host actions 为 `use_mechanic/write_sign/use_vehicle/interact_vehicle`。涉及库存的动作复制完整期望物品和选中槽，延迟执行时比较当前状态，重新验证范围、支撑、空位、权限、模式、生命与插件事件。`SetBlock` 涉及新方块同样经过对应规则和事件；不能直接清除有库存/唱片的方块来丢失物品。

新增状态通过可选 `BlockData.mechanic/rail`、每维度 `RoomExt.vehicles/nextVehicleId` 存档，保持旧 v1 字段与既有存档版本兼容。唱片、容器、车辆及其破坏后掉落都保存名称、Lore、属性和命名空间数据；读取后校验，旧存档没有这些字段时按块 ID 初始化默认状态。浮标是短期运行实体，断线或换维度清理，不在下次登录时恢复旧钓鱼结算。

`world-policy` 示例提供 `blockedMechanics`（如 `["dispenser"]`）、`blockedVehicleTypes`（如 `["minecart"]`）、`disableFishing`、`disableDispensing`，默认不限制玩法；Monitor 将提交成功的机械、铁轨、发射、告示牌、车辆和钓鱼事件写入审计日志。侦测器的前方检测/背面输出参考 [Minecraft 官方介绍](https://www.minecraft.net/en-us/article/block-week-observer)。

唱片机使用已注册的物品 `500 music_disc_13`，服务端提交插入/取出后广播完整唱片快照；客户端播放经官方 1.21.1 资源哈希校验的唱片 13 音频，64 格内按距离衰减。重复状态同步不会重复播放，拔盘、破坏、切换世界和断线会停止声音。

### 发射器特殊行为与事务

发射器从有效的非空槽等概率选择一个物品。以下行为由 Go 执行，并同一提交源容器、输出实体、目标状态和工具耐久；投掷器仍只弹出物品或传入相邻容器。插件取消或校验失败时，不消耗、不生成、不发布成功事件。

| 已注册物品 | 发射器效果 | 对应保护事件 |
| --- | --- | --- |
| 11 种刷怪蛋 420–430 | 前方生成对应生物，消耗一个蛋 | `item.spawn_egg.before`、`entity.spawn.before` |
| 箭 302、雪球 332、鸡蛋 333 | 发射真实投射物；鸡蛋命中 1/8 孵化，成功中 1/32 生成四只幼鸡 | `projectile.launch.before`、`projectile.hit.before`、孵化与生物事件 |
| 打火石 305 | 点燃前方 TNT、可支撑空位的火或完整黑曜石地狱门；结算耐久与耐久附魔 | 点燃、传送门、`tnt.prime.before`、`item.wear.before` |
| TNT 27 | 前方直接生成有引信的 TNT，保留机器来源和原物品元数据 | `tnt.prime.before` 与方块事件 |
| 水桶 307、岩浆桶 308、空桶 306 | 放置/收集源流体；地狱水蒸发；返回桶 | `bucket.empty.before`、`bucket.fill.before` |
| 玻璃瓶 400 | 前方水填成水瓶；返回瓶 | `item.bottle.fill.before` |
| 骨粉 326 | 农作物、三种树苗、草方块草花、短草变高草 | `crop.fertilize.before`、`crop.grow.before` 与整批方块事件 |
| 剪刀 304 | 剪前方成年未剪毛绵羊，掉落羊毛并结算耐久 | 新旧剪毛事件、`item.wear.before`、掉落事件 |
| 20 种盔甲、凋灵骷髅头 364 | 给前方玩家空的对应装备槽穿戴；没有对象时弹出 | `player.equip.before`、背包事件 |
| 矿车 368、船 369 | 在前方/下方有效铁轨或水面放置真实载具；无有效位置时弹出 | `vehicle.place.before`、探测轨事件 |
| 潜影盒 362 | 放置盒子并恢复 27 格库存和盒子的名称、Lore、属性、自定义数据 | 整批方块、库存事件 |
| 凋灵骷髅头 364 | 优先完成真实灵魂沙 181 的 T 型召唤结构，原子消耗结构生成凋灵 | `entity.spawn.before` 和每一格方块保护事件 |

其他现有物品按普通物品弹出（包括盾、未雕刻南瓜、饮用药水和牛奶桶）。未注册的原版物品/系统，如火焰弹、烟花、经验瓶、喷溅药水、蜂巢采蜜和铁傀儡，仍需先实现该物品/系统；本表不表示完整 Minecraft 协议兼容。行为参考 [Minecraft 官方发射器介绍](https://www.minecraft.net/en-us/article/block-week-dispenser)。

新增事件均已出现在 SDK 常量和 `GET /api/events`，字段只读，before 可取消：

| 前置事件 | 完成事件 | 数据 |
| --- | --- | --- |
| `block.ignite.before` | `block.ignited` | 火源位置、原物品、源容器、点燃归因 |
| `portal.create.before` | `portal.created` | 框架、完整门面坐标、源物品与耐久 |
| `player.equip.before` | `player.equipped` | 目标玩家与账号、装备槽、库存前后快照 |
| `entity.shear.before` | `entity.sheared` | 目标生物、工具、羊毛、库存和耐久快照；同时保留 `animal.shear.before/animal.sheared` |
| `item.bottle.fill.before` | `item.bottle.filled` | 水体位置、输入与返回物品、容器前后快照 |
| `item.egg.hatch.before` | `item.egg.hatched` | 投射物、发射来源、原鸡蛋、数量与幼鸡目标 |
| `entity.grow.before` | `entity.grown` | 幼崽、成长倒计时、状态前后快照 |

`block.dispense.before/block.dispensed` 提供 `behavior`（`spawn_egg/egg_projectile/projectile/flint_and_steel/tnt/bone_meal/shears/equip/bucket_empty/bucket_fill/bottle_fill/shulker_box/wither_structure/container_transfer/drop`）、`origin`、`target`、`actor=server`、`source=dispenser/dropper`、完整 `stack/before/after`、`containerBefore/containerAfter`。输出掉落另提供真实输出 `stack` 和 `input/sourceBefore/sourceAfter`；返回桶/瓶满槽时掉到世界，不丢失物品。多方块操作中的方块事件始终标识实际被保护的那一格，原操作目标保留为 `operationTarget`。

工具磨损只允许修改既有 `item.wear.before.amount`，必须是 0..1000 的整数；TNT 引信只允许修改 `tnt.prime.before.fuseTicks` 并重新验证。非法数值类型取消操作，原因 `plugin.invalid_mutation: field`。Monitor 的取消、字段与动作仍被忽略。

SDK `Context.DispenseBlock(e,x,y,z,expected []*ItemStack)` 复制完整九格快照，host action 为 `dispense_block`。执行时验证世界、整数坐标、机器类型、库存及元数据 CAS，再进入同一个 `dispenseMechanic` 规则；不能通过此动作绕过任何 before。例：从事件 `containerAfter` 解码九格期望值后，按正常 SDK 调用触发另一轮；旧快照重放会被拒绝。

`world-policy` 增加 `blockedDispenseBehaviors`（例如 `["spawn_egg","tnt","flint_and_steel"]`），默认空；Monitor 审计记录上述新完成事件。潜影盒的可选 `Container.item` 与幼鸡的 `GrowthTicks` 兼容旧存档、API v1。盒子破坏时重新打包当前内容并保留原 Lore，而不是从已经被消耗的手持槽恢复。

手动穿戴同样经过 `player.equip.before/player.equipped`，`source=player`、`reason=equip`，包含操作者、目标、源槽和装备槽及两边完整库存快照。玩家装水瓶与饮用沿用瓶子、消费、背包和效果/生命/伤害事件，不通过前端绕过保护。

普通物品元数据仍限 8192 字节；`mcraft:shulker_contents` 使用单独的 512 KiB 内容上限，必须恰好 27 格且每格物品再次校验，拒绝嵌套潜影盒与子物品的保留内容键。SDK/宿主插件行消息上限为 64 MiB，以承载合法的完整库存快照。已有普通物品限制与存档字段保持兼容。

`/heal`、`/feed` 的饱腹状态变更提供 SDK `PlayerFoodChanging` / `PlayerFoodChanged`，即 `player.food.before` / `player.food.changed`。字段只读，before 可取消；快照包含操作者与目标账号、完整物品、食物/饱和度/消耗度前后值，原因 `command_heal` 或 `command_feed`。治疗及清理状态效果还经过原有生命/状态效果事件，所有前置校验通过后才提交。`/clear` 经过原有库存事件，提供完整物品元数据及库存比较保护，取消时不会清除玩家物品。新事件不改变 API v1 旧字段；`world-policy` 的 Monitor 审计成功的饱腹状态修改。

### 批量方块操作、世界快照查询与权限进阶

1. **批量方块变更（Batch Actions）**：
   - SDK 提供 `Context.SetBlocks(world string, changes []BlockChange)`，通过单条 `set_blocks` 动作批量下发多达 1024 个方块变更，极大降低跨进程 IPC 往返开销。
   - 服务端集中执行越界检测、容器阻挡与引信判定，原子应用所有变更并分发一次性区块同步广播。

2. **只读世界快照查询接口**：
   - 服务端提供 REST 端点：`GET /api/rooms/{id}/blocks?minX=...&maxX=...&minY=...&maxY=...&minZ=...&maxZ=...`（单次查询上限 4096 方块），返回 `WorldSnapshot`。
   - SDK 提供 `plugin.QueryBlocks(apiURL, worldID, minX, minY, minZ, maxX, maxY, maxZ)`，插件可快速读取包围盒内各方块 ID 与名称。

3. **权限系统进阶（通配符与角色组继承）**：
   - 权限节点支持层级通配符（如 `world.*` 匹配 `world.build`、`world.break`、`world.interact.door`；`*` 匹配所有权限）。
   - 负权限（如 `-world.break`）享有最高否决优先级，直接覆盖匹配的通配符授予。
   - 支持多层角色组（`RoleGroup`）继承机制（如 `super_vip` 继承 `vip`，`vip` 继承 `player`），递归解析权限并向下合并。`Player` 快照同步附带 `Roles` 列表。

4. **统一 RandomTick 调度循环**：
   - 支持通过游戏规则 `/gamerule randomTickSpeed <n>`（默认 3）动态调节世界随机刻速率。
   - 统一驱动农作物/树苗生长、甘蔗/仙人掌递增、耕地退化补水、树叶自然枯萎、冰雪消融以及火焰蔓延灭火。

### 创造模式客户端破坏预测

客户端可以在点击时预测方块消失，但 Go 服务仍通过原有 `block.break.before`、相关组件保护与物品校验提交破坏，提交后才发送 `block.broken` / `block.changed`。插件取消或校验失败时没有成功事件。预测不会生成掉落、消耗物品或写入服务端存档，插件仍使用 API v1 的既有事件和完整玩家物品快照。

服务端在世界信息中声明 `sv.digAck=true`。客户端可为 `dig s=2` 附加正整数 `q`（最大 2147483647）；服务端在处理后返回 `digack`，含 `q,x,y,z,b,epoch,dim`，其中 `b` 是此时权威方块 ID。这是成功与失败都可能收到的状态校正消息，不是成功事件。超出交互距离或过期 epoch 的请求不会探测世界状态。未声明能力的旧服和未附带 `q` 的旧客户端保持原协议。

### 客户端放置预测与权威确认

世界信息声明 `sv.placeAck=true`。支持此能力的网页在右键放置时先预测建筑方块及必要的多格结构，并立即重绘；背包、掉落、保存和插件业务都仍由 Go 决定。放置与破坏使用同一有序预测层；旧地形快照、较早的确认不能覆盖较新的操作，取消/拒绝恢复对应权威方块，超时和跨维度/窗口切换清理预测。浏览器离线规则与显示共用 WASM，因此直接执行本地规则，不把预测数据当作本地规则的输入。旧服未声明能力时继续使用原广播流程。

普通放置保持事件顺序 `block.place.before` → `inventory.change.before` → 提交 → `block.placed` / `block.changed` → `inventory.changed`。旧坐标、`oldBlock`、`block`、库存 `reason=place` / `count=1` 字段保留，增加只读的 `actor, source, target, slot, face, consumed, stack, before, after`。`count` 表示请求放一个，`consumed` 表示实际扣除数（创造为 0），物品快照携带完整名称、Lore、属性、附魔和命名空间数据。组件继续使用原组件事件。任何 before 取消或校验失败都不会扣除物品或发成功事件。`GET /api/events` 对方块事件列出上下文字段，`mutable` 仍为空；Monitor 只读。SDK `e.BlockPlacement()` 返回独立快照，示例 `inventory-guard` 演示通过自定义数据阻止放置，不在核心硬编码 Lore 规则。

新请求可以附带 `q` 和 `preview:[{x,y,z}]`。后者只选择确认坐标，不决定放置位置或方块 ID。服务端仅接受距离合法、序号 1…2147483647、1…4 个不重复坐标且各自距点击位置曼哈顿距离不超过 2 的确认请求；仍按自身物品、支撑、碰撞、权限及插件规则执行。处理后 `placeack:{q,blocks:[{x,y,z,b}],epoch,dim}` 返回每格的最终状态，成功、拒绝和仅发生交互都可能收到，绝不能把它当成功事件。旧客户端无需这些字段，存档格式不变。

### 放置背包数量预测

Go 服声明 `sv.placeInventoryAck=true` 后，客户端在生存放置时立即显示扣一件，创造模式不扣数。该显示不提交库存，也不触发额外游戏成功事件。取消、校验失败和仅交互均返回完整权威背包；旧服继续原流程，浏览器单机直接执行本地规则。超时撤销显示预留，下一份权威快照仍决定真实数量。

`sinv` 与 `placeack.inventory` 提供会话内递增 `rev`、处理至哪个放置序号的 `placeQ`、处理至哪个快捷栏选择序号的 `selQ`、`epoch/dim`。客户端丢弃旧版本和过期世界快照，移除已结算的预留，再在完整权威背包上叠加尚未确认的扣数；不是“失败加一”。连续操作、拾取、丢弃、旧回包和快捷栏切换不会重复扣数或覆盖较新状态。预留匹配原槽、ID、耐久、附魔和全部元数据；预测为空不能重复使用最后一件。序号是协议校正数据，不写入世界存档，不是插件成功通知。

普通放置的 `inventory.change.before/inventory.changed` 仍沿用原规则，事件目录增加只读的可选放置上下文字段，与方块事件一致。SDK `e.PlacementInventory()` 仅为 `reason=place` 的库存事件提供独立 `PlacementSnapshot`，含真实 `Consumed`、`Stack/Before/After` 和操作者；完整元数据不会因显示预测丢失。非放置库存事件继续各自原有字段。任何 before 取消都不扣真实库存，也不发布 `block.placed` 或 `inventory.changed`；可修改字段列表不变，Monitor 只读。`inventory-guard` 示例在 Monitor 记录成功提交的真实消耗。

### 计分板插件化渲染与官方可配置计分板插件

#### 1. 计分板渲染事件 (`scoreboard.render`)
- **事件名称**：`scoreboard.render`（Go SDK 常量：`api.ScoreboardRendering`）
- **触发时机**：服务端定期（每秒）或玩家状态/延迟发生变更时，为每个在线玩家构建计分板数据包时触发。
- **可取消性**：`Cancellable: true`。若插件调用 `e.Cancel()` 或设置 `e.Data["enabled"] = false`，该玩家将不会接收计分板（前端隐藏）。
- **只读快照**：
  - `e.Player`：目标接收玩家的完整元数据快照（包括名字、UUID、坐标 X/Y/Z、生命值 Health、饱腹度 Food、最大生命 MaxHealth、网络延迟 ping 等）。
  - `e.World`：当前世界/房间元数据（世界名称、游戏模式 Mode、在线人数 Online、维度 Dimension 等）。
- **可修改字段 (`mutable`)**：
  - `title`（string）：计分板主标题。
  - `enabled`（bool）：计分板开启/关闭开关。
  - `entries`（array/slice）：计分板条目列表，每项支持 `name`（名称）、`score`（数值）、`icon`（图标 emoji）、`badge`（右侧文本徽章）、`color`（高亮颜色）、`rank`（排名）、`progress`（0.0~1.0 进度条）。
  - `footer`（map）：计分板底部信息，支持 `server`、`mode`、`ping`、`tag` 等自定义标签。
  - `objective`（string）：活跃计分项标识。

#### 2. 官方独立可配置计分板插件 (`custom-scoreboard`)
- **插件目录**：`plugins/custom-scoreboard/`，源码位于 `backend/examples/custom-scoreboard/main.go`。
- **构建方式**：
  ```sh
  go build -o ../plugins/custom-scoreboard/custom-scoreboard ./examples/custom-scoreboard
  ```
- **配置文件 (`plugins/custom-scoreboard/config.json`)**：
  服主无需重新编译，可随时编辑此文件自定义展示内容与版式：
  ```json
  {
    "enabled": true,
    "title": "⚡ CCRAFT 官方服务器 ⚡",
    "entries": [
      {
        "name": "世界",
        "score": 0,
        "icon": "🌐",
        "badge": "{mode}"
      },
      {
        "name": "在线人数",
        "score": "{online}",
        "icon": "👥",
        "badge": "{online} 人"
      },
      {
        "name": "网络延迟",
        "score": "{ping}",
        "icon": "⚡",
        "badge": "{ping}ms"
      },
      {
        "name": "{player_name}",
        "score": "{health}",
        "icon": "❤️",
        "badge": "YOU",
        "progress": "{health_ratio}"
      },
      {
        "name": "游戏时刻",
        "score": 0,
        "icon": "⏳",
        "badge": "{time}"
      },
      {
        "name": "位置坐标",
        "score": 0,
        "icon": "🧭",
        "badge": "{pos_x}, {pos_y}, {pos_z}"
      }
    ],
    "footer": {
      "server": "game.ccraft.app",
      "mode": "{mode}",
      "tag": "ccraft.app"
    }
  }
  ```
- **丰富占位符支持**：
  - `{player_name}`：当前玩家昵称
  - `{ping}`：实测网络往返延迟 (ms)
  - `{online}`：当前在线玩家人数
  - `{world_name}`：世界名称
  - `{mode}`：世界模式（SURVIVAL / CREATIVE 等）
  - `{health}`：玩家当前生命值（如 20）
  - `{max_health}`：玩家最大生命值
  - `{health_ratio}`：生命值百分比（0.00 ~ 1.00，用于血条进度）
  - `{food}`：玩家饱腹度
  - `{pos_x}`, `{pos_y}`, `{pos_z}`：玩家三维坐标
  - `{time}`：游戏昼夜时间（如 14:30）
  - `{dimension}`：当前维度（overworld, nether, the_end）
- **管理命令**：
  - `/screload`（别名 `/scoreboard-reload`）：管理人员可随时在游戏内重载 `config.json`，无需重启服务。

### 服务端反作弊参数配置化与热重载

服务端运动反作弊（超距交互、超距攻击、移动超速、瞬移拉回、穿墙与滞空飞行检测）全面解耦为可配置系统：
- **配置文件**：通过运行目录下的 `anticheat.json` 或环境变量动态配置：
  - `ANTICHEAT_ENABLED` / `enabled`（bool，默认 `true`）：反作弊系统总开关
  - `ANTICHEAT_REACH_DISTANCE` / `reachDistance`（float64，默认 `6.5`）：玩家方块破坏与交互最大距离（格）
  - `ANTICHEAT_ATTACK_REACH` / `attackReach`（float64，默认 `4.5`）：玩家近战攻击实体最大判定距离（格）
  - `ANTICHEAT_MAX_SPEED` / `maxSpeed`（float64，默认 `8.5`）：玩家水平移动基准速度上限（m/s）
  - `ANTICHEAT_SPEED_TOLERANCE` / `speedTolerance`（float64，默认 `1.8`）：瞬移与网络抖动速度容差倍率
  - `ANTICHEAT_MAX_TELEPORT_DIST` / `maxTeleportDist`（float64，默认 `10.0`）：单次位移合法瞬移距离阈值
  - `ANTICHEAT_NOCLIP_ENABLED` / `noclipCheck`（bool，默认 `true`）：穿墙相交检测开关
  - `ANTICHEAT_FLY_ENABLED` / `flyCheck`（bool，默认 `true`）：无滞空特权飞行检测开关
  - `ANTICHEAT_MAX_AIR_TICKS` / `maxAirTicks`（int，默认 `25`）：允许连续离地无重力滞空的最大 tick 数
- **游戏内管理与控制台命令**：
  - `/anticheat status`：查询当前服务端生效的反作弊参数详情
  - `/anticheat reload`：从 `anticheat.json` 重新加载反作弊配置并即刻生效
  - `/anticheat toggle`：一键开启/临时关闭反作弊检测


## 16 位方块和物品注册表

方块 ID 最大为 `65535`。方块、方块状态和普通物品使用同一个背包 ID 空间，不能冲突；`0` 是空气，`255` 保留为 API v1 回退标记。已有 ID 保持不变。自定义条目默认从 `1024` 分配，也可显式请求可用 ID。注册失败会拒绝启动并保留存档；插件取消注册则跳过该条目，不发送成功事件。

在 `OnEnable` 中调用 `Context.RegisterBlock(BlockDefinition)` 和 `Context.RegisterItem(ItemDefinition)`：

```go
p.OnEnable = func(c *plugin.Context) error {
    c.RegisterBlock(plugin.BlockDefinition{
        Key: "myplugin:ruby_block", Name: "红宝石方块",
        ModelBlock: 21, Hardness: 2, Stack: 64,
    })
    c.RegisterItem(plugin.ItemDefinition{
        Key: "myplugin:ruby_wand", Name: "红宝石法杖",
        ModelItem: 276, Stack: 1, Durability: 200,
    })
    return nil
}
```

Key 的命名空间必须等于 `plugin.json` 的插件 ID。`ModelBlock` 是现有原生方块模型 ID，决定形状、贴图、碰撞、遮挡和发光；`Hardness` 决定挖掘时间。此接口复用现有模型，不支持上传任意模型或新纹理。物品 `ModelItem` 复用现有图标及工具/食物等基础行为；`Durability` 为新物品的耐久，默认 0，耐久物品 Stack 必须为 1。`PlacesKey` 可关联已注册方块。行为扩展继续监听现有放置、破坏、背包、掉落、拾取、使用和伤害事件，Lore 不作为核心硬编码规则。

注册时产生 `registry.register.before`、`registry.registered` 或 `registry.register.rejected`；入服发送注册表前产生可取消的 `registry.sync.before`，提交欢迎消息后产生 `registry.synced`。字段 `reason`、`actor`、`target`、`entry`、`registryVersion` 都只读，Monitor 不能取消。注册期间创建世界/物品的插件动作仍进入常规规则与事件入口。方块放置、破坏和改变事件的 `blockKey`、`oldBlockKey` 标识自定义条目，原来的数字字段、完整物品快照和 API v1 都保留。

服务端将定义和分配结果原子保存到 `DATA_DIR/registry.json`。同一个 Key 的定义、模型和 ID 必须保持稳定；重启和停用插件不会复用其 ID 或删除存档中的方块。不能删除 registry.json 后继续加载自定义世界；未知 ID 会阻止读取，原存档保留。注册表随服务端备份和恢复。插件重载新增注册项时，现有玩家需要重新进入以同步注册表。

通过 `GET /api/registry` 查看已分配 ID；`GET /api/events` 查看事件字段。使用 `c.GiveItem(e, "myplugin:ruby_block", count)` 和 `c.SetBlockKey(worldID, x, y, z, "myplugin:ruby_block")` 可避免依赖自动分配的数字 ID。现有 `GiveStack`、`SetBlock`、`SetBlocks` 支持注册后的 ID 和完整元数据，仍受插件保护。

可运行示例：`backend/examples/block-registry`，构建后位于服务端包的 `optional-plugins/block-registry/`。复制到活动 plugins 目录并重启，OP 使用 `/registrydemo` 领取新方块和物品。示例默认不启用，也不会自动修改世界。

### 插件自定义弹窗 UI 与箱子密码锁（chest-lock）

WebCraft 为服务端插件提供了通用客户端弹窗交互系统（Custom UI System），无需编写前端代码即可在游戏窗口弹出 Minecraft 风格的沉浸式表单对话框，并监听玩家的输入与提交。

#### 1. 协议与 SDK API

- **推送自定义弹窗**：`c.OpenCustomUI(worldID, playerID, ui)` 或 `c.OpenCustomUIFor(e, ui)`
  - `ID`：弹窗唯一标识符（如 `chest_lock:set_password`）
  - `Title`：标题文本（Minecraft 浮雕金字/黑字）
  - `Content`：说明文字
  - `Inputs`：表单输入组件列表（支持 `text`, `password`, `number`，可配置 `Label`, `Placeholder`, `DefaultValue`）
  - `Buttons`：按钮列表（可配置 `submit`, `cancel` 类型与按钮文本）
  - `Data`：插件上下文透传数据（回传时原样携带，方便插件还原状态）
- **关闭自定义弹窗**：`c.CloseCustomUI(worldID, playerID, uiID)` 或 `c.CloseCustomUIFor(e, uiID)`
- **修改告示牌文字**：`c.UpdateSign(worldID, x, y, z, lines)`（免物品与手持校验直接写入木牌内容并广播）
- **代开容器窗口**：`c.OpenContainer(worldID, playerID, x, y, z)`（安全为指定玩家打开箱子窗口）
- **客户端提交事件**：`player.custom_ui.submit`（`api.PlayerCustomUISubmit`）
  - 包含 `uiId`, `button`, `values`（输入项键值对）, `customData`, `epoch`

#### 2. 箱子密码锁官方插件（`chest-lock`）

示例源码位于 `backend/examples/chest-lock/main.go`，配置文件位于 `plugins/chest-lock/plugin.json`：
- **放置木牌贴箱子**：当玩家在箱子侧面放置木牌时，核心在 `block.placed` 事件中带入 `placedOnBlockKey = "Chest"` 及箱子坐标；插件拦截并弹出密码设置表单；
- **设置密码并更新木牌**：玩家提交密码后，插件记录锁信息（保存至 `data/plugins/chest-lock/locks.json`），并调用 `c.UpdateSign` 将木牌覆写为 `[私有密码箱]`；
- **开箱拦截与密码验证**：任何玩家右键开箱时，插件监听 `inventory.open.before` 并取消常规开箱，随后向玩家弹出密码输入表单；输入正确后通过 `c.OpenContainer` 为其打开箱子；
- **防盗与解锁**：箱子和木牌受破坏保护（其他人无法破坏）；仅放置木牌的主人打掉木牌后，箱子方可解除密码保护。


## 漏斗、自动转移与动物繁殖（API v1，2026-10-11）

漏斗原生方块 ID 为 **193**，物品 ID 为 **385**。这两个原先未使用的核心 ID 现在保留；不改变既有方块、物品、世界 ID 或区块格式。原版 16×16 材质、合成配方、朝向放置、联机状态同步和浏览器单机注册、五格容器界面同步更新。Go 私人服务器与浏览器离线模拟均支持自动转移与动物繁殖；Go 插件进程仍仅在联机服务端运行。红石供电时锁定，成功转移后冷却 400ms；每次容器转移一件，掉落物可按空余容量部分吸取。实际堆叠上限和物品的名称、Lore、属性、附魔、自定义数据共同决定是否能合并。漏斗插入/抽取遵守熔炉和酿造台的方向与槽位规则；末影箱及临时窗口不参与自动转移。浏览器现有容器范围及基础供电范围见 `BROWSER_LOCAL.md`，未实现的复杂红石/酿造玩法仍由 Go 提供。

| Before / 完成事件 | 触发与原因 |
| --- | --- |
| `inventory.move.before` / `inventory.moved` | 漏斗 `hopper_push`、`hopper_pull`；投掷器 `dropper_transfer` |
| `inventory.pickup.before` / `inventory.picked_up` | 漏斗吸取掉落物 `hopper_pickup`，支持部分吸取 |
| `animal.feed.before` / `animal.fed` | 玩家喂成年动物 `animal_feed` |
| `animal.breed.before` / `animal.bred` | 两只同种成年动物繁殖 `animal_breed`（保留原事件名与 `mob_id`、`mob_type`、`baby_id` 旧字段） |

以上 Before 可取消，所有字段只读（`mutable` 为空）；Monitor 的取消、字段修改、动作均不生效。`inventory.change.before` / `inventory.changed`、`item.consume.before` / `item.consumed` 仍按实际资源变化触发，创造成员喂食不扣物品。仅在所有 Before、容量、位置、原快照校验通过后提交，不在准备阶段修改任何库存、掉落物、父母状态或生物 ID。成功事件只在提交后发送。漏斗一轮推送与吸取是两笔独立事务，取消其中一笔不会撤销另一笔已成功的操作。

自动转移数据含稳定 `reason`、`actor: server`、`source`、`origin`、`target`、`initiator`、槽位、数量及完整 `stack` 快照；容器转移含 `sourceBefore/After`、`destinationBefore/After`，吸取含原掉落物 `before/after`、`entityId`、`remaining`。完成后立即同步观看容器的玩家（包含大箱子两侧）和掉落物剩余数量。快照保留所有元数据，无法通过修改事件中的物品绕过守恒校验。

繁殖支持牛/羊（小麦）、猪（胡萝卜）、鸡（种子）。分别喂两只同种成年动物，在约 3 格内且无遮挡时产生一只幼崽；首只喂食后进入 30 秒可繁殖状态，繁殖后双亲冷却 5 分钟，幼崽成长 20 分钟。这些时长按世界模拟时间推进。附近已喂食配偶的第二次喂食与出生是同一事务，出生被保护插件取消、位置无效或生物容量不足时，第二份食物和双亲状态均保留。远处第一次喂食是已经完成的独立操作；动物后来靠近时会单独尝试繁殖，不再扣食物，取消出生不会退回之前成功喂食的食物。幼崽的 Baby 标记与成长时间在 `entity.spawn.before` 前已经设置，避免生成事件把幼崽视为成年。

繁殖事件含 `parent/mate/baby`、`parentBefore/After`、`mateBefore/After`、双方 `food/mateFood`、`breeder/mateBreeder` 完整来源和物品快照。待繁殖的食物快照、繁殖状态、冷却及幼崽成长时间持久化为可选字段，旧存档默认零值兼容，不升级存档格式。

SDK `Context.ActivateHopper(e,x,y,z,expectedFiveSlots)` 和 `Context.FeedAnimal(e,entityId)` 深复制预期快照，调用正常游戏规则和所有保护事件。过期快照、错误槽位、死亡/不可建造/距离过远、冷却或红石锁定均拒绝。不要在相应完成事件中无限重复提交相同动作。

`world-policy` 示例新增 `disableHopperTransfers`、`disableAnimalFeeding`、`disableAnimalBreeding`（默认均为 false）；并示范通过 `world-policy:lock_transfer=true` 命名空间数据保护物品自动转移。Lore 仍只是文本；插件可自行监听上述事件解释自己的 Lore，核心不匹配任何硬编码 Lore 文本。

`chest-lock` 示例同时监听自动转移 Before：锁定箱子及其连体箱的输入/输出均阻止，自动化没有玩家身份，不能使用 OP 开箱旁路。按事件世界匹配锁记录，避免误拦截其他世界同坐标的库存。

浏览器事件目录新增上述同名自动化、动物成长、经验和供电事件；本地快照同样递归只读，before 可取消，失败不发送成功事件。离线订阅入口为 `LocalSimulation.events.on`；`automation.feed` / `automation.activate` 与玩家操作、定时器共用事务入口，并防止同步回调重入。时长、食物、动物空间参数通过 `rules/automation.js` 导出到规则 JSON，以 Go 一致性测试校验。无需更改 API v1、Go SDK 现有动作、`GET /api/events` 的名字或旧存档格式。


## Spigot 26.3 自然生物生成

完整规则、线上旧配置与新版默认值的区别见 [NATURAL_SPAWNING.md](NATURAL_SPAWNING.md)。自然生成基准怪物 70、动物 10，间隔 1/400 Minecraft ticks；全局有效统计区块除以 289，实际尝试范围单独受 Spigot 范围与视距约束；新增玩家局部容量校验。默认全局采样/成功预算从 32/4 改为 0/0（按区块和分类规则），旧显式配置仍有效。全来源单维度安全上限仍为 1024，不删除现有世界数据。

光照检查包括透光玻璃、侧向天光、树叶衰减、全部已加载地形光源与动态红石灯。晴天露天不刷新普通自然怪物，白天真正黑暗的洞穴仍可刷新；雷暴使用上游专门条件。选种与群体大小使用官方生物群系权重，主世界末影人恢复自然选种，移除旧自动删除规则。新区块初始动物为独立 `chunk_generation` 来源，由 `MOB_GENERATE_CHUNK_ANIMALS` 控制，遵守相同可取消生成入口及安全上限；加载旧区块和恢复存档不会重复生成。

API v1 的生命周期事件与旧字段不变。新增只读审计字段 `biome`、`skyLight`（原始天光）、`blockLight`、`localLight`（日夜/天气处理后的局部光）、`minecraftTick`、`spawningRules=spigot-26.3`；`EntitySpawning/EntitySpawned` 的 SDK `MobLifecycle()` 提供同名字段。`GET /api/server` 保留 `mobSpawning` 并新增 `mobSpawningRules`，公开参考版本、逻辑频率、光照阈值与未实现分类。`GET /api/events` 同步字段，字段不可被插件改写，Monitor 不可取消、改值或发动作。提交前重新验证自然位置，失败、取消都不发完成事件。

`world-policy` 的 `blockedNaturalMobTypes` 继续可用，新增 `blockedNaturalBiomes`，同时保护周期与新区块初始生成；默认空列表不影响玩法。显式持久动作继续使用 `SpawnPersistentMob` 与同一生成入口。存档保持现有版本及兼容规则，账号、世界、物品元数据与进度均保留。未实现的水生/蝙蝠等分类以及原版完整地形、结构和特殊刷怪规则，详见上述规则文档，不宣称整个 Minecraft 完全一致。

## 维度传送指令与插件动作

OP（或具有 `server.admin` 权限的玩家）可使用 `/dimension <overworld|nether|end> [玩家]`，简写 `/dim`。例如 `/dim end` 进入末地，`/dim 地狱` 进入下界，`/dim 普通世界` 回主世界；`/dim nether 玩家名` 可传送同一世界任意维度中的在线玩家。支持 `@s`、账号 ID、`minecraft:overworld`、`minecraft:the_nether`、`minecraft:the_end`。控制台选择世界和在线玩家后可执行；单世界服也支持 `dimension end 玩家名` 自动选择目标。

该指令在当前世界组内切换维度，落点为目标维度出生点附近的安全位置。搜索水平半径通常最多 8 格，下界最多 64 格以覆盖封闭洞穴，使用真实方块（包含玩家修改），要求实体脚部和头部为空气、脚下为实心地面，避开岩浆方块；下界落点低于基岩顶层。不会为已有末地出生平台重建/清空建筑；找不到位置会失败并提示整理出生区域。它不会触发击败末影龙或观看终末之诗。游戏背包、名称、Lore、属性、自定义数据、生命、经验和效果继续保留，维度与坐标沿用原玩家存档格式，重连时按该维度恢复。客户端聊天支持指令、维度与玩家补全。

沿用 API v1 `player.dimension.before/changed` 与 `player.teleport.before/teleported`。两个 before 均可取消，无可修改字段；只有完成位置和维度切换后发成功事件。乘坐载具或垂钓时还经过原有离开载具/收杆 before，取消时保持原维度、坐标和载具状态，命令也不会发 `command.executed`。跨维度 `/tp` 同样传递取消结果。

SDK `e.PlayerTransfer()` 返回脱离事件数据的只读快照：保留旧 `fromDimension/dimension/x/y/z/reason`，新增 `fromWorldId/worldId/fromX/fromY/fromZ/actor/actorPlayer/target/targetAccountId/stack/source`。`e.Player` 始终是被传送玩家，包含完整背包元数据；`stack` 是其手持物品完整快照。命令原因是 `command` 或 `console`，插件为 `plugin`，旧传送门/复活仍保留 `dimension_transfer`。字段目录由 SDK 同步公开到 `GET /api/events`；Monitor 只读，其取消、字段修改和动作均被忽略。

`c.ChangeDimension(e, "nether")` 发送 `change_dimension` 动作（`worldId` 为玩家当前维度，`playerId` 为目标在线玩家，`dimension` 为目标维度），使用相同安全落点和插件保护入口。旧 `c.Teleport` 动作保持当前维度内的坐标传送语义。示例 `world-policy` 可通过可选 `blockedDimensions: ["end"]` 禁止进入指定维度，并审计 `PlayerDimensionChanged` 完成事件。

适用生存世界和无限世界（包括玩家创造模式）；旧版有限创造世界沿用单维度客户端，指令会明确拒绝，不会转移玩家到无法显示的地形。


## 经济系统与玩家安全交易系统

### 1. 经济持久化与转账

服务端集成基于 SQLite（`modernc.org/sqlite`，纯 Go 无 CGO 依赖）的持久化经济系统，数据安全落盘于 `<dataDir>/economy.db`。支持高并发事务与 WAL 日志模式。
- **初始资金**：新玩家首次进服自动创建账户，赠送初始金币 $1000.00。
- **玩家指令**：
  - `/balance`（简写 `/bal` 或 `/money`）：查询当前金币余额；`/balance <玩家名>` 查询指定玩家余额。
  - `/pay <玩家名或ID> <金额>`：向在线玩家转账。执行前触发 `economy.pay.before` 事件。
- **OP 管理指令**：
  - `/eco give <玩家> <金额>`：管理员向玩家发放金币。
  - `/eco take <玩家> <金额>`：管理员扣除玩家金币。
  - `/eco set <玩家> <金额>`：管理员设置玩家金币余额。
  - `/eco bal <玩家>`：查询指定玩家金币余额。
- **插件扩展事件**：
  - `economy.balance.before` / `economy.balance.changed`：余额变更前后事件，before 可取消，可修改 `amount`。
  - `economy.pay.before` / `economy.pay`：转账前后事件，before 可取消（用于风控冻结），可修改 `amount`（用于征收转账税或手续费）。插件示例见 `examples/economy`。

### 2. 玩家面对面安全交易系统

支持玩家之间通过图形面板进行物品与金币的安全交换，具备完善的防欺诈掉包保护与原子事务结算机制。
- **邀请流程**：
  - 玩家输入 `/trade <玩家名或ID>` 向目标玩家发送交易邀请。
  - 被邀请玩家屏幕右上角弹出 Minecraft 浮雕风格交易邀请卡片，显示：“玩家 XX (ID: 1) 邀请你进行交易”，并提供【接受交易】与【拒绝】按钮（或在聊天框输入 `/trade accept` / `/trade deny`），邀请有效期 60 秒。
- **交易面板（物品与金钱双向出价）**：
  - 接受邀请后，双方同步打开 Minecraft 风格全屏半透明交易面板。
  - **双栏出价区**：左侧为“我的出价”（8 个物品槽位 + 金币出价输入框），右侧为“对方出价”（8 个物品槽位 + 对方出资金额展示）。
  - **物品交易**：面板下方展示玩家自身背包（36格），点击或拖拽背包物品即可上架至交易栏；点击已上架的物品可取回背包。
  - **金钱出价与实时校验**：玩家在输入框输入出资金额前，客户端与服务端双重校验玩家是否有足够余额。若超出余额，输入框红字高亮并禁用出资按钮，服务端亦做强制预校验拒绝超出金额。
- **两阶段确认与防欺诈机制 (Anti-Scam)**：
  - **阶段一（锁定出价）**：双方输入金额并放置好物品后，各自点击【确认出价 (锁定)】。锁定后物品与金额被封存，防止误触。
  - **掉包防欺诈自动重置**：若在交易过程中任何一方尝试修改、添加、取回物品或变更出资金额，服务端的双重锁定状态与同意状态立即被强制复位为 `false`，界面恢复未锁定状态，彻底杜绝掉包欺诈。
  - **阶段二（双方锁定后弹出同意按钮）**：**当两边用户都点击确认锁定之后**，面板中央醒目弹出【★ 双方出价已锁定！是否同意进行最终交易？★】确认弹窗与金色【同意交易】大按钮。
  - **原子事务结算**：两边用户均点击【同意交易】后，服务端在单个事务中原子完成金币互转与物品互换，播放经验升级音效并关闭面板，向双方聊天框发送结算清单；中途任何一方取消或掉线，交易物品全额无损退回背包。


## 头顶名牌控制与顶部图片称号系统

### 1. 功能概述

系统支持服务端与插件统一控制玩家头顶名称标签（Nametag）的前缀（Prefix）、后缀（Suffix）、显示名称（DisplayName）以及正上方的**图片徽章与文字称号系统（Title System）**。
- **名牌前缀与后缀**：完全支持 Minecraft 经典颜色代码（`§0`~`§f`, `§r`, `&0`~`&f`），如 `§6[VIP] §fsky §a[Lv.99]§r`。
- **顶部图片称号系统**：在玩家头顶名字正上方居中渲染半透明发光胶囊底框，展示精美图片/像素徽章（Icon/Badge）及专属称号文字。
  - **内置 10 款经典像素徽章**（零延迟即开即用）：`crown`（金色皇冠）、`dragon`（末影龙印）、`vip`/`star`（璀璨之星）、`sword`（钻石神剑）、`shield`（守卫神盾）、`diamond`（璀璨钻石）、`fire`（烈焰流光）、`heart`（荣耀之心）、`builder`（工匠神锤）、`wings`（光辉天使之翼）。
  - **自定义图片 URL 支持**：支持传入 HTTP/HTTPS 外部图片地址或本地图片路径，客户端自动异步请求加载并缓存渲染。
- **多视角支持**：不仅其他联机玩家可见，当玩家按 F5 切换到第三人称视角时，头顶亦会实时渲染自身的称号与前后缀。
- **数据持久化**：玩家或管理员设置的名牌与称号持久化保存于 SQLite 数据库（`player_nametags` 表），断线重连与服务器重启数据不丢失。

### 2. 指令支持

- `/prefix [玩家] <前缀>`：设置前缀文本（支持颜色代码，普通玩家可设置自己，管理员可为他人设置）。
- `/suffix [玩家] <后缀>`：设置后缀文本（支持颜色代码）。
- `/title [玩家] <图标> [称号文字]`：设置顶部图片称号（如 `/title crown 荣耀王者` 或 `/title dragon 末影终结者`）。
- `/nametag clear [玩家]`：清空名牌与称号。
- `/nametag info [玩家]`：查看名牌与称号当前配置状态。

### 3. 插件扩展事件与动作

- **`player.nametag.render` (`api.PlayerNametagRendering`)**：
  - **触发时机**：当玩家加入游戏或更新名牌时，服务端触发生命周期渲染事件。
  - **可修改字段**：`prefix`（前缀）、`suffix`（后缀）、`displayName`（显示名称）、`nameColor`（名称基色）、`title`（称号文本）、`titleColor`（称号颜色）、`titleIcon`（称号图片/徽章）、`titleAnimation`（动画类型）。
  - **只读字段**：`player`（玩家名称）、`id`（实体ID）、`account`（账号SID）、`isOp`（是否为管理员）。
- **`player.nametag.changed` (`api.PlayerNametagChanged`)**：
  - **触发时机**：玩家名牌或称号成功变动后触发广播与通知（只读监控）。
- **插件动作（Action）**：
  - 动作类型：`set_nametag`。
  - SDK 接口：
    - `c.SetPlayerNametag(worldId, playerId, prefix, suffix)`
    - `c.SetPlayerNametagFor(worldId, playerId, prefix, suffix, title, titleIcon)`
    - `c.SetPlayerNametagFull(worldId, playerId, action)`
  - 示例插件参见 `backend/examples/titles/`。

---

## 商店系统插件与市场交易（Shop & Market Plugin）

### 1. 功能概述

系统提供统一的**商店与贸易集市插件系统（Shop System）**，支持高度自定义的商品与回收价格配置、分类管理，并与服务器 SQLite 经济数据库无缝联动，拥有沉浸式的原生体素质感独立 GUI。
- **配置化商品价格与分类**：
  - 支持在 `config.json`（或 `shop.json`）中自由配置每个商品的 `buyPrice`（购买单价）与 `sellPrice`（出售回收单价）；若单价 `<= 0` 则自动关闭对应方向的交易通道；
  - 支持自定义分类（如 `ores` 矿石、`blocks` 建筑、`farming` 农牧食物、`tools` 武器工具、`redstone` 红石机械、`misc` 杂项道具等）；
- **独立现代化 GUI 界面**：
  - 玩家在游戏内输入 `/shop` 即可打开独立商店面板；
  - 包含【🛒 购买商品 (Buy)】与【💰 出售商品 (Sell)】两大标签页自由切换；
  - 支持分类过滤器一键筛选；
  - 32x32 物品像素高清图标实时预览；
  - 数量快速调节器（`-10`、`-1`、输入框、`+1`、`+10`、`[组]`）；输入框采用高对比度暗黑底色与亮金色字体，解决浅色背景白字看不清的问题；
  - 实时小计计算与钱包余额同步；
  - 出售模式提供“⚡ 一键出售背包回收物”功能，瞬间变现背包杂物；
- **经济系统挂钩与原子安全**：
  - 购买时严格校验钱包余额并原子扣除（`economyDB.Withdraw`），若背包空间已满，超出部分安全掉落在玩家脚下并进入恢复队列，绝不吞物；
  - 出售时严格按实际拥有的物品数量扣减，并存入收益（`economyDB.Deposit`）；
  - 支持快捷指令：`/shop buy <商品名/ID> [数量]`、`/shop sell <商品名/ID|hand|all> [数量]`、`/shop reload`（管理员重载配置）；

### 2. 插件事件扩展

- **`shop.buy.before` (`api.ShopBuying`)**：
  - **触发时机**：玩家在商店点击购买、扣款提交之前触发；
  - **可取消**：是（`e.Cancel(reason)` 可拦截阻止本次购买，并将拦截理由反馈给玩家）；
  - **可修改字段**：`count`（购买数量）、`price` / `unitPrice`（支持插件动态给予折扣、VIP 优惠或限购调整）；
  - **只读字段**：`player`、`playerId`、`itemId`、`itemKey`、`itemName`；
- **`shop.bought` (`api.ShopBought`)**：
  - **触发时机**：扣款成功且物品发放完成之后触发的事实通知（用于记录审计日志、成就进度或数据统计）；
- **`shop.sell.before` (`api.ShopSelling`)**：
  - **触发时机**：玩家出售商品、扣除背包与发放金币之前触发；
  - **可取消**：是；
  - **可修改字段**：`count`、`price` / `unitPrice`；
- **`shop.sold` (`api.ShopSold`)**：
  - **触发时机**：出售结算成功后的事实通知；
- **示例插件**：源码位于 `backend/examples/shop/`，二进制位于 `plugins/shop/`。

---

## 领地保护系统插件（Residence Plugin）

### 1. 功能概述

系统提供经典的 **Residence 领地保护系统插件**，允许玩家通过经济系统的金币以当前自身坐标为中心购买受保护的立方体领地空间，并提供细粒度的权限标志与成员管理。

- **三档标准规格与立体正方体覆盖**：
  - 提供 **10\*10**、**50\*50**、**100\*100** 三种平面规格选购；
  - **定价机制**：每一格领地单价为 **$10**，以**平面面积**计算费用（10\*10=$1,000，50\*50=$25,000，100\*100=$100,000）；
  - **空间覆盖**：以当前玩家所在坐标为中心，以**正方体立方体**空间计算覆盖范围（包含上下垂直高度）。
- **三种领地类型与 3D 范围光圈**：
  - **蓝色光圈**：公共领地（`isPublic: true` / `flags.public: true`），**仅管理员可创建、切换或管理**；
  - **绿色光圈**：自己的领地（非公共领地且归属当前玩家）；
  - **黄色光圈**：别人的领地（非公共领地且归属其他玩家）。
- **权限与标志控制（Residence Flags）**：
  - **公共领地（public）**：仅管理员可设置，将领地设为蓝色光圈的公共领地；
  - **建筑权限（build）**：默认仅领地主人及白名单成员可破坏/放置方块，未授权者在领地内破坏或放置会被直接拦截；
  - **准入控制（enter / move）**：支持将领地设置为私密领地，未授权访客在试图跨入领地时会被自动阻止并拉回；
  - **刷怪控制（monsters）**：支持在领地内禁止刷新僵尸、骷髅、苦力怕、末影人等敌对怪物；
  - **战斗与容器（pvp / container）**：支持领地内禁用 PVP 攻击与箱子容器防盗。
- **跨界提示与成员白名单**：
  - 玩家跨越领地边界时，系统会自动发送进入欢迎语与离开提示（支持自定义提示文案）；
  - 主人可针对单个玩家授权特定权限（如 `/res pset <玩家名> build true`）。

### 2. 用户指令列表

- `/res help`：查看领地帮助菜单；
- `/res select <10|50|100>`：预览指定规格的范围边界、平面面积与购买费用；
- `/res create <领地名> <10|50|100> [public]`：以当前脚下为中心扣除金币并购买领地（`public` 仅管理员可用）；
- `/res public [领地名] <true|false>`：切换公共领地状态（仅管理员可用）；
- `/res info [领地名]`：查看领地主人、中心点、边界坐标、全局开关与成员白名单；
- `/res list`：列出自己拥有的所有领地；
- `/res tp <领地名>`：安全传送到指定领地的中心点；
- `/res set <public|monsters|enter|build|pvp|container> <true|false>`：站在领地内快速修改当前领地的全局开关（`public` 仅管理员可用）；
- `/res pset [领地名] <玩家名> <build|enter|container> <true|false>`：设置指定玩家在领地内的具体权限；
- `/res message <enter|leave> <文本>`：自定义进出领地时的提示消息；
- `/res remove <领地名>`：删除领地并退还 80% 购地款项。

### 3. 示例源码与配置

- 源码位于 `backend/examples/residence/`；
- 插件二进制位于 `plugins/residence/residence`；
- 数据持久化存储于 `data/plugins/residence/residences.json`。



