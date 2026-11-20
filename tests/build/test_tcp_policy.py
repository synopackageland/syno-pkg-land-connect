import importlib.util
import shutil
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('tcp_policy', ROOT / 'scripts/tcp_policy.py')
tcp_policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tcp_policy)

# The CLI's own encoder, from the installed (pinned) CLI, on the same manifest text.
CLI_FLAG = '''
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const [cli, file] = process.argv.slice(1);
const YAML = createRequire(cli + '/package.json')('yaml');
const { tcpPolicyFlag } = await import(cli + '/dist/standalone/tcp-listeners.js');
process.stdout.write(tcpPolicyFlag({ tcpListeners: YAML.parse(readFileSync(file, 'utf8')).tcp_listeners }).join(' '));
'''


class TCPPolicyTests(unittest.TestCase):
    def test_manifest_listeners_are_linked_into_the_payload_binaries(self):
        prepare = (ROOT / 'scripts/prepare.py').read_text()
        self.assertIn('tcp_policy_flag(', prepare)
        self.assertIn("+ (' ' + policy if policy else '')", prepare)
        flag = tcp_policy.flag((ROOT / 'dsm-package.yaml').read_text())
        self.assertTrue(flag.startswith(tcp_policy.FLAG))
        self.assertEqual(tcp_policy.listeners((ROOT / 'dsm-package.yaml').read_text())['lan_probe'], {'scope': 'all', 'port': 45001})

    def test_encoding_matches_the_cli(self):
        cli = ROOT / 'node_modules/@synopackageland/cli'
        if not (cli / 'dist/standalone/tcp-listeners.js').exists() or not shutil.which('node'):
            self.skipTest('the pinned CLI is not installed')
        want = subprocess.check_output(['node', '--input-type=module', '-e', CLI_FLAG, str(cli.resolve()), str(ROOT / 'dsm-package.yaml')], text=True)
        self.assertEqual(tcp_policy.flag((ROOT / 'dsm-package.yaml').read_text()), want)

    def test_other_shapes(self):
        self.assertEqual(tcp_policy.flag('package: X\n'), '')
        two = 'tcp_listeners:\n  b_one:\n    port: 2000\n  a_two:\n    scope: all\n    port: 3000\n    port_range: [3000, 3005]\nui: {}\n'
        self.assertEqual(list(tcp_policy.listeners(two)), ['a_two', 'b_one'])
        self.assertEqual(tcp_policy.listeners(two)['b_one'], {'scope': 'loopback', 'port': 2000})
        self.assertEqual(tcp_policy.listeners(two)['a_two']['port_range'], [3000, 3005])
        for bad in ['tcp_listeners:\n  x:\n    port: 80\n', 'tcp_listeners:\n  x:\n    scope: lan\n    port: 2000\n',
                    'tcp_listeners:\n  X:\n    port: 2000\n', 'tcp_listeners:\n  x: {port: 2000}\n']:
            with self.assertRaises(ValueError, msg=bad):
                tcp_policy.listeners(bad)


if __name__ == '__main__':
    unittest.main()
