// @vitest-environment jsdom
// The NetBird page's Self-host server tab: what is missing first, one button,
// the steps with the next action, the server's facts and a clean turn off.
import {afterEach,expect,test,vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {codeKey,grpcSettingsUrl,pollDelay,stepItems,waitingForGRPC,type ServerStatus} from '../src/connect/netbirdServer'
import {translate} from '../src/i18n'
import {relayTokenUrl} from '../src/connect/appUrls'
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const en=(key:string)=>translate('en',key)
afterEach(()=>{document.body.innerHTML=''})
// A step's note only explains; its form and buttons sit on the page, so closing the note keeps them.
function expectNoControls(callout:HTMLElement){expect(callout.querySelectorAll('input,form,button:not([data-ui-callout-close])')).toHaveLength(0);expect(callout.querySelector('[data-ui-callout-close]')).toBeTruthy()}

test('steps map to the step list and a failed one says what to do next',()=>{
 const items=stepItems([{id:'binary',state:'done'},{id:'route',state:'failed',code:'cloudflare_name_taken'},{id:'start',state:'running'},{id:'join',state:'pending'}],en)
 expect(items.map(i=>i.status)).toEqual(['done','error','current','pending'])
 expect(items[1]).toEqual({title:en('nbserver.step.route'),status:'error',description:en('nbserver.code.cloudflare_name_taken')})
 expect(items[0]?.description).toBeUndefined()
 expect(codeKey('something_new')).toBe('nbserver.code.netbird_server_failed')
 expect(pollDelay('working')).toBeLessThan(pollDelay('running'))
 expect(pollDelay('starting')).toBe(pollDelay('working'))
})

test('every step and code has a message in every locale',()=>{
 const keys=[...['prerequisites','binary','config','route','bridge','start','account','setupkey','check','join'].map(id=>'nbserver.step.'+id),...['netbird_server_workers_required','workers_not_connected','workers_routes_permission','workers_zone_account','workers_route_conflict','workers_name_conflict','workers_token_invalid','workers_unavailable'].map(codeKey),'nbserver.guide.title','nbserver.guide.text','nbserver.guide.action','nbserver.token.help','nbserver.token.save','nbserver.token.saved','nbserver.token.edit','nbserver.token.check',codeKey('netbird_server_off'),codeKey('netbird_server_leave_failed'),codeKey('netbird_server_grpc_blocked'),codeKey('netbird_server_signal_blocked')]
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const key of keys)expect(translate(locale,key),`${locale}:${key}`).not.toBe(key)
})

async function open(server:(method:string,params:unknown)=>unknown,connected=false){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,params:unknown)=>{
  if(method==='app.status')return {admin:true}
  if(method==='ports.list')return {entries:[],revision:1,detection:'ready'}
  if(method.startsWith('netbirdserver.'))return server(method,params)
  return connected?{provider:'multiple',state:'connected',connections:[{provider:'netbird',state:'connected',address:'100.64.0.2'}]}:{provider:'multiple',state:'disconnected',connections:[]}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}));await settle()
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='NetBird')!.click();await settle()
 const page=root.querySelector<HTMLElement>('[data-page="netbird"]')!
 const tabs=()=>[...page.querySelectorAll('[data-ui-tab-value]')].map(e=>e.getAttribute('data-ui-tab-value'))
 const openServer=async()=>{page.querySelector<HTMLElement>('[data-ui-tab-value="server"]')!.click();await settle()}
 const tab=()=>page.querySelector<HTMLElement>('[data-tab-panel="server"]')!
 return {root,page,call,tabs,openServer,tab,done:()=>{app.unmount();root.remove()}}
}

test('off, the page has Join and server tabs and reads shared dependencies on entry',async()=>{
 const server=vi.fn(()=>({state:'off'}))
 const f=await open(server)
 expect(f.tabs()).toEqual(['connection','join','server'])
 expect(f.page.querySelector('[data-tab-panel="join"] [data-nb-connect]')).toBeTruthy()
 expect(server).toHaveBeenCalledWith('netbirdserver.status',{})
 await f.openServer()
 expect(server).toHaveBeenCalledWith('netbirdserver.status',{})
 f.done()
})

