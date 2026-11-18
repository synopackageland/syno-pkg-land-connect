// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {fixture,connected} from './providerFixture'
import {settle} from './support'

// One button rule on every provider page (UI button policy 0.14.7 and the
// Connect audit, following DSM): turning a way off is a grey (secondary) tool
// button in the action row at the end of the way's block, never a capsule at the value column under an
// empty label; the way's main action (sign in, connect, reconnect) is the one
// blue tool button; only the button of the dialog that confirms turning off is red.
const button=(root:ParentNode,selector:string)=>{const b=root.querySelector<HTMLButtonElement>(selector);expect(b,selector).toBeTruthy();return {variant:b!.getAttribute('data-variant'),kind:b!.getAttribute('data-kind'),inset:!!b!.closest('[style*="label-column"]'),b:b!}}
const tab=async(root:HTMLElement,value:string)=>{root.querySelector<HTMLElement>(`[data-page="cloudflare"] [data-ui-tab-value="${value}"]`)!.click();await settle()}

test('Tailscale and NetBird turn off with a grey tool button in the status action row',async()=>{
 const f=fixture([connected('tailscale',{loggedIn:true} as never),connected('netbird',{netbirdIP:'100.90.0.2'} as never)]);await settle()
 for(const [nav,page] of [['Tailscale','tailscale'],['NetBird','netbird']]){
  await f.nav(nav)
  expect(button(f.page(page),'[data-stop]')).toMatchObject({variant:'secondary',kind:'tool',inset:false})
 }
 f.done()
})

test('each Cloudflare tab turns its way off with the same grey tool button at the start of its action row',async()=>{
 const f=fixture([connected('cloudflare',{shareUrl:'https://nas.example.com',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}]} as never)],{'apps.workersstatus':()=>({connected:true,subdomain:'example'})});await settle()
 await f.nav('Cloudflare')
 const page=f.page('cloudflare')
 expect(page.querySelector('[data-stop]')).toBeNull()
 expect(button(page,'[data-cf-domain-tab] [data-way-off]')).toMatchObject({variant:'secondary',kind:'tool',inset:false})
 await tab(f.root,'free')
 const off=button(page,'[data-free-link] [data-way-off]')
 expect(off).toMatchObject({variant:'secondary',kind:'tool',inset:false})
 off.b.click();await settle()
 expect(button(document,'[data-free-off-confirm]').variant).toBe('danger')
 f.done()
})

test('the temporary address stops with a grey tool button',async()=>{
 const f=fixture([connected('cloudflare',{mode:'quick',shareUrl:'https://random-words.trycloudflare.com',endpoints:[{url:'https://random-words.trycloudflare.com',public:true}]} as never)]);await settle()
 await f.nav('Cloudflare');await tab(f.root,'temporary')
 expect(button(f.page('cloudflare'),'[data-cf-temporary-stop]')).toMatchObject({variant:'secondary',kind:'tool'})
 f.done()
})

test('reconnecting a saved own domain and signing in are blue tool buttons, not capsules',async()=>{
 const f=fixture([{provider:'cloudflare',state:'disconnected',mode:'domain',hostname:'nas.example.com',shareUrl:''} as never]);await settle()
 await f.nav('Cloudflare')
 expect(button(f.page('cloudflare'),'[data-cf-reconnect]')).toMatchObject({variant:'primary',kind:'tool',inset:true})
 await f.nav('Tailscale')
 expect(button(f.page('tailscale'),'[data-ts-connect]')).toMatchObject({variant:'primary',kind:'tool'})
 await f.nav('NetBird')
 expect(button(f.page('netbird'),'[data-nb-connect]')).toMatchObject({variant:'primary',kind:'tool'})
 f.done()
})
