// @vitest-environment jsdom
// connection.check results with their time in the status bar.
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

test('a connected provider is checked and shows the result with its time',async()=>{
 vi.useFakeTimers()
 let checks=0
 const f=fixture([connected('tailscale')],{'connection.check':p=>++checks<=1?{provider:p.provider,state:'checking'}:{provider:p.provider,state:'ok',checkedAt:'2026-10-12T08:00:00Z',attempts:1}});await settle()
 const row=()=>f.page('tailscale').querySelector<HTMLElement>('[data-connection-check]')!
 expect(f.call).toHaveBeenCalledWith('connection.check',{provider:'tailscale'})
 expect(row().textContent).toContain(t('connect.check.checking'))
 await vi.advanceTimersByTimeAsync(2000);await settle()
 expect(row().textContent).toContain(t('connect.check.ok'))
 expect(row().querySelector('time')?.getAttribute('datetime')).toBe('2026-10-12T08:00:00Z')
 expect(row().querySelector('time')?.textContent).not.toBe('')
 const again=[...row().querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent?.trim()===t('connect.check.again'))!
 const before=checks
 again.click();await settle()
 expect(checks).toBe(before+1)
 expect(f.page('netbird').querySelector('[data-connection-check]')).toBeNull()
 f.done()
})

test('a failed check names its reason',async()=>{
 const f=fixture([connected('netbird',{netbirdIP:'100.64.0.9'})],{'connection.check':p=>({provider:p.provider,state:'failed',code:'check_timeout',checkedAt:'2026-10-12T08:00:00Z',attempts:3})});await settle()
 const row=f.page('netbird').querySelector<HTMLElement>('[data-connection-check]')!
 expect(row.textContent).toContain(t('connect.check.failed'))
 expect(row.textContent).toContain(t('connect.check.code.check_timeout'))
 f.done()
})

test.each(['tailscale','netbird','cloudflare'])('a working %s check that did not reach the NAS from outside says it only checked the NAS side',async provider=>{
 const f=fixture([connected(provider,{netbirdIP:'100.64.0.9'})],{'connection.check':p=>({provider:p.provider,state:'ok',externalVerified:false,checkedAt:'2026-10-12T08:00:00Z',attempts:1})});await settle()
 const row=f.page(provider).querySelector<HTMLElement>('[data-connection-check]')!
 expect(row.querySelector('[data-check-scope]')?.textContent).toBe(t('connect.check.nasOnly'))
 expect(row.textContent).not.toContain(t('connect.check.external'))
 f.done()
})

