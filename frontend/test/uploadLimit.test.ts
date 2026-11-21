// @vitest-environment jsdom
// Cloudflare's upload limits: one line from the catalog's marks, 100 MB per
// file on the own domain and Free link (upload-100mb), 100 seconds per upload
// on the temporary address (upload-100s). The same line shows on the
// Cloudflare page's Connection tab, Own domain, Free link and Temporary (the
// wizard's temporary step too) and in a public App approval.
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {CATALOG} from '../src/connect/advisor'
import {uploadLimit,uploadLimitText,type CloudflareWayId} from '../src/connect/uploadLimit'
import {settle} from './support'

afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

test('the own domain and Free link carry the 100 MB limit, the temporary address 100 seconds, and no other way has one',()=>{
 for(const way of ['cloudflare-domain','cloudflare-workers'] as CloudflareWayId[])expect(uploadLimit(way),way).toEqual({kind:'size',mb:100,plan:'Free/Pro'})
 expect(uploadLimit('cloudflare-quick')).toEqual({kind:'time',seconds:100})
 const marked=(mark:string)=>CATALOG.options.filter(o=>o.pricing.limits.includes(mark)).map(o=>o.id).sort()
 expect(marked('upload-100mb')).toEqual(['cloudflare-domain','cloudflare-workers'])
 expect(marked('upload-100s')).toEqual(['cloudflare-quick'])
 // The comparison table's limits name the same marks.
 expect(CATALOG.options.find(o=>o.id==='cloudflare-quick')!.limits).toContain('upload-100s')
 expect(CATALOG.options.find(o=>o.id==='cloudflare-quick')!.limits).not.toContain('upload-100mb')
 // Cloudflare's paid plans raise the size limit; the plan is named when known.
 expect(uploadLimit('cloudflare-domain','business')).toEqual({kind:'size',mb:200,plan:'Business'})
 expect(uploadLimit('cloudflare-workers','enterprise')).toEqual({kind:'size',mb:500,plan:'Enterprise'})
 expect(uploadLimitText('en','cloudflare-domain')).toBe('Uploads: large files work in a web browser; other apps up to 100 MB per file (Cloudflare Free/Pro) · Downloads: no limit')
 expect(uploadLimitText('en','cloudflare-quick')).toBe('Uploads: large files work in a web browser; in other apps each must finish within 100 seconds · Downloads: no limit')
 expect(uploadLimitText('zh-TW','cloudflare-quick')).toContain('100 秒內完成')
 expect(uploadLimitText('fr','cloudflare-domain')).toContain('100 Mo')
})

test('all nine locales say the line with its placeholders and no trailing period',()=>{
 const bundles=import.meta.glob('../../i18n/messages/*.json',{eager:true,import:'default'}) as Record<string,Record<string,string>>
 expect(Object.keys(bundles)).toHaveLength(9)
 for(const [file,m] of Object.entries(bundles)){
  for(const key of ['row','upload','uploadTime','download','detail','detailTime','label']){
   const v=m['cflimit.'+key]
   expect(v?.trim(),`${file}:${key}`).toBeTruthy()
   expect(v,`${file}:${key}`).not.toMatch(/[.。]$/)
  }
  expect(m['cflimit.upload'],file).toContain('{mb}')
  expect(m['cflimit.upload'],file).toContain('{plan}')
  expect(m['cflimit.detail'],file).toContain('413')
  expect(m['cflimit.uploadTime'],file).toContain('{s}')
  expect(m['cflimit.detailTime'],file).toContain('{s}')
  expect(m['cflimit.detailTime'],file).not.toContain('413')
  // Browser uploads of large files work on every Cloudflare way (iPhone and
  // iPad too); the tooltip says so and keeps the limit for other apps.
  for(const key of ['detail','detailTime'])for(const name of ['File Station','Synology Drive','Synology Photos','iPhone','iPad','WebDAV'])expect(m['cflimit.'+key],`${file}:${key}`).toContain(name)
  expect(m['advisor.limits.upload-100s']?.trim(),file).toBeTruthy()
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
  if(panel==='temporary'){
   expect(l!.dataset.uploadLimitSeconds).toBe('100')
   expect(l!.querySelector('[data-upload-limit-up]')?.textContent).toBe(t('cflimit.uploadTime').replace('{s}','100'))
  }else{
   expect(l!.dataset.uploadLimitMb).toBe('100')
   expect(l!.querySelector('[data-upload-limit-up]')?.textContent).toBe(t('cflimit.upload').replace('{mb}','100').replace('{plan}','Free/Pro'))
  }
  // The separator ends the upload half, so a narrow line never starts with it.
  expect(l!.querySelector('[data-upload-limit-up]')!.nextElementSibling?.hasAttribute('data-upload-limit-sep')).toBe(true)
  expect(l!.querySelector('[data-upload-limit-down]')?.textContent).toBe(t('cflimit.download'))
  // One leading info icon carries why.
  expect(l!.firstElementChild?.hasAttribute('data-upload-limit-info')).toBe(true)
 }
 expect(cf.querySelector('[data-tab-panel="apps"] [data-upload-limit]')).toBeNull()
 app.unmount();root.remove()
})

test('while the temporary address runs, the Connection tab says its time limit, beside the Free link\'s size limit when that is on',async()=>{
 for(const free of [false,true]){
  const root=document.createElement('div');document.body.append(root)
  const quick={provider:'cloudflare',state:'connected',mode:'quick',hostname:'random-words.trycloudflare.com',endpoints:[],shareUrl:''}
  const call=vi.fn(async(method:string)=>{
   if(method==='app.status')return {admin:true}
   if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[quick]}
   if(method==='apps.list')return {apps:[],private:{available:false},public:{available:true,connected:false},relay:free?{connected:true,subdomain:'example'}:{connected:false}}
   if(method==='apps.workersstatus')return free?{connected:true,subdomain:'example'}:{connected:false}
   return {}
  })
  const app=mount(root,createStandaloneContext({call,locale:'en'}));await settle()
  ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
  const lines=[...root.querySelectorAll<HTMLElement>('[data-page="cloudflare"] [data-cf-limit-status] [data-upload-limit]')]
  expect(lines.map(l=>l.dataset.uploadLimitSeconds??`${l.dataset.uploadLimitMb}MB`),String(free)).toEqual(free?['100','100MB']:['100'])
  app.unmount();root.remove()
 }
})
