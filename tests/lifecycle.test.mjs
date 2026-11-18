import {test,before,after,describe} from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync,spawnSync} from 'node:child_process';
import {mkdtempSync,mkdirSync,writeFileSync,readFileSync,copyFileSync,symlinkSync,existsSync,rmSync,statSync,chmodSync,realpathSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {userInfo} from 'node:os';
import {setTimeout as delay} from 'node:timers/promises';
import YAML from 'yaml';
import {createHash} from 'node:crypto';
import {appSdkVersion,sdkSource} from '../node_modules/@synopackageland/cli/dist/runtime/sdk-source.js';
import {renderStandaloneScript} from '../node_modules/@synopackageland/cli/dist/standalone/lifecycle.js';

describe('Linux package-account lifecycle', {skip:process.platform!=='linux'||process.getuid()===0},()=>{
let compiled,control,app,source,diagnostic;
before(()=>{
 compiled=mkdtempSync('/tmp/connect-c1-bin-');control=join(compiled,'control');app=join(compiled,'connectd');
 const sdk=sdkSource(appSdkVersion({kind:'go',dir:resolve('backend')}));
 execFileSync('go',['build','-trimpath','-o',control,'./daemonctl'],{cwd:sdk.goDir});
 // Only this disposable host fixture exposes run() errors. It never starts
 // a tunnel; production/SPK logging stays unchanged; no user credentials.
 source=resolve('backend/cmd/connect-worker/main.go');diagnostic=join(compiled,'diagnostic-main.go');
 const hostSource=readFileSync(source,'utf8')
  .replace('fmt.Fprintln(os.Stderr, "SynoPkgConnect: package service unavailable")','fmt.Fprintln(os.Stderr, "SynoPkgConnect: package service unavailable; fixture detail:", e)');
 assert.doesNotMatch(hostSource,/net\.Listen\("tcp"/,'connectd must not listen on TCP');
 writeFileSync(diagnostic,hostSource);
 execFileSync('go',['build','-trimpath','-o',app,'./cmd/connectd'],{cwd:'backend'});
 // Complete host executable admission before measuring lifecycle deadlines.
 // No service is launched: all binaries refuse missing arguments.
 for(const bin of [control,app])assert.equal(spawnSync(bin,[],{timeout:12000}).status,1);
});
after(()=>rmSync(compiled,{recursive:true,force:true}));
function fixture(){
 const manifest=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
 assert.equal(manifest.daemon?.args[0],'serve','manifest must delegate foreground service to C1');
 const root=mkdtempSync('/tmp/connect-c1-');const target=join(root,'target');const data=join(root,'var');const payload=join(target,'daemon/payload');
 mkdirSync(join(payload,'connect'),{recursive:true});mkdirSync(data,{mode:0o700});
 // Each disposable package gets the real SDK path and linker binding. Only
 // test overlays relocate Connect's package constant; no production bypass.
 const packageID='ConnectFixture'+createHash('sha256').update(root).digest('hex').slice(0,16);
 const anchor=join('/var/packages',packageID),run=join(anchor,'var/run');
 mkdirSync(anchor);symlinkSync(data,join(anchor,'var'));mkdirSync(run,{mode:0o700});
 const localipc=resolve('backend/internal/localipc/socket.go'),relocated=join(root,'socket.go'),overlay=join(root,'overlay.json');
 writeFileSync(relocated,readFileSync(localipc,'utf8').replace('/var/packages/SynoPkgConnect/var/run',run));
 const ingress=resolve('backend/internal/ingress/protocol.go'),peer=join(root,'protocol.go');
 writeFileSync(peer,readFileSync(ingress,'utf8').replace('/var/packages/SynoPkgConnect/var/ingress/control.sock',join(anchor,'var/ingress/control.sock')));
 writeFileSync(overlay,JSON.stringify({Replace:{[source]:diagnostic,[localipc]:relocated,[ingress]:peer}}));
 const fixtureApp=join(payload,'connect/connectd'),worker=join(payload,'connect/connect-worker');
 symlinkSync(control,join(target,'daemon/control'));copyFileSync(app,fixtureApp);
 execFileSync('go',['build','-trimpath','-overlay='+overlay,
  '-ldflags=-X github.com/synopackageland/syno-pkg-land-sdk/ipc.packageID='+packageID+' -X github.com/synopackageland/syno-pkg-land-sdk/ipc.executorTier=none',
  '-o',worker,'./cmd/connect-worker'],{cwd:'backend'});
 assert.equal(spawnSync(worker,[],{timeout:12000}).status,1);
 // Host fixture uses the current non-root account; NAS config stays SynoPkgConnect.
 // CLI 0.7.1 controllers require their own uninstall budget (minimum 2 s).
 const cfg={package:userInfo().username,exec:manifest.daemon.exec,args:manifest.daemon.args,uninstall_args:manifest.daemon.uninstall_args,env:{},timeouts:{start:5,stop:2,status:1,uninstall:2}};
 writeFileSync(join(target,'daemon/config.json'),JSON.stringify(cfg));
 const call=action=>spawnSync(control,[action,target,data],{timeout:12000,encoding:'utf8'});
 const success=action=>{const r=call(action);const log=join(data,'daemon/daemon.log');const diagnostic=r.status!==0&&existsSync(log)?readFileSync(log,'utf8'):'';assert.equal(r.status,0,`${action}: ${r.stderr}${diagnostic}`);};
 const socket=join(run,'control.sock');
 const record=()=>JSON.parse(readFileSync(join(data,'daemon/daemon.pid'),'utf8'));
 // Cloudflare's pasted token and the connection's durable gate.
 const token=join(data,'connect/cloudflare/token');const config=join(data,'connect/connections/cloudflare/provider.json');
 const putToken=()=>{mkdirSync(join(data,'connect/cloudflare'),{recursive:true,mode:0o700});mkdirSync(join(data,'connect/connections/cloudflare'),{recursive:true,mode:0o700});writeFileSync(token,'SYNTHETIC-UNINSTALL-FIXTURE',{mode:0o600});writeFileSync(config,JSON.stringify({provider:'cloudflare',enabled:true,settings:{mode:'token',hostname:'nas.example.com',confirmExposure:true}}),{mode:0o600});};
 const observationDir=join('/tmp',`SynoPkgConnect-${userInfo().uid}-ports-${createHash('sha256').update(realpathSync(data)).digest('hex').slice(0,16)}`);
 const close=()=>{success('stop');rmSync(anchor,{recursive:true,force:true});rmSync(root,{recursive:true,force:true});rmSync(observationDir,{recursive:true,force:true});};
 return {root,target,data,payload,app:fixtureApp,cfg,call,success,socket,record,token,config,putToken,observationDir,close};
}
async function ready(f){
 for(let i=0;i<100;i++){
  if(existsSync(f.socket)){
   const r=spawnSync(f.app,['status',f.payload,f.data],{timeout:1000});
   if(r.status===0)return;
  }
  await delay(20);
 }
 const log=join(f.data,'daemon/daemon.log');
 const status=spawnSync(f.app,['status',f.payload,f.data],{timeout:1000,encoding:'utf8'});
 assert.fail(`connectd socket not ready: ${status.stderr}${existsSync(log)?readFileSync(log,'utf8'):''}`);
}

test('C1 owns foreground connectd start, duplicate start, status, stop, restart and normal uninstall revoke',async()=>{
 const f=fixture();
 try{
  assert.equal(f.call('status').status,3);f.success('start');await ready(f);
  const observation=join(f.observationDir,'ports-observation.json');
  assert.equal(existsSync(join(f.data,'ports-observation.json')),false,'observation must never write the persistent store');
  // With no connection enabled the C-PORT monitor does not run, so readers
  // see the plan as pending a provider without any runtime state.
  await delay(300);
  assert.equal(existsSync(observation),false,'an idle daemon must not run the port monitor');
  const first=f.record();f.success('start');assert.equal(f.record().child,first.child);
  f.success('status');assert.equal(statSync(f.socket).mode&0o777,0o600);assert.equal(existsSync(f.token),false);
  const socketBefore=statSync(f.socket);
  assert.equal(spawnSync(f.app,['serve',f.payload,f.data],{timeout:1000}).status,1,'exclusive app lock must refuse duplicate serve');
  assert.equal(statSync(f.socket).ino,socketBefore.ino,'lock refusal must not unlink live control socket');
  f.putToken();f.success('stop');assert.equal(existsSync(f.socket),false);assert.equal(existsSync(f.token),true,'stop must preserve token');
  f.success('start');await ready(f);f.success('restart');await ready(f);assert.equal(existsSync(f.token),true,'restart is not revoke');
  f.success('uninstall');assert.equal(existsSync(f.token),false);assert.equal(JSON.parse(readFileSync(f.config)).enabled,false);
  assert.equal(existsSync(f.socket),false);assert.equal(f.call('status').status,3);
 }finally{f.close();}
});

test('C1 retained-data uninstall preserves an enabled own domain for the next install',async()=>{
 const f=fixture();
 try{
  f.success('start');await ready(f);f.success('stop');
  const cloud=join(f.data,'connect/cloudflare'),domain=join(cloud,'domain'),home=join(cloud,'home/.cloudflared');
  mkdirSync(domain,{recursive:true,mode:0o700});mkdirSync(home,{recursive:true,mode:0o700});
  const files={
   [join(domain,'domain.json')]:JSON.stringify({hostname:'photos.example.com',tunnelId:'11111111-2222-4333-8444-555555555555'}),
   [join(domain,'credentials.json')]:JSON.stringify({AccountTag:'a'.repeat(32),TunnelID:'11111111-2222-4333-8444-555555555555',TunnelSecret:Buffer.alloc(32).toString('base64')}),
   [join(home,'cert.pem')]:'SYNTHETIC-RETAINED-AUTHORIZATION',
   [join(cloud,'settings.json')]:JSON.stringify({enabled:true,mode:'domain'}),
   [f.config]:JSON.stringify({provider:'cloudflare',enabled:true,settings:{mode:'domain',subdomain:'photos',confirmExposure:true},portBindings:null}),
  };
  for(const [path,raw] of Object.entries(files))writeFileSync(path,raw,{mode:0o600});
  f.success('uninstall');
  for(const [path,raw] of Object.entries(files))assert.equal(readFileSync(path,'utf8'),raw,path);
  assert.equal(f.call('status').status,3,'retained identity must not leave a process running');
  f.success('start');await ready(f);
  for(const [path,raw] of Object.entries(files))assert.equal(readFileSync(path,'utf8'),raw,path);
 }finally{f.close();}
});

test('C1 killed connectd worker comes back without a manual start',async()=>{
 const f=fixture();
 try{
  f.success('start');await ready(f);const serving=f.record().child;
  const worker=()=>Number(spawnSync('pgrep',['-P',String(serving)],{encoding:'utf8'}).stdout.trim().split('\n')[0]);
  const first=worker();assert.ok(first>0,'serve must run its worker as a child');
  // Signal only this disposable fixture's own worker.
  process.kill(first,'SIGKILL');
  for(let i=0;i<200&&!(worker()>0&&worker()!==first);i++)await delay(20);
  assert.notEqual(worker(),first,'the worker must be started again');
  await ready(f);f.success('status');assert.equal(f.record().child,serving,'the supervisor child keeps running');
 }finally{f.close();}
});

test('C1 host fixtures can start concurrently without sharing TCP listeners',async()=>{
 const first=fixture(),second=fixture();
 try{
  first.success('start');await ready(first);
  second.success('start');await ready(second);
  first.success('status');second.success('status');
  assert.notEqual(first.record().child,second.record().child);
 }finally{
  try{second.close();}finally{first.close();}
 }
});

test('C1 forced stop leaves stale app socket and uninstall revokes offline under the daemon lock',async()=>{
 const f=fixture();
 try{
  f.success('start');await ready(f);f.putToken();const child=f.record().child;
  // Signal only this disposable, witnessed fixture process; never product PID inventory.
  process.kill(child,'SIGSTOP');f.success('stop');assert.equal(f.call('status').status,3);
  assert.equal(statSync(f.socket).isSocket(),true,'SIGKILL must leave the app socket');
  // start_failed leaves the same state. Only the exclusive daemon lock, now
  // free, retires the stale app socket; uninstall must not require a start.
  f.success('uninstall');
  assert.equal(existsSync(f.socket),false);assert.equal(existsSync(f.token),false);
  assert.equal(JSON.parse(readFileSync(f.config)).enabled,false);
 }finally{f.close();}
});

test('C1 uninstall propagates token cleanup failure and retains durable disabled state',async()=>{
 const f=fixture();
 try{
  f.success('start');await ready(f);f.success('stop');mkdirSync(f.token,{recursive:true});writeFileSync(join(f.token,'owned-fixture'),'synthetic');
  const result=f.call('uninstall');assert.equal(result.status,1);assert.match(result.stderr,/package service unavailable/);
  assert.equal(JSON.parse(readFileSync(f.config)).enabled,false,'revocation persisted before cleanup error');
  assert.equal(existsSync(f.token),true);assert.equal(f.call('status').status,3);
  rmSync(f.token,{recursive:true});f.success('uninstall');
 }finally{f.close();}
});

test('C1 immediately exiting foreground service with restarts off refuses start',()=>{
 const f=fixture();
 try{
  // The SDK now defaults to restarting a failed child; this fixture tests
  // the explicit no-restart policy's original immediate-exit contract.
  writeFileSync(join(f.target,'daemon/config.json'),JSON.stringify({...f.cfg,restart:{mode:'off'}}));
  writeFileSync(join(f.data,'connect'),'not a directory');
  assert.equal(f.call('start').status,1);assert.equal(f.call('status').status,1);assert.equal(existsSync(f.socket),false);
  const status=f.call('status-json');
  assert.equal(status.status,1);
  const snapshot=JSON.parse(status.stdout);
  assert.equal(snapshot.state,'failed');
  assert.equal(snapshot.reason,'DAEMON_PERMANENT_ERROR');
  assert.equal(snapshot.last_exit?.code,1);
 }finally{f.close();}
});

test('C1 default restart accepts supervision but never reports a crashed foreground service ready',()=>{
 const f=fixture();
 try{
  writeFileSync(join(f.data,'connect'),'not a directory');
  f.success('start');
  const status=f.call('status-json');
  assert.equal(status.status,1);
  const snapshot=JSON.parse(status.stdout);
  assert.equal(snapshot.state,'backoff');
  assert.equal(snapshot.reason,'DAEMON_RESTART_BACKOFF');
  assert.equal(snapshot.last_exit?.code,1);
  assert.ok(snapshot.attempts>=1);
  assert.equal(f.call('status').status,1);
  assert.equal(existsSync(f.socket),false);
 }finally{f.close();}
});

test('C1 supervisor death retires its dead child and uninstall revokes behind the stale Linux socket',async()=>{
 const f=fixture();let child;
 try{
  f.success('start');await ready(f);f.putToken();const r=f.record();child=r.child;process.kill(r.supervisor,'SIGKILL');await delay(100);
  assert.equal(f.call('status').status,1);
  if(process.platform!=='linux'){
   assert.equal(f.call('stop').status,1,'host without PDEATHSIG must refuse surviving child');
   process.kill(child,'SIGTERM');
  }
  for(let i=0;i<100;i++){
   if(f.call('stop').status===0){child=undefined;break;}
   await delay(20);
  }
  assert.equal(child,undefined,'isolated child not retired');
  if(process.platform==='linux'){
   // C1 uses PDEATHSIG=SIGKILL on Linux. The killed app cannot unlink its
   // socket, even after C1 safely retires the dead child's PID inventory.
   assert.equal(statSync(f.socket).isSocket(),true,'PDEATHSIG must leave the app socket');
   // A dead supervisor leaves no app lock holder. Existing offline revoke
   // takes that lock, removes the abandoned socket and revokes saved state.
   f.success('uninstall');
   assert.equal(existsSync(f.socket),false,'locked offline revoke removes the stale socket');
   assert.equal(existsSync(f.token),false,'offline revoke removes saved credentials');
   assert.equal(JSON.parse(readFileSync(f.config)).enabled,false,'offline revoke disables the connection');
  }else{
   assert.equal(existsSync(f.socket),false,'fixture SIGTERM must clean the app socket');
  }
  // Starting again remains possible after the offline cleanup.
  f.success('start');await ready(f);f.success('uninstall');
 }finally{
  if(child){try{process.kill(child,'SIGCONT');process.kill(child,'SIGTERM');}catch{/* fixture already dead */}}
  f.close();
 }
});

test('generated preuninst hook preserves controller uninstall failure',()=>{
 const manifest=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
 const f=fixture();
 try{
  const hook=join(f.root,'preuninst');
  // Only relocate the fixed controller path to the isolated host fixture.
  const script=renderStandaloneScript('preuninst',manifest).replaceAll('"/var/packages/SynoPkgConnect/target/daemon/control"',JSON.stringify(control));
  writeFileSync(hook,script);chmodSync(hook,0o755);
  mkdirSync(join(f.data,'connect'),{mode:0o700});mkdirSync(f.token,{recursive:true});writeFileSync(join(f.token,'fixture'),'synthetic');
  assert.equal(spawnSync('/bin/sh',[hook],{timeout:12000,env:{PATH:process.env.PATH,SYNOPKG_PKGDEST:f.target,SYNOPKG_PKGVAR:f.data}}).status,1);
  assert.equal(JSON.parse(readFileSync(f.config)).enabled,false);
 }finally{f.close();}
});

// DSM runs the outgoing package preuninst hook for both removal and upgrade.
test('generated upgrade preuninst stops the daemon without revoking saved connections',async()=>{
 const f=fixture();
 try{
  f.success('start');await ready(f);f.putToken();
  const before=readFileSync(f.config,'utf8');
  const manifest=YAML.parse(readFileSync('dsm-package.yaml','utf8'));
  const hook=join(f.root,'preuninst');
  writeFileSync(hook,renderStandaloneScript('preuninst',manifest).replaceAll('"/var/packages/SynoPkgConnect/target/daemon/control"',JSON.stringify(control)));
  const result=spawnSync('/bin/sh',[hook],{timeout:12000,env:{PATH:process.env.PATH,SYNOPKG_PKG_STATUS:'UPGRADE',SYNOPKG_PKGDEST:f.target,SYNOPKG_PKGVAR:f.data}});
  assert.equal(result.status,0);
  assert.equal(existsSync(f.socket),false,'upgrade must stop the outgoing daemon');
  assert.equal(existsSync(f.token),true,'upgrade must preserve credentials');
  assert.equal(readFileSync(f.config,'utf8'),before,'upgrade must preserve enabled intent');
 }finally{f.close();}
});
});
