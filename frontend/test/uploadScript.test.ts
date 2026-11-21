// @vitest-environment node
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
// @ts-expect-error plain ES module without types
import { artifact, buildUpload } from '../scripts/build-upload.mjs'

describe('embedded upload script', () => {
  it('matches the source; run pnpm run build:upload after changing it', async () => {
    expect(readFileSync(artifact, 'utf8')).toBe(await buildUpload())
  })

  it('installs itself once in the page that loads it', () => {
    const code = readFileSync(artifact, 'utf8')
    class XHR { open() {} send() {} setRequestHeader() {} abort() {} getResponseHeader() { return null } getAllResponseHeaders() { return '' } get readyState() { return 0 } }
    const win = { XMLHttpRequest: XHR, navigator: { userAgent: 'Chrome' }, location: { href: 'https://nas.example.com/', origin: 'https://nas.example.com' }, addEventListener() {}, document: { addEventListener() {} } }
    const open = XHR.prototype.open
    new Function('window', code)(win)
    expect(XHR.prototype.open).not.toBe(open)
    const patched = XHR.prototype.open
    new Function('window', code)(win)
    expect(XHR.prototype.open).toBe(patched)
  })
})
