# 第一版实现方案

状态：第一版设计记录；主要能力已实现。当前部署、验证范围和文件组织以 deployment.md、compatibility.md 与实际源码为准。

协议静态核对结果与待执行的真实测试见 [compatibility.md](compatibility.md)。

最新实现约束：账号管理与网关适配以 sub2api 的完整生命周期及适用配置为主要依据；不能直接把通用探针提升为生产适配器。迁移范围、已发现差异和验证顺序见 [upstream-migration.md](upstream-migration.md)，与早期简化适配描述冲突时以该约束为准。

## 架构与目录

采用一个 Go 服务、一套 Vue 3 管理页面、一个 SQLite 数据库。前端构建产物通过 Go embed 嵌入；API 与页面同源。无 Redis，无独立任务服务。

原始目录规划（实现中将额度、模型和日志持久化收敛到 internal/store，认证路由收敛到 internal/server）：

```text
cmd/fiftyapi/          程序入口
internal/config/       环境变量与启动校验
internal/store/        SQLite、SQL 迁移和持久化
internal/auth/         初始化、登录、会话、API Key、权限
internal/accounts/     上游账号生命周期与 OAuth
internal/providers/    Claude、OpenAI、Antigravity 适配器
internal/gateway/      协议入口、账号选择、转发
internal/quota/        窗口、预占、结算
internal/models/       模型目录与价格
internal/logs/         请求日志与管理员操作记录
internal/server/       HTTP 路由、中间件、静态资源
web/                   Vue 3 源码
docs/                  需求、设计、部署与兼容性文档
```

用明确的模块边界隔离供应商差异，数据库层直接使用 SQL 和版本化迁移，避免引入完整的插件框架和复杂依赖注入系统。具体库在脚手架阶段选择。

## 页面与权限

普通用户页面：额度概览、API Key、自己的请求日志、密码设置及客户端配置说明。

管理员额外页面：用户与额度、上游账号与 OAuth、模型与价格、全部请求日志、操作记录、系统设置。管理员通过管理接口操作上游凭据，不在页面返回明文令牌。

系统只有 admin、user 两种角色。用户日志和 Key 的所有权由服务端按当前身份约束，不能依赖前端过滤。

## 数据模型

下列为逻辑表设计，最终字段随实现细化。

| 表 | 主要内容与约束 |
| --- | --- |
| system_settings | 初始化状态、日志期限等非敏感设置 |
| users | 用户名唯一、密码哈希、角色、状态、两个额度上限 |
| sessions | 登录会话摘要、用户、到期时间；改密或停用时撤销 |
| api_keys | Key 摘要、展示前缀、所属用户、名称、状态；原文仅创建时返回 |
| upstream_accounts | 平台、名称、加密凭据、到期时间、启停状态、冷却时间、脱敏错误 |
| oauth_sessions | 平台、发起管理员、state 摘要、加密 PKCE verifier、过期和消费状态 |
| models | 平台和上游模型 ID、对外模型 ID、协议能力、同步状态、启停状态 |
| account_models | 上游账号与模型的可用关系、发现时间 |
| model_prices | 模型和价格版本、输入/输出/缓存等单价、生效时间 |
| quota_windows | 用户、窗口类型、代次、开始与到期时间、已用金额、预占金额 |
| request_charges | 请求 ID 唯一、用户、两个窗口代次、预占额、结算额、价格快照、结算状态 |
| request_logs | 请求 ID、用户、Key 标识、上游账号、模型、用量、耗时、状态、脱敏错误 |
| admin_audit_logs | 管理员、操作、目标、脱敏变更内容、时间 |

金额使用定点整数和明确的缩放单位，计算时规定舍入方式，不用浮点数累计金额。价格版本不可覆盖历史；日志清理不删除结算幂等依据。结算记录的归档策略另行设计。

窗口保留代次标识。到期或手动重置创建新代次，旧请求完成时仍结算到原代次，不扣新周期额度。

## 请求与额度流程

1. 校验 API Key、用户状态、请求大小及模型能力。
2. 解析请求，确认存在适用价格，固定本次价格版本。
3. 开启短写事务，读取用户状态与两个窗口；必要时创建待激活窗口代次，检查已用额与预占额，登记本次预占及唯一请求 ID，提交。
4. 选择支持该模型且可用的上游账号，在事务外执行网络调用和流式转发。
5. 用最终 usage 计算消耗，在短事务内幂等结算、释放预占并写入日志。两个窗口记入相同消耗。
6. 若没有计费用量，则释放预占，不消耗额度；失败也可能包含计费用量，不能仅凭 HTTP 状态判断是否收费。

窗口在首笔计费用量时激活。并发请求先共享待激活代次，结算时原子地确定开始时间。预占不能因无计费请求永久启动窗口。必须用并发与跨窗口测试覆盖此状态转换。

预占额按输入估算、输出上限与模型价格计算；有效输出上限必须明确，不采用无界预占。第一版允许剩余额度不足以覆盖最大估算成本时，以剩余额度限制预占并允许一个请求进入，其实际消耗可能超额。其他并发请求不能重复使用这部分预占额度。页面不能承诺绝对零超额。

客户端断连时尝试取消上游请求，并按已经收到的可靠 usage 结算。采用以下策略：缺失或无效用量按 0 结算、释放预占并记录原因，不把预估额当作实际消耗，也不把本地按 0 处理解释为上游免费。先持久化用量证据，再将额度和日志金额一起提交；仅写入/价格快照等结算异常保留人工核算。启动时按同一策略幂等处理旧待核算和在途记录。

管理员修改上限保留已用额与预占额；降低到当前占用以下时停止新请求。重置操作可指定 5 小时、7 天或两者，创建新代次并留审计记录，不抹除历史。

