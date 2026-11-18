// @vitest-environment jsdom
// Phone: the phone browser page's route per way, the best one, and the
// Overview's Phone section with its menu of ways (and the Local network lane).
import {afterEach,expect,test,vi} from 'vitest'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
import type {PhoneAddNasReply} from '../src/connect/phoneLink'
import {bestRoute,localRoute,phoneRoutes,routeFor,routeNeed} from '../src/connect/phoneRoutes'
import {PHONE_CLOSED_KEY} from '../src/connect/phoneHome'
afterEach(()=>{vi.restoreAllMocks();document.body.innerHTML='';localStorage.clear()})

const pin='0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
const PAGE='/m'
const lan:PhoneAddNasReply={name:'DiskStation',ports:true,addresses:[{url:'https://192.168.1.20:5001',kind:'lan',sha256:pin}]}
const ways={
 cloudflare:connected('cloudflare',{endpoints:[{url:'https://nas.example.com',public:true,localPort:5000},{url:'https://files.example.org',public:true,localPort:5000}]}),
 netbird:connected('netbird',{endpoints:[{url:'http://100.90.1.2:5000/',public:false,localPort:5000},{url:'http://nas.netbird.cloud:5000/',public:false,localPort:5000}]}),
 tailscale:{...connected('tailscale',{endpoints:[{url:'http://100.101.1.2:5000/',public:false,localPort:5000}]}),state:'disconnected' as const},
}

test('each running way gives one route to the phone page, then DSM\'s own address',()=>{
 const routes=phoneRoutes(ways,{connected:true,relayState:'connected',dsm:{provider:'workers',url:'https://dsm.example.workers.dev/'}},lan,'https://192.168.1.20:5001')
 expect(routes.map(r=>[r.id,r.link])).toEqual([
  ['cloudflare','https://nas.example.com'+PAGE],
  ['workers','https://dsm.example.workers.dev'+PAGE],
  // A name before an IP address.
  ['netbird','http://nas.netbird.cloud:5000'+PAGE],
  ['local','https://192.168.1.20:5001'+PAGE],
 ])
 expect(routes.map(r=>r.publicHttps)).toEqual([true,true,false,false])
 expect(routes.find(r=>r.id==='netbird')!.notes).toEqual(['netbirdApp'])
 // A LAN address works on the same network only; a self-signed one warns.
 expect(routes.find(r=>r.id==='local')!.notes).toEqual(['sameNetwork','selfSigned'])
 expect(bestRoute(routes)!.id).toBe('cloudflare')
})

test('DSM\'s QuickConnect and DDNS addresses are routes after Connect\'s public ways, before the private networks',()=>{
 const routes=phoneRoutes({netbird:ways.netbird},undefined,lan,undefined,{quickconnect:'https://my-nas.quickconnect.to',ddns:['https://nas.ddns.example.net:5001']})
 expect(routes.map(r=>[r.id,r.link])).toEqual([
  ['quickconnect','https://my-nas.quickconnect.to'+PAGE],
  ['ddns','https://nas.ddns.example.net:5001'+PAGE],
  ['netbird','http://nas.netbird.cloud:5000'+PAGE],
  ['local','https://192.168.1.20:5001'+PAGE],
 ])
 // Public HTTPS: the menu starts on QuickConnect while Cloudflare is off, on Cloudflare once it runs.
 expect(bestRoute(routes)!.id).toBe('quickconnect')
 expect(bestRoute(phoneRoutes({cloudflare:ways.cloudflare},undefined,lan,undefined,{quickconnect:'https://my-nas.quickconnect.to'}))!.id).toBe('cloudflare')
 // DSM's own address is never a DDNS hostname already listed.
 expect(localRoute({name:'n',ports:true,addresses:[{url:'https://nas.ddns.example.net:5001',kind:'other'}]},'https://nas.ddns.example.net:5001',new Set(['nas.ddns.example.net']))).toBeUndefined()
})

