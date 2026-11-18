// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {mount} from '../src/main'
import {createStandaloneContext, createStandaloneHost} from '@synopackageland/sdk'
import {settle} from './support'

function fixture(host?:'dsm'|'standalone-web'){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>method==='app.status'?{admin:true}:method==='binaries.status'?{product:p?.product,current:'1.0.0',phase:'idle'}:{provider:'fake',state:'disconnected',simulation:true,shareUrl:''})
 const ctx=createStandaloneContext({call,...(host==='dsm'?{fullPage:false,host:createStandaloneHost({host,dsmStandalone:false})}:host?{host:createStandaloneHost({host})}:{})})
 const activateWindow=vi.spyOn(ctx.host,'activateWindow')
 const app=mount(root,ctx)
 return {root,app,activateWindow,close(){app.unmount();root.remove()}}
}
test('SDK roots use the public namespace, bounded app scope and distinct mount IDs',async()=>{
 // The 100% bound is the DSM window contract. The standalone fixture owns
 // the viewport and deliberately uses AppRoot's full-page layout instead.
 const a=fixture('dsm'),b=fixture('dsm');await settle()
 try{
  for(const {root} of [a,b]){
   expect(root.querySelector('[data-syno-pkg-land-ui]')).toBeTruthy()
   expect(root.querySelector('.syno-pkg-land-ui-root')).toBeTruthy()
   expect(root.querySelector('[data-syno-ui],.syno-ui-root')).toBeNull()
   expect(root.querySelector<HTMLElement>('[data-ui-app]')?.style.height).toBe('100%')
   expect(root.querySelector('[data-ui-app]')?.getAttribute('data-ui-modal-scope')).toBe('app')
  }
  const ids=[...a.root.querySelectorAll('[id]'),...b.root.querySelectorAll('[id]')].map(n=>n.id)
  expect(ids.length).toBeGreaterThan(10)
  expect(new Set(ids).size).toBe(ids.length)
  expect(readFileSync('src/main.ts','utf8')).toMatch(/themes\/dsm7-embedded.css/)
 }finally{a.app.unmount();b.app.unmount();a.root.remove();b.root.remove()}
})
test('a standalone page uses the viewport layout without inheriting the DSM height constraint',async()=>{
 const rect=vi.spyOn(Element.prototype,'getBoundingClientRect').mockReturnValue({width:1440,height:900,top:0,left:0,right:1440,bottom:900,x:0,y:0,toJSON(){}} as DOMRect)
 const f=fixture('standalone-web');await settle()
 try{
  expect(f.root.querySelector('[data-ui-full-page]')).not.toBeNull()
  expect(f.root.querySelector<HTMLElement>('[data-ui-app]')?.style.height).toBe('')
  expect(f.root.querySelector('[data-ui-layout-mode]')?.getAttribute('data-ui-layout-mode')).toBe('web')
 }finally{f.close();rect.mockRestore()}
})
test('a maximized DSM window explicitly retains window layout',async()=>{
 const rect=vi.spyOn(Element.prototype,'getBoundingClientRect').mockReturnValue({width:1350,height:900,top:0,left:0,right:1350,bottom:900,x:0,y:0,toJSON(){}} as DOMRect)
 const f=fixture('dsm');await settle()
 try{
  expect(f.root.querySelector('.syno-pkg-land-ui-root')?.getAttribute('data-ui-layout-mode')).toBe('window')
 }finally{f.app.unmount();f.root.remove();rect.mockRestore()}
})
test('in a DSM window dialogs may leave the window, and pressing one brings the window to the front',async()=>{
 const f=fixture('dsm')
 try{
  await settle()
  const floating=document.querySelector<HTMLElement>('[data-ui-floating]')
  expect(floating).not.toBeNull()
  floating!.dispatchEvent(new Event('pointerdown'))
  expect(f.activateWindow).toHaveBeenCalledOnce()
 }finally{f.close()}
 expect(document.querySelector('[data-ui-floating]')).toBeNull()
})
test('on a narrow viewport the floating dialogs become sheets that stay on screen',async()=>{
 const width=window.innerWidth
 Object.defineProperty(window,'innerWidth',{configurable:true,value:390})
 const f=fixture('dsm')
 try{
  await settle()
  expect(document.querySelector('[data-ui-floating]')?.getAttribute('data-ui-presentation')).toBe('mobile')
 }finally{f.close();Object.defineProperty(window,'innerWidth',{configurable:true,value:width})}
})
test('outside a DSM window dialogs stay inside the app',async()=>{
 for(const host of [undefined,'standalone-web'] as const){
  const f=fixture(host)
  try{await settle();expect(document.querySelector('[data-ui-floating]')).toBeNull()}finally{f.close()}
 }
})
