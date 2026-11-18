# 低维护账号共享

[English](en/low-maintenance.md)

FiftyAPI 以原生协议、自动目录和参考价格驱动内部预算。预算单位沿用参考 USD，不涉及收款，也不代表 OAuth 订阅的实际扣款或剩余次数。

## 原生接入

- API Key 上游继续使用声明的 Chat Completions、Messages、Responses。
- Claude OAuth 默认保留客户端 metadata 和 system；身份规范化、系统提示补充仅在显式配置后执行。
- Codex OAuth 使用 Responses。保留未知请求字段和采样、输出限制等客户端字段；必要的 OAuth store/stream、设备身份和传输处理仍存在，由上游验证参数。JSON/SSE/WebSocket 保持原协议，不跨协议转换。
- Antigravity 删除 Messages 转换器，使用 `POST /v1internal:streamGenerateContent` 或 `POST /v1internal:generateContent`，请求为上游原生 envelope，至少包含 `model` 和 `request`。项目与凭据由所选账号提供，模型按路由映射；其他请求字段及原生响应保留。客户端必须支持该协议；旧 Anthropic Messages 客户端不能直接使用此来源。自定义推理地址需对应所用的原生 action。

Antigravity 示例（本地 Key 通过 Authorization Bearer 提供）：

```json
{"model":"模型ID","request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64}}}
```

## 自动价格与模型

账号维护继续每日同步健康账号的模型目录。生产服务默认每天下载 LiteLLM 的 `model_prices_and_context_window.json`，仅解析数据，不执行其代码或依赖其服务。失败保留本地版本，一小时后再尝试。系统设置 → 自动维护可以查看状态并立即同步。

按供应商与模型精确匹配；不模糊猜测模型名称。原生 Codex 使用 `openai/上游ID`，Claude 使用 `anthropic/上游ID`；其他来源使用精确目录键。网关别名或未匹配模型可以在“编辑价格 → 跟随价格目录”指定来源键。新发现模型可立即匹配已下载目录，不需要等待下一次联网同步。目录缺失的模型不开放调用。

支持文本输入、输出、缓存读取、缓存写入、Anthropic 一小时缓存写入，以及目录明确提供的 Priority/Fast、Ultrafast、Flex 和 128k/200k/272k 长上下文费率。`priority` 与 `fast` 归为同一档位。只有目录明确收录的价格才可用；并非所有模型都提供全部档位或缓存费率。

原始 token 不乘倍率。预占使用请求档位的保守费率；结算优先使用上游实际返回的档位和用量，未返回档位时使用请求档位，并在日志标记“按请求档位”。实际长上下文用量与缓存时长在结算时选择价格。每次请求保留完整不可变价格快照及来源修订，更新不改变历史账单。手工价格仅提供标准档位并优先于自动同步；重新选择目录键可恢复自动价格。

缺失档位价格在请求前拒绝；执行后才发现用量或价格不足，则释放预占并保留待核算记录，该用户后续调用暂停，管理员核算后恢复。已确认未执行的拒绝按零结算；已取得可靠的中断用量仍按记录结算。

价格以定点整数解析，不能精确表示的费率不会四舍五入成免费价格。预算预占是估算而非上游生成硬限额，最终用量可能超过预估。付费内置工具、图片/音频/视频、区域加价等额外计费维度尚未完整实现，不能把文本参考费用当作这些请求的完整供应商账单。

## 用户管控

在成员的“访问限制”设置模型允许列表和并发。允许全部模型会自动包含后续已定价的新模型；空允许列表禁止全部模型。并发 0 表示仅受上游账号并发约束；正数在账本预占事务内原子检查，HTTP/SSE/WebSocket 共用限制。继续按用户分配 5 小时和 7 天预算，所有 Key 共享。没有独立的加速档位权限表，贵的模型或速度消耗更多预算。

## 客户端版本

系统设置 → 自动维护集中管理 Codex、Claude Code 和 Antigravity 默认版本。账号版本留空继承默认，填写则固定。刷新并保存凭据不会把继承版本变成固定版本。Codex 模型目录与推理使用所选账号的相同身份；不会因下游客户端的版本 query 改变目录身份。

每日从 OpenAI Codex GitHub Releases 检查稳定版本并展示候选值；不会仅根据版本号自动启用或发起付费兼容探测。管理员可以更新默认值或改回前一版本，无需重新部署。客户端身份和上游协议变化仍可能需要维护，候选版本不代表已通过真实兼容验证。

`FIFTYAPI_AUTO_MAINTENANCE=false` 可关闭价格和版本的后台联网检查，保留本地数据及手动同步。默认关闭每日推理探测；只有 `FIFTYAPI_MORNING_PROBES=true` 才运行会消耗上游额度的晨间探测。账号目录同步和必要的 OAuth 刷新仍按账号维护策略运行。

## 数据与验证

使用追加迁移 019–021，保留账号密文、主密钥绑定及历史价格快照，不要求删除数据库。移除了静态内置价格表和 Antigravity 转换代码；旧记录仍可查询，旧零结算记录不会被追溯改账。

本次以临时数据库和模拟上游验证；默认 Codex 身份 0.164.0，模型为 `model` 等 fixture ID，覆盖 Responses JSON/SSE、Messages、原生 Antigravity、目录同步、档位价格、缓存、价格快照、异常恢复和并发权限。本次没有运行真实供应商验收，没有为重构后的 Antigravity 或新的身份默认值建立真实客户端兼容保证。历史转换器验收不适用于当前原生链路。

2026-11-17 另行下载公开 LiteLLM 价格文件进行解析检查：4,171,564 字节，得到 4,190 个目录键，其中含有效 Fast 费率的键 577 个、Flex 433 个、Ultrafast 20 个。目录键数量包含别名，不代表独立模型数，也不代表这些模型已完成推理验收。文件 SHA-256：`63050115948432fd1abf146ad4f0670acb612163f98e084ceed1522ec210a2d5`。`make test`、`make build`、`make test-ui` 通过，浏览器测试 27 项。

## Antigravity 本地测试诊断

模型目录页面的“测试”直接构造原生 `v1internal` envelope，包含 `requestId`、`userAgent`、`requestType` 和 `request.contents`；不生成 Messages 请求。结果返回后，请求预览以服务端构造的请求为准，不展示账号认证和项目。默认推理使用 `daily-cloudcode-pa.googleapis.com`，模型目录继续使用其发现地址；显式配置的推理地址保持原值，自定义站点不会自动转到官方地址。

测试页面中的 HTTP 429 表示所选上游返回请求限制，不等同于本地用户预算不足，也不能单凭该状态断定模型无权限。若上游提供明确的 Google 状态、允许展示的 ErrorInfo 原因代码或 Retry-After/RetryInfo，页面会显示这些诊断与建议重试时间；不展示原始错误消息、项目和配额元数据，不自动重试或切换模型。只有管理员再次点击测试才会再发送请求。

2026-11-18 修复上述预览、文案和默认推理地址回归。模拟上游以 Antigravity 2.23.0 身份和 fixture 模型 `native-probe-model` 验证原生 envelope、429/RetryInfo、敏感信息保护与不重复调用；未调用真实供应商，不能据此确认实际账号的 429 已消失。

验证命令 `make test`、`make build`、`make test-ui` 均通过，包含 28 项浏览器测试。
