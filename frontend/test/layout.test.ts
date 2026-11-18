// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {settle} from './support'
function fixture(){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>method==='app.status'?{admin:true}:method==='binaries.status'?{product:p?.product,current:'1.0.0',phase:'idle'}:{provider:'fake',state:'disconnected',simulation:true,shareUrl:''})
 return {root,app:mount(root,createStandaloneContext({call}))}
}
const page=(root:HTMLElement,name:string)=>root.querySelector<HTMLElement>(`[data-page="${name}"]`)!
const grids=(el:HTMLElement)=>[...el.querySelectorAll<HTMLElement>('[style*="auto-fit"]')]
test('the header and every page share one PageContent that fills the window',async()=>{
 const f=fixture();await settle()
 // Nested PageContents only fill a tabbed page under its tab strip.
 const bodies=[...f.root.querySelectorAll('[data-page-content-body]')].filter(b=>!b.parentElement?.closest('[data-page-content-body]'))
 expect(bodies).toHaveLength(1)
 expect(f.root.querySelector('[data-width]')?.getAttribute('data-width')).toBe('fill')
 expect(bodies[0].contains(f.root.querySelector('h1'))).toBe(true)
 expect(bodies[0].contains(page(f.root,'status'))).toBe(true)
 expect(bodies[0].contains(page(f.root,'settings'))).toBe(true)
 f.app.unmount();f.root.remove()
})
test('custom width limits and card grids are gone from the stylesheet',()=>{
 const css=readFileSync('src/style.css','utf8')
 expect(css).not.toMatch(/max-width|connect-page|connect-cards/)
})
test('the stylesheet keeps only the DSM window root, and components handle the rest',()=>{
 const rules=readFileSync('src/style.css','utf8').split('\n').filter(Boolean).map(line=>line.split('{')[0].trim())
 expect(rules).toEqual(['.dsmapp-vue-root'])
})
test('connector cards use CardGrid so they add columns when wide',async()=>{
 const f=fixture();await settle()
 expect(grids(page(f.root,'settings'))[0].querySelectorAll('[aria-label="cloudflared"],[aria-label="tailscale"]')).toHaveLength(2)
 f.app.unmount();f.root.remove()
})
test('settings shows each connector card once with its details inside, and the GitHub token on its own tab',async()=>{
 const f=fixture();await settle()
 const settings=page(f.root,'settings')
 expect([...settings.querySelectorAll<HTMLElement>('[data-ui-tab-value]')].map(b=>b.dataset.uiTabValue)).toEqual(['connectors','github-token'])
 expect(settings.querySelectorAll('[aria-label="cloudflared"]')).toHaveLength(1)
 for(const product of ['cloudflared','tailscale','netbird']){
  const card=settings.querySelector<HTMLElement>(`[aria-label="${product}"]`)!
  expect(card.textContent).toContain('Verification and recovery')
 }
 expect(settings.querySelector('a[href="https://docs.netbird.io/"]')).toBeTruthy()
 f.app.unmount();f.root.remove()
})

// layout-rules: labels sit above their controls, and a field is as wide as its
// content instead of a fixed label column plus a fixed field.
// A page form's label sits above its field or, on a wide page, in the shared
// label column, so its fields line up with the Connection status rows.
test('every page form puts its labels above the fields or in the shared label column',async()=>{
 const f=fixture();await settle()
 const labels=[...f.root.querySelectorAll<HTMLLabelElement>('[data-page] form label[for]')].filter(l=>!f.root.ownerDocument.getElementById(l.htmlFor)?.matches('[role=checkbox],input[type=checkbox]'))
 expect(labels.length).toBeGreaterThan(5)
 let columns=0
 for(const label of labels){
  let item=label.parentElement
  while(item&&item.style.display!=='flex')item=item.parentElement
  if(item?.style.flexDirection==='column')continue
  columns++
  expect((item?.firstElementChild as HTMLElement|null)?.style.width,label.textContent??'').toBe('var(--ui-label-column)')
 }
 expect(columns).toBeGreaterThan(0)
 f.app.unmount();f.root.remove()
})

