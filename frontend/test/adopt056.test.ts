// @vitest-environment jsdom
// UI 0.5.6-0.5.7 supply label wiring, container-width layout, read-only rows,
// and links, so the app no longer carries its own versions.
import {expect,test,vi} from 'vitest'
import {readFileSync,readdirSync} from 'node:fs'
import {createApp} from 'vue'
import BinarySettings from '../src/BinarySettings.vue'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {settle} from './support'
const css=()=>readFileSync('src/style.css','utf8')
const sources=()=>(readdirSync('src',{recursive:true}) as string[]).filter(f=>f.endsWith('.vue')).map(f=>[f,readFileSync(`src/${f}`,'utf8')])
async function app(status:Record<string,unknown>={provider:'cloudflare',state:'disconnected',simulation:false,shareUrl:''},locale='en'){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(m:string,p?:Record<string,unknown>)=>m==='app.status'?{admin:true}:m==='binaries.status'?{product:p?.product,current:'1.0.0',runtimeVersion:'2026.9.3',phase:'idle'}:status)
 const a=mount(root,createStandaloneContext({call,locale}));await settle()
 return {root,close(){a.unmount();root.remove()}}
}

test('a FormItem label is the only accessible name of its control',async()=>{
 const f=await app()
 const labelled=[...f.root.querySelectorAll<HTMLLabelElement>('label[for]')].map(l=>f.root.ownerDocument.getElementById(l.htmlFor)).filter(Boolean) as HTMLElement[]
 expect(labelled.length).toBeGreaterThan(3)
 for(const control of labelled)expect(control.getAttribute('aria-label'),control.outerHTML.slice(0,80)).toBeNull()
 f.close()
})
test('connector facts are read-only description rows and the version in use is a tag',async()=>{
 const f=await app()
 const binaries=f.root.querySelector<HTMLElement>('[data-page="settings"]')!
 const terms=[...binaries.querySelectorAll('dt')].map(dt=>dt.textContent?.trim())
 expect(terms).toEqual(expect.arrayContaining(['Latest version','Verification']))
 expect([...binaries.querySelectorAll('[data-binary-in-use]')].some(tag=>tag.textContent?.trim()==='In use 2026.9.3')).toBe(true)
 f.close()
})
// UI 0.5.7's own words, kept here so a regression to English shows up per locale.
const hint:Record<string,string>={en:'Opens in a new tab','zh-TW':'在新分頁中開啟','zh-CN':'在新标签页中打开',ja:'新しいタブで開く',ko:'새 탭에서 열기',de:'In neuem Tab öffnen',fr:'Ouvrir dans un nouvel onglet',es:'Abrir en una pestaña nueva',it:'Apri in una nuova scheda'}
test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])('external pages open through Link with the %s new-tab hint',async locale=>{
 const f=await app(undefined,locale)
 const links=[...f.root.querySelectorAll<HTMLAnchorElement>('a[href^="https://"]')]
 expect(links.map(a=>a.href)).toEqual(expect.arrayContaining(['https://developers.cloudflare.com/tunnel/get-started/']))
 for(const a of links){expect(a.target).toBe('_blank');expect(a.rel).toContain('noopener');expect(a.textContent).toContain(hint[locale])}
 f.close()
})

test('links rely on the built-in new-tab hint instead of passing their own translation',()=>{
 for(const [file,text] of sources())expect(text,file).not.toMatch(/external-label|download-label|externalLabel|downloadLabel/)
})
test('fields inside a FieldCell leave the direction to the FormItem',()=>{
 for(const [file,text] of sources())expect(text,file).not.toMatch(/<FormItem[^>]*\sdirection=/)
})
test('the stylesheet no longer restores heading and paragraph margins',()=>{
 expect(css()).not.toMatch(/:is\(h1|\bp\s*\{/)
})
test('BinarySettings still renders without a runtime version',async()=>{
 const call=vi.fn(async(_m:string,p?:Record<string,unknown>)=>({product:p?.product,current:'1.0.0',phase:'idle'}))
 const root=document.createElement('div');document.body.append(root);const a=createApp(BinarySettings,{ctx:createStandaloneContext({call})});a.mount(root);await settle()
 expect(root.querySelectorAll('dl').length).toBeGreaterThan(0)
 a.unmount();root.remove()
})