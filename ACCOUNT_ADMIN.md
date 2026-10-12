# 独立核心账号服务与管理后台

`mcraft-account` 独立部署在账号服务器，提供注册、登录、固定玩家 ID、玩家名字、私人游戏服务器登记和账号管理。账号网页与 `/admin` 后台内嵌在一个 Go 二进制中，运行时不需要 Node、Go 工具链、游戏服或 WASM。所有玩家访问同一个游戏网站，该网站就是游戏客户端；私人游戏服务器由服主分别部署。

玩家访问统一游戏网站时，先注册或登录核心账号并设置名字，再进入游戏首页，选择浏览器本地游戏或线上私人服务器。连接私人服务器时继续使用当前网站客户端，核心会话 Cookie 不发送给私人服；本服身份凭据按目标服务器隔离。账号中心也提供“进入游戏首页”入口，地址来自 `IDENTITY_GAME_HOME_URL`。玩家加入不同服务器始终使用同一个固定 ID；改名与本服插件准入不会重新分配身份。世界、背包、OP、封禁和玩法插件由各游戏服独立管理。

## 分发与首次部署

源码构建分发包需要 Go 和 Node；解压后运行只需要对应平台的可执行文件：

```sh
npm run package:account
# 或仅构建本机包
npm run package:account -- darwin-arm64
```

默认生成 Linux amd64、Linux arm64、macOS arm64 三种包，位于 `dist/releases/webcraft-account-service-<平台>.tar.gz`，校验文件为 `dist/releases/ACCOUNT_SHA256SUMS`，不会覆盖游戏服校验文件。分发包仅含二进制、空目录、公开说明及配置示例，不含开发机账号、存档、私钥或实际环境配置。

解压 Linux 包后：

```sh
tar -xzf webcraft-account-service-linux-amd64.tar.gz
cd webcraft-account-service-linux-amd64
umask 077
cp account.env.example account.env
# 编辑 account.env，填入实际 HTTPS 账号域名与游戏首页地址
./account-start.sh
```

目录结构：

```text
bin/mcraft-account              内嵌账号网页和管理后台的二进制
account-start.sh                 前台运行，接收 SIGTERM/SIGINT 保存退出
account.env.example             配置模板
account-nginx.conf.example       HTTPS 反向代理模板
account-plugins/                 核心账号插件，独立于游戏插件
optional-plugins/account-policy/ 预编译示例，可选安装，不会默认加载
plugin-sdk/                     公开 Go SDK、模块校验文件与示例源码
data/                           本安装自己的核心数据目录
```

`account-start.sh` 必须有 `account.env`，拒绝未替换的示例域名。上线使用 HTTPS，`AUTH_COOKIE_SECURE=true`；本机 HTTP 调试可使用回环地址并设为 `false`。服务默认仅监听 `127.0.0.1:8082`，由 Nginx 将账号域名的**全部路径**转发至该端口，包括账号网页、授权回调入口和管理后台。Nginx 示例覆盖外部传入的转发头。

## 单独授予核心管理权限

1. 首次启动时，保持 `IDENTITY_ADMIN_USERS` 为空。在账号服务普通网页注册账号并设置玩家名字，记录显示的固定玩家 ID。
2. 对当前账号进程发送 SIGTERM，等待正常退出。已有安装先完整备份数据目录。
3. 在 `account.env` 设置 `IDENTITY_ADMIN_USERS=已存在的固定ID`，然后重新运行 `./account-start.sh`。也支持已有账号的用户名，启动时会解析为固定 ID。多个值使用英文逗号分隔。
4. 访问 `https://实际账号域名/admin`，使用该账号登录。

指定未注册的用户名会导致启动失败；配置不会预占账号，也不会创建默认管理员或默认密码。解析后的权限保存在 `data/admins.json`，与任何游戏服 OP 独立。后续清空 `IDENTITY_ADMIN_USERS` 不会删除已持久化的管理权限；需要撤销核心管理员时，应停止服务、备份并编辑 `admins.json` 中的固定 ID 列表。

当前管理员账号受保护，后台不能停用或撤销其会话。普通玩家登录后不能访问管理 API。后台管理操作要求同源与会话绑定的 CSRF 校验。

## 后台资料与在线状态

后台包含总览、用户列表、在线用户和登记服务器列表，支持按用户名、玩家名字或固定 ID 搜索，按状态筛选和分页。用户详情包括固定 ID、账号、玩家名字、注册时间、注册 IP、最近登录时间、登录 IP、停用状态和有效会话数量。

注册和登录的 IP、时间与会话在同一次账号保存中提交；存储失败时全部回滚。旧账号原注册时间和固定 ID 保留，历史没有记录的注册 IP 显示为“未知”，不会用今天的登录 IP 补成注册 IP。账号存档仍采用 API v1 加法兼容；首次写入新增字段前，原文件会备份为 `accounts.json.pre-account-management-<时间>.bak`，权限 `0600`。

