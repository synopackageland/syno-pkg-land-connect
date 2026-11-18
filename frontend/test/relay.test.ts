// @vitest-environment jsdom
// workers.dev: the free link as one public choice in the App dialog, and the
// Free link tab on the Cloudflare page that sets it up, against a mocked
// backend (apps.relayconnect, apps.workersstatus, apps.setrelay,
// apps.relaydisconnect and apps.list's relay field).
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {quotaReset,relayTokenUrl,type AppsView} from '../src/connect/appUrls'
import {subdomainSettingsUrl} from '../src/connect/freeLink'
import {settle} from './support'

const a=(key:string)=>translate('en','apps.'+key)
const view=(relay:AppsView['relay']={connected:true,subdomain:'example'}):AppsView=>({
 private:{available:true,host:'nas.tailnet.ts.net'},
 public:{available:true,connected:true,zone:'example.com'},
 relay,
 apps:[
  {package:'SynoPkgOff',name:'Off App',port:28001,publicCapable:true,protection:{}},
  {package:'SynoPkgMedia',name:'Media App',port:28002,publicCapable:true,media:'high',protection:{}},
  {package:'SynoPkgDomain',name:'Domain App',port:28003,publicCapable:true,protection:{},public:{state:'active',url:'https://domain.example.com/'}},
  {package:'SynoPkgRelay',name:'Relay App',port:28004,publicCapable:true,protection:{},relay:{state:'active',url:'https://spl-relay-1a2b.example.workers.dev/'}},
 ],
})

function fixture(v:AppsView,handlers:Record<string,(params:Record<string,unknown>)=>unknown>={}){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[]}
  if(method==='apps.list')return v
  if(handlers[method])return handlers[method]!(params)
  return {}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}))
 return {root,call,done(){app.unmount();root.remove()}}
}
async function open(root:HTMLElement){[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='App URLs')!.click();await settle()}
const page=(root:HTMLElement)=>root.querySelector<HTMLElement>('[data-page="apps"]')!
const row=(root:HTMLElement,pkg:string)=>page(root).querySelector<HTMLElement>(`[data-app-row="${pkg}"]`)!.closest<HTMLElement>('tr')!
async function openApp(root:HTMLElement,pkg:string){page(root).querySelector<HTMLElement>(`[data-app-open="${pkg}"]`)!.click();await settle();return document.querySelector<HTMLElement>('[data-app-dialog]')!}
const choose=async(d:HTMLElement,label:string)=>{[...d.querySelectorAll<HTMLElement>('[data-dialog-choice] label')].find(l=>l.textContent?.includes(label))!.querySelector<HTMLElement>('[role=radio]')!.click();await settle()}
// The Free link publishes DSM too, so its sign-in asks for the same consent.
const consent=async(s:HTMLElement)=>{s.querySelector<HTMLElement>('[data-free-confirm]')!.click();await settle()}
async function freeTab(root:HTMLElement){
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
 const cf=root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 cf.querySelector<HTMLElement>('[data-ui-tab-value="free"]')!.click();await settle()
 return cf.querySelector<HTMLElement>('[data-free-link]')!
}
afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

test('the token link fills in the relay permissions and the reset shows in the viewer\'s clock',()=>{
 const url=new URL(relayTokenUrl())
 expect(url.origin+url.pathname).toBe('https://dash.cloudflare.com/profile/api-tokens')
 expect(JSON.parse(url.searchParams.get('permissionGroupKeys')!)).toEqual([{key:'workers_scripts',type:'edit'},{key:'workers_routes',type:'edit'},{key:'access',type:'edit'},{key:'access_acct',type:'edit'}])
 expect(url.searchParams.get('accountId')).toBe('*')
 expect(quotaReset('en','Asia/Taipei')).toBe('08:00')
 expect(quotaReset('en','UTC')).toBe('00:00')
})

