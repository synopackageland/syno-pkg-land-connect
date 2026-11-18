#!/usr/bin/env python3
"""Build CLI-owned SPKs, add the Connect reminder wizard, then inspect them."""
import io
import os
import re
import subprocess
import tarfile
import tempfile
from pathlib import Path
from go_toolchain import pin_go_toolchain
from verify_cli import verify_cli

ROOT = Path(__file__).resolve().parent.parent
LOCALES = {'en': '', 'zh-TW': '_cht', 'zh-CN': '_chs', 'ja': '_jpn',
           'ko': '_krn', 'de': '_ger', 'fr': '_fre', 'es': '_spn', 'it': '_ita'}


def reminder_script(name, locale):
    # DSM consumes SYNOPKG_TEMP_LOGFILE, not stdout. On any renderer failure,
    # overwrite partial output with the full static SDK wizard + generic page.
    return f'''#!/bin/sh
base="$(dirname "$0")/{name}"
renderer=/var/packages/SynoPkgConnect/target/daemon/payload/connect/uninstall-reminder
if ! "$renderer" '{locale}' "$base" > "$SYNOPKG_TEMP_LOGFILE" 2>/dev/null; then
    cat "$base" > "$SYNOPKG_TEMP_LOGFILE"
fi
'''


def add_reminder(spk, renderer):
    """Change only uninstall wizard members; retain INFO, payload and hooks."""
    with tarfile.open(spk, 'r:') as source:
        members = source.getmembers()
        contents = {m.name: source.extractfile(m).read() for m in members if m.isfile()}
    additions = {}
    with tempfile.TemporaryDirectory(prefix='connect-wizard-') as directory:
        base = Path(directory) / 'base.json'
        for locale, suffix in LOCALES.items():
            name = 'uninstall_uifile' + suffix
            member = 'WIZARD_UIFILES/' + name
            base.write_bytes(contents[member])
            additions[member] = (subprocess.check_output(
                [str(renderer), locale, str(base), '--generic']), 0o644)
            additions[member + '.sh'] = (reminder_script(name, locale).encode(), 0o755)
    temporary = spk.with_suffix('.spk.tmp')
    try:
        with tarfile.open(temporary, 'w', format=tarfile.GNU_FORMAT) as target:
            for member in members:
                if member.name in additions:
                    continue
                target.addfile(member, io.BytesIO(contents[member.name]) if member.isfile() else None)
            for name, (raw, mode) in sorted(additions.items()):
                member = tarfile.TarInfo(name)
                member.mode, member.size = mode, len(raw)
                target.addfile(member, io.BytesIO(raw))
        temporary.replace(spk)
    finally:
        temporary.unlink(missing_ok=True)


def package_version():
    manifest = (ROOT / 'dsm-package.yaml').read_text()
    version = re.search(r'^version: (\S+)$', manifest, re.M).group(1)
    build = int(re.search(r'^build: (\d+)$', manifest, re.M).group(1))
    return f'{version}-{build:04d}'

if __name__ == '__main__':
    os.chdir(ROOT)
    verify_cli()
    pin_go_toolchain()
    cli = 'node_modules/.bin/synopkgland'
    subprocess.run([cli, 'check', '--json'], check=True)
    subprocess.run([cli, 'build', '--json'], check=True)
    renderer = ROOT / 'build' / 'uninstall-reminder-host'
    subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(renderer),
                    './cmd/uninstall-reminder'], cwd=ROOT / 'backend', check=True)
    for arch in ['x86_64', 'aarch64', 'armv7']:
        spk = ROOT / 'dist' / ('SynoPkgConnect-' + package_version() + '-' + arch + '.spk')
        add_reminder(spk, renderer)
        inspection = subprocess.check_output([cli, 'inspect', str(spk), '--json'])
        (ROOT / 'dist' / (arch + '-inspection.txt')).write_bytes(inspection)
    subprocess.run(['node', '--test', 'tests/artifact.test.mjs'], check=True)
