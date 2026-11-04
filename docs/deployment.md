# 部署与运维

单实例 Go 服务，Vue 页面内嵌，SQLite WAL/FULL、外键及 5 秒锁等待；无 Redis。数据库放在本机持久化磁盘，同一数据库只允许一个服务进程。文件锁不等同于集群支持，不使用 NFS 共享运行数据库。

## 二进制启动

推荐使用 Go 1.26.10；`go.mod` 保留 Go 1.25.0 作为最低语言版本。Rocky Linux 9.8 可使用系统 Go 工具链构建，SQLite 使用纯 Go 驱动，无需安装 SQLite 开发库。执行竞态测试需要 GCC；离线备份需要 Python 3.9+。

```sh
go build -buildvcs=false -o bin/fiftyapi ./cmd/fiftyapi
./scripts/start-local.sh
```

制作发布包使用 `./scripts/build-release.sh`（需要 Node 24+），生成 `CGO_ENABLED=0` 二进制。发布脚本默认 `GOAMD64=v1`，避免发行版工具链的 v2 默认值提高 x86-64 CPU 要求；可以通过环境变量显式覆盖。普通 `go build` 仍使用系统工具链默认值。升级 Go 后需要重新构建二进制才能使用新的运行时和标准库。

Rocky Linux 启用 SELinux 时，Compose 默认 named volume 可继续使用；若改用专属数据目录的 bind mount，使用 `./data:/data:Z`，并确保容器 UID/GID 10001 可写。默认仅监听回环地址的部署无需开放外部端口；对外提供服务时按实际入口配置 firewalld。

本机脚本在 `.local/` 首次创建主密钥和部署口令，以后复用。读取 `.local/setup-token` 填入网页初始化表单，设置管理员用户名和密码；随后正常登录。登录页提供注册入口，邮箱、真实姓名和密码必填；新用户两种额度均为 0，由管理员分配。不要重新生成旧数据库的主密钥。

生产环境可直接运行 `fiftyapi serve`，由进程管理器注入环境变量：

| 变量 | 用途 |
| --- | --- |
| `FIFTYAPI_MASTER_KEY` | 可选：32 字节随机密钥的 Base64 编码；留空时首次启动在数据库旁自动生成 `master.key` |
| `FIFTYAPI_SETUP_TOKEN` | 可选：至少 8 字节；留空时首次初始化在数据库旁自动生成 `setup-token` |
| `FIFTYAPI_DB_PATH` | 新部署默认 `data/fiftyapi.db`；未显式指定时兼容同目录旧数据库 |
| `FIFTYAPI_DATA_DIR` | 默认 `data`；容器默认 `/data`，用于自动选择数据库文件 |
| `FIFTYAPI_LISTEN` | 默认 `127.0.0.1:8080` |
| `FIFTYAPI_COOKIE_SECURE` | HTTPS 部署设 `true`；HTTP（包括内网）设 `false` |
| `ANTIGRAVITY_OAUTH_CLIENT_SECRET` | 可选，覆盖配套桌面客户端参数；通常留空使用内置配置，不能填随机值 |

主密钥错误会拒绝启动，不会覆盖已有凭据。管理员和普通用户密码均为 8–72 字节，使用 bcrypt；会话有效期 12 小时，Cookie HttpOnly/SameSiteStrict，写操作需 CSRF，管理接口校验角色。

## Docker Compose

```sh
cp .env.example .env
docker compose up -d --build
```

`.env` 中两个密钥可留空，程序会在数据卷中自动生成 `/data/master.key` 和 `/data/setup-token`（权限 0600），重启、重建容器时复用。读取初始化口令：`docker compose exec fiftyapi cat /data/setup-token`。不要把两个值统一设为 `fiftyapi`；显式配置的环境变量优先，仍需满足长度和格式要求。依赖网络受限时可设置 `GOPROXY=https://goproxy.cn,direct` 再构建。Compose 默认只绑定 `127.0.0.1:8080`；数据存于 named volume `data`。镜像 UID/GID 为 10001，根目录只读；若改用 bind mount，需要让 10001 对数据目录可写。

首次访问、OAuth 和模型启用流程与二进制一致。`GET /healthz` 检查数据库连接；Docker 配置了健康检查。升级前先备份；新版本启动自动迁移，拒绝比程序更新的数据库 schema。

## 使用已发布镜像

