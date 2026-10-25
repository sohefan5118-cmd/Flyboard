# Flyboard 独立重建：功能与兼容契约清点

> 只读静态清点，日期 2026-10-25。来源：`full-panel-package/flyboard-full-panel-a-baseline-v1` 的 README、Compose、4 个 migration、全部 overrides，以及 `audit/flyboard-*.md`、`audit/flyboard-tests/*` 和相关 daily memory。未读取 `.env`，未访问或记录任何真实密钥/Token/订阅 URL/IP 数据。
>
> **证据边界**：安装包是“XBoard 固定镜像 + 覆盖文件”的部署快照，不是独立 Flyboard 规范；代码、README、历史 audit/memory 存在时序差异。以下将“应兼容的可观察行为”和“不可直接复制的 XBoard 实现”分开。

## 1. 用户可见功能清单

### 1.1 管理入口与权限

- XBoard 管理壳右下角有 FlyBoard 入口；独立页面 `GET /flyboard`。
- FlyBoard 页面是公开可加载的静态壳，但管理数据和动作走管理 API，要求管理员认证；当前基线还将整个管理入口/API置于面板 IP/CIDR 白名单中。
- FlyBoard UI 六页：
  1. **上游配置**：单/多上游保存；URL 支持 `{token}`、`{userid}`、`{email}`、`{uuid}` 占位符；启停、删除、选择拉取、全部拉取；展示流量、到期、节点数、最后拉取/错误。
  2. **主用户防分享**：启停、允许订阅设备数、设备记忆窗口（60–2592000 秒；包中当前设置声明为 120 秒）、整个 XBoard 后台 IP/CIDR 白名单及说明文本。
  3. **主用户管理**：按邮箱/ID搜索；展示状态、到期、设备限制、当前设备/IP、节点数和订阅地址；修改 `device_limit`、清空设备；主用户白名单开关/备注/审计记录。
  4. **独立客户端**：创建/编辑/删除/启停；到期、流量额度、设备数、设备窗口、备注；复制订阅与打开 SVG 二维码。
  5. **上游节点列表**：只展示 Flyboard 导入节点，不混入 XBoard 主节点表。
  6. **拦截日志**：刷新、按条/批量删除、全部清空。
- XBoard 主面板另有“主面板白名单”管理面板：展示 Laravel 实际识别 IP、白名单匹配/启停和拦截日志。此为部署集成能力，不应被误认为 Flyboard 独立核心。

### 1.2 订阅与防分享

- **主用户标准订阅**：保留 `/s/{token}`（实际前缀可配置）及 XBoard 客户端格式协商；Flyboard 在原客户端中追加 Owner Lock/订阅防分享。
- 主订阅指纹基于规范化 IP + User-Agent（兼容旧指纹迁移），限制窗口内 owner 数；超限拒绝（历史验证为 HTTP 429）。Redis 短锁保证并发时“第一个 owner 获胜”。
- 同一 UA/IP-family 的 IP 迁移仅在旧节点设备静默达到 grace 后允许；订阅窗口释放时同时释放节点设备槽，并向节点推送 `sync.devices`。
- **独立客户端订阅**：`/flyboard/sub/{token}`；校验启用、过期、额度、设备数和设备窗口；失败 403，Token 不存在 404，非法格式参数 422；成功输出纯文本节点列表。
- 独立客户端格式：`format=base64|raw|clash`；未显式指定时 Clash/sing-box UA/flag 得 raw，否则 base64。响应包括 `subscription-userinfo`、`profile-update-interval: 24`、base64 profile title、no-store/no-referrer。
- `/flyboard/sub/{token}/qr` 返回订阅 URL 的 SVG QR，no-store/no-referrer。
- 限制声明：IP+UA 不是加密物理设备绑定；复制完整客户端特征无法可靠区分。

### 1.3 上游导入

