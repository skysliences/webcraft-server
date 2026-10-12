# 统一游戏客户端网站部署

玩家只访问统一的 `https://play.example.com` 网站。该网页负责注册、登录、名字设置、游戏首页、本地单机与私人服连接。核心账号服务部署在 `https://login.ccraft.app`，保存账号和固定 ID；各服主部署自己的 Go 游戏服，例如 `https://private.example.com`，保存世界、背包、OP 和插件。

私人游戏服无需运行 Node 前端或另建账号网页。游戏服务器分发包的 `start.sh` 运行 Go 即可，`start-frontend.sh` 只是供统一网站运营者选择使用的客户端启动器。客户端网站不会因某个私人服停止而无法显示首页。

下文 `deployment/*.example` 表示源码模板位置；解压发布包后，相应模板在包根目录。

## Node 网站与固定账号代理

网站运营者可使用独立 `webcraft-client.tar.gz`：解压后复制 `client.env.example` 为 `client.env`，填写核心服务与网站地址，运行 `./start.sh`（Node.js 22 或更新版本）。源码运行方式为 `npm run start:frontend`；原游戏服分发包的可选 `start-frontend.sh` 也保持兼容。生产配置示例：

```dotenv
HOST=127.0.0.1
PORT=8080
CLIENT_PUBLIC_URL=https://play.example.com
IDENTITY_PUBLIC_URL=https://login.ccraft.app
# 固定账号请求目的地；可与公开 issuer 不同，不能由浏览器指定。
IDENTITY_PROXY_URL=http://127.0.0.1:8082
# 只填写实际位于网站 Node 前面的反向代理 IP，不使用通配符。
FRONTEND_TRUSTED_PROXIES=127.0.0.1
```

`IDENTITY_PUBLIC_URL` 是公开账号 issuer，也是签名验证使用的地址。`IDENTITY_PROXY_URL` 是固定网络上游；同机时可直连账号 Go 的回环端口，跨机可使用受控内网地址或公开 HTTPS 核心地址。Node 对核心发送的 `Origin` 始终保持公开 issuer。没有配置该项时使用公开 issuer 作为上游。

网站 Nginx 将整个网站转发至 `127.0.0.1:8080`，包括 `/identity-api/`，并覆盖访客提交的转发头：

