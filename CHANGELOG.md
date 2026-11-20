# Changelog

All notable changes to FiftyAPI are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

English | 中文说明见各版本下方折叠区域

## 0.3.1 (2026-11-20)

### Fixed

- Pending settlements no longer block subsequent inference requests. Requests without reliable usage or a calculable charge settle at zero and release their quota reservations.
- On startup, interrupted and legacy pending requests settle using reliable usage and the original price snapshot when available. Ledger write failures remain visible to administrators.

### Upgrade notes

- Upgrading from v0.3.0 requires no database migration. Restart on v0.3.1 to recover existing pending requests.

<details>
<summary>中文说明</summary>

- 修复待核算请求导致后续模型调用被拒绝的问题。无法取得可靠用量或无法可靠计费的请求按零结算并释放预占额度。
- 启动时恢复中断及历史待核算请求。有可靠用量时按原价格快照结算。管理员仍可查看账本写入异常。
- 从 v0.3.0 升级无需数据库迁移。重启到 v0.3.1 后会处理已有待核算记录。

</details>

## 0.3.0 (2026-11-20)

### Added

- Request logs show the API protocol and observed client transport, with per-hop diagnostics, actionable error details, and a redacted copy action.
- The client setup helper checks connectivity, key acceptance, model compatibility and SSE delivery. Codex also checks the WebSocket handshake. The transport checks use no upstream inference quota.
- The member overview explains budget and settlement blocks and opens diagnostics for the affected request.
- The admin dashboard shows maintenance tasks, supports opt-in SMTP alerts, and reports average SQLite connection wait, upstream account queue time and first streaming output time since startup.
- `fiftyapi restore --check` verifies backup integrity, schema compatibility and the original master key while the service is running.

### Fixed

- The container smoke test verifies that a rejected WebSocket Origin produces one zero-cost diagnostic and that successful handshakes without inference produce no request entries.

### Upgrade notes

- Back up the database and the original master key before upgrading from v0.2.1. Startup applies append-only migrations `022_request_diagnostics.sql` and `023_maintenance_alerts.sql`. To roll back, restore the pre-upgrade database with the older binary.
- Existing requests without diagnostics show **Not recorded** for protocol and transport. To enable maintenance alerts, enter a recipient under **System settings → Email configuration**.
- Download the updated client helper from the upgraded gateway. Transport probes cover the client-to-gateway path. Release verification uses simulated upstreams. See [compatibility](docs/en/compatibility.md).

<details>
<summary>中文说明</summary>

**新增**

- 请求日志同时显示 API 协议与实际客户端传输，提供分段诊断、失败处理提示和脱敏复制。
- 客户端助手检查连通性、API Key、模型兼容性和 SSE 事件到达情况。Codex 还会检查 WebSocket 握手。传输探测不消耗上游推理额度。
- 成员首页显示额度与待核算阻塞原因，并可直接打开对应请求的诊断。
- 管理仪表盘新增维护待办、可选 SMTP 告警，以及从服务启动开始统计的 SQLite 连接等待、上游账号排队和流式首段输出平均耗时。
- `fiftyapi restore --check` 可在服务运行时验证备份完整性、数据库版本和原主密钥。

**修复**

- 容器冒烟测试核对被拒绝的 WebSocket Origin 会产生一条零费用诊断，成功握手且未发起推理时不会产生请求记录。

**升级说明**

- 从 v0.2.1 升级前备份数据库和原主密钥。启动时自动执行追加迁移 `022_request_diagnostics.sql` 和 `023_maintenance_alerts.sql`。回退时用旧版程序配合升级前的数据库备份。
- 没有诊断信息的历史请求，协议及传输显示「未记录」。需要邮件告警时，在「系统设置 → 邮件配置」填写收件邮箱。
- 从升级后的网关下载新版客户端助手。传输探测覆盖客户端到网关的链路。本版使用模拟上游验证，详见 [兼容性](docs/compatibility.md)。

</details>

## 0.2.1 (2026-11-19)

### Changed

- Hide the page scrollbar while keeping native scrolling and scrollbars in dialogs, data panels, and wide tables.
- Simplify the documentation navigation and consolidate related guides.

### Fixed

- Write Codex configuration using explicit TOML sections.
- Pass the morning inference probe setting through Docker Compose.

### Upgrade notes

- Upgrading from v0.2.0 requires no database migrations or configuration format changes. Docker Compose now honors the existing morning inference probe setting.

<details>
<summary>中文说明</summary>

**调整**

- 隐藏整页纵向滚动条，保留原生滚动方式以及弹窗、数据面板和宽表格的滚动条。
- 简化文档导航并合并相关指南。

**修复**

- 使用显式 TOML 节写入 Codex 配置。
- 修复 Docker Compose 未传入晨间推理探测设置的问题。

**升级说明**

- 从 v0.2.0 升级无需数据库迁移或修改配置格式。Docker Compose 现在会读取已有的晨间推理探测设置。

</details>

## 0.2.0 (2026-11-18)

