# Flyboard clean-room 技术架构与现有方案预检

> 日期：2026-10-25。输入契约：`research/feature-contract.md`。本文只依据可观察行为与公开协议重新设计；**不得引入、翻译、反编译或逐行仿写 XBoard/V2Board 及其覆盖包源码、Blade/UI 资产、数据库内部模型或未授权节点二进制**。历史路径仅作为兼容接口规格。

## 1. 结论

推荐采用 **Go 模块化单体 + 独立 worker/scheduler/node-agent 二进制 + React 管理端 + PostgreSQL + Valkey（Redis 协议）**，放在一个 monorepo、由同一份源码构建四个小镜像，以 Docker Compose 一键安装。

- 面板 HTTP/API、订阅输出和 WebSocket control plane 共用领域层，但以进程边界隔离耗时任务。
- 上游拉取只入队，worker 在独立容器中执行严格 SSRF 防护；HTTP 进程绝不等待远端订阅。
- scheduler 只投递周期任务/做租约与死任务回收，不执行网络拉取。
- node-agent 是自行实现、协议公开的可选数据面适配器；只有通过转发前拒绝测试的 agent 才能宣称支持全局设备限制。
- PostgreSQL 是生产唯一推荐数据库；SQLite 只允许本地开发/演示，不承诺 HA、并发 worker 或无损生产迁移。
- 用 **Valkey** 取代新版本 Redis Server：保持 Redis 协议及队列语义，同时避开 Redis 7.4+ 的 RSAL/SSPL、Redis 8 授权变化。若用户自带 Redis，支持 Redis 7.x 协议兼容端点，但不随包分发非宽松许可版本。

该方案比 Laravel/XBoard 宿主重写更容易形成版权边界，也能用 Go 单文件二进制降低一键安装复杂度。

## 2. 现有方案预检与取舍

### 2.1 后端

| 候选 | 结论 | 原因 |
|---|---|---|
| Go 标准库 `net/http` + `chi` | **采用** | 小而稳定、无 ORM/框架隐式行为，路由中间件适合动态管理路径；MIT |
| Gin / Fiber | 不采用 | 均可用，但本项目不需要更重抽象；Fiber 与标准 `net/http` 生态差异增加审计成本 |
| Laravel / Django / NestJS | 不采用 | 成熟但运行时和后台任务栈更重；Laravel 还容易误入宿主源码结构，削弱 clean-room 边界 |

采用模块化单体而非微服务：规模尚不足以支付分布式事务、服务发现和版本编排成本。进程可分、代码不分仓。

### 2.2 数据、迁移与查询

- `pgx/v5` 直接访问 PostgreSQL；`sqlc` 从手写 SQL 生成类型安全代码，不采用重量 ORM。
- `goose/v3` 管理版本化 SQL migration；每个迁移有 PostgreSQL up/down（破坏性 down 可明确拒绝），CI 从空库及上一个发布版本升级。
- SQLite driver 选纯 Go `modernc.org/sqlite`，避免 CGO；仅 dev profile。生产 schema 以 PostgreSQL 为真源，不用 SQLite 作为默认安装库。

**为什么生产选 PostgreSQL：**并发 worker 的行锁、事务替换节点、唯一约束、`SKIP LOCKED`、JSONB、可靠备份恢复均符合契约。SQLite 在单机低负载可运行，但写锁、网络文件系统、并发调度、在线备份和后续横向扩展风险太高。一键 Compose 已能无感带起 PostgreSQL，SQLite 带来的少量简化不值得形成两套生产语义。

### 2.3 Redis 队列与实时状态

- `hibiken/asynq` 提供 Redis 协议队列、重试、超时、唯一任务与调度能力；本项目仍把任务状态写入 PostgreSQL，不能把 asynq 内部键当业务 API。
- 服务端随包使用 Valkey；锁、租约、Lua compare-and-delete、集合弹出均限定为 Redis 7 兼容命令。
- 备选 `River` 很成熟且只依赖 PostgreSQL，但不满足既有 Redis 队列/实时租约边界；若未来移除 Redis 才考虑。
- 风险：asynq 长期维护节奏和严格唯一锁语义需持续观察。通过内部 `Queue` 接口封装；数据库唯一约束/状态机是最终去重，队列 unique 只是第一层。

### 2.4 WebSocket

采用 `github.com/coder/websocket`（原 nhooyr/websocket，ISC）：API 小、支持 context、并发模型清晰。每节点单 writer goroutine、有界发送队列；鉴权 10 秒、ping 55 秒、消息大小上限、读写 deadline。协议消息使用带 `version/type/request_id` 的 JSON envelope，并在 `docs/protocol/` 发布 JSON Schema。

