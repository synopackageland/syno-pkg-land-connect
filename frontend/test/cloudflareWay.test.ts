// @vitest-environment jsdom
// The Cloudflare page's Own domain and Free link tabs on one layout: the same
// rows in the same order, DSM counted with the Apps of the way that serves it
// (one way at a time), Add DSM for a Free link that is on without it, one
// status card for both ways (address, check, no Turn off of its own), and the
// Overview and Settings on the same page surface as the other pages.
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {freeLinkAddress,type WorkersStatus} from '../src/connect/freeLink'
import {dsmWay,hostOf,wayApps} from '../src/connect/cloudflareWay'
import type {AppsView} from '../src/connect/appUrls'
import {settle} from './support'

afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

const t=(key:string)=>translate('en',key)
const view:AppsView={
 private:{available:true,host:'nas.tailnet.ts.net'},
 public:{available:true,connected:true,zone:'example.com'},
 relay:{connected:true,subdomain:'example'},
 apps:[
  {package:'SynoPkgDomain',name:'Domain App',port:28003,publicCapable:true,protection:{},public:{state:'active',url:'https://domain.example.com/'}},
  {package:'SynoPkgRelay',name:'Relay App',port:28004,publicCapable:true,protection:{},relay:{state:'active',url:'https://relay.example.workers.dev/'}},
  {package:'SynoPkgOff',name:'Off App',port:28005,publicCapable:true,protection:{}},
 ],
}
const ownDomain={provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}],shareUrl:''}

function fixture(workers:()=>WorkersStatus,connections:unknown[]=[],handlers:Record<string,()=>unknown>={},appsView:AppsView=view){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections}
  if(method==='apps.list')return structuredClone(appsView)
  if(method==='apps.workersstatus')return workers()
  if(handlers[method])return handlers[method]!()
  return {}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}))
 return {root,call,done(){app.unmount();root.remove()}}
}
async function nav(root:HTMLElement,name:string){[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()===name)!.click();await settle()}
async function tab(root:HTMLElement,value:string){
 await nav(root,'Cloudflare')
 const cf=root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 cf.querySelector<HTMLElement>(`[data-ui-tab-value="${value}"]`)!.click();await settle()
 return cf
}
// The rows a tab shows, in order, by the layout's own markers.
const rows=(way:HTMLElement)=>[...way.querySelectorAll('[data-way-address],[data-way-apps],[data-cf-path],[data-way-change-toggle],[data-way-off]')].map(e=>[...e.attributes].find(a=>a.name.startsWith('data-way-')||a.name==='data-cf-path')!.name)

test('the Free link shows DSM\'s address when it serves DSM, else the account\'s name',()=>{
 expect(freeLinkAddress({connected:true,subdomain:'example',dsm:{provider:'workers',url:'https://nas.example.workers.dev'}})).toBe('nas.example.workers.dev')
 expect(freeLinkAddress({connected:true,subdomain:'example',dsm:{provider:'cloudflare',url:'https://nas.example.com'}})).toBe('*.example.workers.dev')
 expect(freeLinkAddress({connected:false})).toBe('')
 expect(hostOf('https://nas.example.com/path')).toBe('nas.example.com')
 expect(dsmWay({connected:true,dsm:{provider:'workers',url:''}})).toBe('workers')
 expect(dsmWay({connected:true,dsm:{provider:'cloudflare',url:''}})).toBe('cloudflare')
 expect(dsmWay({connected:true})).toBeUndefined()
 expect(wayApps(view,'relay',true)).toBe(2)
 expect(wayApps(view,'public',false)).toBe(1)
 expect(wayApps(undefined,'public',true)).toBeUndefined()
})