test('a backend without the relay offers no free link',async()=>{
 const v=view();delete v.relay
 const f=fixture(v);await settle();await open(f.root)
 expect(page(f.root).querySelector('[data-apps-attention]')).toBeNull()
 const d=await openApp(f.root,'SynoPkgOff')
 expect(d.querySelector('[data-dialog-setup="relay"]')).toBeTruthy()
 f.done()
})

test('the Apps page points to the Free link on the Cloudflare page instead of setting it up itself',async()=>{
 const f=fixture(view({connected:false}));await settle();await open(f.root)
 expect(page(f.root).querySelector('[data-relay-onboarding]')).toBeNull()
 expect(page(f.root).querySelector('input[type=password]')).toBeNull()
 const d=await openApp(f.root,'SynoPkgOff')
 d.querySelector<HTMLElement>('[data-dialog-setup="relay"] [data-dialog-fix]')!.click();await settle()
 const cf=f.root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 expect(cf.style.display).not.toBe('none')
 expect(cf.querySelector('[data-free-link]')).toBeTruthy()
 f.done()
})

test('the Free link checks the pasted token once, clears the field and shows the error without the token',async()=>{
 let fail=true
 const f=fixture(view({connected:false}),{'apps.relayconnect':()=>{if(fail)throw {code:'apps_relay_token_scope'};return {connected:true,subdomain:'example'}}})
 await settle();const s=await freeTab(f.root)
 expect(s.dataset.freeState).toBe('off')
 expect(s.querySelector('[data-cf-path]')).toBeTruthy()
 expect(s.querySelector<HTMLAnchorElement>('[data-free-token-link]')?.getAttribute('href')).toBe(relayTokenUrl())
 const connect=s.querySelector<HTMLButtonElement>('[data-free-connect]')!
 expect(connect.disabled).toBe(true)
 const input=s.querySelector<HTMLInputElement>('[data-free-token]')!
 expect(input.type).toBe('password')
 input.value=' secret-value ';input.dispatchEvent(new Event('input'));await settle()
 expect(connect.disabled).toBe(true)
 await consent(s)
 connect.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.relayconnect',{token:'secret-value'})
 expect(input.value).toBe('')
 const error=s.querySelector<HTMLElement>('[data-free-error]')!
 expect(error.textContent).toContain(a('code.apps_relay_token_scope'))
 expect(error.textContent).not.toContain('secret-value')
 fail=false
 input.value='secret-value';input.dispatchEvent(new Event('input'));await settle()
 connect.click();await settle()
 expect(s.querySelector('[data-free-error]')).toBeNull()
 expect(f.call.mock.calls.filter(c=>c[0]==='apps.workersstatus').length).toBeGreaterThan(1)
 f.done()
})

test('the Free link reads as setting up while it connects, with the same row as every way',async()=>{
 let finish:(v:unknown)=>void=()=>{}
 const f=fixture(view({connected:false}),{'apps.relayconnect':()=>new Promise(r=>{finish=r})})
 await settle();const s=await freeTab(f.root)
 const input=s.querySelector<HTMLInputElement>('[data-free-token]')!
 input.value='secret-value';input.dispatchEvent(new Event('input'));await settle()
 await consent(s)
 s.querySelector<HTMLButtonElement>('[data-free-connect]')!.click();await settle()
 expect(s.querySelector('[data-setting-up]')?.textContent).toContain(translate('en','connect.settling'))
 expect(s.querySelector<HTMLElement>('[data-cf-path]')?.dataset.pathState).toBe('pending')
 finish({connected:true,subdomain:'example'});await settle()
 expect(s.querySelector('[data-setting-up]')).toBeNull()
 f.done()
})

