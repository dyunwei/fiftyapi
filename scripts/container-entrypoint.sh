#!/bin/sh
set -eu
# Set the container default only when neither spelling was configured.
if [ "${FIFTYAPI_LISTEN+x}" != x ]; then
  export FIFTYAPI_LISTEN=0.0.0.0:8080
fi
exec /app/fiftyapi "$@"
