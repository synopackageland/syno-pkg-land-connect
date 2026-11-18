"""Check the public CLI pin; pnpm install --frozen-lockfile verifies downloaded bytes using lock integrity."""
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = '@synopackageland/cli'
VERSION = '0.54.8'
RANGE = '~0.54.8'
INTEGRITY = 'sha512-Q+xFh+xcdxD7n4gMrAuxMueBxrKRU/UOFf13MPEG3F8sYs3C/IN9w8oCrCYjJuJ23RREJbRpClRjqvW7WaVq1w=='


def lock_pin(lock, name):
    """Return (specifier, version, integrity) of a root dependency in pnpm-lock.yaml."""
    importer = re.search(r"^      '?" + re.escape(name) + r"'?:\n        specifier: (\S+)\n        version: ([^(\s]+)",
                         lock, re.M)
    if not importer:
        return None, None, None
    version = importer.group(2)
    resolution = re.search(r"^  '?" + re.escape(name + '@' + version) + r"'?:\n    resolution: \{integrity: ([^,}]+)",
                           lock, re.M)
    return importer.group(1), version, resolution.group(1) if resolution else None


def verify_cli(root=ROOT):
    manifest = json.loads((root / 'package.json').read_text())
    specifier, version, integrity = lock_pin((root / 'pnpm-lock.yaml').read_text(), NAME)
    if (manifest['devDependencies'][NAME] != RANGE or specifier != RANGE
            or version != VERSION or integrity != INTEGRITY):
        raise RuntimeError('CLI public npm pin mismatch')
    installed = json.loads((root / 'node_modules' / NAME / 'package.json').read_text())
    if installed['version'] != VERSION:
        raise RuntimeError('CLI installed version mismatch; run pnpm install --frozen-lockfile')
