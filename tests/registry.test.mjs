import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {execFileSync} from 'node:child_process';
import {ajv} from '../node_modules/@synopackageland/cli/dist/ctl/descriptor.js';
import {readRegistry, registryTools} from '../node_modules/@synopackageland/cli/dist/ctl/registry.js';

const registry = readRegistry('operations.json');
const ops = registry.operations;

test('every mutation is a draft with an input-compatible non-mutating preview', () => {
 const exposed = Object.entries(ops).filter(([,op]) => !['hidden','preview'].includes(op.ai));
 assert.equal(exposed.length,24);
 for (const [name,op] of exposed) {
  if(op.effect === 'read') { assert.equal(op.ai,'read',name); continue; }
  assert.equal(op.ai,'draft',name);
  const preview = ops[op.preview];
  assert.equal(preview.ai,'preview');
  assert.equal(preview.effect,'read');
  assert.deepEqual(preview.paramsSchema,op.paramsSchema);
 }
 const tools = registryTools(registry);
 for (const tool of tools.filter(t => t.ai === 'draft')) assert.equal(tool.execution,'preview-and-human-confirmation');
 for (const tool of tools) assert.notEqual(tool.ai,'hidden');
});

test('real handler results match the registry, including pending, browser login and connector-required states', t => {
 const dir = mkdtempSync(join(tmpdir(),'connect-registry-'));
 t.after(() => rmSync(dir,{recursive:true,force:true}));
 const file = join(dir,'results.json');
 execFileSync('go',['test','./internal/app','-run','^TestRegistryHandlerResults$','-count=1'],{cwd:'backend',env:{...process.env,CONNECT_REGISTRY_RESULTS:file},stdio:'pipe'});
 const results = JSON.parse(readFileSync(file,'utf8'));
 for(const [name,values] of Object.entries(results)) {
  const validate = ajv.compile(ops[name].resultSchema);
  for(const value of values) assert.equal(validate(value),true,`${name}: ${JSON.stringify(validate.errors)}`);
 }
 for(const [name,op] of Object.entries(ops)) if(op.ai !== 'hidden') assert.ok(results[name]?.length,name);
 const pending = results['ctl.connection.connect'];
 assert.ok(pending.some(v => v.state === 'connecting' && v.networkReady === false));
 assert.ok(pending.some(v => v.state === 'needs-login' && v.loginUrl && v.userCode));
 assert.ok(pending.some(v => v.state === 'connector-required' && v.code === 'binary_required'));
 assert.ok(results['binaries.update'].some(v => v.busy && ['queued','checking'].includes(v.phase)));
});

test('connect schema follows current provider handlers and omits retired exposure APIs',()=>{
 const validate=ajv.compile(ops['ctl.connection.connect'].paramsSchema);
 for(const params of [
  {provider:'tailscale',funnel:true},
  {provider:'tailscale',mappings:[{localPort:7443,protocol:'https',publicPort:443}]},
  {provider:'netbird',mappings:[]},
  {provider:'cloudflare',mode:'token',confirmExposure:true},
  {provider:'cloudflare',mode:'quick',token:'retired-token',confirmExposure:true},
  {provider:'tailscale',hostname:'retired-host'},
  {provider:'tailscale',confirmExposure:true},
  {provider:'netbird',loginServer:'https://login.example.com'},
 ]) assert.equal(validate(params),false,JSON.stringify(params));
 for(const params of [{provider:'tailscale'},{provider:'netbird'},{provider:'cloudflare',mode:'quick',confirmExposure:true},{provider:'ipv6',serviceIds:['dsm-https'],confirmExposure:true}])assert.equal(validate(params),true);
});