test('both tabs show the same rows in the same order, and DSM counts on the way that serves it',async()=>{
 const f=fixture(()=>({connected:true,subdomain:'example',dsm:{provider:'workers',url:'https://nas.example.workers.dev'}}),[ownDomain]);await settle()
 const domain=(await tab(f.root,'domain')).querySelector<HTMLElement>('[data-cf-domain-tab]')!
 const domainRows=rows(domain)
 expect(domainRows).toEqual(['data-way-address','data-way-apps','data-cf-path','data-way-change-toggle','data-way-off'])
 expect(domain.querySelector('[data-way-address]')?.textContent).toBe('nas.example.com')
 // DSM is on the Free link, so the own domain counts its one App alone.
 expect(domain.querySelector('[data-way-apps]')?.textContent).toBe('1')
 const free=(await tab(f.root,'free')).querySelector<HTMLElement>('[data-free-link]')!
 expect(rows(free)).toEqual(domainRows)
 expect(free.querySelector('[data-way-address]')?.textContent).toBe('nas.example.workers.dev')
 expect(free.querySelector('[data-way-apps]')?.textContent).toBe('2')
 expect(free.querySelector('[data-free-add-dsm]')).toBeNull()
 // The status card shows the tunnel's address while the tunnel runs.
 expect(f.root.querySelector('[data-page="cloudflare"] [data-cf-free-status]')).toBeNull()
 const apps=await tab(f.root,'apps')
 expect(apps.textContent).toContain('DSM')
 expect(apps.textContent).toContain('nas.example.workers.dev')
 f.done()
})

test('the status card is the same for the Free link alone: the address with copy, Open and QR code, the check, no Turn off',async()=>{
 let dsm:WorkersStatus['dsm']={provider:'workers',url:'https://nas.example.workers.dev'}
 const f=fixture(()=>({connected:true,subdomain:'example',...(dsm?{dsm}:{})}),[],{'connection.check':()=>({provider:'workers',state:'ok',externalVerified:true,checkedAt:'2026-01-01T08:00:00Z'})});await settle()
 const cf=await tab(f.root,'free')
 const card=cf.querySelector<HTMLElement>('[data-status-card]')!
 const row=card.querySelector<HTMLElement>('[data-cf-free-status]')!
 expect(row.textContent).toContain(t('ux.row.address'))
 expect(row.querySelector<HTMLInputElement>('input')?.value).toBe('https://nas.example.workers.dev')
 expect(row.textContent).toContain('Open');expect(row.textContent).toContain('QR code')
 expect(card.querySelector('[data-connection-check]')?.getAttribute('data-check-state')).toBe('ok')
 expect(f.call).toHaveBeenCalledWith('connection.check',{provider:'workers'})
 expect(card.querySelector('[data-stop]')).toBeNull()
 expect(cf.querySelector('[data-free-link] [data-way-off]')).toBeTruthy()
 f.done()
 // On without DSM there is no address to open or check yet, only the account's name.
 dsm=undefined
 const g=fixture(()=>({connected:true,subdomain:'example'}));await settle()
 const card2=(await tab(g.root,'free')).querySelector<HTMLElement>('[data-status-card]')!
 expect(card2.querySelector('[data-cf-free-status]')?.textContent).toContain('*.example.workers.dev')
 expect(card2.querySelector('[data-connection-check]')).toBeNull()
 g.done()
})

test('a new Free link address reads Setting up, never Failed, and turns Working on the page\'s own re-check',async()=>{
 vi.useFakeTimers()
 try{
  const results=[{provider:'workers',state:'starting',code:'cloudflare_dns_pending',attempts:1},{provider:'workers',state:'ok',externalVerified:true,checkedAt:'2026-01-01T08:00:00Z'}]
  const f=fixture(()=>({connected:true,subdomain:'example',dsm:{provider:'workers',url:'https://nas.example.workers.dev'}}),[],{'connection.check':()=>results.length>1?results.shift():results[0]})
  await settle()
  const card=(await tab(f.root,'free')).querySelector<HTMLElement>('[data-status-card]')!
  const check=()=>card.querySelector<HTMLElement>('[data-connection-check]')!
  expect(check().dataset.checkState).toBe('starting')
  expect(check().querySelector('[data-setting-up]')).toBeTruthy()
  expect(check().textContent).not.toContain(t('connect.check.failed'))
  await vi.advanceTimersByTimeAsync(5000);await settle()
  expect(check().dataset.checkState).toBe('ok')
  expect(check().textContent).toContain(t('connect.check.ok'))
  f.done()
 }finally{vi.useRealTimers()}
})

test('the own domain turns off from its tab only, and offers no tunnel token to paste',async()=>{
 const f=fixture(()=>({connected:false}),[ownDomain]);await settle()
 const cf=await tab(f.root,'domain')
 expect(cf.querySelector('[data-status-card] [data-stop]')).toBeNull()
 expect(cf.querySelector('[data-cf-domain-tab] [data-way-off]')).toBeTruthy()
 f.done()
 const g=fixture(()=>({connected:false}));await settle()
 const off=(await tab(g.root,'domain')).querySelector<HTMLElement>('[data-cf-domain-tab]')!
 expect(off.querySelector('input[type=password],[data-cf-token]')).toBeNull()
 expect([...off.querySelectorAll('[data-use-more]')].some(b=>b.textContent?.trim()===t('cloudflare.token'))).toBe(false)
 g.done()
})

