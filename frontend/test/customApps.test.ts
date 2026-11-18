// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext,readDsmPorts,type Context} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {rowStatus,type AppsView} from '../src/connect/appUrls'
import {candidateName,customForm,customRequest,needsRisk,serviceName,urlError,type Candidate} from '../src/connect/customApps'
import {settle} from './support'

const c=(key:string)=>translate('en','apps.custom.'+key)
const plex={package:'custom.plex',name:'Plex',port:32400,publicCapable:true,protection:{},custom:{url:'http://127.0.0.1:32400',appSignIn:false,insecureTls:false,reachable:true}}
const view=():AppsView=>({
 private:{available:true,host:'nas.tailnet.ts.net'},
 public:{available:true,connected:true,zone:'example.com'},
 apps:[{package:'SynoPkgOn',name:'On App',port:28002,publicCapable:true,protection:{}},plex],
})
const serving={provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com'}

function fixture(handlers:Record<string,(params:Record<string,unknown>)=>unknown>={},files?:Context['files']){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[serving]}
  if(method==='apps.list')return view()
  if(handlers[method])return handlers[method]!(params)
  return {}
 })
 const ctx=createStandaloneContext({call,locale:'en'});ctx.files=files
 const app=mount(root,ctx)
 return {root,call,done(){app.unmount();root.remove()}}
}
async function open(root:HTMLElement){[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='App URLs')!.click();await settle()}
const input=(sel:string)=>document.querySelector<HTMLInputElement>(`[data-custom-dialog] ${sel} input`)!
function type(el:HTMLInputElement,value:string){el.value=value;el.dispatchEvent(new Event('input'))}
afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks();vi.unstubAllGlobals()})

test('urlError takes any http or https origin and refuses the rest early',()=>{
 for(const ok of ['http://127.0.0.1:32400','http://192.168.1.20:8080','https://nas.example.com','http://[::1]:8080/'])expect(urlError(ok),ok).toBe('')
 expect(urlError('')).toBe('custom_url_invalid')
 expect(urlError('127.0.0.1:8080')).toBe('custom_url_invalid')
 expect(urlError('ftp://127.0.0.1')).toBe('custom_url_scheme')
 expect(urlError('http://127.0.0.1:8080/web')).toBe('')
 expect(urlError('http://127.0.0.1:8080/?a=1')).toBe('custom_url_path')
 expect(urlError('http://user:pw@127.0.0.1')).toBe('custom_url_invalid')
 for(const bad of ['http://169.254.169.254','http://[fe80::1]','http://0.0.0.0:80','http://[fd00:ec2::254]','http://224.0.0.1'])expect(urlError(bad),bad).toBe('custom_target_refused')
})

test('turning DSM sign-in off needs the risk confirmation once',()=>{
 const form={...customForm(),name:' Plex ',url:'http://127.0.0.1:32400 '}
 expect(form.appSignIn).toBe(false)
 expect(needsRisk(form)).toBe(false)
 expect(customRequest(form)).toEqual({name:'Plex',url:'http://127.0.0.1:32400',appSignIn:false,insecureTls:false})
 form.appSignIn=true
 expect(needsRisk(form)).toBe(true)
 expect(customRequest(form)).not.toHaveProperty('acceptRisk')
 form.acceptRisk=true;form.insecureTls=true
 expect(customRequest(form,'custom.plex')).toEqual({id:'custom.plex',name:'Plex',url:'http://127.0.0.1:32400',appSignIn:true,acceptRisk:true,insecureTls:false})
 const saved={...plex,custom:{...plex.custom,appSignIn:true}}
 expect(customForm(saved).acceptRisk).toBe(true)
 expect(needsRisk(customForm(saved),saved)).toBe(false)
})

test('an unreachable custom app is never waiting',()=>{
 const row={...plex,waiting:true,custom:{...plex.custom,reachable:false}}
 expect(rowStatus(row,'public')).not.toBe('waiting')
})

