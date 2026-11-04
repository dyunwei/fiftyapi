# 维护与发布

## 版本号

首次公开版本为 **0.1.0**，发布标签为 `v0.1.0`。公开更新日志从此版本开始；之前的内部迭代不作为公开发布历史。

版本号保存在 `internal/buildinfo/VERSION`，必须与 `web/package.json`、`web/package-lock.json` 的 `version` 一致（`scripts/check-source.py` 会检查）。遵循语义化版本：不兼容变更或数据库迁移不可回退时提升次版本（0.x 阶段），修复提升补丁版本。

## 发布一个版本

1. 更新三处版本号，在 `CHANGELOG.md` 顶部写本版变更（尤其数据库迁移、配置变更和升级注意事项）。
2. 本地检查：`make test`、`make build`、`make test-ui`。
3. 提交并推送，等待 GitHub Actions 全部通过。
4. 打标签：`git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`。
5. 在 GitHub 页面 Releases → Draft a new release 选择标签，粘贴 CHANGELOG 内容；如附带二进制，上传 `dist/` 中的二进制、`SHA256SUMS`、许可证和依赖声明。

## 许可证义务（LGPL-3.0）

部分代码改编自 sub2api，必须保留 `LICENSE`、`COPYING.GPL-3`、`THIRD_PARTY_NOTICES.md` 和 `docs/sub2api-source.json`。分发二进制或镜像时，同时提供对应版本的完整源码（GitHub 标签即可满足）。新增依赖后运行 `scripts/build-release.sh` 更新依赖声明。

## 绝不提交

`.local/`、`data/`、`.env`、`*.db*`、主密钥、setup token、OAuth 授权文件、真实 API Key。`.gitignore` 已覆盖常见路径；提交前运行 `python3 scripts/check-source.py`。凭据一旦推送到公开仓库，删除文件无效，必须立即在供应商处撤销/轮换。

## 日常维护

- **Issue**：确认能复现后再处理；要求报告者提供版本号与脱敏日志，提醒不要贴令牌。
- **Pull Request**：CI 必须通过；检查是否引入凭据、是否修改许可证声明；合并前在本地跑一遍。
- **Dependabot**：每周会提交依赖更新 PR，CI 通过即可合并；Go/npm 大版本升级需手动验证。
- **安全报告**：按 `SECURITY.md` 通过私密渠道接收，修复发布后再公开。


## 中英文文案

页面支持 `zh-CN` 和 `en`。中文原文用作消息键，英文位于 `web/src/locales/en.json`；后端固定诊断文案位于 `internal/i18n/en.json`，由 Go 和前端共用。新增文案使用 `t()`；含变量的完整句子使用 `{0}`、`{1}` 占位符，避免拼接词序。不要翻译用户输入、模型 ID、凭据或模型回复。API 错误根据 `Accept-Language` 选择语言；未指定时保留中文。邀请与测试邮件同时包含中英文。

修改文案时同时更新英文词典，运行单元测试及中英文浏览器测试，并重新构建嵌入式前端。英文用户文档入口为 [English docs](en/README.md)。
