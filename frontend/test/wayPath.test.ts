// @vitest-environment jsdom
// The shared path picture: one status vocabulary, a flow while connecting,
// words as DOM labels in every locale.
import {createApp,h,type App} from 'vue'
import {afterEach,expect,test} from 'vitest'
import WayPath from '../src/components/WayPath.vue'
import CloudflarePath from '../src/components/CloudflarePath.vue'
import WayStatus from '../src/components/WayStatus.vue'
import {PATH_NODES,PHASE_TAG,pathModel,phaseLinks,wayPhase,type PathWay,type WayPhase} from '../src/connect/wayPath'
import {translate} from '../src/i18n'
import type {ConnectionStatus} from '../src/context'
import {settle} from './support'

let mounted:Array<()=>void>=[]
afterEach(()=>{for(const done of mounted)done();mounted=[]})
function render(component:object,props:Record<string,unknown>):HTMLElement {
 const root=document.createElement('div');document.body.append(root)
 const app:App=createApp({render:()=>h(component,props)});app.mount(root)
 mounted.push(()=>{app.unmount();root.remove()})
 return root
}

test.each([
 ['connected','on'],['connecting','connecting'],['disconnecting','turning-off'],['stopping','turning-off'],
 ['needs-login','attention'],['needs-machine-auth','attention'],['failed','error'],['disconnected','off'],['unknown','off'],
] as const)('%s reads as %s',(state,phase)=>{
 expect(wayPhase({state} as ConnectionStatus)).toBe(phase)
})

test('connecting flows on both links, on works end to end, a problem colours the NAS side',()=>{
 expect(phaseLinks('workers','connecting')).toEqual(['pending','pending'])
 expect(phaseLinks('tailscale','on')).toEqual(['active','active'])
 // Nothing protects IPv6 on the way in.
 expect(phaseLinks('ipv6','on')).toEqual(['attention','active'])
 expect(phaseLinks('netbird','error')).toEqual(['idle','error'])
 // Turning off flows on the NAS side only, as the Overview lane.
 expect(phaseLinks('tailscale','turning-off')).toEqual(['idle','pending'])
 expect(phaseLinks('cloudflare-domain','off')).toEqual(['idle','idle'])
 expect(pathModel('workers','connecting',{links:['active','pending']}).links.map(l=>l.state)).toEqual(['active','pending'])
 expect(pathModel('tailscale','on',{app:'ConvertX'}).target).toBe('app')
})

test('WayPath draws the way as one lane and names it for screen readers',async()=>{
 const root=render(WayPath,{way:'workers',phase:'connecting',locale:'en'});await settle()
 const path=root.querySelector<HTMLElement>('[data-way-path]')!
 expect(path.dataset.phase).toBe('connecting')
 expect([...path.querySelectorAll<HTMLElement>('[data-link-state]')].map(l=>l.dataset.linkState)).toEqual(['pending','pending'])
 const label=path.querySelector('[role="group"]')?.getAttribute('aria-label')??''
 expect(label).toContain(translate('en','overview.node.anyBrowser'))
 expect(label).toContain(translate('en','path.node.workers'))
 expect(label).toContain(translate('en','path.phase.connecting'))
})

test('WayPath can end at one App',async()=>{
 const root=render(WayPath,{way:'tailscale',phase:'on',app:'ConvertX',locale:'en'});await settle()
 expect(root.querySelector('[role="group"]')?.getAttribute('aria-label')).toContain('ConvertX')
})

test('WayStatus uses the same word and colour on every page',async()=>{
 const root=render(WayStatus,{phase:'attention',locale:'en'});await settle()
 expect(root.textContent).toContain(translate('en','path.phase.attention'))
})

const LOCALES=['en','zh-TW','zh-CN','ja','ko','de','fr','it','es']
test.each(LOCALES)('public paths use the Overview browser node; private paths keep devices in %s',async locale=>{
 for(const way of ['workers','cloudflare-domain','cloudflare-quick','ipv6','tailscale','netbird'] as PathWay[]){
  const root=render(WayPath,{way,locale});await settle()
  const privateWay=way==='tailscale'||way==='netbird'
  expect(root.textContent).toContain(translate(locale,privateWay?'path.node.devices':'overview.node.anyBrowser'))
  expect(root.querySelector(privateWay?'.lucide-smartphone':'.lucide-monitor')).toBeTruthy()
 }
 for(const kind of ['domain','free']){
  const root=render(CloudflarePath,{kind,state:'idle',locale});await settle()
  expect(root.textContent).toContain(translate(locale,'overview.node.anyBrowser'))
  expect(root.textContent).not.toContain(translate(locale,'cfpage.path.phone'))
  expect(root.querySelector('.lucide-monitor')).toBeTruthy()
 }
})
// Product names, and Internet where the language borrows the word.
const SAME=['cloudflare','workers','tailscale','netbird','internet']
test.each(LOCALES)('every node and status word is translated in %s',locale=>{
 const nodes=new Set(Object.values(PATH_NODES).flatMap(n=>[n.source,n.via]).concat('nas'))
 for(const node of nodes){
  const key='path.node.'+node
  expect(translate(locale,key),key).not.toBe(key)
  if(locale!=='en'&&!SAME.includes(node))expect(translate(locale,key),`${locale} ${key}`).not.toBe(translate('en',key))
 }
 for(const phase of Object.keys(PHASE_TAG) as WayPhase[])expect(translate(locale,PHASE_TAG[phase].label)).not.toMatch(/\.$/)
 expect(Object.keys(PATH_NODES) as PathWay[]).toHaveLength(6)
})
