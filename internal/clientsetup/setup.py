#!/usr/bin/env python3
"""FiftyAPI local client configuration helper. Python 3.11+, standard library only."""
import argparse
import base64
import copy
import datetime
import getpass
import hashlib
import http.client
import json
import math
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
try:
    import tomllib
except ImportError:
    sys.exit("Python 3.11+ required. Try python3.11 or install a current Python version.")
import urllib.error
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


def toml_key(key):
    if re.fullmatch(r'[A-Za-z0-9_-]+', key):
        return key
    return value(key)


def value(v):
    if isinstance(v, str):
        return json.dumps(v, ensure_ascii=False)
    if isinstance(v, bool):
        return str(v).lower()
    if isinstance(v, (datetime.datetime, datetime.date, datetime.time)):
        return v.isoformat()
    if isinstance(v, dict):
        return '{ ' + ', '.join(toml_key(k) + ' = ' + value(x) for k, x in v.items()) + ' }'
    if isinstance(v, list):
        return '[' + ', '.join(value(x) for x in v) + ']'
    if isinstance(v, float) and math.isnan(v):
        return 'nan'
    return str(v)


def toml_sections(data, path=()):
    assignments = [
        toml_key(key) + ' = ' + value(item)
        for key, item in data.items()
        if not isinstance(item, dict)
    ]
    if path and (assignments or not data):
        assignments.insert(0, '[' + '.'.join(path) + ']')
    if assignments:
        yield '\n'.join(assignments)
    for key, item in data.items():
        if isinstance(item, dict):
            yield from toml_sections(item, (*path, toml_key(key)))


def decode(client, raw):
    return (tomllib.loads(raw.decode('utf-8')) if client == 'codex' else json.loads(raw)) if raw else {}


def encode(client, data):
    if client == 'codex':
        result = '\n\n'.join(toml_sections(data)) + '\n'
    else:
        result = json.dumps(data, ensure_ascii=False, indent=2) + '\n'
    output = result.encode()
    decode(client, output)
    return output


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


def config_path(client):
    env, directory, filename = ('CODEX_HOME', '.codex', 'config.toml') if client == 'codex' else ('CLAUDE_CONFIG_DIR', '.claude', 'settings.json')
    return (Path(os.environ.get(env, str(Path.home() / directory))) / filename).expanduser().absolute()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *unused, **kwargs):
        return None


def catalog_models(base, key, protocol):
    req = urllib.request.Request(base + '/v1/models', headers={'Authorization': 'Bearer ' + key})
    with urllib.request.build_opener(NoRedirect).open(req, timeout=20) as response:
        catalog = json.loads(response.read(2 * 1024 * 1024))
    return [m['id'] for m in catalog.get('data', []) if protocol in (m.get('protocols') or [m.get('protocol')])]


def check_sse(base, key, protocol):
    url = base + '/v1/' + protocol + '?fiftyapi_transport_check=sse'
    req = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + key, 'Accept': 'text/event-stream'})
    try:
        with urllib.request.build_opener(NoRedirect).open(req, timeout=5) as response:
            if response.headers.get_content_type() != 'text/event-stream':
                return 'Expected SSE but received another response. Check the gateway version and proxy routing.'
            arrival = []
            fields = []
            for _ in range(16):
                line = response.readline(256)
                if not line or len(line) == 256:
                    break
                line = line.rstrip(b'\r\n')
                if line:
                    fields.append(line)
                    continue
                if fields == [b'event: fiftyapi.transport_check', f'data: {len(arrival)+1}'.encode()]:
                    arrival.append(time.monotonic())
                fields.clear()
                if len(arrival) == 3:
                    break
    except urllib.error.HTTPError as exc:
        with exc:
            return f'HTTP {exc.code}. Check proxy routing, gateway version and access.'
    except (OSError, ValueError, http.client.HTTPException):
        return 'Connection failed or timed out. Check the network and proxy.'
    if len(arrival) != 3:
        return 'The three test events did not arrive in order. Check proxy routing and stream timeouts.'
    gaps = [round((b - a) * 1000) for a, b in zip(arrival, arrival[1:])]
    print(f'SSE event gaps: {gaps[0]} ms, {gaps[1]} ms')
    if min(gaps) < 250:
        return 'Events arrived together or too close. Check proxy buffering and compression.'
    return None


class UpgradeRequest:
    def request(self, method, url, body=None, headers=None, *, encode_chunked=False):
        # urllib sets Connection: close; retain its proxy and TLS handling while
        # restoring the upgrade header on this one HTTP/1.1 handshake.
        headers = {**(headers or {}), 'Connection': 'Upgrade'}
        super().request(method, url, body, headers, encode_chunked=encode_chunked)


class UpgradeHTTPConnection(UpgradeRequest, http.client.HTTPConnection):
    pass


class UpgradeHTTPSConnection(UpgradeRequest, http.client.HTTPSConnection):
    pass


class UpgradeHTTPHandler(urllib.request.HTTPHandler):
    def http_open(self, req):
        return self.do_open(UpgradeHTTPConnection, req)


class UpgradeHTTPSHandler(urllib.request.HTTPSHandler):
    def https_open(self, req):
        return self.do_open(UpgradeHTTPSConnection, req)


