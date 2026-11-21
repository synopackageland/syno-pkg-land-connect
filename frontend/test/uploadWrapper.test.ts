import { createHash } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ANSWER_MS, CONTROL_MS, STALL_MS, install } from '../src/upload/wrapper'

// A fake XMLHttpRequest whose requests go to an in-memory NAS (FakeNAS). It
// keeps the native shape that matters: state getters on the prototype, on*
// handler properties, and an upload event target.

class Target extends EventTarget {
  [k: string]: any
  dispatchEvent(e: Event): boolean {
    const r = super.dispatchEvent(e)
    const h = this['on' + e.type]
    if (typeof h === 'function') h.call(this, e)
    return r
  }
}

class ProgressEv extends Event {
  lengthComputable: boolean
  loaded: number
  total: number
  constructor(type: string, init: { lengthComputable?: boolean, loaded?: number, total?: number } = {}) {
    super(type)
    this.lengthComputable = !!init.lengthComputable
    this.loaded = init.loaded ?? 0
    this.total = init.total ?? 0
  }
}

let nas: FakeNAS

class FakeXHR extends Target {
  upload = new Target()
  timeout = 0
  responseType = ''
  method = ''
  url = ''
  reqHeaders: [string, string][] = []
  body: any = undefined
  sentNative = false
  aborted = false
  _rs = 0
  _status = 0
  _text = ''
  _headers: Record<string, string> = {}
  open(method: string, url: string) { this.method = method; this.url = url; this.reqHeaders = []; this._rs = 1 }
  setRequestHeader(k: string, v: string) { this.reqHeaders.push([k, v]) }
  done: (() => void) | null = null
  send(body?: any) { this.body = body; this.sentNative = true; nas.handle(this) }
  settle() { const d = this.done; this.done = null; d?.() }
  abort() {
    if (this.aborted || this._rs === 4) return
    this.aborted = true
    this._rs = 4
    this.settle()
    this.dispatchEvent(new ProgressEv('abort'))
  }
  header(k: string) { return this.reqHeaders.find(([n]) => n.toLowerCase() === k.toLowerCase())?.[1] }
  respond(status: number, json?: unknown) {
    if (this.aborted) return
    this.settle()
    this._status = status
    this._text = json === undefined ? '' : JSON.stringify(json)
    this._rs = 4
    this.dispatchEvent(new ProgressEv('load'))
  }
  fail() {
    if (this.aborted) return
    this.settle()
    this._rs = 4
    this.dispatchEvent(new ProgressEv('error'))
  }
  get readyState() { return this._rs }
  get status() { return this._status }
  get statusText() { return 'native' }
  get responseText() { return this._text }
  get response() { return this._text }
  get responseXML() { return null }
  get responseURL() { return this.url }
  getResponseHeader(k: string) { return this._headers[k.toLowerCase()] ?? null }
  getAllResponseHeaders() { return '' }
}

const ORIGIN = 'https://nas.example.com'
const SLOT = 1024
const THRESHOLD = 1000
const UPLOAD = '/webapi/entry.cgi?api=SYNO.FileStation.Upload&method=upload&version=2'

type PutHook = (x: FakeXHR, n: number, off: number, bytes: Uint8Array) => 'normal' | 'hold' | 'fail' | 'sent' | 'fill' | number

class FakeNAS {
  sessions = new Map<string, { create: any, files: Uint8Array[], fills: number[][], received: boolean[][], ended: string | null }>()
  log: string[] = []
  native: FakeXHR[] = []
  creates: any[] = []
  createStatus: number[] = []
  have: number[][] = []
  putHook: PutHook | null = null
  inFlight = 0
  maxInFlight = 0
  slotsInFlight = new Set<string>()
  answer = { status: 200, statusText: 'OK', headers: [['content-type', 'application/json'], ['x-dsm', 'yes']] as [string, string][], body: '{"success":true}' }
  earlyEnd = false
  resultFailure: string | null = null
  createLost = 0 // creates handled whose answer never reaches the page
  resultLost = 0 // result requests whose answer never reaches the page

