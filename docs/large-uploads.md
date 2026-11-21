# Large browser uploads through Cloudflare

Cloudflare refuses a request body over 100 MB on an own domain and on a Free
link (workers.dev). A temporary address has no size limit, but it ends any
request that takes longer than 100 seconds. A browser upload of a large file
through any of these ways would fail, often with nothing on screen.

Syno Connect Center handles this inside the browser page, for every web app that
DSM serves (File Station, Synology Drive, Synology Photos and others). No app
changes and there is no setting. Other clients (desktop sync apps, mobile apps,
WebDAV) keep Cloudflare's limit.

```text
App XHR (one large POST)
   │  wrapped in the page
   ▼
upload.js ──► 2 connections, interleaved pieces ≤ 25 MiB ──► Cloudflare ──► connect-worker
                                                                          │ disk (package var)
                                                                          ▼
                                     one request, original method/URL/headers ──► DSM (loopback)
App XHR ◄── DSM's real status, headers and body ◄────────────────────────────────┘
```

## Where it runs

| Existing | New |
| --- | --- |
| `PinProxy.ServeHTTP` forwards each Cloudflare request (own domain, Free link relay, temporary address) to DSM over the pinned TLS transport | For a public (`cloudflared`) DSM binding, after the Access check and before forwarding, requests under `/_connect/upload` go to the upload handler (`internal/bigupload`) |
| `ModifyResponse` already rewrites selected DSM responses | A `200 text/html` response to a `GET` gets `<script src="/_connect/upload.js?v=…">` right after `<head>`. Every DSM page and same-origin frame loaded through Cloudflare gets it; private network, LAN and App bindings do not |
| `p.tr` dials DSM with the confirmed certificate pin | The upload handler sends its DSM request through the same transport, so pinning, pausing and blocking apply unchanged |
| DSM's CSP allows `script-src 'self'` | Nothing to change: the script is same-origin |
| connect-worker runs as the package account; `${SYNOPKG_PKGVAR}` is on the volume | Temporary data lives in `${SYNOPKG_PKGVAR}/upload-tmp` (never `/tmp`, which is memory on DSM). It is emptied when the worker starts |

The script is written once in TypeScript (`frontend/src/upload/wrapper.ts`),
built into `backend/internal/bigupload/upload.js` by `pnpm --dir frontend run
build:upload`, and embedded in the worker. A frontend test fails when the
embedded copy is stale.

## The page side

`upload.js` replaces a few `XMLHttpRequest` prototype members. A request passes
through untouched unless all of these hold: asynchronous, `POST`/`PUT`/`PATCH`,
same origin, body a `FormData`, `Blob` or buffer, and body larger than 90 MB.

A large body is described, never read into memory:

- a `FormData` becomes parts: each text field and each part header is a small
  byte string; each file is a reference to the `File`;
- a `Blob` or slice (Drive sends `file.slice(offset)`) is one file part;
- the multipart boundary is chosen by the wrapper.

Files are sent with `Blob.slice`, so the browser streams them from disk.

The App's XHR then behaves like a native upload:

| App sees | When |
| --- | --- |
| `loadstart`, `upload.loadstart`, `upload.progress(0)` | synchronously in `send()` |
| `upload.progress` | every 250 ms, byte level within each piece (smoothed, monotonic) |
| `upload.load`, `upload.loadend` | when the NAS has every byte |
| `readystatechange` 2, 3, 4, `progress`, `load`, `loadend` | when DSM answers; `status`, `statusText`, `response`, `responseText`, `responseXML`, `responseURL`, `getResponseHeader` and `getAllResponseHeaders` return DSM's answer, converted for `responseType` |
| `abort` | `abort()` cancels the upload on the NAS, then fires the native event order |
| `timeout` | the App's `timeout` counts from `send()`, as natively |
| `error` | the upload cannot finish (see the failure matrix) |

### Pieces and connections