def check_websocket(base, key):
    nonce = base64.b64encode(os.urandom(16)).decode('ascii')
    req = urllib.request.Request(base + '/v1/responses', headers={
        'Authorization': 'Bearer ' + key, 'Upgrade': 'websocket',
        'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': nonce,
    })
    opener = urllib.request.build_opener(NoRedirect, UpgradeHTTPHandler, UpgradeHTTPSHandler)
    try:
        response = opener.open(req, timeout=5)
    except urllib.error.HTTPError as exc:
        response = exc  # urllib represents the protocol switch as an HTTPError.
    except (OSError, ValueError, http.client.HTTPException):
        return 'Handshake failed or timed out. Check the network and proxy upgrade settings.'
    with response:
        if response.code != 101:
            return f'Expected HTTP 101, got {response.code}. Check the reverse proxy upgrade settings and access.'
        accept = base64.b64encode(hashlib.sha1((nonce + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode('ascii')).digest()).decode('ascii')
        connection = response.headers.get('Connection', '').lower().split(',')
        if ('upgrade' not in [value.strip() for value in connection]
                or response.headers.get('Upgrade', '').lower() != 'websocket'
                or response.headers.get('Sec-WebSocket-Accept') != accept):
            return 'The HTTP 101 response has invalid WebSocket handshake headers.'
    return None


def check(client):
    raw = read(ROOT / (client + '.json'))
    state = json.loads(raw, object_hook=state_hook) if raw else None
    if not state or not state['active']:
        raise ValueError('No active FiftyAPI configuration. Run configure first.')
    path = config_path(client)
    if str(path) != state['path']:
        raise ValueError('The client configuration directory differs from the saved setup.')
    try:
        raw = read(path)
        if raw is None:
            raise ValueError()
        doc = decode(client, raw)
    except ValueError:
        raise ValueError('Cannot read the active client configuration.') from None
    if any(get(doc, field['keys']) != field['after'] for field in state['fields']):
        raise ValueError('Managed settings have changed. Check the local configuration before retrying.')
    if client == 'codex':
        provider = doc['model_providers']['fiftyapi']
        base = provider['base_url'].removesuffix('/v1')
        key, model, protocol = provider['experimental_bearer_token'], doc['model'], 'responses'
        print('Configured transport:', 'WebSocket enabled' if provider['supports_websockets'] else 'HTTP')
    else:
        if claude_conflicts():
            raise ValueError('Inherited Claude environment variables conflict with the saved settings.')
        env = doc['env']
        base, key, model, protocol = env['ANTHROPIC_BASE_URL'], env['ANTHROPIC_AUTH_TOKEN'], env['ANTHROPIC_MODEL'], 'messages'
    print('Local settings: match saved FiftyAPI configuration')
    try:
        models = catalog_models(base, key, protocol)
    except urllib.error.HTTPError as exc:
        if exc.code == 401:
            raise ValueError('Gateway reachable; API key rejected or member disabled.') from None
        raise ValueError(f'Gateway returned HTTP {exc.code} for the model catalog.') from None
    except (OSError, ValueError):
        raise ValueError('Could not read the gateway model catalog. Check the URL, network and TLS.') from None
    print('Gateway reachable; API key accepted')
    model_ok = model in models
    print(f'Model: {"matches" if model_ok else "unavailable or incompatible with"} {protocol}')
    sse_error = check_sse(base, key, protocol)
    print('SSE:', sse_error or 'test events arrived separately')
    websocket_required = client == 'codex' and provider['supports_websockets']
    websocket_error = check_websocket(base, key) if client == 'codex' else None
    if client == 'codex':
        label = 'WebSocket' if websocket_required else 'WebSocket (optional)'
        print(label + ':', websocket_error or 'HTTP 101 handshake verified')
    if not model_ok or sse_error or (websocket_required and websocket_error):
        raise ValueError('One or more required checks failed. See the results above.')


def claude_conflicts():
    keys = ('ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL', 'ANTHROPIC_MODEL',
            'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY')
    return [key for key in keys if os.environ.get(key)]


def configure(args, state):
    client = args.client
    executable = shutil.which('codex' if client == 'codex' else 'claude')
    if executable:
        result = subprocess.run([executable, '--version'], capture_output=True, text=True, timeout=10)
        print('Client:', result.stdout.strip()[:120])
    else:
        print('CLI not found. IDE extensions can bundle their own client; verify its version separately.')
    path = config_path(client)
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
    protocol = 'responses' if client == 'codex' else 'messages'
    models = catalog_models(base, key, protocol)
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
        conflicts = claude_conflicts()
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
    if client == 'codex':
        print('TOML is rewritten using section headers. Parsed settings are retained and the original file is backed up, including comments.')
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
    parser.add_argument('action', choices=['configure', 'check', 'status', 'use', 'restore', 'uninstall'])
    parser.add_argument('client', choices=['codex', 'claude'])
    parser.add_argument('--url')
    parser.add_argument('--model')
    parser.add_argument('--websocket', action='store_true', help='Enable only when the upstream supports Responses WebSocket')
    args = parser.parse_args()
    if os.environ.get('SUDO_USER'):
        raise ValueError('Run as the same user as the client, without sudo')
    if ROOT.is_symlink():
        raise ValueError('Helper directory must not be a symbolic link')
    if args.action == 'check':
        check(args.client)
        return
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