在线人数只统计访问统一网站的已登录用户，按固定 ID 去重。游戏首页、浏览器本地游戏、线上游戏及账号/后台页面可见时约每 30 秒发送网站活动心跳；各页面使用独立随机连接标识，同一用户的活动页面数合并展示。页面隐藏或关闭时结束该页面活动，重新可见时创建新标识。后台约每 15 秒刷新，异常断网或停止心跳后，记录约 90 秒过期。仍有效的登录会话数量**不等于**当前在线人数。游戏服玩家连接、身份租约刷新和登记资料均不算网站访问；私人服不向核心上报玩家在线状态。

停用普通账号会阻止登录并撤销全部核心会话；重新启用不恢复旧会话，但固定 ID、资料及各服存档关联不变。“撤销登录会话”保留账号，只要求重新登录。游戏服在身份租约复核时清除旧身份，通常会有一个校验周期的传播延迟。

## IP 与代理配置

直接提供服务时留空 `IDENTITY_TRUSTED_PROXIES`，注册 IP 取 TCP 连接来源，用户自行提供的 `X-Forwarded-For` 不可信。通过同机 Nginx 时可设置 `127.0.0.1/32,::1/128`。多层反向代理只配置实际代理的 IP 或 CIDR，核心从转发链右侧向左寻找第一个未信任地址；错误或超长的链回退到连接来源。登录和注册限流使用同一验证后的来源 IP，避免所有代理后的用户共用一个限流额度。

不要把全部公网地址设为可信代理。使用 CDN 时，需要同步限制源站入口并配置真实代理网段；此模板只描述直接面向客户端的 Nginx。停用账号、撤销会话等管理操作的来源地址记录在独立审计文件。

## 环境配置

| 配置 | 用途 |
| --- | --- |
| `IDENTITY_HOST` / `IDENTITY_PORT` | 核心进程监听地址和端口；默认 `127.0.0.1:8082` |
| `IDENTITY_PUBLIC_URL` | 公开账号服务 origin；生产为 HTTPS，不带路径或参数 |
| `IDENTITY_GAME_HOME_URL` | 统一游戏客户端网站地址，供登录后的入口使用 |
| `IDENTITY_DATA_DIR` | 核心账号数据；留空时使用包内 `data/` |
| `IDENTITY_PLUGIN_DIR` | 核心插件程序目录；留空时使用包内 `account-plugins/` |
| `IDENTITY_ADMIN_USERS` | 仅绑定已存在的固定 ID / 用户名，解析后持久化 |
| `IDENTITY_TRUSTED_PROXIES` | 可信代理 IP / CIDR，英文逗号分隔 |
| `AUTH_COOKIE_SECURE` | HTTPS 部署必须为 `true` |
| `IDENTITY_ALLOW_LEGACY_COOKIE` | 默认 `false`；仅旧存档本机迁移临时启用 |
| `IDENTITY_IMPORT_FILE` | 可选旧账号导入路径；目标文件不存在时才导入，先备份源文件 |

这些配置属于核心账号服务；私人游戏服配置自己的 `AUTH_ISSUER_URL`、`AUTH_SERVER_ID`、`AUTH_PUBLIC_KEY`、`GAME_PUBLIC_URL` 和 `AUTH_CALLBACK_URL`。游戏服需要核心的**公钥**，不应持有核心签名私钥、密码哈希或核心会话数据库。

## 接入私人游戏服务器

服主用普通核心账号登录账号中心，填写统一游戏网站地址与自己部署的 Go 后端回调地址，登记后获得本服 ID、核心地址和公钥。统一网站负责玩家登录与客户端界面；私人服负责运行世界。网站向核心申请绑定目标服的短期签名身份，交给该服验证并换取本服会话令牌；核心 Cookie 与密码只留在核心账号服务。原一次性授权码/PKCE 流程仍用于旧客户端兼容。同一个账号在各服具有相同 ID，服主可添加本服插件的额外注册/准入规则。

每个游戏服由服主下载、部署，保存自己的单世界及相关维度，无需另建供玩家登录的游戏网站。账号服务和统一游戏网站也可部署在不同主机；网站通过固定的同源账号代理访问核心。完整接入流程参见 [IDENTITY.md](IDENTITY.md)。

统一网站的 Node/Nginx 部署、固定内网账号上游 `IDENTITY_PROXY_URL` 与多层真实 IP 信任配置见 [CLIENT.md](CLIENT.md)。账号 Nginx 模板默认不信任任何外部转发链，仅在运营者显式填入固定网站代理地址后才保留并追加该链。

统一客户端通过私人 Go 服务 `GET /api/server` 发现服务器身份，再向核心申请 `POST /api/auth/server-proof {serverId}`。返回的签名加入凭据有效 60 秒，只能兑换一次，附带核心登记的目标 `callbackApiOrigin`。目标服 `POST /api/auth/client {proof}` 通过核心 `POST /api/token/client` 兑换原有 10 分钟身份租约，并签发独立的本服令牌。核心会话 Cookie 不发送给私人服；客户端本服令牌按目标 origin 与 ID 隔离保存在 `sessionStorage`，REST 使用 Bearer，WebSocket 使用令牌子协议。

