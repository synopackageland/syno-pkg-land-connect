/*! Syno Connect Center relay, derived from hostc 88cdb41 (Apache-2.0). See relay/worker/NOTICE and LICENSE. */

// src/protocol/constants.ts
var PROTOCOL_VERSION = 5;
var PROTOCOL_HEADER = "hostc-protocol";
var PING = "hostc:ping";
var PONG = "hostc:pong";
var CLIENT_STALE_MS = 45e3;
var MAX_CHUNK_BYTES = 64 * 1024;
var STREAM_WINDOW_BYTES = 256 * 1024;
var MAX_WEBSOCKET_MESSAGE_BYTES = 1024 * 1024 - 64;
var MAX_STREAMS = 256;
var RESPONSE_TIMEOUT_MS = 12e4;
var CONNECT_GRACE_MS = 2 * 6e4;
var RECONNECT_GRACE_MS = 10 * 6e4;
var CLOSE_SHUTDOWN = 4e3;
var CLOSE_REPLACED = 4001;
var CLOSE_PROTOCOL_ERROR = 1002;

// src/protocol/frame.ts
var FrameType = {
  /** server → client. JSON `OpenMessage`. Starts a stream. */
  Open: 1,
  /** client → server. JSON `HeadMessage`. Response status and headers. */
  Head: 2,
  /** both. HTTP body chunk or binary WebSocket message. */
  Data: 3,
  /** both. Text WebSocket message (UTF-8). */
  Text: 4,
  /** both. End of this direction. Empty for HTTP, close code + reason for WebSockets. */
  End: 5,
  /** both. UTF-8 reason. Aborts the stream in both directions. */
  Reset: 6,
  /** both. u32. Grants more HTTP body bytes to the peer. */
  Window: 7
};
var FRAME_HEADER_BYTES = 5;
var MAX_STREAM_ID = 4294967295;
var ProtocolError = class extends Error {
  name = "ProtocolError";
};
var EMPTY = new Uint8Array(0);
function encodeFrame(type, stream, payload = EMPTY) {
  if (!Number.isInteger(stream) || stream < 1 || stream > MAX_STREAM_ID) {
    throw new ProtocolError(`invalid stream id ${stream}`);
  }
  const bytes = new Uint8Array(FRAME_HEADER_BYTES + payload.byteLength);
  bytes[0] = type;
  new DataView(bytes.buffer).setUint32(1, stream);
  bytes.set(payload, FRAME_HEADER_BYTES);
  return bytes;
}
function decodeFrame(bytes) {
  if (bytes.byteLength < FRAME_HEADER_BYTES) {
    throw new ProtocolError("frame too short");
  }
  const type = bytes[0];
  if (type < FrameType.Open || type > FrameType.Window) {
    throw new ProtocolError(`unknown frame type ${type}`);
  }
  const stream = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(1);
  if (stream === 0) {
    throw new ProtocolError("stream id 0 is reserved");
  }
  return { type, stream, payload: bytes.subarray(FRAME_HEADER_BYTES) };
}

