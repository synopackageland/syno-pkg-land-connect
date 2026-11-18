// @vitest-environment jsdom
// The Home: first visit or returning, by the backend's state alone; Needs
// attention only while something is wrong; workers.dev as a lane; Compare
// all ways folded at the bottom of both.
import {afterEach,expect,test,vi} from 'vitest'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
import {attentionItems,homeMode} from '../src/connect/home'
import {relayLane} from '../src/connect/topology'
import type {ConnectionStatus} from '../src/context'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks();window.localStorage.clear()})

const apps={apps:[],private:{available:false},public:{available:false,connected:false},relay:{connected:true,relayState:'connected',subdomain:'acme'}}
const mode=(home:HTMLElement)=>home.querySelector('[data-home-mode]')?.getAttribute('data-home-mode')
const status=(provider:string,state:ConnectionStatus['state'],extra:Partial<ConnectionStatus>={})=>({provider,state,simulation:false,shareUrl:'',...extra}) as ConnectionStatus

test('the mode waits for both reads, then follows what is set up, workers.dev included',()=>{
 expect(homeMode({read:false,unavailable:false,lanes:0})).toBe('loading')
 expect(homeMode({read:true,unavailable:false,lanes:0})).toBe('first')
 expect(homeMode({read:true,unavailable:false,lanes:1})).toBe('returning')
 expect(homeMode({read:true,unavailable:false,lanes:0,workers:{connected:true}})).toBe('returning')
 expect(homeMode({read:true,unavailable:true,lanes:0})).toBe('unknown')
})

test('Needs attention lists the DSM review, a failed or waiting way and a broken workers.dev token, in that order',()=>{
 const items=attentionItems({
  cloudflare:status('cloudflare','failed',{code:'pin_proxy_required'}),
  tailscale:status('tailscale','needs-login'),
  netbird:status('netbird','connected'),
  ipv6:status('ipv6','failed',{code:'ipv6_unavailable'}),
 },{connected:true,invalid:true,code:'cloudflare_token_invalid'},false)
 expect(items.map(i=>i.id)).toEqual(['dsm-cloudflare','tailscale','ipv6','workers'])
 expect(attentionItems({tailscale:status('tailscale','failed',{code:'pin_proxy_required'})},undefined,false).map(i=>i.id)).toEqual(['dsm-tailscale'])
 // Nothing is claimed while the status cannot be read.
 expect(attentionItems({tailscale:status('tailscale','needs-login')},undefined,true)).toEqual([])
})

test('a broken workers.dev token alone still makes the returning view',()=>{
 expect(homeMode({read:true,unavailable:false,lanes:0,workers:{connected:false,invalid:true}})).toBe('returning')
 expect(relayLane({connected:false,invalid:true},false)).toBeTruthy()
})

test('nothing set up: the first visit, no Needs attention, the comparison folded',async()=>{
 const f=fixture([],{'apps.workersstatus':()=>({connected:false})});await settle();const home=f.page('status')
 expect(mode(home)).toBe('first')
 expect(home.querySelector('[data-home-first]')).toBeTruthy()
 expect(home.querySelector('[data-needs-attention]')).toBeNull()
 expect(home.querySelector('[data-home-add-way]')).toBeNull()
 expect(home.querySelector('[data-home-compare] [aria-expanded]')?.getAttribute('aria-expanded')).toBe('false')
 f.done()
})

test('workers.dev alone makes the returning view, its lane uses the Cloudflare Worker node and opening the Free link',async()=>{
 const f=fixture([],{'apps.workersstatus':()=>({connected:true,relayState:'connected',subdomain:'acme'}),'apps.list':()=>apps});await settle();const home=f.page('status')
 expect(mode(home)).toBe('returning')
 const lane=home.querySelector<HTMLElement>('[data-overview] [data-lane-id="workers"]')!
 expect(lane.textContent).toContain('Cloudflare')
 expect(lane.textContent).toContain(t('cfpage.path.worker'))
 expect(lane.textContent).toContain(t('overview.nas.running'))
 expect(home.querySelector('[data-needs-attention]')).toBeNull()
 lane.querySelector<HTMLButtonElement>('[data-topology-row]')!.click();await settle()
 const cf=f.page('cloudflare')
 expect(cf.style.display).not.toBe('none')
 expect(cf.querySelector('[data-free-link]')).toBeTruthy()
 f.done()
})

test('workers.dev sits right after Cloudflare among the lanes',async()=>{
 const f=fixture([connected('tailscale'),connected('cloudflare',{shareUrl:'https://nas.example.com'})],{'apps.workersstatus':()=>({connected:true,relayState:'connected',subdomain:'acme'})});await settle()
 expect([...f.page('status').querySelectorAll<HTMLElement>('[data-overview] [data-lane-id]')].map(l=>l.dataset.laneId)).toEqual(['cloudflare','workers','tailscale'])
 f.done()
})

