// @vitest-environment jsdom
// DSM's own protections, read in the background with the user's session
// where the user decides: the setup wizard's Apps step on a public way.
import {afterEach,expect,test,vi} from 'vitest'
import {readDsmSecurity} from '../src/connect/dsmSecurity'
import {CATALOG} from '../src/connect/advisor'
import {settle} from './support'
import {fixture,t} from './providerFixture'
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals()})

const files={token:()=>'fixture-token'} as never

function dsm(replies:Record<string,unknown>){
 const fetch=vi.fn(async(url:string,init:RequestInit)=>{
  expect(url).toBe('/webapi/entry.cgi')
  const form=new URLSearchParams(String(init.body))
  if(!/QuickConnect|DDNS/.test(form.get('api')!))expect(form.get('method')).toBe('get')
  expect(new Headers(init.headers).get('X-SYNO-TOKEN')).toBe('fixture-token')
  const reply=replies[form.get('api')!]
  return {ok:true,json:async()=>reply??{success:false,error:{code:105}}}
 })
 vi.stubGlobal('fetch',fetch)
 return fetch
}

test('DSM protections are read with the session and mapped to on, off or unknown',async()=>{
 dsm({'SYNO.Core.Security.AutoBlock':{success:true,data:{enable:false}},'SYNO.Core.OTP.EnforcePolicy':{success:true,data:{otp_enforce_option:'custom'}}})
 const ctx={files} as never
 expect(await readDsmSecurity(ctx)).toEqual({'auto-block':'off','dsm-2fa':'on'})
 dsm({})
 expect(await readDsmSecurity(ctx)).toEqual({'auto-block':'unknown','dsm-2fa':'unknown'})
 dsm({'SYNO.Core.Security.AutoBlock':{success:true,data:{enable:true}},'SYNO.Core.OTP.EnforcePolicy':{success:true,data:{otp_enforce_option:'none'}}})
 expect(await readDsmSecurity(ctx)).toEqual({'auto-block':'on','dsm-2fa':'off'})
 expect(await readDsmSecurity({files:undefined} as never)).toEqual({'auto-block':'unknown','dsm-2fa':'unknown'})
})

test('the protect checks link only a protection that is off, and say one DSM does not let the user read cannot be checked',async()=>{
 const fetch=dsm({'SYNO.Core.Security.AutoBlock':{success:true,data:{enable:false}}})
 const relay={connected:true,subdomain:'acme'}
 const f=fixture([],{'apps.workersstatus':()=>relay,'apps.list':()=>({private:{available:false},public:{available:false},relay,apps:[{package:'SynoPkgX',name:'X',port:28001,state:'running',protection:{},publicCapable:true}]}),'apps.inspectdsm':()=>({scope:'public'})},files)
 await settle()
 // The page never waits for DSM; no protection is read before a step asks
 // (the Home reads only DSM's QuickConnect and DDNS).
 const apis=()=>fetch.mock.calls.map(([,init])=>new URLSearchParams(String(init.body)).get('api'))
 expect(apis().filter(api=>!/^SYNO\.Core\.(QuickConnect|DDNS\.Record)$/.test(api!))).toEqual([])
 // The Free link is on: Add a way, continue with it, and its Apps step reads DSM.
 f.page('status').querySelector<HTMLElement>('[data-home-add-way]')!.click();await settle()
 const wizard=document.body.querySelector<HTMLElement>('[data-setup-wizard]')!
 wizard.querySelector<HTMLElement>('[data-wizard-way="workers"]')!.click();await settle()
 wizard.querySelector<HTMLElement>('[data-wizard-footer] [data-wizard-next]')!.click();await settle()
 const checks=wizard.querySelector<HTMLElement>('[data-protection-checks]')!
 const block=checks.querySelector<HTMLElement>('[data-protection="auto-block"]')!
 expect(block.dataset.state).toBe('off')
 expect(block.textContent).toContain(t('guide.check.off'))
 expect(block.querySelector('a')?.getAttribute('href')).toBe(CATALOG.protections['auto-block'])
 const otp=checks.querySelector<HTMLElement>('[data-protection="dsm-2fa"]')!
 expect(otp.dataset.state).toBe('unknown')
 expect(otp.textContent).toContain(t('guide.check.unknown'))
 expect(apis().filter(api=>!/^SYNO\.Core\.(QuickConnect|DDNS\.Record)$/.test(api!))).toHaveLength(2)
 f.done()
})