test('the Apps list marks custom apps and adds one through the dialog',async()=>{
 const added=vi.fn(()=>({id:'custom.jellyfin'}))
 const f=fixture({'apps.customcandidates':()=>({candidates:[{name:'Jellyfin',url:'http://127.0.0.1:8096',source:'package'}]}),'apps.customadd':added})
 await settle();await open(f.root)
 expect(f.root.querySelector('[data-app-custom="custom.plex"]')?.textContent).toContain(c('tag'))
 expect(f.root.querySelector('[data-app-custom="SynoPkgOn"]')).toBeNull()
 f.root.querySelector<HTMLElement>('[data-apps-add]')!.click();await settle()
 expect(document.querySelector('[data-custom-dialog]')).toBeTruthy()
 expect(document.querySelector('[data-custom-path]')?.getAttribute('data-gate')).toBe('on')
 type(input('[data-custom-url]'),'http://127.0.0.1:8096');type(input('[data-custom-name]'),'Jellyfin');await settle()
 document.querySelector<HTMLElement>('[data-custom-gate-switch] input, [data-custom-gate-switch]')!.click();await settle()
 expect(document.querySelector('[data-custom-path]')?.getAttribute('data-gate')).toBe('off')
 expect(document.querySelector('[data-custom-risk]')?.textContent).toContain(c('risk.line'))
 document.querySelector<HTMLElement>('[data-custom-save]')!.click();await settle()
 expect(added).not.toHaveBeenCalled()
 document.querySelector<HTMLElement>('[data-custom-accept] input, [data-custom-accept]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-custom-save]')!.click();await settle()
 expect(added).toHaveBeenCalledWith({name:'Jellyfin',url:'http://127.0.0.1:8096',appSignIn:true,insecureTls:false,acceptRisk:true})
 f.done()
})

const portInfo={success:true,data:{port_info:[
 {port_id:'http',name:'HTTP',desc:'Web Station und Web Mail',dst_port:['80'],protocol:'tcp',src_port:null},
 {port_id:'contacts_radicale',name:'Radicale',desc:'Kontakte',dst_port:['5555'],protocol:'tcp',src_port:null},
 {port_id:'ssh',name:'SSH',desc:'Verschlüsselter Terminaldienst',dst_port:['2222'],protocol:'tcp',src_port:null},
 {port_id:'ntp',name:'NTP',desc:'NTP-Dienst',dst_port:['123'],protocol:'udp',src_port:null},
 {port_id:'ftp',name:'FTP',desc:'FTP',dst_port:['21','55536-55899'],protocol:'tcp',src_port:null},
]}}
const files={token:()=>'fixture-token'} as never
function dsmPorts(reply:unknown){
 const fetch=vi.fn(async(_url:string,init:RequestInit)=>{
  const form=new URLSearchParams(String(init.body))
  expect([form.get('api'),form.get('method')]).toEqual(['SYNO.Core.Service.PortInfo','load'])
  return {ok:true,json:async()=>reply}
 })
 vi.stubGlobal('fetch',fetch)
 return fetch
}
const fromNas:Candidate[]=[
 {name:'HTTP',url:'http://127.0.0.1:80',source:'service',service:{id:'http',title:'HTTP',desc:'Web Station',titleKey:'firewall:firewall_service_opt_http'}},
 {name:'Docker(TCP)',url:'http://127.0.0.1:18096',source:'service',service:{id:'media_tcp',title:'Docker(TCP)',desc:'Docker media'}},
 {name:'Radicale',url:'http://127.0.0.1:5555',source:'service',service:{id:'contacts_radicale',title:'Radicale',desc:'Contacts'}},
 {name:'',url:'http://127.0.0.1:2222',source:'listening'},
 {name:'',url:'http://127.0.0.1:9000',source:'listening'},
]