```nginx
server {
    listen 443 ssl;
    server_name play.example.com;
    ssl_certificate /etc/letsencrypt/live/play.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/play.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

Node `/identity-api` 只允许公开客户端需要的账号接口：查询身份、注册、登录、名字、退出、网站活动与申请目标服身份。它验证固定网站来源和请求方式，不代理管理 API、令牌兑换或任意 URL。仅转发核心账号 Cookie，不转发本服 Bearer、管理 CSRF、房主密钥或其他 Cookie。登录响应中的核心 Cookie 由浏览器保存在网站 origin，保持 HttpOnly；核心密码与 Cookie 不发送给私人 Go 游戏服。

## 加载与公共资源缓存

进入游戏时显示账号验证、引擎/贴图下载、服务器连接和地形加载进度；失败或等待较长时可重试。直连 Go 服优先读取 `/api/server`，旧网页收藏仍通过网页发现信息解析其游戏服。账号验证使用本页已核对的服务器信息，存量游戏令牌仍由 Go 验证；首次进入无令牌时直接兑换目标服凭证，沿用原服务端身份和插件事件入口。

HTML 直接提供静态登录表单，启动入口异步读取精简配置后绑定账号交互。首页保留三维背景世界，并异步加载游戏引擎；进入 `/local/*` 或 `/r/*` 后加载所选世界与地形。同步启动依赖只有两个小分包，不代表首页总请求只有两个，也不代表首页完全不加载 WASM。构建使用固定版本 esbuild 合并模块、压缩并按动态导入拆包，Worker 独立打包。源码运行仍可直接加载模块，发布包运行无需 esbuild 或 node_modules；源码构建前执行 `npm ci`。

每个公共文件有独立的 SHA-256 和内容指纹。`config.js` 发布 `assetHashes`，PNG、声音、图集、引擎和 CSS 使用各自的指纹；分包文件名含内容指纹。Node 只对匹配文件当前指纹的 URL 或已知的内容命名分包设置长期缓存，改一个 JS 不会使其他材质和声音失效。HTML、配置、身份接口和任意查询响应不使用长期缓存。公共文件保留 ETag；部署须构建完整快照后原子切换，不能直接修改已发布的指纹文件。

离线缓存仅含公共资源，不缓存身份/API/签名/凭据，也不改变 IndexedDB 存档。原始贴图备份不进入发布包，592 个已打包贴图别名不再逐张预取；离线 PNG 请求由缓存贴图包切片返回原始字节。源 PNG 和原版回退仍在网站包中，贴图包损坏或缺失时游戏仍可回退。首次离线准备把小图片与音效合为一个后台包，后台工作在首页或世界可用后开始；等待用户填写登录/插件表单时允许后台准备，真正加载世界时暂停新的后台下载，避免其他标签永久占用优先状态。

后续更新比较每个文件指纹，直接复制未变化的缓存，只请求变化文件和实时配置。只有完整下载、SHA-256 校验及缓存写入全部成功才原子切换版本；失败、断网或配额不足保留旧完整版本，并保留最近一版供旧标签和回退使用。显示“离线游戏已就绪”后，已登录用户可断网重开首页及本地存档；首次登录和私人服联机仍需要网络。

验证：`npm run client:loading:verify` 实际构建并运行临时 HTTP 网站，检查分包依赖、全部缓存字节校验、离线贴图切片及增量更新。当前构建在 HTML 内直接提供登录表单，两个小启动分包开始异步读取配置，完整构建共 13 个 JS 分包；首次完整离线准备使用 21 次 HTTP 请求（含清单与两种兼容配置入口），缓存 765 项资源、592 个贴图别名，基础资源约 4.4 MiB。单个音效变化的验收只请求清单、该音效和配置，复用其余 762 项。这些数字指离线准备与脚本请求数，不能直接当作所有网络环境下的入服秒数。

## 首页服务端下载与发布资源

登录后的游戏首页提供“下载 Go 服务端”，打开同一网站的 `/server-downloads.html`。页面按平台列出服务端文件、大小和 SHA-256，展示实际网站与核心地址、账号中心入口及开服步骤。玩家下载已编译 Go 程序，自己登记核心身份并运行；网站只提供客户端与下载资源，不创建或代管私服。

从源码发布网站，先构建各平台游戏服，再构建客户端包：

```sh
npm run package:server
npm run package:client
```

`package:server` 默认构建 Linux amd64、Linux arm64、macOS arm64，产物保存在 `dist/releases/`，其中 `server-downloads.json` 记录可下载服务端包的元数据。`package:client` 将已有服务端归档作为下载资源放入 `webcraft-client/downloads/server/`，连同清单、校验文件和部署说明一起打包；网站运行仍只需要 Node，不启动这些 Go 程序。先执行服务端打包，避免发布的网站缺少可用下载。

网站包的下载资源目录：

```text
downloads/server/
  webcraft-server-linux-amd64.tar.gz
  webcraft-server-linux-arm64.tar.gz
  webcraft-server-darwin-arm64.tar.gz
  server-downloads.json
  SHA256SUMS
  deployment.md
```

Node 默认优先使用网站包内的 `downloads/server/`；源码运行时使用 `dist/releases/`。大文件也可独立放在网站机器的其他发布目录，例如在 `client.env` 中配置：

```dotenv
SERVER_DOWNLOADS_DIR=/srv/webcraft/releases
```

相对路径以网站包根目录为基准。将已构建的 `server-downloads.json` 和其中列出的 `webcraft-server-*.tar.gz` 拷到该目录，保留网站包内的 `downloads/server/deployment.md`。更新时使用同一批构建的清单与归档，避免下载校验不一致。不要把账号、世界、真实配置或备份放入发布目录。

网页使用的公开路径：

| 路径 | 内容 |
| --- | --- |
| `/server-downloads.html` | 平台下载和部署页面 |
| `/downloads/server/manifest.json` | 当前可用平台、文件、字节数和 SHA-256 的公开清单 |
| `/downloads/server/webcraft-server-平台.tar.gz` | 清单列出的 Go 服务端下载 |
| `/downloads/server/SHA256SUMS` | 当前发布包的校验文件 |
| `/downloads/server/deployment.md` | 完整私人服部署说明 |

下载服务只公开清单中受支持的固定服务端文件名，不提供目录浏览，也不公开账号服务包、世界或密钥。详细开服说明见 [SERVER_DISTRIBUTION.md](SERVER_DISTRIBUTION.md)。网站运营者需要在机器上构建或拷入真实归档，配置和清单本身不会生成可执行程序。

## 静态网站直接使用 Nginx 账号代理

也可执行 `npm run build:frontend` 后部署静态客户端。`config.js` 至少包含公开 issuer 与相对代理路径：

```js
window.MCRAFT_CONFIG = {
  identityBase: '/identity-api',
  identityIssuer: 'https://accounts.example.com',
  apiBase: '',
  backendPort: 8081
};
```

此时必须另外实现固定上游的 `/identity-api` 代理，不能把账号服务任意 URL 作为浏览器参数。下面是账号 Go 同机运行的示例；`map` 位于 Nginx `http` 上下文，`location` 放入统一网站的 HTTPS `server`：

```nginx
map "$request_method:$http_origin" $client_account_request_ok {
    default 0;
    "GET:" 1;
    "GET:https://play.example.com" 1;
    "POST:https://play.example.com" 1;
}
map $cookie_mcraft_identity $client_core_cookie {
    default "";
    "~^[a-f0-9]{64}$" "mcraft_identity=$cookie_mcraft_identity";
}

# Inside the play.example.com HTTPS server:
location ~ ^/identity-api/auth/(me|register|login|profile|logout|heartbeat|server-proof)$ {
    if ($client_account_request_ok = 0) { return 403; }
    client_max_body_size 8k;
    rewrite ^/identity-api/(.*)$ /api/$1 break;
    proxy_pass http://127.0.0.1:8082;
    proxy_set_header Host accounts.example.com;
    proxy_set_header Origin https://accounts.example.com;
    proxy_set_header Cookie $client_core_cookie;
    proxy_set_header Authorization "";
    proxy_set_header X-Mcraft-CSRF "";
    proxy_set_header X-Owner-Key "";
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_intercept_errors on;
    error_page 301 302 303 307 308 =502 @identity_redirect_blocked;
}
location /identity-api/ { return 404; }
location @identity_redirect_blocked {
    default_type application/json;
    return 502 '{"error":"账号服务返回了无效跳转"}';
}
```

替换实际域名并验证配置。跨机上游应使用 HTTPS 或受控内网连接；同时配置核心的显式可信代理名单。静态客户端需保留 `/local/*`、`/play/*` 等游戏路径回退到 `index.html`。静态代理不接入核心管理接口，管理后台仍在账号服务自己的 `/admin`。

保留首页服务端下载时，将 `/downloads/server/` 固定代理到同机的网站 Node 下载路由；该进程按上文配置公开网站与核心地址，并读取指定发布目录：

```nginx
# Inside the play.example.com HTTPS server:
location /downloads/server/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Real-IP $remote_addr;
}
```

下载页的 HTML、CSS 和 JS 作为静态网站文件部署；`manifest.json` 由 Node 返回当前网站与核心地址，以及允许下载的归档清单。不要把整个项目根目录或 `dist/releases/` 用自动索引直接暴露。首次部署推荐使用上面的 Node 全站方案，统一处理账号代理与下载。

## 真实注册 IP 与多层代理

各层分别验证自己实际连接的上游；信任任何公网客户端的转发头会使注册 IP 和限流失去依据。

| 路径 | 配置 |
| --- | --- |
| 网站 Nginx → 同机 Node | Nginx 用 `$remote_addr` 覆盖 XFF；Node 的 `FRONTEND_TRUSTED_PROXIES=127.0.0.1` 只信任该 socket 来源 |
| 网站 Node → 同机账号 Go | `IDENTITY_PROXY_URL=http://127.0.0.1:8082`；核心 `IDENTITY_TRUSTED_PROXIES=127.0.0.1/32` |
| 网站 Node → 跨机账号 Go | 核心只信任网站 Node 实际出口 IP，以及链上明确受信的代理地址；网站前置 Nginx 的地址也属于需要核对的链路 |
| 网站 Node → 账号公网 Nginx → Go | 账号 Nginx 只对显式列出的固定网站出口 IP 保留并追加 XFF；其他客户端一律覆盖。核心同时信任账号 Nginx 和这些固定代理地址 |

Node 会在可信链末尾追加自己的实际 socket 对端。未经信任的来源只发送 socket 地址；超长或含非法 IP 的链也回退 socket 地址。核心继续从右向左验证链，取第一个未信任的地址。两层都需要配置，不能只设置其中一层。

`deployment/account-nginx.conf.example` 的 `geo` 默认信任列表为空。跨机确需保留网站代理链时，在该列表加入网站 Node 的真实出口 IP，例如将注释行替换为实际 `/32` 或 `/128` 地址，并把核心 `IDENTITY_TRUSTED_PROXIES` 设置为实际代理链上的固定地址。不要使用全部公网 CIDR。默认模板会覆盖外部 XFF；此时经网站公网代理进入账号服务的请求记录网站代理地址，不能恢复已被覆盖的访客地址。同机 Node 直连账号 Go 可以避免这次公网绕行。

## 私人服接入统一客户端

服主在核心登记统一网站地址与自己 Go 服务的回调，配置：

```dotenv
AUTH_ISSUER_URL=https://login.ccraft.app
AUTH_SERVER_ID=本服登记ID
AUTH_PUBLIC_KEY=核心公钥
GAME_PUBLIC_URL=https://play.example.com
AUTH_CALLBACK_URL=https://private.example.com/api/auth/callback
ALLOWED_ORIGINS=https://play.example.com
AUTH_COOKIE_SECURE=true
BACKEND_HOST=127.0.0.1
BACKEND_PORT=8081
```

私人 Nginx 示例在 `deployment/nginx.conf.example`。玩家在统一网站填写私人 Go 服务地址；客户端从 `GET /api/server` 发现本服身份，然后从核心申请一次性 60 秒目标服凭据，交给本服兑换。世界显示继续留在统一客户端。本服令牌按目标 origin 与 ID 隔离保存在 `sessionStorage`，REST 使用 Bearer，WebSocket 通过子协议携带令牌；不用私人域名的第三方登录 Cookie。旧 PKCE 入口仍兼容已有客户端。

核心后台只统计这些已登录页面的网站活动；首页、本地游戏、线上游戏使用同一网站活动逻辑。私人服不向核心上报玩家在线。详细接口与插件事件见 [IDENTITY.md](IDENTITY.md)，账号后台与核心部署见 [ACCOUNT_ADMIN.md](ACCOUNT_ADMIN.md)。

## 验证

构建账号 Go 二进制后可运行 `node scripts/verify-identity-proxy.mjs`；可使用 `--account-bin /绝对路径/mcraft-account` 指定隔离构建产物。测试仅启动临时账号目录和随机本机端口，检查来源、固定上游、Cookie 隔离、代理 IP 链及停止游戏/账号服务后的首页行为。输出结果不含密码、Cookie 或签名凭据。

### 默认原版材质、可选高清与 CDN 二进制

默认方块使用 Minecraft 原版及已有模型 UV 适配的 16×16 贴图。首页或游戏暂停菜单的“画面设置”可切换“高清材质（128×128）”；游戏内切换先保存本地待写入数据，再重新载入世界。原始高清 PNG、账号和世界文件不会被修改。浏览器保存的 `mc.textureQuality` 只控制显示，不改变 Go 服务端的游戏规则或插件事件。

WASM、默认材质包、高清包及离线后台包均通过带内容 SHA256 前缀的 `.bin` 公共地址加载，兼容 Cloudflare 的默认静态扩展名缓存；原 `/engine.wasm` 和贴图地址仍可用。必须在实际发布后检查 `CF-Cache-Status: HIT`，浏览器 `immutable` 响应本身不能证明 CDN 命中。账号、身份凭证、动态配置和 API 保持 `no-store`。

默认离线准备不下载高清包。玩家选择高清并首次成功进入世界后，Service Worker 单独校验并缓存高清包，后续可以离线使用高清材质；从未下载过高清时请联网加载一次，或使用已准备的原版材质。高清包失败不能发布半成品默认缓存，也不会影响原版存档。

启动配置通过 `/client-config.json` 异步读取，体积约 0.5 KB；兼容入口 `/config.js` 也输出同一精简配置。完整资源指纹放在有内容版本的 `/assets/runtime/asset-index-*.js`，进入世界时才读取，并可被 CDN 和离线缓存复用。HTML 登录表单先显示，账号查询在后台验证；在输入或提交之后返回的旧查询不能覆盖新的登录。账号服务暂不可用时，已缓存的本地身份只允许进入离线本地游戏。

创造模式点击破坏有本地显示预测：无需等待 WebSocket 往返即可开始显示方块消失，刚编辑区块优先重绘。服务端通过带请求号的方块校正回复和原有区块广播决定最终状态；插件拒绝时仅恢复对应方块，多个未确认操作互不覆盖。跨维度、世界窗口切换及超时清理预测，保护后来的权威放置。旧服务端没有 `sv.digAck` 时继续等待原有广播。预测不代替 Go 规则或插件事件。

放置也支持显示预测：新 Go 服声明 `placeAck` 后，方块在右键操作时显示，无需等待网络往返；服务端返回对应序号、世界版本与权威方块列表。插件拒绝、空背包、碰撞或支撑失效时恢复对应位置。声明 `placeInventoryAck` 的服同时支持生存背包显示预测：操作时立即显示数量减少，确认后使用权威背包移除已结算的预留，拒绝时恢复；创造模式不扣数。连续搭建和放置后立即破坏共享有序预测层，区块快照与确认按原接收顺序应用并重新覆盖尚未确认的显示。旧服保持兼容，本地单机直接执行浏览器规则，不对共享 WASM 写入未校验预测。

背包消息 `sinv` 和 `placeack.inventory` 附带会话内递增 `rev`、已经处理的放置序号 `placeQ`、快捷栏选择序号 `selQ` 及 `epoch/dim`。网页先拒绝旧版本、过期世界消息，再从完整权威快照扣除尚未确认的放置。预留按原槽和物品 ID、耐久、附魔、完整元数据匹配；最后一件预测为空时不能继续花费。`sel` 可附带独立的 `q`，较早的放置回包不会覆盖用户刚选中的快捷栏，插件拒绝选择后恢复真实槽。确认序号只管理显示，不代表游戏事件成功，不改变服务端校验、账号或存档格式。
