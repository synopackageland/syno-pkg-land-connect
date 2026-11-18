// Minimal test connector, not the production NAS client. Implements v5 over a Unix socket.
import http from 'node:http';
import { createHmac } from 'node:crypto';
import { once } from 'node:events';
import WebSocket from 'ws';
import {
  decodeFrame, decodeOpen, decodeClose, decodeWindow, encodeFrame, encodeHead,
  encodeWindow, encodeClose, encodeText, FrameType as F, STREAM_WINDOW_BYTES,
  MAX_CHUNK_BYTES, sendableCloseCode, sendableCloseReason, stripHopByHop, stripWebSocketHandshake, responseHasBody,
} from '../src/protocol/index.ts';

export async function connect(origin, secret, socketPath) {
  const url = new URL('/_spl/relay/connect', origin);
  url.protocol = 'wss:';
  const token = createHmac('sha256', secret).update('connect:default').digest('base64url');
  const ws = new WebSocket(url, {headers: {'hostc-protocol': '5', authorization: `Bearer ${token}`}, perMessageDeflate: false});
  const streams = new Map();
  let pong = false;
  const send = (type, id, payload) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(encodeFrame(type, id, payload));
  };
  ws.on('message', (data, binary) => {
    if (!binary) { if (data.toString() === 'hostc:pong') pong = true; return; }
    try {
    const frame = decodeFrame(data);
    const {stream: id, payload, type} = frame;
    if (type === F.Open) {
      const open = decodeOpen(payload);
      const headers = Object.fromEntries(stripWebSocketHandshake(open.headers));
      headers.host = new URL(origin).host;
      if (open.websocket) {
        const local = new WebSocket(`ws+unix://${socketPath}:${open.path}`, open.websocket, {headers, perMessageDeflate: false});
        streams.set(id, {local});
        local.on('upgrade', response => {
          const headers = rawHeaders(response.rawHeaders);
          send(F.Head, id, encodeHead({status: 101, headers, body: false, ...(response.headers['sec-websocket-protocol'] ? {protocol:response.headers['sec-websocket-protocol']} : {})}));
        });
        local.on('message', (chunk, binary) => send(binary ? F.Data : F.Text, id, chunk));
        local.on('close', (code, reason) => {send(F.End, id, encodeClose({code, reason:reason.toString()}));streams.delete(id);});
        local.on('error', () => send(F.Reset, id, encodeText('local WebSocket failed')));
        return;
      }
      const state = {credit: STREAM_WINDOW_BYTES, wake: null, local: null};
      streams.set(id, state);
      const req = http.request({socketPath, path:open.path, method:open.method, headers}, res => {
        const body = responseHasBody(open.method, res.statusCode);
        send(F.Head, id, encodeHead({status:res.statusCode, headers:stripHopByHop(rawHeaders(res.rawHeaders)), body}));
        void (async () => {
          try {
            if (body) for await (const value of res) {
              for (let offset=0; offset<value.length;) {
                while (state.credit === 0) await new Promise(resolve => state.wake=resolve);
                const chunk = value.subarray(offset, offset+Math.min(MAX_CHUNK_BYTES,state.credit));
                state.credit -= chunk.length;
                send(F.Data,id,chunk); offset+=chunk.length;
              }
            }
            send(F.End,id); streams.delete(id);
          } catch { send(F.Reset,id,encodeText('local response failed')); }
        })();
      });
      state.local=req;
      req.on('error', () => {send(F.Reset,id,encodeText('local request failed'));streams.delete(id);});
      if (!open.body) req.end();
    } else {
      const state=streams.get(id); if (!state) return;
      if (type === F.Window) {state.credit+=decodeWindow(payload);state.wake?.();state.wake=null;}
      else if (type === F.Reset) {state.local.destroy?.();state.local.terminate?.();streams.delete(id);state.credit=1;state.wake?.();}
      else if (state.local instanceof WebSocket) {
        if(type===F.Data||type===F.Text)state.local.send(payload,{binary:type===F.Data});
        if(type===F.End){const c=decodeClose(payload);state.local.close(sendableCloseCode(c.code),sendableCloseReason(c.reason));}
      } else if (type === F.Data) state.local.write(payload, () => send(F.Window,id,encodeWindow(payload.length)));
      else if (type === F.End) state.local.end();
    }
    } catch { ws.terminate(); }
  });
  ws.on('error', () => {});
  ws.on('close', () => {clearInterval(timer);for(const s of streams.values()){s.local.destroy?.();s.local.terminate?.();}streams.clear();});
  let timer;
  await once(ws,'open',{signal:AbortSignal.timeout(15_000)});
  ws.send('hostc:ping');
  timer=setInterval(()=>ws.send('hostc:ping'),15_000);
  return {ws, pong:()=>pong, close:()=>{clearInterval(timer);ws.close(4000,'test complete');setTimeout(()=>ws.terminate(),1000).unref();}};
}
function rawHeaders(raw) {const out=[];for(let i=0;i<raw.length;i+=2)out.push([raw[i],raw[i+1]]);return out;}
