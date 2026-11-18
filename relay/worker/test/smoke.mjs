// Explicit opt-in live smoke. Credentials are read from a file, never argv values/logs.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { randomBytes, createHash } from 'node:crypto';
import WebSocket, { WebSocketServer } from 'ws';
import { connect } from './connector.mjs';
const [envFile, prefix]=process.argv.slice(2);
if (!envFile || !/^t[0-9]+-[a-z0-9-]+$/.test(prefix??'')) throw new Error('Usage: node test/smoke.mjs <env-file> <thread-prefixed-worker-name>');
const values=Object.fromEntries(fs.readFileSync(envFile,'utf8').split('\n').filter(l=>/^(CLOUDFLARE_API_TOKEN|CLOUDFLARE_ACCOUNT_ID)=/.test(l)).map(l=>{const i=l.indexOf('=');return [l.slice(0,i),l.slice(i+1).trim().replace(/^['"]|['"]$/g,'')];}));
assert.ok(values.CLOUDFLARE_API_TOKEN && values.CLOUDFLARE_ACCOUNT_ID,'Missing test credentials');
const base=`https://api.cloudflare.com/client/v4/accounts/${values.CLOUDFLARE_ACCOUNT_ID}`;
const script=`/workers/scripts/${prefix}`;
const secret=randomBytes(32).toString('hex');
const checks=[];
const publicFetch=(url,options={})=>fetch(url,{...options,signal:AbortSignal.timeout(30_000)});
async function api(method,route,body,expected=200){
  const response=await fetch(base+route,{method,headers:{Authorization:`Bearer ${values.CLOUDFLARE_API_TOKEN}`,...(body instanceof FormData?{}:{'Content-Type':'application/json'})},body:body instanceof FormData?body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(30_000)});
  const json=await response.json();
  assert.equal(response.status,expected,`Cloudflare ${method} failed (${(json.errors??[]).map(e=>e.code).join(',')})`);
  return json;
}
const temp=fs.mkdtempSync(path.join(os.tmpdir(),prefix));
const socketPath=path.join(temp,'echo.sock');
const bytes=Buffer.alloc(8*1024*1024);for(let i=0;i<bytes.length;i++)bytes[i]=i%251;
const server=http.createServer(async(req,res)=>{
  res.setHeader('cache-control','no-store, no-transform');
  if(req.url==='/sse'){
    res.writeHead(200,{'content-type':'text/event-stream'});
    res.write('data: first\n\n');setTimeout(()=>res.end('data: last\n\n'),500);return;
  }
  if(req.url==='/large'){
    const match=/^bytes=(\d+)-(\d+)$/.exec(req.headers.range??'');
    const start=match?Number(match[1]):0,end=match?Number(match[2]):bytes.length-1;
    res.writeHead(match?206:200,{'content-type':'application/octet-stream','content-length':end-start+1,'accept-ranges':'bytes',...(match?{'content-range':`bytes ${start}-${end}/${bytes.length}`}:{})});
    res.end(bytes.subarray(start,end+1));return;
  }
  const parts=[];for await(const b of req)parts.push(b);
  const value=JSON.stringify({method:req.method,path:req.url,host:req.headers.host,origin:req.headers.origin,body:Buffer.concat(parts).toString()});
  res.writeHead(200,{'content-type':'application/json','content-length':Buffer.byteLength(value),'set-cookie':['a=1; Domain=example.com; Path=/','b=2; HttpOnly']});res.end(value);
});
const wss=new WebSocketServer({server});wss.on('connection',ws=>ws.on('message',(data,binary)=>ws.send(data,{binary})));
server.listen(socketPath);await once(server,'listening');
let connector,owned=false;
try {
  // Refuse to overwrite an existing Worker, even if its name has the right prefix.
  const existing=await fetch(base+script,{headers:{Authorization:`Bearer ${values.CLOUDFLARE_API_TOKEN}`}});
  assert.equal(existing.status,404,'Test Worker already exists; choose a fresh test name');
  const sub=await api('GET','/workers/subdomain');assert.ok(sub.result.subdomain,'Account has no workers.dev subdomain');
  const origin=`https://${prefix}.${sub.result.subdomain}.workers.dev`;
  const metadata={main_module:'worker.js',compatibility_date:'2026-10-24',compatibility_flags:['enable_request_signal'],bindings:[{type:'durable_object_namespace',name:'TUNNEL',class_name:'Tunnel'}],migrations:{new_tag:'v1',new_sqlite_classes:['Tunnel']},observability:{enabled:false}};
  const form=new FormData();form.append('metadata',JSON.stringify(metadata));form.append('worker.js',new Blob([fs.readFileSync(new URL('../../../backend/internal/relayworker/worker.js',import.meta.url))],{type:'application/javascript+module'}),'worker.js');
  // Mark cleanup intent before upload because a lost upload reply can still create the Worker.
  owned=true;await api('PUT',script,form);checks.push('SQLite DO upload');
  await api('PUT',script+'/secrets',{name:'RELAY_SECRET',text:secret,type:'secret_text'},201);
  await api('POST',script+'/subdomain',{enabled:true,previews_enabled:false});checks.push('Secret and workers.dev route');
  const route=await api('GET',script+'/subdomain');assert.equal(route.result.enabled,true);assert.equal(route.result.previews_enabled,false);
  // New hostnames need edge/DNS propagation; retry only GET, never the upload.
  let ready=false;for(let n=0;n<20;n++){try{const r=await publicFetch(origin,{signal:AbortSignal.timeout(5000)});if(r.status===502){ready=true;break;}}catch{}await new Promise(r=>setTimeout(r,1000));}
  assert.ok(ready,'workers.dev did not become ready');checks.push('Offline returns 502');
  assert.equal((await publicFetch(origin+'/_spl/relay/connect',{headers:{'hostc-protocol':'4'}})).status,426);
  const bad=new WebSocket(origin.replace('https:','wss:')+'/_spl/relay/connect',{headers:{'hostc-protocol':'5',authorization:'Bearer invalid'}});
  bad.on('error',()=>{});const rejected=await once(bad,'unexpected-response',{signal:AbortSignal.timeout(15_000)});assert.equal(rejected[1].statusCode,401);rejected[1].resume();bad.terminate();checks.push('Protocol and HMAC refusals');
  connector=await connect(origin,secret,socketPath);
  const json=await (await publicFetch(origin+'/echo?x=1',{method:'POST',headers:{origin},body:'echo payload'})).json();
  assert.deepEqual(json,{method:'POST',path:'/echo?x=1',host:new URL(origin).host,origin,body:'echo payload'});checks.push('HTTP POST / query / Host / Origin');
  const cookie=await publicFetch(origin+'/cookies');assert.equal(cookie.headers.getSetCookie().length,2);assert.ok(cookie.headers.getSetCookie().every(c=>!c.toLowerCase().includes('domain=')));checks.push('Host-only repeated cookies');
  const sse=await publicFetch(origin+'/sse');const reader=sse.body.getReader();const first=await reader.read();assert.ok(new TextDecoder().decode(first.value).includes('first'));let rest='';for(;;){const r=await reader.read();if(r.done)break;rest+=new TextDecoder().decode(r.value);}assert.ok(rest.includes('last'));checks.push('Streaming SSE separate chunks');
  const publicWs=new WebSocket(origin.replace('https:','wss:')+'/ws',['chat']);await once(publicWs,'open',{signal:AbortSignal.timeout(15_000)});
  publicWs.send('ping');assert.equal((await once(publicWs,'message',{signal:AbortSignal.timeout(15_000)}))[0].toString(),'ping');
  const payload=Buffer.from([0,255,17]);publicWs.send(payload);assert.deepEqual((await once(publicWs,'message',{signal:AbortSignal.timeout(15_000)}))[0],payload);publicWs.close();checks.push('WebSocket text / binary / protocol');
  const large=await publicFetch(origin+'/large',{headers:{'accept-encoding':'identity'}});assert.equal(large.headers.get('content-length'),String(bytes.length));assert.equal(createHash('sha256').update(Buffer.from(await large.arrayBuffer())).digest('hex'),createHash('sha256').update(bytes).digest('hex'));checks.push('8 MiB download / content-length / SHA256');
  const range=await publicFetch(origin+'/large',{headers:{range:'bytes=100000-999999','accept-encoding':'identity'}});assert.equal(range.status,206);assert.equal(range.headers.get('content-range'),`bytes 100000-999999/${bytes.length}`);assert.equal(range.headers.get('content-length'),'900000');assert.deepEqual(Buffer.from(await range.arrayBuffer()),bytes.subarray(100000,1000000));checks.push('Range 206 / content-range / length / bytes');
  assert.ok(connector.pong());checks.push('Hibernation heartbeat auto-response');
} finally {
  connector?.close();for(const ws of wss.clients)ws.terminate();wss.close();server.closeAllConnections();server.close();
  fs.rmSync(temp,{recursive:true,force:true});
  if(owned){await api('DELETE',script+'?force=true');const gone=await fetch(base+script,{headers:{Authorization:`Bearer ${values.CLOUDFLARE_API_TOKEN}`}});assert.equal(gone.status,404);checks.push('Delete / subsequent GET 404');}
  console.log(JSON.stringify({checks},null,2));
}
