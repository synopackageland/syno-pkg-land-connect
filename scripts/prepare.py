#!/usr/bin/env python3
"""Prepare the native daemon bundles and notices for the manifest UI pipeline."""
import io, os, re, shutil, subprocess, tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

def run(args, cwd=ROOT, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True)

if __name__ == '__main__':
    os.chdir(ROOT)
    from go_toolchain import pin_go_toolchain
    from verify_cli import verify_cli
    from tcp_policy import flag as tcp_policy_flag
    verify_cli()
    pin_go_toolchain()
    for command in ['connectd', 'connect-worker', 'uninstall-reminder']:
        run(['node_modules/.bin/synopkgland', 'audit-entry', './cmd/' + command,
             '--dir', 'backend', '--arch', 'amd64,arm64,arm',
             '--permissions', 'internal/api/permissions.json', '--json'])
    # The manifest's TCP listeners, linked in as the CLI does for its own builds:
    # without it ipc.ListenTCP refuses every listener (the LAN switch's lan_probe).
    policy = tcp_policy_flag((ROOT / 'dsm-package.yaml').read_text())
    # GOARM=7 as the CLI builds armv7: every DSM armv7 platform has VFPv3.
    for arch, goenv in [('x86_64', {'GOARCH': 'amd64'}), ('aarch64', {'GOARCH': 'arm64'}), ('armv7', {'GOARCH': 'arm', 'GOARM': '7'})]:
        bundle = ROOT / 'build' / ('daemon-' + arch)
        if bundle.exists():
            shutil.rmtree(bundle)
        (bundle / 'connect').mkdir(parents=True)
        env = dict(os.environ, CGO_ENABLED='0', GOOS='linux', **goenv)
        for command in ['connectd', 'connect-worker', 'uninstall-reminder']:
            bindings = ('-s -w -X github.com/synopackageland/syno-pkg-land-sdk/ipc.packageID=SynoPkgConnect'
                        ' -X github.com/synopackageland/syno-pkg-land-sdk/ipc.executorTier=none'
                        + (' ' + policy if policy else ''))
            run(['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags=' + bindings, '-o', str(bundle / 'connect' / command), './cmd/' + command], ROOT / 'backend', env)
    ui = ROOT / 'frontend/node_modules/@synopackageland/ui'
    shutil.copy(ui / 'LICENSE', ROOT / 'LICENSES/ui-MIT.txt')
    shutil.copy(ui / 'LICENSES/THIRD_PARTY.md', ROOT / 'LICENSES/ui-THIRD_PARTY.md')
    shutil.copy(ROOT / 'frontend/node_modules/vue/LICENSE', ROOT / 'LICENSES/vue-MIT.txt')
    for name in ['@lucide/vue', '@internationalized/date', 'uqr']:
        pkg = ROOT / 'frontend/node_modules' / name
        licenses = [p for p in pkg.iterdir() if p.is_file() and re.match(r'(?i)^license.*', p.name)]
        if not licenses:
            raise RuntimeError('Missing UI dependency license: ' + name)
        for p in licenses:
            shutil.copy(p, ROOT / 'LICENSES' / (name.replace('/', '__') + '-' + p.name))
    if os.environ.get('SYNOPKGLAND_DOCKER_SSH'):
        # Keep Vue/TypeScript compilation on the configured build host and
        # bring back only its static artifact for the local SPK packer.
        env = dict(os.environ, SYNOPKGLAND_FRONTEND_EXPORT='1')
        raw = subprocess.check_output(['bash', 'scripts/frontend-check.sh', 'pnpm run build'], env=env)
        with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
            members = archive.getmembers()
            if not members or any(not (m.name == 'dist' or m.name.startswith('dist/'))
                                  or '..' in Path(m.name).parts or not (m.isfile() or m.isdir())
                                  for m in members):
                raise RuntimeError('Invalid frontend artifact')
            shutil.rmtree(ROOT / 'frontend/dist', ignore_errors=True)
            archive.extractall(ROOT / 'frontend', filter='data')
    else:
        run(['pnpm', '--dir', 'frontend', 'run', 'build'])
    # The phone browser page, checked against its build.json, served at app/m/
    from mobile_web import install
    install(ROOT / 'frontend/dist')
    run(['node', 'scripts/notices.mjs'])
