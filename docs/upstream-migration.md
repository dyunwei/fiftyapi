# sub2api 账号与网关迁移清单

状态：第一版实现已落地；探针已补齐项目发现外层重试、UA/IDE 版本、端点降级及可选加密会话恢复。gemini-3.5-flash-lite 已通过真实原生协议的文本/SSE/最小工具闭环和用量解析。账号/网关已接入正式服务；Claude Code/Codex 原生客户端仍未真实验收。

## 实现原则

按用户确认的方向，账号管理、供应商适配和影响账号使用的网关设置主要参考 sub2api 的完整实现。先梳理调用链、配置默认值和回归用例，再精简与本项目无关的业务。不能用通用 OAuth 换码成功代替完整账号支持。

已验证的 OAuth 与原生协议实现已从 `internal/probe` 移到共享的 `internal/providers`，由开发探针和账号服务复用。正式 SQLite 账号层新增 `internal/accounts`、`internal/store` 和 `internal/server`，见 [服务说明](deployment.md)。开发辅助脚本对本地 sub2api 的依赖不进入正式服务运行时；这不代表完整迁移已经结束。

## Antigravity 验证进展与先前失败定位

最新结果：用户授权码换取访问令牌成功，返回刷新令牌，有效期 3599 秒；探针成功复用加密会话，模型目录成功读取 26 项。指定 gemini-3.5-flash-lite 的文本/SSE 与工具结果回传实测通过，三次请求均返回用量，详见 [compatibility.md](compatibility.md)。已补充真实刷新和正式网关的 Messages/SSE/工具闭环，见验证记录。

先前失败发生在 `onboardUser` 响应完成但没有解析到 project ID。以下保留其源码核对与修复依据；最新成功结果并不能单独证明当时失败由哪一项改动解决。

仅凭这些结果不能判定账号不可用、项目需要手工创建，或响应一定是延迟生效。

本地参考实现的处理链：

1. `internal/service/antigravity_oauth_service.go` 的换码流程获取用户信息，然后调用 `loadProjectIDWithRetry(..., 3)`。
2. 该函数执行首次尝试及最多 3 次重试，间隔 1、2、4 秒；每次重新调用 `LoadCodeAssist`。
3. 已有项目则直接返回，同时提取订阅信息；否则只选 `allowedTiers` 中标记为默认的 tier，调用 `OnboardUser`。
4. `internal/pkg/antigravity/client.go` 对 `done=false` 最多重复请求 5 次，间隔 2 秒。`done=true` 时解析 response 中的项目，支持字符串和包含 id 的对象。
5. `done=true` 却没有项目时，内层返回错误；外层仍会按上述退避重试完整项目发现流程。探针现已补齐这层行为，并用延迟出现项目的模拟场景验证。
6. 最终没有项目时，换码结果标记 `ProjectIDMissing`，而不是把已获得的 OAuth 授权判定为失败。令牌提供器还有项目补全和补全冷却机制。

参考实现还使用 Antigravity UA/版本、IDE metadata、生产与备用端点、代理传输、订阅和隐私状态处理。探针现已对齐 UA/IDE 版本并支持覆盖，同时对齐发现/初始化/目录的端点降级条件；专用代理、订阅和隐私行为仍待迁移，不能保证完整请求行为一致。

开发探针可选加密会话模式，换码后立即保存令牌，项目发现失败后下次复用；这只用于本地验证，不替代正式 SQLite 账号状态存储。使用说明见 [probe.md](probe.md)。

下一版账号模块需区分授权成功、项目待补全、待重新授权、临时冷却、可调用等状态；前端显示具体状态。生产环境授权成功后应加密持久化令牌，让项目重试无需反复人工授权。只有项目及调用条件满足，才能将账号加入调度。

## 账号与公共网关行为

以下“保留”指保留行为、必要配置与验证，不代表原样复制全部存储或界面。

