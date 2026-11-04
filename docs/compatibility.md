# 兼容性与验证记录

更新：2026-11-02。实现参考本地 sub2api；模拟测试、真实上游调用和原生客户端验收分开记录。

| 能力 | Claude OAuth | OpenAI Codex OAuth | Antigravity OAuth |
| --- | --- | --- | --- |
| 授权、state/PKCE、换码、重新授权 | 已实现，模拟验证 | 已实现，模拟验证 | 用户真实授权通过 |
| 刷新、轮换先持久化、并发合并 | 模拟验证 | 模拟验证 | 模拟及真实刷新通过 |
| 模型目录与账号状态 | 模拟验证 | 真实目录与 Codex manifest 通过 | 真实读取 26 项 |
| HTTP/SSE 推理与用量 | 模拟验证 | gpt-5.6-luna / gpt-6.1-sol 真实通过 | 指定模型真实通过 |
| 工具/结果闭环 | 透传；模拟验证 | 真实流式调用及结果回传通过 | Messages 转换真实通过 |
| compact | 不适用 | 模拟通过；旧 0.146.0 身份返回 404，新身份待验收 | 不提供 Responses |
| 原生 Claude Code / Codex 程序 | 未实测 | 0.164.0-alpha.11.1 固定短提示 WebSocket 通过 | 未实测 |
| 原生 Responses WebSocket | 不适用 | 连接内预热/连续响应、断开结算已验证 | 不提供 Responses |

## 真实 Antigravity 测试

仅使用用户指定的 `gemini-3.5-flash-lite`，固定文本与无副作用的 `probe_add(2,3)` 工具；未主动耗尽限额，未自动换模型，未打印令牌、工具签名或原始凭据。

1. 原生 Go 探针：文本/SSE 返回 `FIFTYAPI_OK`，工具发起及工具结果继续回复 `5`。三次调用分别 270、354、367 Token，合计 **991 Token**。
2. 保存会话真实刷新成功，轮换令牌写回原加密会话；目录读取 **26 项**。
3. 正式网关：临时 SQLite + 加密导入账号 + 用户 API Key → `/v1/messages` SSE 返回 `FIFTYAPI_GATEWAY_OK`，工具调用及结果回传成功。三次合计 **1266 个上游报告 Token**。
4. 网关测试使用各项 1 microUSD/Token 的**测试内部价格**，结算 1266 microUSD；两窗口已用金额与请求日志合计相同，结束后预占为零。该价格不是供应商报价。

临时数据库、测试成员/Key 和主密钥已清理；原探针会话仅保存在被忽略的本机目录中。这证明指定模型的最小链路，不代表 26 个模型、所有工具模式或原生 CLI 均可用。

## 自动化覆盖

- Go race：初始化并发、迁移、密钥校验、会话/CSRF/权限、停用撤销、OAuth owner/单次消费、刷新轮换、账号配置、模型/价格、额度预占/结算/跨窗口/重置、未知用量、重启恢复、日志隔离/清理/游标、会话绑定、单实例锁。
- 模拟上游：Claude 与 OpenAI 原生请求、SSE 完成/截断、缓存 usage；Antigravity 文本/工具/签名转换、非流式聚合；Codex manifest/compact；定价缺失和撤销密钥拒绝。
- Chromium：管理员初始化、登录、一次性显示 Key、创建成员、权限隔离、生成 OAuth 链接、退出切换身份、390px 手机菜单及无横向溢出。只生成授权链接，不自动登录真实供应商。
- 构建：Vue 生产构建、Go vet/单文件、Docker 本地镜像。容器运行检查见 [部署说明](deployment.md)。

## 第一版边界

- OpenAI 为 Responses HTTP/SSE、原生 WebSocket 和 compact 接口（新身份 compact 未实测）；OpenAI/Responses 模型暂不适配 Chat Completions。
- Claude/Antigravity 上游链路为 Messages；已删除客户端 Chat Completions → Messages 适配，Chat 仅直通支持 Chat 的 API 上游；不提供独立 Gemini CLI OAuth，也不把 Messages 与 Responses 任意互转。
- 只支持标准/auto 服务等级；不支持 priority/flex 计价、内置付费工具附加费用或图片/音频生成。
- 定价为内部额度快照；无价格就拒绝，有可靠 usage 自动结算，缺失/无效 usage 按 0 释放预占并在日志说明；仅结算异常保留人工核算。
- 提供优先级/权重、有限 FIFO、健康与模型级冷却、安全切换和上下文绑定；不包含套餐、隐私订阅操作、全部自动修复和模型降级。具体边界见 [账号与网关](reliability-redesign.md)。
- 已做 50 个独立用户的本机模拟并发测试，不能承诺固定生产并发容量；实际瓶颈取决于上游账号及请求时长，SQLite 用户总数本身不是主要限制。