test('connected, the server tab follows Apps',async()=>{
 const f=await open(()=>({state:'off'}),true)
 expect(f.tabs()).toEqual(['connection','apps','server'])
 f.done()
})

test('without Cloudflare on an own domain, the tab says why and opens Cloudflare instead of offering setup',async()=>{
 const f=await open(()=>({state:'off',prerequisite:'cloudflare_own_domain_required'}))
 await f.openServer()
 const prereq=f.tab().querySelector<HTMLElement>('[data-nbserver-prereq]')!
 expect(prereq.textContent).toContain(en('nbserver.code.cloudflare_own_domain_required'))
 expect(f.tab().querySelector('[data-nbserver-prereq-form]')?.textContent).toContain(en('nbserver.prereq.why'))
 expect(f.tab().querySelector('[data-nbserver-setup]')).toBeNull()
 expectNoControls(prereq)
 f.tab().querySelector<HTMLElement>('[data-nbserver-prereq-form] [data-nbserver-cloudflare]')!.click();await settle()
 expect(f.root.querySelector<HTMLElement>('[data-page="cloudflare"]')).toBeTruthy()
 f.done()
})

test('one button starts setup; the steps show, and a failed step offers to continue',async()=>{
 let current:ServerStatus={state:'off',zone:'example.com'}
 const f=await open(method=>{
  if(method==='netbirdserver.setup')current={state:'working',steps:[{id:'prerequisites',state:'done'},{id:'binary',state:'running'},{id:'config',state:'pending'}]}
  return current
 })
 await f.openServer()
 expect(f.tab().textContent).toContain('example.com')
 f.tab().querySelector<HTMLElement>('[data-nbserver-setup]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('netbirdserver.setup',{})
 expect(f.tab().querySelector('[data-nbserver-steps]')!.textContent).toContain(en('nbserver.step.binary'))
 expect(f.tab().querySelector('[data-nbserver-working]')).toBeTruthy()
 current={state:'failed',code:'netbird_server_grpc_blocked',steps:[{id:'binary',state:'done'},{id:'check',state:'failed',code:'netbird_server_grpc_blocked'}]}
 // Opening the tab again reads the status again.
 f.page.querySelector<HTMLElement>('[data-ui-tab-value="join"]')!.click();await settle();await f.openServer()
 const failure=f.tab().querySelector<HTMLElement>('[data-nbserver-failure]')!
 expect(failure.textContent).toContain(en('nbserver.code.netbird_server_grpc_blocked'))
 expect(f.tab().querySelector('[data-nbserver-cloudflare]')).toBeNull()
 expectNoControls(failure)
 // Closing the note leaves the step's buttons on the page.
 failure.querySelector<HTMLElement>('[data-ui-callout-close]')!.click();await settle()
 expect(failure.hidden).toBe(true)
 f.tab().querySelector<HTMLElement>('[data-nbserver-failure-form] [data-nbserver-continue]')!.click();await settle()
 expect(f.call.mock.calls.filter(c=>c[0]==='netbirdserver.setup')).toHaveLength(2)
 f.done()
})

test('running, the tab shows the server facts in 1024-based sizes and the password only on request',async()=>{
 const running:ServerStatus={state:'running',managementUrl:'https://netbird.example.com',version:'0.81.5',peers:3,peersOnline:2,uptimeSeconds:3600,memoryBytes:24*1024*1024,dataBytes:512*1024,adminEmail:'admin@netbird.example.com',joined:true}
 const f=await open(method=>method==='netbirdserver.credentials'?{email:'admin@netbird.example.com',password:'SYNTHETIC-PASSWORD'}:running)
 await f.openServer()
 const tab=f.tab()
 expect(tab.querySelector<HTMLInputElement>('input[data-nbserver-url]')?.value).toBe('https://netbird.example.com')
 expect(tab.querySelector('[data-nbserver-url] a')).toBeNull()
 expect(tab.querySelector('[data-nbserver-dashboard-guide]')?.textContent).toContain(en('nbserver.dashboardHelp'))
 expect(tab.querySelector('[data-nbserver-dashboard-guide] a')?.getAttribute('href')).toContain('docs/netbird-server.md#optional-web-dashboard')
 expect(tab.textContent).toContain('0.81.5')
 expect(tab.querySelector('[data-nbserver-peers]')!.textContent).toContain('2 online of 3')
 expect(tab.querySelector('[data-nbserver-memory]')!.textContent).toContain('24 MB')
 expect(tab.querySelector('[data-nbserver-joined]')).toBeTruthy()
 expect(tab.innerHTML).not.toContain('SYNTHETIC-PASSWORD')
 tab.querySelector<HTMLElement>('[data-nbserver-show-password]')!.click();await settle()
 const shown=()=>[...tab.querySelectorAll<HTMLInputElement>('input,textarea')].map(e=>e.value)
 expect(shown()).toContain('SYNTHETIC-PASSWORD')
 // Leaving the tab forgets it.
 f.page.querySelector<HTMLElement>('[data-ui-tab-value="join"]')?.click();await settle()
 expect(shown()).not.toContain('SYNTHETIC-PASSWORD')
 f.done()
})

test('turning off asks first and can delete the data; a name left on Cloudflare offers to remove it',async()=>{
 let current:ServerStatus={state:'running',managementUrl:'https://netbird.example.com'}
 const f=await open(method=>{
  if(method==='netbirdserver.disable')current={state:'off',cleanupHostname:'netbird.example.com'}
  return current
 })
 await f.openServer()
 f.tab().querySelector<HTMLElement>('[data-nbserver-off]')!.click();await settle()
 const dialog=document.querySelector<HTMLElement>('[data-nbserver-off-dialog]')!
 expect(dialog.textContent).toContain(en('nbserver.offLine'))
 const remove=dialog.querySelector<HTMLElement>('[data-nbserver-remove-data] [role=checkbox], [data-nbserver-remove-data] input, [data-nbserver-remove-data]')!
 remove.click();await settle()
 document.querySelector<HTMLElement>('[data-nbserver-off-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('netbirdserver.disable',{removeData:true})
 const cleanup=f.tab().querySelector<HTMLElement>('[data-nbserver-cleanup]')!
 expect(cleanup.textContent).toContain('netbird.example.com')
 cleanup.querySelector<HTMLElement>('[data-nbserver-cleanup-retry]')!.click();await settle()
 expect(f.call).toHaveBeenLastCalledWith('netbirdserver.status',{})
 expect(f.call).toHaveBeenCalledWith('netbirdserver.disable',{})
 f.done()
})

test('an unreadable status says so instead of offering setup',async()=>{
 const f=await open(()=>{throw Object.assign(new Error('down'),{code:'netbird_server_unavailable'})})
 await f.openServer()
 expect(f.tab().querySelector('[data-nbserver-unavailable]')).toBeTruthy()
 expect(f.tab().querySelector('[data-nbserver-setup]')).toBeNull()
 f.done()
})

test('waiting for the zone gRPC setting links its Network page and keeps going by itself',async()=>{
 expect(grpcSettingsUrl('example.com')).toBe('https://dash.cloudflare.com/?to=/:account/example.com/network')
 const waiting:ServerStatus={state:'working',zone:'example.com',steps:[{id:'bridge',state:'done'},{id:'check',state:'running',code:'netbird_server_grpc_blocked'},{id:'join',state:'pending'}]}
 expect(waitingForGRPC(waiting.steps)).toBe(true)
 expect(stepItems(waiting.steps,en)[1]).toEqual({title:en('nbserver.step.check'),status:'current',description:en('nbserver.guide.title')})
 const opened=vi.spyOn(window,'open').mockImplementation(()=>null)
 const f=await open(()=>waiting)
 await f.openServer()
 const guide=f.tab().querySelector<HTMLElement>('[data-nbserver-guide]')!
 expect(guide.textContent).toContain(en('nbserver.guide.text'))
 expect(f.tab().querySelector('[data-nbserver-failure]')).toBeNull()
 expectNoControls(guide)
 f.tab().querySelector<HTMLElement>('[data-nbserver-guide-form] [data-nbserver-grpc]')!.click();await settle()
 expect(opened).toHaveBeenCalledWith('https://dash.cloudflare.com/?to=/:account/example.com/network','_blank','noopener')
 opened.mockRestore()
 f.done()
})

test('without a token, the tab takes one that never publishes DSM',async()=>{
 let current:ServerStatus={state:'off',prerequisite:'netbird_server_workers_required'}
 const f=await open((method)=>{if(method==='netbirdserver.status')return current;return current})
 await f.openServer()
 const prereq=f.tab().querySelector<HTMLElement>('[data-nbserver-prereq]')!
 expect(prereq.textContent).toContain('Workers Routes Edit')
 expectNoControls(prereq)
 const form=f.tab().querySelector<HTMLElement>('[data-nbserver-prereq-form]')!
 expect(form.textContent).toContain(en('nbserver.token.help'))
 expect(f.tab().querySelector('[data-nbserver-setup]')).toBeNull()
 expect(f.tab().querySelector('[data-nbserver-cloudflare]')).toBeNull()
 const input=form.querySelector<HTMLInputElement>('[data-nbserver-token-input]')!
 input.value='SYNTHETIC-TOKEN';input.dispatchEvent(new Event('input'));await settle()
 current={state:'off',zone:'example.com'}
 form.querySelector<HTMLElement>('[data-nbserver-token-save]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.relayconnect',{token:'SYNTHETIC-TOKEN',noDsm:true})
 expect(f.call.mock.calls.some(c=>c[0]==='apps.workerspublishdsm'||String(c[0]).includes('publish'))).toBe(false)
 expect(f.tab().innerHTML).not.toContain('SYNTHETIC-TOKEN')
 expect(f.tab().querySelector('[data-nbserver-setup]')).toBeTruthy()
 f.done()
})

test('a saved token without the route permission is named before setup; the shared create-token link replaces an insufficient token',async()=>{
 let current:ServerStatus={state:'off',zone:'example.com',prerequisite:'workers_routes_permission'}
 const f=await open(()=>current)
 await f.openServer()
 const prereq=f.tab().querySelector<HTMLElement>('[data-nbserver-prereq]')!
 expect(prereq.textContent).toContain('Workers Routes Edit')
 expect(f.tab().querySelector('[data-nbserver-setup]')).toBeNull()
 const edit=f.tab().querySelector<HTMLAnchorElement>('[data-nbserver-token-edit]')!
 expect(edit.getAttribute('href')).toBe(relayTokenUrl())
 // A newly created replacement token is saved before checking again.
 current={state:'off',zone:'example.com'}
 const reads=f.call.mock.calls.filter(c=>c[0]==='netbirdserver.status').length
 f.tab().querySelector<HTMLElement>('[data-nbserver-token-check]')!.click();await settle()
 expect(f.call.mock.calls.filter(c=>c[0]==='netbirdserver.status').length).toBeGreaterThan(reads)
 expect(f.call.mock.calls.some(c=>c[0]==='apps.relayconnect')).toBe(false)
 expect(f.tab().querySelector('[data-nbserver-setup]')).toBeTruthy()
 f.done()
})

test('the shared token link and instructions retain all four permissions',()=>{
 const url=new URL(relayTokenUrl())
 expect(JSON.parse(url.searchParams.get('permissionGroupKeys')!)).toEqual([{key:'workers_scripts',type:'edit'},{key:'workers_routes',type:'edit'},{key:'access',type:'edit'},{key:'access_acct',type:'edit'}])
 expect(url.searchParams.get('accountId')).toBe('*')
 expect(url.searchParams.get('zoneId')).toBe('all')
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  const line=translate(locale,'apps.relay.step.tokenLine')
  expect(line,locale).toContain('Workers Scripts › Edit')
  expect(line,locale).toContain('Workers Routes › Edit')
  expect(line,locale).toContain('Access: Apps and Policies Edit')
  expect(line,locale).toContain('Access: Organizations, Identity Providers, and Groups Edit')
 }
})

test('a missing route permission names it, takes a new token and continues setup',async()=>{
 const f=await open((method)=>method==='netbirdserver.setup'?{state:'working',steps:[]}:{state:'failed',code:'workers_routes_permission',steps:[{id:'route',state:'done'},{id:'bridge',state:'failed',code:'workers_routes_permission'}]})
 await f.openServer()
 const failure=f.tab().querySelector<HTMLElement>('[data-nbserver-failure]')!
 expect(failure.textContent).toContain('Workers Routes Edit')
 expectNoControls(failure)
 const form=f.tab().querySelector<HTMLElement>('[data-nbserver-failure-form]')!
 expect(form.querySelector('[data-nbserver-cloudflare]')).toBeNull()
 expect(form.querySelector('[data-nbserver-token-edit]')).toBeTruthy()
 const input=form.querySelector<HTMLInputElement>('[data-nbserver-token-input]')!
 input.value='SYNTHETIC-TOKEN';input.dispatchEvent(new Event('input'));await settle()
 form.querySelector<HTMLElement>('[data-nbserver-token-save]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.relayconnect',{token:'SYNTHETIC-TOKEN',noDsm:true})
 expect(f.call).toHaveBeenCalledWith('netbirdserver.setup',{})
 f.done()
})

test('running without the Workers token, the tab names authorization and takes a token',async()=>{
 let current:ServerStatus={state:'running',prerequisite:'netbird_server_workers_required',managementUrl:'https://netbird.example.com'}
 const f=await open(()=>current)
 await f.openServer()
 const bridge=f.tab().querySelector<HTMLElement>('[data-nbserver-bridge]')!
 expect(bridge.textContent).toContain(en('nbserver.code.netbird_server_workers_required'))
 expect(bridge.textContent).not.toContain(en('nbserver.bridge.title'))
 expect(bridge.textContent).toContain('Workers Routes Edit')
 expect(f.tab().querySelector('[data-nbserver-bridge-form]')).toBeTruthy()
 expect(f.tab().querySelector('[data-nbserver-status]')).toBeTruthy()
 expectNoControls(bridge)
 bridge.querySelector<HTMLElement>('[data-ui-callout-close]')!.click();await settle()
 const form=f.tab().querySelector<HTMLElement>('[data-nbserver-bridge-form]')!
 const input=form.querySelector<HTMLInputElement>('[data-nbserver-token-input]')!
 input.value='SYNTHETIC-TOKEN';input.dispatchEvent(new Event('input'));await settle()
 current={state:'running',managementUrl:'https://netbird.example.com'}
 form.querySelector<HTMLElement>('[data-nbserver-token-save]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.relayconnect',{token:'SYNTHETIC-TOKEN',noDsm:true})
 expect(f.call).not.toHaveBeenCalledWith('netbirdserver.setup',expect.anything())
 expect(f.tab().querySelector('[data-nbserver-bridge]')).toBeNull()
 expect(f.tab().querySelector('[data-nbserver-bridge-form]')).toBeNull()
 f.done()
})

test('the Free link names the self-hosted server when it refuses to turn off',()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const key of ['cfpage.code.workers_netbird_server_in_use','nbserver.bridge.title'])expect(translate(locale,key),`${locale}:${key}`).not.toBe(key)
})