- 保存独立上游账号元数据和订阅 URL；拉取后解析节点并事务式替换该上游节点集，记录订阅元数据、节点数、时间和错误。
- 支持历史 URI/base64 URI 列表；审计明确说未新增 YAML/JSON 订阅支持。
- 安全版异步拉取：专用 Redis 队列、并发去重、运行状态、超时和最大响应体；禁用凭据 URL、非 HTTP(S)、非标准端口、IPv6，以及 loopback/private/link-local/CGNAT/metadata/multicast/reserved 地址；混合私网/公网 A 记录 fail closed；不走代理并做 DNS 解析/IP pinning。
- 历史同步 `FlyboardController::pullUpstream()` 仍可在覆盖快照中看到，但事故审计证明它曾阻塞单 HTTP worker。**新实现必须采用异步、安全传输契约，不得复刻同步控制器路径。**

## 2. API / 路由契约

`{admin}` 表示动态管理安全路径。管理路由均应有管理员认证和请求日志；是否施加 XBoard 全局 IP 白名单是宿主集成策略。

### 2.1 页面和公开订阅

| 方法 | 路径 | 契约 |
|---|---|---|
| GET | `/flyboard` | Flyboard 管理静态壳；no-store/noindex；当前集成会先过面板白名单 |
| GET | `/{subscribe_path}/{token}` | 主用户标准订阅；`client` middleware；Owner Lock 为附加策略 |
| GET | `/flyboard/sub/{token}` | 独立客户端订阅；限流 60/min |
| GET | `/flyboard/sub/{token}/qr` | SVG QR；限流 60/min |
| GET | `/{admin}` | XBoard 管理壳；当前基线受面板白名单保护 |

### 2.2 Flyboard 管理 API（前缀 `/api/v2/{admin}/flyboard`，按 XBoard 路由注册方式推导）

- `GET /settings`；`POST /settings`
- `GET /main-users?q=&limit=`
- `POST /main-users/save`（`id`, `device_limit`）
- `POST /main-users/clear-devices`
- `POST /main-users/whitelist`
- `GET|POST /main-users/whitelist-logs`
- `GET /upstreams`
- `POST /upstreams/save`
- `POST /upstreams/drop`
- `POST /upstreams/pull`（异步版应返回提交/状态语义，而非阻塞到下载完成）
- `GET /nodes`
- `GET /clients`
- `POST /clients/save`
- `POST /clients/drop`
- `GET /logs`
- `POST /logs/drop`
- `POST /logs/clear`
- `GET /panel-whitelist`；`POST /panel-whitelist`（兼容入口，但属于 XBoard 集成）

管理响应沿用 XBoard Controller 的 success/error JSON 包装；白名单保存兼容依赖 `status:'success'` 或 `code:0`。

### 2.3 面板白名单 API

- `GET /api/v2/{admin}/panel-whitelist`
- `POST /api/v2/{admin}/panel-whitelist`
- `GET /api/v2/{admin}/panel-whitelist/logs`
- `POST /api/v2/{admin}/panel-whitelist/logs/clear`
- 旧 UI 兼容路径：`/{admin}/api/v2/admin/flyboard/panel-whitelist`（GET/POST）。这是历史补丁，不建议新独立实现保留，除非迁移旧前端。

### 2.4 节点/机器 API（XBoard 节点协议依赖）

在 `server.v2` 鉴权下：
- GET|POST `/api/v2/server/handshake`
- POST `/api/v2/server/report`
- GET `/api/v2/server/config`
- GET `/api/v2/server/user`
- POST `/api/v2/server/push`
- POST `/api/v2/server/alive`
- GET `/api/v2/server/alivelist`
- GET|POST `/api/v2/server/devices`：兼容返回同一映射于 `devices/users/data`
- POST `/api/v2/server/status`

机器接口：POST `/api/v2/server/machine/nodes`、POST `/api/v2/server/machine/status`。

## 3. 数据表及关键字段

### Flyboard 自有表

- `flyboard_upstream_users`
  - `id`, `name`, `email?`, `subscribe_url`, `token?`, `user_id?`, `uuid?`, `user_agent?`
  - `enabled`, `upload`, `download`, `transfer_enable`, `expired_at?`
  - `last_node_count`, `last_pulled_at?`, `last_error?`, timestamps
- `flyboard_upstream_nodes`
  - `id`, `upstream_user_id` FK cascade delete
  - `name`, `type(32)`, `address`, `port?`, `config?`, `enabled`, timestamps
  - index `(upstream_user_id, enabled)`
