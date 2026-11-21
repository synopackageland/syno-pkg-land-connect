// Large uploads through Cloudflare: injected into DSM pages served through a
// Cloudflare way, this wraps XMLHttpRequest so an App's upload larger than
// Cloudflare accepts is carried as pieces to the NAS, which sends it to DSM as
// one request. The App sees native events and DSM's real answer.
// See docs/large-uploads.md. Built into backend/internal/bigupload/upload.js.

// Turns the wrapper on for iPhone and iPad (WebKit on mobile). Set to false to
// leave uploads there untouched.
const WEBKIT_MOBILE = true

export const THRESHOLD = 90 * 1024 * 1024
const BASE = '/_connect/upload/sessions'
const FIRST_PIECE = 8 * 1024 * 1024
const MIN_PIECE = 1024 * 1024
const PIECE_SECONDS = 30
export const STALL_MS = 45_000
export const ANSWER_MS = 30_000
// The wrapper's own small requests (create, check, reuse, cancel) are given up
// after this long without an answer and sent again: Cloudflare can hold an
// answer on its way back to a phone for minutes. The result request waits up
// to 25 s on the NAS, so it gets that much more.
export const CONTROL_MS = 30_000
const RESULT_MS = 25_000 + CONTROL_MS
const TICK_MS = 250
const MAX_DATA = 512 * 1024
const PATCHED = Symbol.for('connect.upload.patched')

export interface Deps {
  now: () => number
  setTimeout: (f: () => void, ms: number) => unknown
  clearTimeout: (t: unknown) => void
  setInterval: (f: () => void, ms: number) => unknown
  clearInterval: (t: unknown) => void
  digest: ((data: ArrayBuffer) => Promise<ArrayBuffer>) | null
  threshold: number
}

type Part = { data: Uint8Array } | { file: Blob, key: string }
interface Answer { status: number, statusText: string, headers: [string, string][], body: Uint8Array }
interface Piece { xhr: XMLHttpRequest, n: number, off: number, len: number, sent: number, start: number, moved: number, probing: boolean }
interface FilePart { blob: Blob, size: number, next: number[], busy: boolean[] }

interface Big {
  phase: 'upload' | 'answer' | 'done' | 'ended'
  method: string
  url: URL
  headers: [string, string][]
  token: string
  parts: Part[]
  files: FilePart[]
  total: number
  dataBytes: number
  mode: 'stream' | 'store'
  id: string
  slot: number
  confirmed: number
  shown: number
  pieces: Set<Piece>
  rate: number
  answer: Answer | null
  readyState: number
  responseType: XMLHttpRequestResponseType
  timers: Set<unknown>
  tick: unknown
  appTimer: unknown
  backoff: number
  uploadDone: boolean
  cancelled: boolean
  nonce: string
}

interface Opened { method: string, url: string, async: boolean, headers: [string, string][] }

const utf8 = new TextEncoder()

function b64(bytes: Uint8Array): string {
  let s = ''
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(s)
}

