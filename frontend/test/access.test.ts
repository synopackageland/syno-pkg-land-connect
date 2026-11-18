// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {settle} from './support'
import {accessApi,normalizeEmails,type AccessStatus} from '../src/connect/access'
import {relayTokenUrl} from '../src/connect/appUrls'
import en from '../../i18n/messages/en.json'

test('canonical emails preserve local parts and reject broad or multiline rules',()=>{
 expect(normalizeEmails([' reader@EXAMPLE.test ','reader@example.test'])).toEqual(['reader@example.test'])
 expect(normalizeEmails(['Reader@example.test','reader@example.test'])).toHaveLength(2)
 for(const value of ['','*@example.test','a@example.test\nb@example.test','Name <a@example.test>'])expect(()=>normalizeEmails([value])).toThrow()
 const url=new URL(relayTokenUrl())
 expect(JSON.parse(url.searchParams.get('permissionGroupKeys')!)).toEqual([{key:'workers_scripts',type:'edit'},{key:'workers_routes',type:'edit'},{key:'access',type:'edit'},{key:'access_acct',type:'edit'}])
})

async function open(initial:string[]=[],failPreflight=false,sessionDuration?:string){
 let attempted=false
 let status:AccessStatus={state:initial.length?'synced':'off',revision:0,emails:initial,endpoints:[],sessionDuration}
 const call=vi.fn(async(method:string,params:unknown)=>{
  if(method==='app.status')return {admin:true}
  if(method==='ports.list')return {entries:[],revision:1,detection:'ready'}
  if(method==='apps.workersstatus')return {connected:false,relayState:'disconnected'}
  if(method==='apps.list')return {apps:[],private:{available:false},public:{available:false,connected:false}}
  if(method===accessApi.get)return attempted&&failPreflight?{...status,state:'error',code:'access_token_required',working:false}:status
  if(method===accessApi.sync)return status
  if(method===accessApi.set){attempted=true;if(failPreflight)return {...status,working:true};const inParams=params as {emails:string[];sessionDuration?:string};status={...status,sessionDuration:inParams.sessionDuration??status.sessionDuration,revision:1,emails:inParams.emails,state:inParams.emails.length?'synced':'off'};return status}
  return {provider:'multiple',state:'disconnected',connections:[]}
 })
 const root=document.createElement('div');document.body.append(root)
 const app=mount(root,createStandaloneContext({call,locale:'en'}));await settle()
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
 const section=root.querySelector<HTMLElement>('[data-access-section]')!
 return {root,section,call,dashboard:(value:string)=>{status={...status,sessionDuration:value}},done:()=>{app.unmount();root.remove()}}
}
test('native warning appears beside the toggle and before the confirmed save',async()=>{
 const f=await open()
 f.section.querySelector<HTMLButtonElement>('[data-access-toggle]')!.click();await settle()
 expect(f.section.querySelector('[data-access-native-warning]')?.textContent).toContain(en['access.native'])
 const input=f.section.querySelector<HTMLInputElement>('input[type=email]')!;input.value=' reader@EXAMPLE.test ';input.dispatchEvent(new Event('input'));await settle()
 f.section.querySelector<HTMLButtonElement>('[data-access-add]')!.click();await settle()
 f.root.querySelector<HTMLButtonElement>('[data-access-review]')!.click();await settle()
 expect(document.querySelector('[data-access-confirm-native]')?.textContent).toContain(en['access.native'])
 expect(f.call.mock.calls.filter(([method])=>method===accessApi.set)).toHaveLength(0)
 document.querySelector<HTMLButtonElement>('[data-access-apply]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith(accessApi.set,{emails:['reader@example.test'],revision:0,confirm:true})
 expect(f.section.querySelector('[data-access-native-warning]')).toBeTruthy()
 f.done()
})
test('turning the restriction off confirms removal before sending an empty list',async()=>{
 const f=await open(['reader@example.test'])
 f.section.querySelector<HTMLButtonElement>('[data-access-toggle]')!.click();await settle()
 f.root.querySelector<HTMLButtonElement>('[data-access-review]')!.click();await settle()
 expect(document.body.textContent).toContain(en['access.confirmOff'])
 expect(f.call.mock.calls.filter(([method])=>method===accessApi.set)).toHaveLength(0)
 document.querySelector<HTMLButtonElement>('[data-access-apply]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith(accessApi.set,{emails:[],revision:0,confirm:true});f.done()
})
test('all supported locales have the warning, states and errors without trailing periods',()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  const messages=JSON.parse(readFileSync(`../i18n/messages/${locale}.json`,'utf8')) as Record<string,string>
  for(const key of Object.keys(en).filter(k=>k.startsWith('access.'))){expect(messages[key],locale+key).toBeTruthy();expect(messages[key]).not.toMatch(/[.。]$/)}
 }
})