正式广泛使用前仍需补齐 Claude 真实账号验证，以及 OpenAI 的真实 OAuth/刷新、compact 可用性和长时间原生客户端工作流验收。OpenAI 核心真实调用已验证，见下文；不同协议及模型不能相互代替验收。


2026-10-31 补充：Antigravity 连续两次工具调用、结果回传与流式追问通过，共 4 次、1967 Token；离线恢复及 50 用户模拟负载通过。


新增 API Key 网关。三平台预设的双协议模拟、两个 FiftyAPI 网关串联、循环拦截和网页操作通过本地验证；真实第三方站点尚未验收，详情见 [网关上游](gateway-upstreams.md)。

## 客户端与管理

Chat Completions 只直通声明支持 Chat 的 API 上游。Claude 和 Antigravity 使用 Messages，OpenAI Codex OAuth 使用 Responses，不在标准协议之间自动转换。API Key 可独立撤销或删除；删除保留历史日志与结算关联。管理员模型列表支持搜索和分页。

## 自动结算

普通断流、取消和用量缺失不再要求人工核算；按已收到的可靠用量结算，未知或无效用量按 0 释放预占。启动时处理旧待核算及在途记录，使用原价格快照并保持幂等。先持久化用量证据，再事务提交账本和日志金额；写入失败保留待核算，不清理其财务证据。说明与验证见 [自动结算](automatic-settlement.md)。

## OpenAI 真实验收

已导入 OpenAI 账号使用 `gpt-5.6-luna` 完成真实目录、Codex manifest、JSON 文本、SSE 工具调用及历史/结果回传、客户端取消与额度结算验证。修复上游响应为 SSE 但缺少正确 Content-Type 时的 502；明确协商 Accept，对缺失或通用响应类型做有限前缀识别，仍要求有效事件及完成标记。

本次 compact 返回 HTTP 404，不能宣称该账号支持；授权码换取、真实令牌轮换、原生 Codex CLI 和 WebSocket 均不在此次通过范围。完整条件、复现命令及用量边界见 [OpenAI 验收](openai-acceptance.md)。

## 目录与账号可见性

同一份 OpenAI 授权已用新版身份真实验证 `gpt-6.1-sol` 的文本、SSE 工具往返、取消及计费。缺失模型由旧客户端版本声明导致，现已统一默认身份并兼容旧内置配置。上游账号新增独立的目录查看、计数、同步时间、搜索分页，保留未定价与停用项；不把目录同步标成推理验证。详见 [账号模型目录](account-models.md)。

## 多账号与 WebSocket

新增故障矩阵、刷新并发取消、删除防复活、管理员调度/批量导入、逐次路由隔离、WebSocket 原生缓存/断开/停机结算验证；真实 gpt-6.1-sol HTTP、工具往返、预热及连接内连续响应通过，本机原生 Codex 固定短提示通过。单连接内顺序处理，API Key 网关需显式开启该传输。完整数据与限制见 [OpenAI 验收](openai-acceptance.md) 和 [账号与网关](reliability-redesign.md)。

## 原生 Chat 与多协议模型

不再自动转换标准协议。API 上游支持多协议声明、逐模型覆盖、共享 ID/价格与协议隔离；Claude/Antigravity 使用 Messages，Codex OAuth 使用 Responses。新增原生 Chat JSON/SSE/usage、工具字段保留、截断核算、协议拒绝、旧模型价格保留及多协议端到端回归。当前没有新增真实第三方 API 账号验收，模拟通过不保证任意 New API 站点的能力。见 [配置和升级](model-protocols.md)。

## 中英文支持

界面、帮助文案、无障碍标签、日期和固定诊断摘要支持简体中文与 English。语言偏好保存在浏览器；未支持的浏览器语言默认使用英文。`/api/` 错误根据 `Accept-Language` 返回对应语言，未指定或未支持时保留中文。邀请和测试邮件同时包含中英文。用户输入、模型 ID、上游原始信息和模型回复保持原文；终端日志及详细工程记录可保留中文。英文使用说明见 [English docs](en/README.md)。