- `flyboard_clients`
  - `id`, `name`, unique `token`, `enabled`, `expires_at?`
  - `traffic_limit`, `traffic_used`, `device_limit`, `device_window`
  - `access_count`, `last_access_at?`, `note?`, timestamps
- `flyboard_access_logs`
  - `id`, `user_id?`, `client_id?`, `ip?`, `user_agent?`, indexed `fingerprint`
  - `action`, `reason?`, `path?`, `created_at`
  - indexes `user_id`, `client_id`, `(action, created_at)`
- `flyboard_main_user_whitelist`
  - PK `user_id`, `enabled`, `note?`, timestamps
- `flyboard_main_user_whitelist_logs`
  - `id`, `user_id?`, `admin_id?`, `action`, `reason?`, `ip?`, `user_agent?`, `path?`, `created_at?`
  - indexes `(user_id,created_at)`, `(action,created_at)`

### 宿主 XBoard 依赖（迁移中未定义）

- `users`/User：至少依赖 `id,email,token,banned,expired_at,device_limit,online_count,last_online_at` 及套餐/节点可见性关系。
- `settings`/`admin_setting()`：关键键 `flyboard_antishare_enable`, `flyboard_antishare_device_limit`, `flyboard_antishare_window`, `xboard_panel_whitelist`, `xboard_panel_whitelist_enabled`, `xboard_panel_block_policy`。
- XBoard server、machine、group/plan/route 表及模型用于节点鉴权、配置、用户可用节点和推送；独立重建必须自行定义或做 adapter，不能假定这些隐式结构存在。

## 4. 后台任务、WebSocket、Redis 行为

### 4.1 Redis 状态

- 主订阅：`flyboard:sub:owners:{identity}`、`flyboard:sub:seen:{identity}`；短互斥锁 `flyboard:lock:{userId}`（3 秒）。
- 独立客户端设备：`flyboard:client:{clientId}` hash；短锁；hash TTL 为 `device_window`；删除客户端时删除该 key。
- 全局节点设备：`user_devices:{userId}`，field 为 `{nodeId}:{ip}`、值为最后证据时间；`user_devices_seen:{userId}` 仅供 UI 展示。
- 节点完整上报辅助：`device_report_seen:{nodeId}`、`device_report_missing:{nodeId}`；每用户锁 `device:lock:{userId}`（5 秒 token 锁，Lua compare-and-delete 释放）。
- 待推送节点集合：`device:push_pending_nodes`。
- WS 心跳缓存 `ws_server:heartbeat`（10 秒刷新，TTL 30 秒）和 `node_ws_alive:{nodeId}`。
- 上游任务状态：`flyboard:pull:v2:{upstreamId}`，24h 状态缓存；pending/running 超过 600 秒呈 error。

### 4.2 全局设备限制

- 所有节点共享 Redis 设备租约，按用户 `device_limit` 对 distinct IP 做全局准入；重复上报同一 IP 刷新，不额外占槽。
- `device_limit=0` 表示不限；有效窗口读取 Flyboard 设置，最小 60 秒，包当前基线 120 秒。
- 设备/alive 和连接数 `online>0` 是在线证据；代码注释和后续修复要求**流量报告不能单独刷新 presence**，避免断线后永久在线。
- 完整快照需对连续缺失作保守处理（阈值 2）；断线/空快照不可立即清除其他节点/用户的全局槽。
- 过期清理同步修正 UI `online_count`；清理用户设备后向所有已连节点重发状态。
- HTTP `/devices` 和 WS `sync.devices` 必须表达同一全局状态。

### 4.3 WebSocket

- Workerman 单 worker；连接后 10 秒鉴权超时；节点用 server token+node id，机器用 machine id+token。
- 事件：入站 `pong`, `node.status`, `report.devices`, `request.devices`；出站 `ping`, `auth.success`, `sync.config`, `sync.users`, `sync.devices`。
- 每 55 秒 ping；每 10 秒 prune 全局租约，并将受影响状态排入待推送集合；另一 10 秒 timer 每次弹出最多 100 个节点并推送。
- 节点连接/重连 full sync 必须包含 config/users/devices；device report 的拒绝结果必须被尊重，之后向所有已连节点广播全局状态。
- README 明确：外部定制 `xboard-node` 不在面板包内；单靠面板无法保证转发前限制。节点端必须实现兼容协议和 fail policy。

