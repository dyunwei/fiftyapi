# FiftyAPI 文档索引

简体中文 | [English](en/README.md)

当前公开版本：**0.1.0**。界面右上角可切换简体中文与 English；选择保存在当前浏览器。

## 部署与使用

| 文档 | 内容 |
| --- | --- |
| [deployment.md](deployment.md) | 部署说明：二进制、Docker、HTTPS、环境变量、备份恢复、升级 |
| [release-quickstart.md](release-quickstart.md) | 二进制发布包快速启动（打包时复制为 `QUICKSTART.md`） |
| [clients.md](clients.md) | Claude Code、Codex 等客户端接入 |

## 管理员功能

| 文档 | 内容 |
| --- | --- |
| [sub2api-migration.md](sub2api-migration.md) | 从 sub2api 迁移用户、原 Key 和上游账号 |
| [gateway-upstreams.md](gateway-upstreams.md) | 接入 Sub2Api / FiftyAPI / New API 上游网关、模型映射与定价 |
| [model-protocols.md](model-protocols.md) | 同一模型的多协议（Chat / Messages / Responses）配置 |
| [account-models.md](account-models.md) | 上游账号模型目录与同步 |
| [email-invitations.md](email-invitations.md) | 邮件配置与成员邀请 |
| [request-logs.md](request-logs.md) | 请求日志与结算异常 |
| [automatic-settlement.md](automatic-settlement.md) | 额度自动结算规则 |

## 设计与兼容性

| 文档 | 内容 |
| --- | --- |
| [design.md](design.md) | 整体架构与实现方案 |
| [reliability-redesign.md](reliability-redesign.md) | 账号调度、重试、健康与冷却设计 |
| [compatibility.md](compatibility.md) | 协议兼容范围、验证记录与已知限制 |
| [openai-acceptance.md](openai-acceptance.md) | OpenAI 真实账号验收与复现方法 |

## 开发与开源

| 文档 | 内容 |
| --- | --- |
| [probe.md](probe.md) | 开发探针 |
| [upstream-migration.md](upstream-migration.md) | 从 sub2api 迁移的功能取舍 |
| [sub2api-source.json](sub2api-source.json) | 改编自 sub2api 的源文件及 SHA-256（许可证要求，勿删） |
| [maintaining.md](maintaining.md) | 发布与维护流程 |
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | 贡献指南 |
| [../SECURITY.md](../SECURITY.md) | 安全问题报告 |

