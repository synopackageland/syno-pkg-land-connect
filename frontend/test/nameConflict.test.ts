// @vitest-environment jsdom
import {afterEach,expect,test} from 'vitest'
import {createApp,h} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import NameConflictRow from '../src/components/NameConflictRow.vue'
import {settle} from './support'
const cleanup:Array<()=>void>=[]
afterEach(()=>{cleanup.splice(0).forEach(fn=>fn())})
test.each(['leftover','other_connect','other'] as const)('offers safe recovery for %s',async reason=>{
 const events:string[]=[]
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({render:()=>h(AppRoot,{},()=>h(NameConflictRow,{locale:'en',conflict:{reason,conflictName:'nas.example.com',suggestedName:'nas-2.example.com',reuseRecord:'record-digest',dnsUrl:'https://dash.cloudflare.com/account/example.com/dns/records',tunnelUrl:'https://one.dash.cloudflare.com/account/networks/connectors'},onReuse:()=>events.push('reuse'),onUse:()=>events.push('use'),onCheck:()=>events.push('check')}))});app.mount(root);cleanup.push(()=>{app.unmount();root.remove()});await settle()
 expect(root.textContent).toContain({leftover:'Left over from an earlier setup',other_connect:'Used by another Syno Connect Center',other:'Used by another service'}[reason])
 const reuse=root.querySelector<HTMLButtonElement>('[data-name-reuse]'),use=root.querySelector<HTMLButtonElement>('[data-name-use]')
 // One main action: reuse a leftover, otherwise the checked alternative.
 expect([!!reuse,!!use]).toEqual(reason==='leftover'?[true,false]:[false,true])
 expect(root.querySelector('[data-name-alternative]')).toBeNull()
 expect(use?.textContent).toBe(reason==='leftover'?undefined:'Use nas-2.example.com')
 ;(reuse??use)!.click();root.querySelector<HTMLButtonElement>('[data-name-check]')!.click();await settle()
 expect(events).toEqual([reason==='leftover'?'reuse':'use','check'])
 // One Cloudflare link: an active Connect's tunnel, otherwise the DNS record.
 expect([...root.querySelectorAll<HTMLAnchorElement>('[data-name-conflict] a')].map(a=>a.href)).toEqual([reason==='other_connect'?'https://one.dash.cloudflare.com/account/networks/connectors':'https://dash.cloudflare.com/account/example.com/dns/records'])
 expect(root.querySelector('[data-name-hint]')?.textContent).toBe({leftover:'Reusing it replaces this DNS record',other_connect:'Turn off the other Syno Connect Center to free this name',other:'Not in use? Delete it in Cloudflare, then check again'}[reason])
 expect(root.textContent).not.toMatch(/cloudflare_name_taken|\.$/)
})

test('approval errors retain recovery details through the existing form',async()=>{
 const {useApprovalFlow}=await import('../src/connect/approvalFlow')
 const {vi}=await import('vitest');const {ref}=await import('vue')
 const pin={mode:'C' as const,issuer:'',notAfter:''}
 const item={package:'ExampleApp',name:'Example App',hostname:'app.example.com',ready:true,port:8080,protection:{},pin,token:'approval'}
 const call=vi.fn(async(method:string,params:Record<string,unknown>)=>{
  if(method==='apps.inspect')return {...item,hostname:params.hostname??item.hostname,...(params.reuseName?{reuseName:params.reuseName}:{})}
  throw {code:'cloudflare_name_taken',details:{reason:'leftover',conflictName:'app.example.com',suggestedName:'app-2.example.com',reuseRecord:'digest',dnsUrl:'https://dash.cloudflare.com/account/example.com/dns/records'}}
 })
 let flow:ReturnType<typeof useApprovalFlow>
 const root=document.createElement('div');document.body.append(root)
 const app=createApp({setup(){flow=useApprovalFlow({ctx:{locale:'en',call},message:()=>'',busy:ref(false)} as unknown as Parameters<typeof useApprovalFlow>[0]);return()=>h('div')}});app.mount(root);cleanup.push(()=>{app.unmount();root.remove()})
 await flow!.inspect(['ExampleApp'],'public');expect(await flow!.approve()).toBe(false)
 expect(flow!.conflict.value?.reason).toBe('leftover')
 vi.useFakeTimers();flow!.recoverName('reuse');await vi.advanceTimersByTimeAsync(310);await settle();vi.useRealTimers()
 expect(call).toHaveBeenCalledWith('apps.inspect',{package:'ExampleApp',scope:'public',hostname:'app.example.com',reuseName:{name:'app.example.com',record:'digest'}})
 expect(flow!.approval.value?.items[0]?.reuseName).toEqual({name:'app.example.com',record:'digest'})
})

test('an unproven previous-install DNS name uses the inline guide, not takeover',async()=>{
 const root=document.createElement('div');document.body.append(root)
 const events:string[]=[]
 const app=createApp({render:()=>h(AppRoot,{},()=>h(NameConflictRow,{locale:'en',conflict:{reason:'leftover',objectType:'dns',conflictName:'nas.example.com',suggestedName:'nas-2.example.com',dashboardUrl:'https://dash.cloudflare.com/account/example.com/dns/records'},onUse:()=>events.push('use'),onCheck:()=>events.push('check')}))});app.mount(root);cleanup.push(()=>{app.unmount();root.remove()});await settle()
 expect(root.querySelector('[data-name-reuse]')).toBeNull()
 expect(root.querySelector<HTMLInputElement>('input[data-resource-conflict-name]')?.value).toBe('nas.example.com')
 root.querySelector<HTMLButtonElement>('[data-resource-conflict-alternative]')!.click();root.querySelector<HTMLButtonElement>('[data-resource-conflict-retry]')!.click();await settle()
 expect(events).toEqual(['use','check'])
})
