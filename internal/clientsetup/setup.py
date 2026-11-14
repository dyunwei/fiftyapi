#!/usr/bin/env python3
"""FiftyAPI local client configuration helper. Python 3.11+, standard library only."""
import argparse
import copy
import datetime
import getpass
import json
import math
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
try:
    import tomllib
except ImportError:
    sys.exit("Python 3.11+ required. Try python3.11 or install a current Python version.")
import urllib.parse
import urllib.request
import warnings

ROOT = Path.home() / '.fiftyapi-helper'


def read(path):
    if path.is_symlink():
        raise ValueError('Refusing symbolic link: ' + str(path))
    return path.read_bytes() if path.exists() else None


def atomic(path, data):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    read(path)
    fd, name = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def value(v):
    if isinstance(v, str):
        return json.dumps(v, ensure_ascii=False)
    if isinstance(v, bool):
        return str(v).lower()
    if isinstance(v, (datetime.datetime, datetime.date, datetime.time)):
        return v.isoformat()
    if isinstance(v, dict):
        return '{ ' + ', '.join(value(k) + ' = ' + value(x) for k, x in v.items()) + ' }'
    if isinstance(v, list):
        return '[' + ', '.join(value(x) for x in v) + ']'
    if isinstance(v, float) and math.isnan(v):
        return 'nan'
    return str(v)


def decode(client, raw):
    return (tomllib.loads(raw.decode('utf-8')) if client == 'codex' else json.loads(raw)) if raw else {}


def encode(client, data):
    result = ('\n'.join(value(k) + ' = ' + value(v) for k, v in data.items()) + '\n') if client == 'codex' else json.dumps(data, ensure_ascii=False, indent=2) + '\n'
    decode(client, result.encode())
    return result.encode()


def get(data, keys):
    for k in keys:
        if not isinstance(data, dict) or k not in data:
            return {'present': False}
        data = data[k]
    return {'present': True, 'value': data}


def put(data, keys, entry):
    for k in keys[:-1]:
        data = data.setdefault(k, {})
        if not isinstance(data, dict):
            raise ValueError('Configuration table has incompatible type')
    if entry['present']:
        data[keys[-1]] = copy.deepcopy(entry['value'])
    else:
        data.pop(keys[-1], None)


def save_state(client, state):
    atomic(ROOT / (client + '.json'), json.dumps(state, ensure_ascii=False, default=lambda v: {'__toml_date__': v.isoformat(), '__type__': type(v).__name__}).encode())


def state_hook(v):
    if '__toml_date__' in v:
        return getattr(datetime, v['__type__']).fromisoformat(v['__toml_date__'])
    return v


def apply(client, state, enable):
    path = Path(state['path'])
    raw = read(path)
    doc = decode(client, raw)
    for item in state['fields']:
        current = get(doc, item['keys'])
        expected = item['before'] if enable else item['after']
        target = item['after'] if enable else item['before']
        if current != expected and current != target:
            raise ValueError('Conflicting changes at ' + '.'.join(item['keys']) + '; no files changed. Keep your backup and resolve manually.')
    for item in state['fields']:
        put(doc, item['keys'], item['after'] if enable else item['before'])
    # Remove only empty containers created by this helper.
    for key in state.get('absent_tables', []):
        if doc.get(key) == {}:
            doc.pop(key)
    output = encode(client, doc)
    if raw is not None:
        backup = ROOT / 'backups' / (client + '-' + datetime.datetime.now().strftime('%Y%m%d%H%M%S%f') + '.bak')
        atomic(backup, raw)
    if read(path) != raw:
        raise ValueError('Configuration changed during operation; retry.')
    if not enable and not state['existed'] and not doc:
        if path.exists():
            path.unlink()
    else:
        atomic(path, output)
    state['active'] = enable
    save_state(client, state)