// src/protocol/messages.ts
var encoder = new TextEncoder();
var decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: false });
function encodeText(value) {
  return encoder.encode(value);
}
function decodeText(bytes) {
  try {
    return decoder.decode(bytes);
  } catch {
    throw new ProtocolError("invalid UTF-8");
  }
}
function encodeOpen(message2) {
  return encodeText(JSON.stringify(message2));
}
function decodeHead(bytes) {
  const value = parseJson(bytes);
  if (!Number.isInteger(value.status) || value.status < 100 || value.status > 599 || !isHeaderList(value.headers) || typeof value.body !== "boolean" || value.protocol !== void 0 && typeof value.protocol !== "string") {
    throw new ProtocolError("invalid HEAD payload");
  }
  return value;
}
function encodeClose(message2) {
  const reason = encodeText(message2.reason);
  const bytes = new Uint8Array(2 + reason.byteLength);
  new DataView(bytes.buffer).setUint16(0, message2.code);
  bytes.set(reason, 2);
  return bytes;
}
function decodeClose(bytes) {
  if (bytes.byteLength < 2) {
    return { code: 1005, reason: "" };
  }
  const code = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint16(0);
  return { code, reason: decodeText(bytes.subarray(2)) };
}
function encodeWindow(bytes) {
  const payload = new Uint8Array(4);
  new DataView(payload.buffer).setUint32(0, bytes);
  return payload;
}
function decodeWindow(bytes) {
  if (bytes.byteLength !== 4) {
    throw new ProtocolError("invalid WINDOW payload");
  }
  const value = new DataView(bytes.buffer, bytes.byteOffset, 4).getUint32(0);
  if (value === 0) {
    throw new ProtocolError("empty WINDOW grant");
  }
  return value;
}
function sendableCloseCode(code) {
  if (code === void 0) {
    return 1e3;
  }
  if (code >= 1e3 && code <= 1014 && code !== 1004 && code !== 1005 && code !== 1006) {
    return code;
  }
  if (code >= 3e3 && code <= 4999) {
    return code;
  }
  return 1e3;
}
function sendableCloseReason(reason) {
  if (!reason) {
    return "";
  }
  let result = reason;
  while (encoder.encode(result).byteLength > 123) {
    result = result.slice(0, -1);
  }
  return result;
}
function parseSubprotocols(header) {
  if (!header) {
    return [];
  }
  const protocols = header.split(",").map((protocol) => protocol.trim());
  const valid = protocols.every((protocol) => /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(protocol));
  return valid && new Set(protocols).size === protocols.length ? protocols : null;
}
function parseJson(bytes) {
  let value;
  try {
    value = JSON.parse(decodeText(bytes));
  } catch {
    throw new ProtocolError("invalid JSON payload");
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ProtocolError("JSON payload must be an object");
  }
  return value;
}
function isHeaderList(value) {
  return Array.isArray(value) && value.every(
    (entry) => Array.isArray(entry) && entry.length === 2 && typeof entry[0] === "string" && typeof entry[1] === "string"
  );
}

// src/protocol/headers.ts
var HOP_BY_HOP = /* @__PURE__ */ new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade"
]);
var WEBSOCKET_HANDSHAKE = /* @__PURE__ */ new Set([
  "sec-websocket-accept",
  "sec-websocket-extensions",
  "sec-websocket-key",
  "sec-websocket-protocol",
  "sec-websocket-version"
]);
function stripHopByHop(headers, drop = []) {
  const dropped = new Set(drop);
  for (const [name, value] of headers) {
    if (name.toLowerCase() === "connection") {
      for (const token of value.split(",")) {
        dropped.add(token.trim().toLowerCase());
      }
    }
  }
  return headers.filter(([name]) => {
    const lower = name.toLowerCase();
    return !HOP_BY_HOP.has(lower) && !dropped.has(lower);
  });
}
function stripWebSocketHandshake(headers) {
  return stripHopByHop(headers, WEBSOCKET_HANDSHAKE);
}

// src/token.ts
var encoder2 = new TextEncoder();
var keys = /* @__PURE__ */ new Map();
async function verifyConnectToken(secret, tunnelId, token) {
  const signature = fromBase64Url(token);
  if (!signature) {
    return false;
  }
  return crypto.subtle.verify("HMAC", await importKey(secret), signature, message(tunnelId));
}
function message(tunnelId) {
  return encoder2.encode(`connect:${tunnelId}`);
}
function importKey(secret) {
  if (encoder2.encode(secret).byteLength < 32) {
    throw new Error("RELAY_SECRET must be at least 32 bytes");
  }
  let key = keys.get(secret);
  if (!key) {
    key = crypto.subtle.importKey("raw", encoder2.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, [
      "sign",
      "verify"
    ]);
    keys.set(secret, key);
  }
  return key;
}
function fromBase64Url(value) {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) {
    return null;
  }
  try {
    const binary = atob(value.replaceAll("-", "+").replaceAll("_", "/"));
    return Uint8Array.from(binary, (char) => char.charCodeAt(0));
  } catch {
    return null;
  }
}

// src/tunnel.ts
import { DurableObject } from "cloudflare:workers";

