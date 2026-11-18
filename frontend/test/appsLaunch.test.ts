// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext,type LaunchParams} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {appsRoute,singleOriginUrl,type AppsView} from '../src/connect/appUrls'
import {settle} from './support'

const a=(key:string,locale='en')=>translate(locale,'apps.'+key)
const view=(over:Partial<AppsView>={}):AppsView=>({
 private:{available:true,host:'nas.tailnet.ts.net'},
 public:{available:true,connected:true,zone:'example.com'},
 apps:[
  {package:'SynoPkgOff',name:'Off App',port:28001,publicCapable:true,protection:{},settingsUrl:'/webman/3rdparty/SynoPkgOff/sso/settings.cgi'},
  {package:'SynoPkgVault',name:'Vault App',port:28002,publicCapable:true,protection:{},external:{single_origin:true},settingsUrl:'/webman/3rdparty/SynoPkgVault/sso/settings.cgi'},
  {package:'SynoPkgVaultOn',name:'Vault On App',port:28003,publicCapable:true,protection:{},external:{single_origin:true},private:{state:'active',url:'https://nas.tailnet.ts.net:28003/'}},
 ],
 ...over,
})

function fixture(launchParams?:LaunchParams,apps=view()){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[]}
  if(method==='apps.list')return apps
  return {}
 })
 const listeners:Array<(p:LaunchParams)=>void>=[]
 const ctx=createStandaloneContext({call,locale:'en'})
 ctx.launchParams=launchParams
 ctx.onLaunch=fn=>{listeners.push(fn);return()=>{listeners.splice(listeners.indexOf(fn),1)}}
 const app=mount(root,ctx)
 return {root,call,listeners,launch:(p:LaunchParams)=>listeners.forEach(fn=>fn(p)),done(){app.unmount();root.remove()}}
}
const shown=(root:HTMLElement,name:string)=>root.querySelector<HTMLElement>(`[data-page="${name}"]`)!.style.display!=='none'
const rowOf=(root:HTMLElement,attribute:string,pkg:string)=>root.querySelector<HTMLElement>(`[${attribute}="${pkg}"]`)!.closest<HTMLElement>('tr')!
const changes=['apps.inspect','apps.setprivate','apps.setpublic','apps.setrelay','apps.reenable','apps.clear']
afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

test('appsRoute reads only /apps[/<package>[/<scope>]]',()=>{
 expect(appsRoute('/apps')).toEqual({})
 expect(appsRoute('/apps/')).toEqual({})
 expect(appsRoute('/apps/SynoPkgOff')).toEqual({package:'SynoPkgOff'})
 expect(appsRoute('/apps/SynoPkgOff/private')).toEqual({package:'SynoPkgOff',scope:'private'})
 expect(appsRoute('/apps/SynoPkgOff/workers')).toEqual({package:'SynoPkgOff',scope:'workers'})
 for(const route of ['/','/apps/SynoPkgOff/relay','/apps/SynoPkgOff/private/x','/apps/../x','/apps/a"b','/apps/SynoPkgOff/constructor','/status','apps/SynoPkgOff','/apps//private'])
  expect(appsRoute(route),route).toBeUndefined()
})

test('the DSM menu link opens Apps on the row and its dialog without changing anything',async()=>{
 const f=fixture({route:'/apps/SynoPkgOff/private'});await settle()
 expect(shown(f.root,'apps')).toBe(true)
 expect(f.call.mock.calls.some(c=>c[0]==='apps.list')).toBe(true)
 expect(rowOf(f.root,'data-app-row','SynoPkgOff').querySelector('[data-launch-focus]')).toBeTruthy()
 expect(f.root.querySelectorAll('[data-launch-focus]')).toHaveLength(1)
 const dialog=document.querySelector<HTMLElement>('[data-app-dialog]')!
 expect(dialog.dataset.package).toBe('SynoPkgOff')
 expect(dialog.querySelector('button[role="switch"]')!.getAttribute('aria-checked')??'false').toBe('false')
 expect(f.call.mock.calls.map(c=>c[0]).filter(m=>changes.includes(m))).toEqual([])
 f.done()
})

test('a launch of the open window moves the mark; other routes leave the page alone',async()=>{
 const f=fixture();await settle()
 expect(shown(f.root,'status')).toBe(true)
 f.launch({route:'/status'});f.launch({route:'/apps/SynoPkgOff/relay'});await settle()
 expect(shown(f.root,'status')).toBe(true)
 f.launch({route:'/apps/SynoPkgVault/public'});await settle()
 expect(shown(f.root,'apps')).toBe(true)
 expect(rowOf(f.root,'data-app-row','SynoPkgVault').querySelector('[data-launch-focus]')).toBeTruthy()
 // QuickConnect names no scope: the row is marked all the same.
 f.launch({route:'/apps/SynoPkgOff'});await settle()
 expect(rowOf(f.root,'data-app-row','SynoPkgOff').querySelector('[data-launch-focus]')).toBeTruthy()
 expect(rowOf(f.root,'data-app-row','SynoPkgVault').querySelector('[data-launch-focus]')).toBeNull()
 expect(f.call.mock.calls.map(c=>c[0]).filter(m=>changes.includes(m))).toEqual([])
 f.done()
 expect(f.listeners).toHaveLength(0)
})

test('with no way set up, a menu link marks the App in the empty state and opens no dialog',async()=>{
 const f=fixture({route:'/apps/SynoPkgOff'},view({private:{available:false,code:'tailscale-off'},public:{available:false,connected:false},apps:view().apps.map(r=>({...r,private:undefined}))}));await settle()
 expect(f.root.querySelector('[data-apps-empty]')).toBeTruthy()
 expect(document.querySelector('[data-app-dialog]')).toBeNull()
 f.done()
})

test('singleOriginUrl is the private origin without a path',()=>{
 const rows=view().apps
 expect(singleOriginUrl(rows[0]!,'nas.tailnet.ts.net')).toBeUndefined()
 expect(singleOriginUrl(rows[1]!,'nas.tailnet.ts.net')).toBe('https://nas.tailnet.ts.net:28002')
 expect(singleOriginUrl(rows[2]!)).toBe('https://nas.tailnet.ts.net:28003')
})

test('the new App URLs strings exist in all 9 locales without a trailing period',()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  for(const key of ['line.singleOrigin','line.singleOriginSet','single.rule','single.step','single.address','single.continue']){
   const text=a(key,locale)
   expect(text,`${locale} ${key}`).not.toBe('apps.'+key)
   expect(text,`${locale} ${key}`).not.toMatch(/[.。]$/)
  }
 }
})
