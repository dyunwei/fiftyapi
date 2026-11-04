#!/usr/bin/env python3
"""Run the local probe with the reference checkout's paired OAuth parameters.

This development helper reads platform client configuration, never user tokens.
No secret is printed or persisted. It is not a production configuration source.
"""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path,
                        default=Path(__file__).resolve().parents[2] / "sub2api")
    parser.add_argument("--onboard", action="store_true",
                        help="缺少项目时，允许按上游默认 tier 初始化 Antigravity 账号")
    parser.add_argument("--save-session", action="store_true",
                        help="在 .local/ 中保存加密会话和独立密钥，下次复用授权")
    parser.add_argument("--model", help="执行文本/SSE 推理验证的明确模型名（消耗上游额度）")
    parser.add_argument("--tools", action="store_true", help="额外执行无副作用工具闭环验证")
    parser.add_argument("--no-login", action="store_true", help="没有会话时停止，不发起新授权")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    try:
        source = (args.reference / "backend/internal/pkg/antigravity/oauth.go").read_text()
        config = json.loads((root / "examples/probe/antigravity.json").read_text())
        client_id = re.search(r'\bClientID\s*=\s*"([^"\n]+)"', source)
        secret = re.search(r'\bvar\s+defaultClientSecret\s*=\s*"([^"\n]+)"', source)
        version = re.search(r'\bDefaultUserAgentVersion\s*=\s*"(\d+\.\d+\.\d+)"', source)
        if not client_id or not secret or not version:
            raise ValueError("参考源码中未找到预期格式的客户端配置")
        if config.get("client_id") != client_id.group(1):
            raise ValueError("示例 client ID 与参考源码不一致，已停止")
        if config.get("client_secret_env") != "ANTIGRAVITY_OAUTH_CLIENT_SECRET":
            raise ValueError("示例 secret 环境变量名不匹配，已停止")
        identity = None
        if args.model:
            transformer = (args.reference / "backend/internal/pkg/antigravity/request_transformer.go").read_text()
            identity = re.search(r'const antigravityIdentity\s*=\s*`([^`]+)`', transformer)
            if not identity:
                raise ValueError("未找到参考身份提示配置")
    except (OSError, ValueError):
        print("无法读取或匹配参考客户端配置；请核对 --reference 路径和示例配置。", file=sys.stderr)
        return 1

    env = os.environ.copy()
    # Deliberately replace a stale/manually entered value for this child only.
    env["ANTIGRAVITY_OAUTH_CLIENT_SECRET"] = secret.group(1)
    if identity:
        env["ANTIGRAVITY_IDENTITY_PROMPT"] = identity.group(1)
    if not env.get("ANTIGRAVITY_USER_AGENT_VERSION"):
        env["ANTIGRAVITY_USER_AGENT_VERSION"] = version.group(1)
    print("本次使用本地 sub2api 源码中的配套客户端参数，覆盖子进程的 secret 环境变量。", flush=True)
    print("参数不落盘、不打印；这不代表上游仍接受该客户端配置。", flush=True)
    try:
        command = ["go", "run", "./cmd/probe", "-config", "examples/probe/antigravity.json"]
        if args.onboard:
            command.append("-onboard")
        if args.save_session:
            command.extend(["-session-file", ".local/antigravity.session", "-key-file", ".local/probe.key"])
        if args.model:
            command.extend(["-model", args.model])
        if args.tools:
            command.append("-tools")
        if args.no_login:
            command.append("-no-login")
        result = subprocess.run(
            command,
            cwd=root, env=env, check=False,
        )
        return result.returncode
    except OSError:
        print("无法启动 Go 探针，请检查 Go 是否安装。", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    sys.exit(main())