test('the own domain offers one-click cleanup only for reported orphaned App addresses',async()=>{
 const appsView:AppsView={...view,public:{...view.public,orphanedRoutes:2}}
 const f=fixture(()=>({connected:false}),[ownDomain],{
  'apps.cleanuporphans':()=>{appsView.public.orphanedRoutes=0;return {removed:2}},
 },appsView);await settle()
 const domain=(await tab(f.root,'domain')).querySelector<HTMLElement>('[data-cf-domain-tab]')!
 expect(domain.querySelector('[data-cf-orphaned-routes]')?.textContent).toContain('2')
 domain.querySelector<HTMLElement>('[data-cf-cleanup-orphans]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.cleanuporphans',{})
 expect(domain.querySelector('[data-cf-orphaned-routes]')).toBeNull()
 f.done()
})

test('a Free link without DSM offers Add DSM in one click',async()=>{
 let dsm:WorkersStatus['dsm']
 const f=fixture(()=>({connected:true,subdomain:'example',...(dsm?{dsm}:{})}),[],{'apps.workerspublishdsm':()=>{dsm={provider:'workers',url:'https://nas.example.workers.dev'};return {connected:true,subdomain:'example',dsm}}});await settle()
 const cf=await tab(f.root,'free')
 const free=cf.querySelector<HTMLElement>('[data-free-link]')!
 expect(free.querySelector('[data-way-address]')?.textContent).toBe('*.example.workers.dev')
 expect(free.querySelector('[data-way-apps]')?.textContent).toBe('1')
 free.querySelector<HTMLElement>('[data-free-add-dsm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.workerspublishdsm',{})
 expect(free.querySelector('[data-free-add-dsm]')).toBeNull()
 expect(free.querySelector('[data-way-address]')?.textContent).toBe('nas.example.workers.dev')
 expect(free.querySelector('[data-way-apps]')?.textContent).toBe('2')
 f.done()
})

test('DSM on the own domain stays there: the Free link names it and offers no Add DSM',async()=>{
 const f=fixture(()=>({connected:true,subdomain:'example',dsm:{provider:'cloudflare',url:'https://nas.example.com'}}),[ownDomain]);await settle()
 const domain=(await tab(f.root,'domain')).querySelector<HTMLElement>('[data-cf-domain-tab]')!
 expect(domain.querySelector('[data-way-apps]')?.textContent).toBe('2')
 const free=(await tab(f.root,'free')).querySelector<HTMLElement>('[data-free-link]')!
 expect(free.querySelector('[data-free-add-dsm]')).toBeNull()
 expect(free.querySelector('[data-free-dsm-elsewhere]')?.textContent).toContain(t('cfpage.free.dsmElsewhere').replace('{host}','nas.example.com'))
 expect(free.querySelector('[data-way-apps]')?.textContent).toBe('1')
 f.done()
})

test('the Overview and Settings sit on the same page surface as the other pages',async()=>{
 const f=fixture(()=>({connected:false}));await settle()
 const surface=()=>f.root.querySelector('[data-page-surface]')?.getAttribute('data-page-surface')
 const overview=surface()
 expect(overview).toBeTruthy()
 for(const name of ['Cloudflare','App URLs','Tailscale','Settings']){await nav(f.root,name);expect(surface(),name).toBe(overview)}
 f.done()
})

test('the new Cloudflare tab strings are in every locale without a trailing period',()=>{
 for(const locale of ['en','de','es','fr','it','ja','ko','zh-CN','zh-TW'])for(const key of ['cfpage.free.addDsm','cfpage.free.dsmAdded','cfpage.free.dsmFailed','cfpage.free.dsmElsewhere','cfpage.domain.turnOff','cloudflare.orphans','cloudflare.cleanupOrphans','cloudflare.orphansDone','cloudflare.orphansFailed','apps.code.apps_dsm_public_elsewhere']){
  const text=translate(locale,key)
  expect(text,`${locale} ${key}`).not.toBe(key)
  expect(text,`${locale} ${key}`).not.toMatch(/[.。]$/)
 }
})