  handle(x: FakeXHR) {
    const u = new URL(x.url, ORIGIN)
    if (!u.pathname.startsWith('/_connect/')) { this.native.push(x); return }
    this.log.push(`${x.method} ${u.pathname}${u.search}`)
    if (x.method === 'PUT' && !x.header('X-Upload-Reuse')) {
      const key = `${u.pathname}:${Math.floor(Number(u.searchParams.get('offset')) / SLOT)}`
      expect(this.slotsInFlight.has(key)).toBe(false)
      this.slotsInFlight.add(key)
      this.inFlight++
      this.maxInFlight = Math.max(this.maxInFlight, this.inFlight)
      x.done = () => { this.inFlight--; this.slotsInFlight.delete(key) }
    }
    const parts = u.pathname.split('/').filter(Boolean) // _connect upload sessions id files n
    void Promise.resolve().then(async () => {
      if (x.method === 'POST' && parts.length === 3) return this.create(x)
      const s = this.sessions.get(parts[3])
      if (!s) return x.respond(404, { error: 'unknown' })
      if (x.method === 'DELETE') { s.ended = 'cancelled'; return x.respond(204) }
      if (x.method === 'GET' && parts[4] === 'result') {
        if (this.resultLost > 0) { this.resultLost--; return }
        return this.result(x, s)
      }
      const n = Number(parts[5]), off = Number(u.searchParams.get('offset'))
      const i = Math.floor(off / SLOT)
      if (x.method === 'GET') return x.respond(200, { fill: i * SLOT + s.fills[n][i] })
      if (s.ended) return x.respond(410, { state: s.ended })
      const reuse = x.header('X-Upload-Reuse')
      if (reuse) {
        const want = createHash('sha256').update(s.files[n].subarray(i * SLOT, i * SLOT + this.slotLen(s, n, i))).digest('hex')
        if (s.received[n][i] && want === reuse) return x.respond(200, { fill: i * SLOT + s.fills[n][i] })
        s.fills[n][i] = 0
        return x.respond(409, { fill: i * SLOT })
      }
      const bytes = new Uint8Array(await (x.body as Blob).arrayBuffer())
      if (i * SLOT + s.fills[n][i] !== off) return x.respond(409, { fill: i * SLOT + s.fills[n][i] })
      const done = () => {}
      const store = (count: number) => {
        s.files[n].set(bytes.subarray(0, count), off)
        s.fills[n][i] += count
        if (s.fills[n][i] === this.slotLen(s, n, i)) s.received[n][i] = true
      }
      const how = this.putHook ? this.putHook(x, n, off, bytes) : 'normal'
      if (how === 'hold') { x.addEventListener('abort', done); return }
      if (how === 'fail') { done(); return x.fail() }
      if (how === 'sent' || how === 'fill') {
        // Every byte left the browser; the NAS has them (or not), the answer never comes.
        x.upload.dispatchEvent(new ProgressEv('progress', { loaded: bytes.length, total: bytes.length }))
        if (how === 'fill') store(bytes.length)
        x.addEventListener('abort', done)
        return
      }
      if (typeof how === 'number') { done(); return x.respond(how, { error: 'x' }) }
      x.upload.dispatchEvent(new ProgressEv('progress', { loaded: bytes.length, total: bytes.length }))
      store(bytes.length)
      done()
      if (this.earlyEnd) { s.ended = 'done'; return x.respond(410, { state: 'done' }) }
      x.respond(200, { fill: off + bytes.length })
    })
  }

  slotLen(s: { files: Uint8Array[] }, n: number, i: number) {
    return Math.min(SLOT, s.files[n].length - i * SLOT)
  }

  create(x: FakeXHR) {
    const status = this.createStatus.shift()
    if (status) return x.respond(status, { error: 'x' })
    const c = JSON.parse(x.body)
    this.creates.push({ ...c, token: x.header('X-SYNO-TOKEN') })
    const same = [...this.sessions.entries()].find(([, v]) => c.nonce && v.create.nonce === c.nonce)
    if (same) return x.respond(201, { id: same[0], slot: SLOT, files: same[1].files.map(() => ({ have: [] })) })
    if (this.createLost > 0) { this.createLost--; this.answerLater(x, c); return }
    const id = `s${this.sessions.size + 1}`
    const files = c.parts.filter((p: any) => p.file).map((p: any) => new Uint8Array(p.file.size))
    const prev = [...this.sessions.values()].at(-1)
    const fills = files.map((f: Uint8Array, n: number) => Array.from({ length: Math.ceil(f.length / SLOT) }, () => 0).map((_, i) => (this.have[n] ?? []).includes(i) && prev ? Math.min(SLOT, f.length - i * SLOT) : 0))
    const received = fills.map((f: number[]) => f.map(v => v > 0))
    if (prev) files.forEach((f: Uint8Array, n: number) => { if (prev.files[n]) f.set(prev.files[n]) })
    this.sessions.set(id, { create: c, files, fills, received, ended: null })
    x.respond(201, { id, slot: SLOT, files: files.map((_: unknown, n: number) => ({ have: this.have[n] ?? [] })) })
  }

