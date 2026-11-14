import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace
import io

spec = importlib.util.spec_from_file_location('helper', Path(__file__).with_name('setup.py'))
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)


class SetupTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        h.ROOT = Path(self.tmp.name) / 'private'

    def test_toml_roundtrip(self):
        source = b'''# comments\nmodel="old"\n[other]\nquoted="a\\nb"\ndate=2026-01-02\narray=[{x=1}, {x=2}]\n[mcp_servers.demo]\ncommand="echo"\nargs=["hello"]\n'''
        self.assertEqual(h.decode('codex', source), h.decode('codex', h.encode('codex', h.decode('codex', source))))

    def test_restore_preserves_unrelated_edits_and_rejects_conflicts(self):
        for client in ['claude', 'codex']:
            path = Path(self.tmp.name) / client
            original = {'permissions': {'allow': ['Read']}, 'model': 'official'}
            path.write_bytes(h.encode(client, original))
            state = {'path': str(path), 'existed': True, 'fields': [{'keys': ['model'], 'before': {'present': True, 'value': 'official'}, 'after': {'present': True, 'value': 'gateway'}}]}
            h.apply(client, state, True)
            doc = h.decode(client, path.read_bytes())
            doc['unrelated'] = 7
            path.write_bytes(h.encode(client, doc))
            h.apply(client, state, False)
            self.assertEqual(h.decode(client, path.read_bytes()), dict(original, unrelated=7))
            h.apply(client, state, True)
            doc['model'] = 'user-edit'
            path.write_bytes(h.encode(client, doc))
            before = path.read_bytes()
            with self.assertRaises(ValueError):
                h.apply(client, state, False)
            self.assertEqual(before, path.read_bytes())

    def test_new_file_is_removed_and_use_is_repeatable(self):
        path = Path(self.tmp.name) / 'new.json'
        state = {'path': str(path), 'existed': False, 'absent_tables': ['env'], 'fields': [{'keys': ['env', 'TOKEN'], 'before': {'present': False}, 'after': {'present': True, 'value': 'simulated'}}]}
        h.apply('claude', state, True)
        h.apply('claude', state, True)
        h.apply('claude', state, False)
        self.assertFalse(path.exists())
        h.apply('claude', state, True)
        self.assertTrue(path.exists())
        self.assertTrue(list((h.ROOT / 'backups').glob('*.bak')))

    def test_configure_reconfigure_restore_with_simulated_catalog(self):
        for client, protocol in [('codex', 'responses'), ('claude', 'messages')]:
            configdir = Path(self.tmp.name) / client
            args = SimpleNamespace(client=client, url='https://example.test', model='simulated-model', websocket=False)
            catalog = json.dumps({'data': [{'id': 'simulated-model', 'protocols': [protocol]}]}).encode()
            class Opener:
                def open(self, req, timeout):
                    assert req.full_url == 'https://example.test/v1/models'
                    return io.BytesIO(catalog)
            with patch.dict(h.os.environ, {'CODEX_HOME': str(configdir), 'CLAUDE_CONFIG_DIR': str(configdir)}, clear=True), patch.object(h.shutil, 'which', return_value=None), patch.object(h.getpass, 'getpass', return_value='sk-simulated'), patch('builtins.input', return_value='y'), patch.object(h.urllib.request, 'build_opener', return_value=Opener()):
                h.configure(args, None)
                state = json.loads((h.ROOT / (client + '.json')).read_bytes())
                self.assertTrue(state['active'])
                h.configure(args, state)
                state = json.loads((h.ROOT / (client + '.json')).read_bytes())
                h.apply(client, state, False)
                self.assertFalse(Path(state['path']).exists())
                h.apply(client, state, True)
                self.assertTrue(Path(state['path']).exists())

    def test_symlink_refused(self):
        path = Path(self.tmp.name) / 'link'
        path.symlink_to(Path(self.tmp.name) / 'target')
        with self.assertRaises(ValueError):
            h.atomic(path, b'secret')


if __name__ == '__main__':
    unittest.main()