test('DSM port definitions are named by DSM in its language, by section, and name a listening port',async()=>{
 dsmPorts(portInfo)
 const dsm=await readDsmPorts({files} as never)
 expect(dsm.map(p=>p.id)).toEqual(['http','contacts_radicale','ssh','ntp','ftp'])
 expect(dsm.find(p=>p.id==='ftp')?.ports).toEqual([21])
 expect(fromNas.map(n=>candidateName(n,dsm))).toEqual(['Web Station und Web Mail (HTTP)','Docker media','Radicale','SSH',''])
 // The generic SDK includes UDP definitions; Connect still names only TCP/all candidates.
 expect(candidateName({name:'',url:'http://127.0.0.1:123',source:'listening'},dsm)).toBe('')
 expect(candidateName({name:'Fallback',url:'http://127.0.0.1:123',source:'service',service:{id:'ntp',title:'Fallback'}},dsm)).toBe('Fallback')
 // Without PortInfo the NAS's own names stay, with DSM's generic titles explained.
 expect(fromNas.map(n=>candidateName(n,[]))).toEqual(['Web Station (HTTP)','Docker media','Radicale','',''])
 for(const reply of [{success:false,error:{code:105}},{success:true,data:{}},{success:true,data:{port_info:[null,{port_id:'',protocol:'tcp',dst_port:['1']}]}}]){
  dsmPorts(reply)
  expect(await readDsmPorts({files} as never)).toEqual([])
 }
 expect(await readDsmPorts({files:undefined} as never)).toEqual([])
 expect(serviceName('HTTPS','File Station')).toBe('File Station (HTTPS)')
 expect(serviceName('','','only_id')).toBe('only_id')
})

test('the add dialog lists DSM services with DSM names and ports, and a failed PortInfo keeps the list',async()=>{
 for(const [reply,first] of [[portInfo,'Web Station und Web Mail (HTTP) · 127.0.0.1:80'],[{success:false},'Web Station (HTTP) · 127.0.0.1:80']] as const){
  dsmPorts(reply)
  const f=fixture({'apps.customcandidates':()=>({candidates:fromNas})},files)
  await settle();await open(f.root)
  f.root.querySelector<HTMLElement>('[data-apps-add]')!.click();await settle()
  const trigger=document.querySelector<HTMLElement>('[data-custom-pick] [role="combobox"]')!
  trigger.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));await settle()
  const options=[...document.querySelectorAll('[role="option"]')].map(o=>o.textContent?.trim())
  expect(options[0]).toBe(first)
  expect(options).toContain(c('pick.port').replace('{port}','9000')+' · 127.0.0.1:9000')
  f.done();document.body.innerHTML=''
 }
})

test('a declared DSM path selects the package instead of creating a manual DSM proxy',async()=>{
 const {knownCandidate}=await import('../src/connect/customApps')
 const c:Candidate={package:'ExampleMusic',name:'Music',source:'package',url:'https://127.0.0.1:5001/synopkgland/ExampleMusic/',icon:'/webman/3rdparty/ExampleMusic/images/app_64.png'}
 expect(knownCandidate(c.url,[c])).toEqual(c)
 expect(knownCandidate('https://foreign.example:5001/synopkgland/ExampleMusic/',[c])).toBeUndefined()
 expect(knownCandidate('https://127.0.0.1:5001/webman/',[c])).toBeUndefined()
 expect(knownCandidate(c.url+'../Other/',[c])).toBeUndefined()
 const f=fixture({'apps.customcandidates':()=>({candidates:[c]})});await settle();await open(f.root)
 f.root.querySelector<HTMLElement>('[data-apps-add]')!.click();await settle()
 expect(document.querySelector('[data-package-candidate=ExampleMusic]')?.textContent).toContain('Music')
 expect(document.querySelector('[data-package-candidate=ExampleMusic] img')?.getAttribute('src')).toBe(c.icon)
 type(input('[data-custom-url]'),c.url);await settle()
 expect(document.querySelector('[data-known-package]')?.textContent).toContain('Music')
 expect(document.querySelector('[data-custom-gate]')).toBeNull()
 document.querySelector<HTMLElement>('[data-custom-save]')!.click();await settle()
 expect(f.call.mock.calls.some(([m])=>m==='apps.customadd')).toBe(false)
 f.done()
})

