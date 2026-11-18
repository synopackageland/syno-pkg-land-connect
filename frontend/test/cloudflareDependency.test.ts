// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {settle} from './support'
afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})
const t=(k:string)=>translate('en',k)
async function fixture(enabled:boolean,fail?:string,authMissing=false,invalid=false){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[{provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com'}]}
  if(method==='netbirdserver.status'&&invalid)return {}
  if(method==='netbirdserver.status')return {state:enabled?'running':'off',dependencies:{enabled,...(authMissing?{problem:{code:'cloudflare_reconnect_required',piece:'authorization',hop:'tunnel',action:'authorize_cloudflare',page:'cloudflare',impact:'maintenance'}}:{})}}
  if(method==='apps.list')return {apps:[],private:{},public:{available:true,connected:!authMissing,invalid:authMissing,zone:'example.com'}}
  if(method==='apps.workersstatus')return {connected:false}
  if(method==='connection.disconnect'||method==='apps.cloudflaredisconnect'){if(fail)throw {code:fail};return {provider:'cloudflare',state:'disconnected'}}
  return {}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}));await settle()
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(e=>e.textContent?.trim()==='Cloudflare')!.click();await settle()
 root.querySelector<HTMLElement>('[data-page="cloudflare"] [data-ui-tab-value="domain"]')!.click();await settle()
 return {root,call,done(){app.unmount();root.remove()}}
}
for(const auth of [false,true])test(`a dependent server refuses ${auth?'authorization removal':'tunnel shutdown'} in the dialog before any mutation`,async()=>{
 const f=await fixture(true)
 f.root.querySelector<HTMLElement>(auth?'[data-cf-disconnect]':'[data-cf-domain-tab] [data-way-off]')!.click();await settle()
 const d=document.querySelector<HTMLElement>(auth?'[data-cf-disconnect-dialog]':'[data-cloudflare-off-dialog]')!
 expect(d.textContent).toContain(t(auth?'dependency.stopMaintenance':'dependency.stopLine'))
 expect(d.textContent).toContain(t('dependency.stopOrder'))
 expect(d.querySelector('[data-cloudflare-off-confirm],[data-cf-disconnect-confirm]')).toBeNull()
 d.querySelector<HTMLElement>('[data-cloudflare-off-server]')!.click();await settle()
 expect(f.root.querySelector<HTMLElement>('[data-page="netbird"]')!.style.display).not.toBe('none')
 expect(f.call.mock.calls.filter(c=>['connection.disconnect','apps.cloudflaredisconnect'].includes(c[0]))).toHaveLength(0)
 f.done()
})
test('a dependency started after the dialog opened is refused inline by the backend',async()=>{
 const f=await fixture(false,'netbird_cloudflare_in_use')
 f.root.querySelector<HTMLElement>('[data-cf-domain-tab] [data-way-off]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-cloudflare-off-confirm]')!.click();await settle()
 expect(document.querySelector('[data-cloudflare-off-error]')?.textContent).toContain(t('dependency.stopOrder'))
 expect(document.querySelector('[data-cloudflare-off-server]')).toBeTruthy()
 f.done()
})
test('a tunnel shutdown failure remains inside the open dialog with its code',async()=>{
 const f=await fixture(false,'cloudflare_save_failed')
 f.root.querySelector<HTMLElement>('[data-cf-domain-tab] [data-way-off]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-cloudflare-off-confirm]')!.click();await settle()
 expect(document.querySelector('[data-cloudflare-off-error]')?.textContent).toContain('cloudflare_save_failed')
 expect(document.querySelector<HTMLButtonElement>('[data-cloudflare-off-confirm]')?.disabled).toBe(false)
 f.done()
})


test('a saved domain with missing authorization still offers the authorization recovery form',async()=>{
 const f=await fixture(true,undefined,true)
 expect(f.root.querySelector('[data-dependency-notice]')?.textContent).toContain(t('dependency.code.cloudflare_reconnect_required'))
 const button=f.root.querySelector<HTMLElement>('[data-cf-account] [data-cf-reconnect]')
 expect(button).toBeTruthy();button!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.cloudflareconnect',{})
 expect(f.call.mock.calls.some(c=>c[0]==='connection.disconnect')).toBe(false)
 f.done()
})

test('unreadable dependency status refuses shutdown and offers an inline status retry',async()=>{
 const f=await fixture(false,undefined,false,true)
 f.root.querySelector<HTMLElement>('[data-cf-domain-tab] [data-way-off]')!.click();await settle()
 const d=document.querySelector<HTMLElement>('[data-cloudflare-off-dialog]')!
 expect(d.querySelector<HTMLButtonElement>('[data-cloudflare-off-confirm]')!.disabled).toBe(true)
 expect(d.textContent).toContain(t('dependency.code.unavailable'))
 expect(d.textContent).toContain(t('dependency.action.check_status'))
 expect(f.call.mock.calls.some(c=>c[0]==='connection.disconnect')).toBe(false)
 f.done()
})

test('a healthy self-hosted server adds no notice to the Cloudflare or NetBird page',async()=>{
 const f=await fixture(true)
 expect(f.root.querySelector('[data-page="cloudflare"] [data-dependency-notice]')).toBeNull()
 ;[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(e=>e.textContent?.trim()==='NetBird')!.click();await settle()
 expect(f.root.querySelector('[data-page="netbird"] [data-dependency-notice]')).toBeNull()
 expect(f.root.textContent).not.toContain(t('dependency.cloudflareUsed'))
 f.done()
})
