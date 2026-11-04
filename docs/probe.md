# 本地 OAuth 与模型发现探针

这是协议验证工具，不是完整网关。目前实现授权会话、一次性换码、刷新、Antigravity 项目发现与可选初始化、模型目录，以及最小文本/SSE/工具闭环推理验证。尚未实现生产调度、Claude Code/Codex 协议转换或模型分页。

## 运行

需要 Go 1.25 或更高版本。探针协议模块本身使用标准库；同仓库的正式服务另有 SQLite 与密码哈希依赖。

```sh
go test ./...
go build -buildvcs=false -o bin/probe ./cmd/probe
./bin/probe -config examples/probe/claude.json
```

也可以选择 `examples/probe/openai.json`。按照终端提示在浏览器打开授权链接，然后粘贴完整回调 URL，或 Claude 页面显示的 `code#state`。本工具不监听回调端口；localhost 页面连接失败时，可从浏览器地址栏复制完整回调 URL。仅提交 code 会被拒绝，因为缺少 state 校验。

配置指定的上游端点会接收授权码、令牌及可选客户端 secret。只使用可信配置文件；工具要求 HTTPS 且不跟随 HTTP 重定向。终端输入授权结果可能可见，应在自己的终端操作，不录屏或共享会话。

默认令牌仅存在进程内存，不打印或落盘。可选择本地加密会话，详见下文。`-refresh` 会测试一次刷新，可能轮换 refresh_token；保存的会话临近过期（3 分钟）时也会自动刷新并保存新令牌。

## 示例的适用范围

三个示例的端点及公开 client ID 是根据本地 sub2api 源码整理的待实测配置，不表示这些客户端已经授权本项目，也不保证线上仍接受这些参数。没有复制参考项目的 OAuth client secret 或账号凭据。

Claude 示例先使用 OAuth beta 请求头验证目录访问；如果上游还要求其他客户端头，应按实测补充，不能把缺少头导致的失败解释成账号无模型。

OpenAI 示例中的 client_version 为待验证的静态值，可能需要按实际客户端调整。其路径是本地参考实现使用的 Codex 目录，不是已完成线上验证的官方接入承诺。官方另有 [Codex app-server 接入说明](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)，其中也明确目录不能作为实际模型权限的证明；是否改用该接入方式待后续评估。

Antigravity 不需要先手动填写 project ID：默认在换码后调用同一上游主机的 `loadCodeAssist`，读取账号已有的 `cloudaicompanionProject`，再获取模型目录。也可以在本地配置的 `models_body.project` 中指定已有项目。

如果账号未返回项目，默认停止。可先在官方 Antigravity 客户端完成登录与初始化后重试；或显式启用初始化：

```sh
python3 scripts/probe-antigravity.py --onboard
```

这会在缺少项目且上游返回默认 tier 时调用 `onboardUser`，改变该账号的服务初始化状态。已发现项目则跳过初始化；没有默认 tier 时不自行选择其他 tier。直接运行 Go 探针时对应参数为 `-onboard`。该流程不要求用户手动创建 Google Cloud 项目；不保证所有账号都能初始化成功。

已补齐参考实现的外层恢复：项目发现流程最多 4 轮，间隔 1、2、4 秒，每轮重新调用 `loadCodeAssist`。`onboardUser` 返回完成但缺少项目时不会立即退出，而是进入下一轮项目发现；`done=false` 时内层最多请求 5 次、间隔 2 秒。外层重试也覆盖发现或初始化错误，整个发现阶段受 2 分钟超时及取消限制。最终失败显示“OAuth 授权成功，项目待补全”，不会把已有授权混同为换码失败。

项目发现 metadata 包含 IDE 版本，发现、初始化和模型请求使用一致的 Antigravity UA。版本优先级为配置中的 `antigravity_user_agent_version`、环境变量 `ANTIGRAVITY_USER_AGENT_VERSION`、内置默认值。辅助脚本在环境未覆盖时读取参考源码的版本默认值。账号级代理专用超时、订阅/隐私管理仍未完成迁移。

当主端点是官方生产端点时，默认允许降级到官方 daily 端点，反向亦然。自定义主端点不会自动访问官方服务，可用 `antigravity_fallback_urls` 显式配置 HTTPS 备用源站。项目发现、初始化、模型目录请求均仅在网络错误或 HTTP 404/408/429/5xx 时切换；401/403 和响应解析错误不在同一请求内切换。父上下文取消后停止请求。与参考实现一致，发现流程的外层重试仍可能重新尝试主端点，不能把“不切换”理解成不重试。

## 保存授权，继续补全项目

```sh
python3 scripts/probe-antigravity.py --onboard --save-session
```

第一次成功换码即保存加密会话，早于项目发现。之后再次运行同一命令，会复用该会话并在临近过期时刷新；即使上次项目获取失败，也不要求重新浏览器授权。

- `.local/antigravity.session`：AES-256-GCM 加密的令牌与到期时间。
- `.local/probe.key`：独立随机密钥，文件权限 0600，仅首次创建；已有会话缺少密钥时停止，不生成替代密钥。
- 会话与供应商、客户端、授权及模型端点绑定，配置改变或文件损坏时拒绝解密，不把令牌发送到改变后的端点。

`.local/` 已忽略版本控制。这是单进程开发辅助，不是生产账号数据库或多进程刷新协调方案；本地密钥与密文都需要保护，不要共享或上传。退出并关闭其他探针后，删除会话文件可重新授权；不要单独删除已有会话所需的密钥。若要同时重建二者，应先确认不再需要旧会话。正式服务仍按需求使用 SQLite 和独立管理的加密主密钥。

