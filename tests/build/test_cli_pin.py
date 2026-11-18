import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


class CLIPinTests(unittest.TestCase):
    def test_public_pin_and_installed_version_are_checked_before_execution(self):
        path = Path(__file__).resolve().parents[2] / 'scripts/verify_cli.py'
        spec = importlib.util.spec_from_file_location('verify_cli', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = {'devDependencies': {module.NAME: module.RANGE}}
            def lock(specifier=module.RANGE, version=module.VERSION, integrity=module.INTEGRITY):
                return (f"importers:\n\n  .:\n    devDependencies:\n      '{module.NAME}':\n"
                        f"        specifier: {specifier}\n        version: {version}(ai@7.0.137)\n\n"
                        f"packages:\n\n  '{module.NAME}@{version}':\n"
                        f"    resolution: {{integrity: {integrity}}}\n")
            installed = root / 'node_modules' / module.NAME / 'package.json'
            installed.parent.mkdir(parents=True)
            installed.write_text(json.dumps({'version': module.VERSION}))
            (root / 'package.json').write_text(json.dumps(manifest))
            (root / 'pnpm-lock.yaml').write_text(lock())
            module.verify_cli(root)
            for key, value in [('version', '0.5.0'), ('specifier', '^0.53.0'), ('integrity', 'sha512-wrong')]:
                (root / 'pnpm-lock.yaml').write_text(lock(**{key: value}))
                with self.assertRaisesRegex(RuntimeError, 'public npm pin mismatch'):
                    module.verify_cli(root)
            (root / 'pnpm-lock.yaml').write_text(lock())
            installed.write_text(json.dumps({'version': '0.5.0'}))
            with self.assertRaisesRegex(RuntimeError, 'installed version mismatch'):
                module.verify_cli(root)


if __name__ == '__main__':
    unittest.main()