  // answerLater makes the session as create would, but the answer is lost.
  answerLater(_x: FakeXHR, c: any) {
    const files = c.parts.filter((p: any) => p.file).map((p: any) => new Uint8Array(p.file.size))
    const fills = files.map((f: Uint8Array) => Array.from({ length: Math.ceil(f.length / SLOT) }, () => 0))
    this.sessions.set(`s${this.sessions.size + 1}`, { create: c, files, fills, received: fills.map((f: number[]) => f.map(() => false)), ended: null })
  }

  result(x: FakeXHR, s: { files: Uint8Array[], received: boolean[][], ended: string | null }) {
    if (this.resultFailure) return x.respond(200, { state: 'failed', code: this.resultFailure })
    if (s.ended === 'done' || s.received.every(r => r.every(Boolean))) {
      s.ended = 'done'
      return x.respond(200, { state: 'done', status: this.answer.status, statusText: this.answer.statusText, headers: this.answer.headers, body: Buffer.from(this.answer.body).toString('base64') })
    }
    x.respond(200, { state: 'pending', received: 0 })
  }

  // body reassembles what DSM would receive.
  body(id = 's1'): Uint8Array {
    const s = this.sessions.get(id)!
    let fi = 0
    const chunks = s.create.parts.map((p: any) => p.data !== undefined ? Buffer.from(p.data, 'base64') : s.files[fi++])
    return new Uint8Array(Buffer.concat(chunks))
  }
}

function makeWindow(ua = 'Mozilla/5.0 Chrome/140') {
  const listeners: Record<string, ((e: Event) => void)[]> = {}
  const docListeners: Record<string, (() => void)[]> = {}
  const fetches: { url: string, init: any }[] = []
  const win: any = {
    XMLHttpRequest: FakeXHR, ProgressEvent: ProgressEv, Event, DOMException, FormData, Blob,
    location: { href: ORIGIN + '/webman/index.cgi', origin: ORIGIN },
    navigator: { userAgent: ua, maxTouchPoints: 0, onLine: true },
    addEventListener: (t: string, f: (e: Event) => void) => { (listeners[t] ??= []).push(f) },
    document: { visibilityState: 'visible', addEventListener: (t: string, f: () => void) => { (docListeners[t] ??= []).push(f) } },
    fetch: (url: string, init: any) => { fetches.push({ url, init }); return Promise.resolve() },
    emit: (t: string, e: any = new Event(t)) => listeners[t]?.forEach(f => f(e)),
    emitDoc: (t: string) => docListeners[t]?.forEach(f => f()),
    fetches,
  }
  return win
}

function bytes(n: number, seed = 7): Uint8Array {
  const b = new Uint8Array(n)
  for (let i = 0; i < n; i++) b[i] = (i * 31 + seed) & 0xff
  return b
}

let win: any

function setup(ua?: string) {
  nas = new FakeNAS()
  win = makeWindow(ua)
  // Fresh class per test so each install patches an unpatched prototype.
  win.XMLHttpRequest = class extends FakeXHR {}
  install(win, {
    now: () => Date.now(), setTimeout: (f, ms) => setTimeout(f, ms), clearTimeout: t => clearTimeout(t as any),
    setInterval: (f, ms) => setInterval(f, ms), clearInterval: t => clearInterval(t as any),
    digest: data => crypto.subtle.digest('SHA-256', data), threshold: THRESHOLD,
  })
}

function record(x: any) {
  const events: string[] = []
  for (const t of ['loadstart', 'progress', 'load', 'loadend', 'abort', 'error', 'timeout', 'readystatechange']) {
    x.addEventListener(t, () => events.push(t === 'readystatechange' ? `rs${x.readyState}` : t))
    x.upload.addEventListener(t, (e: any) => events.push(`up.${t}${t === 'progress' ? `:${e.loaded}` : ''}`))
  }
  return events
}

