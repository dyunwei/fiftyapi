#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -f .env ]; then
  set -a
  . ./.env
  set +a
fi
umask 077
mkdir -p .local data
fiftyapi_local_dir="${FIFTYAPI_DATA_DIR-data}"
if [ -z "$fiftyapi_local_dir" ]; then fiftyapi_local_dir=data; fi
fiftyapi_local_db="${FIFTYAPI_DB_PATH-}"
if [ -z "$fiftyapi_local_db" ]; then
  fiftyapi_local_db="$fiftyapi_local_dir/fiftyapi.db"
fi
if [ ! -s .local/server-master.key ]; then
  if [ -e "$fiftyapi_local_db" ]; then
    printf '%s\n' 'Existing database found without its master key. Restore the original key before starting.' >&2
    exit 1
  fi
  openssl rand -base64 32 > .local/server-master.key
fi
if [ ! -s .local/setup-token ]; then openssl rand -hex 4 > .local/setup-token; fi
export FIFTYAPI_MASTER_KEY="$(cat .local/server-master.key)"
export FIFTYAPI_SETUP_TOKEN="$(cat .local/setup-token)"
export FIFTYAPI_DB_PATH="$fiftyapi_local_db"
export FIFTYAPI_LISTEN="${FIFTYAPI_LISTEN-127.0.0.1:8080}"
printf '%s\n' 'Setup token file: .local/setup-token' 'Keep .local/server-master.key with your backups.'
printf 'Setup token: %s\n' "$FIFTYAPI_SETUP_TOKEN"
exec ./bin/fiftyapi serve