### Added

- Automatic LiteLLM reference-price catalog synchronization, explicit catalog-key mapping, immutable tier/cache price snapshots, and manual price overrides.
- Centralized client identity defaults and daily Codex release candidates, with explicit administrator activation.
- Per-user model allowlists and concurrency limits shared across HTTP, SSE, and WebSocket requests.
- A Python 3.11+ client configuration helper for Codex and Claude Code, with local backups, conflict-aware restore, and copyable setup/restore commands.
- Persistent account-scoped OpenAI device identity and session holds across retries and restarts.

### Changed

- **Breaking:** Antigravity now forwards native `v1internal` requests and responses. Messages conversion has been removed; clients must support the native Antigravity protocol.
- Claude and Codex forwarding preserve client instructions, metadata, and supported native request fields by default. Client identity normalization is configurable.
- Price synchronization and public Codex release checks run daily by default; morning inference probes are off by default. Unknown prices or usage retain safe reservation release and administrator settlement behavior.
- Compact table filters, account search, single-screen desktop dashboards and email settings, authorization-detail dialogs, and refreshed bilingual screenshots.
- Compose supports configurable host listening addresses and data mounts; local startup loads repository `.env` settings.

### Fixed

- Antigravity model tests now build and preview native requests, use the native inference origin, and expose sanitized 429 retry diagnostics without automatic retries.
- Model-test JSON responses are formatted for readability.

### Upgrade notes

