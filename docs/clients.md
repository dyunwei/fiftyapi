# 客户端接入

先在网页创建自己的 API Key。客户端使用 `sk-…` Key，不使用管理员密码或上游 OAuth 令牌。下面为本机示例；其他部署替换为自己的访问地址，内网可使用 HTTP，公网建议 HTTPS。模型名称以本项目 `/v1/models` 返回的 ID 为准，重名模型可能加供应商前缀。

## Claude Code / Messages

在启动客户端的终端设置：

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN='YOUR_FIFTYAPI_KEY'
export ANTHROPIC_MODEL='MODEL_ID_FROM_CATALOG'
claude
```

base URL 不加 `/v1`，客户端调用 `/v1/messages`。如果终端原先配置了供应商的 `ANTHROPIC_API_KEY`，请清除冲突配置。Messages 可路由到 Claude 或 Antigravity 模型；后者由服务转换请求、工具结果和 SSE。`/v1/messages/count_tokens` 为本地估算，不代表供应商精确分词。

Antigravity `gemini-3.5-flash-lite` 的网关工具闭环已实测；真实 Claude OAuth 与 Claude Code 原生程序尚未验收。完整兼容性见 [验证记录](compatibility.md)。

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

OAuth Responses 路由 OpenAI 账号；API Key 上游按配置的 Responses 协议直通。支持 HTTP/SSE 和 `/v1/responses/compact` 接口，上游 compact 能力需单独确认。携带 `client_version` 的目录请求保留上游模型元数据并筛选已启用、已定价模型。HTTP/SSE 使用 `store:false` 完整历史；原生 WebSocket 支持同连接内的 `previous_response_id`，连接丢失后必须发送完整历史。同连接内顺序处理，多连接可并行；不支持 steer、显式 cancel、优先/快速服务等级或把 Gemini 转成 Responses。API Key 上游要显式开启 WebSocket 且确认上游支持，否则将上述 supports_websockets 改为 false。详见 [账号与网关](reliability-redesign.md)。

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
  -d '{"model":"gemini-3.5-flash-lite","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK"}]}'
```

先由管理员为模型设置内部价格并给当前用户额度。401 检查本服务 Key/用户状态；400 检查模型启用、价格和协议；429 检查额度或上游限流；503 检查账号就绪、并发及会话绑定。断流有可靠用量时自动扣除，缺失或无效用量按 0 释放预占；日志说明结算结果，只有结算异常需要管理员核实。诊断时仅提供状态、请求 ID 和脱敏错误，不公开 Key 或授权回调地址。

## Cherry Studio / Chat Completions

已取消 Chat→Messages 自动转换。请选择模型目录声明的协议：

| 本地来源/能力 | 客户端协议 | 路径 |
| --- | --- | --- |
| Claude、Antigravity、仅 Messages 的 API 模型 | Anthropic Messages | /v1/messages |
| Codex OAuth、支持 Responses 的 API 模型 | OpenAI Responses | /v1/responses |
| 声明支持 Chat 的上游 API 模型 | OpenAI Chat Completions | /v1/chat/completions |
| 同时声明 Chat 与 Responses 的 API 模型 | 两种模式均可，使用同一 ID | 按模式选择对应路径 |

Chat/Responses 的 Base URL 通常填写 http://127.0.0.1:8080/v1，Messages 通常填站点根地址。模型 ID 必须使用目录中的完整 ID；Cherry Studio 的供应商 UUID 前缀属于客户端内部标识。

目录 protocols、supported_endpoint_types 和 supported_endpoints 只声明实际配置的能力。升级后重新拉取目录，检查模型的接口模式；旧的 Messages 模型不再声明支持 Chat。Antigravity Gemini 请改为 Anthropic 模式。某个客户端不读取这些字段时，需要手工选择协议。

原生 Chat 保留工具、推理、输出格式、图片等标准/扩展字段，具体能力由上游决定；只替换上游模型 ID，并为流式请求开启 usage。不会把其他协议响应转换成 Chat。价格与路由设置见 [多协议配置](model-protocols.md)。

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
