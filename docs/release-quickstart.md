# 二进制发布包启动

简体中文 | [English](en/release-quickstart.md)

此包包含当前构建机操作系统/架构的 `fiftyapi`，页面已嵌入。首次启动，在本机目录执行：

新安装也可不配置 `FIFTYAPI_MASTER_KEY` 和 `FIFTYAPI_SETUP_TOKEN`，直接执行 `./fiftyapi serve`。程序在数据库所在目录自动保存 `master.key` 与 `setup-token`；默认路径为 `data/`。读取 `data/setup-token` 完成初始化，妥善备份 `data/master.key`。已有数据库须恢复原主密钥。以下是可选的手动配置方式：

```sh
umask 077
mkdir -p secrets data
# 仅在首次创建，恢复旧数据库时必须使用原主密钥
openssl rand -base64 32 > secrets/master.key
openssl rand -hex 32 > secrets/setup-token
export FIFTYAPI_MASTER_KEY="$(cat secrets/master.key)"
export FIFTYAPI_SETUP_TOKEN="$(cat secrets/setup-token)"
export FIFTYAPI_DB_PATH=data/fiftyapi.db
export FIFTYAPI_LISTEN=127.0.0.1:8080
./fiftyapi serve
```

打开 http://127.0.0.1:8080，读取 `secrets/setup-token` 的值填入网页，设置管理员账号。后续启动只重新 export 并运行，**不要重新执行随机密钥生成命令覆盖旧密钥**。

部署、备份及 HTTPS 配置见 `docs/deployment.md`，客户端见 `docs/clients.md`，真实验证范围见 `docs/compatibility.md`。README 面向源码仓库；构建源码时再使用其中的 make 命令。

`SHA256SUMS` 用于检查本包二进制。保留本包 LICENSE、COPYING.GPL-3、THIRD_PARTY_NOTICES.md 和 licenses/；再分发时提供匹配版本的完整源码及构建材料。