不采用已归档的 Gorilla WebSocket 作为新项目核心。WebSocket 不是授权依据：握手 token、node id、时间戳/nonce，并以 HMAC 验证；token 只存哈希。

### 2.5 前端

React + TypeScript + Vite，TanStack Query 负责服务端状态，React Router 路由，Zod 校验 API 数据。首版使用小型自有 CSS/design tokens，不引入来源不清楚的管理模板，也不复刻 XBoard/V2Board 页面、文案、图标布局。

### 2.6 订阅解析与二维码

- URI/base64 URI 列表由 Flyboard 自己实现**规范驱动的小解析包**：逐种协议明确 grammar、长度上限、允许字段和 golden vectors；这是新写互操作代码，不复制面板实现。
- 第一阶段只支持契约已有的 URI/base64 URI；YAML/JSON 明确返回 unsupported，不做“猜格式”。第二阶段若需要 Clash YAML，用 `gopkg.in/yaml.v3` 前必须评估其归档/维护状态，并只映射白名单字段。
- QR 采用 `github.com/yeqown/go-qrcode/v2`（MIT）生成 SVG；SVG 由库编码模块输出并加固定安全响应头，内容只含订阅 URL，不接受任意 SVG 模板。

不存在一个许可证、维护状态、格式覆盖和安全边界都足够可信的“全协议订阅万能解析器”；引入此类库反而可能把未知实现/许可证带入 clean-room 项目。

### 2.7 Docker

使用 Docker/Compose Specification；镜像多阶段构建，运行层固定 digest 的 distroless 或 Alpine（优先 distroless nonroot）。Compose 服务：`panel`, `worker`, `scheduler`, `postgres`, `valkey`；可选 `caddy` TLS 入口和 `node-agent` profile。生产镜像禁止挂载 Docker socket。

## 3. 推荐逻辑架构

```text
Browser / subscription client / node-agent
                 |
          reverse proxy/TLS
                 |
        panel (HTTP + WS hub)
          |              |
     PostgreSQL        Valkey
          ^              ^
          |              |
 worker (safe fetch)   scheduler

node-agent <-- versioned HTTP/WS node-control protocol --> panel
```

### 3.1 领域模块

- **identity/admin**：管理员、角色、session、CSRF、面板 CIDR 策略。
- **users**：独立主用户模型；不假定 XBoard `users` 表。未来宿主互通只走 `UserProvider` adapter/API。
- **upstreams**：凭据、拉取状态、事务式节点快照。
- **subscriptions**：格式协商、配额、owner lock、设备迁移。
- **devices**：全局租约、完整快照缺失阈值、presence、推送 fan-out。
- **nodes**：节点注册、token rotation、HTTP/WS 同语义视图。
- **audit**：可检索、分级保留、敏感字段脱敏。

### 3.2 进程职责

- `flyboard-panel`：API、静态前端、公开订阅、QR、WS hub；提交任务后立即 `202`。
- `flyboard-worker`：消费 `flyboard:upstream`；安全下载、解析、单事务替换；每任务独立 context deadline，默认 45 秒、一次尝试，人工重试显式创建新 execution。
- `flyboard-scheduler`：每 10 秒回收过期设备租约/卡死 pull，并批量产生 sync outbox；每日审计清理与备份检查。调度采用 PostgreSQL advisory lock，多个副本只执行一次。
- `flyboard-node-agent`：运行在节点侧，采集 device/alive，缓存最后已签名配置；执行本地连接准入。Redis/control plane 不可用时默认**新设备 fail-closed、已验证且租约未过期设备继续至 grace 截止**，策略可配置但更改需审计。

### 3.3 一致性

数据库是配置与任务状态真源；Valkey 是短期租约/锁/队列。配置、用户、设备变更在 PostgreSQL 同事务写 `sync_outbox`，dispatcher 至少一次推送；agent 按 `(aggregate_id, revision)` 幂等应用。HTTP `/devices` 与 WS `sync.devices` 调用同一 application service/DTO。

同上游并发提交：数据库部分唯一索引保证每个 upstream 只有一个 `pending/running` execution；插入成功后才入队。worker 以 execution id 领任务并原子切状态，队列重复投递不会重复下载/覆盖。

## 4. 安全设计

### 4.1 管理 API 与动态路径

