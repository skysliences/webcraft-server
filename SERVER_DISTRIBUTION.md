# 部署自己的 WebCraft Go 私人服务器

玩家在统一游戏网站注册、登录，取得固定玩家 ID，再选择本地游戏或加入私人服务器。服主下载 Go 服务端并部署到自己的电脑或服务器；其他玩家仍在统一网站输入这台 **Go 游戏服的地址** 加入。服主只需要运行 Go 服务端，无需安装 Go、Node.js，也无需部署网页或账号服务。

每台游戏服保存自己的世界、背包、OP、插件和配置。新数据目录自动生成一个默认生存的无限主世界，同一存档支持下界、末地，区块按需生成。账号 ID 来自网站使用的同一个核心账号服务，跨服保持不变；游戏权限和进度分别保存。首次加入不会自动成为 OP。

## 1. 下载并解压

从游戏首页点击“下载 Go 服务端”，或访问网站的 `/server-downloads.html`。按**运行服务器的机器**选择 Linux x64（amd64）、Linux ARM64 或 macOS Apple Silicon（arm64）；不要按玩家电脑的平台选择。当前下载包不提供 Windows 可执行程序。

Linux x64 示例：

```sh
tar -xzf webcraft-server-linux-amd64.tar.gz
cd webcraft-server-linux-amd64
cp server.env.example server.env
```

Linux ARM64、macOS 替换对应的包名。下载页提供大小、SHA-256 和 `SHA256SUMS`。在放有下载包与校验文件的目录中，Linux 可运行 `sha256sum -c SHA256SUMS --ignore-missing`；macOS 可运行 `shasum -a 256 webcraft-server-darwin-arm64.tar.gz`，与页面公布值核对。

包内主要文件：

| 文件 | 用途 |
| --- | --- |
| `start.sh`、`server.env.example` | Go 服务启动器与配置模板 |
| `bin/mcraft`、`bin/mcraftctl` | 已编译游戏服与本地管理工具 |
| `engine.wasm` | 服务端世界引擎 |
| `plugins/`、`optional-plugins/` | 已编译示例插件与可选二次登录插件 |
| `plugin-sdk/` | SDK 与插件示例源码 |
| `nginx.conf.example` | 公网 HTTPS 与 WebSocket 代理示例 |
| `data/` | 首次启动后生成，保存本服配置、玩家和区块 |

`frontend-runtime/`、`start-frontend.sh` 是旧部署兼容资源，私人服运行 `./start.sh` 即可。部署统一客户端的网站运营者另见 [CLIENT.md](CLIENT.md)。

## 2. 准备地址并登记核心身份

先确定三个地址。以下域名均为示例，必须替换成实际地址：

| 地址 | 示例 | 谁部署 |
| --- | --- | --- |
| 统一客户端网站 | `https://play.example.com` | 网站运营者，所有玩家访问这里 |
| 核心账号服务 | `https://login.ccraft.app` | 官方/网站运营者，统一注册登录与固定 ID |
| 你的 Go 游戏服公开地址 | `https://private.example.com` | 服主，本服 `/api/`、`/ws/` 可访问 |

在下载页打开“核心账号中心”，使用同一个核心账号登录；若账号中心要求登录，再输入已有账号，无需重新注册。完成玩家名字设置后，在“服主接入自己的游戏服务器”表单填写：

1. **服务器名称**：供你识别这台私服。
2. **统一游戏网站地址**：填写玩家使用的网站首页，例如 `https://play.example.com`。
3. **本服 Go 后端回调地址**：填写你自己的公开游戏服地址加 `/api/auth/callback`，例如 `https://private.example.com/api/auth/callback`。

点击“登记游戏服务器”，页面返回 `AUTH_ISSUER_URL`、`AUTH_SERVER_ID`、`AUTH_PUBLIC_KEY`、`GAME_PUBLIC_URL`、`AUTH_CALLBACK_URL` 五项。把这些值复制到 `server.env`；不要自行编造服务器 ID，也不要使用另一套核心服务的公钥。每个独立游戏服登记自己的 ID 和回调。

