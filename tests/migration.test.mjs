import './catalog-fixture.mjs';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, existsSync, readdirSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import YAML from 'yaml';
test('C1 manifest uses foreground package daemon, retained-data uninstall and prepare pipeline',()=>{
 const m=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
 assert.ok(Number.isInteger(m.build)&&m.build>=84);assert.equal(m.daemon.exec,'connect/connectd');
 assert.deepEqual(m.daemon.args,['serve','${SYNOPKG_PKGDEST}/daemon/payload','${SYNOPKG_PKGVAR}']);
 assert.deepEqual(m.daemon.uninstall_args,['retain','${SYNOPKG_PKGDEST}/daemon/payload','${SYNOPKG_PKGVAR}']);
 assert.equal(m.schema,2);assert.deepEqual(m.backend,{kind:'go',dir:'backend',main:'./cmd/app'});assert.deepEqual(m.capabilities,{});assert.equal(m.privilege,undefined);assert.equal(m.portal,undefined);assert.equal(m.entry,'admin-only');assert.deepEqual(m.ui.build,{cwd:'frontend',command:['python3','../scripts/prepare.py']});
 const build=readFileSync('scripts/build.py','utf8');assert.doesNotMatch(build,/def extend|archive_bytes|spk.write_bytes|hashlib.md5/);
 const prepare=readFileSync('scripts/prepare.py','utf8');assert.doesNotMatch(prepare,/build-spk|extend\(/);
});
// Published on registry.npmjs.org; the integrity is the npm dist.integrity pin.
// CLI and templates are independently pinned published releases.
const PUBLISHED={"@synopackageland/cli": ["0.54.8", "sha512-Q+xFh+xcdxD7n4gMrAuxMueBxrKRU/UOFf13MPEG3F8sYs3C/IN9w8oCrCYjJuJ23RREJbRpClRjqvW7WaVq1w=="], "@synopackageland/templates": ["0.42.1", "sha512-ivwzfb5/toy+cB40DT2XJKnyj1I+Y96qAmjpva+Wh81qtXJ6GildAFpjIzKUrkP5d08OC0yz/6Hadz47CA0M4A=="]};
const published=Object.fromEntries(Object.entries(process.env).filter(([key])=>!['SYNOPKGLAND_SDK_DIR','SYNOPKGLAND_TEMPLATES_DIR'].includes(key)));
test('CLI and templates 0.54.8/0.42.1 are pinned to the public registry and schema 2 passes check',()=>{
 const manifest=JSON.parse(readFileSync('package.json')),lock=YAML.parse(readFileSync('pnpm-lock.yaml','utf8'));
 for(const [name,[version,integrity]] of Object.entries(PUBLISHED)){
  const specifier=name==='@synopackageland/cli'?'~'+version:version;
  assert.equal(manifest.devDependencies[name],specifier,name);
  const dep=lock.importers['.'].devDependencies[name];
  assert.equal(dep.specifier,specifier,name);
  assert.equal(dep.version.split('(')[0],version,name);
  assert.equal(lock.packages[`${name}@${version}`].resolution.integrity,integrity,name);
 }
 const check=JSON.parse(execFileSync('node_modules/.bin/synopkgland',['check','--json'],{encoding:'utf8',env:published}));
 assert.equal(check.ok,true);
});
// The shared supervision SDK is pinned to its published release and module sum.
test('backend pins the shared supervision SDK without copying packages',()=>{
 const gomod=readFileSync('backend/go.mod','utf8');
 const version='v0.53.52';
 assert.match(gomod,new RegExp('github\\.com/synopackageland/syno-pkg-land-sdk\\s+'+version.replaceAll('.','\\.')+'(?:\\s|$)'));
 assert.doesNotMatch(gomod,/^replace\b/m);
 const gosum=readFileSync('backend/go.sum','utf8');
 assert.ok(gosum.split('\n').includes('github.com/synopackageland/syno-pkg-land-sdk v0.53.52 h1:bDDSj5inqndsPmsNaBHx6kSgvpz2EY3Lew2Y9GmdGF4='));
 for(const dir of ['dsm','jsonlimits','dsmservices'])assert.equal(existsSync(`backend/internal/${dir}`),false,dir);
 assert.deepEqual(readdirSync('backend/internal/api').sort(),['operations.json','permissions.go','permissions.json','permissions_test.go']);
});
test('notices come from the templates package export',()=>{
 assert.match(readFileSync('scripts/notices.mjs','utf8'),/from '@synopackageland\/templates\/notices';/);
 assert.equal(existsSync('frontend/third-party-notices.mjs'),false);
 assert.doesNotMatch(readFileSync('frontend/vite.config.ts','utf8'),/notices/i);
});
// The DSM e2e helpers come from the CLI's e2e export; Connect keeps no copy.
test('e2e imports the DSM helpers from @synopackageland/cli/e2e',()=>{
 assert.equal(existsSync('tests/e2e/support/dsm.ts'),false);
 const manifest=JSON.parse(readFileSync('package.json'));
 assert.equal(manifest.scripts['e2e:nas'],'node --env-file-if-exists=.env node_modules/@synopackageland/cli/dist/e2e/dsm.js tunnel -- e2e run');
 for(const [name,version] of Object.entries({e2e:'0.19.0','@e2e-dev/web':'0.14.0',ai:'7.0.137','@ai-sdk/openai':'4.0.91'})) assert.equal(manifest.devDependencies[name],version,name);
 const files=['e2e.config.ts','tests/e2e-llm.test.mjs',...readdirSync('tests/e2e',{recursive:true}).filter(f=>/\.(ts|mjs)$/.test(f)).map(f=>`tests/e2e/${f}`)];
 for(const file of files) assert.doesNotMatch(readFileSync(file,'utf8'),/support\/dsm\.ts|from '\.\/dsm\.ts'/,file);
});
