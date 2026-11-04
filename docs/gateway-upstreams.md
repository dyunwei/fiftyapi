# 接入上游 API Key 网关

支持 Sub2Api、FiftyAPI、New API 及其他兼容网关。管理员使用上游分配的调用 Key 接入；本项目继续管理自己的用户、API Key、5 小时/7 天额度和请求日志。

## 页面操作

1. 在“上游账号”点击“+接入上游API”。OAuth 的“添加账号”入口仍保留。
2. 填写网关名称、平台、Base URL、上游 API Key。
3. 勾选上游支持的 Chat Completions、Messages、Responses 协议；需要长对话 compact 时，确认上游支持后开启对应选项。
4. 选择自动发现或手工配置模型；需要时设置本地别名。
5. 保存后查看目录状态，再到“模型与价格”为需要使用的模型确认本地价格。未定价的模型不开放调用。
6. 用户使用本项目地址和自己的 Key，模型名称使用本项目目录返回的 ID。

平台名称是预设/标识，不是对某个站点兼容性的保证。实际能力受上游版本、渠道、分组和 Key 权限影响。

## 地址和认证

Base URL 可填写 `https://gateway.example` 或 `https://gateway.example/v1`，也支持 `https://gateway.example/prefix/v1`。站点根地址自动补 `/v1`；不要填写具体 `/messages` 或 `/responses` 接口，不要把 Key 放到地址中。

默认要求 HTTPS。可信内网如 Docker 服务名之间使用 HTTP，需要明确勾选“允许 HTTP”；该模式会通过明文连接传输 Key 和请求，应只用于可信网络。HTTPS 不会自动降级 HTTP；上游重定向不会被跟随。

默认认证为 `Authorization: Bearer`，也可选择 `x-api-key`。这一选择用于目录和推理请求；若上游目录不接受所选方式，可切换认证方式或使用手工模型。

Key 加密保存在账号凭据中，读取账号配置不会返回原值。编辑时留空保留旧 Key，填入新值进行轮换，保存后重新验证并同步。代理、超时、并发和额外请求头沿用账号配置；额外请求头不能覆盖认证、连接和循环保护字段。

## 模型发现、命名和价格

自动发现请求 `/v1/models`，支持标准 `data` 数组和 Codex `models` 数组。`has_more/last_id` 分页通过同源 `after_id` 获取，最多 10 页/1000 个上游模型，不跟随上游提供的任意下一页 URL。

目录只提供模型 ID，不证明协议权限。管理员声明上游实际支持的默认协议，并可在每行模型映射中覆盖。一个模型可以同时选择 Chat Completions、Messages、Responses，共用本地 ID 和价格。协议覆盖替换默认能力。

本地模型 ID 按来源隔离；自定义别名全局唯一。修改映射保留已有价格和历史快照；修改别名后需要更新客户端。兼容保留的旧 ID 可能包含协议名称，以目录的 `protocols` 字段为准。详细配置见 [多协议模型](model-protocols.md)。

手工模式不请求上游目录，只验证配置并保存模型，**不能据此证明上游连通性或 Key 权限**。推理不会在保存配置时自动执行。

网关模型不会套用厂商内置价格。管理员需填写本地内部价格；结算依据上游实际 usage 与本地价格快照，不读取上游管理账单或余额。多层网关会分别计算各自实例中对应用户的额度；上游可能另有实际收费。

## 协议与边界

| 能力 | 支持范围 |
| --- | --- |
| Chat Completions | `/v1/chat/completions` 原生 JSON/SSE、工具与 usage |
| Messages | `/v1/messages` JSON、SSE、工具/结果与签名字段保留 |
| Responses | `/v1/responses` JSON、SSE、工具调用与结果保留 |
| compact | `/v1/responses/compact`，需显式开启且上游支持 |
| 模型目录 | `/v1/models`，只对外列出已定价、已启用且有就绪账号的模型 |
| Codex manifest | 上游实际提供时保留元数据并映射 slug；通用目录不能伪造 context window 等信息 |
| 用量异常 | 流中断有可靠 usage 按原价快照自动结算；缺失/无效 usage 按 0 释放预占并说明原因；仅结算异常待核算 |

转发只替换映射的请求模型；Messages 缺少输出上限时补默认值。保留客户端的 system、工具/历史、采样参数和 store/stream 选择；不执行 Claude/Codex OAuth 身份注入、Token 刷新或厂商专用参数删除。

使用标准服务等级，不支持 priority/flex 计价、图片/音频生成或任意跨协议转换。HTTP Responses 要求完整历史；原生 Responses WebSocket 支持同连接上下文，需上游确实支持并由管理员显式开启。上游应按请求返回 JSON 或 SSE；Messages 非流式收到强制 SSE 时不聚合为 JSON。第三方站点及客户端配置需使用实际账号验证。

## 循环和失败处理

直接指向当前网关会被阻止。FiftyAPI 之间传递 `X-FiftyAPI-Relay-Chain`，重复实例或超过 4 个节点时返回 508；本地两节点环路测试未产生模型调用或额度扣减。模型发现也传递此标记。