登记信息与本服配置必须完全一致，包括协议、主机与端口。回调必须以 `/api/auth/callback` 结尾；网站地址必须为首页 origin，不包含 `/r/...`、`/play/...`、查询参数或用户名密码。

## 3. 配置并启动 Go

公网部署的 `server.env` 示例：

```dotenv
ACCOUNT_MODE=central
AUTH_ISSUER_URL=https://login.ccraft.app
AUTH_SERVER_ID=复制账号中心返回的服务器ID
AUTH_PUBLIC_KEY=复制账号中心返回的公钥
GAME_PUBLIC_URL=https://play.example.com
AUTH_CALLBACK_URL=https://private.example.com/api/auth/callback
ALLOWED_ORIGINS=https://play.example.com
AUTH_COOKIE_SECURE=true

# Nginx 与 Go 同机：Go 只监听回环，公网通过 HTTPS 代理。
BACKEND_HOST=127.0.0.1
BACKEND_PORT=8081
MAX_PLAYERS=64
CONSOLE_ENABLED=true

# 首次安装留空 WORLD_ID；已有存档必须保留 data/。
WORLD_ID=
WORLD_NAME='我的生存服务器'
WORLD_MODE=survival
INFINITE_WORLDS=true
LEGACY_MULTI_WORLD=false
```

`ALLOWED_ORIGINS` 填玩家实际访问的网站 origin，与 `GAME_PUBLIC_URL` 一致。多个合法网站用逗号分隔、逐个填写，不使用 `*`。`BACKEND_HOST` 是监听网卡，`AUTH_CALLBACK_URL` 是玩家与核心登记看到的公开地址，它们可以不同。默认存档目录为包内 `data/`，可在配置中用 `DATA_DIR=/绝对路径/存档目录` 改到独立磁盘。

运行：

```sh
./start.sh
```

启动器会读取 `server.env`，检查核心必填项并运行 `bin/mcraft`。缺少配置或未替换示例域名时会停止；不会自动创建另一套单服账号。首次启动生成 `data/world-server.json`，将唯一主世界与服务器绑定。后续启动使用原世界，不会因留空 `WORLD_ID` 再建一个世界。

同机可以检查：

```sh
curl http://127.0.0.1:8081/healthz
curl http://127.0.0.1:8081/api/server
```

`/healthz` 应成功，`/api/server` 应显示本服主世界、核心 issuer 与登记的服务器 ID。游戏服需要能访问核心服务，才能兑换和验证玩家身份；世界与区块保存在本服。

## 4. 公网与局域网接入

公网建议给游戏服分配域名和受浏览器信任的 HTTPS 证书，按 `nginx.conf.example` 将该域名的 `/api/`、`/ws/`、`/healthz` 转发到 `127.0.0.1:8081`。替换域名及证书路径后，使用 `nginx -t` 验证配置再重载。开放或转发 HTTPS 端口（通常 443）；云服务器需要核对安全组，家用网络需要路由器端口映射和可入站的公网地址。

Nginx 必须支持 WebSocket Upgrade。不要把访问 `/` 返回 404 当成故障：Go 私服只提供游戏 API，不提供游戏网页。可以访问 `https://private.example.com/healthz` 与 `https://private.example.com/api/server` 检查。

IP 也可以作为公开地址，但统一账号要求非回环地址使用 HTTPS，证书必须受玩家浏览器信任且匹配该 IP。更方便的做法是给 IP 绑定域名。HTTPS 网站不能连接 `http://公网IP:8081`，浏览器会阻止混合内容；`ALLOWED_ORIGINS` 不能绕过这项限制。

| 场景 | 监听和地址 |
| --- | --- |
| 同一电脑开发测试 | 可用 `http://127.0.0.1:8081` 或 `http://localhost:8081` 登记回调；客户端与核心本机地址也允许 HTTP。`AUTH_COOKIE_SECURE=false` 用于本机 HTTP 配置。 |
| 不同电脑的局域网多人 | 使用局域网可解析的域名、受信 HTTPS 和一致的核心登记；如直接开放 Go 网卡，设 `BACKEND_HOST=0.0.0.0`，并通过 TLS 代理公开游戏服务。`127.0.0.1` 代表各玩家自己的电脑，不能分享给其他电脑。 |
| 公网服务器 | 推荐 Go 监听 `127.0.0.1`，由 Nginx 提供 HTTPS。玩家填写公开域名或具备受信 HTTPS 的 IP。 |

