# 兼容性与验证范围

简体中文 | [English](en/compatibility.md)

本页汇总当前协议、使用边界和仓库已记录的验证证据。模型出现在目录中不等于推理可用；实现接口或通过模拟测试，也不代表所有账号、模型和第三方站点都支持。配置细节见[低维护账号共享](low-maintenance.md)，接入方法见[客户端指南](clients.md)。

## 当前支持的协议

| 上游来源 | 协议与接口 | 接入边界 |
| --- | --- | --- |
| Claude OAuth | Messages：`/v1/messages` | 默认透传 metadata/system；`count_tokens` 为本地估算 |
| OpenAI Codex OAuth | Responses：HTTP JSON/SSE、原生 WebSocket | compact 接口已实现，但已有真实测试返回 404，不能列为已验证可用 |
| Antigravity OAuth | 原生 `v1internal:generateContent` / `v1internal:streamGenerateContent` | 已移除 Messages 转换；需要支持原生 envelope 的客户端 |
| API Key 网关 | 显式声明的 Chat Completions、Messages、Responses | 不在标准协议间自动转换；WebSocket 必须单独启用并由上游支持 |

同一 API 模型可声明多个协议，共享对外 ID 和价格；请求使用的协议必须有可用路由。Chat 仅直通支持 Chat 的 API 上游，保留原生 JSON/SSE、工具和扩展字段，不将其他协议响应转换成 Chat。

Responses HTTP/SSE 使用完整历史；WebSocket 支持预热和同连接内的连续响应。单连接顺序处理，断开后需要发送完整历史；不支持 steer 或显式 cancel 消息。客户端连接每 30 秒发送 Ping，10 秒未收到 Pong 会结束连接、取消在途请求并释放连接名额；这不代表上游推理健康。

## 价格、结算和调度边界

- 预算使用参考 USD 的定点价格快照，不代表 OAuth 订阅实际扣款或剩余额度。模型未定价或请求档位缺少价格时拒绝调用。
- 自动目录支持明确收录的 Priority/Fast、Ultrafast、Flex、缓存及长上下文费率；手工价格仅支持标准档位。并非所有模型都有这些档位，具体见[价格规则](low-maintenance.md#自动价格与模型)。
- 取消或中断时按已收到的可靠用量结算；缺失、无效用量或无法按快照定价时按零结算并释放预占。这不表示供应商未收费。账本写入异常保留核算证据，但历史异常本身不限制后续调用，见[自动结算](request-logs.md#自动结算)。
- 支持优先级、权重、有限 FIFO、账号/模型冷却和条件会话绑定。同一模型的 OAuth 与 API 来源可进入同一路由池；仅在安全条件下切换，签名/加密历史保持账号绑定，执行状态不明时不盲目重放。
- 不提供图片/音频生成、支付、套餐、多实例部署或自动模型降级。付费内置工具及图片/音频/视频等额外计费维度尚未完整实现；字段透传不等于完整功能和费用支持。

## 已记录的真实验收

以下为仓库已有记录，不代表整理文档时重新运行了真实验收，也不覆盖未记录的个人测试。

| 来源 | 客户端身份、模型与范围 | 限制 |
| --- | --- | --- |
| OpenAI Codex OAuth | `gpt-5.6-luna`、`gpt-6.1-sol` 的目录、文本、SSE 工具往返、取消与结算；`gpt-6.1-sol` WebSocket 预热及连续响应；原生 Codex `0.164.0-alpha.11.1` 固定短提示和隔离多工具工作流 | 客户端身份含 `0.146.0`、`0.164.0`；compact 在两种身份下均返回 404。真实授权码换取、令牌轮换和长时间真实项目工作流未有完整验收记录 |
| Antigravity OAuth（历史转换器） | `gemini-3.5-flash-lite` 授权、刷新、目录、文本/SSE、工具往返与内部结算 | 当时使用 Messages 转换器；不能作为当前原生 v1internal 链路的兼容保证。原生客户端版本未记录 |
| Claude OAuth | 仓库尚无真实账号与原生 Claude Code 验收记录 | 当前记录为模拟验证 |
| 第三方 API Key 网关 | 仓库尚无真实第三方站点验收记录 | 本地模拟与 FiftyAPI 串联不代表任意 Sub2Api/New API 站点兼容 |

OpenAI 的日期、身份、模型与复现条件见 [OpenAI 验收](openai-acceptance.md)。Antigravity 历史用量、早期验收及各次变更记录见[历史归档](compatibility-history.md)。外部客户端版本与 FiftyAPI 版本分别记录。

## 自动化验证范围

默认使用模拟上游和临时数据库，不消耗真实供应商额度。真实供应商测试为显式启用，执行前需要授权。

| 范围 | 已有覆盖与条件 |
| --- | --- |
| 协议与账号 | Responses JSON/SSE/WebSocket、Messages、原生 Chat、Antigravity v1internal；OAuth 刷新轮换、路由隔离、冷却和安全切换 |
| 价格与账务 | 不可变快照、档位/缓存价格、幂等结算、取消/断流、零结算、异常恢复、预占释放及用户并发限制 |
| 访问分组 | Go HTTP 测试客户端、模拟 Codex 目录身份 `0.164.0`、Responses 和夹具模型 `model`、`private-model`；覆盖账号隔离、撤销后的会话续接、重试和目录过滤。另用 SQLite 夹具验证 Chat 与 Responses 的目录协议隔离、旧个人限制迁移、模型白名单和组策略按成员限流。未运行原生客户端或真实供应商。 |
| 请求诊断 | 模拟 OpenAI、Codex 身份 `0.164.0`、fixture 模型 `model`；协议不匹配、预算阻塞、限流、WebSocket 握手失败、SSE 首段与队列耗时 |
| Antigravity 目录测试 | 2026-11-18，身份 `2.23.0`、fixture 模型 `native-probe-model`；原生请求预览、默认推理地址、429/RetryInfo、安全脱敏与不重复调用 |
| 客户端传输自检 | Python 3.11 助手、本机网关及模拟 HTTP；Responses/Messages SSE 间隔、事件合并检测、WebSocket 握手。只验证客户端到网关，不运行原生客户端或模型推理 |
| 管理与运维 | 初始化、权限/CSRF、迁移、凭据加密、日志隔离、邮件、备份恢复；浏览器覆盖中英文和移动端布局 |
| Compose 原地升级 | v0.3.0、v0.3.1 容器到当前版本，使用 Python 3 测试客户端、模拟上游和夹具模型 `upgrade-model`。覆盖 Responses WebSocket 在途请求、重连、原 Key、额度预占释放及 HTTP JSON 结算。未运行原生客户端或真实供应商。 |

上述模拟验证不新增真实供应商兼容保证。目录同步、客户端身份候选版本及客户端到网关的传输自检，都不等于模型推理验收。

## 容量与部署

面向小团队、单实例 SQLite 部署。已有本机模拟并发与账务校验，不承诺固定生产容量；实际取决于上游并发、请求时长、磁盘及历史数据规模。现有压测未完整覆盖长流式、大量历史日志、管理页并行访问及生产磁盘，条件和结果见 [SQLite 压测](sqlite-load-test.md)。

## 语言范围

界面、帮助、无障碍标签、日期和固定诊断摘要支持中文与英文，语言选择保存在浏览器。`/api/` 错误遵循支持的 `Accept-Language`；未指定或未支持时保留中文。邀请和测试邮件包含中英文。

用户输入、模型 ID、凭据、上游原始信息和模型回复保持原文；终端日志及详细工程记录可保留中文。