- At most 2 pieces are in flight. The next piece always goes to whichever
  connection is free, so the two connections interleave and the NAS receives
  the file almost in order.
- Files are divided into 25 MiB slots. A piece lies inside one slot. It starts
  at 8 MiB, then sized for about 30 seconds at each connection's measured
  rate (1 MiB to the rest of the slot). A slow line therefore never meets the
  100-second limit.
- Pieces that fail on network errors, 5xx or 408/429 are retried with backoff
  (1 s doubling to 30 s), with no attempt limit, also while
  `navigator.onLine` is false (the wrapper also retries at once on `online`).
  4xx other than 408/409/429, and 507, are final. A `409` means another
  request for the slot got ahead (see *Pieces sent twice*): the wrapper asks
  where the slot continues and goes on at once.
- **Stall:** a piece with no upload progress in the browser for 45 seconds
  is checked on a fresh request (`GET …/files/{n}?offset=`), then aborted and
  continued from where the NAS stopped. A slow upload is never a failure; only
  no progress at all is.
- **Lost answer:** a piece whose bytes are all sent but whose answer has not
  come back after 12 seconds is checked the same way, then aborted. If the NAS
  has the whole piece it counts as done and is not sent again; otherwise the
  rest is sent. The browser has no request timeout of its own.
- **The wrapper's own requests** (create, check, reuse, fail) are sent again
  when no answer comes within 15 seconds; the result request, which waits up
  to 25 seconds on the NAS, after 40. They are retried without limit and never
  fail the upload by themselves. Each upload carries a random nonce, so a
  create sent again gets the session the first one made instead of a second
  one, without asking DSM again.

### Phones whose link goes quiet

Some phones' Wi-Fi drops packets that arrive after about a second in which the
phone sent nothing. Any answer that takes a second or more (a piece's answer
after its body, create, check, result) is then lost, and the server's TCP
retransmissions back off until the answer comes minutes later or never. This
happens on HTTP/2, HTTP/3 and plain HTTP on the LAN alike; Cloudflare is not
involved. Measured on such a phone, a request every second kept every answer
arriving (30 of 30); one every 3 seconds did not.

So while any request of the wrapper has waited 0.5 seconds for its answer, the
wrapper sends `GET /_connect/upload/ping` every second; it stops when nothing
waits. The ping is same-origin, so it rides the same HTTP/2 connection as the
waiting request. Pages that make no large upload send no pings.

### Pieces sent twice

After a broken connection (`ERR_HTTP2_PING_FAILED`) Chrome may send a piece
again on a new connection by itself, so the NAS can receive the same bytes
twice. A `PUT` is idempotent: bytes the slot already has are skipped, a piece
that is entirely stored is answered `200 {fill}` without being stored again,
and only a piece that starts beyond the slot's fill gets `409 {fill}`. The
wrapper treats any `409` and any duplicate as "ask where the slot continues
and go on", never as an error.

### Phones that pause the page

iOS and iPadOS suspend a page when the screen locks or another app comes to
the front; Android Chrome freezes a tab within seconds once another tab or app
is in front. Streaming to DSM cannot survive that (DSM ends a request body
that is idle for 60 seconds). On iPhone, iPad and Android the wrapper therefore
uses **store** mode: the NAS keeps the pieces and opens the DSM request only
when it has the whole body. When the page comes back, the wrapper asks which
bytes the NAS already has and continues from there. The one constant
`WEBKIT_MOBILE` in `wrapper.ts` turns the wrapper off on iPhone and iPad.

Other browsers use **stream** mode.

## The NAS side

### Protocol

All requests are same-origin and carry the DSM session cookie and the
`X-SYNO-TOKEN` header. Bodies are JSON unless stated.

