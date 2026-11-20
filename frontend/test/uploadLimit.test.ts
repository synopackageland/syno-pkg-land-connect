// @vitest-environment jsdom
// Cloudflare's upload limit: one line from the catalog's upload-100mb limit,
// the same on the Cloudflare page's Connection tab, Own domain, Free link and
// Temporary (the wizard's temporary step too) and in a public App approval.
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {CATALOG} from '../src/connect/advisor'
import {uploadLimit,uploadLimitText,type CloudflareWayId} from '../src/connect/uploadLimit'
import {settle} from './support'

afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})
const WAYS:CloudflareWayId[]=['cloudflare-domain','cloudflare-workers','cloudflare-quick']

test('every way through Cloudflare carries the catalog\'s upload limit, and no other way does',()=>{
 for(const way of WAYS)expect(uploadLimit(way),way).toEqual({mb:100,plan:'Free/Pro'})
 const limited=CATALOG.options.filter(o=>o.pricing.limits.includes('upload-100mb')).map(o=>o.id).sort()
 expect(limited).toEqual([...WAYS].sort())
 // Cloudflare's paid plans raise it; the plan is named when known.
 expect(uploadLimit('cloudflare-domain','business')).toEqual({mb:200,plan:'Business'})
 expect(uploadLimit('cloudflare-workers','enterprise')).toEqual({mb:500,plan:'Enterprise'})
 expect(uploadLimitText('en','cloudflare-quick')).toBe('Uploads: up to 100 MB per file (Cloudflare Free/Pro) · Downloads: no limit')
 expect(uploadLimitText('fr','cloudflare-domain')).toContain('100 Mo')
})

test('all nine locales say the line with its placeholders and no trailing period',()=>{
 const bundles=import.meta.glob('../../i18n/messages/*.json',{eager:true,import:'default'}) as Record<string,Record<string,string>>
 expect(Object.keys(bundles)).toHaveLength(9)
 for(const [file,m] of Object.entries(bundles)){
  for(const key of ['row','upload','download','detail','label']){
   const v=m['cflimit.'+key]
   expect(v?.trim(),`${file}:${key}`).toBeTruthy()
   expect(v,`${file}:${key}`).not.toMatch(/[.。]$/)
  }
  expect(m['cflimit.upload'],file).toContain('{mb}')
  expect(m['cflimit.upload'],file).toContain('{plan}')
  expect(m['cflimit.detail'],file).toContain('413')
 }
})

const t=(key:string)=>translate('en',key)
const ownDomain={provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}],shareUrl:''}
test('the Cloudflare page shows the line on Connection, Own domain, Free link and Temporary',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[ownDomain]}
  if(method==='apps.list')return {apps:[],private:{available:false},public:{available:true,connected:true,zone:'example.com'},relay:{connected:true,subdomain:'example'}}
  if(method==='apps.workersstatus')return {connected:true,subdomain:'example'}
  return {}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}));await settle()
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
 const cf=root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 const line=(panel:string)=>cf.querySelector<HTMLElement>(`[data-tab-panel="${panel}"] [data-upload-limit]`)
 for(const panel of ['connection','domain','free','temporary']){
  const l=line(panel)
  expect(l,panel).toBeTruthy()
  expect(l!.dataset.uploadLimitMb).toBe('100')
  expect(l!.querySelector('[data-upload-limit-up]')?.textContent).toBe(t('cflimit.upload').replace('{mb}','100').replace('{plan}','Free/Pro'))
  expect(l!.querySelector('[data-upload-limit-down]')?.textContent).toBe(t('cflimit.download'))
  // One leading info icon carries why.
  expect(l!.firstElementChild?.hasAttribute('data-upload-limit-info')).toBe(true)
 }
 expect(cf.querySelector('[data-tab-panel="apps"] [data-upload-limit]')).toBeNull()
 app.unmount();root.remove()
})
