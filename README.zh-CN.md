# FiftyAPI

[English](README.md) | 简体中文

面向几十人小团队的 AI 上游账号与金额额度网关。一个 Go 服务、一套嵌入式 Vue 3 页面、一个 SQLite 数据库，无 Redis。Fifty 表示面向小团队的定位，不是 50 人的硬性人数限制。

- Claude Code、OpenAI Codex、Antigravity OAuth 授权、重新授权、令牌刷新和模型目录同步。
- Sub2Api、FiftyAPI、New API 网关：API Key 接入、Messages/Responses、模型发现与别名映射。
- 首次访问设置管理员；支持邮箱、真实姓名和密码自助注册，初始额度为 0；管理员也可通过邮件邀请成员。
- 每人共享美元额度：5 小时和 7 天窗口，覆盖所有 API Key 与供应商。
- 用户查看自己的请求日志；管理员管理全部日志、模型价格、日志中的结算异常和操作记录。
- 参考 Sub2Api 的白色侧栏、青绿导航和卡片界面，适配手机。
- Go 单文件或 Docker 部署，适用于单实例、本地持久化磁盘。

![管理控制台](docs/screenshots/admin-dashboard.png)

当前版本：**0.1.0**，首个公开预览版。支持简体中文与 English，可在页面右上角切换，默认根据浏览器语言选择并在本机记住偏好。

支持原生 Chat Completions、Messages 和 Responses；同一 API 上游模型可声明多种协议并共用 ID 和价格。账号并发在「调度与健康」设置。请求日志包含结算异常，系统设置支持邮件和 S3 / Cloudflare R2 备份。[多协议配置](docs/model-protocols.md) · [日志与结算](docs/request-logs.md) · [邮件配置](docs/email-invitations.md)。

上游网关接入、模型映射、定价与兼容边界见 [网关接入说明](docs/gateway-upstreams.md)。

## Docker 安装部署

镜像已发布到 [Docker Hub](https://hub.docker.com/r/dyunwei/fiftyapi)，无需下载源码或安装 Go/Node，直接运行：

```sh
docker run -d --name fiftyapi --restart unless-stopped --stop-timeout 60 \
  -p 28080:8080 -v fiftyapi-data:/data dyunwei/fiftyapi
```

打开 http://localhost:28080；在其他机器访问时使用 `http://服务器IP:28080`。容器启动后，读取初始化口令：

```sh
docker exec fiftyapi cat /data/setup-token
```

将口令填入初始化页面并创建管理员账号。新安装无需预先配置环境变量：服务会自动生成随机主密钥和部署口令，保存到 `fiftyapi-data` 数据卷中，重建容器后继续复用。请保留此数据卷，并单独备份 `/data/master.key`；数据库备份压缩包不包含主密钥。

`28080:8080` 左侧是宿主机端口，可按需更换 `28080`。支持内网 HTTP；发送邮件邀请时，在邮件设置中填写收件人可访问的 HTTP 或 HTTPS 地址，例如 `http://192.168.1.10:28080`。

Compose、HTTPS、升级与恢复见 [部署说明](docs/deployment.md)。

## 本机启动

需要 Go 1.25+；仓库已包含前端产物，直接构建无需 Node。修改前端需 Node 24+。

```sh
go build -buildvcs=false -o bin/fiftyapi ./cmd/fiftyapi
./scripts/start-local.sh
```

打开 http://127.0.0.1:8080 。首次初始化的部署口令保存在 `.local/setup-token`，在本机读取后填入页面。后续启动会复用密钥和数据库。管理员登录后：添加上游账号 → 完成 OAuth → 验证目录 → 核对模型价格 → 创建成员并分配额度 → 成员创建 API Key。

邮件邀请配置见 [邮件邀请](docs/email-invitations.md)。管理员和普通用户的登录密码最少 8 字节。

全部文档见 [文档索引](docs/README.md)。Docker、HTTPS、备份恢复见 [部署说明](docs/deployment.md)。Claude Code / Codex 配置见 [客户端接入](docs/clients.md)。开发探针见 [probe.md](docs/probe.md)。

## 团队贡献账号

管理员可在「上游账号 → 贡献链接」选择供应商并生成临时链接，默认 24 小时有效，可提前停用。同一链接支持多位账号所有者无需登录、自行导入授权 JSON（每次最多 20 个文件，总大小 1 MiB）。完整链接仅在创建后显示，请及时复制保存。

贡献链接管理采用紧凑分页，每页 5 条，并可按有效、已停用、已过期筛选。公开贡献页面提供 Windows、macOS、Linux 的文件查找步骤，按供应商区分 Codex auth.json、Claude Code .credentials.json 和导出的授权 JSON；macOS 钥匙串、自定义目录等情况另有提示。

贡献页面只提供文件导入和账号名称，不允许设置并发、代理或其他高级参数。账号加密保存后默认停用，管理员配置完成并手动启用后，再验证并同步模型。账号列表显示贡献链接编号与提交时间；链接过期或停用不影响已提交的账号。导入只检查授权格式，实际有效性及模型权限由管理员后续验证。

## 验证范围

Antigravity `gemini-3.5-flash-lite` 已通过真实刷新、目录、网关 SSE、工具调用与结果回传，额度和日志核对一致。OpenAI 已导入账号的真实目录、文本、流式工具往返、取消与额度核对通过；原生 Codex 固定短提示与 WebSocket 已验收，真实 OAuth 刷新、新身份 compact 和长时间工具工作流尚未验收。Claude 缺少真实账号，保留模拟验证。模型出现在目录中不代表账号拥有调用权限。

目前支持 Messages、Responses HTTP/SSE、原生 Responses WebSocket 和 compact 接口；compact 的新身份真实可用性仍待验收。Chat Completions 只直通声明支持 Chat 的上游 API；Claude/Antigravity 继续使用 Messages，Codex OAuth 使用 Responses；未实现图片/音频生成、付费套餐、支付或复杂分组调度。完整验证记录与已知限制见 [兼容性](docs/compatibility.md)。

## 开发与构建

```sh
make test             # Go race、vet、表单逻辑、源码检查
make test-load        # 50 用户模拟负载（不调用真实供应商）
make build            # npm ci、前端构建、Go 单文件
make test-ui          # 需先安装 Playwright Chromium
./scripts/build-release.sh  # dist/ 中的本机架构二进制、许可证和依赖声明
```

前端浏览器测试首次需 `cd web && npx playwright install chromium`。测试使用临时数据库和模拟账号；不会执行真实供应商调用。

## 来源与开源

账号协议和部分 Antigravity 转换代码参考或改编自 [sub2api](https://github.com/Wei-Shaw/sub2api)。仓库采用 LGPL-3.0，保留 [LICENSE](LICENSE)、[COPYING.GPL-3](COPYING.GPL-3)、[第三方声明](THIRD_PARTY_NOTICES.md) 及 [源码指纹](docs/sub2api-source.json)。功能取舍见 [迁移清单](docs/upstream-migration.md)。

发布与维护流程见 [维护指南](docs/maintaining.md)；运行数据、主密钥及用户令牌不属于发布内容。
