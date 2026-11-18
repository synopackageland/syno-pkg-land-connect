// @vitest-environment jsdom
import {afterEach,beforeEach,expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {mount} from '../src/main'
import {createStandaloneContext, createStandaloneHost} from '@synopackageland/sdk'
import {settle} from './support'

// A Portal/standalone launch owns the whole page: the SDK's web layout at
// 1200px+, a device-width viewport so a phone reaches mobile, and the app's
// own name and icon since no DSM title bar shows them. A DSM desktop window
// keeps the dense window layout and DSM's page as it is. The SDK's AppShell
// does all of this from ctx.
type Launch={host?:'dsm'|'standalone-web';fullPage?:boolean}
function fixture({host,fullPage}:Launch){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>method==='app.status'?{admin:true}:method==='binaries.status'?{product:p?.product,current:'1.0.0',phase:'idle'}:{provider:'fake',state:'disconnected',simulation:true,shareUrl:''})
 const ctx=createStandaloneContext({call,displayName:'Syno Connect Center',user:{name:'fixture-admin',isAdmin:true},appIcon:'webman/3rdparty/SynoPkgConnect/images/icon_64.png',
  fullPage,host:createStandaloneHost(host?{host,dsmStandalone:false}:{})})
 const app=mount(root,ctx)
 return {root,close(){app.unmount();root.remove()}}
}
const viewport=()=>document.head.querySelector<HTMLMetaElement>('meta[name="viewport"]')
const deviceViewport='width=device-width, initial-scale=1, viewport-fit=cover, interactive-widget=resizes-content'
const sidebarUser=(root:HTMLElement)=>root.querySelector<HTMLElement>('[data-ui-sidebar-user]')
const mode=(root:HTMLElement)=>root.querySelector('[data-ui-layout-mode]')?.getAttribute('data-ui-layout-mode')
let rect:ReturnType<typeof vi.spyOn>
beforeEach(()=>{rect=vi.spyOn(Element.prototype,'getBoundingClientRect').mockReturnValue({width:1440,height:900,top:0,left:0,right:1440,bottom:900,x:0,y:0,toJSON(){}} as DOMRect)})
afterEach(()=>{rect.mockRestore();viewport()?.remove()})

test('a DSM desktop window keeps the window layout and leaves the page viewport alone',async()=>{
 for(const fullPage of [false,undefined]){
  const f=fixture({host:'dsm',fullPage})
  try{
   await settle()
   expect(mode(f.root)).toBe('window')
   expect(viewport()).toBeNull()
   expect(f.root.querySelector('[data-ui-full-page]')).toBeNull()
   expect(sidebarUser(f.root)).toBeNull()
  }finally{f.close()}
 }
})

test('a full page takes the web layout and a device-width viewport, restored on unmount',async()=>{
 // ctx.fullPage from the CLI's DSM window, or (older CLI) any host but a DSM window.
 for(const launch of [{host:'dsm',fullPage:true},{host:'standalone-web'}] as Launch[]){
  const f=fixture(launch)
  await settle()
  expect(mode(f.root),JSON.stringify(launch)).toBe('web')
  expect(viewport()?.content).toBe(deviceViewport)
  expect(f.root.querySelector('[data-ui-full-page]')).toBeTruthy()
  f.close()
  expect(viewport()).toBeNull()
 }
})

test('a prepared phone viewport refits a stale DSM container without a native resize',async()=>{
 const meta=document.createElement('meta')
 meta.name='viewport';meta.content='width=device-width, initial-scale=1'
 document.head.append(meta)
 let width=980
 rect.mockImplementation(()=>({width,height:852,top:0,left:0,right:width,bottom:852,x:0,y:0,toJSON(){}} as DOMRect))
 const refit=vi.fn(()=>{width=393})
 window.addEventListener('resize',refit)
 const f=fixture({host:'dsm',fullPage:true})
 try{
  await settle()
  await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()))
  await settle()
  expect(refit).toHaveBeenCalled()
  // jsdom's ResizeObserver is a no-op; WebKit covers the resulting mobile layout.
  expect(width).toBe(393)
  expect(viewport()).toBe(meta)
  expect(meta.content).toBe(deviceViewport)
 }finally{
  f.close();window.removeEventListener('resize',refit)
 }
 expect(viewport()).toBe(meta)
})

test('a full page heads the sidebar with the app name and icon',async()=>{
 const f=fixture({host:'dsm',fullPage:true})
 try{
  await settle()
  expect(f.root.querySelector('[data-ui-brand-name]')?.textContent).toBe('Syno Connect Center')
  expect(f.root.querySelector('[data-ui-brand-icon]')?.getAttribute('src')).toBe('webman/3rdparty/SynoPkgConnect/images/icon_64.png')
 }finally{f.close()}
})

test('a full page ends the sidebar with the signed-in account and its role',async()=>{
 const f=fixture({host:'dsm',fullPage:true})
 try{
  await settle()
  expect(sidebarUser(f.root)?.textContent).toContain('fixture-admin')
  expect(sidebarUser(f.root)?.textContent).toContain('Administrator')
 }finally{f.close()}
})

test('a full page remembers its sidebar under an app-unique key',async()=>{
 localStorage.clear()
 const f=fixture({host:'dsm',fullPage:true})
 try{
  await settle()
  f.root.querySelector<HTMLButtonElement>('[data-ui-sidebar-collapse]')?.click()
  await settle()
  expect(Object.keys(localStorage)).toContain('SynoPkgConnect:app-sidebar')
 }finally{f.close();localStorage.clear()}
})

test('the entry loads the SDK stylesheet that sizes AppShell to the page',()=>{
 expect(readFileSync('src/main.ts','utf8')).toMatch(/@synopackageland\/sdk\/vue\/style\.css/)
})
