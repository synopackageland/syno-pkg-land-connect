// @vitest-environment jsdom
// A change the running connection cannot keep (apply_unavailable) is that
// action's result: one row beside the provider's status with Apply anyway
// and Dismiss, never its status word or a lasting banner.
import {afterEach,expect,test,vi} from 'vitest'
import type {ConnectionStatus} from '../src/context'
import {browsingThrough,replacesSafely} from '../src/connect/rejected'
import {statusWord} from '../src/connect/providerStatus'
import {byLabel,settle} from './support'
import {fixture,t,connected,type Handler} from './providerFixture'
afterEach(()=>{vi.restoreAllMocks()})

const refused=()=>{throw {code:'apply_unavailable',message:'this change cannot preserve the existing connection'}}
const working:Handler=p=>({provider:p.provider,state:'ok',externalVerified:true,checkedAt:'2026-10-26T08:00:00Z'})
const ownDomain=(host='nas.example.com')=>connected('cloudflare',{mode:'domain',hostname:host,endpoints:[{url:`https://${host}`,public:true,localPort:5000}]})
const connects=(f:{call:{mock:{calls:unknown[][]}}})=>f.call.mock.calls.filter(c=>c[0]==='connection.connect').map(c=>c[1] as Record<string,unknown>)
const dialog=()=>document.body.querySelector<HTMLElement>('[data-rejected-dialog]')

// Change, then send the name typed in its panel; the panel closes once sent.
async function submit(cf:HTMLElement){
 cf.querySelector<HTMLButtonElement>('[data-way-change-toggle] [data-use-more]')!.click();await settle()
 cf.querySelector<HTMLButtonElement>('[data-cf-change-panel] [data-cf-domain]')!.click();await settle()
}
async function changeName(connections:ConnectionStatus[],connect:Handler){
 vi.spyOn(window,'open').mockReturnValue({opener:{},location:{replace:vi.fn()},close:vi.fn(),closed:false} as unknown as Window)
 const f=fixture(connections,{'connection.check':working,'connection.connect':connect});await settle();await f.nav('Cloudflare')
 const cf=f.page('cloudflare')
 cf.querySelector<HTMLButtonElement>('[data-way-change-toggle] [data-use-more]')!.click();await settle()
 const panel=cf.querySelector<HTMLElement>('[data-cf-change-panel]')!
 const name=byLabel(panel,t('cloudflare.subdomain')) as HTMLInputElement
 name.value='photos';name.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 panel.querySelector<HTMLButtonElement>('[data-cf-domain]')!.click();await settle()
 return {...f,cf}
}

test('a refused change on a working own domain stays a row beside it: the badge stays Working and no banner stays',async()=>{
 const f=await changeName([ownDomain()],refused)
 const badge=f.cf.querySelector<HTMLElement>('[data-connection-badge]')!
 expect(badge.dataset.statusWord).toBe('on')
 expect(f.cf.querySelector('[data-apply-status]')).toBeNull()
 expect(f.root.textContent).not.toContain('cannot preserve')
 const row=f.cf.querySelector<HTMLElement>('[data-rejected-change]')!
 expect(row.textContent).toContain(t('apply.rejected.title'))
 expect(row.querySelector('[data-rejected-apply]')?.textContent).toBe(t('apply.rejected.apply'))
 expect(row.textContent).not.toContain('apply_unavailable')
 f.done()
})

test('Apply anyway confirms in one line, then sends the same change with replace',async()=>{
 let calls=0
 const f=await changeName([ownDomain()],p=>{if(++calls===1)refused();return {...ownDomain('photos.example.com'),...p,state:'connecting'}})
 f.cf.querySelector<HTMLButtonElement>('[data-rejected-apply]')!.click();await settle()
 expect(dialog()!.textContent).toContain(t('apply.rejected.drops'))
 expect(dialog()!.querySelector('[data-rejected-yours]')).toBeNull()
 expect(connects(f)).toHaveLength(1)
 dialog()!.querySelector<HTMLButtonElement>('[data-rejected-confirm]')!.click();await settle()
 expect(connects(f)).toEqual([
  {provider:'cloudflare',mode:'domain',subdomain:'photos',confirmExposure:true},
  {provider:'cloudflare',mode:'domain',subdomain:'photos',confirmExposure:true,replace:true},
 ])
 expect(f.cf.querySelector('[data-rejected-change]')).toBeNull()
 f.done()
})