核心统一身份只允许回环 HTTP；`http://192.168.x.x:8081/api/auth/callback` 不能用于 central 登记。纯局域网 HTTP 如需单独测试，可使用后文的隔离 legacy 模式，但该模式不提供网站的跨服统一身份。

## 5. 玩家如何加入

1. 玩家访问统一游戏网站，注册或登录核心账号并设置玩家名字。
2. 在游戏首页打开“加入线上服务器”，输入你的 **Go 游戏服公开地址**：例如 `https://private.example.com`；本机测试输入 `127.0.0.1:8081`。
3. 保存或直接连接。网页发现 `/api/server`，验证本服登记身份，兑换该服专用会话，再加入这台服务器的主世界。

玩家会继续留在原网站，不会跳到你的网页，也不会进入网站运营者的本地游戏服。不要填写统一网站地址、核心账号地址或自己的网页端口 8080。世界、背包与 OP 由所选 Go 服务决定；固定玩家 ID 保持不变。核心登录 Cookie 与密码不发送给私服。

## 6. OP、运行管理和备份

在运行 `./start.sh` 的 Go 终端输入命令，不带 `/`。先从核心账号中心复制服主自己的 **32 位固定玩家 ID**，授予 OP：

```text
op 你的32位固定玩家ID
gamemode creative 玩家名字
gamemode survival 玩家名字
deop 玩家名字
defaultgamemode survival
list
save-all
backup
stop
```

固定 ID 可在首次入服前授权；玩家名字必须已被本服认识，切换模式需要该玩家在线。`op` 只改变权限，不自动改变模式。`deop` 撤销权限但保留原模式；需要时先切回生存。OP 在游戏中可用 `/gamemode creative`、`/gamemode survival 玩家名字` 等命令。支持 `0/1/2/3`、`s/c/a/sp` 及冒险、旁观模式。

默认模式只应用于首次加入本服的玩家；已有玩家的模式与进度分别保存。`data/server.json` 保存 OP 固定 ID、权限和默认模式。极限模式已死亡玩家只能进入旁观。更完整的权限、白名单、封禁、插件重载、备份恢复见 [OPERATIONS.md](OPERATIONS.md)。

后台进程可用本地管理工具，无需网页后台。进入发布包目录执行：

```sh
./bin/mcraftctl --data ./data --url http://127.0.0.1:8081 status
./bin/mcraftctl --data ./data --url http://127.0.0.1:8081 op 你的32位固定玩家ID
./bin/mcraftctl --data ./data --url http://127.0.0.1:8081 backup
./bin/mcraftctl --data ./data --url http://127.0.0.1:8081 stop
```

工具从 `data/admin.key` 读取本地管理密钥；如果修改了存档路径或端口，也要修改 `--data`、`--url`。不要公开 `data/` 或该密钥。`backup` 在 `data/backups/` 生成备份；`stop`、SIGTERM、SIGINT 会执行最后保存并退出。不要用 `kill -9` 做日常关闭。

Linux 可把启动器交给 systemd 管理，例如 `/etc/systemd/system/webcraft.service`：

```ini
[Unit]
Description=WebCraft private Go server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=webcraft
WorkingDirectory=/opt/webcraft
ExecStart=/opt/webcraft/start.sh
Restart=on-failure
RestartSec=5
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
```

先创建有权读程序、写本服 `data/` 的 `webcraft` 系统用户，将包放到实际目录并替换上面的路径；之后 `systemctl daemon-reload`、`systemctl enable --now webcraft`。查看日志用 `journalctl -u webcraft -f`，正常停止用 `systemctl stop webcraft`。systemd 无交互终端，使用 `mcraftctl` 管理。此为部署模板，需在自己的 Linux 主机验证。

