// @vitest-environment jsdom
// The Free link helpers: the workers.dev name the page fills in, the name to
// try after a taken one and where the link stands; and the Cloudflare
// page's Apps tab, which lists every App on Cloudflare by way.
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {defaultAccountName,freeLinkState,nextName,openFreeLink,toLabel,validLabel} from '../src/connect/freeLink'
import type {AppsView} from '../src/connect/appUrls'
import {settle} from './support'

afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

test('a typed name becomes one DNS label',()=>{
 expect(toLabel('My NAS_01')).toBe('my-nas-01')
 expect(toLabel('--Ab--c--')).toBe('ab-c')
 expect(toLabel('x'.repeat(80))).toHaveLength(63)
 expect(validLabel('my-nas')).toBe(true)
 for(const bad of ['','-a','a-','A','a_b','x'.repeat(64)])expect(validLabel(bad)).toBe(false)
})

test('the filled-in name is the NAS host name, else the installation name, never an address',()=>{
 expect(defaultAccountName('Home-NAS.local','nas-1a2b')).toBe('home-nas')
 expect(defaultAccountName('192.0.2.10','nas-1a2b')).toBe('nas-1a2b')
 expect(defaultAccountName('[fd00::1]','nas-1a2b')).toBe('nas-1a2b')
 expect(defaultAccountName('localhost','nas-1a2b')).toBe('nas-1a2b')
 expect(defaultAccountName('localhost','')).toBe('my-nas')
})

test('a taken name suggests the same name with three digits, still one label',()=>{
 expect(nextName('photos',()=>0)).toBe('photos-100')
 expect(nextName('photos',()=>0.999)).toBe('photos-999')
 const long=nextName('a'.repeat(63),()=>0.5)
 expect(validLabel(long)).toBe(true)
 expect(long.endsWith('-550')).toBe(true)
})

test('the link state follows the status',()=>{
 expect(freeLinkState(undefined)).toBe('off')
 expect(freeLinkState({connected:false})).toBe('off')
 expect(freeLinkState({connected:false,code:'workers_subdomain_required'})).toBe('name')
 expect(freeLinkState({connected:true,subdomain:'a'})).toBe('on')
 expect(freeLinkState({connected:true,invalid:true})).toBe('attention')
})

test('openFreeLink opens the Cloudflare page on its Free link tab without a guide',()=>{
 const go=vi.fn()
 openFreeLink({go})
 expect(go).toHaveBeenCalledWith('cloudflare',{tab:'free'})
})

const view:AppsView={
 private:{available:true},
 public:{available:true,connected:true,zone:'example.com'},
 relay:{connected:true,subdomain:'example'},
 apps:[
  {package:'SynoPkgOff',name:'Off App',port:28001,publicCapable:true,protection:{}},
  {package:'SynoPkgDomain',name:'Domain App',port:28003,publicCapable:true,protection:{},public:{state:'active',url:'https://domain.example.com/'}},
  {package:'SynoPkgRelay',name:'Relay App',port:28004,publicCapable:true,protection:{},relay:{state:'active',url:'https://relay.example.workers.dev/'}},
 ],
}
function fixture(apps:AppsView){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[]}
  if(method==='apps.list')return apps
  return {}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}))
 return {root,done(){app.unmount();root.remove()}}
}
async function appsTab(root:HTMLElement){
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
 const cf=root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 cf.querySelector<HTMLElement>('[data-ui-tab-value="apps"]')!.click();await settle()
 return cf.querySelector<HTMLElement>('[data-cf-apps]')!
}

test('the Apps tab lists each App on Cloudflare with its way and opens the Apps page',async()=>{
 const f=fixture(view);await settle()
 const tab=await appsTab(f.root)
 expect(tab.querySelector('[data-cf-app="SynoPkgOff"]')).toBeNull()
 const ways=Object.fromEntries([...tab.querySelectorAll<HTMLElement>('[data-cf-app-way]')].map(e=>[e.closest('tr')!.querySelector<HTMLElement>('[data-cf-app]')!.dataset.cfApp,e.textContent]))
 expect(ways).toEqual({SynoPkgDomain:translate('en','cfpage.tab.domain'),SynoPkgRelay:translate('en','cfpage.tab.free')})
 expect([...tab.querySelectorAll('th')].map(h=>h.textContent?.trim()).filter(Boolean)).toEqual(['App',translate('en','cfpage.apps.way'),translate('en','apps.row.address'),translate('en','cfpage.apps.status')])
 // App URLs opens from a link above the list.
 tab.querySelector<HTMLElement>('[data-cf-apps-open]')!.click();await settle()
 expect(f.root.querySelector<HTMLElement>('[data-page="apps"]')!.style.display).not.toBe('none')
 f.done()
})

test('without Apps on Cloudflare the tab says so',async()=>{
 const f=fixture({...view,apps:[view.apps[0]!]});await settle()
 const tab=await appsTab(f.root)
 expect(tab.querySelector('[data-cf-apps-empty]')?.textContent).toBe(translate('en','cfpage.apps.empty'))
 expect(tab.querySelector('[data-cf-apps-table]')).toBeNull()
 f.done()
})