test('an account without a workers.dev name asks for one, filled in, and offers another when it is taken',async()=>{
 const sent:Record<string,unknown>[]=[]
 const f=fixture(view({connected:false}),{'apps.relayconnect':p=>{sent.push(p);if(!p.subdomain)return {connected:false,code:'workers_subdomain_required'};if(p.subdomain!=='other')throw {code:'workers_subdomain_taken'};return {connected:true,subdomain:'other'}}})
 await settle();const s=await freeTab(f.root)
 const input=s.querySelector<HTMLInputElement>('[data-free-token]')!
 input.value='secret-value';input.dispatchEvent(new Event('input'));await settle()
 await consent(s)
 s.querySelector<HTMLElement>('[data-free-connect]')!.click();await settle()
 expect(s.querySelector('[data-free-step="name"]')).toBeTruthy()
 expect(s.querySelector('[data-free-token-done]')).toBeTruthy()
 const name=s.querySelector<HTMLInputElement>('[data-free-name-input]')!
 expect(name.value).toMatch(/^[a-z0-9-]+$/)
 expect(s.querySelector('[data-free-suffix]')?.textContent).toBe('.workers.dev')
 expect(s.querySelector('[data-free-step="name"] [data-free-token]')).toBeNull()
 name.value='Taken Name';name.dispatchEvent(new Event('input'));await settle()
 expect(s.querySelector('[data-free-example]')?.textContent).toContain('taken-name')
 s.querySelector<HTMLElement>('[data-free-claim]')!.click();await settle()
 expect(sent.at(-1)).toEqual({token:'secret-value',subdomain:'taken-name'})
 expect(s.textContent).toContain(translate('en','cfpage.code.workers_subdomain_taken'))
 const suggestion=s.querySelector<HTMLElement>('[data-free-suggestion]')!
 expect(suggestion.textContent).toMatch(/taken-name-\d{3}/)
 suggestion.click();await settle()
 expect(name.value).toMatch(/^taken-name-\d{3}$/)
 name.value='other';name.dispatchEvent(new Event('input'));await settle()
 s.querySelector<HTMLElement>('[data-free-claim]')!.click();await settle()
 expect(sent.at(-1)).toEqual({token:'secret-value',subdomain:'other'})
 expect(s.querySelector('[data-free-step="name"]')).toBeNull()
 f.done()
})

test('a media App is never picked silently: the free link says it uses the quota fast',async()=>{
 const f=fixture(view());await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgMedia')
 expect(d.querySelector('[data-dialog-choice]')!.textContent).toContain(a('relay.mediaRow'))
 f.done()
})

test('turning one off deletes its Worker; Turn off on the Cloudflare page deletes every Worker, then the token',async()=>{
 const f=fixture(view(),{'apps.workersstatus':()=>({connected:true,subdomain:'example'})});await settle();await open(f.root)
 const d1=await openApp(f.root,'SynoPkgRelay')
 await choose(d1,a('status.off'))
 d1.querySelector<HTMLElement>('[data-dialog-save]')!.click();await settle()
 expect(d1.textContent).toContain(a('off.relayRule'))
 expect(d1.textContent).not.toContain(a('off.dns'))
 d1.querySelector<HTMLElement>('[data-off-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.setrelay',{packages:['SynoPkgRelay'],enabled:false})
 expect(page(f.root).querySelector('[data-relay-disconnect]')).toBeNull()
 const free=await freeTab(f.root)
 expect(free.dataset.freeState).toBe('on')
 // The name shows once, in the page's Connection status.
 expect(free.querySelector('[data-free-name]')).toBeNull()
 expect(f.root.querySelector('[data-page="cloudflare"] [data-cf-free-status]')?.textContent).toContain('example.workers.dev')
 free.querySelector<HTMLElement>('[data-way-off]')!.click();await settle()
 const d=document.querySelector<HTMLElement>('[data-free-off-dialog]')!
 expect(d.textContent).toContain(a('relay.disconnectLine'))
 expect(d.textContent).toContain(a('relay.disconnectToken'))
 d.querySelector<HTMLElement>('[data-free-off-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.relaydisconnect',{})
 expect(f.call.mock.calls.some(c=>c[0]==='apps.cloudflaredisconnect')).toBe(false)
 f.done()
})