test('a running server shows its disconnected Cloudflare prerequisite, with a direct recovery action',async()=>{
 const f=await open(()=>({state:'running',hostname:'netbird.example.com',prerequisite:'cloudflare_not_connected',dependencies:{enabled:true,problem:{code:'cloudflare_not_connected',piece:'tunnel',hop:'tunnel',action:'connect_cloudflare',page:'cloudflare',impact:'connection'}}}))
 await f.openServer()
 const problem=f.page.querySelector('[data-server-dependency]')
 expect(problem?.textContent).toContain(en('dependency.code.cloudflare_not_connected'))
 expect(problem?.querySelector('[data-dependency-action]')).toBeTruthy()
 expect(f.tab().textContent).not.toContain(en('nbserver.joined'))
 f.done()
})

test('server shutdown failures stay inside the confirmation dialog',async()=>{
 const f=await open(method=>{if(method==='netbirdserver.disable')throw {code:'netbird_server_leave_failed'};return {state:'running',hostname:'netbird.example.com'}})
 await f.openServer();f.tab().querySelector<HTMLElement>('[data-nbserver-off]')!.click();await settle()
 document.querySelector<HTMLElement>('[data-nbserver-off-confirm]')!.click();await settle()
 expect(document.querySelector('[data-nbserver-off-dialog] [data-nbserver-action-error]')?.textContent).toContain(en('nbserver.code.netbird_server_leave_failed'))
 f.done()
})

