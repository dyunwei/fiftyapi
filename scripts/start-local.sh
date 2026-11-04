#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
umask 077
mkdir -p .local data
fiftyapi_local_dir="${FIFTYAPI_DATA_DIR-${ZTOKENMGR_DATA_DIR:-data}}"
if [ -z "$fiftyapi_local_dir" ]; then fiftyapi_local_dir=data; fi
fiftyapi_local_db="${FIFTYAPI_DB_PATH-${ZTOKENMGR_DB_PATH:-}}"
if [ -z "$fiftyapi_local_db" ]; then
  if [ -e "$fiftyapi_local_dir/fiftyapi.db" ] && [ -e "$fiftyapi_local_dir/ztokenmgr.db" ]; then
    printf '%s\n' 'Both database names exist. Set FIFTYAPI_DB_PATH explicitly.' >&2
    exit 1
  fi
  fiftyapi_local_db="$fiftyapi_local_dir/fiftyapi.db"
  if [ -e "$fiftyapi_local_dir/ztokenmgr.db" ]; then fiftyapi_local_db="$fiftyapi_local_dir/ztokenmgr.db"; fi
fi
if [ ! -s .local/server-master.key ]; then
  if [ -e "$fiftyapi_local_db" ]; then
    printf '%s\n' 'Existing database found without its master key. Restore the original key before starting.' >&2
    exit 1
  fi
  openssl rand -base64 32 > .local/server-master.key
fi
if [ ! -s .local/setup-token ]; then openssl rand -hex 32 > .local/setup-token; fi
export FIFTYAPI_MASTER_KEY="$(cat .local/server-master.key)"
export FIFTYAPI_SETUP_TOKEN="$(cat .local/setup-token)"
export FIFTYAPI_DB_PATH="$fiftyapi_local_db"
export FIFTYAPI_LISTEN="${FIFTYAPI_LISTEN-${ZTOKENMGR_LISTEN:-127.0.0.1:8080}}"
printf '%s\n' 'Setup token file: .local/setup-token' 'Keep .local/server-master.key with your backups.'
exec ./bin/fiftyapi serve