test('From this NAS remains visible while loading, empty, and failed, with manual URL and retry',async()=>{
 let resolve!:(x:unknown)=>void
 const load=vi.fn(()=>new Promise(r=>{resolve=r}))
 const f=fixture({'apps.customcandidates':load});await settle();await open(f.root)
 f.root.querySelector<HTMLElement>('[data-apps-add]')!.click();await settle()
 expect(document.querySelector('[data-custom-pick]')).not.toBeNull()
 expect(document.querySelector('[data-candidates-loading]')).not.toBeNull()
 expect(input('[data-custom-url]')).not.toBeNull()
 resolve({candidates:[]});await settle()
 expect(document.querySelector('[data-candidates-empty]')?.textContent).toContain(c('pick.empty'))
 f.done()
 const retry=vi.fn().mockRejectedValueOnce(new Error('Discovery unavailable')).mockResolvedValue({candidates:fromNas})
 const g=fixture({'apps.customcandidates':retry});await settle();await open(g.root)
 g.root.querySelector<HTMLElement>('[data-apps-add]')!.click();await settle()
 expect(document.querySelector('[data-candidates-error]')?.textContent).toContain('Discovery unavailable')
 expect(input('[data-custom-url]')).not.toBeNull()
 document.querySelector<HTMLElement>('[data-candidates-retry]')!.click();await settle()
 expect(retry).toHaveBeenCalledTimes(2)
 expect(document.querySelector('[data-candidates-error]')).toBeNull()
 expect(document.querySelector('[data-custom-pick] [role=combobox]')).not.toBeNull()
 g.done()
})

test('Login Portal uses the administrator window and keeps native icons and custom settings',async()=>{
 const {readSynologyPortals}=await import('../src/connect/customApps')
 const portal={id:'SYNO.SDS.SheetStation.Application',display_name:'Synology Drive',alias:'documents',https_port:7443}
 const webApi=vi.fn(async(api:string)=>api==='SYNO.Core.AppPortal'?{portal:[portal]}:{JSConfig:{'drive.js':{'SYNO.SDS.Drive.Application':{appPortalId:portal.id,icon:'images/Drive_{0}.png',jsBaseURL:'webman/3rdparty/Drive'}}}})
 expect(await readSynologyPortals({webApi} as never)).toEqual([{...portal,icon:'/webman/3rdparty/Drive/images/Drive_64.png'}])
 expect(webApi).toHaveBeenCalledWith('SYNO.Core.AppPortal','list',2,{additional:['default_setting']})
 await expect(readSynologyPortals({webApi:vi.fn().mockRejectedValue({code:'dsm_105'})} as never)).rejects.toEqual({code:'dsm_105'})
 expect(await readSynologyPortals({} as never)).toEqual([])
})

test('choosing a Synology path fills a saveable URL and name without sharing it',async()=>{
 const candidate:Candidate={name:'Synology Photos',url:'https://127.0.0.1:5001/photo/',source:'synology',icon:'/webman/3rdparty/Photos/images/Photos_64.png'}
 const added=vi.fn(()=>({id:'custom.photos'}))
 const f=fixture({'apps.customcandidates':()=>({candidates:[candidate]}),'apps.customadd':added});await settle();await open(f.root)
 f.root.querySelector<HTMLElement>('[data-apps-add]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-synology-candidates] button')!.click();await settle()
 expect(input('[data-custom-url]').value).toBe(candidate.url)
 expect(input('[data-custom-name]').value).toBe(candidate.name)
 document.querySelector<HTMLElement>('[data-custom-save]')!.click();await settle()
 expect(added).toHaveBeenCalledWith({name:candidate.name,url:candidate.url,appSignIn:false,insecureTls:false})
 expect(f.call.mock.calls.some(([m])=>m==='apps.setpublic'||m==='apps.setrelay')).toBe(false)
 f.done()
})

test('Login Portal suggestions contain only fields accepted by the strict backend route',async()=>{
 const {readSynologyPortals}=await import('../src/connect/customApps')
 const webApi=vi.fn(async(api:string)=>api==='SYNO.Core.AppPortal'?{portal:[{
  id:'SYNO.SDS.App.FileStation3.Instance',display_name:'File Station',alias:'files',https_port:7443,
  acl:null,fqdn:'private.example',hsts:false,enable_redirect:false,
  additional:{default_setting:{alias:'file',fqdn:'file.example',http_port:7000,https_port:7001,hsts:false}},
 }]}:{JSConfig:{}})
 expect(await readSynologyPortals({webApi} as never)).toStrictEqual([{
  id:'SYNO.SDS.App.FileStation3.Instance',display_name:'File Station',alias:'files',https_port:7443,
  additional:{default_setting:{alias:'file'}},
 }])
})