- 首次启动用一次性 bootstrap token 创建首个管理员；token 通过 Docker secret/只读文件注入，用后失效。
- 管理员密码 Argon2id；登录使用随机 256-bit server-side session，Cookie 为 `HttpOnly; Secure; SameSite=Strict`，定期轮换；状态变更要求 CSRF token、Origin 校验。可后续增加 OIDC/WebAuthn。
- 动态 `{admin}` 路径是**降噪而非认证**。路径生成至少 128 bit 随机值，存数据库（日志显示时遮罩）；UI 为 `/{admin}/flyboard`，兼容 API 为 `/api/v2/{admin}/flyboard/*`。旧路径重定向默认关闭。
- 路由先严格校验 path segment 再进入 admin middleware；未知管理路径统一 404。登录、管理 API 有独立限流。反向代理 access log 必须对动态段和订阅 token 脱敏。
- CIDR 白名单只保护管理域；先正确解析可信代理链，默认只信显式配置的 proxy CIDR。emergency off 仅允许容器内 CLI + 本地 secret，操作写审计。
- 角色至少 `owner/operator/auditor`；删除、清设备、改白名单和 token rotation 使用细粒度权限。

### 4.2 安全拉取

1. 只接收 `https`（显式开发开关才允许 http），拒绝 userinfo、fragment、IPv6、非标准端口和超长 URL；凭据单独加密保存，展示永不回显。
2. 自有 resolver 查询全部 A/AAAA；拒绝任一 loopback、RFC1918、link-local、CGNAT、metadata、multicast、reserved 或 IPv6 地址，空结果也拒绝。
3. 从验证后的公网 A 集合选择并让自定义 `DialContext` **直连固定 IP**；TLS `ServerName` 保留原 hostname。每次 redirect 重新执行完整校验，最多 3 次；禁用环境代理。
4. connect/TLS/header/total deadline 分开；压缩后解压字节也计入 4 MiB 上限；限制 content type、行数、单行长度和节点数。
5. worker 容器 nonroot、read-only、drop capabilities；生产再以 egress firewall 阻断私网作为第二道防线。
6. 错误只记录 host 的 HMAC/任务 id/错误类别，不记录完整 URL、token、正文。失败保留旧节点快照。

## 5. 数据模型与迁移

在输入契约六张自有表基础上，独立实现新增：

- `admins`, `admin_sessions`, `roles/admin_roles`
- `users`（独立主用户，不复用宿主表）
- `nodes`, `node_tokens`, `machines`
- `upstream_pull_executions`（状态、deadline、去重键、sanitized error）
- `device_policy`, `sync_outbox`
- `settings`（typed key/version，不保存明文 secret）
- `schema_migrations`（goose）

订阅 URL、上游 token 等应用层用 envelope encryption（XChaCha20-Poly1305）；master key 只从 Docker secret/file 读取并支持 key id 轮换。查询 token 只保存 SHA-256/HMAC lookup digest，需要显示的 token 仅创建时返回一次。

迁移规则：向前兼容至少一个 minor 版本；先 expand、再部署、最后 contract。服务启动只检查 schema，不自动执行生产迁移；`flyboard migrate up` 是 Compose upgrade 步骤并持 advisory lock。任何破坏性迁移先做备份并要求显式确认。

## 6. 依赖清单（建议约束）

版本以 Go module/npm lockfile 固定完整版本和校验和；下列为允许更新范围，合并时由 Renovate PR + CI 验证。发布前必须生成 SBOM 并以扫描结果复核许可证，不能只依赖本表。