function bigUpload(file = new File([bytes(5000) as BlobPart], 'movie.mp4', { type: 'video/mp4' }), configure?: (x: any) => void) {
  const x: any = new win.XMLHttpRequest()
  const events = record(x)
  x.open('POST', UPLOAD)
  x.setRequestHeader('X-SYNO-TOKEN', 'tok')
  configure?.(x)
  const fd = new FormData()
  fd.append('path', '/home')
  fd.append('file', file)
  x.send(fd)
  return { x, events, file }
}

async function run(ms: number) {
  await vi.advanceTimersByTimeAsync(ms)
}

async function until(cond: () => boolean, limit = 120_000) {
  for (let t = 0; t < limit && !cond(); t += 50) {
    await vi.advanceTimersByTimeAsync(50)
    // Blob reads and digests resolve outside the fake clock.
    await new Promise(r => setImmediate(r))
  }
  expect(cond()).toBe(true)
}

beforeEach(() => { vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] }); setup() })
afterEach(() => { vi.useRealTimers() })

describe('upload wrapper', () => {
  it('uses stream mode on desktop browsers, Mac Safari included', async () => {
    for (const ua of ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/157.0', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Version/26.0 Safari/605.1.15']) {
      setup(ua)
      const { events } = bigUpload()
      await until(() => events.includes('loadend'))
      expect(nas.creates[0].mode).toBe('stream')
    }
  })

  it('passes small, synchronous, cross-origin, GET and tokenless requests through', () => {
    const cases: [string, string, boolean, any, string | null][] = [
      ['POST', UPLOAD, true, new Blob([bytes(THRESHOLD) as BlobPart]), 'tok'],
      ['POST', UPLOAD, false, new Blob([bytes(5000) as BlobPart]), 'tok'],
      ['POST', 'https://other.example.com/up', true, new Blob([bytes(5000) as BlobPart]), 'tok'],
      ['GET', UPLOAD, true, null, 'tok'],
      ['POST', UPLOAD, true, new Blob([bytes(5000) as BlobPart]), null],
      ['POST', UPLOAD, true, 'a string body', 'tok'],
    ]
    for (const [method, url, async, body, token] of cases) {
      const x: any = new win.XMLHttpRequest()
      x.open(method, url, async)
      if (token) x.setRequestHeader('X-SYNO-TOKEN', token)
      x.send(body)
      expect(x.sentNative).toBe(true)
      expect(x.body).toBe(body)
    }
    expect(nas.native).toHaveLength(cases.length)
    expect(nas.log).toEqual([])
  })

  it('uploads a large FormData and hands the App DSM answer with native events', async () => {
    const { x, events, file } = bigUpload(undefined, x => { x.responseType = 'json' })
    // Progress starts synchronously, before any network round trip.
    expect(events).toEqual(['loadstart', 'up.loadstart', 'up.progress:0'])
    expect(x.readyState).toBe(1)
    expect(x.sentNative).toBe(false)
    await until(() => events.includes('loadend'))
    const c = nas.creates[0]
    expect(c.method).toBe('POST')
    expect(c.url).toBe(UPLOAD)
    expect(c.mode).toBe('stream')
    expect(c.token).toBe('tok')
    const type = c.headers.find(([k]: string[]) => k === 'Content-Type')[1]
    const boundary = type.split('boundary=')[1]
    const body = Buffer.from(nas.body())
    const want = Buffer.concat([
      Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="path"\r\n\r\n/home\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="movie.mp4"\r\nContent-Type: video/mp4\r\n\r\n`),
      Buffer.from(await file.arrayBuffer()), Buffer.from(`\r\n--${boundary}--\r\n`)])
    expect(body.equals(want)).toBe(true)
    const tail = events.slice(events.indexOf('up.load') - 1)
    expect(tail).toEqual([`up.progress:${want.length}`, 'up.load', 'up.loadend', 'rs2', 'rs3', 'progress', 'rs4', 'load', 'loadend'])
    const ups = events.filter(e => e.startsWith('up.progress:')).map(e => Number(e.split(':')[1]))
    expect([...ups].sort((a, b) => a - b)).toEqual(ups)
    expect(x.status).toBe(200)
    expect(x.statusText).toBe('OK')
    expect(x.response).toEqual({ success: true })
    expect(() => x.responseText).toThrow()
    expect(x.getResponseHeader('X-DSM')).toBe('yes')
    expect(x.getAllResponseHeaders()).toBe('content-type: application/json\r\nx-dsm: yes\r\n')
    expect(x.responseURL).toBe(ORIGIN + UPLOAD)
  })

  it('gives text, arraybuffer and blob responses', async () => {
    for (const type of ['', 'text', 'arraybuffer', 'blob']) {
      setup()
      const { x, events } = bigUpload(undefined, x => { x.responseType = type })
      await until(() => events.includes('loadend'))
      const got = type === 'arraybuffer' ? Buffer.from(x.response).toString()
        : type === 'blob' ? await (x.response as Blob).text() : x.response
      expect(got).toBe('{"success":true}')
      if (type === '' || type === 'text') expect(x.responseText).toBe('{"success":true}')
    }
  })

  it('sends a raw Blob body with the App Content-Type and keeps two pieces in flight on different slots', async () => {
    const x: any = new win.XMLHttpRequest()
    const events = record(x)
    x.open('POST', UPLOAD)
    x.setRequestHeader('X-SYNO-TOKEN', 'tok')
    x.setRequestHeader('Content-Type', 'application/octet-stream')
    const data = bytes(8000, 3)
    x.send(new File([data as BlobPart], 'a.bin').slice(0))
    await until(() => events.includes('loadend'))
    expect(nas.creates[0].headers).toEqual([['X-SYNO-TOKEN', 'tok'], ['Content-Type', 'application/octet-stream']])
    expect(Buffer.from(nas.body()).equals(Buffer.from(data))).toBe(true)
    expect(nas.maxInFlight).toBe(2)
  })

  it('aborts: native event order, DELETE on the NAS, no more pieces', async () => {
    nas.putHook = () => 'hold'
    const { x, events } = bigUpload()
    await until(() => nas.log.some(l => l.startsWith('PUT')))
    events.length = 0
    x.abort()
    expect(events).toEqual(['rs4', 'up.abort', 'up.loadend', 'abort', 'loadend'])
    expect(x.readyState).toBe(0)
    expect(x.status).toBe(0)
    await run(100)
    expect(nas.log.at(-1)).toBe('DELETE /_connect/upload/sessions/s1')
    const puts = nas.log.filter(l => l.startsWith('PUT')).length
    await run(60_000)
    expect(nas.log.filter(l => l.startsWith('PUT')).length).toBe(puts)
  })

  it('aborts before the session exists, then cancels it when it arrives', async () => {
    const { x, events } = bigUpload()
    x.abort()
    expect(events.at(-1)).toBe('loadend')
    await run(100)
    expect(nas.log).toEqual(['POST /_connect/upload/sessions', 'DELETE /_connect/upload/sessions/s1'])
  })

  it('times out like a native request', async () => {
    nas.putHook = () => 'hold'
    const { events } = bigUpload(undefined, x => { x.timeout = 5000 })
    await run(5000)
    expect(events.slice(-5)).toEqual(['rs4', 'up.timeout', 'up.loadend', 'timeout', 'loadend'])
    await run(10)
    expect(nas.log).toContain('DELETE /_connect/upload/sessions/s1')
  })

  it('retries network errors, also offline, and at once when back online', async () => {
    let failed = 0
    nas.putHook = () => win.navigator.onLine ? 'normal' : (failed++, 'fail')
    win.navigator.onLine = false
    const { events } = bigUpload()
    await until(() => failed >= 6) // backoff 1 s, 2 s, 4 s, ...
    const puts = () => nas.log.filter(l => l.startsWith('PUT')).length
    const before = puts()
    await run(1000)
    expect(puts()).toBe(before)
    win.navigator.onLine = true
    win.emit('online')
    await until(() => puts() > before, 200)
    await until(() => events.includes('load'))
    expect(events).not.toContain('error')
  })

  it('does not retry 507 or 401, and waits on 429', async () => {
    for (const status of [507, 401]) {
      setup()
      nas.createStatus = [status]
      const { events } = bigUpload()
      await until(() => events.includes('loadend'))
      expect(events.slice(-5)).toEqual(['rs4', 'up.error', 'up.loadend', 'error', 'loadend'])
      await run(60_000)
      expect(nas.log.filter(l => l.startsWith('POST'))).toHaveLength(1)
    }
    setup()
    nas.createStatus = [429, 429]
    const { events } = bigUpload()
    await until(() => events.includes('load'))
    expect(nas.log.filter(l => l.startsWith('POST'))).toHaveLength(3)
  })

  it('fails the App request on a 507 piece without retrying', async () => {
    nas.putHook = () => 507
    const { events } = bigUpload()
    await until(() => events.includes('loadend'))
    expect(events).toContain('error')
    await run(60_000)
    expect(nas.log.filter(l => l.startsWith('PUT')).length).toBeLessThanOrEqual(2)
  })

  it('a stalled piece is checked after the stall period and continued where the NAS stopped', async () => {
    let held = 0
    nas.putHook = (_x, _n, off) => off === 0 && held++ === 0 ? 'hold' : 'normal'
    const { events } = bigUpload()
    await until(() => held === 1)
    await run(STALL_MS - 1000)
    expect(nas.log.some(l => l.startsWith('GET /_connect/upload/sessions/s1/files/0'))).toBe(false)
    await until(() => events.includes('load'), STALL_MS)
    expect(nas.log).toContain('GET /_connect/upload/sessions/s1/files/0?offset=0')
    expect(events).not.toContain('error')
  })

  it('a slow piece that keeps moving is not a stall', async () => {
    let first: FakeXHR | null = null
    nas.putHook = (x, _n, off) => {
      if (off !== 0 || first) return 'normal'
      first = x
      return 'hold'
    }
    bigUpload()
    await until(() => first !== null)
    for (let t = 0; t < 3 * STALL_MS; t += 10_000) {
      first!.upload.dispatchEvent(new ProgressEv('progress', { loaded: 1 + t / 10_000, total: 1024 }))
      await run(10_000)
    }
    expect(nas.log.some(l => l.includes('/files/0?offset=0') && l.startsWith('GET'))).toBe(false)
  })

  it('a piece whose answer is lost counts as done when the NAS has it, without sending it again', async () => {
    let lost = 0
    nas.putHook = (_x, _n, off) => off === 0 && lost++ === 0 ? 'fill' : 'normal'
    const { events } = bigUpload()
    await until(() => lost === 1)
    await run(ANSWER_MS - 1000)
    expect(nas.log.some(l => l.startsWith('GET /_connect/upload/sessions/s1/files/'))).toBe(false)
    await until(() => events.includes('load'), ANSWER_MS)
    expect(nas.log).toContain('GET /_connect/upload/sessions/s1/files/0?offset=0')
    expect(nas.log.filter(l => l === 'PUT /_connect/upload/sessions/s1/files/0?offset=0')).toHaveLength(1)
  })

  it('a piece whose answer is lost and that did not arrive is sent again', async () => {
    let lost = 0
    nas.putHook = (_x, _n, off) => off === 0 && lost++ === 0 ? 'sent' : 'normal'
    const { events } = bigUpload()
    await until(() => events.includes('load'), ANSWER_MS + 10_000)
    expect(nas.log.filter(l => l === 'PUT /_connect/upload/sessions/s1/files/0?offset=0')).toHaveLength(2)
  })

  it('a create whose answer is lost is sent again and gets the same session', async () => {
    nas.createLost = 1
    const { x, events, file } = bigUpload()
    await run(CONTROL_MS - 1000)
    expect(nas.creates).toHaveLength(1)
    await until(() => events.includes('loadend'), CONTROL_MS + 10_000)
    expect(nas.creates).toHaveLength(2)
    expect(nas.creates[0].nonce).toMatch(/^[0-9a-f]{32}$/)
    expect(nas.creates[1].nonce).toBe(nas.creates[0].nonce)
    expect(nas.sessions.size).toBe(1)
    expect(x.status).toBe(200)
    expect(Buffer.from(nas.body()).includes(Buffer.from(await file.arrayBuffer()))).toBe(true)
  })

  it('a result request whose answer is lost is sent again', async () => {
    nas.resultLost = 1
    const { x, events } = bigUpload()
    await until(() => events.includes('up.load'))
    await run(50_000)
    expect(events).not.toContain('load')
    await until(() => events.includes('loadend'), 20_000)
    expect(x.status).toBe(200)
    expect(nas.log.filter(l => l === 'GET /_connect/upload/sessions/s1/result')).toHaveLength(2)
  })

  it('fails the App request when the file can no longer be read, instead of retrying', async () => {
    // The browser fails a piece of an unreadable file like a network error.
    class GoneBlob extends Blob {
      slice(start?: number, end?: number, type?: string): Blob {
        const b = super.slice(start, end, type)
        if (end !== undefined && start !== undefined && end - start === 1) b.arrayBuffer = () => Promise.reject(new DOMException('gone', 'NotReadableError'))
        return b
      }
    }
    nas.putHook = () => 'fail'
    const x: any = new win.XMLHttpRequest()
    const events = record(x)
    x.open('POST', UPLOAD)
    x.setRequestHeader('X-SYNO-TOKEN', 'tok')
    x.send(new GoneBlob([bytes(5000) as BlobPart]))
    await until(() => events.includes('loadend'), 5000)
    expect(events.slice(-2)).toEqual(['error', 'loadend'])
    expect(nas.log.filter(l => l.startsWith('PUT'))).toHaveLength(2)
  })

  it('hands over DSM early answer when the NAS ends the session', async () => {
    nas.earlyEnd = true
    nas.answer = { status: 200, statusText: 'OK', headers: [], body: '{"success":false,"error":{"code":414}}' }
    const { x, events } = bigUpload()
    await until(() => events.includes('loadend'))
    expect(events).toContain('load')
    expect(x.responseText).toContain('414')
  })

  it('fails the App request when DSM request breaks', async () => {
    nas.resultFailure = 'stream_broken'
    const { events } = bigUpload()
    await until(() => events.includes('loadend'))
    expect(events.slice(-2)).toEqual(['error', 'loadend'])
    expect(nas.log).not.toContain('DELETE /_connect/upload/sessions/s1')
  })

  it('reuses slots the NAS kept from a failed attempt after checking their hash', async () => {
    const file = new File([bytes(5000, 9) as BlobPart], 'movie.mp4')
    nas.resultFailure = 'stream_broken'
    let r = bigUpload(file)
    await until(() => r.events.includes('loadend'))
    nas.resultFailure = null
    nas.have = [[0, 2]]
    nas.log = []
    r = bigUpload(file)
    await until(() => r.events.includes('loadend'))
    expect(r.events).toContain('load')
    const puts = nas.log.filter(l => l.startsWith('PUT'))
    expect(puts.filter(l => l.endsWith('offset=0') || l.endsWith('offset=2048'))).toHaveLength(2) // confirmations only
    expect(puts.some(l => l.endsWith('offset=1024'))).toBe(true)
    const file2 = Buffer.from(await file.arrayBuffer())
    expect(Buffer.from(nas.body('s2')).includes(file2)).toBe(true)
  })

  it('cancels uploads when the tab closes', async () => {
    nas.putHook = () => 'hold'
    bigUpload()
    await until(() => nas.log.some(l => l.startsWith('PUT')))
    win.emit('pagehide', Object.assign(new Event('pagehide'), { persisted: false }))
    expect(win.fetches).toEqual([{ url: '/_connect/upload/sessions/s1', init: expect.objectContaining({ method: 'DELETE', keepalive: true }) }])
  })

  it('opening the request again ends the upload silently', async () => {
    nas.putHook = () => 'hold'
    const { x, events } = bigUpload()
    await until(() => nas.log.some(l => l.startsWith('PUT')))
    const n = events.length
    x.open('GET', '/webapi/entry.cgi')
    expect(events.length).toBe(n)
    expect(x.readyState).toBe(1)
    await run(10)
    expect(nas.log).toContain('DELETE /_connect/upload/sessions/s1')
  })

  it('uses store mode on phones and checks paused pieces when the page returns', async () => {
    for (const ua of ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', 'Mozilla/5.0 (Linux; Android 15; 22126RN91Y) Chrome/157.0 Mobile Safari/537.36']) {
      setup(ua)
      if (ua.includes('Macintosh')) win.navigator.maxTouchPoints = 5
      // install read the user agent at setup; re-install for the iPad case.
      if (ua.includes('Macintosh')) {
        win.XMLHttpRequest = class extends FakeXHR {}
        install(win, { now: () => Date.now(), setTimeout: (f, ms) => setTimeout(f, ms), clearTimeout: t => clearTimeout(t as any), setInterval: (f, ms) => setInterval(f, ms), clearInterval: t => clearInterval(t as any), digest: null, threshold: THRESHOLD })
      }
      let paused = 0
      nas.putHook = (_x, _n, off) => off === 0 && paused++ === 0 ? 'hold' : 'normal'
      const { events } = bigUpload()
      await until(() => paused === 1)
      expect(nas.creates[0].mode).toBe('store')
      await run(6000)
      win.emitDoc('visibilitychange')
      await until(() => events.includes('load'), 5000)
      expect(nas.log).toContain('GET /_connect/upload/sessions/s1/files/0?offset=0')
    }
  })
})
