# 服务端运营与区块世界

Go 后端负责世界、物品、伤害、权限与存档。前端可单独部署。管理页面为 `/admin`，使用普通账号登录，只有运营权限账号可以操作。现有默认世界的账号所有者会在首次升级时成为管理员；新安装通过本地 `mcraftctl op 用户名` 指定管理员。

## 构建与启动

新私服默认生存、空背包；OP 通过进程控制台或 `mcraftctl op` 授予，按核心固定账号 ID 存储。支持 `gamemode survival|creative [玩家]`、`defaultgamemode`、`deop` 和 `stop`；默认模式只影响首次加入的玩家，各玩家模式独立持久化。前端菜单向 OP 提供模式切换；服务端再次校验权限。新增 OP 和默认模式 before/完成事件供插件取消与审计。下载部署包与公网配置见 [SERVER_DISTRIBUTION.md](SERVER_DISTRIBUTION.md)。

在项目根目录运行 `npm run plugins:build`、`npm run build:frontend`、`npm run start:backend`，另一个终端运行 `npm run start:frontend`。Node 20+，插件冒烟测试需要 Node 22+。后台管理与游戏共享登录 Cookie。

构建控制台：在 `backend` 运行 `go build -o bin/mcraftctl ./cmd/mcraftctl`。默认后端地址为 `http://127.0.0.1:8081`，数据目录为项目的 `data`。可通过 `--url`、`--data` 指定。CLI 自动读取数据目录中权限为 0600 的 `admin.key`，不要公开或放入前端。

```sh
backend/bin/mcraftctl --data ./data op sky
backend/bin/mcraftctl --data ./data status
backend/bin/mcraftctl --data ./data whitelist on
backend/bin/mcraftctl --data ./data whitelist add 玩家账号
backend/bin/mcraftctl --data ./data ban 玩家账号 原因
backend/bin/mcraftctl --data ./data pardon 玩家账号
backend/bin/mcraftctl --data ./data permission role 玩家账号 moderator
backend/bin/mcraftctl --data ./data permission deny 玩家账号 server.backup
backend/bin/mcraftctl --data ./data maintenance on 服务器维护中
backend/bin/mcraftctl --data ./data save-all
backend/bin/mcraftctl --data ./data backup
backend/bin/mcraftctl --data ./data plugins reload
backend/bin/mcraftctl --data ./data plugins disable world-policy
```

命令名、用户名、原因都使用空格分隔。后台提供在线玩家、全局封禁/白名单、维护、角色与权限节点、插件启停与重载、自动保存间隔、自动备份周期/保留数量、备份下载及审计记录。默认自动保存 5 秒、备份 15 分钟、保留 7 个自动备份；手动备份不自动清理。维护与白名单控制新连接，已有玩家可通过 kick 下线。管理员仍受到全局封禁。管理员可选择世界与在线玩家执行 `/time`、`/give`、`/tp`、`/gamerule` 等游戏命令。

角色 player、moderator、admin。moderator 默认有 status/kick/ban/whitelist。允许节点支持 `*` 与 `server.*`；拒绝节点优先。插件命令也可以声明命名空间权限节点。设置提交带 revision 校验，拒绝旧版本，前端保存时读取最新权限与封禁信息。Cookie 写操作验证 Origin；控制台密钥只用于受信任的运营工具。插件是本机可执行程序，拥有进程权限，应只安装可信插件。

## 备份和离线恢复

ZIP 包包含账号、世界、各维度区块、服务器配置与插件数据；不包含管理密钥、插件可执行文件、审计日志。生成前刷新内存状态。恢复前校验路径、大小、账号配置和区块内容，另外创建当前状态备份；文件替换失败会回滚。

先正常停止后端，再运行以下命令，随后重新启动：

```sh
backend/bin/mcraftctl --data ./data --confirm restore manual-实际时间.zip
```

只允许恢复数据目录 `backups` 下的 ZIP。恢复会把账号、世界、插件数据与设置恢复至该时间点。保持原 `admin.key`。同一数据目录的第二个后端会被文件锁拒绝。Unix 崩溃后锁自动释放；Windows 的独占锁文件在确认没有服务进程后需手动清理。备份应另行复制到其他磁盘或远程存储；当前实现只提供本机备份。

## 无限探索与存档

`INFINITE_WORLDS=true` 默认启用。新世界使用确定性 `noise-v1`：多层噪声、海陆、温湿度生态群系、山地、地下洞穴、矿脉和树木，按玩家周边按需生成。坐标支持负数，实际边界为 X/Z ±30,000,000，高度仍为 128。前端使用浮动原点，服务端与插件始终使用真实世界坐标。

旧世界自动升级时备份原 JSON。原有区域保留原生成器与编辑；向区域外探索才使用新生成器，交界处可能出现高度变化。原有账号、背包、容器、物品 Lore/属性、自定义数据、维度和进度保留。

每个区块为 16×16×128。目录结构：

```text
data/rooms/<世界ID>.json                 # 玩家、世界设置与进度
data/worlds/<世界ID>/<维度>/region/
  r.<区域X>.<区域Z>/c.<区块X>.<区块Z>.mcc
```

每个区域目录分组 32×32 个区块。MCC v1 为 gzip JSON，保存基础地形、校验和、修改、完整容器物品、编辑者与方块附加状态，独立原子写入。正常探索的缓存目标为每维度 512 个区块；玩家附近及活跃物理、实体区块受到保护，人数多时可超过此目标。未改变区块不重复写盘。腐败区块会报错并保留原文件，不会静默覆盖。管理撤销会扫描所有已保存区块，超大世界的撤销可能较慢。

这是 Minecraft 式生成和区块架构，**不是原版同种子地形或 Anvil `.mca`/NBT 格式兼容**。提高世界高度、更丰富结构、异步区块生成与多进程分服可在这个基础上继续开发。

## Linux 部署

将项目和当前平台构建放在 `/opt/webcraft`。创建 webcraft 系统用户、`/var/lib/webcraft` 数据目录并赋予该用户权限。`deploy/mcraft.service` 为 systemd 示例；`/etc/webcraft/server.env` 至少配置 `ALLOWED_ORIGINS=https://你的域名`。使用现有 HTTPS Nginx 服务块合并 `deploy/nginx.conf.example`，静态目录使用 `dist/frontend`。这些配置是部署模板，本次在 macOS 本地验证，未实际安装 Linux 服务。

日志通过 stdout/stderr 交由 systemd/journald 管理。`GET /healthz` 返回服务及插件状态；`GET /api/admin/status` 返回 TPS、刻耗时、Go 内存/协程、在线玩家与缓存区块。TPS 目标为每秒 10 个服务刻，每刻执行两个游戏刻。大型同步备份和首次区块生成仍可能产生短暂停顿，生产应根据玩家数量做容量压测。

新增事件与可修改字段详见 `PLUGINS.md` 和 `GET /api/events`。完成事件只在提交后触发，保护插件可以取消操作；Monitor 为只读。