| Request | Body / answer |
| --- | --- |
| `POST /_connect/upload/sessions` | `{method, url, headers:[[k,v]], parts:[{data:base64} or {file:{key,size}}], mode:"stream"or"store", nonce}` → `201 {id, slot, files:[{have:[slot…]}]}`; the same DSM session and nonce again get the same session |
| `PUT /_connect/upload/sessions/{id}/files/{n}?offset=O` | raw bytes → `200 {fill}` (bytes the slot already has are skipped); `409 {fill}` when `O` is beyond where the slot continues; `409 {wait:true}` when it is too far ahead; `409` when a newer request for the slot took over |
| same, with `X-Upload-Reuse: <sha256>` and an empty body | `200` when the NAS has that complete slot with this hash, else `409` |
| `GET /_connect/upload/sessions/{id}/files/{n}?offset=O` | `{fill}`: where the slot holding `O` continues |
| `GET /_connect/upload/sessions/{id}/result` | waits up to 25 s → `{state:"pending", received}`, `{state:"done", status, statusText, headers, body:base64}` (DSM's `Set-Cookie` are real headers) or `{state:"failed", code}` |
| `POST /_connect/upload/sessions/{id}/fail` | `204`; the page reported an error to the App: the session fails at once, frees its place, and its data waits for reuse |
| `DELETE /_connect/upload/sessions/{id}` | `204`; cancels the DSM request and deletes the data |
| `GET /_connect/upload/ping` | `204`; no session needed (the heartbeat) |
| `GET /_connect/upload.js` | the script |

Errors: `401` DSM says the session is not valid; `403` not this session's
upload; `404` unknown; `410 {state}` the session has ended; `413` request too
large for the protocol; `429` too many sessions (the wrapper waits and tries
again); `503` DSM could not be asked (the wrapper tries again); `507 {message}`
not enough space (not retried).

A custom protocol is used rather than tus. tus sends one upload strictly in
order; interleaving needs its concatenation extension (one upload per piece,
then a final copy into a new file), which doubles disk use and adds a
dependency. Here the NAS keeps one sparse file per file part, accepts pieces by
offset, and needs no final copy.

### Stream to DSM

The DSM request carries the App's method, URL, App headers (minus `Cookie`,
`Host`, `Content-Length`, hop-by-hop and forwarding headers), and the session
request's own `Cookie`, `User-Agent`, `Origin`, `Referer` and
`Accept-Language`. `Content-Length` is the exact body size. The body is read
in order: text parts from memory, file parts from their temp file, waiting for
missing bytes.

- **stream** mode opens the request at the first stored byte; DSM writes while
  pieces arrive.
- **store** mode opens it once every byte is stored.

DSM's answer (at most 1 MiB) is kept in memory for 30 minutes after the
page's last request, so a phone that comes back late still gets it.

### Ordering, buffer and space

- Pieces are written straight into the file part's temp file at their offset.
  Within a slot bytes are accepted only where the slot continues, so each slot
  is hashed (SHA-256) while it is written.
- In stream mode a piece is refused (`409 wait`) if its slot starts more than
  2 slots (50 MiB) after the first missing byte. Data waiting ahead of DSM is
  therefore at most about 2 pieces, on disk, not in memory.
- The whole file part stays on disk until the DSM request ends, so a new
  request after a failure can reuse it.
- A new session reserves the full size of its files, in stream mode too: the
  whole file part stays on disk until DSM answers. It is refused with `507`
  (with a message saying so) unless the volume keeps at least 1 GiB free after
  all reservations. DSM's own staging copy needs the same space again on its
  volume.
- Each size is checked against the space left before it is added, so no sum
  can overflow, and one session has at most 262,144 slots (6.25 TiB). Nothing
  is allocated or written before these checks pass.
- At most 8 sessions per DSM session and 16 in total.
- Memory per connection is one 256 KiB copy buffer.

### Lifetime and cleanup

| Event | Result |
| --- | --- |
| DSM answers | data deleted at once, space released; the answer stays 30 minutes after the last request |
| the page reports an error (`POST …/fail`) | session failed at once, place freed |
| `abort()` or `DELETE` | DSM request cancelled, data deleted |
| tab closed (`pagehide` sends `DELETE` with `keepalive`) | as above |
| no request for a session for 30 minutes | session ended, as a failure |
| failed session's data not reused within 30 minutes | deleted |
| worker start | `upload-tmp` emptied |

