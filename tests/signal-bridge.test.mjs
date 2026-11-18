/* global setTimeout, Response, Request, ReadableStream, TextDecoder */
import {test} from 'node:test';
import assert from 'node:assert/strict';
import bridge, {frame} from '../backend/internal/signalbridge/bridge.js';

// The NetBird Signal bridge Worker against a fake of the server's WebSocket
// proxy, which speaks HTTP/2 frames.
const PATH='/signalexchange.SignalExchange/ConnectStream';
const DATA=0,HEADERS=1,SETTINGS=4,PING=6,WINDOW=8;
const tick=()=>new Promise((r)=>setTimeout(r,5));

class FakeSocket {
 sent=[];closed=false;listeners={};
 accept(){}
 send(b){if(this.closed)throw new Error('closed');this.sent.push(new Uint8Array(b));}
 close(){this.closed=true;}
 addEventListener(t,f){(this.listeners[t]??=[]).push(f);}
 emit(t,ev={}){for(const f of this.listeners[t]??[])f(ev);}
 server(...frames){const n=frames.reduce((a,f)=>a+f.length,0),b=new Uint8Array(n);let o=0;for(const f of frames){b.set(f,o);o+=f.length;}this.emit('message',{data:b.buffer});}
 // Every frame the bridge sent after the connection preface.
 frames(){
  let b=new Uint8Array(this.sent.reduce((a,s)=>a+s.length,0)),o=0;
  for(const s of this.sent){b.set(s,o);o+=s.length;}
  b=b.subarray(24);
  const out=[];
  while(b.length>=9){const len=b[0]<<16|b[1]<<8|b[2];out.push({type:b[3],flags:b[4],id:new DataView(b.buffer,b.byteOffset+5,4).getUint32(0),payload:b.slice(9,9+len)});b=b.subarray(9+len);}
  return out;
 }
}

function harness(){
 const ws=new FakeSocket(),opened=[],passed=[];
 globalThis.fetch=async(input,init)=>{
  if(init?.headers?.Upgrade==='websocket'){opened.push(String(input));return {webSocket:ws};}
  passed.push(input);return new Response('origin');
 };
 let push;
 const body=new ReadableStream({start(c){push=c;}});
 const request=new Request(`https://netbird.example.com${PATH}`,{method:'POST',body,duplex:'half',headers:{'content-type':'application/grpc','x-wiretrustee-peer-id':'peer','authorization':'secret'}});
 return {ws,opened,passed,request,push};
}
const u32=(n)=>{const b=new Uint8Array(4);new DataView(b.buffer).setUint32(0,n);return b;};
const answer=()=>frame(HEADERS,4,1,new Uint8Array([0x88]));
const sentData=(ws)=>ws.frames().filter((f)=>f.type===DATA&&f.payload.length).reduce((n,f)=>n+f.payload.length,0);

test('other paths go to the tunnel unchanged',async()=>{
 const h=harness();
 const res=await bridge.fetch(new Request('https://netbird.example.com/management.ManagementService/Login',{method:'POST'}));
 assert.equal(await res.text(),'origin');
 assert.equal(h.passed.length,1);
 assert.equal(h.opened.length,0);
});

test('the stream opens on the same name and answers once the server registers the peer',async()=>{
 const h=harness();
 const pending=bridge.fetch(h.request);
 await tick();
 assert.deepEqual(h.opened,['https://netbird.example.com/ws-proxy/signal']);
 const fr=h.ws.frames();
 assert.equal(fr[0].type,SETTINGS);
 const headers=fr.find((f)=>f.type===HEADERS);
 const text=new TextDecoder().decode(headers.payload);
 assert.match(text,/x-wiretrustee-peer-id/);
 assert.doesNotMatch(text,/authorization|secret/);
 h.ws.server(answer(),frame(PING,0,0,new Uint8Array(8)));
 const res=await pending;
 assert.equal(res.headers.get('x-wiretrustee-peer-registered'),'1');
 assert.ok(h.ws.frames().some((f)=>f.type===PING&&f.flags===1));
 // Server messages reach the client, and their window is given back.
 h.ws.server(frame(DATA,0,1,new Uint8Array([0,0,0,0,1,7])));
 const reader=res.body.getReader();
 assert.deepEqual([...(await reader.read()).value],[0,0,0,0,1,7]);
 assert.ok(h.ws.frames().some((f)=>f.type===WINDOW&&f.id===1));
 reader.cancel();
});

test('client messages wait for the server window',async()=>{
 const h=harness();
 const pending=bridge.fetch(h.request);
 await tick();
 // The server allows 10 bytes per stream.
 h.ws.server(frame(SETTINGS,0,0,new Uint8Array([0,4,...u32(10)])),answer());
 await pending;
 h.push.enqueue(new Uint8Array(25).fill(1));
 await tick();
 assert.equal(sentData(h.ws),10);
 h.ws.server(frame(WINDOW,0,1,u32(10)));
 assert.equal(sentData(h.ws),20);
 h.ws.server(frame(WINDOW,0,1,u32(100)));
 assert.equal(sentData(h.ws),25);
 // The connection window binds too.
 h.ws.server(frame(WINDOW,0,1,u32(200000)));
 h.push.enqueue(new Uint8Array(70000));
 await tick();
 assert.equal(sentData(h.ws),65535);
 h.ws.server(frame(WINDOW,0,0,u32(10000)));
 assert.equal(sentData(h.ws),70025);
 assert.ok(h.ws.frames().every((f)=>f.payload.length<=16384));
 h.push.close();
 await tick();
 const last=h.ws.frames().at(-1);
 assert.equal(last.type,DATA);
 assert.equal(last.flags&1,1);
});

test('a client that goes away closes the server stream',async()=>{
 const h=harness();
 const pending=bridge.fetch(h.request);
 await tick();
 h.ws.server(answer());
 const res=await pending;
 await res.body.cancel();
 await tick();
 assert.ok(h.ws.closed);
});

test('a server that refuses the stream ends the call as unavailable',async()=>{
 const h=harness();
 const pending=bridge.fetch(h.request);
 await tick();
 h.ws.emit('close');
 const res=await pending;
 assert.equal(res.headers.get('grpc-status'),'14');
});

test('no WebSocket answer is unavailable',async()=>{
 harness();
 globalThis.fetch=async()=>new Response('no',{status:502});
 const res=await bridge.fetch(new Request(`https://netbird.example.com${PATH}`,{method:'POST',body:'x'}));
 assert.equal(res.headers.get('grpc-status'),'14');
});

test('the management root explains device sign-in without touching API or OIDC routes',async()=>{
 const h=harness();
 for(const method of ['GET','HEAD']){
  const res=await bridge.fetch(new Request('https://netbird.example.com/',{method}));
  assert.equal(res.status,200);
  assert.match(res.headers.get('content-type'),/text\/html/);
  assert.match(res.headers.get('content-security-policy'),/default-src 'none'/);
  const text=await res.text();
  if(method==='HEAD')assert.equal(text,'');
  else{assert.match(text,/NetBird management server/);assert.match(text,/https:\/\/netbird.example.com/);assert.match(text,/Install NetBird/);assert.match(text,/administrator/);assert.doesNotMatch(text,/<script|404 page not found/);}
 }
 for(const path of ['/api/users','/oauth2/.well-known/openid-configuration','/ws-proxy/signal','/management.ManagementService/GetServerKey']){
  assert.equal(await (await bridge.fetch(new Request('https://netbird.example.com'+path))).text(),'origin');
 }
 assert.equal(h.passed.length,4);
 assert.equal(h.opened.length,0);
});
