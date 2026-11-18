// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://192.0.2.10:5000/"}
// Reserved documentation addresses only; never copy device evidence identifiers.
import {afterEach,expect,test,vi} from 'vitest'
import {fixture,t,connected} from './providerFixture'
import {settle} from './support'

let env:ReturnType<typeof fixture>|undefined
afterEach(()=>{env?.done();env=undefined;vi.unstubAllGlobals()})
const failed={...connected('cloudflare',{mode:'domain',hostname:'nas.example.com'}),state:'failed' as const,code:'pin_proxy_required'}
const pin={mode:'S',spki:'b'.repeat(64),certSha256:'c'.repeat(64),issuer:'Fixture DSM',notAfter:'2030-01-01T00:00:00Z'}
const view={apps:[],public:{available:true,connected:true,zone:'example.com'},private:{available:false}}

test.each(['quick','domain'])('a %s tunnel explains a changed DSM port and offers inline review',async mode=>{
 const changed={...failed,mode,dsmPortChange:{from:5001,to:5443}}
 env=fixture([changed],{
  'connection.status':()=>({provider:'multiple',state:'failed',connections:[changed]}),
  'apps.list':()=>view,
  'apps.workersstatus':()=>({connected:false}),
  'apps.inspectdsm':()=>({scope:'public',dsm:{port:5443,pin:{...pin,previous:pin}},token:'port-review'}),
 })
 await settle();await env.nav('Cloudflare')
 const review=env.root.querySelector<HTMLElement>('[data-dsm-review]')!
 expect(review.textContent).toContain('5001')
 expect(review.textContent).toContain('5443')
 expect(review.textContent).not.toContain(t('apps.dsmReview.line'))
 review.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 expect(env.call).toHaveBeenCalledWith('apps.inspectdsm',{scope:'public'})
 expect(document.querySelector('[data-apps-approval]')?.textContent).toContain('5443')
 expect(document.querySelector('[data-dsm-cert-change]')).toBeNull()
})

function setup(refuseReconnect=false){
 let trusted=false
 const status=()=>({provider:'multiple',state:trusted?'connected':'failed',connections:[trusted?connected('cloudflare',{mode:'domain',hostname:'nas.example.com'}):failed]})
 env=fixture([failed],{
  'connection.status':status,
  'apps.list':()=>view,
  'apps.workersstatus':()=>({connected:false}),
  'apps.inspectdsm':()=>({scope:'public',dsm:{port:6443,pin},token:'fixture-review'}),
  'apps.setdsm':()=>{trusted=true;return {approved:true}},
  'connection.connect':()=>{if(refuseReconnect)throw {code:'pin_proxy_required'};trusted=true;return status()},
 })
 return env
}

test('HTTP LAN-IP entry: enabled own domain with no pin has an inline action and Trust restores it',async()=>{
 expect(location.protocol).toBe('http:');expect(location.hostname).toBe('192.0.2.10');expect(location.port).toBe('5000')
 vi.stubGlobal('isSecureContext',false)
 const random=crypto.getRandomValues.bind(crypto)
 vi.stubGlobal('crypto',{getRandomValues:random,get subtle(){throw new Error('secure-context crypto must not be required')}})
 const f=setup();await settle();await f.nav('Cloudflare');const cf=f.page('cloudflare')
 const checkbox=cf.querySelector('[data-cf-domain-tab] [data-cf-confirm]')!
 expect(checkbox.getAttribute('aria-checked')).not.toBe('true')
 const reconnect=cf.querySelector<HTMLButtonElement>('[data-cf-domain-tab] [data-cf-reconnect]')!
 expect(reconnect.disabled).toBe(true)
 expect(cf.querySelector('[data-connection-state="failed"]')?.textContent).toBe(t('apps.dsmReview.action'))
 const banner=cf.querySelector('[data-status-card] [data-dsm-review]')!
 expect(banner.querySelector('[data-dsm-review-open]')).not.toBeNull()
 // Every DSM banner on all mounted pages owns its action inline.
 for(const item of f.root.querySelectorAll('[data-dsm-review]'))expect(item.querySelector('[data-dsm-review-open]')).not.toBeNull()
 ;(banner.querySelector('[data-dsm-review-open]') as HTMLElement).click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.inspectdsm',{scope:'public'})
 const dialog=document.querySelector<HTMLElement>('[data-apps-approval]')!
 expect(dialog.textContent).toContain('6443')
 dialog.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.setdsm',{scope:'public',dsm:{port:6443,pin},token:'fixture-review'})
 expect(f.root.querySelector('[data-dsm-review]')).toBeNull()
 expect(cf.querySelector('[data-connection-state="connected"]')).not.toBeNull()
})

