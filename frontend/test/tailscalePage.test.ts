// @vitest-environment jsdom
// The Tailscale page reads like NetBird's: Join while off, Apps once connected,
// and nothing about services, ports or Funnel.
import {afterEach,expect,test,vi} from 'vitest'
import {fixture,connected,t} from './providerFixture'
import {settle} from './support'
afterEach(()=>vi.restoreAllMocks())
const tabs=(page:HTMLElement)=>[...page.querySelectorAll<HTMLElement>('[data-ui-tab-value]')].map(b=>b.dataset.uiTabValue)
const noServices=(page:HTMLElement)=>expect(page.querySelector('[data-service-id],[data-ts-pick],[data-follow-services],[data-port-rows],[data-funnel],[data-exposure-confirm]')).toBeNull()

test('off, Tailscale shows only Join with its sign-in form',async()=>{
 const f=fixture();await settle();await f.nav('Tailscale')
 const page=f.page('tailscale')
 expect(tabs(page)).toEqual(['connection','join'])
 expect(page.querySelector('[data-tab-panel="join"] [data-connect-form="tailscale"]')).toBeTruthy()
 noServices(page);f.done()
})

test('Join sends only the sign-in and the control server from Advanced',async()=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 const f=fixture();await settle();await f.nav('Tailscale')
 const page=f.page('tailscale')
 page.querySelector<HTMLButtonElement>('[data-ts-connect]')!.click();await settle()
 expect(f.call.mock.calls.find(([m])=>m==='connection.connect')?.[1]).toEqual({provider:'tailscale'})
 f.call.mockClear()
 page.querySelector<HTMLElement>('[data-connect-advanced] button[aria-expanded]')!.click();await settle()
 const set=(sel:string,v:string)=>{const i=page.querySelector<HTMLInputElement>(sel)!;i.value=v;i.dispatchEvent(new Event('input'))}
 set('[data-ts-key]','tskey-auth-x');set('[data-login-server]','https://headscale.example.com');set('[data-login-origin]','https://id.example.com');await settle()
 page.querySelector<HTMLButtonElement>('[data-ts-connect]')!.click();await settle()
 expect(f.call.mock.calls.find(([m])=>m==='connection.connect')?.[1]).toEqual({provider:'tailscale',token:'tskey-auth-x',loginServer:'https://headscale.example.com',loginOrigin:'https://id.example.com'})
 f.done()
})

test('connected, Tailscale shows only Apps with the shared App list',async()=>{
 const f=fixture([connected('tailscale')]);await settle();await f.nav('Tailscale')
 const page=f.page('tailscale')
 expect(tabs(page)).toEqual(['connection','apps'])
 expect(page.querySelector('[data-tab-panel="apps"] [data-apps-page="private"]')).toBeTruthy()
 expect(page.textContent).not.toContain(t('tab.advanced'))
 noServices(page);f.done()
})

test('connected, the two Tailscale addresses are labelled by kind and the IPs are not repeated',async()=>{
 const endpoints=[{url:'http://100.64.0.10:5000/',public:false,localPort:5000},{url:'http://nas.example.ts.net:5000/',public:false,localPort:5000}]
 const labels=(page:HTMLElement)=>[...page.querySelectorAll<HTMLElement>('[data-ts-address]')].map(e=>e.dataset.tsAddress)
 let f=fixture([connected('tailscale',{endpoints,tailscaleIPs:['100.64.0.10']})]);await settle();await f.nav('Tailscale')
 let page=f.page('tailscale')
 expect(labels(page)).toEqual(['ip','dns'])
 expect(page.textContent).toContain('Tailscale IP')
 expect(page.textContent).toContain('MagicDNS')
 expect(page.textContent).not.toContain('Tailscale IP addresses')
 f.done()
 // An IPv6 address the rows do not show keeps its own row, with only that address.
 f=fixture([connected('tailscale',{endpoints,tailscaleIPs:['100.64.0.10','fd7a:115c:a1e0::a']})]);await settle();await f.nav('Tailscale')
 page=f.page('tailscale')
 expect(page.querySelector('[data-ts-other-ips]')?.textContent?.trim()).toBe('fd7a:115c:a1e0::a')
 f.done()
})
