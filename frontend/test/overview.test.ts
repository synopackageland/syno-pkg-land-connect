// @vitest-environment jsdom
// The Overview: a topology lane per method that is set up or running, its
// addresses under the lane, workers.dev included; the wizard dialog opened
// from Set up or Add a way, and the closed comparison below.
import {createStandaloneContext, createStandaloneHost} from '@synopackageland/sdk'
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

const shown=(el:HTMLElement)=>el.style.display!=='none'
const lane=(home:HTMLElement,id:string)=>home.querySelector<HTMLElement>(`[data-overview] [data-lane-id="${id}"]`)

test('without any connection the Overview is a picture and one Set up, which opens the wizard dialog',async()=>{
 const f=fixture();await settle();const home=f.page('status')
 expect(home.querySelector('[data-topology-row][data-lane-id]')).toBeNull()
 const first=home.querySelector<HTMLElement>('[data-home-first]')!
 // 0111 uses three labelled nodes; the question mark is an aria-hidden icon.
 const picture=first.querySelector('[data-no-way-picture]')!
 expect(picture.getAttribute('role')).toBe('img')
 expect(picture.getAttribute('aria-label')).toContain(t('path.phase.off'))
 for(const [node,key] of [['devices','devices'],['way','none'],['nas','nas']]){
  expect(picture.querySelector(`[data-no-way-node="${node}"]`)?.textContent).toBe(t('path.node.'+key))
 }
 expect(picture.querySelector('[data-no-way-node="way"][data-state="none"] svg[aria-hidden="true"]')).toBeTruthy()
 expect(first.textContent).toContain(t('wizard.title'))
 expect(home.querySelector('[data-setup-wizard]')).toBeNull()
 expect(first.querySelectorAll('button')).toHaveLength(1)
 // Nothing opens by itself.
 expect(document.body.querySelector('[data-setup-wizard]')).toBeNull()
 first.querySelector<HTMLElement>('[data-home-set-up]')!.click();await settle()
 const wizard=document.body.querySelector<HTMLElement>('[data-setup-wizard]')!
 expect(wizard.closest('[role="dialog"]')).toBeTruthy()
 expect(wizard.dataset.wizardStep).toBe('choose')
 expect(wizard.closest('[role="dialog"]')!.querySelector('header [data-dialog-close]')).toBeTruthy()
 expect(wizard.querySelector('[data-wizard-cancel]')).toBeNull()
 // The cards that recommended a way of their own are gone; one place chooses.
 expect(home.querySelector('[data-use-tier]')).toBeNull()
 // The detailed comparison stays, closed.
 expect(home.querySelector('[data-choice-notes] [aria-expanded]')?.getAttribute('aria-expanded')).toBe('false')
 f.done()
})

test('once a method is set up, the Home is the returning view: the lanes, no uses',async()=>{
 const f=fixture([connected('tailscale')]);await settle();const home=f.page('status')
 expect(home.querySelector('[data-home-mode]')?.getAttribute('data-home-mode')).toBe('returning')
 expect(home.querySelector('[data-lane-id="tailscale"]')).toBeTruthy()
 expect(home.querySelector('[data-guide-section]')).toBeNull()
 expect(home.querySelector('[data-overview-start]')).toBeNull()
 f.done()
})

const relay=(status:Record<string,unknown>)=>({'apps.workersstatus':()=>status,'apps.list':()=>({private:{available:false},public:{available:false},relay:status,apps:[]})})

test('a connected workers.dev account gets its lane after Cloudflare, which opens the Free link',async()=>{
 const f=fixture([connected('cloudflare',{shareUrl:'https://nas.example.com'}),connected('tailscale')],relay({connected:true,relayState:'connected',subdomain:'acme'}));await settle()
 const home=f.page('status')
 expect([...home.querySelectorAll<HTMLElement>('[data-overview] [data-lane-id]')].map(l=>l.dataset.laneId)).toEqual(['cloudflare','workers','tailscale'])
 const workers=lane(home,'workers')!
 expect(workers.textContent).toContain('Cloudflare')
 expect(workers.textContent).toContain(t('overview.nas.running'))
 expect(workers.textContent).toContain(t('cfpage.path.worker'))
 workers.querySelector<HTMLButtonElement>('[data-topology-row]')!.click();await settle()
 const cf=f.page('cloudflare')
 expect(shown(cf)).toBe(true)
 expect(cf.querySelector('[data-free-link]')).toBeTruthy()
 f.done()
})

