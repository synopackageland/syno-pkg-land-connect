// @vitest-environment jsdom
import {afterEach,expect, test, vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {createStandaloneContext} from '@synopackageland/sdk'
afterEach(()=>vi.unstubAllGlobals())
const u=(key:string,locale='en')=>translate(locale,`ux.${key}`)
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
function click(root:HTMLElement,text:string){const node=[...root.querySelectorAll('button')].find(n=>n.textContent?.trim()===text);expect(node,text).toBeTruthy();node!.click()}
function clickNav(root:HTMLElement,label:string){const node=[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()===label);expect(node,label).toBeTruthy();node!.click()}
function page(root:HTMLElement,name:string){return root.querySelector<HTMLElement>(`[data-page="${name}"]`)!}
function fixture(admitted=true,locale='en'){
 const root=document.createElement('div');document.body.append(root)
 const reopen:Array<()=>void>=[]
 const call=vi.fn(async(method:string)=>{
  if(method==='app.status'){if(!admitted)throw {code:'admin_required'};return {user:'fixture',package:'SynoPkgConnect',version:'test'}}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 })
 const ctx=createStandaloneContext({call,locale,reopen:(fn:()=>void)=>{reopen.push(fn);return()=>{}}});ctx.files={endpoint:'cgi/api.cgi',token:()=>'fixture-token'}
 return {root,ctx,call,reopen,app:mount(root,ctx)}
}
test('overview keeps every running lane and scoped disable leaves the other active',async()=>{
 const f=fixture();await settle()
 const connections=[{provider:'cloudflare',state:'connected',shareUrl:'https://s.example.com/sharing/ExampleID'},{provider:'tailscale',state:'connected',loggedIn:true,shareUrl:'',endpoints:[{url:'https://nas.fixture.ts.net',localPort:5000,public:false}]}]
 f.call.mockImplementation(async(method:string)=>{
  if(method==='app.status')return {admin:true}
  if(method==='netbirdserver.status')return {state:'off',dependencies:{enabled:false}}
  if(method==='connection.disconnect')connections[0]={...connections[0]!,state:'disconnected',shareUrl:''}
  if(method==='connection.status'||method==='connection.disconnect')return {provider:'multiple',state:'connected',connections}
  return {}
 })
 click(f.root,u('refresh'));await settle()
 expect([...page(f.root,'status').querySelectorAll<HTMLElement>('[data-overview] [data-lane-id]')].map(l=>l.dataset.laneId)).toEqual(['cloudflare','tailscale'])
 expect(page(f.root,'status').querySelector('[data-lane-id="tailscale"]')?.textContent).toContain(translate('en','overview.nas.running'))
 clickNav(f.root,'Cloudflare');await settle();page(f.root,'cloudflare').querySelector<HTMLElement>('[data-cf-domain-tab] [data-way-off]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-cloudflare-off-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.disconnect',{provider:'cloudflare'})
 expect(page(f.root,'status').querySelector('[data-overview] [data-lane-id="cloudflare"]')).toBeNull()
 expect(page(f.root,'status').querySelector('[data-lane-id="tailscale"]')?.textContent).toContain(translate('en','overview.nas.running'))
 f.app.unmount();f.root.remove()
})
test('ordinary user refused by the entry gate cannot see or submit connection settings',async()=>{
 const f=fixture(false);await settle();expect(f.root.textContent).toContain(translate('en','error.admin_required'))
 expect(f.root.querySelector('input')).toBeNull();expect(f.call).not.toHaveBeenCalledWith('connection.status')
 for(const label of ['Cloudflare','Tailscale','NetBird','IPv6 direct','Settings'])clickNav(f.root,label)
 await settle();expect(f.root.querySelector('input')).toBeNull();expect(f.root.querySelector('[data-setup]')).toBeNull()
 expect(f.call.mock.calls.map(c=>c[0])).toEqual(['app.status'])
 f.app.unmount();f.root.remove()
})
test('session failure has actionable retry text; unmounted reopen does no work',async()=>{
 const f=fixture();await settle();f.call.mockRejectedValue({code:'session_required'})
 click(f.root,u('refresh'));await settle();expect(f.root.textContent).toContain(translate('en','error.session_required'))
 f.app.unmount();const n=f.call.mock.calls.length;f.reopen.forEach(fn=>fn());await settle();expect(f.call.mock.calls.length).toBe(n)
 f.root.remove()
})
test('two mounts and in-flight unmount retain independent state',async()=>{
 const a=fixture(),b=fixture();await settle();const n=b.call.mock.calls.length
 a.reopen.forEach(fn=>fn());await settle();expect(b.call.mock.calls.length).toBe(n)
 a.app.unmount();b.app.unmount();a.root.remove();b.root.remove()
})
test('the header refresh also re-reads the IPv6 checklist and the connectors',async()=>{
 const f=fixture();await settle()
 const reads=(method:string)=>f.call.mock.calls.filter(c=>c[0]===method).length
 const ipv6=reads('ipv6.status'),binaries=reads('binaries.status')
 expect(ipv6).toBe(1);expect(binaries).toBe(4)
 click(f.root,u('refresh'));await settle()
 expect(reads('ipv6.status')).toBe(ipv6+1)
 expect(reads('binaries.status')).toBe(binaries*2)
 f.app.unmount();f.root.remove()
})
// SDK 0.10.6: a DSM cookie without a SynoToken is refused as credential_not_ready.
test('a page DSM gave no sign-in token says so and forgets what it saw',async()=>{
 const f=fixture();await settle();f.call.mockRejectedValue({code:'credential_not_ready'})
 click(f.root,u('refresh'));await settle()
 const text=translate('en','error.credential_not_ready')
 expect(text).not.toBe('error.credential_not_ready')
 expect(f.root.textContent).toContain(text)
 f.app.unmount();f.root.remove()
})
test('a Connect that stopped after repeated crashes says how to start it again',async()=>{
 const f=fixture();await settle()
 f.call.mockImplementation(async(method:string)=>{if(method==='app.status')return {admin:true};if(method==='connection.status')throw {code:'connect_stopped'};return {}})
 click(f.root,u('refresh'));await settle()
 const text=translate('en','error.connect_stopped')
 expect(text).not.toBe('error.connect_stopped')
 expect(f.root.textContent).toContain(text)
 f.app.unmount();f.root.remove()
})