| 依赖 | 版本约束 | 许可证 | 用途 | 主要风险/缓解 |
|---|---:|---|---|---|
| Go | `>=1.24,<1.27` | BSD-3-Clause | 后端/agent | 每次只支持两个活跃 minor；及时跟进安全补丁 |
| `go-chi/chi/v5` | `>=5.2,<6` | MIT | HTTP 路由 | 动态路径冲突用路由单测覆盖 |
| `jackc/pgx/v5` | `>=5.7,<6` | MIT | PostgreSQL driver/pool | major 锁定；连接池和取消测试 |
| `sqlc` | `>=1.29,<2` | MIT | SQL 代码生成 | 生成物必须可重复，CI 检查 dirty diff |
| `pressly/goose/v3` | `>=3.24,<4` | MIT | migration | 禁止应用启动时隐式生产迁移 |
| `redis/go-redis/v9` | `>=9.7,<10` | BSD-2-Clause | 租约、锁、缓存 | Redis/Valkey 差异跑双兼容测试 |
| `hibiken/asynq` | `>=0.25,<0.26` | MIT | Redis 队列 | 版本仍为 0.x、维护节奏风险；封装 adapter，DB 最终去重 |
| `coder/websocket` | `>=1.8,<2` | ISC | WS control plane | backpressure/慢客户端须压力测试 |
| `golang.org/x/crypto` | 与 Go 支持线匹配、锁定 | BSD-3-Clause | Argon2id/XChaCha20 | 安全更新优先，不宽松漂移 |
| `yeqown/go-qrcode/v2` | `>=2.2,<3` | MIT | QR/SVG | fuzz URL/escaping；响应禁止缓存 |
| `modernc.org/sqlite` | 锁定已审计 minor | BSD-3-Clause | dev/test SQLite | 依赖树较大；不进入生产镜像 profile |
| React / React DOM | `>=19,<20` | MIT | 管理 UI | major 锁定；不用来源不明 admin template |
| TypeScript | `>=5.9,<6` | Apache-2.0 | 前端类型 | strict 模式 |
| Vite | `>=7,<8` | MIT | 前端构建 | Node 版本门槛；只存在 build stage |
| TanStack Query | `>=5,<6` | MIT | API 状态 | 避免缓存敏感响应，登出彻底清 cache |
| React Router | `>=7,<8` | MIT | SPA 路由 | basename 必须覆盖动态 admin path |
| Zod | `>=4,<5` | MIT | runtime schema | 大 payload 在服务端先限长 |
| Valkey image/server | `>=8.1,<9`，固定 digest | BSD-3-Clause | Redis 兼容服务 | 命令兼容集测试；禁公网暴露、启 ACL |
| PostgreSQL | `>=17,<19`，固定 digest | PostgreSQL License | 主数据库 | 主版本升级需 dump/restore 演练 |
| Caddy（可选） | `>=2.10,<3` | Apache-2.0 | TLS/reverse proxy | 自动证书需网络与持久卷；也支持用户自有代理 |
| Docker Engine / Compose | Engine 27+、Compose v2 | Apache-2.0（组件） | 一键部署 | Docker Desktop 条款另行核对；不打包 Docker 本身 |

不引入 AGPL/GPL/SSPL/RSAL 依赖作为默认运行组件。若传递依赖出现 copyleft 或许可证不明，发布门禁失败并人工处置。

## 7. 单仓库目录

```text
flyboard-rebuild/
├── cmd/
│   ├── panel/              # HTTP/API/WS/static
│   ├── worker/             # queue consumer + safe fetch
│   ├── scheduler/          # leases/outbox/maintenance
│   ├── node-agent/         # optional node-side enforcement
│   └── flyboardctl/        # bootstrap/migrate/backup/restore
├── internal/
│   ├── admin/ users/ upstreams/ subscriptions/
│   ├── devices/ nodes/ audit/ settings/
│   ├── app/                # use cases, transactions, DTOs
│   ├── ports/              # DB, queue, clock, resolver, crypto
│   └── adapters/
│       ├── postgres/ valkey/ asynq/ websocket/
│       ├── safefetch/ uri/ qrcode/ httpapi/
│       └── hostprovider/   # future integration, no XBoard code
├── db/{migrations,queries}/
├── protocol/{schema,testvectors}/
├── web/{src,public}/
├── deploy/{compose,caddy,secrets.example}/
├── tests/{contract,integration,e2e,fixtures,fuzz}/
├── docs/{operations,protocol,security,clean-room}/
├── research/{feature-contract.md,architecture.md}
├── Dockerfile
├── compose.yaml
├── go.mod go.sum package.json pnpm-lock.yaml
├── LICENSES/ THIRD_PARTY_NOTICES.md sbom/
└── Makefile
```

`docs/clean-room/` 保留需求来源、黑盒测试与实现者声明；禁止把参考产品源码、截图切图、反编译产物或复制文案提交到仓库。

## 8. 安装、升级、备份与恢复

### 8.1 一键安装

`docker compose up -d` 前由安装脚本生成 secrets 文件（0600）、随机 admin path、数据库/Valkey 凭据和 master key；脚本只编排官方 Compose，不 curl-pipe shell。healthcheck 分 readiness/liveness；panel 必须等待 schema、PostgreSQL、Valkey 就绪，worker readiness 还验证队列。

所有镜像以 semver tag + digest 固定，发布 cosign 签名、provenance 与 CycloneDX/SPDX SBOM。默认只暴露代理的 80/443；数据库和 Valkey 仅 internal network。

### 8.2 升级

