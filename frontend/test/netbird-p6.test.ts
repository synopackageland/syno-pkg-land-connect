// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {trustedLoginUrl} from '../src/connect/login'
async function settle(){for(let i=0;i<30;i++){await Promise.resolve();await nextTick()}}
test('NetBird complete URI exact allowlist',()=>{
 const url='https://login.netbird.io/activate?user_code=ABCD-EFGH'
 expect(trustedLoginUrl('netbird',url)).toBe(url)
 for(const bad of [url+'\n',url+'#x',url+'&next=x',url.replace('login.','api.'),url.replace('.io/','.io:443/'),url.replace('ABCD-','ABCD%2D')])expect(trustedLoginUrl('netbird',bad)).toBe('')
})
test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])('NetBird %s zero field SSO and blank tab survives connecting',async locale=>{
 const tab={opener:{},location:{replace:vi.fn()},close:vi.fn()};vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const root=document.createElement('div');document.body.append(root)
 const url='https://login.netbird.io/activate?user_code=ABCD-EFGH'
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true}:method==='ports.list'?{entries:[],revision:1,detection:'ready'}:method==='connection.connect'?{provider:'netbird',state:'needs-login',phase:'awaiting-login',attemptId:1,loginUrl:url,userCode:'ABCD-EFGH',deadline:new Date(Date.now()+600000).toISOString()}:{provider:'multiple',state:'disconnected',connections:[]})
 const app=mount(root,createStandaloneContext({call,locale}));await settle()
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='NetBird')!.click();await settle()
 const page=root.querySelector<HTMLElement>('[data-page="netbird"]')!
 expect(page.querySelector('[data-start-section] input')).toBeNull()
 const button=page.querySelector<HTMLButtonElement>('[data-nb-connect]')!
 expect(button.disabled).toBe(false);button.click();expect(window.open).toHaveBeenCalledWith('','_blank');await settle()
 expect(call).toHaveBeenCalledWith('connection.connect',{provider:'netbird'})
 expect(tab.location.replace).toHaveBeenCalledWith(url)
 expect(page.querySelector<HTMLInputElement>('[data-nb-code]')?.value).toBe('ABCD-EFGH')
 expect(page.querySelector('[data-nb-countdown]')).toBeTruthy()
 app.unmount();root.remove();vi.restoreAllMocks()
})

test('NetBird delays navigation until trusted challenge, and cancels the blank tab',async()=>{
 vi.useFakeTimers()
 const {fixture}=await import('./providerFixture')
 const tab={opener:{},location:{replace:vi.fn()},close:vi.fn()};vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>({provider:'netbird',state:'connecting',attemptId:1,phase:'obtaining-login-url'})});await settle();await f.nav('NetBird')
 f.page('netbird').querySelector<HTMLButtonElement>('[data-nb-connect]')!.click();await settle()
 expect(tab.close).not.toHaveBeenCalled();expect(tab.location.replace).not.toHaveBeenCalled()
 f.page('netbird').querySelector<HTMLButtonElement>('[data-stop]')!.click();await settle()
 expect(tab.close).toHaveBeenCalled();expect(f.call).toHaveBeenCalledWith('connection.disconnect',{provider:'netbird'})
 f.done();vi.useRealTimers();vi.restoreAllMocks()
})
test('NetBird self-hosted key requires a visible combined confirmation',async()=>{
 const {fixture}=await import('./providerFixture')
 const f=fixture();await settle();await f.nav('NetBird')
 const page=f.page('netbird')
 for(const [selector,value] of [['[data-nb-key]','SYNTHETIC-SETUP-KEY'],['[data-nb-management]','https://management.example.test']]){
  const input=page.querySelector<HTMLInputElement>(selector!)!;input.value=value!;input.dispatchEvent(new Event('input',{bubbles:true}))
 }
 await settle();page.querySelector<HTMLButtonElement>('[data-nb-connect]')!.click();await settle()
 expect(f.call.mock.calls.filter(c=>c[0]==='connection.connect')).toHaveLength(0)
 expect(f.root.textContent).toContain('https://management.example.test')
 f.root.querySelector<HTMLButtonElement>('[data-nb-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'netbird',managementUrl:'https://management.example.test',token:'SYNTHETIC-SETUP-KEY',confirmManagementPolicy:true})
 expect(page.querySelector<HTMLInputElement>('[data-nb-key]')!.value).toBe('')
 f.done()
})

test('self-hosted management origin is implicit, while unsafe NetBird lookalikes remain rejected',()=>{
 const managementUrl='https://mesh.example.test:443/'
 expect(trustedLoginUrl('netbird','https://mesh.example.test/device',{managementUrl})).toBe('https://mesh.example.test/device')
 expect(trustedLoginUrl('netbird','https://idp.example.test/device',{managementUrl})).toBe('')
 for(const raw of ['https://login.netbird.io.evil.test/device','https://mesh.example.test/device%2fnext','https://user@mesh.example.test/device','http://mesh.example.test/device'])expect(trustedLoginUrl('netbird',raw,{managementUrl},['https://login.netbird.io.evil.test'])).toBe('')
})

test('an external IdP is needs-action and only trusted after the administrator confirms the shown host',async()=>{
 const {fixture}=await import('./providerFixture')
 const pending={provider:'netbird',state:'needs-login',phase:'trust-login-origin',code:'login_origin_required',managementUrl:'https://mesh.example.test',loginOrigin:'https://idp.example.test',loginOrigins:[],simulation:false,shareUrl:''} as const
 const tab={opener:{},location:{replace:vi.fn()},close:vi.fn()};vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([{...pending,loginOrigins:[]}],{'connection.connect':p=>({provider:'netbird',state:'needs-login',phase:'awaiting-login',managementUrl:p.managementUrl,loginOrigins:p.loginOrigins,loginUrl:'https://idp.example.test/device'})})
 try{
  await settle();await f.nav('NetBird');const page=f.page('netbird')
  expect(page.querySelector('[data-connection-badge]')?.getAttribute('data-status-word')).toBe('attention')
  expect(page.querySelector('[data-nb-origin-required]')?.textContent).toContain('https://idp.example.test')
  expect(page.querySelector('[data-login-action]')).toBeNull()
  expect(tab.location.replace).not.toHaveBeenCalled()
  page.querySelector<HTMLElement>('[data-nb-trust]')!.click();await settle()
  const dialog=document.querySelector<HTMLElement>('[data-nb-trust-dialog]')!
  expect(dialog.textContent).toContain('https://idp.example.test')
  expect(f.call.mock.calls.filter(c=>c[0]==='connection.connect')).toHaveLength(0)
  dialog.querySelector<HTMLElement>('[data-nb-trust-confirm]')!.click();await settle()
  expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'netbird',managementUrl:'https://mesh.example.test',loginOrigins:['https://idp.example.test']})
  expect(tab.location.replace).toHaveBeenCalledWith('https://idp.example.test/device')
 }finally{f.done();vi.restoreAllMocks()}
})