test('a workers.dev token that stopped working is one item under Needs attention with its one action',async()=>{
 const f=fixture([],{'apps.workersstatus':()=>({connected:true,relayState:'connected',subdomain:'acme',invalid:true}),'apps.list':()=>apps});await settle();const home=f.page('status')
 const item=home.querySelector<HTMLElement>('[data-attention-id="workers"]')!
 expect(item.textContent).toContain(t('home.workers.invalid'))
 expect(item.querySelectorAll('button:not([data-ui-callout-close])')).toHaveLength(1)
 item.querySelector<HTMLElement>('[data-attention-open]')!.click();await settle()
 const cf=f.page('cloudflare')
 expect(cf.style.display).not.toBe('none')
 expect(cf.querySelector('[data-free-link]')).toBeTruthy()
 f.done()
})

test('a backend without workers.dev still shows the Home',async()=>{
 const f=fixture([connected('tailscale')],{'apps.workersstatus':()=>{throw {code:'unknown_method'}}});await settle();const home=f.page('status')
 expect(mode(home)).toBe('returning')
 expect(home.querySelector('[data-lane-id="workers"]')).toBeNull()
 f.done()
})

test('the DSM review shows once on the Home, inside Needs attention',async()=>{
 const f=fixture([status('cloudflare','failed',{code:'pin_proxy_required'}),status('tailscale','failed',{code:'pin_proxy_required'})]);await settle();const home=f.page('status')
 expect(home.querySelectorAll('[data-dsm-review]')).toHaveLength(2)
 expect(home.querySelectorAll('[data-needs-attention] [data-dsm-review]')).toHaveLength(2)
 f.done()
})

test('workers.dev is read again after each refresh',async()=>{
 const f=fixture([connected('tailscale')]);await settle()
 const reads=()=>f.call.mock.calls.filter(c=>c[0]==='apps.workersstatus').length
 const before=reads()
 expect(before).toBeGreaterThan(0)
 f.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(reads()).toBe(before+1)
 f.done()
})

test('unused stale public authorization is silent on Home, while an enabled way warns',()=>{
 const connections={cloudflare:status('cloudflare','failed',{code:'cloudflare_reconnect_required'})}
 expect(attentionItems(connections,{connected:true,usage:{public:false,relay:true}},false)).toEqual([])
 expect(attentionItems(connections,{connected:true,usage:{public:true,relay:true}},false).map(i=>i.id)).toEqual(['cloudflare'])
 expect(attentionItems({}, {connected:false,invalid:true,usage:{public:false,relay:false}},false)).toEqual([])
})

test('Free link Overview and Cloudflare share one check, hide disabled ways and keep the NAS name separate from its address',async()=>{
 let finishCheck:(value:unknown)=>void=()=>{}
 const pending=new Promise(resolve=>{finishCheck=resolve})
 const url='https://dsm.example.workers.dev'
 const f=fixture([status('cloudflare','disconnected',{mode:'domain',hostname:'old.example.com'}),status('tailscale','disconnected',{loggedIn:true})],{
  'app.status':()=>({admin:true,nasName:'Home NAS'}),
  'apps.workersstatus':()=>({connected:true,relayState:'connected',subdomain:'example',dsm:{provider:'workers',url}}),
  'apps.list':()=>apps,
  'connection.check':()=>pending,
 })
 try{
  await settle();const home=f.page('status')
  expect(home.querySelector('[data-lane-id="cloudflare"]')).toBeNull()
  expect(home.querySelector('[data-lane-id="tailscale"]')).toBeNull()
  expect(home.textContent).toContain('Home NAS')
  expect(home.querySelector('[data-lane-id="workers"]')?.textContent).toContain(t('cfpage.path.worker'))
  await f.nav('Cloudflare')
  expect(f.call.mock.calls.filter(c=>c[0]==='connection.check'&&c[1]?.provider==='workers')).toHaveLength(1)
  finishCheck({provider:'workers',state:'ok',externalVerified:true,checkedAt:'2026-10-27T02:20:00Z'});await settle()
  expect(home.querySelector('[data-lane-id="workers"]')?.textContent).toContain(t('overview.reach.external'))
  expect(home.querySelector<HTMLInputElement>('[data-lane-id="workers"] input')?.value).toBe(url)
  const cf=f.page('cloudflare')
  expect(cf.querySelector('[data-connection-check]')?.getAttribute('data-check-state')).toBe('ok')
  expect(cf.querySelector('[data-check-scope]')?.textContent).toBe(t('connect.check.external'))
 }finally{f.done()}
})

test('an unused off Free link with stale credentials has no Overview lane',()=>{
 const workers={connected:false,invalid:true,usage:{public:false,relay:false}}
 expect(relayLane(workers,false)).toBeUndefined()
 expect(homeMode({read:true,unavailable:false,lanes:0,workers})).toBe('first')
})
