# Flyboard 独立重建硬性范围

日期：2026-10-25

## 基线

以 `full-panel-package/flyboard-full-panel-a-baseline-v1/payload/compose.yaml` 的实际 Compose 挂载文件、A 当前实际生效行为及 `research/feature-contract.md` 为功能基线。建立“旧挂载/行为 → Flyboard 新模块 → 测试用例”的逐项追踪矩阵；未映射、未实现或未测试的条目不得标记完成。

## 必须排除

发行物、安装包、测试夹具和仓库不得包含 A 的：

- 生产密钥、APP_KEY、管理员密码或会话；
- 数据库内容、Redis/Valkey 状态、用户业务数据；
- 节点 ID、节点 token、机器 token、订阅 token、上游凭据；
- 日志中的真实 IP、URL 或其他生产标识。

新服务器安装时必须独立生成数据库凭据、Valkey 凭据、加密主密钥、管理员路径、首个管理员凭据及节点注册凭据。

## 硬性功能

1. v13 跨节点全局设备限制：共享租约、跨节点原子准入、快照缺失容错、租约清理、HTTP/WS 状态一致、节点转发前拒绝。
2. 当前 Owner Lock：订阅 IP+UA owner、并发首设备原子获胜、窗口控制、合规 IP 迁移、释放节点设备槽、白名单与审计。
3. 完整独立管理后台：管理员认证、用户、上游、节点、客户端、设备、白名单、日志和设置。
4. 完整路由/API、异步安全拉取 worker、scheduler、WebSocket control plane、可审计的独立 node-agent。
5. 一键 Docker 安装、升级、备份、恢复、卸载和安装后 doctor/验收。
6. 不依赖、不复制、不翻译 Xboard/V2Board 源码、前端资产、镜像或无源码节点二进制。

## 完成门禁

- 单元、集成、契约、并发、WebSocket、节点准入和全新服务器 E2E 全部通过；
- 新服务器实际安装成功，管理员凭据和访问地址正确输出；
- 用户亲自确认安装与关键功能成功；
- 在此之前不得创建或上传 `sohefan5118-cmd/Flyboard`。
