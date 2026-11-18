// @vitest-environment jsdom
import {createApp,h,nextTick} from 'vue'
import {afterEach,expect,test} from 'vitest'
import {AppRoot} from '@synopackageland/ui'
import HostnameInput from '../src/components/HostnameInput.vue'
import {hostnameIssue} from '../src/connect/hostname'
import {translate} from '../src/i18n'
import {relayLane} from '../src/connect/topology'
import './support'
const cleanups:(()=>void)[]=[]
afterEach(()=>cleanups.splice(0).forEach(f=>f()))
test.each(['dsm.example.workers.dev','app.example.com'])('only the label of %s is editable',async host=>{
 const root=document.createElement('div');document.body.append(root)
 let changed=''
 const app=createApp({render:()=>h(AppRoot,()=>h(HostnameInput,{modelValue:host,hostname:host,locale:'en','onUpdate:modelValue':v=>{changed=v}}))})
 app.mount(root);cleanups.push(()=>{app.unmount();root.remove()});await nextTick()
 const input=root.querySelector('input')!
 expect(input.value).toBe(host.split('.')[0])
 expect(root.querySelector('[data-hostname-suffix]')?.textContent).toBe(host.slice(host.indexOf('.')))
 input.value='my-nas';input.dispatchEvent(new Event('input',{bubbles:true}));await nextTick()
 expect(changed).toBe('my-nas'+host.slice(host.indexOf('.')))
})
test('DNS label errors are specific, including an attempted suffix injection',()=>{
 for(const [label,issue] of [['','required'],['x'.repeat(64),'length'],['bad.name','format'],['UPPER','format'],['-start','format'],['end-','format'],['a_b','format'],['valid-9','']]){
  expect(hostnameIssue(label+'.example.com','dsm.example.com')).toBe(issue)
 }
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const key of ['required','length','format']){
  expect(translate(locale,'apps.hostname.'+key)).not.toBe('apps.hostname.'+key)
  expect(translate(locale,'apps.hostname.'+key)).not.toMatch(/[.。]$/)
 }
})
test('Overview and Free link never call an offline relay running, even with a cached successful check',()=>{
 const check={provider:'workers',state:'ok',externalVerified:true} as const
 for(const relayState of ['connecting','reconnecting','disconnected'] as const){
  const lane=relayLane({connected:true,relayState},false,check)!
  expect(lane.links[1].label).not.toBe('overview.nas.running')
  expect(lane.links[0].state).not.toBe('active')
 }
 expect(relayLane({connected:true,relayState:'connected'},false,check)!.links[1].label).toBe('overview.nas.running')
})
