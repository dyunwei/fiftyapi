# 贡献指南

欢迎提交 Issue 和 Pull Request。

## 开发环境

- Go 1.25+，推荐使用与 CI 一致的 Go 1.26.10；修改前端需要 Node 24+。
- 构建与测试命令见 [README](README.zh-CN.md#开发与构建)。

## 提交 PR 前

1. `make test` 通过；修改前端时同时运行 `make build` 和 `make test-ui`。
2. 不要提交数据库、`.local/`、`data/`、`.env`、OAuth 授权文件或任何真实令牌；示例使用占位符。
3. 修改前端源码后提交重新构建的 `internal/server/ui` 产物（仓库内置前端，以便无 Node 构建）。
4. 行为变化同步更新 `docs/` 中对应文档。
5. 测试不得调用真实供应商；真实验收测试默认跳过。

## 许可证

提交即表示同意以 [LGPL-3.0](LICENSE) 授权你的贡献。

## 中英文

修改界面文案时同步更新 `web/src/locales/en.json`；固定诊断消息位于 `internal/i18n/en.json`。`t()` 使用完整中文句子作为键，变量使用占位符。英文文档位于 `README.md` 和 `docs/en/`。修改后检查两种语言的桌面与手机布局。

## Contributing (English)

Issues and pull requests are welcome. Use Go 1.25+ (Go 1.26.10 recommended, matching CI) and Node 24+ for frontend changes.

Before submitting:

1. Run `make test`. For frontend changes, also run `make build` and `make test-ui`.
2. Never commit databases, `.local/`, `data/`, `.env`, authorization files or real credentials. Use placeholders in examples.
3. Commit rebuilt assets in `internal/server/ui` when changing frontend sources so Go builds work without Node.
4. Update the relevant documentation. Keep Chinese and English text in sync; use complete messages with placeholders in `t()`, with English catalogs in `web/src/locales/en.json` and shared diagnostics in `internal/i18n/en.json`.
5. Tests must use simulated upstreams; real-provider acceptance tests remain opt-in.

Contributions are accepted under [LGPL-3.0](LICENSE). Build commands are in the [English README](README.md#development).
