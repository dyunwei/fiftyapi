#!/usr/bin/env python3
"""Create a FiftyAPI migration bundle using PostgreSQL's psql client.

Use --docker for the standard sub2api PostgreSQL container (no host psql or
manual password needed), or PGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSFILE/PGSERVICE
for a direct connection. Never writes to sub2api or prints credentials.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

SQL = """
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT json_build_object(
 'users', COALESCE((SELECT json_agg(to_jsonb(u)) FROM users u WHERE to_jsonb(u)->>'deleted_at' IS NULL), '[]'::json),
 'api_keys', COALESCE((SELECT json_agg(to_jsonb(k)) FROM api_keys k WHERE to_jsonb(k)->>'deleted_at' IS NULL), '[]'::json),
 'accounts', COALESCE((SELECT json_agg(to_jsonb(a)) FROM accounts a WHERE to_jsonb(a)->>'deleted_at' IS NULL), '[]'::json),
 'proxies', COALESCE((SELECT json_agg(to_jsonb(p)) FROM proxies p WHERE to_jsonb(p)->>'deleted_at' IS NULL), '[]'::json)
);
COMMIT;
"""
# This fixed shell program runs inside the PostgreSQL container. Credentials are
# resolved there, never returned by docker inspect or interpolated into arguments.
DOCKER_PSQL = r'''set -eu
if [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then
    PGPASSWORD="$(cat "$POSTGRES_PASSWORD_FILE")"
    export PGPASSWORD
elif [ "${POSTGRES_PASSWORD+x}" = x ]; then
    PGPASSWORD="$POSTGRES_PASSWORD"
    export PGPASSWORD
fi
exec psql -X -q -A -t -w -v ON_ERROR_STOP=1 \
    -h 127.0.0.1 -p "${PGPORT:-5432}" \
    -U "${1:-${POSTGRES_USER:-postgres}}" \
    -d "${2:-${POSTGRES_DB:-${POSTGRES_USER:-postgres}}}" -f -
'''


FIELDS = {
 'users': ('id', 'email', 'username', 'password_hash', 'status', 'totp_enabled'),
 'api_keys': ('id', 'user_id', 'name', 'key', 'status', 'group_id', 'ip_whitelist', 'ip_blacklist', 'quota', 'rate_limit_5h', 'rate_limit_1d', 'rate_limit_7d', 'expires_at'),
 'accounts': ('id', 'name', 'platform', 'type', 'status', 'credentials', 'proxy_id', 'concurrency', 'priority', 'expires_at'),
 'proxies': ('id', 'protocol', 'host', 'port', 'username', 'password', 'status', 'expires_at'),
}


def bundle(raw, source):
    result = {'type': 'fiftyapi-sub2api-migration', 'version': 1, 'source': source}
    # Explicit projection excludes TOTP secrets, sessions, billing and unrelated data.
    for kind, fields in FIELDS.items():
        if not isinstance(raw.get(kind), list):
            raise ValueError('missing source table')
        result[kind] = [{k: row[k] for k in fields if k in row} for row in raw[kind]]
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, help='Stable unique source identifier; reuse for retries, use a different value for another database.')
    parser.add_argument('--output', required=True, type=Path, help='New private JSON file; existing files are never overwritten.')
    parser.add_argument('--docker', '--docker-container', dest='docker_container', nargs='?', const='sub2api-postgres', metavar='CONTAINER', help='Run psql in this PostgreSQL container; defaults to sub2api-postgres when the name is omitted. Reuses container credentials.')
    parser.add_argument('--db-user', help='Override the database user (otherwise use container POSTGRES_USER or local PG settings).')
    parser.add_argument('--database', help='Override the database name (otherwise use container POSTGRES_DB or local PG settings).')
    args = parser.parse_args()
    if not args.source.strip() or len(args.source.encode()) > 128 or any(ord(c) < 32 for c in args.source):
        parser.error('source must be a nonempty identifier of at most 128 UTF-8 bytes')
    if args.output.exists():
        parser.error('output already exists; choose a new path')
    try:
        if args.docker_container is not None:
            command = ['docker', 'exec', '-i', '--', args.docker_container, 'sh', '-c', DOCKER_PSQL,
                       'fiftyapi-export', args.db_user or '', args.database or '']
            process = subprocess.run(command, input=SQL.encode(), capture_output=True, check=True)
        else:
            command = ['psql', '-X', '-q', '-A', '-t', '-w', '-v', 'ON_ERROR_STOP=1']
            if args.db_user:
                command += ['-U', args.db_user]
            if args.database:
                command += ['-d', args.database]
            process = subprocess.run(command + ['-c', SQL], capture_output=True, check=True)
        payload = bundle(json.loads(process.stdout), args.source)
        encoded = json.dumps(payload, ensure_ascii=False, separators=(',', ':')).encode()
        limits = {'users': 5000, 'api_keys': 20000, 'accounts': 1000, 'proxies': 1000}
        if len(encoded) > 31 << 20 or any(len(payload[k]) > v for k, v in limits.items()):
            raise ValueError('bundle too large')
        fd = os.open(args.output, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'wb') as output:
            output.write(encoded)
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, KeyError):
        print('Export failed. Check Docker access and the PostgreSQL container name/credentials, or local psql connection settings; also check source schema, output path and size limits. No source data was changed.', file=sys.stderr)
        return 1
    print('Migration file created with mode 0600. It contains password hashes, API keys and upstream credentials; transfer privately and remove it after migration.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