test('the best route is public HTTPS first, then the others in order',()=>{
 const routes=phoneRoutes({},undefined,lan,undefined)
 expect(routes.map(r=>r.id)).toEqual(['local'])
 expect(bestRoute(routes)!.link).toBe('https://192.168.1.20:5001'+PAGE)
 expect(bestRoute(phoneRoutes({netbird:ways.netbird},undefined,lan,undefined))!.id).toBe('netbird')
 expect(bestRoute([])).toBeUndefined()
 // One need per route: the one that stops the page first.
 expect(routeNeed(routes[0])).toBe('sameNetwork')
 expect(routeNeed(routeFor('local','https://nas.ddns.example.net:5001',['selfSigned'])!)).toBe('selfSigned')
 expect(routeNeed(routeFor('cloudflare','https://nas.example.com')!)).toBeUndefined()
})

test('DSM\'s own address is the one this browser uses, unless a way serves it',()=>{
 const reply:PhoneAddNasReply={name:'n',ports:true,addresses:[{url:'https://192.168.1.20:5001',kind:'lan'},{url:'https://192.168.2.20:5001',kind:'lan'}]}
 expect(localRoute(reply,'https://192.168.2.20:5001')!.origin).toBe('https://192.168.2.20:5001')
 // Off the LAN, on an address no way serves (DDNS): that address, without the same-network note.
 const ddns:PhoneAddNasReply={name:'n',ports:true,addresses:[...reply.addresses,{url:'https://nas.ddns.example.net:5001',kind:'other'}]}
 expect(localRoute(ddns,'https://nas.ddns.example.net:5001')).toMatchObject({origin:'https://nas.ddns.example.net:5001',notes:[]})
 // On a Cloudflare hostname: that is the Cloudflare route; DSM's own is the LAN address.
 const viaCf:PhoneAddNasReply={name:'n',ports:true,addresses:[reply.addresses[0],{url:'https://nas.example.com',kind:'other'}]}
 expect(phoneRoutes({cloudflare:ways.cloudflare},undefined,viaCf,'https://nas.example.com').map(r=>r.origin)).toEqual(['https://nas.example.com','https://192.168.1.20:5001'])
 // No reply yet: this browser's own origin.
 expect(localRoute(undefined,'https://192.168.1.20:5001')!.origin).toBe('https://192.168.1.20:5001')
 expect(localRoute({name:'n',ports:false,addresses:[]},'https://192.168.1.20:5001')).toBeUndefined()
})

test('a temporary address says it changes; a private way names its app',()=>{
 expect(routeFor('cloudflare','https://a-b.trycloudflare.com')!.notes).toEqual(['temporary'])
 expect(routeFor('tailscale','http://100.101.1.2:5000/')!.notes).toEqual(['tailscaleApp'])
 expect(routeFor('cloudflare','not a url')).toBeUndefined()
})

// The tap hint plays only while the phone is on screen, and retires once the phone is tried.
class FakeObserver {
 static all:FakeObserver[]=[]
 constructor(public cb:IntersectionObserverCallback){FakeObserver.all.push(this)}
 observe(){} disconnect(){} unobserve(){} takeRecords(){return []}
 seen(ratio:number){this.cb([{isIntersecting:ratio>0,intersectionRatio:ratio} as IntersectionObserverEntry],this as unknown as IntersectionObserver)}
}

const workersOff=()=>({connected:false,relayState:'disconnected'})
const section=(root:ParentNode)=>root.querySelector<HTMLElement>('[data-home-phone]')
const cards=(root:ParentNode)=>[...root.querySelectorAll<HTMLElement>('[data-overview] [data-url-card]')]
const toggle=(root:ParentNode)=>section(root)!.querySelector<HTMLElement>('button[aria-expanded]')!