Docker Hub 镜像为 [`dyunwei/fiftyapi`](https://hub.docker.com/r/dyunwei/fiftyapi)。使用仓库中的 Compose 配置时，将 `.env` 的 `FIFTYAPI_IMAGE` 设置为 `dyunwei/fiftyapi`，再执行：

```sh
docker compose pull
docker compose up -d --no-build
```

无需源码也可直接运行镜像：

```sh
docker run -d --name fiftyapi --restart unless-stopped --stop-timeout 60 \
  -p 28080:8080 -v fiftyapi-data:/data dyunwei/fiftyapi
docker exec fiftyapi cat /data/setup-token
```

无需提前配置两个密钥。打开 `http://服务器内网IP:28080`，填入上一步读取的口令并设置管理员。`FIFTYAPI_PUBLIC_URL` 只需在发送邀请时配置为收件人能访问的 HTTP 或 HTTPS 站点地址，例如 `http://192.168.1.10:28080`，也可在网页邮件配置中填写。HTTP 部署保持 `FIFTYAPI_COOKIE_SECURE=false`。宿主机端口为 28080，容器内仍监听 8080。

自动生成的主密钥必须与数据卷一起保留，并另行备份；在线数据库备份仍不包含它。已有数据库丢失 `master.key` 且未提供原 `FIFTYAPI_MASTER_KEY` 时会拒绝启动，不生成替代密钥。恢复旧数据库须先恢复原主密钥；自定义数据库路径时，自动密钥文件位于该数据库所在目录。

## HTTPS 反向代理

页面和 `/api/`、`/v1/` 保持同源；设置 `FIFTYAPI_COOKIE_SECURE=true`。代理需允许长时间流式响应、关闭缓冲，并限制请求体至少能容纳服务允许的 4 MiB 网关输入。例如 Nginx 的同一 `location /` 中配置：

```nginx
proxy_pass http://127.0.0.1:8080;
proxy_set_header Host $host;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_buffering off;
proxy_read_timeout 600s;
client_max_body_size 4m;
```

登录限流按直接连接 IP 计算，当前不信任任意转发 IP；反向代理后的多人登录会共享这一限制（每分钟 10 次）。

## 上游账号与配置

管理员在“上游账号”添加 Claude、OpenAI 或 Antigravity。授权链接在浏览器中打开；授权完成后把结果地址粘贴回页面。localhost 回调未监听时浏览器可能提示连接失败，保留地址栏结果粘贴即可。Claude 使用其供应商回调；OAuth state、PKCE、有效期和单次消费在服务端校验。

授权成功后先加密保存凭据，再发现项目与模型。`catalog_ready` 只表示目录可读；`project_pending` 可重试补全；`reauth_required` 需要重新授权。后台每分钟维护账号，目录检查有 5 分钟间隔。刷新先保存轮换令牌，避免后续目录失败丢失授权。Antigravity 初始化可在授权时明确勾选，后台不隐式执行 onboardUser。

账号“配置”提供分平台表单：网关完整 URL、代理、请求头、客户端版本、超时，以及平台适用的身份/metadata/system 设置。URL 必须 HTTPS；代理支持 HTTP/SOCKS。代理密码显示为 `***`，原样保存保留密码。自定义请求头不能覆盖认证凭据。修改后重新验证账号；通常保留默认值即可。完整支持范围见 [迁移清单](upstream-migration.md)。

已有探针会话也可导入（服务主密钥和数据库环境保持一致）：

```sh
./bin/fiftyapi import-antigravity --name antigravity-main --admin ADMIN_NAME \
  --config examples/probe/antigravity.json \
  --session-file .local/antigravity.session --key-file .local/providers.key
```

`--key-file` 必须指向该会话原有密钥；按自己的实际路径调整。导入后在页面验证目录。

## 价格、额度及待核算请求

模型首次同步可匹配内置文本价格快照；无价格的模型禁止调用，需管理员确认价格后启用。目录刷新不覆盖人工价格。页面单位为 USD/百万 Token，后端按整数 microUSD 存储与计算（1 USD = 1,000,000 microUSD）。输入、输出、缓存读取和缓存写入分开计价；这里只是内部额度核算，不是 OAuth 订阅实际账单。

所有 Key 和供应商共享用户的 5 小时/7 天预算，首次正金额结算开始窗口，到期恢复，剩余额度不累计。任意窗口不足即阻止新调用。在途请求可能超出预估与额度；修改上限不会清空已用金额，明确“重置额度”会留下操作记录。

请求先预占，再按可靠 usage 与原价格快照结算。断流/取消但已有可靠用量时自动按已收到用量结算；缺失或无效用量按 0 结算并释放预占，日志明确原因。只有账本写入失败、价格快照损坏等异常保留待核算；启动时也会按此策略处理旧待核算和在途记录。按 0 结算表示不向本地用户分摊这笔用量，不代表上游没有产生费用。详见 [自动结算](automatic-settlement.md)。日志仅记录模型、状态、Token、金额、时长等元数据，不保存完整提示词/回复。默认保留 30 天，可设 1–3650 天；清理日志不重置额度账本。

## 备份和恢复

最稳妥方式：停止服务 → 复制整个数据目录（含存在的 WAL/SHM 文件）→ 单独备份原主密钥 → 再启动。运行时不要只复制 `.db` 主文件当完整备份；在线备份应使用 SQLite backup API。Docker named volume 也应在停机后备份。

恢复到空数据目录，修正权限，设置同一主密钥再启动。升级失败需要旧程序与升级前数据库备份一起恢复，不能让旧程序直接打开新 schema。主密钥丢失后无法恢复上游令牌；初始化部署口令不能替代主密钥。


## 离线备份工具

需 Linux/Unix 与 Python 3.9+，并先停止服务和账号导入命令。在源码目录或发布包中执行：

```sh
python3 scripts/backup-offline.py create --db data/fiftyapi.db --output backups/first-snapshot
python3 scripts/backup-offline.py restore --from backups/first-snapshot --output restored-data
```

输出目录必须尚不存在；运行中的服务持有文件锁时拒绝备份。工具保存 `.db` 及存在的 WAL/SHM，生成 SHA-256 清单，恢复前后检查内容，不会自动复制主密钥。恢复后以 `FIFTYAPI_DB_PATH=restored-data/fiftyapi.db` 和**原主密钥**启动；不要同时启动原数据库与其副本连接同一批上游账号。

Docker named volume 可停机后导出到本机目录再备份；恢复 bind mount 时确保 UID 10001 可写。备份也包含用户元数据，应限制读取权限。

`fiftyapi version` 无需主密钥即可查看版本，`/api/version` 返回版本和构建 commit。请求日志可按完整请求 ID 查询；手工核算会一次提交额度、日志金额/依据与管理员审计。


### 网页授权返回 invalid_client

内置 Antigravity 客户端已有配套参数，通常无需设置 `ANTIGRAVITY_OAUTH_CLIENT_SECRET`。以前在终端随意填写过该变量时，网页服务会继承旧值；开发探针辅助脚本会对子进程覆盖该值，所以探针成功不代表网页服务环境已修正。

本机启动先执行 `unset ANTIGRAVITY_OAUTH_CLIENT_SECRET` 再启动服务；Docker 则清空 `.env` 中的该变量，并重新创建容器。修正后在页面重新生成授权链接，不要复用失败的回调。不要输出或分享完整环境、授权结果及令牌。


## 邀请普通用户

新版“添加成员”通过邮件发送一次性设置密码链接，可在系统设置配置 SMTP 和公共访问地址，也兼容部署环境变量。完整配置、有效期、失败重试和旧用户兼容规则见 [邮件邀请说明](email-invitations.md)。未配置邮件时，原账号仍可登录，但不能从页面发送邀请。


## API Key 网关上游

在“上游账号 → +接入上游API”添加 Sub2Api、FiftyAPI、New API 等兼容来源。API Key 加密保存，支持 Chat Completions / Messages / Responses、目录发现、手工映射与本地定价；不执行 OAuth 专用改写。完整步骤和边界见 [网关接入](gateway-upstreams.md)。升级前备份数据库及原主密钥；旧程序不能打开新 schema。

## 升级与管理接口

先按本文离线备份流程备份数据库及主密钥，再替换二进制/重新构建容器并启动；保持原 DB 路径、主密钥和监听地址。首次启动自动执行所需迁移，当前 schema 为 9。升级后不能用旧二进制直接打开已迁移数据库；回退需恢复升级前备份。前端已嵌入新二进制，更新源码不会改变正在运行的旧进程。

API Key 页面同时提供撤销与删除：撤销后保留可见记录；删除不可恢复，立即拒绝后续调用并从列表与数量统计移除，保留历史请求、价格快照和结算关联。已在执行的请求仍可结算。接口均要求当前用户会话和 CSRF，不能操作其他用户的 Key：

- `POST /api/keys/{id}/revoke`：撤销。
- `DELETE /api/keys/{id}`：删除；如需保留可见记录应使用撤销端点。

模型与价格页面默认每页 10 项，可选择 10/20/50/100。搜索匹配模型 ID、上游 ID 或平台，忽略 ASCII 大小写，`%`/`_` 按字面匹配。编辑价格后保留搜索和页码。

- `GET /api/admin/models?page=1&page_size=20&q=gemini` 返回 `{items,total,page,page_size}`。
- `GET /api/models` 同样可带分页参数，但只返回当前可公开调用的模型。
- page 从 1 开始，page_size 为 1–100；page/page_size 省略时分别默认为 1/20；不传任何分页或搜索参数时保持原有数组格式。公开协议目录 `/v1/models` 继续返回完整可调用目录。
- Chat Completions 接入方式见 [客户端配置](clients.md)，支持范围见 [兼容性](compatibility.md)。

## 日志与系统设置

不再提供独立待核算页面。在管理员「请求日志」选择「结算异常」并筛选，可以查看结算详情、按已保存用量重试，或填写金额和依据人工核算。重试不重新调用模型，成功的重试/人工核算与账本、日志、审计一起事务提交。旧 `#charges` 书签跳转到日志异常筛选，旧 charges 管理 API 兼容保留。

普通用户和管理员日志均支持服务端分页，页面默认 10 项（API 默认 20 项），可选 10/20/50/100；筛选后回到第一页，翻页保留筛选。日志持久化失败的账本异常也会显示，缺失的 HTTP 状态显示“未记录”。接口和权限说明见 [请求日志](request-logs.md)。

「系统设置 → 邮件配置」可保存 SMTP 参数、启停邀请、测试发送和恢复环境配置；保存后立即生效。密码留空保留、勾选清除才删除，读取不返回密码。网页配置使用原主密钥加密存入现有 settings 表，无新增数据库迁移。完整说明见 [邮件配置](email-invitations.md)。

升级仍需先备份数据库和原主密钥，再切换新版二进制/容器并重启一次；之后邮件设置变更无需重启。

## 账号模型目录与 OpenAI 身份

账号保存独立模型同步时间，不重写旧凭据和模型关联。旧目录时间显示未记录，重新同步后补齐。保持原主密钥和数据库，按前文备份/回滚方式操作。OpenAI 旧内置官方 0.146.0 身份改用已实测的 0.164.0；无需重新授权，启动新版后验证并同步。账号模型的查看、搜索、分页及定价状态见 [账号模型目录](account-models.md)。

## 升级与 WebSocket 反向代理

账号策略、健康、冷却与请求尝试会持久化保存。原账号 ID、加密绑定、用户/Key、模型、账本和原 DB 文件名保留。先停服并完成前文离线备份，保留原主密钥，再替换程序；旧版本回滚须恢复升级前数据库。无需先删除或重新导入账号。

原生 WebSocket 使用 `GET /v1/responses`（兼容 `/responses`）。反向代理需要转发 Upgrade/Connection 并允许长连接；例如 Nginx 对该路径配置：

```nginx
location = /v1/responses {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
    proxy_buffering off;
}
```

此路径同时承载 HTTP POST，其他既有代理路径仍需保留；按实际域名配置 Host/HTTPS，浏览器 Origin 默认同源校验。服务收到停机信号先取消 WebSocket 并等待结算，再关闭数据库。连接内上下文在重启后丢失，客户端应发送完整历史。详见 [账号与网关](reliability-redesign.md) 和 [客户端](clients.md)。

Compose 已设置 60 秒停机宽限；直接 docker run 或进程管理器也应给予至少 60 秒。已经开始的 OAuth 令牌轮换最多执行 30 秒，保存后才关闭数据库，避免浏览器取消或部署停机丢失新刷新令牌。SIGKILL/主机断电仍无法保证远端已轮换但尚未返回的令牌可恢复。

## 协议升级

迁移 009 将数据库升级到 schema 9，保留旧模型 ID、价格和记录。新增多协议能力，移除 Chat→Messages 自动转换；原来用 Chat 调 Claude/Antigravity/仅 Messages 网关的客户端需要改为 Messages 模式。上游 API 支持多选默认协议及逐模型覆盖。升级前备份，回退恢复旧备份；完整操作见 [模型协议](model-protocols.md)。

## 并发设置入口

账号最大并发只在“调度与健康”编辑；连接配置移除并发输入，保存连接不会更改调度策略。为兼容旧客户端，配置读取仍返回 max_concurrency，但更新连接时忽略该字段；旧表单、缺省 0 都不会重置已设置的并发。已有设置和旧凭据的一次性迁移保留，无新增迁移，当前 schema 为 9。

## 自助注册

管理员初始化后，登录页可切换到「注册账号」。`POST /api/auth/register` 接收 `email`、`real_name`、`password` 三个必填字段：邮箱会去除首尾空格并转成小写，真实姓名去除首尾空格且最多 80 个字符，密码为 8–72 字节。邮箱同时作为登录账号，真实姓名保存在成员的显示名称中，管理员可在团队成员列表查看。

注册只创建普通成员，5 小时和每周额度均固定为 0，不能在请求中指定角色或额度。成功后返回登录页，使用邮箱和密码登录；管理员分配额度后才能调用模型。不依赖 SMTP，不执行邮箱所有权验证。已有邮箱或账号（包括邀请中的账号）不能重复注册；原有管理员和邀请成员的登录方式继续保留。

注册请求执行来源校验，并按来源 IP 限制每分钟 5 次；密码使用现有 bcrypt 成本加密保存。用户与注册操作记录在同一事务中写入。

### 上游模型调用测试与每日激活

管理员在「上游账号 → 已同步模型」中点击「测试」，会弹出调用过程界面，显示协议输入、等待状态、HTTP 状态、耗时与响应正文。多协议模型可切换协议后重新测试，默认发送 `hi`。请求固定到当前账号及模型，复用账号代理、认证刷新与协议适配配置，不要求本地定价，也不计入本站用户账单；仍会消耗上游额度。每次测试最长 90 秒，响应显示上限 256 KiB，并遵守账号并发限制。接口为 `POST /api/admin/accounts/{id}/models/test`，请求体为 `{"model":"已同步的模型 ID","protocol":"responses"}`，需要管理员会话和 CSRF 令牌。

服务运行期间，每天服务进程本地时区的 07:00，自动向每个已启用、已同步模型且无需重新授权的 OpenAI OAuth 账号发送一次 `hi`，选取目录中按 ID 排序的第一个模型，使用 Responses 协议。任务用于尝试启动上游的 5 小时额度窗口，实际额度窗口由上游规则决定。其他供应商及 API Key 网关不参与。结果写入服务日志，包含账号 ID、模型、状态及耗时，不记录凭据或响应正文。失败不会无限重试；停机错过的任务不会补跑，重启后等待下次 07:00。容器时区由 `TZ` 决定（Compose 默认 `Asia/Shanghai`）；本机运行使用操作系统时区。下次执行时间及 UTC 偏移会写入启动日志。

### 管理员运行日志

管理员侧栏的「运行日志」用于查看后台任务和服务运行情况，与请求日志、管理员操作记录分别保存。页面支持按时间、级别（信息 / 警告 / 错误）、模块及账号 ID 筛选、分页和手动刷新。

当前记录服务启动、监听失败、运行异常、关闭及关闭超时；每日 OpenAI 测试的下次执行时间、批次开始结束、各账号结果；账号令牌刷新、验证与模型同步、额度查询、手动模型测试的执行结果；账号维护读取失败及日志清理失败。运行摘要采用固定文案，不记录密钥、令牌、认证请求头、上游错误原文或请求响应正文。HTTP 状态与耗时单独展示。无法打开数据库等发生在数据库初始化之前的异常，以及运行日志自身写入失败，仍需查看终端或容器日志。

运行记录固定保留 7 天，不受请求日志保留天数设置影响，后台每 5 分钟清理一次；过期记录在查询时立即隐藏。相同级别、模块、事件、账号、摘要及 HTTP 状态在同一 5 分钟时间段内合并，保留首次时间、最近时间、累计次数和最后一次耗时。服务重启后记录仍保留；不补录升级前的终端日志。强制终止或断电可能来不及记录关闭事件。

查询接口：`GET /api/admin/runtime-logs`，仅管理员会话可访问。可选参数：`page`（默认 1）、`page_size`（默认 20，最大 100）、`level`、`module`（`server` / `scheduler` / `accounts` / `database`）、`account_id`、`since` 与 `until`（Unix 秒）。时间条件按最近发生时间筛选。

### 在线备份到 S3 / Cloudflare R2

管理员可在「系统设置 → 备份」配置 S3 兼容存储。填写 HTTPS 端点（仅源站，不带存储桶路径）、区域、预先创建的存储桶、Key 前缀和读写凭据；支持 Path Style。R2 的区域填写 `auto`，端点通常为 `https://<account_id>.r2.cloudflarestorage.com`。其他 S3 服务填写其实际区域与端点。配置依据 [AWS Signature V4](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-authenticating-requests.html) 和 [Cloudflare R2 S3 API](https://developers.cloudflare.com/r2/api/s3/api/)。

Access Key ID 与 Secret Access Key 使用原系统主密钥加密保存，读取接口不返回凭据；留空表示沿用已保存值。测试连接使用表单当前值，上传并删除一个 `.connection-test-*` 小文件；它验证写入和删除权限，下载还需要对象读取权限。服务不跟随 S3 重定向，需填写正确区域的端点。

可配置自动备份开关、每天执行时间（服务进程本地时区）及默认保留天数（1–3650 天，默认 14 天）。计划每 30 秒检查一次，自动备份每天最多启动一次；当天时间已过且尚未执行时，保存配置或重启后会补跑一次。失败的自动任务当天不再自动重建，可手动补备份。手动创建时可单独指定本次保留天数。修改默认保留时间只影响之后创建的备份，不改变已有记录的过期时间。

备份在后台执行，前端可查看状态、文件名、大小、触发方式、开始与过期时间，并下载或删除文件。单次只允许一个备份任务。服务使用 `VACUUM INTO` 创建包含已提交 WAL 数据的一致性 SQLite 快照，并检查快照完整性；生成 `tar.gz` 压缩包，内含 `fiftyapi.db` 和兼容离线恢复脚本的 SHA-256 `manifest.json`。数据库为单连接，生成快照期间其他数据库操作可能短暂等待。临时目录位于数据库同级数据卷，权限为 0700，结束后删除；启动时清理同一数据库上次中断的临时目录，避免使用容器较小的 `/tmp`。数据卷需预留快照与压缩文件所需空间。

当前采用单文件上传，压缩后上限 5 GiB，不支持分卷。任务最长 30 分钟，单次 S3 请求最长 15 分钟。凭据与账号数据在数据库内仍保持原有加密，压缩包本身不额外加密，应限制存储桶访问权限。备份不包含 `FIFTYAPI_MASTER_KEY`、环境变量或外部文件，**必须单独保管原主密钥**。备份记录为本地数据库记录，不扫描其他实例或前缀中的对象；删除与过期清理仅操作记录中属于本系统的文件。每条记录保存当时的加密存储配置，更换当前存储桶后仍可下载和清理旧文件。旧凭据被撤销后，旧备份的访问与清理也会失败，应在撤销前处理旧备份。

每 30 秒尝试清理过期文件，失败保留记录并重试，详情写入「运行日志 → 备份」。停机期间不能清理，启动后继续执行；对于启用版本控制的存储桶，删除当前对象不保证历史版本被物理清除，需在存储侧配置版本生命周期。运行中被中断的任务会在重启后标记失败。上传成功后如果本地结果落库失败，记录仍保留其对象地址；检查数据库并重启后，记录会标记失败，随后可删除或到期清理。

下载备份后，在可信本机解压并使用现有离线恢复脚本恢复到**新目录**，不会覆盖现有数据库：

```sh
mkdir /secure/backup-extracted
tar -xzf fiftyapi-<time>-<id>.tar.gz -C /secure/backup-extracted
python3 scripts/backup-offline.py restore \
  --from /secure/backup-extracted \
  --output /secure/restored-data
```

恢复前停止服务；将恢复得到的数据库作为 `FIFTYAPI_DB_PATH`，使用原 `FIFTYAPI_MASTER_KEY` 再启动。恢复的是快照时刻数据，不包含快照后发生的请求或设置变更。备份本身包含快照时刻的备份计划及记录，恢复后检查存储配置和自动备份开关；不要让原实例和恢复实例同时管理同一套远端备份。

管理接口均要求管理员会话，变更操作还要求 CSRF：

- `GET /api/admin/settings/backup`：脱敏配置、服务时区与下次执行时间。
- `PUT /api/admin/settings/backup`：保存存储与计划配置。
- `POST /api/admin/settings/backup/test`：测试当前提交的配置。
- `GET /api/admin/backups?page=1&page_size=20`：备份记录，单页最多 100 项。
- `POST /api/admin/backups`：后台创建备份；请求体 `{}` 使用默认保留天数，也可提交 `{"retention_days": 14}`，返回 202。
- `GET /api/admin/backups/{id}/download`：下载未过期的已完成备份。
- `DELETE /api/admin/backups/{id}`：删除对应远端文件并保留删除记录。
