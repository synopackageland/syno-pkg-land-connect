import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, existsSync} from 'node:fs';
import YAML from 'yaml';
// Published on registry.npmjs.org; the integrity is the npm dist.integrity pin.
const PINS={
 "@synopackageland/ui": ["0.25.30", "sha512-tSZNGlc7OptTkuWhIUvk5szCr5YHQJ82narhgmwL6JNQL0Fd2hCkqLlaR8/yJMgMMxo0SUkKlo+9jv6wOIYywQ=="],
 "@synopackageland/sdk": ["0.53.52", "sha512-P7QAXnlHkRIyHQn0MF0EymXea2q79FbZdR3pAmF/ga+3gKEEqDV37yiygg5Fayij8syfT11TexIVR+1xvMdv6Q=="]
};
test('native Vue scaffold ships a non-root package with public pinned UI and MIT license', () => {
 for (const file of ['dsm-package.yaml','backend/cmd/app/main.go','frontend/src/main.ts','LICENSE']) assert.ok(existsSync(file), file);
 assert.match(readFileSync('dsm-package.yaml','utf8'), /package: SynoPkgConnect/);
 assert.match(readFileSync('dsm-package.yaml','utf8'), /capabilities: \{\}/);
 const pkg = JSON.parse(readFileSync('frontend/package.json','utf8'));
 const lock=YAML.parse(readFileSync('frontend/pnpm-lock.yaml','utf8'));
 for(const [name,[version,integrity]] of Object.entries(PINS)){
  // UI follows its patch range; the SDK stays exact.
  const specifier=name==='@synopackageland/ui'?'~'+version:version;
  assert.equal(pkg.dependencies[name],specifier,name);
  const dep=lock.importers['.'].dependencies[name];
  assert.equal(dep.specifier,specifier,name);
  assert.equal(dep.version.split('(')[0],version,name);
  assert.equal(lock.packages[`${name}@${version}`].resolution.integrity,integrity,name);
 }
 assert.equal(existsSync('third-party/ui'),false);
 assert.match(readFileSync('LICENSE','utf8'), /Copyright \(c\) 2026 synopkgland/);
});

test('daemon uses the SDK lifecycle budgets', () => {
 const {daemon}=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
 assert.equal(daemon.timeouts, undefined);
});

// Descriptions come only from the catalog; the manifest names the app in every language.
test('Package Center names the app in every language', () => {
 const m=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
 assert.equal(m.displayname,'Syno Connect Center');
 for (const info of Object.values(m.i18n.info)) assert.equal(info.displayname,'Syno Connect Center');
 assert.deepEqual(Object.keys(m.i18n.info).sort(),['de','es','fr','it','ja','ko','zh-CN','zh-TW']);
});

test('maintainer matches the other Syno Package Land packages', () => {
 assert.equal(YAML.parse(readFileSync('dsm-package.yaml','utf8')).maintainer,'Syno Package Land');
});
