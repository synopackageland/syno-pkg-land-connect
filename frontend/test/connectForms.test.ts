// @vitest-environment jsdom
// The connect form contract the setup wizard relies on: each provider's form
// mounts on its own under the shared state, starts its provider with
// connection.connect, then emits submitted and, once the provider runs,
// connected. Its actions slot sits before the one primary button.
import {createApp,h} from 'vue'
import {expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {provideConnectState} from '../src/connect/state'
import {CONNECT_FORMS,type ConnectFormProvider} from '../src/components/connect-forms'
import {settle} from './support'

const ports={detection:'ready',mappingState:'pending-provider',revision:1,entries:[{id:'dsm-http',name:'DSM HTTP',protocol:'http',port:5000,source:'dsm',enabled:true,state:'ready'}]}
function mountForm(provider:ConnectFormProvider){
 const root=document.createElement('div');document.body.append(root)
 const events:string[]=[]
 const call=vi.fn(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='app.status')return {admin:true}
  if(method==='ports.list'||method==='ports.prepare')return ports
  if(method==='connection.connect')return {provider:'multiple',state:'connected',simulation:false,shareUrl:'',connections:[{provider:params.provider,state:'connected',simulation:false,shareUrl:''}]}
  return {provider:'multiple',state:'disconnected',simulation:false,shareUrl:'',connections:[]}
 })
 const app=createApp({setup(){provideConnectState(createStandaloneContext({call}));return()=>h(CONNECT_FORMS[provider],{onSubmitted:()=>events.push('submitted'),onConnected:()=>events.push('connected')},{actions:()=>h('button',{'data-wizard-back':''},'Back')})}})
 app.mount(root)
 return {root,call,events,done(){app.unmount();root.remove()}}
}
const click=(root:HTMLElement,selector:string)=>root.querySelector<HTMLElement>(selector)!.click()

test.each([
 ['tailscale',[],'[data-ts-connect]'],
 ['netbird',[],'[data-nb-connect]'],
 ['cloudflare',[],''],
] as const)('the %s form starts its provider on its own and reports submitted, then connected',async(provider,consent,button)=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 const f=mountForm(provider);await settle()
 expect(f.root.querySelector('[data-wizard-back]')).toBeTruthy()
 if(provider==='cloudflare'){
  // My own domain: the name, the consent, then sign in.
  const input=f.root.querySelector<HTMLInputElement>('input')!;input.value='nas';input.dispatchEvent(new Event('input'));await settle()
  click(f.root,'[data-cf-confirm]');await settle()
  click(f.root,'[data-cf-domain]')
 }else{
  for(const c of consent){click(f.root,c);await settle()}
  click(f.root,button)
 }
 await settle();await settle()
 const sent=f.call.mock.calls.find(([method])=>method==='connection.connect')
 expect(sent?.[1]).toMatchObject({provider})
 expect(f.events).toEqual(['submitted','connected'])
 f.done()
})

test('the Tailscale form sends only the provider by default',async()=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 const f=mountForm('tailscale');await settle()
 click(f.root,'[data-ts-connect]');await settle();await settle()
 expect(f.call.mock.calls.find(([m])=>m==='connection.connect')?.[1]).toEqual({provider:'tailscale'})
 f.done()
})

test('the IPv6 wizard form is a read-only guide without a connect action',async()=>{
 const f=mountForm('ipv6');await settle()
 expect(f.root.querySelector('[data-ipv6-checklist]')).toBeTruthy()
 expect(f.root.querySelector('[data-ipv6-connect],[data-ipv6-confirm]')).toBeNull()
 expect(f.events).toEqual([])
 expect(f.call.mock.calls.some(([m])=>m==='connection.connect')).toBe(false)
 f.done()
})

test('the exposure consent is a value-column row, with the button and the note',async()=>{
 const f=mountForm('cloudflare');await settle()
 // The consent belongs to the field above it: it starts on the value column,
 // not under the label, and steps back only when the form stacks (FormItem inset).
 expect(f.root.querySelector('[data-cf-confirm]')?.closest('[data-form-item][data-inset]')).toBeTruthy()
 expect(f.root.querySelector('[data-cf-confirm]')?.closest('[data-form-item]')).toBeTruthy()
 f.done()
})