test('an outside check is not marked as NAS side only',async()=>{
 const f=fixture([connected('cloudflare',{shareUrl:'https://nas.example.com'})],{'connection.check':p=>({provider:p.provider,state:'ok',externalVerified:true,checkedAt:'2026-10-12T08:00:00Z',attempts:1})});await settle()
 const row=f.page('cloudflare').querySelector<HTMLElement>('[data-connection-check]')!
 expect(row.querySelector('[data-check-scope]')?.textContent).toBe(t('connect.check.external'))
 expect(row.textContent).not.toContain(t('connect.check.nasOnly'))
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the connection check is translated in %s',locale=>{
 for(const key of ['connect.check.title','connect.check.checking','connect.check.ok','connect.check.failed','connect.check.notChecked','connect.check.again','connect.check.checkedAt','connect.check.external','connect.check.nasOnly','connect.check.code.not_connected','connect.check.code.check_timeout','connect.check.code.check_failed'])
  expect(translate(locale,key),`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('a settling connection reads as setting up in %s, never failed, and is asked again every 5 s',async locale=>{
 vi.useFakeTimers()
 let ready=false
 const f=fixture([connected('cloudflare',{mode:'quick',shareUrl:'https://new.trycloudflare.com'})],{'connection.check':p=>({provider:p.provider,state:ready?'ok':'starting',code:ready?undefined:'cloudflare_dns_pending',externalVerified:ready,checkedAt:'2026-10-26T08:00:00Z'})},undefined,locale);await settle()
 const row=()=>f.page('cloudflare').querySelector<HTMLElement>('[data-connection-check]')!
 expect(row().querySelector('[data-setting-up]')?.textContent).toContain(translate(locale,'connect.settling'))
 expect(row().textContent).not.toContain(translate(locale,'connect.check.failed'))
 expect(f.page('cloudflare').querySelector('[data-connection-badge]')?.getAttribute('data-status-word')).toBe('connecting')
 const overview=f.page('status')
 expect(overview.querySelector('[data-setting-up]')?.textContent).toContain(translate(locale,'connect.settling'))
 expect(overview.querySelector('[data-lane-reason]')).toBeNull()
 const count=()=>f.call.mock.calls.filter(c=>c[0]==='connection.check').length
 const before=count()
 await vi.advanceTimersByTimeAsync(4999);await settle()
 expect(count()).toBe(before)
 ready=true
 await vi.advanceTimersByTimeAsync(1);await settle()
 expect(count()).toBe(before+1)
 expect(row().textContent).toContain(translate(locale,'connect.check.ok'))
 expect(row().querySelector('[data-setting-up]')).toBeNull()
 expect(overview.querySelector('[data-setting-up]')).toBeNull()
 expect(f.page('cloudflare').querySelector('[data-connection-badge]')?.getAttribute('data-status-word')).toBe('on')
 for(const key of ['connect.settling','overview.reach.settingUp','wizard.connect.continueAnyway'])expect(translate(locale,key)).not.toMatch(/[.。．]$/)
 f.done()
})

test.each(['tailscale','netbird'])('a settling %s connection reads as setting up, like Cloudflare',async provider=>{
 const f=fixture([connected(provider,{netbirdIP:'100.64.0.9'})],{'connection.check':p=>({provider:p.provider,state:'starting',code:'not_connected',checkedAt:'2026-10-26T08:00:00Z'})});await settle()
 expect(f.page(provider).querySelector('[data-connection-check] [data-setting-up]')).toBeTruthy()
 expect(f.page(provider).querySelector('[data-connection-badge]')?.getAttribute('data-status-word')).toBe('connecting')
 expect(f.page(provider).textContent).not.toContain(t('connect.check.failed'))
 f.done()
})

test('a check that failed after the settling window names its reason and offers Check again on the Overview',async()=>{
 const f=fixture([connected('tailscale')],{'connection.check':p=>({provider:p.provider,state:'failed',code:'not_connected',checkedAt:'2026-10-26T08:00:00Z',attempts:4})});await settle()
 const overview=f.page('status')
 expect(overview.querySelector('[data-lane-reason]')?.textContent).toBe(t('connect.check.code.not_connected'))
 expect(overview.querySelector('[data-setting-up]')).toBeNull()
 const count=()=>f.call.mock.calls.filter(c=>c[0]==='connection.check').length
 const before=count()
 overview.querySelector<HTMLButtonElement>('[data-lane-check]')!.click();await settle()
 expect(count()).toBe(before+1)
 f.done()
})

test('a settling own domain draws its path as pending, then active once its check passes',async()=>{
 let ready=false
 const f=fixture([connected('cloudflare',{mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})],{'connection.check':p=>({provider:p.provider,state:ready?'ok':'starting',externalVerified:ready,checkedAt:'2026-10-26T08:00:00Z'})});await settle()
 await f.nav('Cloudflare')
 const path=()=>f.page('cloudflare').querySelector<HTMLElement>('[data-cf-path="domain"]')!.dataset.pathState
 expect(path()).toBe('pending')
 // The row shows no time while it settles.
 expect(f.page('cloudflare').querySelector('[data-connection-check] time')).toBeNull()
 ready=true
 const again=[...f.page('cloudflare').querySelectorAll<HTMLButtonElement>('[data-connection-check] button')].find(b=>b.textContent?.trim()===t('connect.check.again'))!
 again.click();await settle()
 expect(path()).toBe('active')
 f.done()
})

test('an offline relay overrides a cached successful check on the Cloudflare page',async()=>{
 const f=fixture([],{'apps.workersstatus':()=>({connected:true,relayState:'reconnecting',subdomain:'example',dsm:{provider:'workers',url:'https://dsm.example.workers.dev'}}),'connection.check':p=>({provider:p.provider,state:'ok',externalVerified:true,checkedAt:'2026-10-27T08:00:00Z'})})
 await settle();await f.nav('Cloudflare')
 const row=f.page('cloudflare').querySelector<HTMLElement>('[data-connection-check]')!
 expect(row.textContent).toContain(t('overview.relay.reconnecting'))
 expect(row.querySelector('[data-check-scope]')).toBeNull()
 expect(row.querySelector('[data-setting-up]')).toBeNull()
 f.done()
})