// src/pages.ts
function errorResponse(request, { status, title, message: message2 }) {
  const headers = { "cache-control": "no-store" };
  if (!wantsHtml(request)) {
    return Response.json({ error: title, message: message2 }, { status, headers });
  }
  return html(
    status,
    title,
    `<p class="code">${status}</p><h1>${escape(title)}</h1><p>${escape(message2)}</p>`,
    headers
  );
}
var pages = {
  notFound: {
    status: 404,
    title: "Tunnel not found",
    message: "This link is not connected to anything right now: its tunnel was stopped or has expired. Ask its administrator to check the relay connection."
  },
  offline: {
    status: 502,
    title: "Tunnel offline",
    message: "The computer behind this link is not connected right now. It is probably reconnecting; try again in a moment."
  },
  upstreamFailed: {
    status: 502,
    title: "Local server unavailable",
    message: "The tunnel is up, but the local server behind it did not answer. If it is yours, ask its administrator to check its connection."
  },
  timeout: {
    status: 504,
    title: "Local server timed out",
    message: "The local server behind this link took too long to start responding."
  },
  busy: {
    status: 503,
    title: "Tunnel busy",
    message: "This tunnel is handling too many requests at once. Try again in a moment."
  }
};
function wantsHtml(request) {
  return (request.headers.get("accept") ?? "").includes("text/html");
}
function html(status, title, body, headers) {
  return new Response(
    `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>${escape(title)} \xB7 Syno Connect Center</title>
<style>
:root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: Canvas; color: CanvasText; }
main { max-width: 34rem; padding: 2rem; line-height: 1.6; }
h1 { font-size: 1.5rem; margin: 0 0 1rem; }
.code { font-family: ui-monospace, monospace; opacity: 0.6; margin: 0; }
footer { margin-top: 2rem; font-size: 0.875rem; opacity: 0.6; }
footer a { color: inherit; }
</style>
</head>
<body><main>${body}<footer>Served by <a href="https://github.com/akazwz/hostc">hostc</a>, public URLs for local servers</footer></main></body>
</html>`,
    { status, headers: { "content-type": "text/html; charset=utf-8", ...headers } }
  );
}
function escape(value) {
  return value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
}

