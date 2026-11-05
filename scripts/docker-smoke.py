#!/usr/bin/env python3
"""Exercise only a local image with temporary credentials/data; publish nothing."""
import base64
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import socket
import urllib.parse
import subprocess
import tempfile
import time
import urllib.request
import uuid

name='fiftyapi-smoke-'+uuid.uuid4().hex[:12]
volume=name+'-data'
image=os.environ.get('FIFTYAPI_TEST_IMAGE','fiftyapi:local')
automatic=os.environ.get('FIFTYAPI_TEST_AUTO_SECRETS')=='1'
bind_mount=os.environ.get('FIFTYAPI_TEST_BIND_MOUNT')=='1'
prefix='FIFTYAPI_'
def docker(*args):
    return subprocess.check_output(['docker',*args],text=True,stderr=subprocess.PIPE).strip()
with tempfile.TemporaryDirectory(prefix='fiftyapi-docker-') as directory:
    env=Path(directory)/'runtime.env'
    setup=secrets.token_hex(4)
    password=secrets.token_hex(16)
    env.write_text(prefix+'MASTER_KEY='+base64.b64encode(secrets.token_bytes(32)).decode()+'\n'+prefix+'SETUP_TOKEN='+setup+'\n')
    if automatic:env.write_text('FIFTYAPI_MASTER_KEY=\nFIFTYAPI_SETUP_TOKEN=\n')
    env.chmod(0o600)
    mount=volume+':/data'
    try:
        docker('volume','create',volume)
        if bind_mount:
            data=Path(directory)/'data'
            data.mkdir(mode=0o755)
            # Reproduce a private file left by an earlier root-run container.
            docker('run','--rm','--user','0:0','--entrypoint','sh','-v',str(data)+':/data',image,'-c','touch /data/ownership-fixture; chmod 600 /data/ownership-fixture')
            mount=str(data)+':/data'
        docker('run','-d','--name',name,'--read-only','--tmpfs','/tmp:rw,noexec,nosuid,size=32m','--security-opt','no-new-privileges:true','--env-file',str(env),'-v',mount,'-p','127.0.0.1::8080',image)
        port=docker('port',name,'8080/tcp').rsplit(':',1)[1]
        origin='http://127.0.0.1:'+port
        client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        def request(path,body=None,headers=None,method=None):
            data=None if body is None else json.dumps(body).encode()
            return client.open(urllib.request.Request(origin+path,data=data,headers={'Content-Type':'application/json',**(headers or {})},method=method),timeout=5)
        for attempt in range(100):
            try:
                if request('/healthz').status==200:break
            except OSError:time.sleep(.1)
        else:raise RuntimeError('container health did not become ready')
        if automatic:
            setup=docker('exec',name,'cat','/data/setup-token')
            generated_key=docker('exec',name,'cat','/data/master.key')
            assert len(base64.b64decode(generated_key,validate=True))==32
            assert len(setup)==8 and all(c in '0123456789abcdef' for c in setup)
            for secret in ['master.key','setup-token']:
                assert docker('exec',name,'stat','-c','%a','/data/'+secret)=='600'
        assert b'<html' in request('/').read()
        assert json.load(request('/api/setup/status'))['initialized'] is False
        request('/api/setup',{'username':'admin','password':password,'setup_token':setup}).close()
        request('/api/auth/login',{'username':'admin','password':password}).close()
        csrf=json.load(request('/api/me'))['csrf_token']
        mail_config={'enabled':True,'public_url':'http://gateway.internal:8080','host':'smtp.example','port':'587','from':'sender@example.test','username':'smoke-user','password':'docker-smtp-fixture-password','tls_mode':'starttls'}
        request('/api/admin/settings/email',mail_config,{'X-CSRF-Token':csrf},method='PUT').close()

        imported=json.load(request('/api/admin/accounts/import',{'provider':'openai','verify':False,'accounts':[{'name':'container-import','auth':{'access_token':'fixture-container-access','expires_in':3600}}]},{'X-CSRF-Token':csrf}))
        assert imported['imported']==1
        account=imported['items'][0]['id']
        policy={'priority':12,'weight':3,'concurrency':4,'max_queue':7,'wait_ms':750}
        request(f'/api/admin/accounts/{account}/routing',policy,{'X-CSRF-Token':csrf},method='PUT').close()
        stale_config=json.load(request(f'/api/admin/accounts/{account}/config'))
        policy['concurrency']=6
        request(f'/api/admin/accounts/{account}/routing',policy,{'X-CSRF-Token':csrf},method='PUT').close()
        stale_config['timeout_seconds']=120
        request(f'/api/admin/accounts/{account}/config',stale_config,{'X-CSRF-Token':csrf},method='PUT').close()
        assert json.load(request(f'/api/admin/accounts/{account}/routing'))['policy']==policy
        token=json.load(request('/api/keys',{'name':'container-websocket'},{'X-CSRF-Token':csrf}))['token']
        target=urllib.parse.urlsplit(origin)
        for path,origin_header,expected in [('/v1/responses',origin,101),('/responses',origin,101),('/v1/responses','https://untrusted.example',403)]:
            with socket.create_connection((target.hostname,target.port),timeout=5) as connection:
                upgrade=(f'GET {path} HTTP/1.1\r\nHost: {target.netloc}\r\nAuthorization: Bearer {token}\r\nOrigin: {origin_header}\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: '+base64.b64encode(secrets.token_bytes(16)).decode()+'\r\n\r\n')
                connection.sendall(upgrade.encode())
                with connection.makefile('rb') as response:
                    assert int(response.readline().split()[1])==expected
                    while response.readline().strip():pass

        if automatic:
            docker('stop','--time','60',name)
            docker('rm',name)
            docker('run','-d','--name',name,'--read-only','--tmpfs','/tmp:rw,noexec,nosuid,size=32m','--security-opt','no-new-privileges:true','--env-file',str(env),'-v',mount,'-p','127.0.0.1::8080',image)
        else:docker('restart',name)
        origin='http://127.0.0.1:'+docker('port',name,'8080/tcp').rsplit(':',1)[1]
        for attempt in range(100):
            try:
                initialized=json.load(request('/api/setup/status'))['initialized']
                break
            except OSError:time.sleep(.1)
        else:raise RuntimeError('restart did not become ready')
        assert initialized is True
        if automatic:
            assert docker('exec',name,'cat','/data/master.key')==generated_key
            assert docker('exec',name,'cat','/data/setup-token')==setup
        request('/api/auth/login',{'username':'admin','password':password}).close()
        assert json.load(request('/api/me'))['user']['username']=='admin'
        csrf=json.load(request('/api/me'))['csrf_token']
        persisted=json.load(request(f'/api/admin/accounts/{account}/routing'))
        assert persisted['policy']==policy
        assert persisted['health']['cooldowns']==[]
        summary=json.load(request('/api/admin/accounts'))
        assert len(summary)==1 and 'fixture-container-access' not in json.dumps(summary)
        request(f'/api/admin/accounts/{account}',headers={'X-CSRF-Token':csrf},method='DELETE').close()
        assert json.load(request('/api/admin/accounts'))==[]
        saved=json.load(request('/api/admin/settings/email'))
        assert saved['source']=='database' and saved['password_set'] is True and saved['config']['enabled'] is True
        assert not saved['config'].get('password')
        assert saved['config']['public_url']=='http://gateway.internal:8080'
        logs=json.load(request('/api/admin/logs?page=1&page_size=20'))
        assert logs['items']==[] and logs['total']==0

        if bind_mount:
            assert docker('exec','--user','10001:10001',name,'stat','-c','%u:%g:%a','/data/ownership-fixture')=='10001:10001:600'
        status=docker('exec',name,'cat','/proc/1/status')
        assert next(line.split()[1:] for line in status.splitlines() if line.startswith('Uid:'))==['10001']*4
        assert next(line.split()[1:] for line in status.splitlines() if line.startswith('Gid:'))==['10001']*4
        assert docker('inspect','--format','{{.HostConfig.ReadonlyRootfs}}',name)=='true'
        docker('exec',name,'test','-f','/app/licenses/index.json')
        docker('exec','--user','10001:10001',name,'test','-w','/data')
        docker('exec',name,'test','-f','/data/fiftyapi.db')
        print(('Automatic persisted secrets: ' if automatic else 'FiftyAPI variables: ')+'Docker smoke passed: embedded UI, setup, persisted restart, login, UID 10001, read-only root, writable data and dependency notices, encrypted email settings, account import/routing persistence/deletion, stale connection save preserves scheduling, authenticated WebSocket routes/Origin isolation and paginated logs. Temporary resources removed.')
    finally:
        subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        if bind_mount:
            subprocess.run(['docker','run','--rm','--user','0:0','--entrypoint','sh','-v',mount,image,'-c','find /data -mindepth 1 -delete'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