- Back up the database and its original master key before upgrading from v0.1.1. New append-only migrations are `018_openai_session_identity.sql`, `019_price_catalog.sql`, `020_native_antigravity.sql`, and `021_user_access.sql`; startup applies them automatically. Existing accounts and historical accounting records are preserved. Rollback requires the pre-upgrade database and older binary.
- Existing Antigravity sources become native-only. Messages clients must switch to a compatible source or use a native Antigravity client.
- Static bundled prices have been removed. Confirm model pricing after upgrade; select a catalog key for unmatched aliases or retain manual prices. Unpriced models remain unavailable.
- `FIFTYAPI_AUTO_MAINTENANCE=false` disables background public price/release checks while retaining cached data and manual sync. Necessary account refresh and catalog maintenance remain active.
- This release was checked with simulated upstreams. Historical Antigravity converter acceptance does not establish compatibility for the new native path. Client-helper platform and extension limitations are documented in [client setup](docs/en/clients.md#automatic-client-setup); provider verification scope is in [compatibility](docs/en/compatibility.md).

<details>
<summary>中文说明</summary>

**新增**

- LiteLLM 参考价格目录自动同步、显式目录键映射、档位与缓存价格快照，以及手动价格覆盖。
- 集中管理客户端默认身份，每日发现 Codex 候选版本，由管理员明确启用。
- 每位成员的模型白名单与并发限制，HTTP、SSE 和 WebSocket 共用。
- Python 3.11+ 客户端配置助手，支持 Codex 与 Claude Code、本地备份、冲突检查、恢复，以及可复制的配置与恢复命令。
- OpenAI 账号级持久化设备标识，重试和重启保留会话等待状态。

**调整与修复**

- **不兼容变更**：Antigravity 改为原生 `v1internal` 请求与响应透传，移除 Messages 转换，客户端须支持原生 Antigravity 协议。
- Claude 与 Codex 默认保留客户端指令、元数据及支持的原生请求字段；客户端身份规范化可配置。
- 默认每日同步价格并检查公开 Codex 候选版本，晨间推理探测默认关闭。价格或用量不确定时仍释放预占并由管理员核算。
- 表格筛选栏更紧凑，新增账号搜索；桌面仪表盘与邮件配置可单屏显示，授权详情改为弹窗，更新中英文截图。
- Compose 支持配置宿主机监听地址与数据挂载；本地启动脚本加载仓库 `.env`。
- Antigravity 模型测试使用原生请求和推理地址，展示脱敏的 429 重试诊断，不自动重试；模型测试 JSON 响应格式化显示。

**升级注意事项**

- 从 v0.1.1 升级前备份数据库及原主密钥。新增迁移为 `018_openai_session_identity.sql`、`019_price_catalog.sql`、`020_native_antigravity.sql`、`021_user_access.sql`，启动时自动执行，保留账号和历史账务记录。回退须恢复升级前数据库并使用旧版程序。
- 既有 Antigravity 来源改为仅原生协议；Messages 客户端须改用兼容来源，或使用支持原生 Antigravity 的客户端。
- 移除静态内置价格，升级后确认模型价格；别名未匹配时手动选择目录键，或保留手动价格。未定价模型仍不可调用。
- `FIFTYAPI_AUTO_MAINTENANCE=false` 可关闭后台公开价格与版本检查，保留本地缓存与手动同步；必要账号刷新与目录维护仍运行。
- 本版使用模拟上游验证，历史 Antigravity 转换器验收不适用于新的原生路径。客户端助手的平台与扩展限制见 [客户端配置助手](docs/clients.md#一键配置客户端)，供应商验证范围见 [兼容性](docs/compatibility.md)。

</details>

## 0.1.1 (2026-11-13)

### Added

- Proxy management with health checks and proxy selection for upstream accounts and account contribution links.
- Remaining budget progress for each member's 5-hour and 7-day windows in the team table.

### Changed

- Background model catalog synchronization now runs every 24 hours to reduce upstream requests.
- The environment example now explains source builds versus the published `dyunwei/fiftyapi` Docker image.

### Upgrade notes

- This release adds SQLite migration `017_proxy_servers.sql` for proxy settings and account/contribution bindings. Existing migrations remain unchanged and the new migration runs automatically at startup.
- Back up the database and its master key before upgrading. To roll back, restore the pre-upgrade database together with the older binary.
- Provider compatibility limitations remain unchanged; see [compatibility](docs/en/compatibility.md). No new live-provider compatibility is claimed.

<details>
<summary>中文说明</summary>

**新增**

- 代理管理、健康检查，以及上游账号和账号贡献链接的代理选择。
- 成员列表展示每位成员 5 小时与 7 天窗口的剩余额度进度。

**调整**

- 后台模型目录同步间隔调整为 24 小时，减少上游请求。
- 环境变量示例补充源码构建与使用已发布 `dyunwei/fiftyapi` Docker 镜像的说明。

**升级注意事项**

- 新增 SQLite 迁移 `017_proxy_servers.sql`，保存代理配置及账号、贡献链接绑定；现有迁移不变，新迁移在启动时自动执行。
- 升级前备份数据库和主密钥。回退时须同时恢复升级前的数据库和旧版程序。
- 上游兼容性限制保持不变，详见 [兼容性](docs/compatibility.md)；本版不新增真实供应商兼容性声明。

</details>

## 0.1.0 (2026-11-09)

First public preview.

### Added

- **Upstream accounts** – Claude Code, OpenAI Codex and Antigravity OAuth with authorization, token refresh and model catalog sync.
- **API gateways** – connect Sub2Api, New API, other FiftyAPI instances and compatible upstreams, with model aliases, mapping search and bulk clearing.
- **Native protocols** – Chat Completions, Messages and Responses (HTTP, SSE and WebSocket); one model can declare several protocols.
- **Scheduling** – priority, weights, concurrency limits, health checks, cooldown and safe retries; OAuth accounts and API gateways can share a routing pool for the same public model ID.
- **Budgets** – per-user dollar budgets with 5-hour and 7-day windows shared across all keys and providers.
- **Logs & settlement** – personal request logs, model pricing, automatic settlement and audit trail.
- **Members** – first-run admin setup, self-registration, email invitations and account contribution links.
- **Operations** – single binary or Docker image, automatic master key generation, local and S3 / Cloudflare R2 backups.
- **UI** – English and Simplified Chinese, responsive configuration forms and accessible help tips.

### Known limitations

- Claude OAuth has only been verified against simulated upstreams.
- Compact returned HTTP 404 for the tested OpenAI account with both older and newer client identities.
- A bounded native Codex multi-tool WebSocket workflow passed; long-duration real-project workflows remain unverified.
- Real OpenAI authorization-code exchange and refresh-token rotation remain unverified.
- Single instance only; image/audio generation and payments are out of scope.

See [compatibility](docs/en/compatibility.md) for details.

<details>
<summary>中文说明</summary>

首个公开预览版。

**新增**

- **上游账号**：Claude Code、OpenAI Codex、Antigravity OAuth 授权、令牌刷新与模型目录同步。
- **API 网关**：接入 Sub2Api、New API、其他 FiftyAPI 及兼容上游，支持模型别名、映射搜索和批量清除。
- **原生协议**：Chat Completions、Messages、Responses（HTTP、SSE、WebSocket），同一模型可声明多种协议。
- **调度**：优先级、权重、并发上限、健康检查、冷却与安全重试；相同对外模型 ID 的 OAuth 账号与 API 网关可共同参与路由。
- **额度**：每人 5 小时与 7 天美元额度，所有 Key 和供应商共享。
- **日志与结算**：个人请求日志、模型定价、自动结算与操作审计。
- **成员**：首次访问设置管理员、自助注册、邮件邀请、账号贡献链接。
- **运维**：单文件或 Docker 部署，自动生成主密钥，本地及 S3 / Cloudflare R2 备份。
- **界面**：简体中文与 English，响应式配置表单与支持无障碍访问的帮助提示。

**已知限制**

- Claude OAuth 目前只有模拟验证。
- 测试中的 OpenAI 账号在新旧客户端身份下调用 compact 均返回 HTTP 404。
- 有界的原生 Codex 多工具 WebSocket 工作流已通过，长时间真实项目工作流尚未验证。
- OpenAI 真实授权码换取与刷新令牌轮换尚未验证。
- 仅支持单实例；不支持图片/音频生成和支付。

详见 [兼容性](docs/compatibility.md)。

</details>