test('a token that stopped working points to the Free link tab and keeps the Apps already on',async()=>{
 const f=fixture(view({connected:true,invalid:true,subdomain:'example'}),{'apps.workersstatus':()=>({connected:true,invalid:true,subdomain:'example'})});await settle();await open(f.root)
 const notice=page(f.root).querySelector<HTMLElement>('[data-apps-attention="relay"]')!
 expect(notice.textContent).toContain(a('relay.invalid'))
 // The App keeps its free link; no new one can be picked while the token is broken.
 expect(row(f.root,'SynoPkgRelay').querySelector('[data-app-way="relay"]')).toBeTruthy()
 expect(page(f.root).querySelector('[data-apps-batch-create="relay"]')).toBeNull()
 notice.querySelector<HTMLElement>('[data-apps-attention-open]')!.click();await settle()
 const free=f.root.querySelector<HTMLElement>('[data-page="cloudflare"] [data-free-link]')!
 expect(free.dataset.freeState).toBe('attention')
 expect(free.querySelector('[data-free-invalid]')).toBeTruthy()
 expect(free.querySelector('[data-free-token]')).toBeTruthy()
 f.done()
})

// The Free link runs without the tunnel: the status badge never reads Off beside it.
test('with the tunnel off, the Cloudflare status badge follows the Free link',async()=>{
 for(const [workers,word] of [[{connected:true,subdomain:'example'},'on'],[{connected:true,invalid:true,subdomain:'example'},'attention'],[{connected:false},'off']] as const){
  const f=fixture(view(),{'apps.workersstatus':()=>workers});await settle()
  await freeTab(f.root)
  expect(f.root.querySelector('[data-page="cloudflare"] [data-connection-badge]')?.getAttribute('data-status-word'),JSON.stringify(workers)).toBe(word)
  f.done()
 }
})

test('changing the workers.dev name warns that every App address changes and links Cloudflare\'s page',async()=>{
 const f=fixture(view(),{'apps.workersstatus':()=>({connected:true,subdomain:'example'})});await settle()
 const free=await freeTab(f.root)
 expect(free.querySelector('[data-free-rename]')).toBeNull()
 const more=[...free.querySelectorAll<HTMLElement>('[data-use-more]')].find(b=>b.textContent?.includes(translate('en','cfpage.free.rename')))!
 more.click();await settle()
 const rename=free.querySelector<HTMLElement>('[data-free-rename]')!
 expect(rename.querySelector('[data-free-rename-warning]')?.textContent).toContain(translate('en','cfpage.free.renameWarn'))
 expect(rename.querySelector('[data-free-rename-link]')?.getAttribute('href')).toBe(subdomainSettingsUrl)
 f.done()
})


test.each(['apps_relay_token_scope','workers_api_refused','unexpected_cleanup_failure'])('a reconnecting Free link shows %s inside its dialog and allows retry',async(code)=>{
 const v=view({connected:true,subdomain:'example',relayState:'reconnecting'})
 let fail=true
 const f=fixture(v,{'apps.workersstatus':()=>v.relay,'apps.relaydisconnect':()=>{if(fail)throw {code};v.relay={connected:false};return {disconnected:true}}})
 await settle();const free=await freeTab(f.root)
 free.querySelector<HTMLElement>('[data-way-off]')!.click();await settle()
 const d=document.querySelector<HTMLElement>('[data-free-off-dialog]')!
 d.querySelector<HTMLElement>('[data-free-off-confirm]')!.click();await settle()
 expect(d.querySelector('[data-free-off-error]')?.textContent).toContain(code)
 if(code==='apps_relay_token_scope')expect(d.querySelector('[data-free-off-error]')?.textContent).toContain(a('code.apps_relay_token_scope'))
 expect(d.querySelector<HTMLButtonElement>('[data-free-off-confirm]')!.disabled).toBe(false)
 fail=false;d.querySelector<HTMLElement>('[data-free-off-confirm]')!.click();await settle()
 expect(document.querySelector('[data-free-off-dialog]')).toBeNull()
 expect(free.dataset.freeState).toBe('off')
 f.done()
})