直接运行 Go 时可同时指定 `-session-file` 与 `-key-file`；两者必须是不同文件。未指定时维持内存模式。

测试前仍需在本机设置与示例 client ID 配套的 `ANTIGRAVITY_OAUTH_CLIENT_SECRET`。该值不等于个人 Google 账号密码；不要自行随便创建一个不匹配的 secret，也不要提交到 JSON 或聊天。若有自己获准使用的 OAuth 客户端配置，需要成套修改 client ID、secret、redirect URI 和 scope。

```sh
# zsh：隐藏输入，不把值写进命令历史
read -rs 'ANTIGRAVITY_OAUTH_CLIENT_SECRET?输入配套的 OAuth client secret: '
export ANTIGRAVITY_OAUTH_CLIENT_SECRET
go run ./cmd/probe -config examples/probe/antigravity.json
unset ANTIGRAVITY_OAUTH_CLIENT_SECRET
```

上面的环境变量仅提供给探针进程，探针不从现有客户端目录读取账号令牌。

模型结果仅输出去重后的 ID，不输出供应商原始响应。缺失、null 或错误对象会报错，合法空列表则正常返回。当前只读取一页，不能将输出宣称为完整可用模型全集。

## 验证与后续

### 指定模型推理

```sh
python3 scripts/probe-antigravity.py --save-session --no-login \
  --model gemini-3.5-flash-lite --tools
```

`--model` 启用一次最小文本/SSE 推理，`--tools` 再执行工具发起和结果回传，共三次推理请求，会消耗上游额度。只执行固定的本地加法 `probe_add(2,3)`，不执行模型任意指定的工具或命令。`--no-login` 表示缺少本地会话时停止，不自动进入浏览器授权。

辅助脚本从参考源码读取 Antigravity 身份提示作为本次环境配置，探针使用 v1internal 包装、请求 ID、Antigravity UA 和 `streamGenerateContent?alt=sse`；官方端点的推理优先 daily。当前推理不自动重放、不自动换模型；项目发现与目录的降级策略不意味着推理也会重试。

模型返回的所有原始工具 part 都原样留在内存历史中回传，包括 thoughtSignature 和调用 ID，不生成虚构签名、不打印思考内容。这也符合 [Gemini Generate Content 的签名处理说明](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures)。该探针只覆盖一次完整函数调用，尚不覆盖任意并行或增量工具参数组合。

SSE 解析检查事件 JSON 和 STOP 结束状态，错误事件、截断和大小超限都算失败。用量是累积快照，逐字段取最大值，不逐事件相加；缺失字段保持 null，候选输出和思考 Token 分开记录。终端输出各阶段结果；即使后续阶段失败，先前阶段结果也会保留。

2026-10-30 已使用 gemini-3.5-flash-lite 完成三次真实推理验证，详细结果见 [compatibility.md](compatibility.md)。

测试覆盖 PKCE 参数、错配/过期/重复 state、并发一次性消费、JSON/form 换码、刷新令牌保留、目录解析、错误脱敏、禁止重定向、初始化后延迟出现项目、退避与取消、端点降级条件，以及加密会话的损坏/错密钥/错配置拒绝和跨次恢复。模拟上游测试需要允许监听本机临时端口。

真实测试结果记录到 [compatibility.md](compatibility.md)，不要记录授权 URL、授权结果或令牌。下一步将已验证的适配行为接入正式账号与网关模块，并验证其他平台和目标客户端。

协议源码现位于 `internal/providers`，命令仍是 `cmd/probe`，不影响现有运行方式。正式账号存储和导入步骤见 [部署说明](deployment.md)。

## 错误定位

如果 Antigravity 换码返回 `invalid_client`，可以用本地辅助脚本排除手工输入配套参数出错：

```sh
python3 scripts/probe-antigravity.py
```

脚本读取本机 sub2api 源码（默认与本仓库同级的 `../sub2api`）中的公开 client ID 和默认 client secret，核对与示例 ID 一致，然后仅在探针子进程中设置 secret；不打印或保存 secret，也不读取用户账号令牌。可用 `--reference /path/to/sub2api` 指定参考仓库。该脚本只是开发辅助，不是发布后的默认依赖。

它会覆盖子进程继承的手工 secret 值；仍需打开本次新生成的授权链接。如果配套参数仍返回 `invalid_client`，应核实参考参数是否已失效或被上游拒绝，不能认为一定是用户复制出错。

终端会区分 OAuth 换码、OAuth 刷新和模型发现阶段。项目发现失败会额外注明 Antigravity 项目自动发现失败。HTTP 错误只显示状态及白名单内的标准错误码，不输出原始响应或任意错误描述。

- `invalid_client`：核对 client ID 与配套 client secret，尤其检查误填、空格或复制时带入引号。
- `invalid_grant`：重新启动探针、打开本次新链接并使用新回调；授权码只能使用一次，旧回调不能跨进程复用。也需核对 redirect URI。
- `UNAUTHENTICATED`：上游未接受访问令牌；不能据此认定缺少 project ID。
- 没有标准错误码的 401：先根据阶段定位，不猜测账号权限或改动 project ID。

重试时重新生成授权链接。分享错误时只贴终端的阶段、HTTP 状态和标准错误码，不贴完整回调 URL。