SQLite 开启 WAL、外键和合理的锁等待时间。所有网络调用都在事务外；额度与结算优先于日志清理等后台写入。单实例中限制同时进入的写事务，锁等待失败必须返回明确错误。

## 上游 OAuth 与模型

三个平台分别实现授权、换取令牌、刷新、验证与模型发现；共用账号状态和加密存储。刷新按账号合并并发操作，刷新失败后保留可恢复状态，不能反复使用失效令牌。

OAuth 会话短时有效、绑定发起管理员、验证 state，并在换码时一次性消费；平台支持时使用 PKCE。回调或手动粘贴授权结果的方式依平台验证，不能假定自建域名都能作为回调地址。

加密主密钥由环境变量或外部文件提供，不放入数据库或源码；备份需要同时保管数据库和主密钥。主密钥缺失时不能自动生成新密钥覆盖旧数据。

模型自动同步后仍经过协议能力、价格和管理员启用状态筛选。对外模型 ID 不允许含混地路由到不同平台；发生重名时用明确别名。同步失败保留上次成功目录并显示时间和错误。

账号选择已升级为优先级、加权健康负载、有限 FIFO、模型冷却和有条件的会话绑定；原生 WebSocket 复用同一套账本与调度，详见 [账号与网关](reliability-redesign.md)。仅在确认安全且尚未向客户端输出时尝试换账号；无法判断上游是否已经执行的请求不盲目重试。内部多次尝试均需记录，不能重复向用户结算同一成功结果。

## 协议边界与本地参考位置

以下来自本地 sub2api 源码检查，证明存在可参考的实现，不代表已验证线上兼容性。

| 能力 | 首版方案 | sub2api 参考位置（相对 backend/） |
| --- | --- | --- |
| Claude Code | Messages、流式事件、工具调用；核实 count_tokens | internal/server/routes/gateway.go、internal/service/oauth_service.go |
| Codex | Responses、模型目录；核实 compact、WebSocket 及客户端配置需求 | internal/server/routes/gateway.go、internal/service/openai_oauth_service.go |
| OpenAI OAuth 模型 | 授权后的 Codex 模型目录 | internal/service/openai_models_list.go |
| Antigravity | 先验证 OAuth、发现模型与 Messages 转换；不默认支持全部模型跨协议转换 | internal/service/antigravity_oauth_service.go、internal/pkg/antigravity/ |
| 管理员初始化 | 参考初始化流程，独立实现一次性初始化与部署口令 | internal/setup/ |

优先原生协议：Claude OAuth 对接 Claude Code，OpenAI OAuth 对接 Codex。Antigravity 到 Claude Code 的支持以验证结果确定；不在第一版承诺 Responses 与所有供应商之间任意转换。

客户端版本和配置方式写入兼容性说明。必须使用真实客户端验证流式结束、工具结果回传、取消与错误处理，不能只用简单 HTTP 成功响应作为兼容依据。

## 接口草案

管理与用户 API 统一放在 `/api` 下；网关 API 使用独立 API Key 认证。

| 范围 | 接口组 |
| --- | --- |
| 初始化 | GET /api/setup/status、POST /api/setup |
| 会话 | POST /api/auth/login、POST /api/auth/logout、GET /api/me、PUT /api/me/password |
| 用户 | /api/admin/users，支持创建、停用、重置密码及配置额度 |
| Key | /api/keys，支持创建、列出、撤销；仅操作当前用户的 Key |
| 额度 | GET /api/quota；POST /api/admin/users/:id/quota/reset |
| 上游账号 | /api/admin/accounts，支持启停、验证、刷新、重新授权与模型同步 |
| OAuth | POST /api/admin/oauth/:provider/start、POST /api/admin/oauth/:provider/complete；具体回调依平台确定 |
| 模型 | GET /api/models；/api/admin/models，管理启停、别名及价格版本 |
| 日志 | GET /api/logs、GET /api/admin/logs、GET /api/admin/audit-logs |
| 结算异常 | 合并到管理员请求日志，支持详情、幂等重试和人工结算；旧 charges API 保留兼容 |
| 设置 | GET/PUT /api/admin/settings |
| 网关 | POST /v1/messages、POST /v1/messages/count_tokens、POST /v1/responses、GET /v1/models；最终按客户端验证补充必要端点 |

页面登录使用 HttpOnly 会话 Cookie，配置适当的 SameSite 与 HTTPS 安全属性；状态修改接口进行 CSRF 防护。初始化只能成功一次，部署口令从日志和响应中脱敏。

## 开发顺序与验收

1. **先做协议可行性验证**：逐家验证授权、刷新、模型发现、最小流式调用和工具回传，明确客户端版本与必要端点。需要真实账号时使用部署者本地凭据，不写入仓库。
2. **建立基础服务**：Go/Vue 脚手架、SQLite 迁移、初始化、登录、用户和 Key。验收重复初始化失败、无公开注册及用户数据隔离。
3. **实现额度与价格**：独立完成双窗口、定点金额、预占和结算。测试并发抢额度、幂等、重置/跨窗口、价格变化、断连及重启恢复。
4. **接入网关与页面**：逐平台加入真实转发、用量解析、账号冷却与管理页面，记录端到端兼容结果，不以模拟结果替代线上验证。
5. **完善发布**：日志清理、备份恢复、Docker 与可执行文件、容量压测、许可证与凭据检查、README 和配置示例。

开源发布前核对每个复用文件的来源和许可证，避免整套搬入不需要的业务模块。发布 GitHub 仓库及上传镜像作为单独的发布动作执行。