test('address cards have Open and QR code, no Phone; one Phone section under the topology, open by default',async()=>{
 const f=fixture([ways.cloudflare],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 const [first]=cards(f.root)
 expect(first.querySelector('[data-url-phone]')).toBeNull()
 expect(first.querySelector('[data-url-qr]')).toBeTruthy()
 expect(first.querySelector('[data-url-open]')).toBeTruthy()
 expect(f.root.querySelector('[data-overview] [data-phone-panel]')).toBeNull()
 const phone=section(f.root)!
 expect(phone.textContent).toContain(t('phone.section.title'))
 // No lead text: three columns, how to open it, the phone with a few short points, then adding it to the Home Screen.
 expect(phone.querySelector('[data-phone-lead]')).toBeNull()
 expect([...phone.querySelectorAll('[data-phone-column]')].map(e=>e.getAttribute('data-phone-column'))).toEqual(['scan','phone','next'])
 expect(phone.querySelector('[data-phone-column="scan"]')?.textContent).toContain(t('phone.section.scan'))
 expect(phone.querySelector('[data-phone-column="scan"] [data-phone-route-qr]')).toBeTruthy()
 expect(phone.querySelector('[data-phone-column="phone"] [data-phone-try]')?.textContent).toBe(t('phone.demo.try'))
 expect([...phone.querySelectorAll('[data-phone-column="phone"] [data-phone-point]')].map(e=>e.getAttribute('data-phone-point'))).toEqual(['layout','same','live'])
 expect(phone.querySelector('[data-phone-column="next"] [data-phone-home-screen]')).toBeTruthy()
 // iPhone and Android as equals, three steps each, then the result once; Chrome's ⋮ is an icon.
 expect([...phone.querySelectorAll('[data-phone-platform]')].map(p=>[p.getAttribute('data-phone-platform'),[...p.querySelectorAll('[data-phone-step]')].map(e=>e.getAttribute('data-phone-step'))])).toEqual([['iphone',['safari','share','add']],['android',['chrome','menu','install']]])
 const menu=phone.querySelector('[data-phone-step="menu"]')!
 expect(menu.textContent).not.toContain('{menu}')
 expect(menu.querySelector('svg[role=img]')?.getAttribute('aria-label')).toBe(t('phone.home.menuIcon'))
 expect(phone.querySelectorAll('[data-phone-result]').length).toBe(1)
 expect(phone.textContent).not.toMatch(/Android App/i)
 // The menu starts on Cloudflare, the public HTTPS way.
 const panel=phone.querySelector<HTMLElement>('[data-phone-panel]')!
 expect(panel.querySelector('[data-phone-frame] img')).toBeTruthy()
 expect(panel.querySelector('[data-phone-route-url]')?.textContent).toBe('https://nas.example.com'+PAGE)
 expect(panel.querySelector('[data-phone-route-qr]')?.getAttribute('aria-label')).toBe(t('connect.url.qrLabel').replace('{url}','https://nas.example.com'+PAGE))
 expect(panel.querySelector('[data-phone-way]')).toBeTruthy()
 expect(localStorage.getItem(PHONE_CLOSED_KEY)).toBeNull()
 // Folding it is remembered: the next visit keeps it folded until asked.
 expect(toggle(f.root).getAttribute('aria-expanded')).toBe('true')
 toggle(f.root).click();await settle()
 expect(toggle(f.root).getAttribute('aria-expanded')).toBe('false')
 expect(localStorage.getItem(PHONE_CLOSED_KEY)).toBe('1')
 f.done()
 const again=fixture([ways.cloudflare],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 expect(toggle(again.root).getAttribute('aria-expanded')).toBe('false')
 // The header's Connect your phone opens it.
 again.root.querySelector<HTMLElement>('[data-phone-open]')!.click();await settle()
 expect(toggle(again.root).getAttribute('aria-expanded')).toBe('true')
 again.done()
})

test('a private way names its app; with no remote route a Local network lane and the section on DSM\'s own address',async()=>{
 const ts=fixture([connected('tailscale',{endpoints:[{url:'http://100.101.1.2:5000/',public:false,localPort:5000}]})],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 const note=ts.root.querySelector<HTMLElement>('[data-home-phone] [data-phone-note]')!
 expect([note.dataset.phoneNote,note.textContent?.trim()]).toEqual(['tailscaleApp',t('phone.need.tailscaleApp')])
 // A way gives a route, so there is no Local network lane.
 expect(ts.root.querySelector('[data-overview]')?.textContent).not.toContain(t('phone.route.local'))
 ts.done()
 const f=fixture([],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 expect(f.root.querySelector('[data-home-first]')).toBeTruthy()
 const local=f.root.querySelector<HTMLElement>('[data-home-local]')!
 expect(local.textContent).toContain(t('phone.route.local'))
 expect(local.querySelector<HTMLInputElement>('[data-url-card] input')?.value).toBe('https://192.168.1.20:5001')
 expect(f.root.querySelector('[data-home-phone] [data-phone-route-url]')?.textContent).toBe('https://192.168.1.20:5001'+PAGE)
 const need=f.root.querySelector<HTMLElement>('[data-home-phone] [data-phone-note]')!
 expect([need.dataset.phoneNote,need.textContent?.trim()]).toEqual(['sameNetwork',t('phone.need.sameNetwork')])
 f.done()
})

test('the menu lists every way with an address, and picking one shows its code',async()=>{
 const f=fixture([ways.cloudflare,connected('tailscale',{endpoints:[{url:'http://100.101.1.2:5000/',public:false,localPort:5000}]})],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 const phone=section(f.root)!
 phone.querySelector<HTMLElement>('[data-phone-way][role=combobox], [data-phone-way] [role=combobox]')!.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));await settle()
 const options=[...document.querySelectorAll<HTMLElement>('[role="option"]')]
 expect(options.map(o=>o.textContent?.trim())).toEqual(['Cloudflare · nas.example.com',`${t('ux.method.tailscale')} · 100.101.1.2:5000`,`${t('phone.route.local')} · 192.168.1.20:5001`])
 options[1].dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));await settle()
 expect(phone.querySelector('[data-phone-route-url]')?.textContent).toBe('http://100.101.1.2:5000'+PAGE)
 f.done()
})

test('the phone demo shows its tap hint once on screen, and not after the phone was tried',async()=>{
 FakeObserver.all=[]
 vi.stubGlobal('IntersectionObserver',FakeObserver)
 const f=fixture([ways.cloudflare],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 const frame=section(f.root)!.querySelector<HTMLElement>('[data-phone-frame]')!
 vi.useFakeTimers()
 try{
  // Mounted but not seen (folded or scrolled away): nothing plays.
  vi.advanceTimersByTime(2000);await settle()
  expect(frame.querySelector('[data-phone-hint]')).toBeNull()
  expect(FakeObserver.all.length).toBe(1)
  // Half the phone (its tab bar cut off) is not enough.
  FakeObserver.all[0].seen(.5)
  vi.advanceTimersByTime(2000);await settle()
  expect(frame.querySelector('[data-phone-hint]')).toBeNull()
  FakeObserver.all[0].seen(1)
  vi.advanceTimersByTime(1000);await settle()
  expect(frame.querySelector('[data-phone-hint]')).toBeTruthy()
  // The Apps tab leads on to an App; any other tap retires the hint for good.
  frame.querySelector<HTMLElement>('[data-phone-spot="apps"]')!.click();await settle()
  vi.advanceTimersByTime(800);await settle()
  expect(frame.querySelector('[data-phone-hint]')).toBeTruthy()
  frame.querySelector<HTMLElement>('[data-phone-spot="FileStation"]')!.click();await settle()
  vi.advanceTimersByTime(10000);await settle()
  expect(frame.querySelector('[data-phone-hint]')).toBeNull()
  expect(localStorage.getItem('synopkgconnect.phoneTried')).toBe('1')
 }finally{vi.useRealTimers()}
 f.done()
 const again=fixture([ways.cloudflare],{'phone.addnas':()=>lan,'apps.workersstatus':workersOff})
 await settle(40)
 // Tried before: no observer, no hint.
 expect(FakeObserver.all.length).toBe(1)
 expect(again.root.querySelector('[data-phone-hint]')).toBeNull()
 again.done()
 vi.unstubAllGlobals()
})