function unb64(s: string): Uint8Array {
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function hex(buf: ArrayBuffer): string {
  return Array.from(new Uint8Array(buf), b => b.toString(16).padStart(2, '0')).join('')
}

function fileKey(b: Blob): string {
  const f = b as Partial<File>
  return `${b.size}:${f.name ?? ''}:${f.lastModified ?? ''}:${b.type}`
}

// describe turns a body into parts without reading any file, plus the
// Content-Type the browser would send. null: not a body this wrapper carries.
export function describe(body: unknown, win: Window, boundary: string): { parts: Part[], type: string } | null {
  const W = win as unknown as { FormData: typeof FormData, Blob: typeof Blob }
  if (body instanceof W.FormData) {
    const parts: Part[] = []
    const text = (s: string) => {
      const last = parts[parts.length - 1]
      const bytes = utf8.encode(s)
      if (last && 'data' in last) {
        const joined = new Uint8Array(last.data.length + bytes.length)
        joined.set(last.data)
        joined.set(bytes, last.data.length)
        last.data = joined
      } else parts.push({ data: bytes })
    }
    const esc = (s: string) => s.replace(/\r/g, '%0D').replace(/\n/g, '%0A').replace(/"/g, '%22')
    body.forEach((v, name) => {
      if (typeof v === 'string') {
        text(`--${boundary}\r\nContent-Disposition: form-data; name="${esc(name)}"\r\n\r\n${v.replace(/\r?\n|\r/g, '\r\n')}\r\n`)
      } else {
        text(`--${boundary}\r\nContent-Disposition: form-data; name="${esc(name)}"; filename="${esc(v.name || 'blob')}"\r\nContent-Type: ${v.type || 'application/octet-stream'}\r\n\r\n`)
        if (v.size > 0) parts.push({ file: v, key: fileKey(v) })
        text('\r\n')
      }
    })
    text(`--${boundary}--\r\n`)
    return { parts, type: `multipart/form-data; boundary=${boundary}` }
  }
  if (body instanceof W.Blob) return { parts: body.size ? [{ file: body, key: fileKey(body) }] : [], type: body.type }
  if (body instanceof ArrayBuffer || ArrayBuffer.isView(body)) {
    const blob = new W.Blob([body as BlobPart])
    return { parts: [{ file: blob, key: fileKey(blob) }], type: '' }
  }
  return null
}

function partSize(p: Part): number {
  return 'data' in p ? p.data.length : p.file.size
}

// readable tells a file the browser can no longer read (deleted, moved,
// permission gone), which fails like a network error but never recovers.
async function readable(blob: Blob, off: number): Promise<boolean> {
  try {
    await blob.slice(off, off + 1).arrayBuffer()
    return true
  } catch {
    return false
  }
}

// nonce names one upload, so a create sent again after a lost answer finds
// the session the first one made.
function nonce(win: Window): string {
  const b = new Uint8Array(16)
  if (win.crypto?.getRandomValues) win.crypto.getRandomValues(b)
  else for (let i = 0; i < b.length; i++) b[i] = Math.floor(Math.random() * 256)
  return Array.from(b, v => v.toString(16).padStart(2, '0')).join('')
}

function isMobileWebKit(win: Window): boolean {
  const n = win.navigator
  return /iPhone|iPad|iPod/.test(n.userAgent) || (/Macintosh/.test(n.userAgent) && (n.maxTouchPoints ?? 0) > 1)
}

function defaultDeps(win: Window): Deps {
  const subtle = win.crypto?.subtle
  return {
    now: () => win.performance.now(),
    setTimeout: (f, ms) => win.setTimeout(f, ms),
    clearTimeout: t => win.clearTimeout(t as number),
    setInterval: (f, ms) => win.setInterval(f, ms),
    clearInterval: t => win.clearInterval(t as number),
    digest: subtle ? data => subtle.digest('SHA-256', data) : null,
    threshold: THRESHOLD,
  }
}

export function install(win: Window, given?: Partial<Deps>): boolean {
  const W = win as unknown as { XMLHttpRequest: typeof XMLHttpRequest, ProgressEvent: typeof ProgressEvent, Event: typeof Event, DOMException: typeof DOMException, DOMParser?: typeof DOMParser }
  const X = W.XMLHttpRequest
  if (!X) return false
  const P = X.prototype as XMLHttpRequest & Record<symbol, unknown>
  if (P[PATCHED]) return false
  const webkit = isMobileWebKit(win)
  if (webkit && !WEBKIT_MOBILE) return false
  // Phones pause or freeze a page that leaves the screen (iOS on lock or app
  // switch; Android Chrome for a background tab within seconds), which would
  // break a stream to DSM. There the NAS stores the whole body first.
  const mobile = webkit || /Android/.test(win.navigator.userAgent)
  P[PATCHED] = true
  const deps: Deps = { ...defaultDeps(win), ...given }
  const desc = (k: string) => {
    for (let p: object | null = X.prototype; p; p = Object.getPrototypeOf(p)) {
      const d = Object.getOwnPropertyDescriptor(p, k)
      if (d) return d
    }
    return undefined
  }
  const o = {
    open: P.open, send: P.send, setRequestHeader: P.setRequestHeader, abort: P.abort,
    getResponseHeader: P.getResponseHeader, getAllResponseHeaders: P.getAllResponseHeaders,
  }
  const opened = new WeakMap<XMLHttpRequest, Opened>()
  const bigs = new WeakMap<XMLHttpRequest, Big>()
  const live = new Set<Big>()

  // ---- requests of the wrapper itself (native methods, never wrapped) ----

  interface Res { status: number, json: any }
  // request sends one of the wrapper's requests. A request without an answer
  // after limit ms (0: no limit) is aborted and reported as a network error.
  function request(big: Big, method: string, path: string, body: Document | XMLHttpRequestBodyInit | null, headers: [string, string][] = [], onXhr?: (x: XMLHttpRequest) => void, limit = CONTROL_MS): Promise<Res> {
    return new Promise(done => {
      let settled = false
      let timer: unknown
      const resolve = (r: Res) => {
        if (settled) return
        settled = true
        if (timer !== undefined) deps.clearTimeout(timer)
        done(r)
      }
      const x = new X()
      o.open.call(x, method, path, true)
      o.setRequestHeader.call(x, 'X-SYNO-TOKEN', big.token)
      for (const [k, v] of headers) o.setRequestHeader.call(x, k, v)
      x.onload = () => {
        let json: any = null
        try { json = JSON.parse(x.responseText) } catch { /* not JSON */ }
        resolve({ status: x.status, json })
      }
      x.onerror = x.ontimeout = () => resolve({ status: 0, json: null })
      x.onabort = () => resolve({ status: -1, json: null })
      onXhr?.(x)
      o.send.call(x, body)
      if (limit > 0) {
        timer = deps.setTimeout(() => {
          resolve({ status: 0, json: null })
          o.abort.call(x)
        }, limit)
      }
    })
  }

  function later(big: Big, ms: number, f: () => void) {
    const t = deps.setTimeout(() => { big.timers.delete(t); f() }, ms)
    big.timers.add(t)
  }

  const pauseFor = (big: Big) => {
    const ms = big.backoff
    big.backoff = Math.min(big.backoff * 2, 30_000)
    return ms
  }

  // Retry soon after the browser is online again.
  const waiting = new Set<() => void>()
  win.addEventListener('online', () => {
    const all = [...waiting]
    waiting.clear()
    for (const f of all) f()
  })
  function retry(big: Big, f: () => void) {
    let fired = false
    const run = () => { if (!fired) { fired = true; waiting.delete(run); if (active(big)) f() } }
    waiting.add(run)
    later(big, pauseFor(big), run)
  }

  const active = (big: Big) => big.phase === 'upload' || big.phase === 'answer'

  // ---- events ----

  function fire(target: EventTarget, type: string, loaded?: number, total?: number) {
    target.dispatchEvent(loaded === undefined ? new W.Event(type) : new W.ProgressEvent(type, { lengthComputable: total !== undefined && total > 0, loaded, total }))
  }

  function stopTimers(big: Big) {
    for (const t of big.timers) deps.clearTimeout(t)
    big.timers.clear()
    if (big.tick !== undefined) deps.clearInterval(big.tick)
    big.tick = undefined
    if (big.appTimer !== undefined) deps.clearTimeout(big.appTimer)
    big.appTimer = undefined
    for (const p of big.pieces) o.abort.call(p.xhr)
    big.pieces.clear()
    live.delete(big)
  }

  function cancelRemote(big: Big, keepalive = false) {
    big.cancelled = true
    if (!big.id) return
    const path = `${BASE}/${big.id}`
    const f = (win as unknown as { fetch?: typeof fetch }).fetch
    if (keepalive && f) {
      f.call(win, path, { method: 'DELETE', keepalive: true, credentials: 'same-origin', headers: { 'X-SYNO-TOKEN': big.token } }).catch(() => {})
      return
    }
    void request(big, 'DELETE', path, null)
  }

  // end finishes an upload that will not get DSM's answer.
  function end(x: XMLHttpRequest, big: Big, kind: 'abort' | 'error' | 'timeout') {
    if (!active(big)) return
    big.phase = 'ended'
    stopTimers(big)
    if (kind !== 'error') cancelRemote(big)
    big.readyState = 4
    fire(x, 'readystatechange')
    if (!big.uploadDone) {
      fire(x.upload, kind, 0, 0)
      fire(x.upload, 'loadend', 0, 0)
    }
    fire(x, kind, 0, 0)
    fire(x, 'loadend', 0, 0)
    if (kind === 'abort') big.readyState = 0
  }

  function deliver(x: XMLHttpRequest, big: Big, a: Answer) {
    if (!active(big)) return
    finishUpload(x, big)
    big.phase = 'done'
    stopTimers(big)
    big.answer = a
    big.readyState = 2
    fire(x, 'readystatechange')
    big.readyState = 3
    fire(x, 'readystatechange')
    fire(x, 'progress', a.body.length, a.body.length)
    big.readyState = 4
    fire(x, 'readystatechange')
    fire(x, 'load', a.body.length, a.body.length)
    fire(x, 'loadend', a.body.length, a.body.length)
  }

  function finishUpload(x: XMLHttpRequest, big: Big) {
    if (big.uploadDone) return
    big.uploadDone = true
    if (big.tick !== undefined) deps.clearInterval(big.tick)
    big.tick = undefined
    big.shown = big.total
    fire(x.upload, 'progress', big.total, big.total)
    fire(x.upload, 'load', big.total, big.total)
    fire(x.upload, 'loadend', big.total, big.total)
  }

  // ---- progress ----

  function estimate(big: Big): number {
    let v = big.confirmed
    const now = deps.now()
    for (const p of big.pieces) {
      // The browser hands bytes to its socket faster than they reach the NAS;
      // once a rate is known, a piece counts no faster than that rate.
      v += big.rate > 0 ? Math.min(p.sent, big.rate * (now - p.start)) : p.sent
    }
    big.shown = Math.max(big.shown, Math.min(Math.floor(v), big.total - 1))
    return big.shown
  }

  function tick(x: XMLHttpRequest, big: Big) {
    if (big.phase !== 'upload' || big.uploadDone) return
    const before = big.shown
    const v = estimate(big)
    if (v !== before) fire(x.upload, 'progress', v, big.total)
    watch(x, big)
  }

  // ---- stalls and lost answers ----

  function watch(x: XMLHttpRequest, big: Big) {
    const now = deps.now()
    for (const p of big.pieces) {
      if (p.probing) continue
      const idle = now - p.moved
      if ((p.sent >= p.len && idle >= ANSWER_MS) || (p.sent < p.len && idle >= STALL_MS)) void probe(x, big, p)
    }
  }

  async function probe(x: XMLHttpRequest, big: Big, p: Piece) {
    p.probing = true
    const r = await request(big, 'GET', `${BASE}/${big.id}/files/${p.n}?offset=${p.off}`, null)
    if (!active(big) || !big.pieces.has(p)) return
    if (r.status === 200 && typeof r.json?.fill === 'number') {
      // Whatever the old request still delivers is superseded by the next piece.
      big.pieces.delete(p)
      o.abort.call(p.xhr)
      stored(x, big, p.n, p.off, r.json.fill)
      schedule(x, big)
      return
    }
    if (r.status === 410) return answerPhase(x, big)
    if (isFinal(r.status)) return end(x, big, 'error')
    p.probing = false
    p.moved = deps.now() // check again after another period
  }

  // ---- pieces ----

  // stored records that slot bytes up to fill (file offset) are on the NAS.
  function stored(x: XMLHttpRequest, big: Big, n: number, off: number, fill: number) {
    const f = big.files[n]
    const i = Math.floor(off / big.slot)
    const start = i * big.slot
    const end = Math.min(start + big.slot, f.size)
    const was = start + f.next[i]
    const now = Math.max(start, Math.min(fill, end))
    big.confirmed += now - was
    f.next[i] = now - start
    f.busy[i] = false
    void x
  }

  const isFinal = (status: number) => status === 401 || status === 403 || status === 404 || status === 507 || status === 400 || status === 411 || status === 413

  function pieceSize(big: Big, remaining: number): number {
    const want = big.rate > 0 ? big.rate * PIECE_SECONDS * 1000 : FIRST_PIECE
    return Math.max(Math.min(MIN_PIECE, remaining), Math.min(remaining, Math.floor(want / MIN_PIECE) * MIN_PIECE || MIN_PIECE))
  }

  function schedule(x: XMLHttpRequest, big: Big) {
    if (big.phase !== 'upload') return
    let left = false
    for (let n = 0; n < big.files.length; n++) {
      const f = big.files[n]
      for (let i = 0; i < f.next.length; i++) {
        const len = Math.min(big.slot, f.size - i * big.slot)
        if (f.next[i] >= len) continue
        left = true
        if (f.busy[i] || big.pieces.size >= 2) continue
        const off = i * big.slot + f.next[i]
        send(x, big, n, i, off, pieceSize(big, len - f.next[i]))
      }
    }
    if (!left && big.pieces.size === 0) {
      finishUpload(x, big)
      answerPhase(x, big)
    }
  }

  function send(x: XMLHttpRequest, big: Big, n: number, i: number, off: number, len: number) {
    const f = big.files[n]
    f.busy[i] = true
    const now = deps.now()
    const p: Piece = { xhr: null as unknown as XMLHttpRequest, n, off, len, sent: 0, start: now, moved: now, probing: false }
    big.pieces.add(p)
    void request(big, 'PUT', `${BASE}/${big.id}/files/${n}?offset=${off}`, f.blob.slice(off, off + len), [], px => {
      p.xhr = px
      px.upload.onprogress = e => {
        if (e.loaded > p.sent) { p.sent = Math.min(e.loaded, len); p.moved = deps.now() }
      }
      px.upload.onload = () => { p.sent = len; p.moved = deps.now() }
    }, 0).then(async r => { // pieces are watched by tick() instead of a limit
      if (!big.pieces.has(p)) return // superseded after a probe, or ended
      big.pieces.delete(p)
      if (!active(big)) return
      if (r.status === 200 && typeof r.json?.fill === 'number') {
        big.backoff = 1000
        const took = deps.now() - p.start
        if (took > 0 && r.json.fill > off) {
          const rate = (r.json.fill - off) / took
          big.rate = big.rate > 0 ? big.rate * 0.5 + rate * 0.5 : rate
        }
        stored(x, big, n, off, r.json.fill)
        return schedule(x, big)
      }
      if (r.status === 409 && typeof r.json?.fill === 'number') {
        stored(x, big, n, off, r.json.fill)
        return schedule(x, big)
      }
      f.busy[i] = false
      if (r.status === 409 && r.json?.wait) return later(big, 1000, () => schedule(x, big))
      if (r.status === 410) return answerPhase(x, big)
      if (isFinal(r.status)) return end(x, big, 'error')
      if (r.status === 0 && !(await readable(f.blob, off))) return end(x, big, 'error')
      retry(big, () => schedule(x, big)) // network error, 5xx, 408, 429, 409 superseded
    })
  }

  // ---- DSM's answer ----

  function answerPhase(x: XMLHttpRequest, big: Big) {
    if (!active(big) || big.phase === 'answer') return
    big.phase = 'answer'
    for (const p of big.pieces) o.abort.call(p.xhr)
    big.pieces.clear()
    if (big.tick !== undefined) deps.clearInterval(big.tick)
    big.tick = undefined
    void poll(x, big)
  }

  async function poll(x: XMLHttpRequest, big: Big) {
    while (big.phase === 'answer') {
      const r = await request(big, 'GET', `${BASE}/${big.id}/result`, null, [], undefined, RESULT_MS)
      if (big.phase !== 'answer') return
      const j = r.json
      if (r.status === 200 && j?.state === 'done') {
        big.backoff = 1000
        return deliver(x, big, { status: j.status, statusText: j.statusText ?? '', headers: j.headers ?? [], body: unb64(j.body ?? '') })
      }
      if (r.status === 200 && j?.state === 'pending') { big.backoff = 1000; continue }
      if ((r.status === 200 && j?.state) || isFinal(r.status)) return end(x, big, 'error')
      await new Promise<void>(res => retry(big, res))
    }
  }

  // ---- start ----

  async function reuse(big: Big, have: number[][]) {
    if (!deps.digest) return
    for (let n = 0; n < big.files.length; n++) {
      for (const i of have[n] ?? []) {
        const f = big.files[n]
        const start = i * big.slot
        const len = Math.min(big.slot, f.size - start)
        if (!active(big) || len <= 0) return
        const sum = hex(await deps.digest(await f.blob.slice(start, start + len).arrayBuffer()))
        const r = await request(big, 'PUT', `${BASE}/${big.id}/files/${n}?offset=${start}`, null, [['X-Upload-Reuse', sum]])
        if (r.status === 200 && f.next[i] === 0) {
          f.next[i] = len
          big.confirmed += len
        }
      }
    }
  }

  async function start(x: XMLHttpRequest, big: Big) {
    const body = JSON.stringify({
      method: big.method, url: big.url.pathname + big.url.search, headers: big.headers, mode: big.mode, nonce: big.nonce,
      parts: big.parts.map(p => 'data' in p ? { data: b64(p.data) } : { file: { key: p.key, size: p.file.size } }),
    })
    for (;;) {
      const r = await request(big, 'POST', BASE, body, [['Content-Type', 'application/json']])
      if (!active(big)) {
        if (r.status === 201 && r.json?.id) { big.id = r.json.id; cancelRemote(big) }
        return
      }
      if (r.status === 201 && r.json?.id) {
        big.id = r.json.id
        big.slot = r.json.slot
        big.backoff = 1000
        big.confirmed = big.dataBytes
        for (const f of big.files) {
          const count = Math.ceil(f.size / big.slot)
          f.next = new Array(count).fill(0)
          f.busy = new Array(count).fill(false)
        }
        await reuse(big, (r.json.files ?? []).map((f: { have?: number[] }) => f.have ?? []))
        return schedule(x, big)
      }
      if (isFinal(r.status)) return end(x, big, 'error')
      await new Promise<void>(res => retry(big, res)) // network error, 5xx, 429 busy
    }
  }

  function bigSend(x: XMLHttpRequest, op: Opened, url: URL, token: string, d: { parts: Part[], type: string }, total: number) {
    let headers = op.headers
    // A multipart body gets the wrapper's boundary; a Blob's type applies only
    // when the App set no Content-Type, as natively.
    if (d.type.startsWith('multipart/form-data') || (d.type && !headers.some(([k]) => k.toLowerCase() === 'content-type'))) {
      headers = headers.filter(([k]) => k.toLowerCase() !== 'content-type').concat([['Content-Type', d.type]])
    }
    const files: FilePart[] = []
    let dataBytes = 0
    for (const p of d.parts) {
      if ('data' in p) dataBytes += p.data.length
      else files.push({ blob: p.file, size: p.file.size, next: [], busy: [] })
    }
    const big: Big = {
      phase: 'upload', method: op.method, url, headers, token, parts: d.parts, files, total, dataBytes,
      mode: mobile ? 'store' : 'stream', id: '', slot: 0, confirmed: 0, shown: 0, pieces: new Set(), rate: 0, answer: null,
      readyState: 1, responseType: x.responseType, timers: new Set(), tick: undefined, appTimer: undefined, backoff: 1000,
      uploadDone: false, cancelled: false, nonce: nonce(win),
    }
    bigs.set(x, big)
    live.add(big)
    fire(x, 'loadstart', 0, 0)
    fire(x.upload, 'loadstart', 0, total)
    fire(x.upload, 'progress', 0, total)
    big.tick = deps.setInterval(() => tick(x, big), TICK_MS)
    later(big, 100, () => tick(x, big)) // the first bytes show before the first interval
    if (x.timeout > 0) big.appTimer = deps.setTimeout(() => end(x, big, 'timeout'), x.timeout)
    void start(x, big)
  }

  // ---- XMLHttpRequest members ----

  P.open = function (this: XMLHttpRequest, ...args: unknown[]) {
    const [method, url, async] = args
    const big = bigs.get(this)
    if (big) {
      // Opening again ends the previous request without events, as natively.
      if (active(big)) { big.phase = 'ended'; stopTimers(big); cancelRemote(big) }
      bigs.delete(this)
    }
    opened.set(this, { method: String(method).toUpperCase(), url: String(url), async: args.length < 3 || async !== false, headers: [] })
    return (o.open as (...a: unknown[]) => void).apply(this, args)
  } as typeof P.open

  P.setRequestHeader = function (this: XMLHttpRequest, k: string, v: string) {
    o.setRequestHeader.call(this, k, v)
    opened.get(this)?.headers.push([String(k), String(v)])
  }

  P.send = function (this: XMLHttpRequest, body?: Document | XMLHttpRequestBodyInit | null) {
    const op = opened.get(this)
    const native = () => o.send.call(this, body ?? null)
    if (!op || !op.async || bigs.has(this) || !['POST', 'PUT', 'PATCH'].includes(op.method)) return native()
    let url: URL
    try { url = new URL(op.url, win.location.href) } catch { return native() }
    if (url.origin !== win.location.origin || url.pathname.startsWith('/_connect/')) return native()
    const quick = body instanceof (win as unknown as { Blob: typeof Blob }).Blob ? body.size
      : body instanceof ArrayBuffer || ArrayBuffer.isView(body) ? body.byteLength : null
    if (quick !== null && quick <= deps.threshold) return native()
    const d = describe(body, win, '----ConnectUpload' + Math.random().toString(36).slice(2) + Date.now().toString(36))
    if (!d) return native()
    const total = d.parts.reduce((s, p) => s + partSize(p), 0)
    const data = d.parts.reduce((s, p) => s + ('data' in p ? p.data.length : 0), 0)
    if (total <= deps.threshold || data > MAX_DATA) return native()
    const header = op.headers.find(([k]) => k.toLowerCase() === 'x-syno-token')?.[1]
    const syno = (win as unknown as { SYNO?: { SDS?: { Session?: { SynoToken?: string } } } }).SYNO
    const token = header || url.searchParams.get('SynoToken') || syno?.SDS?.Session?.SynoToken || ''
    if (!token) return native()
    bigSend(this, op, url, token, d, total)
  }

  P.abort = function (this: XMLHttpRequest) {
    const big = bigs.get(this)
    if (!big) return o.abort.call(this)
    if (active(big)) return end(this, big, 'abort')
    big.readyState = 0
  }

  const answerOf = (x: XMLHttpRequest) => {
    const big = bigs.get(x)
    return big ? big : null
  }

  function header(a: Answer, name: string): string | null {
    const want = name.toLowerCase()
    const v = a.headers.filter(([k]) => k === want).map(([, v]) => v)
    return v.length ? v.join(', ') : null
  }

  function text(a: Answer): string {
    return new TextDecoder().decode(a.body)
  }

  function getter(name: string, get: (big: Big, x: XMLHttpRequest, native: () => unknown) => unknown) {
    const d = desc(name)
    if (!d?.get) return
    const nativeGet = d.get
    Object.defineProperty(X.prototype, name, {
      configurable: true, enumerable: d.enumerable,
      get(this: XMLHttpRequest) {
        const big = answerOf(this)
        return big ? get(big, this, () => nativeGet.call(this)) : nativeGet.call(this)
      },
    })
  }

  getter('readyState', big => big.readyState)
  getter('status', big => big.readyState >= 2 && big.answer ? big.answer.status : 0)
  getter('statusText', big => big.readyState >= 2 && big.answer ? big.answer.statusText : '')
  getter('responseURL', big => big.readyState >= 2 && big.answer ? big.url.href : '')
  getter('responseText', (big, x) => {
    const t = x.responseType
    if (t !== '' && t !== 'text') throw new W.DOMException('responseText is only available for responseType "" or "text"', 'InvalidStateError')
    return big.readyState >= 3 && big.answer ? text(big.answer) : ''
  })
  getter('responseXML', (big, x) => {
    const t = x.responseType
    if (t !== '' && t !== 'document') throw new W.DOMException('responseXML is only available for responseType "" or "document"', 'InvalidStateError')
    if (big.readyState !== 4 || !big.answer) return null
    return parseDocument(big.answer, t === 'document')
  })
  getter('response', (big, x) => {
    const a = big.answer
    const t = x.responseType
    if (t === '' || t === 'text') return big.readyState >= 3 && a ? text(a) : ''
    if (big.readyState !== 4 || !a) return null
    switch (t) {
      case 'json':
        try { return JSON.parse(text(a)) } catch { return null }
      case 'arraybuffer':
        return a.body.slice().buffer
      case 'blob':
        return new (win as unknown as { Blob: typeof Blob }).Blob([a.body.slice()], { type: header(a, 'content-type') ?? '' })
      case 'document':
        return parseDocument(a, true)
    }
    return null
  })

  function parseDocument(a: Answer, html: boolean): Document | null {
    const ct = (header(a, 'content-type') ?? '').split(';')[0].trim().toLowerCase()
    const xml = ct === 'text/xml' || ct === 'application/xml' || ct.endsWith('+xml')
    if (!W.DOMParser || !(xml || (html && ct === 'text/html'))) return null
    try { return new W.DOMParser().parseFromString(text(a), xml ? ct as DOMParserSupportedType : 'text/html') } catch { return null }
  }

  P.getResponseHeader = function (this: XMLHttpRequest, name: string) {
    const big = answerOf(this)
    if (!big) return o.getResponseHeader.call(this, name)
    return big.readyState >= 2 && big.answer ? header(big.answer, name) : null
  }

  P.getAllResponseHeaders = function (this: XMLHttpRequest) {
    const big = answerOf(this)
    if (!big) return o.getAllResponseHeaders.call(this)
    if (big.readyState < 2 || !big.answer) return ''
    const names = [...new Set(big.answer.headers.map(([k]) => k))].sort()
    return names.map(k => `${k}: ${header(big.answer!, k)}\r\n`).join('')
  }

  // ---- page life ----

  win.addEventListener('pagehide', e => {
    if ((e as PageTransitionEvent).persisted) return
    for (const big of [...live]) {
      if (!active(big)) continue
      big.phase = 'ended'
      stopTimers(big)
      cancelRemote(big, true)
    }
  })

  // A page the phone paused may come back with requests that will never
  // answer; check those that stopped moving at once instead of after the
  // stall period.
  if (mobile) {
    win.document?.addEventListener('visibilitychange', () => {
      if (win.document.visibilityState !== 'visible') return
      const now = deps.now()
      for (const big of live) {
        for (const p of big.pieces) if (now - p.moved > 5000) p.moved = now - STALL_MS
      }
    })
  }

  return true
}

declare const __CONNECT_UPLOAD_AUTO__: boolean
if (typeof __CONNECT_UPLOAD_AUTO__ !== 'undefined' && __CONNECT_UPLOAD_AUTO__) install(window)
