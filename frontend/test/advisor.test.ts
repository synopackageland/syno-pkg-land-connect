// @vitest-environment jsdom
// The catalog behind the comparison table and the method pages: the one
// catalog ctl recommend uses, its words in every locale.
import {afterEach,expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {translate} from '../src/i18n'
import {CATALOG,catalogKeys,comparisonRows} from '../src/connect/advisor'
import catalog from '../../backend/internal/advisor/catalog.json'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

const LOCALES=['en','zh-TW','zh-CN','ja','ko','de','fr','it','es']
// Product names, and a few short words, read the same in some languages.
const NAMES=new Set(['Tailscale','NetBird','Cloudflare Registrar','Synology Drive Client (6690)','No','Public','HTTPS'])

test('the frontend reads the very catalog file the backend embeds',()=>{
 const embedded=JSON.parse(readFileSync('../backend/internal/advisor/catalog.json','utf8'))
 expect(CATALOG).toEqual(embedded)
})

test.each(LOCALES)('every catalog value has words in %s without a trailing period',locale=>{
 for(const key of [...catalogKeys(),'advisor.recommended','advisor.reason.ipv6_no_global_address','advisor.reason.ipv6_unknown','advisor.reason.already_running','advisor.yes','advisor.no'])
  {
   const text=translate(locale,key)
   expect(text,`${locale} ${key}`).not.toBe(key)
   if(locale!=='en'&&!NAMES.has(translate('en',key)))expect(text,`${locale} ${key}`).not.toBe(translate('en',key))
   expect(text,`${locale} ${key}`).not.toMatch(/[.。]$/)
  }
})

test('direct IPv6 is in the comparison marked No protection',async()=>{
 const f=fixture();await settle();const home=f.page('status')
 const table=home.querySelector<HTMLElement>('[data-choice-notes]')!
 expect(table.textContent).toContain(t('advisor.column.protection'))
 const row=comparisonRows('en',t).find(r=>r.id==='ipv6')!
 expect(row.protection).toBe(t('guide.protection.none'))
 f.done()
})

test('the Cloudflare methods are named by what they give, with no number that reads like a card\'s',()=>{
 for(const locale of LOCALES)for(const id of ['cloudflare-quick','cloudflare-domain']){
  const name=translate(locale,`advisor.option.${id}`)
  expect(name,`${locale} ${id}`).toMatch(/^Cloudflare /)
  expect(name,`${locale} ${id}`).not.toMatch(/\d/)
 }
})

test('both Cloudflare options say Cloudflare provides a trusted HTTPS certificate with no DSM certificate or open port',()=>{
 for(const id of ['cloudflare-quick','cloudflare-domain'])expect(CATALOG.options.find(o=>o.id===id)!.https).toBe('cloudflare-certificate')
 for(const locale of LOCALES)expect(translate(locale,'advisor.https.cloudflare-certificate'),locale).toMatch(/Cloudflare/)
 const en=translate('en','advisor.https.cloudflare-certificate')
 for(const words of ['trusted','certificate','port'])expect(en).toContain(words)
 for(const locale of LOCALES){
  const note=translate(locale,'advisor.httpsNote')
  expect(note,locale).not.toBe(locale==='en'?'advisor.httpsNote':translate('en','advisor.httpsNote'))
  expect(note,locale).not.toMatch(/[.。]$/)
 }
 expect(translate('en','advisor.httpsNote')).toMatch(/browser to Cloudflare.*tunnel to the NAS.*decrypts/)
})

test('the comparison and the Cloudflare page say how far Cloudflare\'s HTTPS encrypts',async()=>{
 const f=fixture([connected('cloudflare',{mode:'domain',hostname:'nas.example.com'})]);await settle()
 expect(f.page('status').querySelector('[data-advisor-https-note]')?.textContent).toBe(t('advisor.httpsNote'))
 // On the Cloudflare page it is an InfoTip on the Own domain tab.
 expect(f.page('cloudflare').querySelector('[data-tab-panel="domain"] [data-cf-https]')).toBeTruthy()
 f.done()
})

test('the comparison lists every catalog option, QuickConnect included, with its plain-language security',async()=>{
 const f=fixture([connected('tailscale')]);await settle()
 const table=f.page('status').querySelector<HTMLElement>('[data-choice-notes]')!
 for(const option of CATALOG.options){
  expect(table.textContent).toContain(t(`advisor.option.${option.id}`))
  expect(table.textContent).toContain(t(`advisor.who.${option.who}`))
  expect(table.textContent).toContain(t(`advisor.via.${option.via}`))
  expect(table.textContent).toContain(t(`advisor.https.${option.https}`))
  for(const pair of option.pairWith)expect(table.textContent).toContain(t(`advisor.pairWith.${pair}`))
 }
 for(const column of ['option','fit','reach','cost','accounts','client','difficulty','who','via','https','public','pairWith'])expect(table.textContent).toContain(t(`advisor.column.${column}`))
 f.done()
})

test('a running Tailscale or NetBird lane says what to do on the other devices and links the app',async()=>{
 const f=fixture([connected('tailscale'),connected('netbird',{netbirdIP:'100.64.0.9'}),connected('cloudflare',{shareUrl:'https://nas.example.com'})]);await settle()
 const home=f.page('status')
 for(const id of ['tailscale','netbird']){
  const next=home.querySelector<HTMLElement>(`[data-lane-id="${id}"] [data-lane-next]`)!
  expect(next.textContent).toContain(t(`overview.next.${id}`))
  expect(next.querySelector('a')?.getAttribute('href')).toBe(CATALOG.options.find(o=>o.id===id)!.links!.download)
 }
 expect(home.querySelector('[data-lane-id="cloudflare"] [data-lane-next]')).toBeNull()
 f.done()
})

test('the own-domain start links Cloudflare Registrar and lets the name be typed before consent',async()=>{
 const f=fixture();await settle();await f.nav('Cloudflare')
 const start=f.page('cloudflare').querySelector<HTMLElement>('[data-cf-domain-tab]')!
 const registrar=start.querySelector<HTMLElement>('[data-cf-no-domain]')!
 expect(registrar.textContent).toContain(t('cloudflare.noDomain'))
 expect(registrar.querySelector('a')?.getAttribute('href')).toBe(CATALOG.options.find(o=>o.id==='cloudflare-domain')!.links!.registrar)
 // The name field before consent; its default is the backend's suggestedName, absent here.
 const name=start.querySelector<HTMLInputElement>('[data-connect-form] input:not([type=checkbox])')!
 expect(name.disabled).toBe(false)
 expect(start.querySelector<HTMLButtonElement>('[data-cf-domain]')!.disabled).toBe(true)
 f.done()
})

test('without an IPv6 address the checklist offers the methods that need none',async()=>{
 const f=fixture([],{'ipv6.status':()=>({state:'unavailable',reason:'no_global_address',addresses:[],endpoints:[]})});await settle()
 const fallback=f.page('ipv6').querySelector<HTMLElement>('[data-ipv6-fallback]')!
 expect(fallback.textContent).toContain(t('connect.ipv6.check.address.fallback'))
 fallback.querySelector<HTMLElement>('[data-ipv6-fallback-go="tailscale"]')!.click();await settle()
 expect(f.page('tailscale').style.display).not.toBe('none')
 f.done()
})

test('TCP services such as Drive Client go over a private network, never Cloudflare',()=>{
 expect(CATALOG.options.filter(o=>o.provider==='cloudflare').map(o=>o.id)).toEqual(['cloudflare-quick','cloudflare-domain'])
 for(const p of catalog.tcpServices)expect(p.providers,p.id).toEqual(['tailscale','netbird'])
})
