# 客户端接入

先在网页创建自己的 API Key。客户端使用 `sk-…` Key，不使用管理员密码或上游 OAuth 令牌。下面为本机示例；其他部署替换为自己的访问地址，内网可使用 HTTP，公网建议 HTTPS。模型名称以本项目 `/v1/models` 返回的 ID 为准，重名模型可能加供应商前缀。

使用 Codex 或 Claude Code 时，也可以在网页的「使用密钥」中复制[一键配置命令](#一键配置客户端)。

## Claude Code / Messages

在启动客户端的终端设置：

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN='YOUR_FIFTYAPI_KEY'
export ANTHROPIC_MODEL='MODEL_ID_FROM_CATALOG'
claude
```

base URL 不加 `/v1`，客户端调用 `/v1/messages`。如果终端原先配置了供应商的 `ANTHROPIC_API_KEY`，请清除冲突配置。Messages 路由到 Claude 或支持 Messages 的 API 上游。Antigravity 需要原生 v1internal 客户端，见 [低维护账号共享](low-maintenance.md)。`/v1/messages/count_tokens` 为本地估算，不代表供应商精确分词。

历史 Antigravity Messages 转换器的工具闭环验收不适用于当前原生链路。仓库尚无真实 Claude OAuth 与原生 Claude Code 验收记录；已记录的范围见[兼容性](compatibility.md)。

## Codex / Responses

在 `~/.codex/config.toml` 添加自定义 provider，保留原有其他设置：

```toml
model_provider = "fiftyapi"
model = "MODEL_ID_FROM_CATALOG"

[model_providers.fiftyapi]
name = "fiftyapi"
base_url = "http://127.0.0.1:8080/v1"
env_key = "FIFTYAPI_API_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = true
```

```sh
export FIFTYAPI_API_KEY='YOUR_FIFTYAPI_KEY'
codex
```

`env_key` 和 Responses provider 的配置方式参考 [OpenAI 自定义 Responses provider 示例](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)。这里环境变量保存的是本服务 Key，服务端才负责供应商授权。已使用本机 Codex 0.164.0-alpha.11.1、gpt-6.1-sol 完成固定短提示的 WebSocket 接入验收；长时间工具工作流仍需按实际场景验证。

OAuth Responses 路由 OpenAI 账号；API Key 上游按配置的 Responses 协议直通。支持 HTTP/SSE 和 `/v1/responses/compact` 接口，上游 compact 能力需单独确认。携带 `client_version` 的目录请求保留上游模型元数据并筛选已启用、已定价模型。HTTP/SSE 使用 `store:false` 完整历史；原生 WebSocket 支持同连接内的 `previous_response_id`，连接丢失后必须发送完整历史。同连接内顺序处理，多连接可并行；不支持 steer、显式 cancel 或把 Gemini 转成 Responses。加速档位需要上游支持且自动价格目录包含对应费率，见[价格规则](low-maintenance.md#自动价格与模型)。API Key 上游要显式开启 WebSocket 且确认上游支持，否则将上述 supports_websockets 改为 false。详见 [账号与网关](reliability-redesign.md)。

## 最小 API 调用

```sh
export FIFTYAPI_API_KEY='YOUR_FIFTYAPI_KEY'
curl -sS http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer $FIFTYAPI_API_KEY"
```

```sh
curl -N http://127.0.0.1:8080/v1/messages \
  -H "Authorization: Bearer $FIFTYAPI_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"YOUR_MESSAGES_MODEL_ID","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK"}]}'
```

先由管理员为模型设置内部价格并给当前用户额度。401 检查本服务 Key/用户状态；400 检查模型启用、价格和协议；429 检查额度或上游限流；503 检查账号就绪、并发及会话绑定。断流有可靠用量时自动扣除，缺失或无效用量按零结算并释放预占；账本写入异常保留核算证据，但历史异常本身不会限制后续调用。日志说明结算结果，只有结算异常需要管理员核实。诊断时仅提供状态、请求 ID 和脱敏错误，不公开 Key 或授权回调地址。

## Cherry Studio / Chat Completions

已取消 Chat→Messages 自动转换。请选择模型目录声明的协议：

| 本地来源/能力 | 客户端协议 | 路径 |
| --- | --- | --- |
| Claude、仅 Messages 的 API 模型 | Anthropic Messages | /v1/messages |
| Codex OAuth、支持 Responses 的 API 模型 | OpenAI Responses | /v1/responses |
| 声明支持 Chat 的上游 API 模型 | OpenAI Chat Completions | /v1/chat/completions |
| 同时声明 Chat 与 Responses 的 API 模型 | 两种模式均可，使用同一 ID | 按模式选择对应路径 |

Chat/Responses 的 Base URL 通常填写 http://127.0.0.1:8080/v1，Messages 通常填站点根地址。模型 ID 必须使用目录中的完整 ID；Cherry Studio 的供应商 UUID 前缀属于客户端内部标识。

目录 protocols、supported_endpoint_types 和 supported_endpoints 只声明实际配置的能力。升级后重新拉取目录，检查模型的接口模式；旧的 Messages 模型不再声明支持 Chat。Antigravity 需要支持原生 v1internal 的客户端，不能使用 Anthropic Messages 模式。某个客户端不读取这些字段时，需要手工选择协议。

原生 Chat 保留工具、推理、输出格式、图片等标准/扩展字段，具体能力由上游决定；只替换上游模型 ID，并为流式请求开启 usage。不会把其他协议响应转换成 Chat。价格与路由设置见 [多协议配置](gateway-upstreams.md#协议与边界)。

```sh
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer $FIFTYAPI_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"YOUR_CHAT_MODEL_ID","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Reply OK"}]}'
```

若 Responses 模型返回 Not Found，核对实际请求 URL 是否为 `/v1/responses`，以及是否意外拼成 `/v1/v1/responses`。若服务端没有对应日志，还需要排查客户端本地模型记录、鉴权或到达网关前的请求路径，不能直接认定上游模型权限错误。

协议选择参考 [Cherry Studio v2.1.7 端点解析](https://github.com/CherryHQ/cherry-studio/blob/v2.1.7/src/main/ai/provider/endpoint.ts) 和 [目录适配](https://github.com/CherryHQ/cherry-studio/blob/v2.1.7/src/main/ai/provider/listModels.ts)。

## OpenAI 实测状态

`gpt-5.6-luna` 的真实 Responses JSON、SSE 工具往返和客户端取消已通过，日志与两窗口额度核对一致。上游误标 SSE 响应类型导致的 502 已修复。旧 0.146.0 身份下上游 `/responses/compact` 返回 404，新身份的 compact 尚待重新验收，现阶段不应依赖该账号自动压缩上下文；不会静默改用普通推理伪造压缩结果。原生 Codex 固定短提示与 WebSocket 已验收，详见文末记录。详见 [验收记录](openai-acceptance.md)。

将默认 OpenAI 客户端身份统一为实测的 0.164.0，已完成 `gpt-6.1-sol` 真实核心调用；旧版本声明可能导致目录缺少模型甚至直接调用返回 `model_not_found`。账号中的“验证并同步”会显示具体结果，见 [账号模型说明](account-models.md)。

## 调用受限状态

成员首页的「调用受限」区列出未分配、已耗尽或被在途请求预占的额度窗口。已耗尽且正在计时的窗口显示预计重置时间。在途请求完成后，刷新首页可查看最新额度。结算异常可在请求日志中查看。

## 客户端自检

在「API 密钥 → 使用」选择客户端后，页面会显示可用上游、模型启用或定价、访问权限、额度及协议中的第一个卡点。管理员可直接进入对应页面处理。这项检查读取当前登录用户的配置和额度，不调用上游。配置客户端后，继续用下面的命令检查实际连接。

在「使用 API 密钥 → 客户端接入自检」复制适合当前系统和客户端的命令。也可以从站点下载当前的 `/setup.py`，执行 `python3.11 fiftyapi-setup.py check codex` 或 `python3.11 fiftyapi-setup.py check claude`。Windows 使用 `py -3.11`。

在配置客户端的同一用户和终端运行。助手比对当前本机配置与保存的接入配置，再通过 `/v1/models` 检查连通性、密钥和模型协议。检查不会改写配置或发送推理，也不会输出密钥。Codex 显示配置是否启用 WebSocket；实际传输以[请求诊断](request-logs.md#请求诊断)为准。

网关在客户端使用的 API 路径上发出三条本地 SSE 测试事件，助手显示接收间隔。事件挤在一起时检查反向代理的响应缓冲或压缩。Codex 还会在 `/v1/responses` 验证 WebSocket 升级响应，随后断开，过程中不会发出推理请求。Codex 配置开启 WebSocket 时，握手失败会令自检失败。Claude Code 只检查 SSE。新版助手需要服务器支持 SSE 探测，老版本可能返回 400 或 405。结果只覆盖运行助手的网络路径，不保证上游支持 WebSocket 或实际模型调用成功。

## 一键配置客户端

在「使用密钥」中选择客户端、系统和模型，复制「一键配置」命令。需要 Python 3.11+（macOS/Linux 使用 `python3.11`，Windows 使用 Python launcher `py -3.11`）。助手由当前站点 `/setup.py` 提供，可以先下载审阅。无需管理员权限，不安装客户端。

VS Code 用户应在使用 Codex 的窗口中打开新终端执行。Remote SSH、WSL、Dev Container 会配置对应环境中的用户；不要使用 sudo。容器重建后保留配置需要持久化用户目录。助手识别 `CODEX_HOME`、`CLAUDE_CONFIG_DIR`。完成后重新加载窗口并新建会话。IDE 插件可能自带客户端，终端中的 CLI 版本不能代表插件版本。

输入密钥时终端不回显。助手仅请求 `/v1/models` 验证密钥、模型和协议，不发起推理。拒绝重定向，避免将密钥转发到其他地址。公网使用 HTTPS。Codex 使用 Responses，Claude Code 使用 Messages；WebSocket 仅在明确勾选时启用。

Codex 使用 `experimental_bearer_token` 持久保存密钥，以便从桌面启动的 VS Code 也可读取，不依赖 shell 环境变量，不修改 `auth.json`。该字段官方不推荐用于一般密钥管理；本助手为插件接入采用本机明文存储。Claude Code 合并用户 settings.json 的 env，并将三个模型别名统一指向所选模型；已继承的鉴权、地址或云供应商环境变量需要用户先清除。项目或组织设置仍可能覆盖用户配置。Claude Code VS Code 插件尚未验收。

配置和恢复会保留解析后的其他设置，Codex 的 TOML 会重新排版，项目路径分别写在各自的 `[projects."路径"]` 节中，注释保存在原文件备份中。每次修改前保存完整原文件，恢复只处理受管理字段。检测到这些字段被其他工具修改时停止，需手动解决；不会强行覆盖。原文件不存在且恢复后没有其他设置时删除文件。

页面默认展示一键配置，手动编辑文件的步骤收在「手动配置（可选）」中。

「恢复接入前的配置」指恢复首次通过助手接入 FiftyAPI 前的模型、服务地址与鉴权设置，保留之后修改的其他设置。例如此前使用 Codex 官方服务，恢复后沿用原来的接入方式。这不等于重置为客户端出厂默认设置。每次修改前另有完整文件备份，但目前不支持选择某个时间点自动恢复；不要将这两种恢复方式混淆。页面会按当前客户端与系统显示可直接复制的恢复命令。

助手保存于 `~/.fiftyapi-helper/helper.py`，使用示例：

```sh
python3.11 ~/.fiftyapi-helper/helper.py status codex
python3.11 ~/.fiftyapi-helper/helper.py configure codex
python3.11 ~/.fiftyapi-helper/helper.py restore codex
python3.11 ~/.fiftyapi-helper/helper.py use codex
python3.11 ~/.fiftyapi-helper/helper.py uninstall codex
```

Claude Code 将 `codex` 换成 `claude`。Windows 用 `py -3.11 "$HOME\.fiftyapi-helper\helper.py" restore codex` 等对应命令。configure 可重新选择站点、密钥和模型，恢复保留原接入字段。每次操作只处理一个客户端。

配置、恢复状态、备份均可能含密钥，只保存在本机，不要提交或分享。Unix 新文件仅用户可访问；Windows 应使用私有用户目录并检查 ACL。卸载会恢复受管理配置，删除对应恢复状态；全部客户端卸载后移除助手，但保留 `~/.fiftyapi-helper/backups/`，备份需用户自行清理。如果进程异常退出，确认没有助手运行后可移除 `operation.lock` 目录重试。

### 验证范围

2026-11-14：Python 3.11 测试覆盖模拟模型目录（Responses / Messages）、重复配置、恢复/重新启用、冲突拒绝、其他字段保留及符号链接拒绝。本机 Codex CLI **0.169.0-alpha.11.1** 使用隔离 CODEX_HOME，通过 `features list` 解析含模拟模型的持久鉴权配置；未发送推理请求。没有对 Windows、macOS、Remote SSH、VS Code 插件或 Claude Code 原生程序做端到端验收，也没有验证 DeepSeek/Kimi 等真实模型兼容性。模型实际能力以现有[兼容性记录](compatibility.md)为准。