test('the confirm says so plainly when this page comes through the address it switches',async()=>{
 const f=await changeName([ownDomain(window.location.host)],refused)
 f.cf.querySelector<HTMLButtonElement>('[data-rejected-apply]')!.click();await settle()
 expect(dialog()!.querySelector('[data-rejected-yours]')?.textContent).toBe(t('apply.rejected.yours'))
 // It still allows it.
 expect(dialog()!.querySelector<HTMLButtonElement>('[data-rejected-confirm]')!.disabled).toBe(false)
 f.done()
})

test('Dismiss, leaving the page and a later passing check each clear the row',async()=>{
 const f=await changeName([ownDomain()],refused)
 f.cf.querySelector<HTMLButtonElement>('[data-rejected-dismiss]')!.click();await settle()
 expect(f.cf.querySelector('[data-rejected-change]')).toBeNull()
 await submit(f.cf)
 expect(f.cf.querySelector('[data-rejected-change]')).toBeTruthy()
 await f.nav('Tailscale');await f.nav('Cloudflare')
 expect(f.cf.querySelector('[data-rejected-change]')).toBeNull()
 await submit(f.cf)
 expect(f.cf.querySelector('[data-rejected-change]')).toBeTruthy()
 const again=[...f.cf.querySelectorAll<HTMLButtonElement>('[data-connection-check] button')].find(b=>b.textContent?.trim()===t('connect.check.again'))!
 again.click();await settle()
 expect(f.cf.querySelector('[data-rejected-change]')).toBeNull()
 f.done()
})

test('a temporary address is replaced by an own domain without asking, unless this page comes through it',async()=>{
 const temporary=connected('cloudflare',{mode:'quick',endpoints:[{url:'https://fixture-quick.trycloudflare.com',public:true,localPort:5000}]})
 expect(replacesSafely({provider:'cloudflare'},temporary,'nas.local:5001')).toBe(true)
 expect(replacesSafely({provider:'cloudflare'},temporary,'fixture-quick.trycloudflare.com')).toBe(false)
 expect(replacesSafely({provider:'cloudflare'},ownDomain(),'nas.local:5001')).toBe(false)
 expect(replacesSafely({provider:'tailscale'},temporary,'nas.local:5001')).toBe(false)
 expect(browsingThrough(ownDomain(),'nas.example.com')).toBe(true)
 vi.spyOn(window,'open').mockReturnValue({opener:{},location:{replace:vi.fn()},close:vi.fn(),closed:false} as unknown as Window)
 let calls=0
 const f=fixture([temporary],{'connection.check':working,'connection.connect':p=>{if(++calls===1)refused();return {...ownDomain(),...p,state:'connecting'}}});await settle();await f.nav('Cloudflare')
 const cf=f.page('cloudflare')
 const form=cf.querySelector<HTMLElement>('[data-cf-temporary]')!
 const name=byLabel(form,t('cloudflare.subdomain')) as HTMLInputElement
 name.value='photos';name.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 form.querySelector<HTMLButtonElement>('[data-cf-domain]')!.click();await settle()
 expect(connects(f).map(p=>p.replace)).toEqual([undefined,true])
 expect(cf.querySelector('[data-rejected-change]')).toBeNull()
 f.done()
})

test('an older daemon\'s refusal kept as the status reads as Working, with no banner',()=>{
 const status={...ownDomain(),apply:{phase:'precheck-failed',code:'apply_unavailable'}} as ConnectionStatus
 expect(statusWord(status,false,{provider:'cloudflare',state:'ok'})).toBe('on')
 const rolled={...ownDomain(),apply:{phase:'rolled-back',code:'apply_unavailable'}} as ConnectionStatus
 expect(statusWord(rolled,false,{provider:'cloudflare',state:'ok'})).toBe('on')
 // A change that really failed still needs the user.
 const failed={...ownDomain(),apply:{phase:'rolled-back',code:'apply_failed'}} as ConnectionStatus
 expect(statusWord(failed,false,{provider:'cloudflare',state:'ok'})).toBe('attention')
})
