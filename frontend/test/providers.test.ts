// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {translate} from '../src/i18n'
import {shellCopy} from '../src/shell/i18n'
const u=(key:string,locale='en')=>translate(locale,`ux.${key}`)
// The connect form folds its start options; a connected page has Advanced as a tab.
async function openStartOptions(root:HTMLElement){page(root,'tailscale').querySelector<HTMLElement>('[data-connect-advanced] button[aria-expanded]')!.click();await settle()}
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
function click(root:HTMLElement,text:string){const button=[...root.querySelectorAll('button')].find(b=>b.textContent?.trim()===text);expect(button,text).toBeTruthy();button!.click()}
function clickNav(root:HTMLElement,label:string){const node=[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()===label);expect(node,label).toBeTruthy();node!.click()}
function page(root:HTMLElement,name:string){return root.querySelector<HTMLElement>(`[data-page="${name}"]`)!}
function fixture(locale='en',status:unknown={provider:'fake',state:'disconnected',simulation:true,shareUrl:''}){
 vi.spyOn(window,'open').mockReturnValue(null)
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true}:method==='ports.list'?{detection:'ready',mappingState:'pending-provider',revision:1,entries:[{id:'dsm-http',name:'DSM HTTP',protocol:'http',port:5000,source:'dsm',enabled:true,state:'ready'}]}:status)
 const ctx=createStandaloneContext({call,locale})
 return {root,call,app:mount(root,ctx)}
}
test('IPv6 shows an expanded guide and never starts a connection',async()=>{
 const f=fixture();await settle();clickNav(f.root,shellCopy('en').ipv6);await settle()
 const ipv6=page(f.root,'ipv6')
 expect(ipv6.querySelector('[data-ipv6-checklist]')).toBeTruthy()
 expect(ipv6.querySelector('[data-ipv6-risk]')?.textContent).toContain(translate('en','risk.ipv6'))
 expect(ipv6.querySelector('[data-ipv6-connect],[data-ipv6-confirm]')).toBeNull()
 expect(f.call.mock.calls.some(([m])=>m==='connection.connect')).toBe(false)
 f.app.unmount();f.root.remove()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('provider screen uses complete %s translations',async locale=>{
 const {translate}=await import('../src/i18n')
 for(const key of ['key','login.tailscale','attention','connecting','failed']){
  expect(translate(locale,`provider.${key}`)).not.toBe(`provider.${key}`)
 }
 for(const key of ['advanced'])expect(u(key,locale)).not.toBe(`ux.${key}`)
 const f=fixture(locale);await settle();clickNav(f.root,'Tailscale');await settle()
 expect(page(f.root,'tailscale').querySelector('[data-ts-connect]')?.textContent?.trim()).toBe(translate(locale,'provider.login.tailscale'))
 expect(page(f.root,'tailscale').querySelector('[data-exposure-confirm]')).toBeNull()
 clickNav(f.root,shellCopy(locale).ipv6);await settle();expect(page(f.root,'ipv6').textContent).toContain(translate(locale,'risk.ipv6'))
 f.app.unmount();f.root.remove()
})
test('switching away clears the optional auth key',async()=>{
 const f=fixture();await settle();clickNav(f.root,'Tailscale');await settle();await openStartOptions(f.root)
 const input=page(f.root,'tailscale').querySelector('input[type=password]') as HTMLInputElement
 input.value='SYNTHETIC';input.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 clickNav(f.root,shellCopy('en').ipv6);await settle();clickNav(f.root,'Tailscale');await settle()
 expect((page(f.root,'tailscale').querySelector('input[type=password]') as HTMLInputElement).value).toBe('')
 f.app.unmount();f.root.remove()
})
test('missing Tailscale binaries that cannot be downloaded say so',async()=>{
 const f=fixture();await settle();clickNav(f.root,'Tailscale');await settle()
 f.call.mockImplementation(async()=>{throw {code:'binary_required'}})
 click(page(f.root,'tailscale'),'Sign in to Tailscale');await settle()
 expect(f.call).toHaveBeenCalledWith('binaries.update',{product:'tailscale'})
 expect(f.root.textContent).toContain(translate('en','connect.downloadFailed'))
 f.app.unmount();f.root.remove()
})

test('a connect refused while another start is pending reads as still connecting, not failed',async()=>{
 const f=fixture();await settle();clickNav(f.root,'Tailscale');await settle()
 f.call.mockImplementation(async(method:string)=>{if(method==='connection.connect')throw {code:'connect_busy'};return {provider:'tailscale',state:'connecting',simulation:false,shareUrl:''}})
 click(page(f.root,'tailscale'),'Sign in to Tailscale');await settle()
 expect(f.root.querySelector('.app-notices')).toBeNull()
 expect(document.body.textContent).toContain(translate('en','provider.connecting'))
 expect(f.root.querySelector('.app-notices [role=alert]')).toBeNull()
 f.app.unmount();f.root.remove()
})
