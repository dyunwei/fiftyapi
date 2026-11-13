# Changelog

All notable changes to FiftyAPI are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

English | 中文说明见各版本下方折叠区域

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