### 4.4 队列和调度

- Compose 主服务启用 Horizon；专用队列 `flyboard_upstream`，worker 审计配置 timeout 45s、tries 1。
- `FlyboardPullJob`：Redis connection/专队列、同一 upstream 并发去重；排队超 540 秒失败；状态 pending→running→completed/error；更新上游 `last_error`。
- 网络传输置于独立 subprocess，敏感参数经 stdin 而非 argv；外层硬超时约 request timeout+1 秒。
- Compose `scheduler` 等待 Redis Unix socket 后运行 `php artisan schedule:work`，且不启用 Horizon。
- `cleanup:online-status` 将 DB `online_count` 与 display Redis 状态对齐；源码快照未显示 Laravel Kernel 调度声明，主要实时清理由 WS timer 承担。重建需明确选择 scheduler cadence，不能假称现有 Kernel 已注册。

## 5. 新实现验收矩阵

| 领域 | 验收场景 | 期望 |
|---|---|---|
| 页面/认证 | `/flyboard` 加载；未登录调用全部管理读写 API | 壳 200；管理 API 401/403；无用户/Token 泄漏 |
| 管理权限 | 非白名单访问管理入口/API；白名单+有效 admin | 前者 403 并审计，后者正常；有可验证的 emergency off 流程 |
| 上游 CRUD | 新增、编辑、停用、删除上游 | 字段 round-trip；删除级联节点；其他上游不受影响 |
| 上游安全 | loopback/private/metadata/CGNAT/IPv6/带凭据/非 HTTP/非标准端口/混合 DNS | 全部 fail closed，错误不回显凭据 |
| 上游边界 | DNS hang、HTTP hang、超大 body、错误格式 | 硬超时；max bytes 生效；旧节点保留或按明确事务策略回滚，不产生半导入 |
| 队列 | 同 upstream 6 个并发提交 | 仅 1 个实际 job；其余返回 duplicate/running；状态最终一致 |
| 队列故障 | Redis/worker 不可用、任务卡死 | 快速返回可重试错误；pending 最迟 10 分钟转 error；管理 HTTP 不阻塞 |
| 解析 | URI、base64 URI、非法节点、YAML/JSON | 支持格式正确导入；非法过滤；YAML/JSON 明确不支持或新规范明确扩展 |
| 独立订阅 | 无 Token、禁用、过期、超额、非法 format | 404/403/403/403/422；均有 no-store，必要日志完整 |
| 独立格式 | default、raw、base64、Clash/sing-box UA | 正文及响应头与契约一致；QR 为有效 SVG 且只编码订阅 URL |
| 客户端并发 | `device_limit=1` 两不同指纹同时请求 | 原子地只允许一个；窗口到期后新 owner 可接管 |
| 主订阅 Owner Lock | 首设备、同指纹、第二 IP/UA、并发首请求 | 首/同指纹通过；第二拒绝 429；并发仅一个 owner |
| IP 迁移 | 同 UA/IP-family，旧租约活跃 vs 静默超过 grace | 活跃时拒绝；静默后原子迁移并释放旧节点槽 |
| 跨节点设备 | A 节点手机 1 在线，B/C 节点手机 2 连接 | `device_limit=1` 时手机 2 在转发前被拒；所有节点状态一致 |
| 节点重连 | WS 断开重连、机器模式多节点 | full sync 含 config/users/devices；重连不清空首设备槽 |
| 快照容错 | 一次空/缺失上报、连续两次缺失、节点断连 | 一次不误踢；达到阈值/租约后释放；不清别的节点状态 |
| Presence | 仅 traffic 持续、device/alive 停止 | 租约仍按窗口过期，`online_count` 归零 |
| 清理 | 管理员清用户设备 | Redis enforcement/display 同时清；DB count 对齐；所有节点获 `sync.devices` |
| WS 时序 | report.devices 超限 | 拒绝被 handler 尊重；不得短暂下发允许状态 |
| HTTP/WS 一致 | 比较 `/server/devices` 与 `sync.devices` | 用户→IP 映射语义一致，兼容 `devices/users/data` |
| 故障策略 | Redis 故障时新设备连接 | 必须预先定义并测试 fail-closed/open；安全目标建议限制路径 fail-closed，不能静默绕过 |
| 日志隐私 | allowed/blocked、面板白名单、任务错误 | 可追责但不记录明文订阅 Token/完整 URL/密钥；删除功能按权限工作 |
| 性能 | 拉取任务运行期间持续采样管理 API | 无单 worker 卡死；响应时延不因网络 timeout 同步放大 |
| 迁移 | 空库运行 migration；已有部分表/列升级 | 幂等成功、索引/FK正确；rollback 行为有测试 |
| 节点兼容 | 官方节点与定制节点分别连接 | 官方节点仅基础协议，不虚假宣称全局转发限制；定制节点通过跨节点并发测试后才标记兼容 |

