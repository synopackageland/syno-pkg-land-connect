import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
SPEC = importlib.util.spec_from_file_location('connect_build', ROOT / 'scripts/build.py')
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)


class UninstallReminderTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.work = tempfile.TemporaryDirectory(prefix='connect-wizard-test-')
        cls.renderer = Path(cls.work.name) / 'renderer'
        subprocess.run(['go', 'build', '-o', str(cls.renderer), './cmd/uninstall-reminder'],
                       cwd=ROOT / 'backend', check=True)

    @classmethod
    def tearDownClass(cls):
        cls.work.cleanup()

    def test_build_adds_nine_localized_static_and_dynamic_pages_without_changing_hooks(self):
        with tempfile.TemporaryDirectory() as directory:
            spk = Path(directory) / 'fixture.spk'
            original = subprocess.check_output(['node', '--input-type=module', '-e',
                "import {renderUninstallWizardFiles} from './node_modules/@synopackageland/cli/dist/build-spk/uninstall-wizard.js';"
                "console.log(JSON.stringify(renderUninstallWizardFiles('SynoPkgConnect','native')))"] , cwd=ROOT)
            wizards = json.loads(original)
            unchanged = {'INFO': b'checksum="payload-checksum"\n', 'package.tgz': b'untouched payload',
                         'scripts/preuninst': b'unchanged preuninst', 'scripts/postuninst': b'unchanged postuninst',
                         'conf/privilege': b'{"defaults":{"run-as":"package"}}'}
            with tarfile.open(spk, 'w') as archive:
                for name, raw in {**unchanged, **{'WIZARD_UIFILES/' + k: v.encode() for k, v in wizards.items()}}.items():
                    member = tarfile.TarInfo(name)
                    member.size, member.mode = len(raw), 0o755 if name.startswith('scripts/') else 0o644
                    archive.addfile(member, io.BytesIO(raw))
            build.add_reminder(spk, self.renderer)
            with tarfile.open(spk) as archive:
                for name, raw in unchanged.items():
                    self.assertEqual(archive.extractfile(name).read(), raw)
                for locale, suffix in build.LOCALES.items():
                    name = 'uninstall_uifile' + suffix
                    raw = archive.extractfile('WIZARD_UIFILES/' + name).read()
                    pages = json.loads(raw)
                    self.assertEqual(pages[:-1], json.loads(wizards[name]), locale)
                    self.assertEqual(len(pages[-1]['items']), 4, locale)
                    self.assertIn('dash.cloudflare.com', pages[-1]['items'][1]['desc'])
                    self.assertIn('login.tailscale.com/admin/machines', pages[-1]['items'][2]['desc'])
                    self.assertIn('app.netbird.io/peers', pages[-1]['items'][3]['desc'])
                    script = archive.extractfile('WIZARD_UIFILES/' + name + '.sh').read()
                    self.assertEqual(archive.getmember('WIZARD_UIFILES/' + name + '.sh').mode, 0o755)
                    subprocess.run(['sh', '-n'], input=script, check=True)
                    # The installed renderer is absent on this host: real shell
                    # execution must overwrite any output with valid static JSON.
                    base = Path(directory) / name
                    base.write_bytes(raw)
                    shell = base.with_name(name + '.sh')
                    shell.write_bytes(script)
                    output = Path(directory) / 'wizard.json'
                    env = dict(os.environ, SYNOPKG_TEMP_LOGFILE=str(output))
                    subprocess.run(['sh', str(shell)], env=env, check=True, capture_output=True)
                    self.assertEqual(json.loads(output.read_bytes()), pages)

    def test_missing_renderer_cannot_leak_its_error_or_partial_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'base').write_text('[{"step_title":"fallback","items":[]}]')
            renderer = root / 'renderer'
            renderer.write_text('#!/bin/sh\necho SECRET-PARTIAL\necho SECRET-ERROR >&2\nexit 1\n')
            renderer.chmod(0o755)
            script = build.reminder_script('base', 'en').replace(
                '/var/packages/SynoPkgConnect/target/daemon/payload/connect/uninstall-reminder', str(renderer))
            shell = root / 'wizard.sh'
            shell.write_text(script)
            output = root / 'output'
            result = subprocess.run(['sh', str(shell)], capture_output=True, check=True,
                                    env=dict(os.environ, SYNOPKG_TEMP_LOGFILE=str(output)))
            self.assertEqual(result.stdout + result.stderr, b'')
            self.assertEqual(output.read_bytes(), (root / 'base').read_bytes())