此流程提供 `account.server_authorize.before`（可取消、无可修改字段）、`account.server_authorized` 和 `account.server_authorization.consumed`。仅在对应提交成功后发完成事件；数据包含固定账号、目标服与稳定原因，不含签名凭据或会话令牌。加入、授权和身份租约检查都不产生网站在线记录。

## 管理 API 与插件事件

以下接口均属于核心账号域名，要求核心管理员会话；写操作额外要求 `Origin` 和从 `/api/admin/me` 获得的 `X-Mcraft-CSRF`：

| 路径 | 行为 |
| --- | --- |
| `GET /api/admin/me` | 管理员身份与管理会话校验信息 |
| `GET /api/admin/stats` | 总用户、在线人数、会话和服务器统计 |
| `GET /api/admin/users?q=&status=&page=&pageSize=` | 搜索和分页；状态 `all/enabled/disabled/online` |
| `GET /api/admin/users/{id}` | 单个用户资料及网站活动 |
| `GET /api/admin/online` | 网站已登录用户与活动页面统计 |
| `GET /api/admin/servers` | 登记游戏服与服主资料；兼容在线字段固定为 0 |
| `POST /api/admin/users/{id}/status` | JSON `{ "disabled": true/false }` |
| `POST /api/admin/users/{id}/sessions/revoke` | JSON `{}`，撤销普通账号核心会话 |

返回内容没有密码、密码哈希、签名凭据、Cookie 或会话哈希。管理变更在提交前发出可取消事件，成功保存后才发出完成事件，并记录操作者、目标、原因及管理快照。在线活动和离线清理也提供独立事件。具体名称及可修改字段以核心 `GET /api/events` 目录和 [PLUGINS.md](PLUGINS.md) 为准；核心加载自己的插件目录。

`data/admin-audit.jsonl` 记录管理操作的尝试、取消、失败和提交结果。审计尝试无法保存时，不改变账号。后台显示已提交但审计结果写入异常的提示时，需要检查磁盘和服务日志。

## 可选插件与开发 SDK

分发包提供当前平台的预编译 `optional-plugins/account-policy/`，但默认的 `account-plugins/` 保持为空。此示例通过 `protectedAccountIds` 保护指定固定 ID，取消对它们的账号停用或会话撤销；Monitor 记录成功管理和在线活动，示例日志不复制注册 IP 或身份凭据。

运营者决定启用时，先正常停止核心进程，再复制可选插件：

```sh
cp -R optional-plugins/account-policy account-plugins/account-policy
# 编辑 account-plugins/account-policy/plugin.json 的 config.protectedAccountIds
./account-start.sh
```

若设置了其他 `IDENTITY_PLUGIN_DIR`，复制到该目录；不要安装到游戏服插件目录。插件状态写入本核心数据目录的 `plugins/account-policy/`。直接使用预编译示例仍不需要 Go 或 Node；不同平台应使用相同目标平台的分发包。

开发者使用 `plugin-sdk/pkg/plugin/`，模块名为 `mcraft/backend`。分发包括 `go.mod`、`go.sum` 和 `examples/` 源码；在开发机器安装 Go 1.24 或更新版本后，可以构建示例或新增自己的插件：

```sh
cd plugin-sdk
go build -trimpath -o ../optional-plugins/account-policy/account-policy ./examples/account-policy
# 交叉构建 Linux amd64 示例
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o account-policy-linux-amd64 ./examples/account-policy
```

SDK 与 `examples/` 只是开发源码，核心不会把它们作为插件扫描。插件仍通过标准输入/输出的 API v1 协议运行，入口和订阅权限以 manifest、SDK 与事件目录为准。核心插件可以处理账号事件；世界动作和游戏规则由各游戏服处理。

## 备份、恢复与升级

停止账号进程后，备份整个 `IDENTITY_DATA_DIR`，另存部署配置与账号插件程序。核心数据包含：

- `accounts.json`：账号、bcrypt 哈希和核心会话。
- `signing-key.json`：身份签名私钥，恢复时必须保留原值。
- `admins.json`：独立核心管理员 ID。
- `servers.json`：已登记私人服务器。
- `admin-audit.jsonl`、`plugins/`：管理审计与核心插件自己的数据。

在线心跳与 CSRF 信息是内存状态，重启后按新活动重新建立。不要只备份游戏世界来代替账号备份，也不要用分发包中的空目录覆盖已有核心数据。升级时停止进程、备份，然后只替换二进制与公开文档，保留 `account.env`、核心数据、签名私钥和插件目录。

构建完成后可运行隔离压缩包验收：

```sh
node scripts/smoke-account-package.mjs
```

验收使用临时目录和随机本机端口，只运行已构建的本机平台包，不访问真实账号存档。覆盖网页内嵌、注册资料、代理来源、管理权限、停机授权、重启后固定 ID 与权限保持、敏感文件保护和只读管理行为，结果保存到 `artifacts/account-package-smoke.json`。