| 能力 | 迁移要求 | 本地参考位置（相对 backend/） |
| --- | --- | --- |
| OAuth 客户端参数 | ID、secret、scope、回调、编码方式成套管理；平台默认值与管理员覆盖语义明确 | internal/pkg/oauth/、internal/pkg/openai/、internal/pkg/antigravity/ |
| 授权与账号创建 | 授权、账号信息、项目/订阅、凭据持久化及重新授权分阶段处理 | internal/service/*oauth_service.go、internal/handler/admin/ |
| 令牌生命周期 | 过期提前刷新、轮换、刷新失败恢复、并发刷新合并、后台刷新和请求路径超时 | internal/service/*token_provider.go、token_refresh_service.go、oauth_refresh_api.go、refresh_policy.go |
| 网络与代理 | OAuth 与推理的有效代理选择保持一致，保留连接/TLS/请求超时；不直接照搬通用默认 Transport | internal/pkg/antigravity/client.go、internal/pkg/proxyurl/、internal/pkg/proxyutil/；其他平台需继续逐一核对 |
| 账号配置 | 模型映射、请求头覆盖、代理、优先级/并发及启停状态的实际消费者都需追踪 | frontend/src/components/account/CreateAccountModal.vue、EditAccountModal.vue、backend/internal/service/account* |
| 流式与错误 | 行长度、数据间隔超时、保活、取消、结束与错误事件、已输出后的重试限制 | internal/config/config.go 的 GatewayConfig、internal/service/*gateway*stream* |
| 限流与调度 | 供应商限流与用户金额额度分离；保留冷却和必要会话黏性，防止换号破坏上下文 | internal/service/ratelimit_service.go、*gateway*scheduling*、antigravity_gateway_retry.go |

## 各平台配置清单

| 平台 | 必须核对并保留适用行为 | 源码入口 |
| --- | --- | --- |
| Claude | OAuth beta/版本请求头、客户端版本及默认值、账号身份头与 metadata 处理、OAuth system blocks 注入、dateline 归一化、cache_control/TTL 改写选项、Token 用量与缓存计价 | internal/pkg/claude/、internal/service/settings_view.go、gateway_forward.go、claude_token_provider.go |
| OpenAI/Codex | 客户端版本/UA 与模型目录联动、账号身份与会话关联、Responses/SSE/WebSocket、compact、service_tier 策略及计价、刷新及限流窗口 | internal/service/openai_codex_identity.go、openai_codex_account_identity.go、openai_codex_models_service.go、openai_gateway*、openai_token_provider.go |
| Antigravity/Gemini 模型 | UA/版本、metadata、项目初始化和补全、订阅状态、生产/备用端点策略、模型映射、thinking 配置、工具签名/结果转换、schema 清理、签名及预算修复开关、限流冷却、usage 提取、隐私状态 | internal/pkg/antigravity/、internal/service/antigravity_oauth_service.go、antigravity_token_provider.go、antigravity_gateway*、antigravity_privacy_service.go |

Gemini 模型相关行为纳入 Antigravity 适配审查；这不自动把独立 Gemini CLI OAuth 供应商纳入第一版。独立 Gemini 的配置需要标明适用范围，不能拿它的 OAuth 参数替代 Antigravity 参数。

上表是首轮核对，不是穷尽字段列表。配置迁移时需要建立“界面字段 → 管理接口 → 持久化键/配置项 → 生效位置 → 默认值 → 回归用例”的逐项映射。不能因设置名称相似就合并。

## 配置取舍与页面

- 影响授权、请求协议、令牌、模型、用量和恢复行为的配置优先保留，后台以平台区块展示。
- 区分全局默认、平台默认、账号覆盖和客户端传入值；优先级按实际调用链确认，不能凭统一规则替换不同平台的语义。
- 将版本同步、手动覆盖、当前生效值与来源分开展示，避免“界面为空”被误解为实际不传参数。
- 请求改写、模型 fallback 或 service_tier 改变调用语义/价格时，显示实际模型和计价信息，不能静默隐藏。
- 已废弃或无效果字段不重新实现。例如本地 Settings 明确将 EnableCCHSigning 标为 no-op。
- 复杂调度权重、商业套餐或非首版供应商功能可以剔除，但先证明不依赖这些模块完成基础适配。
- SQLite/单实例替换 PostgreSQL/Redis 的存储、缓存和锁实现；保留并发正确性、刷新去重和冷却语义，不引入 Redis 运行依赖。

## 后续实施与验收

1. 固定参考源码版本或内容指纹；迁移文件记录来源、许可证、默认值与依赖，确认实际复用方式。
2. 先完成 Antigravity 一条纵向链：加密账号存储 → 授权 → 用户信息 → 项目/订阅 → 待补全重试 → 刷新 → 目录 → 流式推理 → 工具闭环。
3. 每一阶段保留独立可见状态和脱敏诊断；避免以一条笼统的“账号失败”覆盖已成功的授权。
4. 对照同版本 sub2api 的请求方法、地址、请求头名称、必要 JSON 字段、重试和状态更新验证一致性；不把原始凭据或用户对话保存为测试夹具。
5. 逐项迁移 Claude 和 OpenAI/Codex 相关配置及回归用例，模拟验证与真实客户端验证分开记录。

验收前仍不能宣称“与 sub2api 完全兼容”。目前不再要求用户为每个探针遗漏反复授权；应先完成上述链路迁移，再集中进行下一轮真实测试。


## 第一版落地与取舍（2026-10-30）

| 配置/行为 | 保存与生效位置 | 默认与验证 |
| --- | --- | --- |
| 配套 OAuth 参数、PKCE/state、回调、重新授权 | providers/defaults、providers/oauth、accounts/oauth | 按平台默认；模拟 owner/单次消费验证；AG 真实通过 |
| 加密、提前刷新、后台维护、刷新去重、项目补全 | accounts/service、accounts/access、accounts/maintenance | AES-GCM 绑定账号 ID；先保存轮换令牌；AG 真实刷新 |
| gateway_url、proxy_url、headers、UA/version、timeout | accounts/config → 加密 Credential → providers.HTTPClient/gateway | 并发仅由调度策略设置（默认 5）；连接配置界面为分平台表单；代理认证隐藏，超时上限 600s |
| Claude metadata/fingerprint/system | accounts/config → gateway/claude | 默认只留规范化 user_id、每上游账号稳定指纹、保留 session、补 Claude Code system；可显式关闭；回归验证 |
| Codex 账号头、模型元信息、加密 reasoning、stateless、compact | providers/identity、gateway/native/models | Responses HTTP/SSE；支持 manifest；拒绝 previous_response_id 和非标准 tier；模拟验证 |
| AG UA/IDE、端点降级、项目初始化、身份提示 | providers/antigravity/models、gateway/antigravity | 默认生命周期对齐参考；onboard 需授权时勾选；生产/备用端点受限；真实通过 |
| AG thinking、schema、tool/signature/usage | internal/compat/antigravity → gateway/antigravity | 复用原转换器并抑制内容 debug 日志；实际工具闭环通过 |
| 会话、并发、冷却 | gateway/scheduler、store/gateway_sessions | 持久化会话 hash、并发上限、429 冷却；不静默换模型/重放推理 |

未迁移全部高级业务开关：计费套餐、fast/priority tier、dateline/cache TTL 自动改写、供应商隐私/订阅修改以及所有异常修复器。优先级、权重和原生 Responses WebSocket 已在后续阶段实现；默认保留客户端 cache_control，仍不承诺完整 sub2api 行为。需要其余能力时先补价格与回归，再对外启用。

## 对齐补充

账号策略/优先级权重、健康负载、有限 FIFO、模型级冷却、OAuth 刷新合并与轮换持久化、可迁移会话条件绑定、安全拒绝重试、批量授权文件导入、账号删除保留历史、原生 Responses WebSocket 已实现。保留单实例 SQLite 和内部美元账本，新增初始零额度的邮箱自助注册；未复制商城/支付/套餐及全部自动修复。WebSocket 同连接内顺序执行且缓存不跨连接，共享上游连接池/并行 stream/steer 尚未实现。详见 [实现与边界](reliability-redesign.md)。

## 明确调整

按用户确认取消标准协议之间的自动转换，原生 Chat API 上游与 Messages/Responses 分别匹配；OAuth 按固定适配器选择，Antigravity 保留专用 Messages 适配。模型新增多协议能力而非只按名称推断；配置和旧 ID/价格保留方式见 [模型协议](model-protocols.md)。