// src/tunnel.ts
var CONNECT_URL = "https://relay.invalid/connect";
var STREAM_CEILING_KEY = "streamCeiling";
var STREAM_ID_BLOCK = 1024;
var MAX_STREAM_WINDOW_BYTES = 4 * 1024 * 1024;
var WINDOW_GROWTH_MS = 1e3;
var WINDOW_JITTER_MS = 5;
var WINDOW_BUDGET_BYTES = 16 * 1024 * 1024;
var Tunnel = class extends DurableObject {
  streamIdsLoaded;
  nextStream = 1;
  streamCeiling = 0;
  http = /* @__PURE__ */ new Map();
  pendingWebSockets = /* @__PURE__ */ new Map();
  windowBudget = WINDOW_BUDGET_BYTES;
  constructor(ctx, env) {
    super(ctx, env);
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair(PING, PONG));
  }
  async fetch(request) {
    const isConnect = request.url === CONNECT_URL;
    if (isConnect) {
      return this.acceptClient();
    }
    return request.headers.get("upgrade")?.toLowerCase() === "websocket" ? this.proxyWebSocket(request) : this.proxyHttp(request);
  }
  async alarm() {
    const client = this.client();
    if (client && !isStale(this.ctx, client)) {
      await this.ctx.storage.setAlarm(Date.now() + RECONNECT_GRACE_MS);
      return;
    }
    if (client) {
      this.retire(client, 1001, "heartbeat timeout");
      await this.clientGone(1001);
      return;
    }
    await this.expire();
  }
  async webSocketMessage(ws, message2) {
    const attachment = attachmentOf(ws);
    if (attachment?.kind === "public") {
      this.fromPublic(ws, attachment.stream, message2);
      return;
    }
    if (attachment?.kind !== "client" || attachment.retired || typeof message2 === "string") {
      return;
    }
    try {
      this.fromClient(decodeFrame(new Uint8Array(message2)));
    } catch (error) {
      if (!(error instanceof ProtocolError)) {
        throw error;
      }
      this.retire(ws, CLOSE_PROTOCOL_ERROR, error.message);
      await this.clientGone(CLOSE_PROTOCOL_ERROR);
    }
  }
  async webSocketClose(ws, code, reason) {
    await this.socketGone(ws, code, reason);
  }
  async webSocketError(ws) {
    await this.socketGone(ws, 1006, "");
  }
  async socketGone(ws, code, reason) {
    safeClose(ws, code, reason);
    const attachment = attachmentOf(ws);
    if (attachment?.kind === "public") {
      this.sendFrame(FrameType.End, attachment.stream, encodeClose({ code, reason }));
      return;
    }
    if (attachment?.kind === "client" && !attachment.retired) {
      ws.serializeAttachment({ ...attachment, retired: true });
      await this.clientGone(code);
    }
  }
  // ---------------------------------------------------------------------------
  // Client connection
  async acceptClient() {
    for (const old of this.ctx.getWebSockets("client")) {
      this.retire(old, CLOSE_REPLACED, "replaced by a new connection");
    }
    this.failAll();
    const [client, server] = Object.values(new WebSocketPair());
    this.ctx.acceptWebSocket(server, ["client"]);
    server.serializeAttachment({ kind: "client", since: Date.now() });
    await this.ctx.storage.setAlarm(Date.now() + RECONNECT_GRACE_MS);
    return new Response(null, { status: 101, webSocket: client });
  }
  /** The current client socket, if any. */
  client() {
    return this.ctx.getWebSockets("client").find((ws) => {
      const attachment = attachmentOf(ws);
      return attachment?.kind === "client" && !attachment.retired && ws.readyState === WebSocket.OPEN;
    });
  }
  /** The current client, unless it has stopped answering heartbeats. */
  async liveClient() {
    const client = this.client();
    if (client && isStale(this.ctx, client)) {
      this.retire(client, 1001, "heartbeat timeout");
      await this.clientGone(1001);
      return void 0;
    }
    return client;
  }
  /** Marks a client socket as no longer current, then closes it. Its later events are ignored. */
  retire(ws, code, reason) {
    const attachment = attachmentOf(ws);
    if (attachment?.kind === "client") {
      ws.serializeAttachment({ ...attachment, retired: true });
    }
    safeClose(ws, code, reason);
  }
  async clientGone(code) {
    this.failAll();
    if (code === CLOSE_SHUTDOWN) {
      await this.expire();
      return;
    }
    await this.ctx.storage.setAlarm(Date.now() + RECONNECT_GRACE_MS);
  }
  async expire() {
    for (const ws of this.ctx.getWebSockets()) {
      this.retire(ws, 1001, "tunnel expired");
    }
    this.failAll();
    await this.ctx.storage.deleteAlarm();
  }
  /** Ends every stream. The client's local sockets and requests are gone with its connection. */
  failAll() {
    for (const stream of this.http.values()) {
      this.fail(stream, pages.offline);
    }
    for (const [id, pending] of this.pendingWebSockets) {
      this.pendingWebSockets.delete(id);
      pending.settle(new Response("Tunnel offline", { status: 502 }));
    }
    for (const ws of this.ctx.getWebSockets("public")) {
      safeClose(ws, 1001, "tunnel disconnected");
    }
  }
  fromClient(frame) {
    const { stream: id, payload } = frame;
    const http = this.http.get(id);
    switch (frame.type) {
      case FrameType.Open:
        throw new ProtocolError("OPEN is server-to-client only");
      case FrameType.Head: {
        let head;
        let headers;
        try {
          head = decodeHead(payload);
          headers = http ? publicResponseHeaders(head.headers) : publicResponseHeaders(stripWebSocketHandshake(head.headers));
        } catch {
          this.sendFrame(FrameType.Reset, id, encodeText("invalid response head"));
          if (http) {
            this.fail(http, pages.upstreamFailed);
          }
          this.pendingWebSockets.get(id)?.settle(new Response("Invalid response from local server", { status: 502 }));
          this.pendingWebSockets.delete(id);
          return;
        }
        if (http) {
          if (http.headReceived) {
            throw new ProtocolError("duplicate HEAD");
          }
          http.headReceived = true;
          http.settleHead({ ok: true, head, headers });
        } else {
          this.settleWebSocket(id, head, headers);
        }
        return;
      }
      case FrameType.Data:
      case FrameType.Text: {
        if (http) {
          this.receiveBody(http, frame);
          return;
        }
        if (payload.byteLength > MAX_WEBSOCKET_MESSAGE_BYTES) {
          throw new ProtocolError("WebSocket message too big");
        }
        const publicSocket = this.publicSocket(id);
        if (publicSocket?.readyState === WebSocket.OPEN) {
          publicSocket.send(frame.type === FrameType.Text ? decodeText(payload) : payload);
        }
        return;
      }
      case FrameType.End: {
        if (http) {
          http.ended = true;
          this.flush(http);
          return;
        }
        const close = decodeClose(payload);
        const publicSocket = this.publicSocket(id);
        if (publicSocket) {
          safeClose(publicSocket, close.code, close.reason);
        }
        return;
      }
      case FrameType.Reset: {
        if (http) {
          this.fail(http, pages.upstreamFailed);
          return;
        }
        const pending = this.pendingWebSockets.get(id);
        if (pending) {
          this.pendingWebSockets.delete(id);
          pending.settle(new Response("Local WebSocket unavailable", { status: 502 }));
          return;
        }
        const publicSocket = this.publicSocket(id);
        if (publicSocket) {
          safeClose(publicSocket, 1011, decodeText(payload));
        }
        return;
      }
      case FrameType.Window: {
        const bytes = decodeWindow(payload);
        if (http) {
          http.sendCredit += bytes;
          http.wakeUploader?.();
        }
        return;
      }
    }
  }
  sendFrame(type, id, payload) {
    const client = this.client();
    if (client) {
      client.send(encodeFrame(type, id, payload));
    }
  }
  /**
   * Stream ids only ever increase for the lifetime of the tunnel, including across hibernation,
   * so a late frame can never be mistaken for a newer stream.
   */
  async allocateStream() {
    this.streamIdsLoaded ??= this.ctx.storage.get(STREAM_CEILING_KEY).then((ceiling = 1) => {
      this.nextStream = ceiling;
      this.streamCeiling = ceiling;
    });
    await this.streamIdsLoaded;
    if (this.nextStream > 4294967295) {
      throw new ProtocolError("stream ids exhausted; replace Worker");
    }
    const id = this.nextStream++;
    if (id >= this.streamCeiling) {
      this.streamCeiling = id + STREAM_ID_BLOCK;
      void this.ctx.storage.put(STREAM_CEILING_KEY, this.streamCeiling);
    }
    return id;
  }
  streamCount() {
    return this.http.size + this.pendingWebSockets.size + this.ctx.getWebSockets("public").length;
  }
  // ---------------------------------------------------------------------------
  // Public HTTP
  async proxyHttp(request) {
    if (!await this.liveClient()) {
      return errorResponse(request, pages.offline);
    }
    if (this.streamCount() >= MAX_STREAMS) {
      return errorResponse(request, pages.busy);
    }
    const id = await this.allocateStream();
    let settleHead;
    const headResult = new Promise((resolve) => {
      settleHead = resolve;
    });
    const stream = {
      id,
      settleHead,
      headReceived: false,
      sendCredit: STREAM_WINDOW_BYTES,
      wakeUploader: null,
      uploading: request.body !== null,
      receiveCredit: STREAM_WINDOW_BYTES,
      window: STREAM_WINDOW_BYTES,
      windowStartedAt: 0,
      windowBytes: 0,
      fastestWindowMs: 0,
      queue: [],
      pulling: false,
      ended: false,
      controller: null,
      done: false
    };
    this.http.set(id, stream);
    const url = new URL(request.url);
    this.sendFrame(
      FrameType.Open,
      id,
      encodeOpen({
        method: request.method,
        path: url.pathname + url.search,
        headers: forwardedRequestHeaders(request.headers),
        body: request.body !== null
      })
    );
    if (request.body) {
      void this.upload(stream, request.body);
    }
    request.signal.addEventListener("abort", () => this.reset(stream, "public client went away"));
    const result = await withTimeout(headResult, RESPONSE_TIMEOUT_MS);
    if (!result) {
      this.reset(stream, "response timeout");
      return errorResponse(request, pages.timeout);
    }
    if (!result.ok) {
      return errorResponse(request, result.page);
    }
    const { head, headers } = result;
    if (head.status < 200) {
      this.reset(stream, `unsupported status ${head.status}`);
      return errorResponse(request, pages.upstreamFailed);
    }
    if (!head.body || NULL_BODY_STATUSES.has(head.status)) {
      this.finish(stream);
      return new Response(null, { status: head.status, headers });
    }
    const body = new ReadableStream(
      {
        start: (controller) => {
          stream.controller = controller;
        },
        pull: () => {
          stream.pulling = true;
          this.flush(stream);
        },
        cancel: () => this.reset(stream, "public client went away")
      },
      { highWaterMark: 0 }
    );
    let output = body;
    const length = headers.get("content-length");
    if (length !== null) {
      const fixed = new FixedLengthStream(Number(length));
      this.ctx.waitUntil(body.pipeTo(fixed.writable).catch(() => this.reset(stream, "response length mismatch")));
      output = fixed.readable;
    }
    return new Response(output, {
      status: head.status,
      headers,
      // Compressed bodies from the local server are passed through untouched.
      encodeBody: headers.has("content-encoding") ? "manual" : "automatic"
    });
  }
  /** Streams the public request body to the client, never exceeding the granted window. */
  async upload(stream, body) {
    const reader = body.getReader();
    try {
      for (; ; ) {
        const { done, value } = await reader.read();
        if (done) {
          break;
        }
        for (let offset = 0; offset < value.byteLength; offset += MAX_CHUNK_BYTES) {
          const chunk = value.subarray(offset, offset + MAX_CHUNK_BYTES);
          while (!stream.done && stream.sendCredit < chunk.byteLength) {
            await new Promise((resolve) => {
              stream.wakeUploader = resolve;
            });
          }
          if (stream.done) {
            await reader.cancel();
            return;
          }
          stream.sendCredit -= chunk.byteLength;
          this.sendFrame(FrameType.Data, stream.id, chunk);
        }
      }
      stream.uploading = false;
      if (!stream.done) {
        this.sendFrame(FrameType.End, stream.id);
      }
    } catch {
      this.reset(stream, "public request body failed");
    }
  }
  receiveBody(stream, frame) {
    if (frame.type !== FrameType.Data || !stream.headReceived || stream.ended) {
      throw new ProtocolError("unexpected response body frame");
    }
    if (frame.payload.byteLength > MAX_CHUNK_BYTES || frame.payload.byteLength > stream.receiveCredit) {
      throw new ProtocolError("response body exceeded the flow control window");
    }
    stream.receiveCredit -= frame.payload.byteLength;
    stream.queue.push(frame.payload);
    this.flush(stream);
  }
  /** Hands queued chunks to the public response as it pulls, returning window to the client. */
  flush(stream) {
    const { controller } = stream;
    if (!controller || stream.done) {
      return;
    }
    while (stream.pulling && stream.queue.length > 0) {
      const chunk = stream.queue.shift();
      stream.pulling = false;
      controller.enqueue(chunk);
      const grant = chunk.byteLength + this.growWindow(stream, chunk.byteLength);
      stream.receiveCredit += grant;
      this.sendFrame(FrameType.Window, stream.id, encodeWindow(grant));
    }
    if (stream.ended && stream.queue.length === 0) {
      controller.close();
      this.finish(stream);
    }
  }
  /**
   * Counts bytes handed to the visitor and, after each full window, decides whether the window held
   * the stream back. While it does, a window takes one round trip whatever its size, so a doubled
   * window takes no longer than the fastest one. Once the link is the limit, a doubled window takes
   * twice as long and growing further would only queue data ahead of other streams. Nothing queued
   * means the visitor keeps up. Returns the bytes to grant on top.
   */
  growWindow(stream, bytes) {
    stream.windowBytes += bytes;
    if (stream.windowBytes < stream.window) {
      return 0;
    }
    const now = Date.now();
    const elapsed = now - stream.windowStartedAt;
    const timed = stream.windowStartedAt > 0;
    stream.windowStartedAt = now;
    stream.windowBytes = 0;
    if (!timed) {
      return 0;
    }
    stream.fastestWindowMs = stream.fastestWindowMs === 0 ? elapsed : Math.min(stream.fastestWindowMs, elapsed);
    const limitedByWindow = elapsed < WINDOW_GROWTH_MS && elapsed <= stream.fastestWindowMs * 1.5 + WINDOW_JITTER_MS && stream.queue.length === 0;
    if (!limitedByWindow) {
      return 0;
    }
    const extra = Math.min(stream.window, MAX_STREAM_WINDOW_BYTES - stream.window, this.windowBudget);
    stream.window += extra;
    this.windowBudget -= extra;
    return extra;
  }
  /** Normal completion of the response. */
  finish(stream) {
    if (stream.done) {
      return;
    }
    if (stream.uploading) {
      this.sendFrame(FrameType.Reset, stream.id, encodeText("response finished"));
    }
    this.close(stream);
  }
  /** Aborts a stream from our side and tells the client. */
  reset(stream, reason) {
    if (stream.done) {
      return;
    }
    this.sendFrame(FrameType.Reset, stream.id, encodeText(reason));
    this.fail(stream, pages.upstreamFailed);
  }
  /** Aborts a stream without telling the client (it reset it, or it is gone). */
  fail(stream, page) {
    if (stream.done) {
      return;
    }
    stream.settleHead({ ok: false, page });
    try {
      stream.controller?.error(new Error(page.title));
    } catch {
    }
    this.close(stream);
  }
  close(stream) {
    stream.done = true;
    stream.queue = [];
    this.http.delete(stream.id);
    this.windowBudget += stream.window - STREAM_WINDOW_BYTES;
    stream.wakeUploader?.();
  }
  // ---------------------------------------------------------------------------
  // Public WebSockets
  async proxyWebSocket(request) {
    if (!await this.liveClient()) {
      return new Response("Tunnel offline", { status: 502 });
    }
    if (this.streamCount() >= MAX_STREAMS) {
      return new Response("Tunnel busy", { status: 503 });
    }
    const offered = parseSubprotocols(request.headers.get("sec-websocket-protocol"));
    if (!offered) {
      return new Response("Invalid Sec-WebSocket-Protocol header", { status: 400 });
    }
    const id = await this.allocateStream();
    const accepted = new Promise((settle) => {
      this.pendingWebSockets.set(id, { offered, settle });
    });
    const url = new URL(request.url);
    this.sendFrame(
      FrameType.Open,
      id,
      encodeOpen({
        method: request.method,
        path: url.pathname + url.search,
        headers: stripWebSocketHandshake(forwardedRequestHeaders(request.headers)),
        body: false,
        websocket: offered
      })
    );
    const response = await withTimeout(accepted, RESPONSE_TIMEOUT_MS);
    if (response) {
      return response;
    }
    this.pendingWebSockets.delete(id);
    this.sendFrame(FrameType.Reset, id, encodeText("response timeout"));
    return new Response("Local server timed out", { status: 504 });
  }
  /**
   * Runs inside the HEAD frame handler, so the public socket is accepted before
   * any following DATA frame for this stream is processed.
   */
  settleWebSocket(id, head, headers) {
    const pending = this.pendingWebSockets.get(id);
    if (!pending) {
      return;
    }
    this.pendingWebSockets.delete(id);
    if (head.status !== 101) {
      const status = head.status >= 200 && !NULL_BODY_STATUSES.has(head.status) ? head.status : 502;
      pending.settle(new Response(null, { status, headers }));
      return;
    }
    if (head.protocol !== void 0 && !pending.offered.includes(head.protocol)) {
      this.sendFrame(FrameType.Reset, id, encodeText("server selected a subprotocol that was not offered"));
      pending.settle(new Response("Invalid WebSocket subprotocol", { status: 502 }));
      return;
    }
    const [client, server] = Object.values(new WebSocketPair());
    this.ctx.acceptWebSocket(server, ["public", `s:${id}`]);
    server.serializeAttachment({ kind: "public", stream: id });
    if (head.protocol) {
      headers.set("sec-websocket-protocol", head.protocol);
    }
    pending.settle(new Response(null, { status: 101, webSocket: client, headers }));
  }
  fromPublic(ws, id, message2) {
    if (!this.client()) {
      safeClose(ws, 1001, "tunnel disconnected");
      return;
    }
    const text = typeof message2 === "string";
    const payload = text ? encodeText(message2) : new Uint8Array(message2);
    if (payload.byteLength > MAX_WEBSOCKET_MESSAGE_BYTES) {
      safeClose(ws, 1009, "message too big");
      this.sendFrame(FrameType.End, id, encodeClose({ code: 1009, reason: "message too big" }));
      return;
    }
    this.sendFrame(text ? FrameType.Text : FrameType.Data, id, payload);
  }
  publicSocket(id) {
    return this.ctx.getWebSockets(`s:${id}`)[0];
  }
};
var NULL_BODY_STATUSES = /* @__PURE__ */ new Set([101, 204, 205, 304]);
function forwardedRequestHeaders(headers) {
  const list = [];
  for (const [name, value] of headers) {
    if (!name.startsWith("cf-") && name !== "cdn-loop" && name !== "host") {
      list.push([name, value]);
    }
  }
  return stripHopByHop(list);
}
function publicResponseHeaders(list) {
  const headers = new Headers();
  for (const [name, value] of stripHopByHop(list)) {
    if (name.toLowerCase() === "set-cookie") {
      headers.append(name, withoutCookieDomain(value));
      continue;
    }
    headers.append(name, value);
  }
  const length = headers.get("content-length");
  if (length !== null && (!/^(0|[1-9][0-9]*)$/.test(length) || !Number.isSafeInteger(Number(length)))) {
    throw new ProtocolError("invalid content-length");
  }
  return headers;
}
function withoutCookieDomain(cookie) {
  const [pair = "", ...attributes] = cookie.split(";");
  const kept = attributes.filter((attribute) => attribute.split("=")[0]?.trim().toLowerCase() !== "domain");
  return [pair, ...kept].join(";");
}
function attachmentOf(ws) {
  return ws.deserializeAttachment();
}
function isStale(ctx, client) {
  const attachment = attachmentOf(client);
  const lastSeen = ctx.getWebSocketAutoResponseTimestamp(client)?.getTime() ?? (attachment?.kind === "client" ? attachment.since : 0);
  return Date.now() - lastSeen > CLIENT_STALE_MS;
}
function safeClose(ws, code, reason) {
  try {
    ws.close(sendableCloseCode(code), sendableCloseReason(reason));
  } catch {
  }
}
function withTimeout(promise, ms) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => resolve(void 0), ms);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      }
    );
  });
}

// src/index.ts
var TUNNEL_ID = "default";
var CONNECT_PATH = "/_spl/relay/connect";
var index_default = {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname === CONNECT_PATH) {
      if (request.headers.get(PROTOCOL_HEADER) !== String(PROTOCOL_VERSION)) {
        return Response.json({ error: "Unsupported relay protocol" }, { status: 426 });
      }
      if (request.method !== "GET" || request.headers.get("upgrade")?.toLowerCase() !== "websocket") {
        return Response.json({ error: "Expected a WebSocket upgrade" }, { status: 426 });
      }
      const token = request.headers.get("authorization")?.match(/^Bearer (\S+)$/)?.[1];
      if (!env.RELAY_SECRET || env.RELAY_SECRET.length < 32 || !token || !await verifyConnectToken(env.RELAY_SECRET, TUNNEL_ID, token)) {
        return Response.json({ error: "Invalid relay credentials" }, { status: 401 });
      }
      return env.TUNNEL.getByName(TUNNEL_ID).fetch(new Request(CONNECT_URL, request));
    }
    return env.TUNNEL.getByName(TUNNEL_ID).fetch(request);
  }
};
export {
  CONNECT_PATH,
  TUNNEL_ID,
  Tunnel,
  index_default as default
};