应保持拓扑无环：第三方网关可能删除自定义标记，不能依靠它识别所有跨第三方的间接环路。不要把链路接回自己；恢复的数据库副本保留实例标识，不应与原数据库同时连接同一批上游账号。

只在明确拒绝且尚未输出实际内容等安全条件下切换账号；不重放执行结果不明的推理，也不静默切换模型。429 使用现有账号冷却；失败后可通过本地请求 ID 查日志。上游 Key、请求正文与原始错误体不会写入请求日志。

## 验证记录

- 三个平台预设 × 两种协议的本机 TLS 模拟测试：JSON/SSE、工具、参数保留、认证、usage 与日志。
- Key 加密/隐藏/轮换、手工模式、分页、别名冲突和定价保留。
- 旧表迁移后保留账号 ID、凭据字节、模型关系、请求日志和会话绑定，恢复外键检查。
- 两个实际 FiftyAPI 网关处理器串联：自动发现、两层别名、工具/签名回传、分别结算通过。
- 故意 A→B→A 环路被阻止，未调用模型、未扣款；重定向不携带 Key 跟随。
- 浏览器：创建网关、配置本地价格、调用本机上游、核对额度、Key 轮换及手机表单。

没有使用真实 Sub2Api 或 New API 站点的账号验收，也没有新增真实 Claude/Codex 调用。第三方能力参考其标准协议与 [New API 官方接口说明](https://github.com/QuantumNous/new-api/blob/main/README.md)，本地模拟结果不等于任意已部署版本全部可用。

升级应先备份并停止旧服务。服务进程在迁移前取得单实例锁；重复启动会在打开数据库前退出。迁移及备份工具的离线操作边界见部署说明。

界面预览：[桌面配置](screenshots/gateway-config.png) · [手机配置](screenshots/gateway-mobile.png)。

## 调度与 WebSocket

API Key 上游与 OAuth 账号共用优先级、权重、并发、队列、健康/模型冷却与安全切换。在“调度与健康”编辑；日志“路由详情”展示尝试。新接入 API Key 网关的 Responses WebSocket 默认关闭，只有上游支持且管理员显式开启才允许原生升级；启用 Responses 本身不代表支持 WebSocket。不同接入账号的模型 ID 保留各自命名空间，不自动把不同模型或不同供应商当作同一权限池。完整范围见 [可靠性说明](reliability-redesign.md)。

## 账号额度保护

在账号配置的「额度保护」中按需设置，默认全部关闭。策略保存到账号的 `inference_policy`，对选中该账号的每次转发生效，包括 Responses HTTP、compact 和 WebSocket；切换账号重试时使用新账号的策略。

```json
{"inference_policy":{"max_effort":"medium","max_thinking_tokens":4096,"force_standard":true}}
```

- `max_effort`：空值不限制，可选 `low`、`medium`、`high`。保留较低请求值，较高值降到上限；缺省值使用上限，避免模型默认高档位。Codex Responses 使用 `reasoning.effort`，Chat Completions 使用 `reasoning_effort`。Claude 使用 `output_config.effort`，同时限制消息级 effort；显式 `enabled` / `disabled` 且未使用 effort 的旧模型不注入该字段，避免兼容性错误。旧模型请用思考 token 预算。启用策略后，无法识别的 effort 返回 400，不静默放行。
- `max_thinking_tokens`：0 不限制，其余为 1024–1048576。Claude 原生 Messages 只限制 `thinking.type=enabled` 的 `budget_tokens`，保留更低预算和关闭状态；自适应思考需要设置 `max_effort`，不会被转换成旧式显式思考。Codex 不支持独立的思考 token 硬上限，这个字段不作用于 Codex。
- Antigravity 当前提供 Messages 转 Gemini 的兼容链路，按转换后的实际思考预算控制，包括动态预算和 Opus 自适应默认预算。`low` / `medium` / `high` 对应网关预算上限 2048 / 8192 / 24576，与 `max_thinking_tokens` 取较小值；这是本网关的预算映射，不是厂商定义的等价档位。不会切换模型。未知签名导致转换器关闭思考时仍保持该行为。
- `force_standard`：Codex 覆盖请求体 `service_tier=default`；Claude 移除 `speed` 并使用 `service_tier=standard_only`；移除自定义 `OpenAI-Service-Tier` 请求头，避免重新启用快速档位。Antigravity 当前转换器不传递 Fast 选项，没有额外可控制的 Fast 档位。

API Key 上游网关账号按请求协议使用相同规则。规则优先于客户端参数和快速档位请求头；不支持这些字段的第三方上游可能拒绝请求。Claude 旧模型如不支持 effort，应清空强度上限并使用显式思考预算。强度上限用于控制推理倾向，不保证固定 token 消耗；实际用量仍按上游返回结算。原生 Codex 未启用标准速度策略时，非标准档位仍返回 400，保持当前标准档位定价约束。

参数依据：[OpenAI reasoning](https://developers.openai.com/api/docs/guides/reasoning)、[Claude effort](https://platform.claude.com/docs/en/build-with-claude/effort)、[Claude Fast mode](https://platform.claude.com/docs/en/build-with-claude/fast-mode)。
