# FiftyAPI 文档

简体中文 | [English](en/README.md)

- [部署与运维](deployment.md) · Docker、二进制、HTTPS、升级和恢复
- [接入客户端](clients.md) · Claude Code、Codex、Cherry Studio 和协议选择
- [迁移旧站](sub2api-migration.md) · 在首次初始化时从 sub2api 导入
- [管理账号与额度](#管理账号与额度) · 上游、定价、成员和请求日志
- [查看兼容性](compatibility.md) · 已验证的客户端、协议和限制

## 管理账号与额度

| 要做的事 | 指南 |
| --- | --- |
| 接入上游、同步模型 | [OAuth 账号与模型目录](account-models.md) · [API 网关](gateway-upstreams.md) |
| 配置定价、访问权限和客户端默认值 | [账号共享设置](low-maintenance.md) |
| 邀请成员、排查扣费 | [邮件邀请](email-invitations.md) · [请求日志](request-logs.md) |

<details>
<summary>其他操作与开发资料</summary>

- **其他操作** · [发布包快速启动](release-quickstart.md)
- **开发与发布** · [参与贡献](../CONTRIBUTING.md) · [维护与发布](maintaining.md) · [更新日志](../CHANGELOG.md) · [安全问题报告](../SECURITY.md)
- **设计** · [架构](design.md) · [调度与重试](reliability-redesign.md) · [sub2api 功能取舍](upstream-migration.md)
- **验证记录** · [兼容性历史归档](compatibility-history.md) · [开发探针](probe.md) · [OpenAI 验收](openai-acceptance.md) · [SQLite 压测](sqlite-load-test.md)
- **第三方来源** · [第三方声明](../THIRD_PARTY_NOTICES.md) · [sub2api 源码指纹](sub2api-source.json)

</details>
