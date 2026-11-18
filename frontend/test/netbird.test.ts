// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])('NetBird %s keeps key advanced and clears transient credentials',async locale=>{
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true}:method==='ports.list'?{entries:[],revision:1,detection:'ready'}:{provider:'multiple',state:'disconnected',connections:[]})
 const app=mount(root,createStandaloneContext({call,locale}));await settle()
 const nav=[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='NetBird')!;nav.click();await settle()
 const page=root.querySelector<HTMLElement>('[data-page="netbird"]')!
 expect(page.querySelector('[data-start-section] input')).toBeNull()
 const key=page.querySelector<HTMLInputElement>('input[type=password]')!
 key.value='SYNTHETIC-NB-KEY';key.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 page.querySelector<HTMLButtonElement>('[data-nb-connect]')!.click();await settle()
 expect(call).toHaveBeenCalledWith('connection.connect',{provider:'netbird',token:'SYNTHETIC-NB-KEY'})
 expect(key.value).toBe('')
 key.value='TRANSIENT';key.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 const ts=[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Tailscale')!;ts.click();await settle();nav.click();await settle()
 expect(key.value).toBe('')
 app.unmount();root.remove()
})
