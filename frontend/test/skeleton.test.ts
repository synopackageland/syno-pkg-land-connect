// @vitest-environment jsdom
// The shared provider page skeleton: a Connection tab first with the status
// card (state word, path picture, address, Turn off) and the connect form
// while the provider is off, then the page's own tabs with Advanced last once
// it is connected.
// Cloudflare keeps its tabs while off (its Free link and Apps need no
// tunnel) and embeds the connect form in Own domain; NetBird keeps its
// Self-host server tab in every state and embeds its form in Join. No Help tab:
// explanations are InfoTips and one "Learn more" link.
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,connected} from './providerFixture'
import {statusWord,pathLinks,pathWay} from '../src/connect/providerStatus'
import {LEARN_MORE} from '../src/connect/pageTabs'
import type {ConnectionStatus} from '../src/context'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

const tabs=(page:HTMLElement)=>[...page.querySelectorAll<HTMLElement>('[data-page-tabs] [data-ui-tab-value]')].map(b=>b.dataset.uiTabValue)
// SectionTabs keep-mounted hides an inactive panel's wrapper, not the panel itself.
const shown=(el:HTMLElement|null)=>!!el&&el.closest<HTMLElement>('.ui-tab-panel')?.style.display!=='none'

test('the tailscale page keeps its tabs while off: Join holds the connect form',async()=>{
 const f=fixture();await settle()
 const page=f.page('tailscale')
 expect(page.querySelector('[data-connection-badge]')?.textContent).toBe(translate('en','ux.state.disconnected'))
 expect(page.querySelector('[data-status-card] [data-provider-path]')).toBeTruthy()
 expect(tabs(page)).toEqual(['connection','join'])
 expect(page.querySelector('[data-tab-panel="join"] [data-connect-section] [data-connect-form="tailscale"]')).toBeTruthy()
 expect(shown(page.querySelector('[data-tab-panel="join"]'))).toBe(true)
 expect(page.querySelector('[data-learn-more]')?.getAttribute('href')).toBe(LEARN_MORE.tailscale)
 f.done()
})

test('the netbird page keeps its tabs while off: Join holds the connect form, then Self-host server',async()=>{
 const f=fixture();await settle()
 const page=f.page('netbird')
 expect(page.querySelector('[data-connection-badge]')?.textContent).toBe(translate('en','ux.state.disconnected'))
 expect(tabs(page)).toEqual(['connection','join','server'])
 expect(page.querySelector('[data-tab-panel="join"] [data-connect-section] [data-connect-form="netbird"]')).toBeTruthy()
 expect(shown(page.querySelector('[data-tab-panel="join"]'))).toBe(true)
 expect(page.querySelector('[data-learn-more]')?.getAttribute('href')).toBe(LEARN_MORE.netbird)
 f.done()
})

test('the cloudflare page keeps its tabs while off and embeds the connect form in Own domain',async()=>{
 const f=fixture();await settle()
 const page=f.page('cloudflare')
 expect(page.querySelector('[data-connection-badge]')?.textContent).toBe(translate('en','ux.state.disconnected'))
 // Cloudflare draws its path per way on each tab, so the card has none.
 expect(page.querySelector('[data-status-card] [data-provider-path]')).toBeNull()
 expect(tabs(page)).toEqual(['connection','domain','free','temporary','apps'])
 expect(page.querySelector('[data-tab-panel="domain"] [data-connect-form="cloudflare"]')).toBeTruthy()
 expect(page.querySelector('[data-tab-panel="help"]')).toBeNull()
 expect(page.querySelector('[data-learn-more]')?.getAttribute('href')).toBe(LEARN_MORE.cloudflare)
 f.done()
})

test.each([
 // Cloudflare picks its landing tab once it reads the Free link; until then, Connection.
 ['cloudflare',{mode:'domain',hostname:'nas.example.com'},['connection','domain','free','temporary','apps'],'connection'],
 ['tailscale',{},['connection','apps'],'connection'],
] as const)('a connected %s page opens on its landing tab and puts Advanced last',async(name,extra,expected,landing)=>{
 const f=fixture([connected(name,extra as Partial<ConnectionStatus>)]);await settle()
 const page=f.page(name)
 expect(page.querySelector('[data-connection-badge]')?.textContent).toBe(translate('en','ux.state.connected'))
 expect(tabs(page)).toEqual(expected)
 expect(shown(page.querySelector(`[data-tab-panel="${landing}"]`))).toBe(true)
 expect(page.querySelector('[data-connect-section]')).toBeNull()
 f.done()
})

test('a failed NetBird opens on Connection, where the failure is explained',async()=>{
 const f=fixture([{provider:'netbird',state:'failed',code:'netbird_failed',simulation:false,shareUrl:''} as ConnectionStatus]);await settle()
 const page=f.page('netbird')
 expect(tabs(page)).toEqual(['connection','join','server'])
 expect(shown(page.querySelector('[data-tab-panel="connection"]'))).toBe(true)
 expect(shown(page.querySelector('[data-tab-panel="join"]'))).toBe(false)
 f.done()
})

