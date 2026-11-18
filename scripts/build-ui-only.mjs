// Repackage a UI-only release with the pinned CLI's assembler, without compiling Go.
// Usage: node scripts/build-ui-only.mjs <previous-release-assets-directory> [--reuse-release-contract]
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {mkdirSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {resolve, join} from 'node:path';
import {parse} from 'yaml';
import {loadStandaloneManifest} from '@synopackageland/cli/dist/standalone/manifest.js';
import {assembleStandaloneSpk} from '@synopackageland/cli/dist/standalone/spk.js';
import {checkCgiBinary} from '@synopackageland/cli/dist/standalone/binary.js';
import {makeDescriptor} from '@synopackageland/cli/dist/ctl/descriptor.js';

const root = process.cwd();
const previous = resolve(process.argv[2]);
// Explicitly ship only UI when master also has unreleased native changes.
// The released runtime, policy, hooks and descriptor remain the authority.
const reuseReleaseContract = process.argv[3] === '--reuse-release-contract';
if (process.argv.length > (reuseReleaseContract ? 4 : 3)) throw new Error('Unexpected arguments');
const loaded = loadStandaloneManifest(root);
if (!loaded.ok) throw new Error(JSON.stringify(loaded.errors));
const manifest = loaded.manifest;
const priorVersion = `${manifest.version}-${String(manifest.build - 1).padStart(4, '0')}`;
// Native source and package policy must match the release supplying the binaries.
const priorRef = `v${priorVersion}`;
if (!reuseReleaseContract)
  execFileSync('git', ['diff', '--exit-code', priorRef, '--', 'backend', 'dsm-scripts']);
const oldManifest = execFileSync('git', ['show', `${priorRef}:dsm-package.yaml`], {encoding: 'utf8'});
const currentManifest = readFileSync('dsm-package.yaml', 'utf8');
const policy = text => {
  const value = parse(text);
  delete value.build;
  // Names the vendored phone page bundle, which a UI-only release may replace.
  delete value.ui_secret_ignores;
  if (reuseReleaseContract) delete value.ctl;
  return JSON.stringify(value);
};
if (policy(oldManifest) !== policy(currentManifest))
  throw new Error('Package policy changed; a UI-only release cannot ship it');
const work = join(root, 'build/ui-only');
const cli = join(root, 'node_modules/.bin/synopkgland');
const run = (cmd, args) => execFileSync(cmd, args, {stdio: 'inherit'});
const hash = path => createHash('sha256').update(readFileSync(path)).digest('hex');
mkdirSync(work, {recursive: true});
mkdirSync('dist', {recursive: true});
run(cli, ['check', '--json']);
run('node', ['scripts/notices.mjs']);

for (const arch of manifest.arches) {
  const baseline = join(previous, `${manifest.package}-${priorVersion}-${arch}.spk`);
  const sums = readFileSync(join(previous, 'SHA256SUMS'), 'utf8');
  if (!sums.split('\n').some(line => line === `${hash(baseline)}  ${baseline.split('/').pop()}`))
    throw new Error('Previous release checksum mismatch');
  const stage = join(work, arch);
  rmSync(stage, {recursive: true, force: true});
  mkdirSync(stage, {recursive: true});
  // Python's data filter rejects archive paths and links that escape extraction.
  run('python3', ['-c', `
import sys, tarfile
from pathlib import Path
stage = Path(sys.argv[2])
with tarfile.open(sys.argv[1]) as archive:
    archive.extractall(stage / 'outer', filter='data')
with tarfile.open(stage / 'outer/package.tgz') as archive:
    archive.extractall(stage / 'payload', filter='data')
`, baseline, stage]);
  const payload = join(stage, 'payload');
  const cgiPath = join(payload, 'ui/cgi/api.cgi');
  const problem = checkCgiBinary(root, cgiPath, arch);
  if (problem) throw new Error(JSON.stringify(problem));
  const record = JSON.parse(readFileSync(join(stage, 'outer/sdk-package.json')));
  const oldDescription = JSON.parse(readFileSync(join(payload, 'synopkgland-api.json')));
  if (oldDescription.sha256 !== record.ctl.hash) throw new Error('Previous API descriptor mismatch');
  const apiDescription = makeDescriptor({...oldDescription.payload,
    package: {...oldDescription.payload.package, version: manifest.spkVersion}});
  // Retain the source manifest's Go backend contract while supplying existing artifacts.
  const targets = {...manifest.daemon.targets, [arch]: join(payload, 'daemon/payload')};
  const outputPath = join(root, 'dist', `${manifest.package}-${manifest.spkVersion}-${arch}.spk`);
  await assembleStandaloneSpk({
    manifest: {...manifest, daemon: {...manifest.daemon, targets}}, arch, outputPath,
    cgiPath, daemonControl: join(payload, 'daemon/control'),
    appBackupControl: join(payload, 'backup/control'), apiDescription,
    apiRegistry: record.ctl.registry,
  });
  // Preserve the existing uninstall reminder pages; do not build a host renderer.
  // In release-contract mode retain every other outer member and every runtime
  // payload member too. Only UI assets/notices and descriptor version may change.
  run('python3', ['-c', `
import io, sys, tarfile, hashlib, json
from pathlib import Path
spk, baseline = map(Path, sys.argv[1:3])
reuse_contract = sys.argv[3] == 'true'
def entries(path):
    with tarfile.open(path) as archive:
        return {m.name: (m, archive.extractfile(m).read() if m.isfile() else None)
                for m in archive.getmembers()}
def packed(items, gzip=False):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w:gz' if gzip else 'w', format=tarfile.GNU_FORMAT) as out:
        for m, data in items.values():
            if data is not None: m.size = len(data)
            out.addfile(m, io.BytesIO(data) if data is not None else None)
    return buffer.getvalue()
def inner(items):
    with tarfile.open(fileobj=io.BytesIO(items['package.tgz'][1])) as archive:
        return {m.name: (m, archive.extractfile(m).read() if m.isfile() else None)
                for m in archive.getmembers()}
if reuse_contract:
    old, new = entries(baseline), entries(spk)
    before, after = inner(old), inner(new)
    def ui(name):
        return (name.startswith('ui/') and not name.startswith('ui/cgi')) or name.startswith('LICENSES/ui/')
    payload = {name: value for name, value in before.items() if not ui(name)}
    payload.update({name: value for name, value in after.items() if ui(name) or name == 'synopkgland-api.json'})
    runtime_before = {n: v[1] for n, v in before.items() if not ui(n) and n != 'synopkgland-api.json'}
    runtime_after = {n: v[1] for n, v in payload.items() if not ui(n) and n != 'synopkgland-api.json'}
    if runtime_before != runtime_after: raise RuntimeError('Released runtime changed')
    old_descriptor = json.loads(before['synopkgland-api.json'][1])
    new_descriptor = json.loads(payload['synopkgland-api.json'][1])
    new_descriptor['payload']['package']['version'] = old_descriptor['payload']['package']['version']
    new_descriptor['sha256'] = old_descriptor['sha256']
    if old_descriptor != new_descriptor: raise RuntimeError('Released API contract changed')
    raw = packed(payload, gzip=True)
    old['package.tgz'] = (new['package.tgz'][0], raw)
    info = new['INFO'][1].decode()
    import re
    info = re.sub(r'^checksum="[a-f0-9]+"$', 'checksum="' + hashlib.md5(raw).hexdigest() + '"', info, flags=re.M)
    old['INFO'] = (new['INFO'][0], info.encode())
    record = json.loads(old['sdk-package.json'][1])
    generated = json.loads(new['sdk-package.json'][1])
    record['ui_module'] = generated['ui_module']
    record['ctl']['hash'] = generated['ctl']['hash']
    old['sdk-package.json'] = (new['sdk-package.json'][0], (json.dumps(record) + '\\n').encode())
    spk.write_bytes(packed(old))
with tarfile.open(baseline) as old:
    reminders = [(m, old.extractfile(m).read()) for m in old.getmembers()
                 if m.isfile() and m.name.startswith('WIZARD_UIFILES/uninstall_uifile')]
with tarfile.open(spk) as new:
    entries = [(m, new.extractfile(m).read() if m.isfile() else None)
               for m in new.getmembers() if m.name not in {m.name for m, _ in reminders}]
with tarfile.open(spk, 'w', format=tarfile.GNU_FORMAT) as out:
    for m, data in entries + reminders:
        out.addfile(m, io.BytesIO(data) if data is not None else None)
def binaries(path):
    with tarfile.open(path) as outer:
        with tarfile.open(fileobj=outer.extractfile('package.tgz')) as inner:
            return {m.name: hashlib.sha256(data).hexdigest()
                    for m in inner.getmembers() if m.isfile()
                    for data in [inner.extractfile(m).read()] if data.startswith(b'\\x7fELF')}
before, after = binaries(baseline), binaries(spk)
if not before or before != after:
    raise RuntimeError('Native binary inventory or digest changed')
print(spk.name + ': ' + str(len(after)) + ' native binaries identical to previous release')
`, outputPath, baseline, String(reuseReleaseContract)]);
  const inspection = execFileSync(cli, ['inspect', outputPath, '--json']);
  const result = JSON.parse(inspection);
  if (!result.ok) throw new Error('SPK inspection failed');
  result.data.spk = outputPath.split('/').pop();
  writeFileSync(join(root, 'dist', `${arch}-inspection.txt`), JSON.stringify(result, null, 2) + '\n');
}
