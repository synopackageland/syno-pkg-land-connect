// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {settle} from './support'
import {translate} from '../src/i18n'
const mounted:Array<()=>void>=[]
afterEach(()=>{mounted.splice(0).forEach(f=>f());vi.useRealTimers()})
function fixture(extraConnections:Record<string,unknown>[]=[],entries:Record<string,unknown>[]=[]){
 const root=document.createElement('div');document.body.append(root)
 let state='needs-login'
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true}:method==='connection.status'?{provider:'multiple',connections:[{provider:'tailscale',state,loggedIn:state==='connected',shareUrl:'',loginUrl:'https://login.tailscale.com/a/fixture'},...JSON.parse(JSON.stringify(extraConnections))]}:method==='ports.list'?{entries,detection:'ready',revision:1}:{})
 const app=mount(root,createStandaloneContext({call}));mounted.push(()=>{app.unmount();root.remove()})
 const page=(name:string)=>root.querySelector<HTMLElement>(`[data-page="${name}"]`)!
 const nav=async(name:string)=>{[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(b=>b.textContent?.trim()===name)!.click();await settle()}
 return {root,call,page,nav,setState:(s:string)=>{state=s}}
}
test('the Home shows a method waiting for sign-in as a lane and under Needs attention; requirements and limitations are disclosed on demand',async()=>{
 const f=fixture();await settle();const home=f.page('status')
 // No way runs yet, so DSM's own address follows as the Local network card (its Phone action)
 expect([...home.querySelectorAll<HTMLElement>('[data-overview] [data-lane-id]')].map(l=>l.dataset.laneId)).toEqual(['tailscale','local'])
 expect([...home.querySelectorAll<HTMLElement>('[data-attention-item]')].map(i=>i.dataset.attentionId)).toEqual(['tailscale'])
 expect(home.querySelector('[data-home-add-way]')).toBeTruthy()
 expect(home.textContent).not.toContain('A free plan is available; bring your own domain')
 expect(home.querySelector('[data-choice-notes]')).toBeTruthy()
})
test('IPv6 polling keeps the saved services as read-only port information',async()=>{
 vi.useFakeTimers();const f=fixture([{provider:'ipv6',state:'disconnected',serviceIds:['dsm-http']}],[{id:'dsm-http',name:'DSM HTTP',protocol:'http',port:5000,source:'dsm',enabled:true,state:'ready'}]);await settle();await f.nav('IPv6 direct');const ipv6=f.page('ipv6')
 expect(ipv6.querySelector('[data-ipv6-services]')?.textContent).toContain('DSM HTTP (5000)')
 expect(ipv6.querySelector('[data-service-id]')).toBeNull()
 await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(ipv6.querySelector('[data-ipv6-services]')?.textContent).toContain('DSM HTTP (5000)')
})
test('unchanged Cloudflare polling preserves an edited own-domain name',async()=>{
 vi.useFakeTimers();const f=fixture([{provider:'cloudflare',state:'disconnected'}]);await settle();await f.nav('Cloudflare');const cf=f.page('cloudflare')
 const input=cf.querySelector('[data-cf-domain-tab] input:not([type=checkbox])') as HTMLInputElement
 input.value='photos';input.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(input.value).toBe('photos')
})
test('Cloudflare starts without a secret; the temporary address has its tab and Own domain has no token to paste',async()=>{
 const f=fixture();await settle();await f.nav('Cloudflare');const cf=f.page('cloudflare')
 const start=cf.querySelector<HTMLElement>('[data-cf-domain-tab]')!
 expect(start.querySelector('[data-cf-quick]')).toBeNull()
 expect(start.querySelector('input[type=password]')).toBeNull()
 expect(cf.querySelector('[data-tab-panel="temporary"] [data-cf-quick]')).toBeTruthy()
 expect(cf.querySelector('[data-advanced-section]')).toBeNull()
 expect([...start.querySelectorAll<HTMLButtonElement>('[data-use-more]')].some(b=>b.textContent?.trim()===translate('en','cloudflare.token'))).toBe(false)
 expect(start.querySelector('[data-cf-token],[data-token-guide]')).toBeNull()
})
test('Tailscale shows a real authorization action and updates success after login without manual refresh',async()=>{
 vi.useFakeTimers();const f=fixture();await settle();await f.nav('Tailscale')
 const ts=f.page('tailscale');expect(ts.querySelector('[data-login-action]')).toBeTruthy()
 expect(ts.querySelector('[data-connection-state="needs-login"]')).toBeTruthy()
 f.setState('connected');await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(ts.querySelector('[data-connection-state="connected"]')).toBeTruthy()
 f.call.mockRejectedValue({code:'service_unavailable'});await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(ts.querySelector('[data-connection-state="unknown"]')).toBeTruthy()
 expect(ts.querySelector('[data-connection-state="connected"]')).toBeNull()
})
test('a daemon-reported unknown read stays unconfirmed and keeps the disable action',async()=>{
 const f=fixture();f.setState('unknown');await settle();await f.nav('Tailscale')
 const ts=f.page('tailscale')
 expect(ts.querySelector('[data-connection-state="unknown"]')?.textContent).toBe('Status unavailable')
 expect(ts.querySelector('[data-stop]')).toBeTruthy()
 expect(ts.textContent).not.toContain('Failed')
})
test('a start that fails after it was accepted shows the reported reason',async()=>{
 const f=fixture([]);f.setState('failed');f.call.mockImplementation(async(method:string)=>method==='app.status'?{admin:true}:method==='connection.status'?{provider:'multiple',connections:[{provider:'tailscale',state:'failed',code:'binary_required',shareUrl:''}]}:method==='ports.list'?{entries:[],detection:'ready',revision:1}:{})
 await settle();await f.nav('Tailscale')
 expect(f.page('tailscale').querySelector('[data-connection-reason]')?.textContent).toBe(translate('en','provider.binaryRequired'))
})
// Give each independent UI scenario its own timeout and fixture cleanup.
test.each([
 ['tailscale_start_failed','provider.startFailed'],
 ['start_failed','provider.startFailed'],
 ['tailscale_stop_failed','provider.stopFailed'],
])('a start or stop that failed in the daemon names that step, never a bare failure: %s',async(code,key)=>{
  const f=fixture([]);f.setState('failed');f.call.mockImplementation(async(method:string)=>method==='app.status'?{admin:true}:method==='connection.status'?{provider:'multiple',connections:[{provider:'tailscale',state:'failed',code,shareUrl:''}]}:method==='ports.list'?{entries:[],detection:'ready',revision:1}:{})
  await settle();await f.nav('Tailscale')
  expect(f.page('tailscale').querySelector('[data-connection-reason]')?.textContent).toBe(translate('en',key))
})
test('an accepted disconnect reads as disconnecting until polling sees it stopped',async()=>{
 vi.useFakeTimers();const f=fixture();f.setState('connected');await settle();await f.nav('Tailscale');const ts=f.page('tailscale')
 const status=f.call.getMockImplementation()!
 f.call.mockImplementation(async(method:string,params?:Record<string,unknown>)=>method==='connection.disconnect'?{provider:'multiple',connections:[{provider:'tailscale',state:'disconnecting',shareUrl:''}]}:status(method,params))
 ts.querySelector<HTMLElement>('[data-stop]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.disconnect',{provider:'tailscale'})
 expect(translate('en','ux.state.disconnecting')).not.toBe('ux.state.disconnecting')
 expect(ts.querySelector('[data-connection-state="disconnecting"]')?.textContent).toBe(translate('en','ux.state.disconnecting'))
 f.setState('disconnected');await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(ts.querySelector('[data-connection-state="disconnected"]')).toBeTruthy()
})
test('a mutation refused while a disconnect is still stopping is progress, not a failure',async()=>{
 const f=fixture();f.setState('connected');await settle();await f.nav('Tailscale')
 const status=f.call.getMockImplementation()!
 f.call.mockImplementation(async(method:string,params?:Record<string,unknown>)=>{if(method==='connection.disconnect')throw {code:'disconnect_busy'};return status(method,params)})
 f.page('tailscale').querySelector<HTMLElement>('[data-stop]')!.click();await settle()
 expect(translate('en','provider.disconnecting')).not.toBe('provider.disconnecting')
 // A start or stop still running is a passing toast, not a notice that stays.
 expect(f.root.querySelector('.app-notices')).toBeNull()
 expect(document.body.textContent).toContain(translate('en','provider.disconnecting'))
 expect(f.root.querySelector('.app-notices [role=alert]')).toBeNull()
})
