// @vitest-environment jsdom
// The sign-in tab opens within the click; a blocked tab falls back to a button (D10).
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

async function startTailscale(f:ReturnType<typeof fixture>){
 await f.nav('Tailscale')
 const page=f.page('tailscale')


 page.querySelector<HTMLButtonElement>('[data-ts-connect]')!.click()
}

test('sign-in opens a blank tab within the click and sends it to the sign-in page',async()=>{
 const tab={opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn(),closed:false}
 const open=vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>({provider:'tailscale',state:'needs-login',loginUrl:'https://login.tailscale.com/a/fixture',simulation:false,shareUrl:''})});await settle()
 await startTailscale(f)
 expect(open).toHaveBeenCalledWith('','_blank')
 await settle()
 expect(tab.opener).toBeNull()
 expect(tab.location.replace).toHaveBeenCalledWith('https://login.tailscale.com/a/fixture')
 expect(f.page('tailscale').querySelector('[data-login-fallback]')).toBeNull()
 f.done()
})

test('a blocked sign-in tab falls back to a button, the address and a QR code',async()=>{
 const open=vi.spyOn(window,'open').mockReturnValue(null)
 const f=fixture([],{'connection.connect':()=>({provider:'tailscale',state:'needs-login',loginUrl:'https://login.tailscale.com/a/fixture',simulation:false,shareUrl:''})});await settle()
 await startTailscale(f);await settle()
 const fallback=f.page('tailscale').querySelector<HTMLElement>('[data-login-fallback]')!
 expect(fallback.textContent).toContain(t('connect.login.blocked'))
 expect(fallback.querySelector('[data-url-card]')).toBeNull()
 expect(f.page('tailscale').querySelector<HTMLInputElement>('[data-login-url] input')?.value).toBe('https://login.tailscale.com/a/fixture')
 open.mockClear()
 fallback.querySelector<HTMLElement>('[data-login-action]')!.click()
 expect(open).toHaveBeenCalledWith('https://login.tailscale.com/a/fixture','_blank','noopener,noreferrer')
 f.done()
})

test('a sign-in tab is closed when no sign-in is needed, and never sent to an unknown site',async()=>{
 const tab={opener:{},location:{replace:vi.fn()},close:vi.fn(),closed:false}
 vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>({provider:'tailscale',state:'needs-login',loginUrl:'https://evil.example/a/x',simulation:false,shareUrl:''})});await settle()
 await startTailscale(f);await settle()
 expect(tab.location.replace).not.toHaveBeenCalled()
 f.setStatus([connected('tailscale')])
 f.call.mockClear()
 ;(f.root.querySelector('[data-refresh]') as HTMLElement).click();await settle()
 expect(tab.close).toHaveBeenCalled()
 f.done()
})

test('the blank sign-in tab says what it is waiting for, how long and what happens next',async()=>{
 const tab={opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn(),closed:false,document:document.implementation.createHTMLDocument('')}
 vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>new Promise(()=>{})});await settle()
 await startTailscale(f)
 const page=tab.document,named=(key:string)=>t(key).replace('{provider}','Tailscale')
 expect(page.documentElement.lang).toBe('en')
 expect(page.title).toBe(named('connect.login.preparing'))
 expect(page.querySelector('h1')?.textContent).toBe(named('connect.login.preparing'))
 expect(page.querySelector('[data-login-wait]')?.textContent).toBe(named('connect.login.preparingWait'))
 expect(page.querySelector('[data-login-next]')?.textContent).toBe(named('connect.login.preparingNext'))
 expect(page.querySelector('[role="status"][aria-busy="true"]')).not.toBeNull()
 expect(page.querySelector('[data-login-spinner]')).not.toBeNull()
 f.done()
})

test('the sign-in tab speaks the app language',async()=>{
 expect(translate('zh-TW','connect.login.preparingWait')).toContain('{provider}')
 expect(translate('zh-TW','connect.login.preparingWait')).not.toBe(translate('en','connect.login.preparingWait'))
})

test('a sign-in tab closed before the address arrives says so and offers the address',async()=>{
 const tab={opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn(),closed:false}
 const open=vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>({provider:'tailscale',state:'needs-login',loginUrl:'https://login.tailscale.com/a/fixture',simulation:false,shareUrl:''})});await settle()
 await startTailscale(f)
 tab.closed=true
 await settle()
 expect(tab.location.replace).not.toHaveBeenCalled()
 const fallback=f.page('tailscale').querySelector<HTMLElement>('[data-login-fallback]')!
 expect(fallback.textContent).toContain(t('connect.login.closed'))
 expect(fallback.textContent).not.toContain(t('connect.login.blocked'))
 expect(fallback.querySelector('[data-url-card]')).toBeNull()
 expect(f.page('tailscale').querySelector<HTMLInputElement>('[data-login-url] input')?.value).toBe('https://login.tailscale.com/a/fixture')
 open.mockClear()
 fallback.querySelector<HTMLElement>('[data-login-action]')!.click()
 expect(open).toHaveBeenCalledWith('https://login.tailscale.com/a/fixture','_blank','noopener,noreferrer')
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the sign-in fallback is translated in %s',locale=>{
 for(const key of ['connect.login.blocked','connect.login.closed','connect.login.preparing','connect.login.open','connect.login.failed','connect.login.failedNext','dependency.changeBlocked'])
  expect(translate(locale,key),`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
})

test.each(['netbird_cloudflare_in_use','cloudflare_login_failed'])('a refused sign-in keeps its tab open with the translated reason: %s',async code=>{
 const tab={opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn(),closed:false,document:document.implementation.createHTMLDocument('')}
 vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>{throw {code}}});await settle()
 await startTailscale(f);await settle()
 expect(tab.close).not.toHaveBeenCalled()
 expect(tab.document.querySelector('[data-login-error]')?.textContent).toBe(code==='netbird_cloudflare_in_use'?t('dependency.changeBlocked'):t('cloudflare.code.cloudflare_login_failed'))
 expect(tab.document.querySelector('[data-login-spinner]')).toBeNull()
 expect(tab.document.querySelector('[role="alert"][aria-busy="false"]')).not.toBeNull()
 expect(tab.document.querySelector('[data-login-next]')?.textContent).toBe(t('connect.login.failedNext'))
 expect(tab.opener).toBeNull()
 f.done()
})

test('a sign-in that fails during status polling leaves its reason in the waiting tab',async()=>{
 const tab={opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn(),closed:false,document:document.implementation.createHTMLDocument('')}
 vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>({provider:'tailscale',state:'connecting',simulation:false,shareUrl:''})});await settle()
 await startTailscale(f);await settle()
 f.setStatus([{provider:'tailscale',state:'failed',code:'start_failed',simulation:false,shareUrl:''}])
 f.root.querySelector<HTMLElement>('[data-refresh]')!.click();await settle()
 expect(tab.close).not.toHaveBeenCalled()
 expect(tab.document.querySelector('[data-login-error]')?.textContent).toBe(t('provider.startFailed'))
 f.done()
})
