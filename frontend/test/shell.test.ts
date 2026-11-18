// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {activePage, createStandaloneContext} from '@synopackageland/sdk'
import {shellPages,type ShellPage} from '../src/shell/pages'
import {shellCatalogs,shellCopy} from '../src/shell/i18n'

async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
function nav(root:HTMLElement){return [...root.querySelectorAll<HTMLElement>('[data-nav-item]')]}
function navLabels(root:HTMLElement){return nav(root).map(node=>node.textContent?.trim())}
function clickNav(root:HTMLElement,label:string){const node=nav(root).find(node=>node.textContent?.trim()===label);expect(node,label).toBeTruthy();node!.click()}
function page(root:HTMLElement,name:ShellPage){return root.querySelector<HTMLElement>(`[data-page="${name}"]`)!}
function shown(root:HTMLElement,name:ShellPage){return page(root,name).style.display!=='none'}

test('the shell lists every page with the overview first',()=>{
 expect(shellPages()).toEqual(['status','apps','cloudflare','tailscale','netbird','ipv6','settings'])
 expect(activePage(undefined,shellPages())).toBe('status')
 expect(activePage('settings',shellPages())).toBe('settings')
 expect(activePage('missing' as ShellPage,['status'])).toBe('status')
})

test('nine navigation catalogs stay complete and resolve DSM locale aliases',()=>{
 expect(Object.keys(shellCatalogs)).toEqual(['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])
 for(const catalog of Object.values(shellCatalogs)){
  expect(Object.keys(catalog).sort()).toEqual(Object.keys(shellCatalogs.en).sort())
  expect(Object.values(catalog).every(value=>typeof value==='string'&&value.trim().length>0)).toBe(true)
  const labels=Object.values(catalog).map(value=>value.trim().toLowerCase())
  expect(new Set(labels).size).toBe(labels.length)
 }
 expect(shellCopy('cht')).toEqual(shellCatalogs['zh-TW'])
 expect(shellCopy('zh_CN')).toEqual(shellCatalogs['zh-CN'])
 expect(shellCopy('unknown')).toEqual(shellCatalogs.en)
})

test('choosing a sidebar entry swaps the visible page and keeps others mounted',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true,user:'fixture',package:'SynoPkgConnect',version:'test'}:{provider:'fake',state:'disconnected',simulation:true,shareUrl:''})
 const app=mount(root,createStandaloneContext({call}))
 try{
  await settle()
  expect(navLabels(root)).toEqual(shellPages().map(p=>shellCopy('en')[p]))
  expect(root.querySelector('h1')?.textContent?.trim()).toBe(shellCopy('en').status)
  expect(nav(root)[0]?.getAttribute('aria-current')).toBe('page')
  expect(shown(root,'status')).toBe(true)
  clickNav(root,'Settings');await settle()
  expect(root.querySelector('h1')?.textContent?.trim()).toBe(shellCopy('en').settings)
  expect(shown(root,'settings')).toBe(true)
  expect(shown(root,'status')).toBe(false)
  expect(page(root,'status')).toBeTruthy()
 }finally{app.unmount();root.remove()}
})
test('pages show a skeleton while the app loads, and navigation still works',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(()=>new Promise<never>(()=>{}))
 const app=mount(root,createStandaloneContext({call}));await settle()
 const status=root.querySelector<HTMLElement>('[data-page="status"]')!
 expect(status.querySelector('[aria-busy="true"]')).toBeTruthy()
 const nav=[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Tailscale')!
 nav.click();await settle()
 expect(root.querySelector<HTMLElement>('[data-page="tailscale"]')!.style.display).toBe('')
 expect(root.querySelector<HTMLElement>('[data-page="tailscale"] [aria-busy="true"]')).toBeTruthy()
 app.unmount();root.remove()
})
