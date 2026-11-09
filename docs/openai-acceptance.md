# OpenAI 真实账号验收

以下记录公开预览版所依据的真实验收结果；新版客户端身份和 GPT-6.1 实测见文末。

日期：2026-11-02；修复使用页面已导入的 OpenAI 账号和目录中的 `gpt-5.6-luna`，不把目录可见等同于调用权限。

## 结果

| 项目 | 结果 |
| --- | --- |
| 真实目录及 Codex manifest | 通过，目录 5 项，元数据保留并筛选已启用、定价的模型 |
| 非流式文本 | 通过，上游 SSE 聚合为 JSON，固定文本标记一致 |
| 流式工具调用 | 通过，只允许固定的 fiftyapi_probe_add(2,3)，未执行模型指定的其他操作 |
| 多轮工具结果回传 | 通过，保留原始响应项目及加密 reasoning 历史，最终答案为 5 |
| 客户端取消 | 首个文本增量后断开，账本自动结算，预占归零 |
| 日志、5 小时及 7 天额度 | 临时库中的 4 条请求日志与两窗口金额一致，无待核算 |
| compact | 此次真实上游返回 HTTP 404，未通过；模拟测试通过不代表真实支持 |
| OAuth 换码、真实刷新与轮换 | 未验证，本次复用已导入且有效的 access token |
| 原生 Codex 客户端、WebSocket | 本轮未验收；当时尚未实现，后续验收见文末补充 |

最终成功的四请求验收批次按临时内部测试价格 1 microUSD/Token（缓存输入相同价格）结算为 **424 microUSD**。取消请求未收到可靠用量，按现有策略结算为 0 并释放预占。这是内部核算验证，不是供应商报价，也不代表取消请求没有上游消耗；该数字不含此前诊断失败或独立 compact 尝试的上游消耗。

## 修复的问题

真实上游返回 HTTP 200 和 SSE 正文，但未声明 `text/event-stream`，旧实现按 JSON 解析而返回 502。现在普通 OpenAI 推理明确请求 SSE，compact 明确请求 JSON；响应类型缺失、`text/plain` 或 `application/octet-stream` 时，最多查看 6 字节前缀识别 `event:` / `data:`，随后仍执行完整 SSE 解析、完成事件和用量校验。明确声明 JSON/HTML 的响应不会被擅自重解释为 SSE。

模拟回归覆盖标准/缺失/通用响应类型、非流式聚合、SSE 透传、JSON compact、通用类型 JSON、错误内容、截断及费用释放。没有自动重放推理或切换模型。

## 复现

测试默认跳过，CI 不访问真实供应商。需明确指定源数据库绝对路径、原主密钥文件绝对路径、启用中的 OpenAI 账号 ID 和模型。不要把密钥内容放进命令行。

```sh
FIFTYAPI_TEST_OPENAI_LIVE=1 \
FIFTYAPI_TEST_OPENAI_DB=/absolute/path/data/fiftyapi.db \
FIFTYAPI_TEST_OPENAI_KEY_FILE=/absolute/path/.local/server-master.key \
FIFTYAPI_TEST_OPENAI_ACCOUNT=3 \
FIFTYAPI_TEST_OPENAI_MODEL=gpt-5.6-luna \
go test -count=1 -timeout 8m -run '^TestOpenAILive$' -v ./internal/gateway
```

会产生四次真实推理请求。工具仅计算固定的 2+3；取消请求只要求输出 1 到 40。上游未必支持硬性输出 Token 限制，测试使用短提示、超时和主动断开控制规模，不承诺供应商消耗上限。

只有设置 `FIFTYAPI_TEST_OPENAI_COMPACT=1` 才会在核心验收后再验证一次 compact；404 会明确失败，不自动降级。目录验证可有限重试，推理不重试。

源数据库以只读方式打开；凭据仅在内存中解密，复制到独立随机主密钥加密的临时数据库，并移除刷新令牌，避免隔离测试轮换正式授权。access token 少于 10 分钟有效期时停止，先在正式服务验证账号后重试。临时库、成员、Key、价格和额度退出时清理，不修改正式用户或上游配置，不输出响应正文、令牌或工具调用 ID。

代码：`internal/gateway/openai_live_test.go`；修复：`internal/gateway/native.go`。

## 补充：GPT-6.1 Sol 与目录版本

已确认目录差异来自旧 Codex 身份：同一授权，0.146.0 返回 5 项、0.153.0 返回 6 项、0.164.0 返回 10 项并包含 `gpt-6.1-sol`。旧身份下的 `model_not_found` 不能解释为该账号没有权限。详细对照与升级操作见 [账号模型目录](account-models.md)。

使用统一的新身份和 `FIFTYAPI_TEST_OPENAI_MODEL=gpt-6.1-sol`，真实目录/manifest、JSON 文本、SSE 工具调用/历史回传和客户端取消全部通过。最终四请求批次测试内部金额 **377 microUSD**，两窗口、日志和账本一致，预占及待核算为零；取消未取得可靠用量，结算 0。与前文一样，内部测试金额不是供应商报价，也不统计此前诊断请求的消耗。

此次未启用 compact 测试；前文 404 是旧 0.146.0 身份下的历史结果，新身份的 compact 可用性尚待重新验证。OAuth 换码、真实刷新、原生 CLI 和 WebSocket 不在本轮通过范围。源数据库和正式账单未修改。

## 补充：原生 WebSocket 与客户端

2026-11-02，复用同一只读源账号的有效 access token，gpt-6.1-sol 真实目录 10 项。源库不迁移、不刷新令牌、不改余额；所有测试账号、Key、账本和定价均在临时库。

- HTTP 四请求重新通过：短文本、固定加法工具与结果回传、首个文本后取消。内部测试核算 377 microUSD，两窗口一致、预占 0；取消未获得可靠 usage，按既有策略为 0，这不表示供应商没有消耗。
- 原生 WebSocket 三 turn 通过：generate:false 预热、带预热 ID 的短答、同连接前一响应 ID 延续并推断模型。内部测试核算 152 microUSD，预占 0。没有模拟历史重建或 HTTP 降级。
- 本机 Codex **0.164.0-alpha.11.1** 使用临时 HOME/CODEX_HOME、自定义 Responses provider、supports_websockets=true，固定短提示返回指定标记。服务确认 1 次 WebSocket 接入、2 条成功请求（包含客户端预热），预占 0。子进程只持有临时网关 Key，不加载原用户配置，不执行工具或访问工作区文件。

上述金额来自 1 microUSD/报告 token 的内部测试价格，不是供应商报价；不含其他诊断尝试消耗。新增断开/撤销/Origin/停机排空及用户隔离由模拟竞态用例验证。真实 Claude、授权码换取、真实 refresh token 轮换、新身份 compact、长时间带工具的 Codex 工作流仍未覆盖。

复现：沿用上文只读源参数，模型换成 gpt-6.1-sol。额外设置 `FIFTYAPI_TEST_OPENAI_WS_LIVE=1` 会在四请求后追加三次 WS turn；设置 `FIFTYAPI_TEST_CODEX_CLI=/absolute/path/to/codex` 则只执行目录验证与原生 CLI 固定短提示，跳过四请求/WS 批次。默认全部跳过，需明确授权少量真实消耗。测试代码不输出子进程正文或凭据。

服务单连接内顺序执行，跨连接缓存不共享；不支持 steer 或显式 cancel，关闭连接会取消。接入配置与恢复方式见 [客户端](clients.md)，服务限制见 [账号与网关](reliability-redesign.md)。
