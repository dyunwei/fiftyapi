"""Offline exporter checks; never connect to a real database."""
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from contextlib import redirect_stderr, redirect_stdout
from io import StringIO

spec = importlib.util.spec_from_file_location('export_sub2api', Path(__file__).with_name('export-sub2api.py'))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportTest(unittest.TestCase):
    def test_projection_retains_required_fields_and_excludes_totp_secrets(self):
        raw = {'users': [{'id': 1, 'email': 'member@example.com', 'password_hash': 'fixture', 'totp_enabled': True, 'totp_secret_encrypted': 'never-export'}], 'api_keys': [{'id': 1, 'key': 'legacy-key-fixture', 'group_id': 2, 'ip_whitelist': ['127.0.0.1'], 'expires_at': None}], 'accounts': [], 'proxies': []}
        bundle = exporter.bundle(raw, 'stable-source')
        self.assertEqual(bundle['users'][0]['password_hash'], 'fixture')
        self.assertTrue(bundle['users'][0]['totp_enabled'])
        self.assertNotIn('totp_secret_encrypted', bundle['users'][0])
        self.assertEqual(bundle['api_keys'][0]['ip_whitelist'], ['127.0.0.1'])
        self.assertEqual(bundle['source'], 'stable-source')

    def test_read_only_snapshot_private_file_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / 'migration.json'
            completed = type('Process', (), {'stdout': json.dumps({k: [] for k in exporter.FIELDS}).encode()})()
            with patch('sys.argv', ['export-sub2api.py', '--source', 'site', '--output', str(target)]), patch.object(exporter.subprocess, 'run', return_value=completed) as run, redirect_stdout(StringIO()):
                self.assertEqual(exporter.main(), 0)
                self.assertEqual(stat.S_IMODE(os.stat(target).st_mode), 0o600)
                sql = run.call_args.args[0][-1]
                self.assertIn('REPEATABLE READ READ ONLY', sql)
                original = target.read_bytes()
                errors = StringIO()
                with redirect_stderr(errors), self.assertRaises(SystemExit) as raised:
                    exporter.main()
                self.assertEqual(raised.exception.code, 2)
                self.assertIn('output already exists; choose a new path', errors.getvalue())
                self.assertEqual(target.read_bytes(), original)
                self.assertEqual(run.call_count, 1)


    def test_docker_uses_container_psql_without_host_credentials(self):
        for option in [['--docker'], ['--docker-container', 'custom-db', '--db-user', 'custom-user', '--database', 'custom-name']]:
            with self.subTest(option=option), tempfile.TemporaryDirectory() as folder:
                target = Path(folder) / 'migration.json'
                completed = type('Process', (), {'stdout': json.dumps({k: [] for k in exporter.FIELDS}).encode()})()
                argv = ['export-sub2api.py', '--source', 'site', '--output', str(target)] + option
                with patch('sys.argv', argv), patch.object(exporter.subprocess, 'run', return_value=completed) as run, redirect_stdout(StringIO()):
                    self.assertEqual(exporter.main(), 0)
                    command = run.call_args.args[0]
                    self.assertEqual(command[:4], ['docker', 'exec', '-i', '--'])
                    self.assertEqual(command[4], 'sub2api-postgres' if len(option) == 1 else 'custom-db')
                    self.assertEqual(command[5:8], ['sh', '-c', exporter.DOCKER_PSQL])
                    self.assertIn('REPEATABLE READ READ ONLY', run.call_args.kwargs['input'].decode())
                    self.assertNotIn('inspect', command)
                    if len(option) > 1:
                        self.assertEqual(command[-2:], ['custom-user', 'custom-name'])

    def test_container_shell_reads_password_env_or_secret_file_privately(self):
        with tempfile.TemporaryDirectory() as folder:
            stub = Path(folder) / 'psql'
            stub.write_text('#!' + sys.executable + '\nimport json, os, sys\nprint(json.dumps({"args":sys.argv[1:], "password":os.environ.get("PGPASSWORD"), "sql":sys.stdin.read()}))\n')
            stub.chmod(0o700)
            secret = Path(folder) / 'secret'
            secret.write_text('secret-file-fixture\n')
            env = {'PATH': folder + os.pathsep + os.defpath, 'POSTGRES_USER': 'source-user', 'POSTGRES_DB': 'source-db', 'POSTGRES_PASSWORD': 'env-password-fixture'}
            for file_mode in [False, True]:
                with self.subTest(file_mode=file_mode):
                    settings = dict(env)
                    if file_mode:
                        settings['POSTGRES_PASSWORD_FILE'] = str(secret)
                    completed = subprocess.run(['sh', '-c', exporter.DOCKER_PSQL, 'fiftyapi-export', '', ''], input=exporter.SQL, text=True, capture_output=True, check=True, env=settings)
                    invocation = json.loads(completed.stdout)
                    expected = 'secret-file-fixture' if file_mode else 'env-password-fixture'
                    self.assertEqual(invocation['password'], expected)
                    self.assertNotIn(expected, invocation['args'])
                    self.assertIn('source-user', invocation['args'])
                    self.assertIn('source-db', invocation['args'])
                    self.assertIn('-w', invocation['args'])
                    self.assertEqual(invocation['sql'], exporter.SQL)
                    self.assertEqual(completed.stderr, '')
            completed = subprocess.run(['sh', '-c', exporter.DOCKER_PSQL, 'fiftyapi-export', 'override-user', 'override-db'], input='', text=True, capture_output=True, check=True, env=env)
            self.assertEqual(json.loads(completed.stdout)['args'][-6:], ['-U', 'override-user', '-d', 'override-db', '-f', '-'])

    def test_docker_failure_does_not_print_credentials_or_create_bundle(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / 'migration.json'
            failure = subprocess.CalledProcessError(1, ['docker'], stderr=b'private-password-fixture')
            output = StringIO()
            with patch('sys.argv', ['export-sub2api.py', '--docker', '--source', 'site', '--output', str(target)]), patch.object(exporter.subprocess, 'run', side_effect=failure), redirect_stderr(output):
                self.assertEqual(exporter.main(), 1)
            self.assertNotIn('private-password-fixture', output.getvalue())
            self.assertFalse(target.exists())


if __name__ == '__main__':
    unittest.main()