Each session and file part has its own timer; nothing runs while there are no
uploads. Space is released by exactly one call per file part.

### Session check

- `POST sessions` asks DSM (loopback, `SYNO.Core.Desktop.Timeout` `check`)
  with the request's cookie and token. Only DSM answering `success:false` is a
  `401`; if DSM cannot be asked (error, restart, a slow answer the page gave
  up on) the answer is `503` and the page tries again. Without a valid session
  nothing is stored.
- The session is bound to a hash of the DSM session cookie and token. Every
  later request must carry the same pair, compared in constant time. Another
  user or another DSM session gets `403` and cannot see or reuse its uploads.
- DSM checks the final request itself, as for any upload.
- The worker runs as the package account. Tokens and cookies stay in memory
  only and are never logged.

## Failure matrix

| Failure | NAS | App's XHR | Data |
| --- | --- | --- | --- |
| Piece hits a network error, 5xx, 408 or 429 | — | still uploading; piece retried with backoff | kept |
| Phone's link drops answers after a second of quiet | answers ping every second while one is awaited | still uploading | kept |
| Piece sent twice (browser repeats it on a new connection) | stored bytes skipped; `200 {fill}` | still uploading | kept once |
| Piece gets `409` (offset mismatch or taken over) | — | asks where the slot continues and goes on at once | kept |
| The browser can no longer read the file (deleted, moved) | — | `error`, not retried | kept 30 min |
| Browser offline | — | still uploading; retried on `online` and with backoff | kept |
| Piece stalls 45 s with no progress | old request ignored from then on | still uploading; continues from the NAS's fill | kept |
| Piece sent, no answer for 12 s | has the piece (or part of it) | still uploading; request aborted, NAS asked; what it has counts, the rest is sent | kept |
| Answer to create, check, reuse or result lost for 15 s (40 s for result) | — | still uploading; request sent again, the same session continues | kept |
| DSM slow or restarting at create | `503` | still uploading; create sent again | — |
| Page paused (phone, store mode) | waits | continues where the NAS stopped | kept |
| Page paused or offline over 60 s in stream mode (desktop) | DSM ends the idle request; session fails `stream_broken` | `error` | kept 30 min for reuse |
| Wrapper gives the App `error` (file unreadable, final status) | `POST …/fail`: session fails, place freed | `error` | kept 30 min for reuse |
| App sends the same file again after an error | new session; complete slots offered | wrapper hashes each offered slot and reuses the ones that match | reused, not uploaded again |
| DSM answers early (for example an error) | the answer is final; further pieces get `410` | DSM's answer | deleted |
| DSM unreachable or certificate pin fails | session fails | `error` | kept 30 min |
| No space, or a size the volume cannot hold (`507`) | nothing stored or allocated | `error`, not retried | — |
| Too many sessions (`429`) | — | waits, tries again | — |
| DSM session expired (`401`) | nothing stored | `error` | — |
| Another session's ID (`403`) | refused | — | untouched |
| `abort()` | DSM request cancelled | `abort` | deleted |
| App `timeout` | DSM request cancelled | `timeout` | deleted |
| Tab closed | `DELETE` or, if lost, 30-minute expiry | — | deleted |
| Worker restart | — | pieces fail, then `error` | emptied on start |

DSM refuses a request it has already seen for Drive and Photos ("request
replayed"), so after a broken DSM request the NAS never sends the stored body
again. The App's own new request has fresh credentials; the wrapper then only
uploads what is missing.

## Limits and notes

- File Station redraws its upload row about every 5 seconds, and Synology
  Photos counts items, not bytes; these are the apps' own behaviour.
- Peak disk use is about twice the file: the temp copy plus DSM's own staging.
- Uploads made with `fetch` or inside a Worker are not covered. DSM's apps
  use XHR.