def configure(args, state):
    client = args.client
    executable = shutil.which('codex' if client == 'codex' else 'claude')
    if executable:
        result = subprocess.run([executable, '--version'], capture_output=True, text=True, timeout=10)
        print('Client:', result.stdout.strip()[:120])
    else:
        print('CLI not found. IDE extensions can bundle their own client; verify its version separately.')
    path = Path(os.environ.get('CODEX_HOME', str(Path.home() / '.codex'))) / 'config.toml' if client == 'codex' else Path(os.environ.get('CLAUDE_CONFIG_DIR', str(Path.home() / '.claude'))) / 'settings.json'
    path = path.expanduser().absolute()
    if state and str(path) != state['path']:
        raise ValueError('Configuration directory changed; restore the previous directory first.')
    base = (args.url or input('FiftyAPI site URL: ')).rstrip('/')
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError('Expected an HTTP(S) site URL without credentials, query or fragment')
    print('Target:', path, '\nSite:', base)
    print('The key will be stored in plaintext locally. Config permissions: owner only on Unix; on Windows use a private user profile.')
    if parsed.scheme == 'http':
        print('HTTP sends the API key without transport encryption.')
    with warnings.catch_warnings():
        warnings.simplefilter('error', getpass.GetPassWarning)
        key = getpass.getpass('FiftyAPI API key: ').strip()
    if not key or any(ord(c) < 32 for c in key):
        raise ValueError('Invalid key')
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *unused, **kwargs):
            return None
    req = urllib.request.Request(base + '/v1/models', headers={'Authorization': 'Bearer ' + key})
    with urllib.request.build_opener(NoRedirect).open(req, timeout=20) as response:
        catalog = json.loads(response.read(2 * 1024 * 1024))
    protocol = 'responses' if client == 'codex' else 'messages'
    models = [m['id'] for m in catalog.get('data', []) if protocol in (m.get('protocols') or [m.get('protocol')])]
    if not models:
        raise ValueError('No models advertise ' + protocol + '; contact the administrator.')
    print('Available models:', ', '.join(models))
    model = args.model or input('Model ID: ').strip()
    if model not in models:
        raise ValueError('Model does not advertise the required protocol')
    raw = read(path)
    doc = decode(client, raw)
    if not isinstance(doc, dict):
        raise ValueError('Expected configuration object')
    if client == 'codex':
        changes = [(['model'], model), (['model_provider'], 'fiftyapi'), (['model_providers', 'fiftyapi'], {'name': 'FiftyAPI', 'base_url': base + '/v1', 'experimental_bearer_token': key, 'wire_api': 'responses', 'requires_openai_auth': False, 'supports_websockets': args.websocket})]
    else:
        conflicts = [k for k in ('ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL', 'ANTHROPIC_MODEL', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY') if os.environ.get(k)]
        if conflicts:
            raise ValueError('Remove conflicting inherited environment variables first: ' + ', '.join(conflicts))
        changes = [(['env', k], v) for k, v in {'ANTHROPIC_BASE_URL': base, 'ANTHROPIC_AUTH_TOKEN': key, 'ANTHROPIC_API_KEY': '', 'ANTHROPIC_MODEL': model, 'ANTHROPIC_DEFAULT_OPUS_MODEL': model, 'ANTHROPIC_DEFAULT_SONNET_MODEL': model, 'ANTHROPIC_DEFAULT_HAIKU_MODEL': model, 'CLAUDE_CODE_USE_BEDROCK': '0', 'CLAUDE_CODE_USE_VERTEX': '0', 'CLAUDE_CODE_USE_FOUNDRY': '0', 'CLAUDE_CODE_OAUTH_TOKEN': ''}.items()]
    fields = []
    for keys, v in changes:
        before = get(doc, keys)
        if state:
            old = next(f for f in state['fields'] if f['keys'] == keys)
            expected = old['after'] if state['active'] else old['before']
            if before != expected:
                raise ValueError('Managed fields changed; restore or resolve the conflict first.')
            before = old['before']
        fields.append({'keys': keys, 'before': before, 'after': {'present': True, 'value': v}})
    new = {'path': str(path), 'existed': state['existed'] if state else raw is not None, 'fields': fields, 'absent_tables': state['absent_tables'] if state else [k for k in ('model_providers', 'env') if k not in doc], 'active': False}
    preview = copy.deepcopy(doc)
    for keys, v in changes:
        put(preview, keys, {'present': True, 'value': v})
    encode(client, preview)
    print('TOML formatting/comments are normalized; all parsed settings are retained. Full original bytes are backed up.')
    if input('Apply configuration? [y/N] ').lower() != 'y':
        return
    # Save recovery information before changing the client file.
    if state and state['active']:
        apply(client, state, False)
    save_state(client, new)
    apply(client, new, True)
    source = Path(__file__).resolve().read_bytes()
    atomic(ROOT / 'helper.py', source)
    print('Configured. Reload your VS Code window and start a new session, or launch the CLI.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['configure', 'status', 'use', 'restore', 'uninstall'])
    parser.add_argument('client', choices=['codex', 'claude'])
    parser.add_argument('--url')
    parser.add_argument('--model')
    parser.add_argument('--websocket', action='store_true', help='Enable only when the upstream supports Responses WebSocket')
    args = parser.parse_args()
    if os.environ.get('SUDO_USER'):
        raise ValueError('Run as the same user as the client, without sudo')
    if ROOT.is_symlink():
        raise ValueError('Helper directory must not be a symbolic link')
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    if os.name != 'nt':
        ROOT.chmod(0o700)
    lock = ROOT / 'operation.lock'
    try:
        lock.mkdir()
    except FileExistsError:
        raise ValueError('Another helper is running. If it crashed, remove ' + str(lock)) from None
    try:
        raw = read(ROOT / (args.client + '.json'))
        state = json.loads(raw, object_hook=state_hook) if raw else None
        if args.action == 'configure':
            configure(args, state)
        elif args.action == 'status':
            print(args.client + ': ' + ('configured' if state and state['active'] else 'inactive'))
            if state:
                print('Path:', state['path'])
        elif state:
            if args.action == 'uninstall':
                if state['active']:
                    apply(args.client, state, False)
                (ROOT / (args.client + '.json')).unlink()
                if not any(ROOT.glob('*.json')):
                    (ROOT / 'helper.py').unlink(missing_ok=True)
                print('Removed client setup. Private backups retained at', ROOT / 'backups')
            else:
                apply(args.client, state, args.action == 'use')
                print('Updated. Reload VS Code and start a new session.')
        else:
            raise ValueError('No saved configuration for this client')
    finally:
        lock.rmdir()


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        # Network exceptions can include sensitive URLs; never print response bodies or keys.
        if isinstance(exc, (ValueError, FileNotFoundError)):
            print('Error:', str(exc), file=sys.stderr)
        else:
            print('Error:', type(exc).__name__, '(no credentials logged)', file=sys.stderr)
        sys.exit(1)