test('upgrade with no pin trusts on the next explicit admin reconnect, never on page load',async()=>{
 const f=setup();await settle();await f.nav('Cloudflare');const cf=f.page('cloudflare')
 expect(f.call.mock.calls.some(([method])=>method==='connection.connect'||method==='apps.setdsm')).toBe(false)
 cf.querySelector<HTMLElement>('[data-cf-domain-tab] [data-cf-confirm]')!.click();await settle()
 cf.querySelector<HTMLElement>('[data-cf-domain-tab] [data-cf-reconnect]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',confirmExposure:true})
 expect(cf.querySelector('[data-dsm-review]')).toBeNull()
 expect(cf.querySelector('[data-connection-state="connected"]')).not.toBeNull()
})

test('unavailable DSM HTTPS explains what to check inline and keeps the retry action',async()=>{
 const f=setup();f.call.mockImplementation(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='apps.inspectdsm')throw {code:'apps_dsm_https_unavailable'}
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'failed',connections:[failed]}
  if(method==='apps.list')return view
  if(method==='apps.workersstatus')return {connected:false}
  if(method==='ports.list')return {entries:[],revision:1}
  return params
 })
 await settle();await f.nav('Cloudflare')
 const banner=f.page('cloudflare').querySelector('[data-dsm-review]')!
 ;(banner.querySelector('[data-dsm-review-open]') as HTMLElement).click();await settle()
 expect(banner.querySelector('[data-dsm-review-error]')?.textContent).toBe(t('apps.code.apps_dsm_https_unavailable'))
 expect((banner.querySelector('[data-dsm-review-open]') as HTMLButtonElement).disabled).toBe(false)
 expect(document.querySelector('[data-apps-approval]')).toBeNull()
 expect(f.call.mock.calls.some(([method])=>method==='apps.setdsm')).toBe(false)
})


test('the screenshot global Needs attention banner also owns its review action after a refused reconnect',async()=>{
 const f=setup(true);await settle();await f.nav('Cloudflare');const cf=f.page('cloudflare')
 cf.querySelector<HTMLElement>('[data-cf-domain-tab] [data-cf-confirm]')!.click();await settle()
 cf.querySelector<HTMLElement>('[data-cf-domain-tab] [data-cf-reconnect]')!.click();await settle()
 const globalBanner=f.root.querySelector<HTMLElement>('[data-dsm-review]')!
 expect(globalBanner.closest('[data-page]')).toBeNull()
 expect(globalBanner.querySelector('[data-dsm-review-open]')).not.toBeNull()
 expect(cf.querySelector('[data-status-card] [data-dsm-review-open]')).not.toBeNull()
 globalBanner.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-apps-approval] [data-approve-confirm]')!.click();await settle()
 expect(f.root.querySelector('[data-dsm-review]')).toBeNull()
 expect(cf.querySelector('[data-connection-state="connected"]')).not.toBeNull()
})


test('a generic Apps error reuses the inline certificate dialog rather than relying on the global banner',async()=>{
 const f=setup();const original=f.call.getMockImplementation()!;let refused=true
 f.call.mockImplementation(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='apps.list'&&refused)throw {code:'pin_proxy_required'}
  if(method==='apps.setdsm')refused=false
  return original(method,params)
 })
 await settle();await f.nav('App URLs')
 const reviews=f.page('apps').querySelectorAll<HTMLElement>('[data-dsm-review]')
 expect(reviews.length).toBeGreaterThan(1)
 const inline=reviews[reviews.length-1]!
 expect(inline.querySelector('[data-dsm-review-open]')).not.toBeNull()
 inline.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-apps-approval] [data-approve-confirm]')!.click();await settle()
 expect(f.root.querySelector('[data-dsm-review]')).toBeNull()
})
