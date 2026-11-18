import hashlib
import importlib.util
import json
import re
from pathlib import Path
import shutil
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('mobile_web', ROOT / 'scripts/mobile_web.py')
mobile_web = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mobile_web)


class MobileWebTests(unittest.TestCase):
    def test_vendored_page_is_the_recorded_build(self):
        build = mobile_web.check()
        self.assertEqual(build['base'], mobile_web.BASE)

    def test_a_changed_added_or_missing_file_is_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory) / 'web'
            shutil.copytree(mobile_web.VENDOR, folder)
            mobile_web.check(folder)
            (folder / 'index.html').write_text('<!doctype html>')
            with self.assertRaisesRegex(RuntimeError, 'index.html differs'):
                mobile_web.check(folder)
            shutil.copy(mobile_web.VENDOR / 'index.html', folder / 'index.html')
            (folder / 'extra.js').write_text('')
            with self.assertRaisesRegex(RuntimeError, 'extra.js'):
                mobile_web.check(folder)
            (folder / 'extra.js').unlink()
            (folder / 'manifest.webmanifest').unlink()
            with self.assertRaisesRegex(RuntimeError, 'manifest.webmanifest'):
                mobile_web.check(folder)

    def test_only_a_committed_relative_build_is_accepted(self):
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory) / 'web'
            folder.mkdir()
            (folder / 'index.html').write_text('x')
            (folder / 'manifest.webmanifest').write_text('{}')
            files = {n: hashlib.sha256((folder / n).read_bytes()).hexdigest() for n in ['index.html', 'manifest.webmanifest']}
            def write(**changes):
                (folder / 'build.json').write_text(json.dumps({'schema': 1, 'commit': 'abc', 'dirty': False, 'base': mobile_web.BASE, 'files': files, **changes}))
            write()
            mobile_web.check(folder)
            write(dirty=True)
            with self.assertRaisesRegex(RuntimeError, 'committed'):
                mobile_web.check(folder)
            write(base='/m/')
            with self.assertRaisesRegex(RuntimeError, 'relative build'):
                mobile_web.check(folder)

    def test_notices_belong_to_the_vendored_build(self):
        build = mobile_web.check()
        text = mobile_web.NOTICES.read_text()
        self.assertEqual(text.split('\n', 1)[0], mobile_web.NOTICES_HEADER.format(commit=build['commit']))
        for name in ['react', 'react-dom', 'react-native-web', 'expo', '@expo/vector-icons']:
            self.assertRegex(text, rf'(?m)^## {re.escape(name)} \d')
        self.assertIn('## Font MaterialIcons', text)
        original = mobile_web.NOTICES
        with tempfile.TemporaryDirectory() as directory:
            stale = Path(directory) / 'notices.md'
            stale.write_text(mobile_web.NOTICES_HEADER.format(commit='0' * 40) + '\n')
            mobile_web.NOTICES = stale
            try:
                with self.assertRaisesRegex(RuntimeError, 'another build'):
                    mobile_web.check()
                stale.unlink()
                with self.assertRaisesRegex(RuntimeError, 'no third-party notices'):
                    mobile_web.check()
            finally:
                mobile_web.NOTICES = original

    def test_notices_list_bundled_packages_and_their_vendored_licenses(self):
        with tempfile.TemporaryDirectory() as directory:
            mobile = Path(directory)
            def package(folder, name, files):
                (mobile / folder).mkdir(parents=True)
                (mobile / folder / 'package.json').write_text(json.dumps({'name': name, 'version': '1.0.0', 'license': 'MIT'}))
                for path, body in files.items():
                    (mobile / folder / path).parent.mkdir(parents=True, exist_ok=True)
                    (mobile / folder / path).write_text(body)
            package('node_modules/a', 'a', {'LICENSE': 'A license', 'vendor/x/LICENSE': 'X license', 'vendor/x/i.js': ''})
            package('node_modules/b', 'b', {})
            package('node_modules/b/node_modules/@s/c', '@s/c', {'LICENSE.md': 'C license'})
            package('node_modules/unused', 'unused', {'LICENSE': 'never'})
            sources = ['/node_modules/a/vendor/x/i.js', 'node_modules/b/index.js', '/node_modules/b/node_modules/@s/c/i.js', '\x00polyfill', '/src/app.ts']
            text = mobile_web.notices(mobile, sources, 'f' * 40, folder=mobile / 'empty')
            self.assertEqual(re.findall(r'(?m)^## (.+)$', text), ['@s/c 1.0.0', 'a 1.0.0', 'b 1.0.0'])
            self.assertIn('A license', text)
            self.assertIn('Vendored in vendor/x:', text)
            self.assertIn('X license', text)
            self.assertIn('C license', text)
            self.assertNotIn('never', text)
            self.assertEqual(text.count('Vendored in'), 1)
            self.assertIn('ships no license file', text)

    def test_the_manifest_ignore_names_the_entry_bundle(self):
        bundle = mobile_web.entry_bundle()
        self.assertIn(bundle, mobile_web.check()['files'])
        self.assertIn(f'file: "app/{mobile_web.SERVED}/{bundle}"', (ROOT / 'dsm-package.yaml').read_text())

    def test_install_puts_the_page_under_app_m(self):
        with tempfile.TemporaryDirectory() as directory:
            mobile_web.install(directory)
            self.assertTrue((Path(directory) / 'm/index.html').is_file())
            self.assertTrue((Path(directory) / 'm/build.json').is_file())

    def test_the_installed_manifest_opens_the_fixed_address(self):
        with tempfile.TemporaryDirectory() as directory:
            mobile_web.install(directory)
            manifest = json.loads((Path(directory) / 'm/manifest.webmanifest').read_text())
            self.assertEqual(manifest['start_url'], '/webman/3rdparty/SynoPkgConnect/m/index.html')
            self.assertEqual(manifest['scope'], '/webman/3rdparty/SynoPkgConnect/')
            self.assertEqual(manifest['display'], 'standalone')
            mobile_web.check()
        stable = (ROOT / 'dsm-package.yaml').read_text()
        self.assertRegex(stable, r'stable_pages:\n\s+m: m/index.html')


if __name__ == '__main__':
    unittest.main()
