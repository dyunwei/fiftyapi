#!/usr/bin/env bash
# Export sub2api Docker data for the FiftyAPI setup wizard. No source writes,
# service stops, dependency installs, credential output or data uploads.
set -Eeuo pipefail
umask 077

fail() { printf '\n错误 / Error: %s\n' "$*" >&2; exit 1; }
container=""
output=""
while (($#)); do
 case "$1" in
  --container) (($#>=2)) || fail '--container needs a name'; container="$2"; shift 2 ;;
  --output) (($#>=2)) || fail '--output needs a new file path'; output="$2"; shift 2 ;;
  -h|--help) printf 'Usage: bash migrate-sub2api.sh [--container POSTGRES_CONTAINER] [--output NEW_FILE]\n'; exit 0 ;;
  *) fail "Unknown argument: $1" ;;
 esac
done
printf '\nFiftyAPI · sub2api Docker 迁移 / Docker migration\n'
printf '此工具只读导出数据，不停止旧服务，不上传数据，也不安装软件。\nRead-only export; no service stops, uploads or dependency installation.\n\n'
printf '[1/4] 检查 Docker / Checking Docker…\n'
command -v docker >/dev/null || fail '未找到 Docker，请在旧站 Docker 宿主机执行 / Run on the source Docker host.'
command -v sha256sum >/dev/null || fail 'sha256sum is required on the Docker host.'
engine_id=$(docker info --format '{{.ID}}' 2>/dev/null) || fail '无法访问 Docker，请使用 sudo 或检查 Docker 服务 / Cannot access Docker.'
[[ -n "$engine_id" ]] || fail 'Docker engine identity is unavailable.'
printf '[2/4] 查找 PostgreSQL 容器 / Finding PostgreSQL container…\n'
if [[ -z "$container" ]]; then
 listing=$(docker ps --format '{{.Names}}|{{.Image}}|{{.Label "com.docker.compose.service"}}' 2>/dev/null) || fail 'Unable to list running containers.'
 candidates=()
 while IFS='|' read -r name image service; do
  if [[ "$image" == postgres:* || "$image" == postgres || "$image" == */postgres:* || "$service" == postgres ]]; then candidates+=("$name"); fi
 done <<< "$listing"
 if ((${#candidates[@]}==1)); then
  container=${candidates[0]}
 elif ((${#candidates[@]}==0)); then
  fail '没有找到 PostgreSQL 容器。请保持数据库运行，或添加 --container 容器名 / No running PostgreSQL container found.'
 else
  printf '发现多个数据库，请选择 / Select the source database:\n'
  for i in "${!candidates[@]}"; do printf '  %s) %s\n' "$((i+1))" "${candidates[$i]}"; done
  if ! { read -r -p '输入序号 / Number: ' choice < /dev/tty; } 2>/dev/null; then
   fail '多个数据库且无交互终端，请使用 --container 明确指定 / Specify --container when running without a terminal.'
  fi
  [[ "$choice" =~ ^[0-9]+$ && ${#choice} -le 3 ]] || fail 'Invalid selection.'
  number=$((10#$choice)); ((number>=1 && number<=${#candidates[@]})) || fail 'Invalid selection.'
  container=${candidates[$((number-1))]}
 fi
fi
[[ "$container" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || fail 'Invalid container name.'
printf '数据库容器 / Database container: %s\n' "$container"
source_id=$(printf '%s\n%s' "$engine_id" "$container" | sha256sum)
source_id=${source_id%% *}
created=0
cleanup() { if ((created)); then rm -f -- "$output"; fi; }
trap cleanup EXIT
if [[ -z "$output" ]]; then
 output=$(mktemp /tmp/fiftyapi-sub2api-XXXXXX.json) || fail 'Cannot create private output file.'
else
 (set -o noclobber; : > "$output") 2>/dev/null || fail '输出文件已存在或不可写 / Output must be a new writable file.'
fi
created=1
chmod 600 -- "$output"
printf '[3/4] 只读导出用户、Key、账号和代理 / Exporting a consistent snapshot…\n'
if ! docker exec -i -- "$container" sh -c '
set -eu
if [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then
 PGPASSWORD="$(cat "$POSTGRES_PASSWORD_FILE")"; export PGPASSWORD
elif [ "${POSTGRES_PASSWORD+x}" = x ]; then
 PGPASSWORD="$POSTGRES_PASSWORD"; export PGPASSWORD
fi
exec psql -X -q -A -t -w -v ON_ERROR_STOP=1 -v source="$1" \
 -h 127.0.0.1 -p "${PGPORT:-5432}" \
 -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}" -f -
' fiftyapi-migration "$source_id" > "$output" 2>/dev/null <<'FIFTYAPI_SQL'
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
DO $check$
BEGIN
 IF (SELECT count(*) FROM users r WHERE to_jsonb(r)->>'deleted_at' IS NULL)>5000
 OR (SELECT count(*) FROM api_keys r WHERE to_jsonb(r)->>'deleted_at' IS NULL)>20000
 OR (SELECT count(*) FROM accounts r WHERE to_jsonb(r)->>'deleted_at' IS NULL)>1000
 OR (SELECT count(*) FROM proxies r WHERE to_jsonb(r)->>'deleted_at' IS NULL)>1000 THEN
  RAISE EXCEPTION 'Migration exceeds supported record limits';
 END IF;
END;
$check$;
SELECT jsonb_build_object(
 'type', 'fiftyapi-sub2api-migration', 'version', 1,
 'source', 'docker:' || :'source' || ':' || md5(current_database()),
 'users', COALESCE((SELECT jsonb_agg((SELECT jsonb_object_agg(key,value) FROM jsonb_each(to_jsonb(r)) WHERE key IN ('id','email','username','password_hash','status','totp_enabled'))) FROM users r WHERE to_jsonb(r)->>'deleted_at' IS NULL), '[]'::jsonb),
 'api_keys', COALESCE((SELECT jsonb_agg((SELECT jsonb_object_agg(key,value) FROM jsonb_each(to_jsonb(r)) WHERE key IN ('id','user_id','name','key','status','group_id','ip_whitelist','ip_blacklist','quota','rate_limit_5h','rate_limit_1d','rate_limit_7d','expires_at'))) FROM api_keys r WHERE to_jsonb(r)->>'deleted_at' IS NULL), '[]'::jsonb),
 'accounts', COALESCE((SELECT jsonb_agg((SELECT jsonb_object_agg(key,value) FROM jsonb_each(to_jsonb(r)) WHERE key IN ('id','name','platform','type','status','credentials','proxy_id','concurrency','priority','expires_at'))) FROM accounts r WHERE to_jsonb(r)->>'deleted_at' IS NULL), '[]'::jsonb),
 'proxies', COALESCE((SELECT jsonb_agg((SELECT jsonb_object_agg(key,value) FROM jsonb_each(to_jsonb(r)) WHERE key IN ('id','protocol','host','port','username','password','status','expires_at'))) FROM proxies r WHERE to_jsonb(r)->>'deleted_at' IS NULL), '[]'::jsonb)
);
COMMIT;
FIFTYAPI_SQL
then
 fail '导出失败。请确认选中了旧站 PostgreSQL 容器，数据库配置有效且表结构兼容。未更改源数据。 / Export failed; source data was not changed.'
fi
bytes=$(wc -c < "$output")
((bytes>0 && bytes<=31*1024*1024)) || fail '导出为空或超过 31 MiB / Empty export or file exceeds 31 MiB.'
if [[ -n "${SUDO_UID:-}" && -n "${SUDO_GID:-}" && "$SUDO_UID" =~ ^[0-9]+$ && "$SUDO_GID" =~ ^[0-9]+$ ]]; then
 chown "$SUDO_UID:$SUDO_GID" -- "$output" || fail 'Unable to assign the file to the invoking user.'
fi
created=0
printf '[4/4] 导出完成 / Export complete\n\n'
printf '迁移文件 / Migration file: %s\n' "$output"
printf '文件大小 / Size: %s bytes\n' "$bytes"
printf '\n下一步：将该文件下载到浏览器所在电脑，回到 FiftyAPI 网页点击「已导出，选择迁移文件」。\n'
printf 'Next: download this file to your browser computer, return to FiftyAPI and choose the migration file.\n'
printf '可使用已有 SSH/SFTP 工具下载；如果浏览器就在这台电脑，直接选择上述文件。\n'
printf 'Use your SSH/SFTP client, or select the file directly if the browser is on this host.\n'
printf '文件含完整密钥和令牌，迁移完成后删除。正式切换前停止旧站应用，保持数据库运行，再导出最终文件。\n'
printf 'Delete the private file after migration. Before final cutover, stop the old application, keep PostgreSQL running and export the final snapshot.\n'
