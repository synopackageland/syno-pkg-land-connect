// @vitest-environment jsdom
// Every address can be copied, opened and scanned as a QR code.
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

test('every address has copy, open and a QR code',async()=>{
 const f=fixture([connected('tailscale',{endpoints:[{url:'https://nas.tail1.ts.net',public:false,localPort:5000},{url:'https://nas.tail1.ts.net:8443',public:false,localPort:5001}]})]);await settle()
 const cards=f.page('tailscale').querySelectorAll<HTMLElement>('[data-url-card]')
 expect(cards).toHaveLength(2)
 const card=cards[1]!
 expect(card.querySelector<HTMLInputElement>('input')?.value).toBe('https://nas.tail1.ts.net:8443')
 expect([...card.querySelectorAll('button')].some(b=>b.getAttribute('aria-label')===translate('en','ux.copy'))).toBe(true)
 const open=vi.spyOn(window,'open').mockReturnValue(null)
 card.querySelector<HTMLElement>('[data-url-open]')!.click()
 expect(open).toHaveBeenCalledWith('https://nas.tail1.ts.net:8443','_blank','noopener,noreferrer')
 expect(card.querySelector('[data-qr]')).toBeNull()
 card.querySelector<HTMLElement>('[data-url-qr]')!.click();await settle()
 const qr=card.querySelector<SVGElement>('svg[data-qr]')!
 expect(qr.getAttribute('role')).toBe('img')
 expect(qr.getAttribute('aria-label')).toContain('https://nas.tail1.ts.net:8443')
 expect(qr.querySelector('path')?.getAttribute('d')).toMatch(/^M\d/)
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the address card is translated in %s',locale=>{
 for(const key of ['connect.url.open','connect.url.qr','connect.url.qrLabel'])
  expect(translate(locale,key),`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
})

test('copying an address without the Clipboard API, as in a DSM window, falls back and says Copied',async()=>{
 const f=fixture([connected('tailscale',{endpoints:[{url:'https://nas.tail1.ts.net',public:false,localPort:5000}]})]);await settle()
 const clipboard=Object.getOwnPropertyDescriptor(navigator,'clipboard'),execCommand=Object.getOwnPropertyDescriptor(document,'execCommand')
 Object.defineProperty(navigator,'clipboard',{value:undefined,configurable:true})
 const exec=vi.fn(()=>true);Object.defineProperty(document,'execCommand',{value:exec,configurable:true})
 const card=f.page('tailscale').querySelector<HTMLElement>('[data-url-card]')!
 const copy=[...card.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.getAttribute('aria-label')===translate('en','ux.copy'))!
 copy.click();await settle()
 expect(exec).toHaveBeenCalledWith('copy')
 expect(card.querySelector('[data-copy-feedback]')?.textContent?.trim()).toBe('Copied')
 f.done()
 if(clipboard)Object.defineProperty(navigator,'clipboard',clipboard);else delete (navigator as {clipboard?:unknown}).clipboard
 if(execCommand)Object.defineProperty(document,'execCommand',execCommand);else delete (document as {execCommand?:unknown}).execCommand
})
