import { beforeEach, describe, expect, it } from 'vitest';
import { env, reset, SELF } from 'cloudflare:test';
import { connect, createTunnel, publicUrl, readAll } from './helpers.ts';
import { FrameType, encodeText, decodeWindow, MAX_CHUNK_BYTES, STREAM_WINDOW_BYTES } from '../src/protocol/index.ts';
import worker from '../src/index.ts';
import { signConnectToken, verifyConnectToken } from '../src/token.ts';

beforeEach(async () => { await reset(); });
describe('Connect response contract', () => {
  it('streams a Range response beyond the initial window with exact length and bytes', async () => {
    const tunnel = await createTunnel(); const client = await connect(tunnel);
    const pending = SELF.fetch(publicUrl(tunnel, '/media'), {headers: {range:'bytes=100-400099'}});
    const open = await client.next(f => f.type === FrameType.Open);
    client.head(open.stream, {status:206, headers:[['content-length','400000'],['content-range','bytes 100-400099/8000000'],['accept-ranges','bytes']]});
    const response = await pending;
    expect(response.status).toBe(206);
    expect(response.headers.get('content-length')).toBe('400000');
    expect(response.headers.get('content-range')).toBe('bytes 100-400099/8000000');
    const body = readAll(response); let credit=STREAM_WINDOW_BYTES;
    const expected=new Uint8Array(400000).map((_,i)=>i%251);
    for(let offset=0;offset<expected.length;){
      const chunk=expected.subarray(offset,offset+MAX_CHUNK_BYTES);
      while(credit<chunk.length)credit+=decodeWindow((await client.next(f=>f.type===FrameType.Window)).payload);
      client.send(FrameType.Data,open.stream,chunk);credit-=chunk.length;offset+=chunk.length;
    }
    client.send(FrameType.End,open.stream);
    expect(await body).toEqual(expected);
  });
  it('preserves HEAD representation length without sending a body', async () => {
    const client=await connect(await createTunnel());const pending=SELF.fetch('https://api.test/media',{method:'HEAD'});
    const open=await client.next(f=>f.type===FrameType.Open);
    client.head(open.stream,{body:false,headers:[['content-length','8000000']]});
    const response=await pending;
    expect(response.headers.get('content-length')).toBe('8000000');
    expect(await response.text()).toBe('');
  });
  it('rejects malformed or duplicate lengths without disconnecting the connector', async () => {
    const client=await connect(await createTunnel());
    for(const headers of [[['content-length','-1']], [['content-length','1'],['content-length','2']]]){
      const pending=SELF.fetch('https://api.test/bad');const open=await client.next(f=>f.type===FrameType.Open);
      client.head(open.stream,{headers: headers as [string,string][]});
      expect((await pending).status).toBe(502);
      expect((await client.next(f=>f.type===FrameType.Reset)).stream).toBe(open.stream);
    }
    expect(client.closed).toBeNull();
  });
  it('fails a truncated declared-length stream', async () => {
    const client=await connect(await createTunnel());const pending=SELF.fetch('https://api.test/short');
    const open=await client.next(f=>f.type===FrameType.Open);
    client.head(open.stream,{headers:[['content-length','5']]});
    const response=await pending;const failure=expect(response.arrayBuffer()).rejects.toThrow();
    client.send(FrameType.Data,open.stream,encodeText('abc'));client.send(FrameType.End,open.stream);
    await failure;
  });
});
describe('per-Worker HMAC', () => {
  it('fails closed when the Worker secret is absent or too short', async () => {
    for (const secret of ['', 'short']) {
      const request=new Request('https://relay.example.com/_spl/relay/connect', {
        headers:{upgrade:'websocket','hostc-protocol':'5',authorization:'Bearer test'},
      });
      expect((await worker.fetch(request,{...env, RELAY_SECRET:secret})).status).toBe(401);
    }
  });
  it('uses the documented UTF-8 secret and rejects cross-Worker replay', async () => {
    const secret='0123456789abcdef'.repeat(4);
    const token=await signConnectToken(secret,'default');
    expect(token).toBe('8rxmXIdh67AQj7j82TKSd3EORdz5gHHJUIM8dnI47eM');
    expect(await verifyConnectToken(secret,'default',token)).toBe(true);
    expect(await verifyConnectToken('f'.repeat(64),'default',token)).toBe(false);
    expect(await verifyConnectToken(secret,'other',token)).toBe(false);
    expect(await verifyConnectToken(secret,'default','invalid token')).toBe(false);
  });
});