1. 校验签名、阅读 release notes、运行 `flyboardctl doctor`。
2. `flyboardctl backup create --pre-upgrade`，并实际校验 archive manifest。
3. 拉取固定 digest；运行一次性 `migrate` job（advisory lock）。
4. 先 worker/scheduler 后 panel，检查 schema/build/protocol compatibility。
5. smoke test：登录、提交 pull、订阅、WS node handshake；成功后清理旧镜像。

不承诺数据库 downgrade。应用回滚只在 migration 标记 backward-compatible 时允许，否则恢复升级前备份。

### 8.3 备份

- PostgreSQL：`pg_dump --format=custom`，包含 schema/version；大部署推荐外部 PITR。
- 加密 secrets/master keys 与 Compose 配置单独备份；没有 master key，数据库中的凭据不可恢复。
- Valkey 不列为权威备份：恢复后队列由 DB pending 状态重建，设备租约重新建立；短暂按 fail policy 处理。
- 备份 archive 加密、SHA-256 manifest、保留策略；每个 release CI 和运维 runbook 都做全新环境 restore drill。

## 9. 分阶段实现与测试门禁

### Phase 0：clean-room 与供应链

- 冻结行为契约、错误码、JSON schema、节点协议；记录实现者未接触禁用源码的边界。
- 建仓、license policy、Dependabot/Renovate、SBOM、secret scan、SAST、可重复构建。
- **门禁：**禁止来源不明文件；所有 direct/transitive license 允许；镜像无 critical CVE（有时限豁免流程）；协议测试向量评审通过。

### Phase 1：骨架、认证、数据库

- panel、静态 React 壳、管理员 bootstrap/RBAC/session/CSRF、动态路径、PostgreSQL migration、审计。
- **门禁：**空库 up、前一 schema upgrade、重复 migrate；未认证全管理 API 401/403；动态路径/可信代理/CIDR/CSRF/限流测试；备份恢复一次。

### Phase 2：上游与独立客户端

- CRUD、safe-fetch worker、URI/base64 parser、事务替换、订阅三格式、QR、设备 owner lock。
- **门禁：**契约 SSRF 地址全集、DNS rebinding/redirect/mixed DNS、hang、压缩炸弹、超大 body；6 并发只下载一次；失败保留旧快照；parser/QR fuzz；并发 device_limit 原子测试。

### Phase 3：设备控制面与 node-agent

- versioned HTTP/WS、nodes/machines、租约、快照缺失阈值、outbox、agent 转发前准入。
- **门禁：**跨三个节点并发设备；重连 full sync；单次/两次缺失；traffic 不续 presence；HTTP/WS DTO 一致；Valkey/网络分区/慢 WS 客户端/乱序重复消息；新设备 fail-closed 实测。未通过前 UI 必须标记“仅观测，不保证转发限制”。

### Phase 4：兼容与运维

- `/api/v2/{admin}/flyboard` compatibility facade、success/error wrapper；宿主 adapter 只依据公开 API，不连宿主私有表。加入 scheduler、升级命令和可观测性。
- **门禁：**feature-contract 全矩阵；旧客户端黑盒 contract test；10 倍目标负载 soak 24h；pull 期间 API p95 不显著退化；零明文 token/URL 日志；升级/失败回滚/恢复演练。

### Phase 5：发布

- 两人审查安全模型和许可证；外部渗透测试重点覆盖 SSRF、auth、动态路径、token 泄漏、WS spoofing。
- **门禁：**发布镜像签名/SBOM/provenance；安装于全新主机并通过 smoke suite；operator 文档含 emergency off、key rotation、restore、Redis/DB 故障策略。

## 10. 已知风险与明确非目标

- IP+UA 只能做租约启发式，不能证明物理设备身份；UI/文档不得宣传为不可绕过。
- node-agent 若不控制真实转发准入，面板只能观测/同步，不能保证全局限制。
- 首版不支持任意 Clash YAML/JSON、历史错误路由、XBoard 私有数据库或原 UI。
- 单 panel 实例先交付；多实例 WS fan-out 可利用 Valkey pub/sub/outbox 后续开启，但必须先做顺序与丢消息测试。
- Valkey 故障会影响限制与队列：订阅/新设备限制路径 fail-closed，普通管理只读可降级；不得静默 fail-open。
- 依赖版本会变化；实际 release 以锁文件、SBOM、许可证扫描和安全公告为准，本报告的区间不是免审批准。

以上设计覆盖输入契约中的面板、worker、scheduler、节点协议、设备限制、动态管理路径、升级与备份，同时把 XBoard/V2Board 保持在纯黑盒兼容边界之外。