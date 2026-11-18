import {test} from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readdirSync,readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import YAML from 'yaml';
import {decodePng} from './png.mjs';
import {inspectStandaloneSpk} from '../node_modules/@synopackageland/cli/dist/standalone/inspect-spk.js';
import {renderStandaloneScript} from '../node_modules/@synopackageland/cli/dist/standalone/lifecycle.js';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {goBinaryVersion} from '../node_modules/@synopackageland/cli/dist/build-spk/go-toolchain.js';
// The CLI re-encodes the project PNGs, so compare pixels rather than bytes.
const rgba=png=>{const {width,height,bpp,px}=decodePng(png);if(bpp===4)return {width,height,px};
 const out=Buffer.alloc(width*height*4,255);for(let i=0;i<width*height;i++)px.copy(out,i*4,i*3,i*3+3);return {width,height,px:out};};
const repoIcon=size=>rgba(readFileSync(`assets/icon/icon_${size}.png`));

// ELF e_machine and class per SPK architecture.
const ELF={x86_64:[62,2],aarch64:[183,2],armv7:[40,1]};
// Go 1.26.0 crashed at start on armv7 kernel 3.10 (DSM 7.1); the SDK pins go1.26.10.
const GO='go1.26.10';
const manifest=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
const VERSION=`${manifest.version}-${String(manifest.build).padStart(4,'0')}`;
for(const arch of ['x86_64','aarch64','armv7'])test(`${arch} untouched C1 SPK includes the foreground daemon, fixed hooks and notices`,async()=>{
 const spk=`dist/SynoPkgConnect-${VERSION}-${arch}.spk`;
 const read=name=>execFileSync('tar',['-xOf',spk,name],{maxBuffer:100*1024*1024});
 const info=read('INFO').toString();const tgz=read('package.tgz');
 assert.match(info,/package="SynoPkgConnect"/);assert.ok(info.includes(`version="${VERSION}"`));assert.match(info,/precheckstartstop="yes"/);
 assert.ok(info.includes(createHash('md5').update(tgz).digest('hex')));
 const extract=name=>execFileSync('tar',['-xzOf','-',name],{input:tgz,maxBuffer:100*1024*1024});
 const dir=mkdtempSync(join(tmpdir(),'connect-artifact-'));
 for(const name of ['daemon/payload/connect/connectd','daemon/payload/connect/connect-worker','daemon/payload/connect/uninstall-reminder','daemon/control','ui/cgi/api.cgi']){
  const elf=extract(name);assert.equal(elf.subarray(0,4).toString('hex'),'7f454c46',name);
  assert.deepEqual([elf.readUInt16LE(18),elf[4]],ELF[arch],name);
  const file=join(dir,name.replaceAll('/','_'));writeFileSync(file,elf);assert.equal(goBinaryVersion(file),GO,name);
 }
 const cfg=JSON.parse(extract('daemon/config.json'));
 assert.equal(cfg.package,'SynoPkgConnect');assert.equal(cfg.exec,'connect/connectd');
 assert.deepEqual(cfg.args,['serve','${SYNOPKG_PKGDEST}/daemon/payload','${SYNOPKG_PKGVAR}']);
 assert.deepEqual(cfg.uninstall_args,['retain','${SYNOPKG_PKGDEST}/daemon/payload','${SYNOPKG_PKGVAR}']);
 assert.deepEqual(cfg.env,{});assert.deepEqual(cfg.timeouts,(YAML.parse(readFileSync('dsm-package.yaml','utf8')).daemon.timeouts ?? {start:30,stop:30,status:5,uninstall:60}));
 // CLI 0.10.3 ships dsm-scripts/ as scripts/app and wraps each in the SDK script of the same name.
 const appScripts=readdirSync('dsm-scripts');
 for(const hook of appScripts)assert.deepEqual(read(`scripts/app/${hook}`),readFileSync(`dsm-scripts/${hook}`),hook);
 for(const hook of ['start-stop-status','preupgrade','preuninst','postuninst'])assert.equal(read(`scripts/${hook}`).toString(),renderStandaloneScript(hook,{package:cfg.package,daemon:cfg,capabilities:{},appScripts,ui:{shortPaths:YAML.parse(readFileSync('dsm-package.yaml','utf8')).ui.short_paths}}));
 assert.match(read('scripts/preuninst').toString(),/exec .*daemon\/control" uninstall/);
 assert.doesNotMatch(read('scripts/preuninst').toString(),/\|\| true/);
 assert.doesNotMatch(read('conf/privilege').toString(),/"run-as"\s*:\s*"root"/);
 assert.match(info,/os_min_ver="7.2-64570"/);
 assert.match(info,/displayname="Syno Connect Center"/);assert.match(info,/displayname_cht="Syno Connect Center"/);
 assert.match(info,/maintainer="Syno Package Land"/);assert.doesNotMatch(info,/native DSM package/);
 for(const suffix of ['', '_cht','_chs','_jpn','_krn','_ger','_fre','_spn','_ita']){
  const install=JSON.parse(read(`WIZARD_UIFILES/install_uifile${suffix}`));
  assert.ok(install[0].items.some(item=>item.desc?.length>0));
  const dynamic=read(`WIZARD_UIFILES/install_uifile${suffix}.sh`).toString();
  assert.match(dynamic,/@appdata\/\$pkg/);
  assert.doesNotMatch(dynamic,/rm -|chown|mkdir/);
  const uninstall=JSON.parse(read(`WIZARD_UIFILES/uninstall_uifile${suffix}`));
  const checkbox=uninstall.flatMap(step=>step.items).flatMap(item=>item.subitems||[]).find(item=>item.key==='pkgwizard_remove_additional');
  assert.equal(checkbox?.defaultValue,false);
  assert.equal(uninstall.at(-1).items.length,4);
  assert.match(uninstall.at(-1).items[1].desc,/https:\/\/dash.cloudflare.com\//);
  const reminder=read(`WIZARD_UIFILES/uninstall_uifile${suffix}.sh`).toString();
  assert.match(reminder,/uninstall-reminder/);
  assert.match(reminder,/SYNOPKG_TEMP_LOGFILE/);
 }
 // No executor App: the uninstall wizard tells an admin to remove the Application Privileges rules by hand.
 assert.match(JSON.stringify(JSON.parse(read('WIZARD_UIFILES/uninstall_uifile'))),/Control Panel > Application Privileges after uninstall/);
 assert.match(JSON.stringify(JSON.parse(read('WIZARD_UIFILES/uninstall_uifile_cht'))),/「控制台」>「應用程式權限」中的規則/);
 // cloudflared is downloaded from its official release, never bundled.
 assert.match(extract('LICENSES/binaries/cloudflared-LICENSE').toString(),/Apache License/);
 assert.match(extract('LICENSES/APP.txt').toString(),/synopkgland/);
 assert.match(extract('LICENSES/DAEMON-LICENSE.txt').toString(),/MIT/);
 const list=execFileSync('tar',['-tzf','-'],{input:tgz}).toString();
 assert.doesNotMatch(list,/connect\/token|connect\/cloudflared|cloudflared-dependencies|cmd\/dev|\.cache|control.sock/);
 // Discovery stays private, explicit and administrator-only on every arch.
 assert.ok(list.split('\n').includes('synopkgland-api.json'));
 assert.doesNotMatch(list,/ui\/.*synopkgland-api\.json/);
 const description=JSON.parse(extract('synopkgland-api.json'));
 assert.deepEqual(description.payload.aliases,['connect']);
 assert.deepEqual(description.payload.package,{id:'SynoPkgConnect',version:/^version="([^"]+)"$/m.exec(info)?.[1],displayName:'Syno Connect Center'});
 const commands=description.payload.commands;
 const manifest=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
 const operations=JSON.parse(readFileSync(manifest.ctl.registry,'utf8')).operations;
 const declared=Object.fromEntries(Object.entries(operations).filter(([,op])=>op.ai!=='hidden'));
 assert.equal(description.version,2);
 assert.deepEqual(description.payload.operations,operations);
 assert.deepEqual(Object.keys(commands).sort(),Object.keys(declared).sort());
 for(const [id,command] of Object.entries(commands)){
  assert.equal(command.permission,'administrators');
  assert.deepEqual(command.cli,declared[id].cli,id);
  assert.deepEqual(command.flow,declared[id].flow,id);
  assert.equal(command.effect,declared[id].effect,id);
 }
 assert.deepEqual(Object.keys(description.payload.bindings).sort(),Object.keys(operations).sort());
 assert.equal(commands['ctl.ports.delete'].effect,'destructive');
 assert.equal(commands['ctl.connection.forget'].effect,'destructive');
 assert.equal(commands['ctl.connection.connect'].paramsSchema.allOf[0].properties.token.writeOnly,true);
 assert.equal(commands['ctl.ports.upsert'].paramsSchema.allOf[0].properties.name.writeOnly,true);
 assert.doesNotMatch(JSON.stringify(description),/"password"/);
 // A sign-in link is only ever a result field: no command takes or shows one.
 for(const [id,command] of Object.entries(commands))assert.doesNotMatch(JSON.stringify([command.paramsSchema,command.examples]),/loginUrl|userCode/,id);
 for(const [name,size] of [['PACKAGE_ICON.PNG',64],['PACKAGE_ICON_256.PNG',256]])assert.deepEqual(rgba(read(name)),repoIcon(size),name);
 const entries=Object.values(JSON.parse(extract('ui/config'))).flatMap(Object.entries);
 const ui=Object.fromEntries(entries)['SYNO.SDS.SynoPkgConnect.Instance'];
 // The CLI names the window icons after their content, so DSM's icon cache follows a new icon.
 assert.match(ui.icon,/^images\/app_[0-9a-f]+_\{0\}\.png$/);
 for(const size of [16,24,32,48,64,72,256])assert.deepEqual(rgba(extract(`ui/${ui.icon.replace('{0}',size)}`)),repoIcon(size),`app_${size}.png`);
 assert.equal(ui.title,'synopkgland:app_title');
 // DSM resolves synopkgland:<key> only when the entry names its texts directory.
 for(const [id,entry] of entries)if([entry.title,entry.desc].some(text=>text?.startsWith('synopkgland:')))assert.equal(entry.texts,'texts',id);
 assert.match(extract('ui/texts/cht/strings').toString(),/^app_title="Syno Connect Center"$/m);
 assert.match(extract('ui/texts/enu/strings').toString(),/^app_title="Syno Connect Center"$/m);
 const inspection=await inspectStandaloneSpk(spk);assert.deepEqual(inspection.findings,[]);
 assert.equal(inspection.icon.source,'custom');
 assert.equal(inspection.entry,'admin-only');
 // Mobile discovery exposes only static metadata and the bundled SDK entry.
 // Access to App data still goes through the administrator-only CGI.
 const mobile=JSON.parse(extract('ui/app/mobile.json'));
 assert.equal(mobile.package,'SynoPkgConnect');
 assert.equal(mobile.version,VERSION);
 assert.equal(mobile.entry,'launch.html');
 assert.equal(mobile.start,'/');
 assert.equal(mobile.minShell,1);
 assert.deepEqual(mobile.capabilities,[]);
 assert.equal(mobile.intake,undefined);
 assert.equal(mobile.portal,undefined);
 assert.deepEqual(Object.keys(mobile.name).sort(),['de','en','es','fr','it','ja','ko','zh-CN','zh-TW']);
 assert.match(extract('ui/app/launch.html').toString(),/name="viewport" content="width=device-width/);
 for(const size of [192,512]){
  const icon=decodePng(extract(`ui/app/icons/mobile-${size}.png`));
  assert.equal(icon.width,size);assert.equal(icon.height,size);
 }
 assert.equal(ui.defaultAlias,undefined);
 assert.equal(inspection.version,VERSION);
 // build.py keeps the CLI's own `inspect --json`; its DSM 7 format section must be clean.
 const saved=JSON.parse(readFileSync(`dist/${arch}-inspection.txt`,'utf8')).data;
 assert.equal(saved.synopkgland,true);
 // The only warning is the reviewed ignore for the phone browser page's icon glyph map (dsm-package.yaml).
 const reviewed=w=>w.code==='SPK_UI_SECRET_IGNORED'&&/\/m\/_expo\/static\/js\/web\/index-[0-9a-f]+\.js$/.test(w.path);
 assert.deepEqual({...saved.dsm,warnings:saved.dsm.warnings.filter(w=>!reviewed(w))},{ok:true,errors:[],warnings:[]});
 assert.equal(saved.dsm.warnings.filter(reviewed).length,1);
 assert.equal(saved.publicUi.minified,true);
 assert.equal(saved.publicUi.sourceMapFree,true);
 assert.ok(saved.publicUi.bundles>0);
 assert.deepEqual(saved.publicUi.unminified,[]);
 assert.deepEqual(saved.publicUi.sourceMaps,[]);
});
