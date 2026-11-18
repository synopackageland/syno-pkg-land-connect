// Connect adaptation of hostc (Apache-2.0): one permanent tunnel per Worker.
import { PROTOCOL_HEADER, PROTOCOL_VERSION } from './protocol/index.ts';
import { verifyConnectToken } from './token.ts';
import { CONNECT_URL } from './tunnel.ts';
export { Tunnel } from './tunnel.ts';

export const TUNNEL_ID = 'default';
export const CONNECT_PATH = '/_spl/relay/connect';

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname === CONNECT_PATH) {
      if (request.headers.get(PROTOCOL_HEADER) !== String(PROTOCOL_VERSION)) {
        return Response.json({ error: 'Unsupported relay protocol' }, { status: 426 });
      }
      if (request.method !== 'GET' || request.headers.get('upgrade')?.toLowerCase() !== 'websocket') {
        return Response.json({ error: 'Expected a WebSocket upgrade' }, { status: 426 });
      }
      // Never pass a public caller's authorization or arbitrary tunnel id to the DO.
      const token = request.headers.get('authorization')?.match(/^Bearer (\S+)$/)?.[1];
      if (!env.RELAY_SECRET || env.RELAY_SECRET.length < 32 || !token || !(await verifyConnectToken(env.RELAY_SECRET, TUNNEL_ID, token))) {
        return Response.json({ error: 'Invalid relay credentials' }, { status: 401 });
      }
      return env.TUNNEL.getByName(TUNNEL_ID).fetch(new Request(CONNECT_URL, request));
    }
    return env.TUNNEL.getByName(TUNNEL_ID).fetch(request);
  },
} satisfies ExportedHandler<Env>;