test('an async preflight refusal keeps the unsaved email draft and warning',async()=>{
 const f=await open([],true)
 f.section.querySelector<HTMLButtonElement>('[data-access-toggle]')!.click();await settle()
 const input=f.section.querySelector<HTMLInputElement>('input[type=email]')!;input.value='reader@example.test';input.dispatchEvent(new Event('input'));await settle()
 f.section.querySelector<HTMLButtonElement>('[data-access-add]')!.click();await settle()
 f.root.querySelector<HTMLButtonElement>('[data-access-review]')!.click();await settle()
 document.querySelector<HTMLButtonElement>('[data-access-apply]')!.click();await settle()
 expect(f.section.textContent).toContain('reader@example.test')
 expect(f.section.querySelector('[data-access-native-warning]')).toBeTruthy()
 expect(f.section.querySelector('[data-access-error]')?.textContent).toContain(en['access.code.access_token_required'])
 f.done()
})


test('session defaults to 30 days and an explicit UI change uses the confirmed apply',async()=>{
 const f=await open(['reader@example.test'])
 expect(f.section.querySelector('[data-access-session]')?.textContent).toContain('30 days')
 f.section.querySelector<HTMLElement>('[data-access-session][role=combobox], [data-access-session] [role=combobox]')!.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}))
 await settle()
 const option=[...document.querySelectorAll<HTMLElement>('[role=option]')].find(n=>n.textContent?.trim()==='1 week')!
 expect(option).toBeTruthy();option.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));await settle()
 f.root.querySelector<HTMLButtonElement>('[data-access-review]')!.click();await settle()
 expect(f.call.mock.calls.filter(([method])=>method===accessApi.set)).toHaveLength(0)
 document.querySelector<HTMLButtonElement>('[data-access-apply]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith(accessApi.set,{emails:['reader@example.test'],revision:0,confirm:true,sessionDuration:'168h'})
 f.done()
})

test('sync displays a dashboard custom value without treating it as an unsaved UI edit',async()=>{
 const f=await open(['reader@example.test'],false,'24h')
 f.dashboard('2h45m')
 f.section.querySelector<HTMLButtonElement>('[data-access-sync]')!.click();await settle()
 expect(f.section.querySelector('[data-access-session]')?.textContent).toContain('2h45m')
 expect(f.root.querySelector<HTMLButtonElement>('[data-access-review]')!.disabled).toBe(true)
 // An unrelated email draft must not send a session overwrite.
 const input=f.section.querySelector<HTMLInputElement>('input[type=email]')!;input.value='editor@example.test';input.dispatchEvent(new Event('input'));await settle()
 f.section.querySelector<HTMLButtonElement>('[data-access-add]')!.click();await settle()
 f.dashboard('48h')
 f.section.querySelector<HTMLButtonElement>('[data-access-sync]')!.click();await settle()
 expect(f.section.querySelector('[data-access-session]')?.textContent).toContain('48h')
 f.root.querySelector<HTMLButtonElement>('[data-access-review]')!.click();await settle()
 document.querySelector<HTMLButtonElement>('[data-access-apply]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith(accessApi.set,{emails:['editor@example.test','reader@example.test'],revision:0,confirm:true})
 f.done()
})
