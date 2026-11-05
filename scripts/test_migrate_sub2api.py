"""Exercise the one-command Docker exporter with a synthetic Docker CLI."""
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import time
import uuid
import unittest

SCRIPT = Path(__file__).with_name('migrate-sub2api.sh').resolve()
FAKE = r'''
import json, os, sys
args = sys.argv[1:]
if args[0] == 'info':
    print('stable-engine-fixture')
elif args[0] == 'ps':
    if os.environ.get('MULTIPLE') == '1':
        print('db-one|postgres:18|postgres\ndb-two|postgres:18|postgres')
    else:
        print('old-postgres|postgres:18|postgres')
elif args[0] == 'exec':
    sql = sys.stdin.read()
    with open(os.environ['CAPTURE'], 'w') as output:
        json.dump({'args': args, 'sql': sql}, output)
    if os.environ.get('FAIL_EXPORT') == '1':
        print('private-password-fixture', file=sys.stderr)
        sys.exit(1)
    print(json.dumps({'type':'fiftyapi-sub2api-migration', 'version':1, 'source':'docker:test', 'users':[], 'api_keys':[], 'accounts':[], 'proxies':[]}))
else:
    sys.exit(1)
'''


class DockerMigrationScriptTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        docker = self.root / 'docker'
        docker.write_text('#!' + sys.executable + '\n' + FAKE)
        docker.chmod(0o700)
        self.env = {**os.environ, 'PATH': str(self.root) + os.pathsep + os.environ['PATH'], 'CAPTURE': str(self.root / 'capture.json')}
        # Tests do not chown to any caller inherited from the test environment.
        self.env.pop('SUDO_UID', None)
        self.env.pop('SUDO_GID', None)
        self.target = self.root / 'migration.json'

    def tearDown(self):
        self.directory.cleanup()

    def run_script(self, *args, piped=False):
        command = ['bash'] if piped else ['bash', str(SCRIPT)]
        if piped:
            command += ['-s', '--']
        command += ['--output', str(self.target), *args]
        return subprocess.run(command, input=SCRIPT.read_text() if piped else '', capture_output=True, text=True, env=self.env)

    def test_pipe_mode_auto_detects_and_exports_private_snapshot(self):
        result = self.run_script(piped=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(stat.S_IMODE(self.target.stat().st_mode), 0o600)
        self.assertEqual(json.loads(self.target.read_text())['version'], 1)
        capture = json.loads((self.root / 'capture.json').read_text())
        self.assertEqual(capture['args'][:5], ['exec', '-i', '--', 'old-postgres', 'sh'])
        self.assertIn('REPEATABLE READ READ ONLY', capture['sql'])
        self.assertIn('jsonb_object_agg', capture['sql'])
        self.assertNotIn('totp_secret_encrypted', capture['sql'])
        self.assertIn('[4/4]', result.stdout)
        self.assertNotIn('private-password-fixture', result.stdout + result.stderr)

    def test_explicit_container_and_no_overwrite(self):
        self.env['MULTIPLE'] = '1'
        result = self.run_script('--container', 'custom-db')
        self.assertEqual(result.returncode, 0, result.stderr)
        original = self.target.read_bytes()
        result = self.run_script('--container', 'custom-db')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.target.read_bytes(), original)

    def test_failure_cleans_partial_file_and_hides_credentials(self):
        self.env['FAIL_EXPORT'] = '1'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.target.exists())
        self.assertNotIn('private-password-fixture', result.stdout + result.stderr)


@unittest.skipUnless(os.environ.get('FIFTYAPI_TEST_DOCKER') == '1', 'isolated Docker integration is opt-in')
class DockerPostgresIntegrationTest(unittest.TestCase):
    def test_one_command_exports_real_postgres_without_touching_other_containers(self):
        name = 'fiftyapi-migration-test-' + uuid.uuid4().hex[:12]
        def docker(*args, **kwargs):
            return subprocess.run(['docker', *args], capture_output=True, text=True, check=True, **kwargs)
        created = False
        try:
            docker('run', '-d', '--name', name, '--network', 'none', '--tmpfs', '/var/lib/postgresql', '-e', 'POSTGRES_USER=migration_fixture', '-e', 'POSTGRES_DB=source_fixture', '-e', 'POSTGRES_PASSWORD=postgres-test-password-fixture', 'postgres:18-alpine')
            created = True
            for _ in range(100):
                ready = subprocess.run(['docker', 'exec', name, 'sh', '-c', 'PGPASSWORD="$POSTGRES_PASSWORD" psql -h 127.0.0.1 -U migration_fixture -d source_fixture -Atqc "SELECT 1"'], capture_output=True)
                if ready.returncode == 0:
                    break
                time.sleep(0.1)
            else:
                self.fail('isolated PostgreSQL did not become ready')
            seed = """
CREATE TABLE users(id int, email text, username text, password_hash text, status text, totp_enabled bool, totp_secret_encrypted text, deleted_at timestamptz);
CREATE TABLE api_keys(id int, user_id int, name text, key text, status text, group_id int, deleted_at timestamptz);
CREATE TABLE accounts(id int, name text, platform text, type text, status text, credentials jsonb, concurrency int, priority int, deleted_at timestamptz);
CREATE TABLE proxies(id int, protocol text, host text, port int, status text, deleted_at timestamptz);
INSERT INTO users VALUES(1,'fixture@example.com','Fixture','hash-fixture','active',false,'must-not-export',NULL),(2,'removed@example.com','Deleted','hash-fixture','active',false,'must-not-export',now());
INSERT INTO api_keys VALUES(1,1,'Fixture key','docker-key-fixture','active',NULL,NULL);
INSERT INTO accounts VALUES(1,'Fixture account','openai','oauth','active','{"refresh_token":"docker-token-fixture"}',5,50,NULL);
INSERT INTO proxies VALUES(1,'http','proxy.example',8080,'active',NULL);
"""
            docker('exec', '-i', name, 'psql', '-U', 'migration_fixture', '-d', 'source_fixture', '-v', 'ON_ERROR_STOP=1', input=seed)
            with tempfile.TemporaryDirectory() as folder:
                sources = []
                for index in range(2):
                    output = Path(folder) / ('migration-' + str(index) + '.json')
                    completed = subprocess.run(['bash', str(SCRIPT), '--container', name, '--output', str(output)], capture_output=True, text=True, check=True)
                    bundle = json.loads(output.read_text())
                    self.assertEqual(bundle['type'], 'fiftyapi-sub2api-migration')
                    self.assertEqual({k: len(bundle[k]) for k in ['users', 'api_keys', 'accounts', 'proxies']}, {k: 1 for k in ['users', 'api_keys', 'accounts', 'proxies']})
                    self.assertNotIn('totp_secret_encrypted', bundle['users'][0])
                    self.assertNotIn('must-not-export', output.read_text())
                    self.assertNotIn('docker-key-fixture', completed.stdout + completed.stderr)
                    self.assertNotIn('docker-token-fixture', completed.stdout + completed.stderr)
                    sources.append(bundle['source'])
                self.assertEqual(sources[0], sources[1])
            count = docker('exec', name, 'psql', '-U', 'migration_fixture', '-d', 'source_fixture', '-Atqc', 'SELECT count(*) FROM users').stdout.strip()
            self.assertEqual(count, '2')
        finally:
            if created:
                subprocess.run(['docker', 'rm', '-f', '-v', name], capture_output=True)


if __name__ == '__main__':
    unittest.main()
