# 一键配置客户端

在「使用密钥」中选择客户端、系统和模型，复制「一键配置」命令。需要 Python 3.11+（macOS/Linux 使用 `python3.11`，Windows 使用 Python launcher `py -3.11`）。助手由当前站点 `/setup.py` 提供，可以先下载审阅。无需管理员权限，不安装客户端。

VS Code 用户应在使用 Codex 的窗口中打开新终端执行。Remote SSH、WSL、Dev Container 会配置对应环境中的用户；不要使用 sudo。容器重建后保留配置需要持久化用户目录。助手识别 `CODEX_HOME`、`CLAUDE_CONFIG_DIR`。完成后重新加载窗口并新建会话。IDE 插件可能自带客户端，终端中的 CLI 版本不能代表插件版本。

输入密钥时终端不回显。助手仅请求 `/v1/models` 验证密钥、模型和协议，不发起推理。拒绝重定向，避免将密钥转发到其他地址。公网使用 HTTPS。Codex 使用 Responses，Claude Code 使用 Messages；WebSocket 仅在明确勾选时启用。

Codex 使用 `experimental_bearer_token` 持久保存密钥，以便从桌面启动的 VS Code 也可读取，不依赖 shell 环境变量，不修改 `auth.json`。该字段官方不推荐用于一般密钥管理；本助手为插件接入采用本机明文存储。Claude Code 合并用户 settings.json 的 env，并将三个模型别名统一指向所选模型；已继承的鉴权、地址或云供应商环境变量需要用户先清除。项目或组织设置仍可能覆盖用户配置。Claude Code VS Code 插件尚未验收。

配置和恢复会保留解析后的其他设置，但 TOML 注释、格式会规范化。每次修改前保存完整原文件，恢复只处理受管理字段。检测到这些字段被其他工具修改时停止，需手动解决；不会强行覆盖。原文件不存在且恢复后没有其他设置时删除文件。

页面默认展示一键配置，手动编辑文件的步骤收在「手动配置（可选）」中。

「恢复接入前的配置」指恢复首次通过助手接入 FiftyAPI 前的模型、服务地址与鉴权设置，保留之后修改的其他设置。例如此前使用 Codex 官方服务，恢复后沿用原来的接入方式。这不等于重置为客户端出厂默认设置。每次修改前另有完整文件备份，但目前不支持选择某个时间点自动恢复；不要将这两种恢复方式混淆。页面会按当前客户端与系统显示可直接复制的恢复命令。

助手保存于 `~/.fiftyapi-helper/helper.py`，使用示例：

```sh
python3.11 ~/.fiftyapi-helper/helper.py status codex
python3.11 ~/.fiftyapi-helper/helper.py configure codex
python3.11 ~/.fiftyapi-helper/helper.py restore codex
python3.11 ~/.fiftyapi-helper/helper.py use codex
python3.11 ~/.fiftyapi-helper/helper.py uninstall codex
```

Claude Code 将 `codex` 换成 `claude`。Windows 用 `py -3.11 "$HOME\.fiftyapi-helper\helper.py" restore codex` 等对应命令。configure 可重新选择站点、密钥和模型，恢复保留原接入字段。每次操作只处理一个客户端。

配置、恢复状态、备份均可能含密钥，只保存在本机，不要提交或分享。Unix 新文件仅用户可访问；Windows 应使用私有用户目录并检查 ACL。卸载会恢复受管理配置，删除对应恢复状态；全部客户端卸载后移除助手，但保留 `~/.fiftyapi-helper/backups/`，备份需用户自行清理。如果进程异常退出，确认没有助手运行后可移除 `operation.lock` 目录重试。

## 验证范围

2026-11-14：Python 3.11 测试覆盖模拟模型目录（Responses / Messages）、重复配置、恢复/重新启用、冲突拒绝、其他字段保留及符号链接拒绝。本机 Codex CLI **0.169.0-alpha.11.1** 使用隔离 CODEX_HOME，通过 `features list` 解析含模拟模型的持久鉴权配置；未发送推理请求。没有对 Windows、macOS、Remote SSH、VS Code 插件或 Claude Code 原生程序做端到端验收，也没有验证 DeepSeek/Kimi 等真实模型兼容性。模型实际能力以现有[兼容性记录](compatibility.md)为准。
