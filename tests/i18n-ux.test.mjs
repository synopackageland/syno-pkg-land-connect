import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
import {join} from 'node:path';
import {TextDecoder} from 'node:util';
const dir='i18n/messages';
const locales=readdirSync(dir).filter(f=>f.endsWith('.json')).map(f=>f.slice(0,-5));
const load=l=>JSON.parse(readFileSync(join(dir,`${l}.json`),'utf8'));
const walk=d=>readdirSync(d,{withFileTypes:true}).flatMap(e=>e.isDirectory()?walk(join(d,e.name)):/\.(vue|ts)$/.test(e.name)?[join(d,e.name)]:[]);
// A concatenated key prefix such as 'ux.state.' is not itself a message.
// Expand its catalog entries; literal keys still require an exact lookup.
const used=new Set(walk('frontend/src').flatMap(f=>[...readFileSync(f,'utf8').matchAll(/\bu\('([\w.-]+)'\)|'ux\.([\w.-]+)'/g)].flatMap(m=>{
 const key=m[1]?`ux.${m[1]}`:`ux.${m[2]}`;
 if(!key.endsWith('.'))return [key];
 const keys=Object.keys(load('en')).filter(k=>k.startsWith(key));
 assert.ok(keys.length,`Unknown dynamic key prefix: ${key}`);
 return keys;
})));
test('all nine locales share the same ux keys and none is empty', () => {
 assert.equal(locales.length,9);
 const base=Object.keys(load('en')).filter(k=>k.startsWith('ux.')).sort();
 assert.ok(base.length>20);
 for (const l of locales) {
  const m=load(l);
  assert.deepEqual(Object.keys(m).filter(k=>k.startsWith('ux.')).sort(),base,l);
  for (const k of base) assert.ok(typeof m[k]==='string'&&m[k].trim(),`${l}:${k}`);
 }
});
test('every ux key referenced by the frontend exists in every locale', () => {
 assert.ok(used.size>10);
 for (const l of locales) { const m=load(l); for (const k of used) assert.ok(k in m,`${l}:${k}`); }
});
// The Han characters of a double-byte code page: GB2312 holds only simplified
// forms and Big5 only traditional ones.
const hanzi=(encoding,leads,trails)=>{const decoder=new TextDecoder(encoding);return new Set(leads.flatMap(a=>trails.map(b=>decoder.decode(Uint8Array.of(a,b)))).filter(c=>/^\p{Script=Han}$/u.test(c)))};
const range=(a,b)=>Array.from({length:b-a+1},(_,i)=>a+i);
test('Chinese catalogs stay in their own script', () => {
 const scripts={'zh-CN':hanzi('gb18030',range(0xB0,0xF7),range(0xA1,0xFE)),'zh-TW':hanzi('big5',range(0xA4,0xF9),[...range(0x40,0x7E),...range(0xA1,0xFE)])};
 for (const [l,known] of Object.entries(scripts)) for (const [k,v] of Object.entries(load(l))) {
  const foreign=[...v].filter(c=>/\p{Script=Han}/u.test(c)&&!known.has(c));
  assert.deepEqual(foreign,[],`${l}:${k}`);
 }
});
// Product names (Cloudflare Tunnel, NetBird IP) stay in English; ordinary words do not.
test('Chinese, Japanese and Korean text leaves no ordinary English word untranslated', () => {
 for (const l of ['zh-TW','zh-CN','ja','ko']) for (const [k,v] of Object.entries(load(l)))
  assert.doesNotMatch(v,/\b(?:Auth key|tunnel|token|peer|client|userspace)\b/,`${l}:${k}`);
});
// zh-CN uses mainland wording, not Taiwan wording written in simplified characters.
// DSM's own messages from the templates glossary stay word for word as DSM writes them, less the final period.
const glossary=JSON.parse(readFileSync('node_modules/@synopackageland/templates/templates/native-go/i18n/glossary.json','utf8'));
const dsmText=new Set(glossary.terms.map(term=>term.text['zh-CN'].replace(/[.。．]$/,'')));
// The app's zh-CN copy: the message catalog, the shell catalog, the SDK's
// AppShell catalog the app renders and the 'zh-CN' rows of the provider
// message tables in frontend/src.
const zhCNCopy=[
 ...Object.entries(load('zh-CN')),
 ...Object.entries(JSON.parse(readFileSync('frontend/src/shell/i18n/zh-CN.json','utf8'))).map(([k,v])=>[`shell:${k}`,v]),
 ...Object.entries(JSON.parse(readFileSync('frontend/node_modules/@synopackageland/sdk/dist/i18n/shell/zh-CN.json','utf8'))).map(([k,v])=>[`sdk-shell:${k}`,v]),
 ...walk('frontend/src').flatMap(f=>[...readFileSync(f,'utf8').matchAll(/^ *'zh-CN':\[(.*)\],?$/gm)].flatMap(m=>[...m[1].matchAll(/'([^']*)'/g)].map((s,i)=>[`${f}:${i}`,s[1]]))),
];
test('zh-CN keeps mainland wording', () => {
 assert.ok(zhCNCopy.some(([k])=>k.startsWith('frontend/src/netbirdMessages.ts')));
 assert.ok(zhCNCopy.some(([k])=>k.startsWith('sdk-shell:')));
 const taiwan=/连线|联机|帐号|帐户|透过|存取|装置|支援|进阶|设定|复原|程式|网路|登入|登出|资讯|讯息|档案|伺服器|预设|软体|视窗|萤幕|滑鼠/;
 for (const [k,v] of zhCNCopy) if(!dsmText.has(v))assert.doesNotMatch(v,taiwan,`zh-CN:${k}`);
});
// One word for "account" across the whole app: the mainland 账号, never 帐号.
test('zh-CN writes account as 账号 everywhere', () => {
 for (const [k,v] of zhCNCopy) assert.doesNotMatch(v,/帐号/,`zh-CN:${k}`);
});
// UI strings drop their final period; periods inside a string stay. An ellipsis is not a period.
test('no UI string ends with a period', () => {
 const period=/(?<!\.)[.。．]$/;
 const late=[];
 for (const l of locales) for (const [k,v] of Object.entries(load(l))) if (period.test(v.trimEnd())) late.push(`${l}:${k}`);
 for (const f of ['frontend/src/netbirdMessages.ts','frontend/src/tailscaleMessages.ts']) for (const m of readFileSync(f,'utf8').matchAll(/'((?:[^'\\]|\\.)*)'(?=[,\]])/g)) if (period.test(m[1])) late.push(`${f}:${m[1]}`);
 assert.deepEqual(late,[]);
});
