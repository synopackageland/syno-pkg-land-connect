// NetBird Signal bridge for Connect's self-hosted NetBird server.
//
// Cloudflare holds a gRPC stream's response headers until the request ends,
// so Signal's two-way stream never starts through the tunnel. Only that
// stream and the exact root page are routed here. The Worker answers it at once and carries it to the
// server's own WebSocket proxy (/ws-proxy/signal) on the same name, which the
// tunnel serves, speaking the small part of HTTP/2 that one stream needs.
// Single calls need trailers, which a Worker cannot send, so they never come
// here.
const enc = new TextEncoder()
const PREFACE = enc.encode('PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n')
const DATA = 0, HEADERS = 1, RST = 3, SETTINGS = 4, PING = 6, GOAWAY = 7, WINDOW = 8
const END_STREAM = 1, ACK = 1, END_HEADERS = 4, PADDED = 8
const INITIAL_WINDOW_SETTING = 4
const MAX_FRAME = 16384, DEFAULT_WINDOW = 65535, RECEIVE_WINDOW = 1 << 20
// The client's messages are held here while the server's window is closed;
// past this the client is not read further.
const QUEUE_LIMIT = 1 << 20
const HEADERS_WAIT = 15000
const PATH = '/signalexchange.SignalExchange/ConnectStream'
const FORWARD = /^(x-wiretrustee-|x-netbird-|grpc-(timeout|encoding|accept-encoding)$|user-agent$)/

export function frame(type, flags, stream, payload = new Uint8Array(0)) {
  const b = new Uint8Array(9 + payload.length)
  b[0] = payload.length >> 16; b[1] = payload.length >> 8 & 255; b[2] = payload.length & 255
  b[3] = type; b[4] = flags
  new DataView(b.buffer).setUint32(5, stream)
  b.set(payload, 9)
  return b
}
const u32 = (n) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n); return b }
// HPACK integer with a 7-bit prefix, for string lengths without Huffman.
function int7(n) { if (n < 127) return [n]; const out = [127]; n -= 127; while (n >= 128) { out.push(n % 128 + 128); n = Math.floor(n / 128) } out.push(n); return out }
// Every field as "literal without indexing, new name": no table to keep.
function hpack(fields) { const out = []; for (const [k, v] of fields) { const kb = enc.encode(k), vb = enc.encode(v); out.push(0, ...int7(kb.length), ...kb, ...int7(vb.length), ...vb) } return new Uint8Array(out) }
function concat(...parts) { const b = new Uint8Array(parts.reduce((n, p) => n + p.length, 0)); let o = 0; for (const p of parts) { b.set(p, o); o += p.length } return b }
const grpcError = (code, message) => new Response(null, { headers: { 'content-type': 'application/grpc', 'grpc-status': String(code), 'grpc-message': message } })

// This is an app endpoint, not a dashboard. Reuse the existing Worker:
// no extra process or container, and its routes are removed with the server.
function landingPage(url, head) {
  const origin = url.origin.replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))
  const html = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>NetBird management server</title>