升级前执行 `backup` 并正常停止。保留 `data/`、`server.env`、自定义插件及其数据；新包解压到另一目录，只替换程序、引擎与需要更新的插件，不用空 `data/` 覆盖旧存档。`WORLD_ID` 的既有绑定、区块和固定 ID 背包都会保留。多个独立服务必须使用不同的数据目录、监听端口和核心服务器 ID。

## 插件与协议

部署包包含三款示例插件和可选 `server-auth` 二次注册示例。把自定义可执行插件与 `plugin.json` 放入 `plugins/插件ID/`；插件必须与服务器平台一致。二次登录只扩展本服准入，不能替换核心固定 ID。

SDK 可用本地 module replace 引用：

```sh
go mod edit -require=mcraft/backend@v0.0.0
go mod edit -replace=mcraft/backend=/absolute/path/to/plugin-sdk
go build -o /absolute/path/to/plugins/myplugin/myplugin .
```

插件监听玩法、伤害、物品与运营事件；可取消字段和可修改字段由 `GET /api/events` 声明，Monitor 只读。OP、玩家模式和插件动作经过同一服务端规则与事件入口。开发说明见 [PLUGINS.md](PLUGINS.md) 和 [ITEMS.md](ITEMS.md)。下载页面本身不改变世界状态。

本项目使用 Go 服务端与插件 API v1，参考 Spigot 的服务端设计。Java JAR、Minecraft 原版网络协议及 Anvil 存档尚未兼容；区块采用本项目的服务端格式。

## 常见连接问题

| 现象 | 排查 |
| --- | --- |
| 配置不完整，启动器退出 | 复制核心登记返回的五项，替换所有示例域名，设置网站来源。 |
| 本机可访问，别人连不上 | 核对公网/局域网地址、监听网卡、安全组、防火墙、端口映射；不要把 `127.0.0.1` 分享出去。 |
| `/healthz` 正常，但登录或入服失败 | 核对同一个 issuer、公钥、服务器 ID、完整回调地址；私服应可访问核心。 |
| 证书、混合内容或跨域错误 | 使用受信 HTTPS；`ALLOWED_ORIGINS` 必须包含玩家网站实际 origin；核对 WebSocket 代理。 |
| 输入 IP 后看到了另一台服 | 检查该 IP/域名的 Nginx 上游和 `/api/server` 返回的服务器 ID；每台服登记独立 ID。 |
| 插件无法执行 | 下载机器架构要正确，插件二进制也要与该平台一致；查看 Go 日志。 |
| 误建了新世界或丢失背包 | 停服后检查 `DATA_DIR` 是否仍指向原目录；不要删除原始存档，先备份再操作。 |

## 隔离开发测试

仅在独立临时数据目录使用 `ACCOUNT_MODE=legacy`、留空 `AUTH_ISSUER_URL`，并使用测试端口，可运行旧 API v1 单服账号。此模式不能让统一网站的核心账号跨服登录；正式私服使用默认 central。不要把现有服务器切换到 legacy 或清空它的存档来测试。

## 网站运营者构建和发布

在源码根目录依次运行：

```sh
npm run package:server
npm run package:client
```

第一步生成 `dist/releases/webcraft-server-linux-amd64.tar.gz`、`webcraft-server-linux-arm64.tar.gz`、`webcraft-server-darwin-arm64.tar.gz` 及 `SHA256SUMS`；可用 `npm run package:server -- linux-amd64` 仅构建一个目标。第二步将可用服务端下载资源与部署说明带入独立客户端网站包，供首页下载入口使用。具体网站部署见 [CLIENT.md](CLIENT.md)。

下载清单位于 `/downloads/server/manifest.json`，校验文件位于 `/downloads/server/SHA256SUMS`，本说明位于 `/downloads/server/deployment.md`。网站可通过 `SERVER_DOWNLOADS_DIR` 指向独立的发布目录；该目录用于公开、已发布的服务端包，不用于账号、世界、密钥或备份。发布包构建不读取本地 `.env`，不包含真实账号、私钥、服务器配置或玩家进度。
