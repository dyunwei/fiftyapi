# Changelog

All notable changes to FiftyAPI are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

English | 中文说明见各版本下方折叠区域

## [Unreleased]

### Added

- Weighted routing across OAuth accounts and API gateway sources that expose the same public model ID.
- Compact, accessible help tips for advanced account and gateway configuration.

### Changed

- Gateway model mapping now supports a denser responsive editor, mapping search and bulk clearing.

<details>
<summary>中文说明</summary>

**新增**

- 对外模型 ID 相同时，OAuth 账号与 API 网关来源可按优先级和权重共同调度。
- 为账号与网关的高级配置增加紧凑且支持无障碍访问的帮助提示。

**变更**

- 网关模型映射改为更紧凑的响应式编辑器，并支持搜索和批量清除。

</details>

## [0.1.0] - 2026-11-04

First public preview.

### Added

- **Upstream accounts** – Claude Code, OpenAI Codex and Antigravity OAuth with authorization, token refresh and model catalog sync.
- **API gateways** – connect Sub2Api, New API, other FiftyAPI instances and compatible upstreams, with model aliases.
- **Native protocols** – Chat Completions, Messages and Responses (HTTP, SSE and WebSocket); one model can declare several protocols.
- **Scheduling** – priority, weights, concurrency limits, health checks, cooldown and safe retries.
- **Budgets** – per-user dollar budgets with 5-hour and 7-day windows shared across all keys and providers.
- **Logs & settlement** – personal request logs, model pricing, automatic settlement and audit trail.
- **Members** – first-run admin setup, self-registration, email invitations and account contribution links.
- **Operations** – single binary or Docker image, automatic master key generation, local and S3 / Cloudflare R2 backups.
- **UI** – English and Simplified Chinese, mobile friendly.

### Known limitations

- Claude OAuth has only been verified against simulated upstreams.
- Compact with the newer Codex client identity and long-running native workflows are not yet verified.
- Single instance only; image/audio generation and payments are out of scope.

See [compatibility](docs/en/compatibility.md) for details.

<details>
<summary>中文说明</summary>

首个公开预览版。

**新增**

- **上游账号**：Claude Code、OpenAI Codex、Antigravity OAuth 授权、令牌刷新与模型目录同步。
- **API 网关**：接入 Sub2Api、New API、其他 FiftyAPI 及兼容上游，支持模型别名。
- **原生协议**：Chat Completions、Messages、Responses（HTTP、SSE、WebSocket），同一模型可声明多种协议。
- **调度**：优先级、权重、并发上限、健康检查、冷却与安全重试。
- **额度**：每人 5 小时与 7 天美元额度，所有 Key 和供应商共享。
- **日志与结算**：个人请求日志、模型定价、自动结算与操作审计。
- **成员**：首次访问设置管理员、自助注册、邮件邀请、账号贡献链接。
- **运维**：单文件或 Docker 部署，自动生成主密钥，本地及 S3 / Cloudflare R2 备份。
- **界面**：简体中文与 English，适配手机。

**已知限制**

- Claude OAuth 目前只有模拟验证。
- 新版 Codex 身份下的 compact 和长时间原生工作流尚未验证。
- 仅支持单实例；不支持图片/音频生成和支付。

详见 [兼容性](docs/compatibility.md)。

</details>

[Unreleased]: https://github.com/dyunwei/fiftyapi/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dyunwei/fiftyapi/releases/tag/v0.1.0