// layout-rules: a titled block on a page is a SettingsSection; Card is only for
// an item in a list, so a page has one heading style besides its header.
test('page sections use SettingsSection and cards appear only as list items',async()=>{
 const f=fixture();await settle()
 const cards=[...f.root.querySelectorAll<HTMLElement>('[data-page] section > header > h3')]
 const loose=cards.filter(h=>!h.closest('[style*="auto-fit"]')).map(h=>`${h.closest('[data-page]')?.getAttribute('data-page')}: ${h.textContent}`)
 expect(loose).toEqual([])
 f.app.unmount();f.root.remove()
})

// UI 0.9.7's Space packs a row at the leading edge by default, so a row of
// buttons or tags needs no justify-content="start" of its own.
test('button rows take Space\'s leading-edge default without restating it',async()=>{
 const {readdirSync}=await import('node:fs')
 const files=(readdirSync('src',{recursive:true}) as string[]).filter(f=>f.endsWith('.vue'))
 const rows=files.flatMap(f=>(readFileSync(`src/${f}`,'utf8').match(/<Space\b[^>]*>/g)??[]).filter(tag=>!/direction="column"/.test(tag)).map(tag=>`${f}: ${tag}`))
 expect(rows.length).toBeGreaterThan(3)
 expect(rows.filter(row=>/justify-content="start"/.test(row))).toEqual([])
})

// The SDK shell places notices between the page header and the page, and
// leaves no empty notice area when there is nothing to say.
test('notices sit in the shell between the header and the page, only when present',async()=>{
 const f=fixture();await settle()
 expect(f.root.querySelector('.app-notices')).toBeNull()
 f.app.unmount();f.root.remove()
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>{if(method==='app.status')return {admin:true};throw {code:'internal_error'}})
 const app=mount(root,createStandaloneContext({call}));await settle()
 const notices=root.querySelector<HTMLElement>('.app-notices')!
 expect(notices.querySelector('[role=alert]')).toBeTruthy()
 const body=root.querySelector('[data-page-content-body]')!
 const order=[...body.querySelectorAll('h1,.app-notices,[data-page="status"]')].map(n=>n.matches('h1')?'header':n.matches('.app-notices')?'notices':'page')
 expect(order).toEqual(['header','notices','page'])
 app.unmount();root.remove()
})

// SDK 0.10.0 adds no notices block when every notice in the slot is switched off
// by v-if, so the App passes its notices slot without a guard of its own.
test('the SDK shell adds no notices block when every notice is switched off',async()=>{
 const {createApp,createCommentVNode,h}=await import('vue')
 const {AppShell}=await import('@synopackageland/sdk/vue')
 const root=document.createElement('div');document.body.append(root)
 const ctx=createStandaloneContext({call:vi.fn()})
 const app=createApp({render:()=>h(AppShell,{ctx,locale:'en',pages:[{id:'status',title:'Status'}],modelValue:'status'},{
  notices:()=>[createCommentVNode('v-if',true),createCommentVNode('v-if',true)],
  default:()=>h('p','page'),
 })})
 app.mount(root);await settle()
 expect(root.textContent).toContain('page')
 expect(root.querySelector('.app-notices')).toBeNull()
 app.unmount();root.remove()
})

// UI 0.10 spaces page sections with --ui-section-gap (DSM's 12px); Space's
// regular spacing is that gap, while relaxed is a fixed 16px of its own.
test('each page stacks its sections at the UI section gap',async()=>{
 const f=fixture();await settle()
 for(const name of ['status','apps','cloudflare','tailscale','ipv6','netbird','settings']){
  const root=page(f.root,name),column='[style*="flex-direction: column"]'
  const stack=root.matches(column)?root:root.querySelector<HTMLElement>(column)
  expect(stack,name).toBeTruthy()
  expect(stack!.style.gap,name).toBe('var(--ui-section-gap)')
 }
 f.app.unmount();f.root.remove()
})

// The UI kit's Button centres a leading icon on its label's line and keeps
// the gap (theme button.css); a component's own vertical-align or margin on
// that icon pulled the Phone button's icon below its text.
test('no component restyles the icon inside a kit Button',async()=>{
 const {readdirSync}=await import('node:fs')
 const files=(readdirSync('src',{recursive:true}) as string[]).filter(f=>f.endsWith('.vue')||f.endsWith('.css'))
 const hits=files.filter(f=>/vertical-align|:deep\(svg\)/.test(readFileSync(`src/${f}`,'utf8')))
 expect(hits).toEqual([])
})