test('connected NetBird has nothing to set on the NAS, so the page is its status card, its Apps and the server',async()=>{
 const f=fixture([connected('netbird',{netbirdIP:'100.64.0.9'})]);await settle()
 const page=f.page('netbird')
 expect(tabs(page)).toEqual(['connection','apps','server'])
 expect(page.querySelector('[data-connect-section]')).toBeNull()
 expect(page.querySelector('[data-stop]')?.textContent?.trim()).toBe(translate('en','connect.disable'))
 f.done()
})

test('a risk sentence links the provider documentation with Learn more',async()=>{
 const f=fixture();await settle()
 expect(f.page('ipv6').querySelector('[data-ipv6-risk] [data-risk-more]')?.getAttribute('href')).toBe(LEARN_MORE.ipv6)
 f.done()
})

test('a draft survives switching tabs, since every panel stays mounted',async()=>{
 const f=fixture([{provider:'cloudflare',state:'disconnected',simulation:false,shareUrl:''} as ConnectionStatus]);await settle()
 const page=f.page('cloudflare')
 page.querySelector<HTMLButtonElement>('[data-ui-tab-value="domain"]')!.click();await settle()
 const host=()=>page.querySelector<HTMLInputElement>('[data-cf-domain-tab] [data-connect-form] input:not([type=checkbox])')!
 host().value='draft';host().dispatchEvent(new Event('input',{bubbles:true}));await settle()
 page.querySelector<HTMLButtonElement>('[data-ui-tab-value="apps"]')!.click();await settle()
 page.querySelector<HTMLButtonElement>('[data-ui-tab-value="domain"]')!.click();await settle()
 expect(host().value).toBe('draft')
 f.done()
})

const status=(state:ConnectionStatus['state'],extra:Partial<ConnectionStatus>={})=>({provider:'tailscale',state,simulation:false,shareUrl:'',...extra}) as ConnectionStatus
test('every connection state reads as one of the shared status words',()=>{
 expect(statusWord(undefined,false)).toBe('off')
 expect(statusWord(status('disconnected'),false)).toBe('off')
 expect(statusWord(status('connecting'),false)).toBe('connecting')
 expect(statusWord(status('connected'),false)).toBe('on')
 expect(statusWord(status('connected',{apply:{phase:'applying'}}),false)).toBe('connecting')
 expect(statusWord(status('connected',{apply:{phase:'rolled-back'}}),false)).toBe('attention')
 for(const state of ['needs-login','needs-machine-auth'] as const)expect(statusWord(status(state),false),state).toBe('attention')
 expect(statusWord(status('failed'),false)).toBe('error')
 expect(statusWord(status('connected'),true)).toBe('unknown')
})

test('the path picture is idle before set up, moves while connecting and follows the Overview lane once running',()=>{
 expect(pathLinks('tailscale',undefined,undefined,false)).toEqual(['idle','idle'])
 expect(pathWay('ipv6',undefined)).toBe('ipv6')
 expect(pathWay('cloudflare',status('connected',{provider:'cloudflare',mode:'quick'}))).toBe('cloudflare-quick')
 expect(pathLinks('tailscale',status('connecting'),undefined,false)).toEqual(['pending','pending'])
 expect(pathLinks('cloudflare',status('connected',{provider:'cloudflare'}),{provider:'cloudflare',state:'ok',externalVerified:true},false)).toEqual(['active','active'])
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the skeleton is translated in %s',locale=>{
 for(const key of ['skel.state.unknown','skel.apply','skel.cf.https','cfpage.tab.temporary','tab.advanced','tab.checklist','ux.learnMore']){
  const text=translate(locale,key)
  expect(text,`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
  expect(text).not.toMatch(/[.。．]$/)
 }
})

test('a delayed Cloudflare landing read never overrides the selected tab',async()=>{
 let finish!:(value:unknown)=>void
 const pending=new Promise(resolve=>{finish=resolve})
 const f=fixture([],{'apps.workersstatus':()=>pending});await settle();await f.nav('Cloudflare')
 const page=f.page('cloudflare')
 page.querySelector<HTMLButtonElement>('[data-ui-tab-value="temporary"]')!.click();await settle()
 finish({connected:false});await settle()
 expect(shown(page.querySelector('[data-tab-panel="temporary"]'))).toBe(true)
 f.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(shown(page.querySelector('[data-tab-panel="temporary"]'))).toBe(true)
 f.done()
})

test('Cloudflare connection state refreshes preserve the selected tab',async()=>{
 const f=fixture([connected('cloudflare',{mode:'quick'})]);await settle();await f.nav('Cloudflare')
 const page=f.page('cloudflare')
 page.querySelector<HTMLButtonElement>('[data-ui-tab-value="temporary"]')!.click();await settle()
 f.setStatus([{...connected('cloudflare'),state:'connecting',mode:'quick'}])
 f.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 f.setStatus([connected('cloudflare',{mode:'quick'})])
 f.root.querySelector<HTMLButtonElement>('[data-refresh]')!.click();await settle()
 expect(shown(page.querySelector('[data-tab-panel="temporary"]'))).toBe(true)
 f.done()
})