test('a workers.dev token that stopped working asks for a new one under Needs attention; a backend without the relay shows no lane',async()=>{
 const f=fixture([connected('tailscale')],relay({connected:true,invalid:true,subdomain:'acme'}));await settle()
 const workers=lane(f.page('status'),'workers')!
 expect(workers.textContent).toContain(t('overview.relay.invalid'))
 expect(f.page('status').querySelector('[data-attention-id="workers"]')?.textContent).toContain(t('home.workers.invalid'))
 f.done()
 const g=fixture([connected('tailscale')],relay({connected:false,code:'workers_unavailable'}));await settle()
 expect(g.page('status').querySelector('[data-lane-id="workers"]')).toBeNull()
 g.done()
})

test('workers.dev not yet connected is no lane and no item',async()=>{
 const f=fixture([connected('tailscale')],relay({connected:false}));await settle()
 const home=f.page('status')
 expect(home.querySelector('[data-lane-id="workers"]')).toBeNull()
 expect(home.querySelector('[data-attention-id="workers"]')).toBeNull()
 f.done()
})

test('buttons keep the default size, except a Callout\'s one action, which ui docs/callout.md draws small',async()=>{
 const sources=import.meta.glob('../src/**/*.vue',{query:'?raw',import:'default',eager:true}) as Record<string,string>
 expect(Object.keys(sources).length).toBeGreaterThan(20)
 for(const [file,source] of Object.entries(sources))for(const line of source.split('\n').filter(l=>/<Button[^>]*\ssize=/.test(l))){
  expect(line,file).toMatch(/#actions><Button [^>]*size="sm"/)
  expect(source,file).toMatch(/<Callout/)
 }
})

test('each running method gets a lane with its addresses; one Add a way below',async()=>{
 const f=fixture([
  connected('cloudflare',{shareUrl:'https://nas.example.com'}),
  connected('tailscale',{endpoints:[{url:'https://nas.tail1.ts.net',public:false,localPort:5000}]}),
 ],{'connection.check':p=>({provider:p.provider,state:'ok',externalVerified:p.provider==='cloudflare',checkedAt:'2026-10-12T08:00:00Z',attempts:1})});await settle()
 const home=f.page('status')
  expect([...home.querySelectorAll<HTMLElement>('[data-overview] [data-lane-id]')].map(l=>l.dataset.laneId)).toEqual(['cloudflare','tailscale'])
 expect(lane(home,'cloudflare')!.textContent).toContain(t('overview.reach.external'))
 expect(lane(home,'tailscale')!.textContent).toContain(t('overview.reach.private'))
 expect(lane(home,'tailscale')!.textContent).toContain(t('overview.nas.running'))
 expect(lane(home,'cloudflare')!.querySelector<HTMLInputElement>('[data-url-card] input')?.value).toBe('https://nas.example.com')
 expect(lane(home,'tailscale')!.querySelector<HTMLInputElement>('[data-url-card] input')?.value).toBe('https://nas.tail1.ts.net')
 expect(home.querySelectorAll('[data-overview-add]')).toHaveLength(0)
 expect(home.querySelectorAll('[data-home-add-way]')).toHaveLength(1)
 f.done()
})

test('choosing a lane opens its provider page',async()=>{
 const f=fixture([connected('tailscale')]);await settle()
 lane(f.page('status'),'tailscale')!.querySelector<HTMLButtonElement>('[data-topology-row]')!.click();await settle()
 expect(shown(f.page('tailscale'))).toBe(true)
 expect(shown(f.page('status'))).toBe(false)
 f.done()
})

test('Add a way opens the wizard dialog over the lanes, and its header close closes it',async()=>{
 const f=fixture([connected('tailscale')]);await settle();const home=f.page('status')
 home.querySelector<HTMLElement>('[data-home-add-way]')!.click();await settle()
 const wizard=()=>document.body.querySelector<HTMLElement>('[data-setup-wizard]')
 expect(wizard()?.closest('[role="dialog"]')).toBeTruthy()
 expect(home.querySelector('[data-overview]')).toBeTruthy()
 expect(wizard()!.querySelector('[data-wizard-way="tailscale"] [data-wizard-already]')).toBeTruthy()
 wizard()!.closest('[role="dialog"]')!.querySelector<HTMLElement>('header [data-dialog-close]')!.click();await settle()
 expect(wizard()).toBeNull()
 expect(home.querySelector('[data-overview]')).toBeTruthy()
 f.done()
})

test('a temporary address says under its lane that it changes on restart, and offers the Free link',async()=>{
 const f=fixture([connected('cloudflare',{mode:'quick',shareUrl:'https://fixture-quick.trycloudflare.com'})]);await settle()
 const note=lane(f.page('status'),'cloudflare')!.querySelector<HTMLElement>('[data-lane-temporary]')!
 expect(note.textContent).toContain(t('temporary.changes'))
 note.querySelector<HTMLElement>('[data-lane-free-link]')!.click();await settle()
 const wizard=document.body.querySelector<HTMLElement>('[data-setup-wizard]')!
 expect(wizard.dataset.wizardStep).toBe('choose')
 expect(wizard.querySelector('[data-wizard-way="workers"]')!.getAttribute('aria-checked')).toBe('true')
 f.done()
 // An own domain stays the same: no note.
 const g=fixture([connected('cloudflare',{mode:'domain',shareUrl:'https://nas.example.com'})]);await settle()
 expect(g.page('status').querySelector('[data-lane-temporary]')).toBeNull()
 g.done()
})

test('the Overview checks every running method once and again when the page is refreshed',async()=>{
 const f=fixture([connected('cloudflare',{shareUrl:'https://nas.example.com'}),connected('netbird',{netbirdIP:'100.64.0.9'})]);await settle()
 const overview=()=>f.call.mock.calls.filter(c=>c[0]==='connection.check').map(c=>(c[1] as {provider:string}).provider)
 // Overview and the provider status bars share one request per method
 expect(overview().filter(p=>p==='cloudflare')).toHaveLength(1)
 expect(overview().filter(p=>p==='netbird')).toHaveLength(1)
 expect(f.page('status').querySelector('[data-overview-recheck]')).toBeNull()
 f.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(overview().filter(p=>p==='cloudflare')).toHaveLength(2)
 expect(overview().filter(p=>p==='netbird')).toHaveLength(2)
 f.done()
})

test('a check still running is asked again until it settles',async()=>{
 vi.useFakeTimers()
 let n=0
 const f=fixture([connected('cloudflare',{shareUrl:'https://nas.example.com'})],{'connection.check':p=>++n<3?{provider:p.provider,state:'checking'}:{provider:p.provider,state:'failed',code:'check_timeout'}});await settle()
 const cf=()=>lane(f.page('status'),'cloudflare')!
 expect(cf().textContent).toContain(t('overview.reach.notChecked'))
 await vi.advanceTimersByTimeAsync(2000);await settle()
 await vi.advanceTimersByTimeAsync(2000);await settle()
 expect(cf().textContent).toContain(t('overview.reach.unreachable'))
 expect(cf().querySelector('[data-lane-reason]')?.textContent).toContain(t('connect.check.code.check_timeout'))
 f.done()
})

test('a failed method shows red in its lane and its reason under Needs attention',async()=>{
 const f=fixture([{provider:'netbird',state:'failed',code:'netbird_start_failed',simulation:false,shareUrl:''}]);await settle()
 const nb=lane(f.page('status'),'netbird')!
 expect(nb.textContent).toContain(t('ux.state.failed'))
 expect(nb.querySelector('[data-lane-reason]')).toBeNull()
 expect(f.page('status').querySelector('[data-attention-id="netbird"]')?.textContent).toContain(t('provider.startFailed'))
 f.done()
})

test('a sign-in lane shows a key and waits for the user',async()=>{
 const f=fixture([{provider:'tailscale',state:'needs-login',simulation:false,shareUrl:''}]);await settle()
 const ts=lane(f.page('status'),'tailscale')!
 expect(ts.textContent).toContain(t('ux.state.needs-login'))
 expect(ts.querySelector('[data-topology-row]')?.getAttribute('aria-label')).toContain(t('ux.state.needs-login'))
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the Overview is translated in %s',locale=>{
 for(const key of ['overview.title','home.attention.title','home.addWay','home.compare','wizard.title','overview.relay.invalid','home.setUp','home.switchFreeLink','temporary.changes','overview.node.nas','overview.node.anyBrowser','overview.node.yourDevices','overview.node.router',
  'overview.reach.external','overview.reach.notChecked','overview.reach.private','overview.reach.unverified','overview.reach.unreachable','overview.reach.waiting','overview.reach.off',
  'overview.nas.running','overview.nas.checkFailed','overview.nas.approval','overview.peersOnline'])
  expect(translate(locale,key),`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
})

test('the NAS name comes from the server independently of its public browser address',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const {mount}=await import('../src/main')
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true,nasName:'Home NAS'}:method==='connection.status'?{provider:'multiple',state:'disconnected',simulation:false,shareUrl:'',connections:[connected('tailscale')]}:method==='connection.check'?{provider:'tailscale',state:'not-checked'}:{})
 const app=mount(root,createStandaloneContext({call,fullPage:false,host:createStandaloneHost({host:'dsm',dsmStandalone:false})}));await settle()
 const nas=root.querySelector<HTMLElement>('[data-page="status"] [data-topology-target]')!
 expect(nas.textContent).toContain(t('overview.node.nas'))
 expect(nas.textContent).toContain('Home NAS')
 expect(nas.textContent).not.toContain('dsm')
 app.unmount();root.remove()
})

test('while the daemon does not answer the lanes are unknown and show no addresses',async()=>{
 vi.useFakeTimers()
 let n=0
 const f=fixture([],{'connection.status':()=>{if(++n>1)throw {code:'provider_unavailable'};return {provider:'multiple',state:'connected',simulation:false,shareUrl:'',connections:[connected('cloudflare',{shareUrl:'https://nas.example.com'})]}}});await settle()
 const cf=()=>lane(f.page('status'),'cloudflare')!
 expect(cf().querySelector('[data-url-card]')).toBeTruthy()
 await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(cf().textContent).toContain(t('setup.unknown'))
 expect(cf().querySelector('[data-url-card]')).toBeNull()
 f.done()
})

test('the topology is named for assistive technology without repeating the page title on screen',async()=>{
 const f=fixture([connected('tailscale')]);await settle()
 const home=f.page('status')
 expect(home.querySelector('[data-overview] [role="group"]')?.getAttribute('aria-label')).toBe(t('overview.title'))
 expect(home.textContent).not.toContain(t('overview.title'))
 f.done()
})

test('a lane names how many devices are online when the daemon reports it',async()=>{
 const f=fixture([connected('tailscale',{peersOnline:3}),connected('netbird',{netbirdIP:'100.64.0.9'})]);await settle()
 const home=f.page('status')
 expect(lane(home,'tailscale')!.textContent).toContain(t('overview.peersOnline').replace('{count}','3'))
 expect(lane(home,'netbird')!.textContent).not.toContain(t('overview.peersOnline').replace('{count}',''))
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the online device count keeps its number in %s',locale=>{
 expect(translate(locale,'overview.peersOnline')).toContain('{count}')
})