## 6. 疑似源自 XBoard、不能直接复制的内容

### 明确是 XBoard 宿主代码/资产

- 固定镜像 `ghcr.io/cedar2025/xboard@sha256:...`、容器目录 `/www`、Octane/Horizon 启动方式及 Redis Unix socket布局。
- `AdminRoute.php` 除 Flyboard 小段外的配置、邮件模板、套餐、订单、用户、节点/机器等整套路由。
- `web.php` 的主页、动态 `secure_path`、`subscribe_path`、管理 SPA；`admin.blade.php` 管理壳。
- `ClientController`, `Client/AppController`, `User/ServerController`, `ServerRoute`, `UniProxyController`, `ServerService`, `NodeWorker` 的大部分都是 XBoard 主干或深度覆盖。
- XBoard middleware/response 基类：`admin`, `client`, `server.v2`, `log`, `ApplyRuntimeSettings`, `ForceJson`, `Language`，以及 Controller `success/error` 包装。
- XBoard models/schema/helpers：User、Server、ServerMachine、NodeRegistry、NodeSyncService、`admin_setting()`、CacheKey、用户套餐/节点授权关系。
- `XBOARD_ACCESS_TOKEN` localStorage 约定、动态管理路径和现有前端 token 包装。
- BaconQrCode、Workerman、Laravel/Horizon 依赖是否可用取决于 XBoard 镜像；独立项目不能假定存在。

### 来源/授权与质量风险

- 包名和 README 自称“FlyBoard/XBoard”，且安装方式是对固定 XBoard 镜像覆盖 26 个文件；没有许可证/来源清单。**在完成许可证核验前，不应逐字复制 XBoard PHP/Blade、路由或 UI。**
- 可重建的是经黑盒可观察的行为、数据契约和协议；建议 clean-room 重写 Flyboard domain、REST API、队列、设备租约和 adapter。
- 定制 `xboard-node globaldevices5-device-limit2` 为外部二进制，历史审计曾指出离线包无源码；不可复制、不可仅凭字符串证明安全。应定义开放节点协议并自行实现/测试，或取得源码及授权。
- 历史 `FlyboardController` 含同步任意 URL 拉取和 TCP probing；事故中造成面板超时。该段不仅不应复制，也不满足当前安全契约。
- 主用户白名单与整个 XBoard 后台白名单是后期定制，概念曾多次混淆；独立产品应拆成不同命名/权限域，避免照搬 UI。

## 7. 重建建议的边界

建议将新实现拆为：`flyboard-core`（上游/客户端/订阅/审计）、`xboard-adapter`（主用户、节点可见性、管理鉴权）、`node-control-protocol`（HTTP+WS sync）、`safe-fetch-worker`。以本报告的可观察契约和验收矩阵为准，不以覆盖文件内部类结构为准。

需在正式规格中补决策的点：
1. 独立实现是否仍兼容 XBoard 动态 `{admin}` 路径和 success wrapper；
2. Redis 故障的限制 fail policy；
3. YAML/JSON 订阅是否列入新范围；
4. 面板白名单是否属于 Flyboard 产品还是宿主 SSO/WAF；
5. 是否兼容历史错误路径；
6. 主用户白名单究竟是豁免何种策略（当前快照的数据和 UI 存在，但业务语义不够自洽）。
