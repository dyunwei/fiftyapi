#!/usr/bin/env python3
"""Verify a Compose image upgrade with a temporary volume and a simulated upstream."""
import base64
from contextlib import contextmanager
import hashlib
import http.client
import http.cookiejar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import secrets
import socket
import struct
import subprocess
import tempfile
import threading
import time
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]
PROJECT = 'fiftyapi-upgrade-' + uuid.uuid4().hex[:12]
SENT = threading.Event()
FINISH = threading.Event()


class UpstreamHandler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def do_GET(self):
        if (self.path != '/v1/responses' or self.headers.get('Upgrade', '').lower() != 'websocket'
                or self.headers.get('Authorization') != 'Bearer upgrade-fixture'):
            self.send_error(400)
            return
        accept = hashlib.sha1((self.headers['Sec-WebSocket-Key'] +
                               '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()
        self.send_response(101)
        self.send_header('Upgrade', 'websocket')
        self.send_header('Connection', 'Upgrade')
        self.send_header('Sec-WebSocket-Accept', base64.b64encode(accept).decode())
        self.end_headers()
        self.close_connection = True
        frame = self.rfile.read(2)
        if len(frame) != 2 or frame[0] != 0x81 or not frame[1] & 0x80:
            return
        size = frame[1] & 0x7f
        if size == 126:
            size = struct.unpack('!H', self.rfile.read(2))[0]
        elif size == 127:
            size = struct.unpack('!Q', self.rfile.read(8))[0]
        mask = self.rfile.read(4)
        data = self.rfile.read(size)
        event = json.loads(bytes(byte ^ mask[i % 4] for i, byte in enumerate(data)))
        if event.get('type') == 'response.create':
            SENT.set()
            FINISH.wait(90)

    def do_POST(self):
        if self.path != '/v1/responses' or self.headers.get('Authorization') != 'Bearer upgrade-fixture':
            self.send_error(400)
            return
        self.rfile.read(int(self.headers['Content-Length']))
        payload = {
            'id': 'resp_upgrade', 'object': 'response', 'status': 'completed',
            'output': [], 'usage': {'input_tokens': 2, 'output_tokens': 1, 'total_tokens': 3},
        }
        data = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_):
        pass


@contextmanager
def websocket(port, token):
    with socket.create_connection(('127.0.0.1', port), timeout=5) as conn:
        key = base64.b64encode(secrets.token_bytes(16)).decode()
        conn.sendall((
            f'GET /v1/responses HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n'
            f'Authorization: Bearer {token}\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n'
            f'Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {key}\r\n\r\n'
        ).encode())
        with conn.makefile('rb') as response:
            status = response.readline().split()
            http.client.parse_headers(response)
            assert status[1] == b'101', status
        yield conn


def send_text(conn, payload):
    content = json.dumps(payload).encode()
    size = len(content)
    header = bytes((0x81, 0x80 | size)) if size < 126 else b'\x81\xfe' + struct.pack('!H', size)
    mask = secrets.token_bytes(4)
    conn.sendall(header + mask + bytes(byte ^ mask[i % 4] for i, byte in enumerate(content)))


def run(directory, upstream):
    old_image = os.environ.get('FIFTYAPI_TEST_OLD_IMAGE', 'fiftyapi:upgrade-baseline')
    new_image = os.environ.get('FIFTYAPI_TEST_IMAGE', 'fiftyapi:local')
    setup = secrets.token_hex(4)
    password = secrets.token_hex(16)
    override = directory / 'override.json'
    override.write_text(json.dumps({'services': {'fiftyapi': {
        'extra_hosts': ['host.docker.internal:host-gateway'],
        'environment': {'FIFTYAPI_AUTO_MAINTENANCE': 'false'},
    }}}))
    env_file = directory / 'runtime.env'
    env_file.write_text('')
    # Keep the test isolated from the repository's real Compose environment.
    env = {k: v for k, v in os.environ.items() if not k.startswith(('FIFTYAPI_', 'COMPOSE_', 'ANTIGRAVITY_'))}
    env.update({
        'FIFTYAPI_LISTEN': '127.0.0.1:', 'FIFTYAPI_SETUP_TOKEN': setup,
        'FIFTYAPI_MASTER_KEY': base64.b64encode(secrets.token_bytes(32)).decode(),
    })
    args = ['docker', 'compose', '--env-file', str(env_file), '-p', PROJECT,
            '-f', str(ROOT / 'compose.yaml'), '-f', str(override)]

    def compose(*command, image=new_image):
        return subprocess.check_output(
            [*args, *command], env={**env, 'FIFTYAPI_IMAGE': image}, text=True, timeout=120,
        ).strip()

    client = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                         urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    port = 0

    def request(path, body=None, headers=None, method=None):
        req = urllib.request.Request(f'http://127.0.0.1:{port}' + path,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers={'Content-Type': 'application/json', **(headers or {})},
                                     method=method)
        with client.open(req, timeout=5) as response:
            return json.load(response)

    def ready(image):
        nonlocal port
        port = int(compose('port', 'fiftyapi', '8080', image=image).rsplit(':', 1)[1])
        for _ in range(100):
            try:
                assert request('/healthz')['status'] == 'ok'
                return
            except OSError:
                time.sleep(.1)
        raise RuntimeError('container did not become ready')

    def login():
        request('/api/auth/login', {'username': 'admin', 'password': password})
        return request('/api/me')

    try:
        compose('up', '-d', '--no-build', '--pull', 'never', image=old_image)
        ready(old_image)
        before = compose('ps', '-q', 'fiftyapi', image=old_image)
        request('/api/setup', {'username': 'admin', 'password': password, 'setup_token': setup})
        session = login()
        csrf = {'X-CSRF-Token': session['csrf_token']}
        user = session['user']['id']
        request(f'/api/admin/users/{user}/quota',
                {'limit_5h_microusd': 1000000, 'limit_7d_microusd': 2000000}, csrf, 'PUT')
        budget = request('/api/quota')
        token = request('/api/keys', {'name': 'upgrade'}, csrf)['token']
        auth = {'Authorization': 'Bearer ' + token}
        created = request('/api/admin/gateway-accounts', {
            'name': 'simulated upgrade upstream', 'api_key': 'upgrade-fixture',
            'gateway': {
                'platform': 'new-api', 'base_url': f'http://host.docker.internal:{upstream.server_port}/v1',
                'allow_http': True, 'websocket': True, 'protocols': ['responses'], 'discovery': 'manual', 'auth_mode': 'bearer',
                'models': [{'id': 'upgrade-model', 'upstream_id': 'fixture-model', 'protocol': 'responses'}],
            },
        }, csrf)
        assert created['models'] == ['upgrade-model'], created
        request('/api/admin/models', {
            'id': 'upgrade-model', 'enabled': True,
            'price': {'input': 1000000, 'output': 1000000, 'cached': 0, 'cache_write': 0},
        }, csrf, 'PUT')
        with websocket(port, token) as conn:
            send_text(conn, {'type': 'response.create', 'model': 'upgrade-model', 'input': 'interrupt'})
            if not SENT.wait(15):
                try:
                    print('Test WebSocket reply:', conn.recv(4096))
                except TimeoutError:
                    pass
                print(request('/api/admin/logs?page=1&page_size=10'))
                print(compose('logs', '--tail', '30', 'fiftyapi', image=old_image))
                raise RuntimeError('the old container did not dispatch the simulated turn')
            assert all(w['reserved'] > 0 for w in request('/api/quota'))
            compose('up', '-d', '--no-build', '--pull', 'never')
        ready(new_image)
        assert compose('ps', '-q', 'fiftyapi') != before, 'Compose did not replace the old container'
        session = login()
        assert session['user']['id'] == user
        quota = request('/api/quota')
        assert request('/api/client-readiness?protocol=responses')['code'] == 'ready'
        assert request('/api/client-readiness?protocol=messages')['code'] == 'protocol'
        assert [w['limit'] for w in quota] == [w['limit'] for w in budget]
        assert all(w['reserved'] == 0 and w['used'] == 0 for w in quota), quota
        assert any(m['id'] == 'upgrade-model' for m in request('/v1/models', headers=auth)['data'])
        with websocket(port, token):
            pass
        response = request('/v1/responses', {'model': 'upgrade-model', 'input': 'continue'}, auth)
        assert response['id'] == 'resp_upgrade', response
        assert all(w['used'] == 3 and w['reserved'] == 0 for w in request('/api/quota'))
        print('Compose upgrade passed: container replacement, old API key, WebSocket reconnect, budgets, interrupted reservation and new settlement.')
    finally:
        FINISH.set()
        compose('down', '--volumes', '--remove-orphans')


if __name__ == '__main__':
    with tempfile.TemporaryDirectory(prefix='fiftyapi-upgrade-') as directory, ThreadingHTTPServer(('0.0.0.0', 0), UpstreamHandler) as upstream:
        worker = threading.Thread(target=upstream.serve_forever, daemon=True)
        worker.start()
        try:
            run(Path(directory), upstream)
        finally:
            upstream.shutdown()
            worker.join()