<style>body{font:17px/1.6 system-ui,sans-serif;margin:3rem auto;padding:0 1.25rem;max-width:42rem;color:#202b3c;background:#fafbfc}h1{line-height:1.25}code{overflow-wrap:anywhere}li{margin:.75rem 0}a{color:#145ac2}</style>
<main><h1>NetBird management server</h1><p>This address is for NetBird apps. It does not host a web dashboard</p>
<h2>Connect a device</h2><ol><li><a href="https://netbird.io/download" rel="noreferrer">Install NetBird</a> on your device</li><li>In NetBird, set the management server URL to <code>${origin}</code></li><li>Sign in with the administrator account and password shown in Syno Connect Center &gt; NetBird &gt; Self-host server</li></ol>
<p>Want a web dashboard? You can run the official <code>netbirdio/dashboard</code> container yourself. Syno Connect Center does not install or manage it</p>
<p><a href="https://github.com/synopackageland/syno-pkg-land-connect/blob/main/docs/netbird-server.md#optional-web-dashboard" rel="noreferrer">Dashboard setup guide</a></p></main></html>`
  return new Response(head ? null : html, {headers:{'content-type':'text/html; charset=utf-8','cache-control':'no-store','content-security-policy':"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",'x-content-type-options':'nosniff','referrer-policy':'no-referrer'}})
}

export default {
  async fetch(request) {
    const url = new URL(request.url)
    if (url.pathname === '/' && ['GET', 'HEAD'].includes(request.method)) return landingPage(url, request.method === 'HEAD')
    if (request.method !== 'POST' || url.pathname !== PATH) return fetch(request)
    let ws
    try {
      ws = (await fetch(`https://${url.host}/ws-proxy/signal`, { headers: { Upgrade: 'websocket' } })).webSocket
    } catch {}
    if (!ws) return grpcError(14, 'signal unavailable')
    ws.accept()
    ws.binaryType = 'arraybuffer'
    const { readable, writable } = new TransformStream()
    const out = writable.getWriter()
    let buf = new Uint8Array(0), head = false, closed = false, answer
    const headed = new Promise((resolve) => { answer = resolve })
    const finish = (err) => {
      if (closed) return
      closed = true
      ;(err ? out.abort(err) : out.close()).catch(() => {})
      try { ws.close(1000) } catch {}
      if (!head) { head = true; answer(false) }
      wake()
    }
    // The client went away: the response can no longer be written.
    out.closed.catch(() => finish())
    request.signal?.addEventListener('abort', () => finish())
    const send = (b) => { try { ws.send(b) } catch (e) { finish(e) } }

    // Outbound flow control: the server's windows for the connection and
    // the stream, and the client's bytes waiting for them.
    let connWindow = DEFAULT_WINDOW, streamWindow = DEFAULT_WINDOW, peerInitial = DEFAULT_WINDOW
    let queue = [], queued = 0, ending = false, ended = false, drained
    const wake = () => { if (drained) { drained(); drained = undefined } }
    const flush = () => {
      while (!closed && queue.length) {
        const room = Math.min(connWindow, streamWindow, MAX_FRAME)
        if (room <= 0) return
        const chunk = queue[0]
        const n = Math.min(room, chunk.length)
        send(frame(DATA, 0, 1, chunk.subarray(0, n)))
        connWindow -= n; streamWindow -= n; queued -= n
        if (n === chunk.length) queue.shift()
        else queue[0] = chunk.subarray(n)
      }
      if (!closed && ending && !ended && !queue.length) { ended = true; send(frame(DATA, END_STREAM, 1)) }
      if (queued < QUEUE_LIMIT) wake()
    }

    const handle = (type, flags, id, p, len) => {
      if (type === SETTINGS && !(flags & ACK)) {
        for (let i = 0; i + 6 <= p.length; i += 6) {
          if ((p[i] << 8 | p[i + 1]) !== INITIAL_WINDOW_SETTING) continue
          const size = new DataView(p.buffer, p.byteOffset + i + 2, 4).getUint32(0)
          streamWindow += size - peerInitial
          peerInitial = size
        }
        send(frame(SETTINGS, ACK, 0))
        flush()
      } else if (type === PING && !(flags & ACK)) send(frame(PING, ACK, 0, p))
      else if (type === WINDOW && p.length === 4) {
        const n = new DataView(p.buffer, p.byteOffset, 4).getUint32(0) & 0x7fffffff
        if (id === 0) connWindow += n
        else if (id === 1) streamWindow += n
        flush()
      } else if (type === GOAWAY || type === RST && id === 1) finish(new Error('signal reset'))
      else if (type === HEADERS && id === 1) {
        // The first block is the answer (Signal sends its registration
        // header at once); a later block is the trailers that end the call.
        if (head) return finish()
        head = true
        answer(!(flags & END_STREAM))
        if (flags & END_STREAM) finish()
      } else if (type === DATA && id === 1) {
        if (flags & PADDED) p = p.subarray(1, p.length - p[0])
        if (len > 0) send(concat(frame(WINDOW, 0, 0, u32(len)), frame(WINDOW, 0, 1, u32(len))))
        if (p.length) out.write(p.slice()).catch(() => finish())
        if (flags & END_STREAM) finish()
      }
    }
    ws.addEventListener('message', (ev) => {
      buf = concat(buf, typeof ev.data === 'string' ? enc.encode(ev.data) : new Uint8Array(ev.data))
      while (buf.length >= 9 && !closed) {
        const len = buf[0] << 16 | buf[1] << 8 | buf[2]
        if (buf.length < 9 + len) break
        const id = new DataView(buf.buffer, buf.byteOffset + 5, 4).getUint32(0) & 0x7fffffff
        handle(buf[3], buf[4], id, buf.subarray(9, 9 + len), len)
        buf = buf.slice(9 + len)
      }
    })
    ws.addEventListener('close', () => finish(new Error('signal closed')))
    ws.addEventListener('error', () => finish(new Error('signal failed')))

    const fields = [[':method', 'POST'], [':scheme', 'http'], [':path', PATH], [':authority', url.host], ['content-type', 'application/grpc'], ['te', 'trailers']]
    for (const [k, v] of request.headers) if (FORWARD.test(k)) fields.push([k, v])
    send(concat(PREFACE, frame(SETTINGS, 0, 0, concat(new Uint8Array([0, INITIAL_WINDOW_SETTING]), u32(RECEIVE_WINDOW))), frame(WINDOW, 0, 0, u32(RECEIVE_WINDOW - DEFAULT_WINDOW)), frame(HEADERS, END_HEADERS, 1, hpack(fields))))
    ;(async () => {
      try {
        const r = request.body.getReader()
        for (;;) {
          while (!closed && queued >= QUEUE_LIMIT) await new Promise((resolve) => { drained = resolve })
          if (closed) break
          const { done, value } = await r.read()
          if (done || closed) break
          if (value.length) { queue.push(value); queued += value.length }
          flush()
        }
        ending = true
        flush()
      } catch (e) { finish(e) }
    })()

    let timer
    const ok = await Promise.race([headed, new Promise((resolve) => { timer = setTimeout(() => resolve(false), HEADERS_WAIT) })])
    clearTimeout(timer)
    if (!ok) { finish(); return grpcError(14, 'signal unavailable') }
    return new Response(readable, { headers: { 'content-type': 'application/grpc', 'x-wiretrustee-peer-registered': '1' } })
  },
}
