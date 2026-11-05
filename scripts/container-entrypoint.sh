#!/bin/sh
set -eu
# Set the container default only when neither spelling was configured.
if [ "${FIFTYAPI_LISTEN+x}" != x ]; then
  export FIFTYAPI_LISTEN=0.0.0.0:8080
fi
if [ "$(id -u)" = 0 ]; then
  data_dir=${FIFTYAPI_DATA_DIR:-/data}
  mkdir -p "$data_dir"
  # Fix existing databases and private key files too. Do not follow symlinks
  # or rewrite ownership of files that already belong to the service user.
  find "$data_dir" -xdev \( ! -user app -o ! -group app \) -exec chown -h app:app {} +
  exec su-exec app:app /app/fiftyapi "$@"
fi
exec /app/fiftyapi "$@"
