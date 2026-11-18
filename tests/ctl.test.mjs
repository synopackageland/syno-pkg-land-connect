import './catalog-fixture.mjs';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync,spawnSync} from 'node:child_process';
import {mkdtempSync,rmSync,mkdirSync,writeFileSync,readFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import * as tar from 'tar';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {loadStandaloneManifest} from '../node_modules/@synopackageland/cli/dist/standalone/manifest.js';
import {buildApiDescription} from '../node_modules/@synopackageland/cli/dist/standalone/api-description.js';
import {inspectApiDescription} from '../node_modules/@synopackageland/cli/dist/standalone/api-description-inspect.js';
import {ajv} from '../node_modules/@synopackageland/cli/dist/ctl/descriptor.js';
import {CtlStore} from '../node_modules/@synopackageland/cli/dist/ctl/store.js';
import {runAiTool} from '../node_modules/@synopackageland/cli/dist/ctl/ai-tools.js';
import {CtlClient} from '../node_modules/@synopackageland/cli/dist/ctl/service.js';

const loaded=loadStandaloneManifest(resolve('.'));
assert.equal(loaded.ok,true,JSON.stringify(loaded.errors));
const built=await buildApiDescription(loaded.manifest,undefined,async(command,args,options)=>{
 const r=spawnSync(command,args,{...options,encoding:'utf8'});
 assert.equal(r.status,0,r.stderr);
 return {status:r.status,stdout:r.stdout};
});
const {description,registry}=built;
const commands=description.payload.commands;
const business=Object.fromEntries(Object.entries(commands).filter(([,c])=>c.ai!=='preview'));
const expected={
 'app.status':'read','ctl.connection.status':'read','ctl.connection.disconnect':'write',
 'ctl.ports.list':'read','ctl.ipv6.status':'read','ctl.ports.upsert':'write',
 'ctl.ports.delete':'destructive','ctl.ports.dsmset':'write','ctl.ports.followset':'write','binaries.status':'read',
 'integration.status':'read','ctl.connection.connect':'write','connection.check':'read',
 'ctl.connection.forget':'destructive','binaries.check':'write','binaries.update':'write','binaries.rollback':'write',
 'ctl.cloudflare.access.status':'read','ctl.cloudflare.access.set':'write','ctl.cloudflare.access.sync':'write',
 'advisor.recommend':'read','ctl.diagnose':'read','external.status':'read','external.set':'write',
};

test('actual Go registry generates every declared administrator command and passes ctl inspect',()=>{
 assert.deepEqual(Object.fromEntries(Object.entries(business).map(([id,c])=>[id,c.effect])),expected);
 for(const c of Object.values(commands)){
  assert.equal(c.permission,'administrators');
  assert.equal(registry[c.flow.method??c.flow.start],'administrators');
  const validate=ajv.compile(c.paramsSchema);
  assert.ok(c.examples.length);
  for(const example of c.examples)assert.equal(validate(example),true,JSON.stringify(validate.errors));
  assert.equal(validate({unexpected:'value'}),false,c.cli.join(' '));
 }
 assert.deepEqual(inspectApiDescription(JSON.stringify(description),{id:loaded.manifest.package,version:loaded.manifest.spkVersion},description.sha256,registry),[]);
 const changed={...registry};changed['ctl.ports.upsert']='authenticated';
 assert.match(inspectApiDescription(JSON.stringify(description),{id:loaded.manifest.package,version:loaded.manifest.spkVersion},description.sha256,changed).join(' '),/BINDING_MISMATCH/);
});

test('every command declares its result and recommend names exactly the shared catalog',()=>{
 for(const [id,c] of Object.entries(commands)){assert.ok(c.resultSchema,id);ajv.compile(c.resultSchema);}
 const catalog=JSON.parse(readFileSync('backend/internal/advisor/catalog.json','utf8'));
 const result=commands['advisor.recommend'].resultSchema.properties;
 assert.deepEqual(result.options.items.properties.id.enum,catalog.options.map(o=>o.id));
 assert.deepEqual(result.options.items.properties.https.enum,[...new Set(catalog.options.map(o=>o.https))]);
 const set=ajv.compile(commands['external.set'].paramsSchema);
 assert.equal(set({hostname:'nas.example.com',httpsPort:443}),true);
 assert.equal(set({hostname:''}),true);
 for(const bad of [{},{hostname:'https://nas.example.com'},{hostname:'nas.example.com/x'},{hostname:'nas.example.com',httpsPort:0},{hostname:'nas.example.com',enable_https:true}])assert.equal(set(bad),false,JSON.stringify(bad));
 const advice=ajv.compile(commands['external.status'].resultSchema);
 assert.equal(advice({state:'not-set',current:{hostname:''},suggested:{hostname:'nas.example.com',httpsPort:443,source:'cloudflare'}}),true);
 assert.equal(advice({state:'no-stable-address',reason:'temporary-address',current:{hostname:''},suggested:null}),true);
 assert.deepEqual(result.goal.enum,catalog.goals);
 assert.deepEqual(commands['advisor.recommend'].paramsSchema.properties.goal.enum,catalog.goals);
 const connection=ajv.compile(commands['ctl.connection.connect'].resultSchema);
 assert.equal(connection({provider:'netbird',state:'connector-required',code:'binary_required',next:[]}),true);
 assert.equal(connection({state:'connected'}),false);
});

test('port schemas reject unsafe values and retain explicit exposure confirmation',()=>{
 const upsert=ajv.compile(commands['ctl.ports.upsert'].paramsSchema);
 const valid={name:'Service',protocol:'https',port:7443,enabled:true,confirmExposure:true};
 assert.equal(upsert(valid),true);
 for(const override of [{port:0},{port:65536},{port:1.5},{protocol:'udp'},{enabled:true,confirmExposure:false},{name:''},{id:'dsm-http'},{owner:'admin'}])assert.equal(upsert({...valid,...override}),false,JSON.stringify(override));
 assert.equal(upsert({name:'Disabled service',protocol:'tcp',port:7443,enabled:false}),true);
 const dsm=ajv.compile(commands['ctl.ports.dsmset'].paramsSchema);
 for(const params of [{id:'dsm-http',enabled:true},{id:'dsm-http',enabled:true,confirmExposure:false},{id:'custom-00000000000000000000000000000000',enabled:false}])assert.equal(dsm(params),false);
 assert.equal(dsm({id:'dsm-http',enabled:false}),true);
 assert.equal(ajv.validate(commands['ctl.ports.delete'].paramsSchema,{id:'dsm-http'}),false);
 assert.equal(ajv.validate(commands['binaries.status'].paramsSchema,{product:'arbitrary'}),false);
});

// sample is a minimal daemon answer of each command's declared shape.
function sample(method,params){
 if(method.endsWith('.preview'))return {method:method.slice(0,-8),effect:commands[method.slice(0,-8)].effect,requiresConfirmation:true};
 if(method.startsWith('ctl.cloudflare.access.'))return {state:'off',revision:0,working:false,emailCount:0,endpointCount:0,protectedCount:0};
 if(method.startsWith('ctl.ports.'))return {entries:[],detection:'ready',mappingState:'ready',revision:1};
 if(method.startsWith('ctl.connection.'))return {provider:params.provider??'multiple',state:'connecting',simulation:false,loggedIn:false,networkReady:false,externalVerified:false};
 if(method==='connection.check')return {provider:params.provider,state:'checking',externalVerified:false};
 if(method.startsWith('binaries.'))return {product:params.product,busy:true,current:'',runtimeVersion:'',latest:'',previous:'',checkedAt:'',verification:'',updateAvailable:false,phase:'queued',error:''};
 if(method==='external.set')return {changed:true,previous:{hostname:''},advice:{state:'matches',current:{hostname:params.hostname,https_port:params.httpsPort},suggested:{hostname:params.hostname,httpsPort:443,source:'cloudflare'}}};
 if(method==='integration.status')return {state:'not-enabled',reason:'not-requested',packageRunning:'unknown',scheduler:'not-probed'};
 return {};
}

async function fixture(t, respond=sample){
 const root=mkdtempSync(join(tmpdir(),'connect-ctl-'));t.after(()=>rmSync(root,{recursive:true,force:true}));
 const sessions=new Map();
 const store=new CtlStore(root,{get:async key=>sessions.get(key)??null,set:async(key,value)=>{sessions.set(key,value);},delete:async key=>{sessions.delete(key);}});
 const host={name:'fixture',origin:'https://fixture.invalid',username:'admin',approved:{SynoPkgConnect:{version:loaded.manifest.spkVersion}}};
 await store.add(host,{origin:host.origin,username:host.username,cookie:'fixture-cookie',token:'fixture-token'});
 const calls=[];
 const client=new CtlClient(store,host,async(path,body)=>{
  if(path==='/webapi/entry.cgi')return {success:true,data:{total:1,packages:[{id:'SynoPkgConnect',version:loaded.manifest.spkVersion}]}};
  if(body.method==='synopkgland.describe')return {success:true,data:description};
  calls.push(body);
  return {success:true,data:respond(body.method,body.params)};
 });
 return {client,calls};
}

test('ctl list discovery, dry-run and missing-yes gate every state-changing command',async t=>{
 const {client,calls}=await fixture(t);
 const listing=await client.discover();
 assert.deepEqual(listing.diagnostics,[]);
 assert.equal(Object.keys(listing.apps[0].payload.commands).length,Object.keys(commands).length);
 for(const [id,params] of [
  ['ctl.connection.connect',{provider:'netbird',token:'private-setup-key',managementUrl:'https://private.example.invalid',loginOrigins:['https://private-login.example.invalid'],confirmManagementPolicy:true}],
  ['ctl.connection.disconnect',{provider:'tailscale'}],
  ['ctl.connection.forget',{provider:'ipv6'}],
  ['binaries.update',{product:'tailscale'}],
  ['binaries.rollback',{product:'tailscale'}],
  ['ctl.ports.upsert',{name:'private-service-token',protocol:'https',port:7443,enabled:true,confirmExposure:true}],
  ['ctl.ports.delete',{id:'custom-00000000000000000000000000000000'}],
  ['ctl.ports.dsmset',{id:'dsm-http',enabled:false}],
  ['external.set',{hostname:'nas.example.com',httpsPort:443}],
 ]){
  const path=commands[id].cli,args=['--input','-'],stdin=JSON.stringify(params);
  const dry=await client.execute('connect',path,args,{stdin,dryRun:true});
  assert.equal(dry.dryRun,true);assert.equal(dry.request.effect,commands[id].effect);
  assert.doesNotMatch(JSON.stringify(dry),/private-service-token|private-setup-key|private\.example|private-login/);
  await assert.rejects(client.execute('connect',path,args,{stdin}),e=>e.code==='CTL_CONFIRM_REQUIRED'&&!JSON.stringify(e).includes('private-service-token'));
  assert.deepEqual(calls,[]);
  await client.execute('connect',path,args,{stdin,yes:true,wait:false});
  assert.equal(calls.length,1);assert.equal(calls[0].method,commands[id].flow.method??commands[id].flow.start);
  assert.deepEqual(calls[0].params,params);calls.length=0;
 }
 await assert.rejects(client.execute('connect',['ports','upsert'],['--name','private-service-token'],{dryRun:true}),e=>e.code==='CTL_PARAMS_INVALID');
 assert.deepEqual(calls,[]);
});

test('pinned CLI check accepts the command manifest without building an SPK',()=>{
 const output=JSON.parse(execFileSync('node_modules/.bin/synopkgland',['check','--json'],{encoding:'utf8'}));
 assert.equal(output.ok,true,JSON.stringify(output));
});

test('connection schema validates provider choices, exposure consent and private JSON-only input',async t=>{
 const validate=ajv.compile(commands['ctl.connection.connect'].paramsSchema);
 for(const params of [{provider:'fake'},{provider:'cloudflare',mode:'quick'},{provider:'ipv6',confirmExposure:false},{provider:'tailscale',token:12},{provider:'tailscale',mappings:[{localPort:0,protocol:'http',publicPort:443}]},{provider:'netbird',owner:'admin'}])assert.equal(validate(params),false,JSON.stringify(params));
 const {client,calls}=await fixture(t);
 await assert.rejects(client.execute('connect',['connection','connect'],['--token','private-key','--provider','netbird'],{dryRun:true}),e=>e.code==='CTL_PARAMS_INVALID');
 assert.deepEqual(calls,[]);
 for(const id of ['connection.check','binaries.status','integration.status']){
  const params=commands[id].examples[0];
  await client.execute('connect',commands[id].cli,['--input','-'],{stdin:JSON.stringify(params),yes:true,wait:false});
  assert.deepEqual(calls.pop(),{method:commands[id].flow.method??commands[id].flow.start,params});
 }
});

test('CLI inspect reads all command metadata and detects registry tampering without a release build',async t=>{
 const root=mkdtempSync(join(tmpdir(),'connect-ctl-inspect-'));t.after(()=>rmSync(root,{recursive:true,force:true}));
 const payload=join(root,'payload');mkdirSync(payload);
 writeFileSync(join(payload,'synopkgland-api.json'),JSON.stringify(description));
 await tar.c({gzip:true,file:join(root,'package.tgz'),cwd:payload},['synopkgland-api.json']);
 const checksum=createHash('md5').update(readFileSync(join(root,'package.tgz'))).digest('hex');
 writeFileSync(join(root,'INFO'),`package="SynoPkgConnect"\nversion="${loaded.manifest.spkVersion}"\narch="x86_64"\nos_min_ver="7.1-42661"\nchecksum="${checksum}"\n`);
 const metadata={schema:2,backend:'go',capabilities:[],ctl:{hash:description.sha256,registry}};
 const inspect=async()=>{
  writeFileSync(join(root,'sdk-package.json'),JSON.stringify(metadata));
  const spk=join(root,'metadata-only.spk');
  await tar.c({file:spk,cwd:root},['INFO','package.tgz','sdk-package.json']);
  const result=spawnSync('node_modules/.bin/synopkgland',['inspect',spk,'--json'],{encoding:'utf8'});
  const output=JSON.parse(result.stdout);
  // This is intentionally an incomplete metadata fixture, never an installable
  // App. Check only ctl findings; unrelated DSM/payload errors are expected.
  assert.equal(output.ok,false);
  assert.equal(output.data.synopkgland,true);
  assert.ok(output.data.findings.length>0);
  return output.data.findings.filter(f=>f.startsWith('CTL_'));
 };
 assert.deepEqual(await inspect(),[]);
 metadata.ctl.registry={...registry,'ctl.ports.upsert':'authenticated'};
 assert.match((await inspect()).join(' '),/CTL_COMMAND_BINDING_MISMATCH/);
});


test('every draft AI tool calls only its preview even if a caller supplies yes',async t=>{
 const {client,calls}=await fixture(t);
 for(const [method,op] of Object.entries(commands).filter(([,c])=>c.ai==='draft')){
  const result=await runAiTool(client,'connect',method,op.examples[0],{yes:true});
  assert.equal(result.confirmationRequired,true);
  assert.equal(result.draft.dryRun,true);
  assert.deepEqual(calls.map(c=>c.method),[op.preview]);
  calls.length=0;
 }
});

test('binary job flows retain product, wait through pending, and never replay a failed start',async t=>{
 let polls=0;
 const {client,calls}=await fixture(t,(method,params)=>{
  const value=sample(method,params);
  if(method==='binaries.status' && ++polls>1)return {...value,busy:false,phase:'ready',current:'1.1.0'};
  return value;
 });
 const result=await client.execute('connect',['binaries','update'],['--product','tailscale'],{yes:true});
 assert.equal(result.busy,false);assert.equal(result.current,'1.1.0');
 assert.deepEqual(calls.map(c=>c.method),['binaries.update','binaries.status','binaries.status']);
 assert.ok(calls.every(c=>c.params.product==='tailscale'));
 const failed=await fixture(t,(method,params)=>({...sample(method,params),phase:method==='binaries.status'?'failed':'queued'}));
 await assert.rejects(failed.client.execute('connect',['binaries','update'],['--product','tailscale'],{yes:true}),e=>e.code==='CTL_JOB_FAILED');
 assert.deepEqual(failed.calls.map(c=>c.method),['binaries.update','binaries.status']);
});