test('a foreign object guide stays inside its failed step and Retry resumes setup',async()=>{
 let current={state:'failed',code:'workers_name_conflict',steps:[{id:'bridge',state:'failed',code:'workers_name_conflict',conflict:{reason:'other',objectType:'worker',conflictName:'connect-nbsignal-example',dashboardUrl:'https://dash.cloudflare.com/account/workers/services/view/connect-nbsignal-example/production/settings'}}]} as unknown as ServerStatus
 const f=await open(method=>{if(method==='netbirdserver.setup')current={state:'working',steps:[{id:'bridge',state:'running'}]};return current})
 await f.openServer()
 const guide=f.tab().querySelector<HTMLElement>('[data-resource-conflict-guide]')!
 expect(guide).toBeTruthy()
 expect(guide.querySelector<HTMLInputElement>('input[data-resource-conflict-name]')?.value).toBe('connect-nbsignal-example')
 expect(guide.querySelector<HTMLAnchorElement>('[data-resource-conflict-open]')?.href).toContain('/connect-nbsignal-example/production/settings')
 expect(guide.textContent).toContain('Workers & Pages')
 guide.querySelector<HTMLElement>('[data-ui-callout-close]')!.click();await settle()
 expect(guide.querySelector('[data-resource-conflict-retry]')).toBeTruthy()
 guide.querySelector<HTMLElement>('[data-resource-conflict-retry]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('netbirdserver.setup',{})
 expect(f.tab().querySelector('[data-resource-conflict-guide]')).toBeNull()
 expect(f.tab().querySelector('[data-nbserver-working]')).toBeTruthy()
 f.done()
})


test('choosing another Worker name requests a fresh name without granting overwrite',async()=>{
 let current={state:'failed',code:'workers_name_conflict',steps:[{id:'bridge',state:'failed',conflict:{reason:'other',objectType:'worker',conflictName:'connect-nbsignal-example',suggestedName:'connect-nbsignal-example-2'}}]} as unknown as ServerStatus
 const f=await open(method=>{if(method==='netbirdserver.setup')current={state:'working'};return current})
 await f.openServer();f.tab().querySelector<HTMLElement>('[data-resource-conflict-alternative]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('netbirdserver.setup',{useAnotherName:true})
 expect(f.tab().querySelector('[data-resource-conflict-guide]')).toBeNull();f.done()
})

test('missing Worker authorization never labels existing online devices disconnected',async()=>{
 const f=await open(()=>({state:'running',joined:true,peers:2,peersOnline:2,prerequisite:'workers_not_connected',dependencies:{enabled:true,problem:{code:'workers_not_connected',piece:'token',action:'save_token',page:'netbird',hop:'bridge',impact:'maintenance'}}}),true)
 await f.openServer()
 const notice=f.tab().querySelector('[data-nbserver-bridge]')!
 expect(notice.textContent).toContain(en('dependency.code.workers_not_connected'))
 expect(notice.textContent).not.toContain(en('nbserver.bridge.title'))
 expect(f.tab().querySelector('[data-nbserver-token-input]')).toBeTruthy()
 f.done()
})
