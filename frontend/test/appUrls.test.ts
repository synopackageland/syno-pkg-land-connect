// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {fingerprint,pauseReasons,protectionLines,rowStatus,type AppRow,type AppsView,type Inspection} from '../src/connect/appUrls'
import {settle} from './support'

const a=(key:string)=>translate('en','apps.'+key)
const dsmPin={mode:'W' as const,names:['nas.example.com'],issuer:'Example CA',notAfter:'2027-01-31T00:00:00Z'}
const base=():AppsView=>({
 private:{available:true,host:'nas.tailnet.ts.net'},
 public:{available:true,connected:true,zone:'example.com'},
 apps:[
  {package:'SynoPkgOff',name:'Off App',port:28001,publicCapable:true,protection:{}},
  {package:'SynoPkgOn',name:'On App',port:28002,publicCapable:true,protection:{upstreamAccess:'public'},private:{state:'active',url:'https://nas.tailnet.ts.net:28002/'}},
  {package:'SynoPkgWait',name:'Wait App',port:28003,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28003/',reachable:false}},
  {package:'SynoPkgRoute',name:'Route App',port:28004,protection:{},private:{state:'paused',reason:'route_mismatch',url:'https://nas.tailnet.ts.net:28004/'}},
  {package:'SynoPkgClean',name:'Clean App',port:28005,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28005/',cleanup:true}},
  {package:'SynoPkgGone',name:'Gone App',port:28006,removed:true,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28006/'}},
  {package:'SynoPkgOdd',name:'Odd App',port:28008,protection:{},private:{state:'paused',reason:'something new',url:'https://nas.tailnet.ts.net:28008/'}},
  {package:'SynoPkgOld',name:'Old App',port:28007,needsUpdate:true,protection:{passthrough:['/api/public']}},
 ],
})
const serving={provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com'}
const needsDsm={provider:'cloudflare',state:'failed',code:'pin_proxy_required',mode:'domain',hostname:'nas.example.com'}
const inspection=(pkg:string,over:Partial<Inspection>={}):Inspection=>({ready:true,package:pkg,name:pkg.replace('SynoPkg','')+' App',port:28001,protection:{passthrough:['/share']},pin:dsmPin,...over})

function fixture(view:AppsView=base(),handlers:Record<string,(params:Record<string,unknown>)=>unknown>={},connections:unknown[]=[]){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return handlers[method]?handlers[method]!(params):{provider:'multiple',state:'connected',connections}
  if(method==='netbirdserver.status')return {state:'off',dependencies:{enabled:false}}
  if(method==='apps.list')return handlers['apps.list']?handlers['apps.list'](params):view
  if(handlers[method])return handlers[method]!(params)
  return {}
 })
 const ctx=createStandaloneContext({call,locale:'en'})
 const app=mount(root,ctx)
 return {root,call,done(){app.unmount();root.remove()}}
}
async function open(root:HTMLElement){[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='App URLs')!.click();await settle()}
const page=(root:HTMLElement)=>root.querySelector<HTMLElement>('[data-page="apps"]')!
// Each row's marker sits in its App cell; the row is the table row around it.
const row=(root:HTMLElement,pkg:string)=>page(root).querySelector<HTMLElement>(`[data-app-row="${pkg}"]`)!.closest<HTMLElement>('tr')!
const dialog=()=>document.querySelector<HTMLElement>('[data-apps-approval]')
afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

test('the list is read only when the page opens or is refreshed',async()=>{
 const f=fixture();await settle()
 expect(f.call.mock.calls.map(c=>c[0])).not.toContain('apps.list')
 await open(f.root)
 expect(f.call.mock.calls.filter(c=>c[0]==='apps.list')).toHaveLength(1)
 f.done()
})

test('a busy NAS read keeps the last list, says so and tries again',async()=>{
 vi.useFakeTimers()
 try{
  let fail=1,reads=0
  const f=fixture(base(),{'apps.list':()=>{reads++;if(fail-->0)throw {code:'apps_unavailable'};return base()}})
  await settle();await open(f.root)
  expect(page(f.root).querySelector('[data-apps-error]')).toBeNull()
  expect(page(f.root).querySelector('[data-apps-loading]')?.textContent).toContain(a('busy'))
  await vi.advanceTimersByTimeAsync(2000);await settle()
  expect(reads).toBe(2)
  expect(page(f.root).querySelector('[data-apps-table]')).toBeTruthy()
  expect(page(f.root).querySelector('[data-apps-busy]')).toBeNull()
  fail=2
  ;[...f.root.querySelectorAll<HTMLElement>('button')].find(b=>b.textContent?.trim()===translate('en','ux.refresh'))!.click();await settle()
  expect(page(f.root).querySelector('[data-app-row="SynoPkgOn"]')).toBeTruthy()
  expect(page(f.root).querySelector('[data-apps-busy]')?.textContent).toContain(a('busy'))
  expect(page(f.root).querySelector('[data-apps-error]')).toBeNull()
  await vi.advanceTimersByTimeAsync(2000);await settle()
  expect(reads).toBe(4);expect(page(f.root).querySelector('[data-apps-busy]')).toBeTruthy()
  await vi.advanceTimersByTimeAsync(4000);await settle()
  expect(reads).toBe(5);expect(page(f.root).querySelector('[data-apps-busy]')).toBeNull()
  f.done()
 }finally{vi.useRealTimers()}
})

test('a refused read is an error and leaving the page stops busy retries',async()=>{
 vi.useFakeTimers()
 try{
  let code='apps_state_corrupt',reads=0
  const f=fixture(base(),{'apps.list':()=>{reads++;throw {code}}})
  await settle();await open(f.root)
  expect(page(f.root).querySelector('[data-apps-error]')?.textContent).toContain(a('code.apps_state_corrupt'))
  await vi.advanceTimersByTimeAsync(60000);await settle()
  expect(reads).toBe(1)
  code='apps_unavailable'
  ;[...f.root.querySelectorAll<HTMLElement>('button')].find(b=>b.textContent?.trim()===translate('en','ux.refresh'))!.click();await settle()
  expect(reads).toBe(2);expect(page(f.root).querySelector('[data-apps-error]')).toBeNull()
  ;[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Tailscale')!.click();await settle()
  await vi.advanceTimersByTimeAsync(60000);await settle()
  expect(reads).toBe(2)
  f.done()
 }finally{vi.useRealTimers()}
})

test('the Cloudflare sign-in is disconnected on the Cloudflare page, which explains what stays and deletes only the sign-in',async()=>{
 const f=fixture();await settle();await open(f.root)
 expect(page(f.root).querySelector('[data-cf-disconnect]')).toBeNull()
 ;[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
 f.root.querySelector<HTMLElement>('[data-page="cloudflare"]')!.querySelector<HTMLElement>('[data-cf-disconnect]')!.click();await settle()
 expect(document.querySelector('[data-cf-disconnect-dialog]')?.textContent).toContain(a('cf.disconnectLine'))
 document.querySelector<HTMLElement>('[data-cf-disconnect-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.cloudflaredisconnect',{})
 f.done()
})

test('rowStatus and protection lines follow the spec order',()=>{
 const r:AppRow={package:'p',name:'p',port:1,protection:{}}
 expect(rowStatus(r,'private')).toBe('off')
 expect(rowStatus(r,'public')).toBe('needs-update')
 expect(rowStatus({...r,removed:true,private:{state:'paused',url:''}},'private')).toBe('removed')
 expect(rowStatus({...r,private:{state:'paused',url:'',cleanup:true}},'private')).toBe('cleanup')
 expect(rowStatus({...r,private:{state:'paused',url:'',reachable:false}},'private')).toBe('paused')
 expect(rowStatus({...r,private:{state:'paused',reason:'removed',url:''}},'private')).toBe('removed')
 expect(protectionLines({upstreamAccess:'public',passthrough:['/a'],optionalPassthrough:['/b']},'public').map(l=>l.key)).toEqual(['ownSignIn','passthrough','optional','publicDsm'])
 expect(fingerprint('0aFF')).toBe('0A:FF')
 for(const reason of pauseReasons)expect(a('reason.'+reason),reason).not.toBe('apps.reason.'+reason)
})

// Give each independent UI scenario its own timeout and fixture cleanup.
test.each([
 ['serving',[serving],'serving','cf.connected','success'],
 ['needs DSM trust',[needsDsm],'stopped','cf.notServing','danger'],
 ['off',[],'stopped','cf.notServing','danger'],
] as const)('the Cloudflare chip follows the tunnel, not only the authorization: %s',async(_case,connections,health,key,tone)=>{
  const f=fixture(base(),{},[...connections]);await settle();await open(f.root)
  // Apps also offers recovery when the tunnel needs DSM trust.
  expect(!!page(f.root).querySelector('[data-dsm-review-open]')).toBe(connections.some(c=>c.code==='pin_proxy_required'))
  expect(page(f.root).querySelector('[data-cf-zone]')).toBeNull()
  ;[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
  const chip=f.root.querySelector<HTMLElement>('[data-page="cloudflare"] [data-cf-zone]')!
  expect(chip,health+String(connections.length)).toBeTruthy()
  expect(chip.dataset.cfHealth).toBe(health)
  expect(chip.textContent).toContain(a(key).replace('{zone}','example.com'))
  expect(chip.outerHTML).toContain(tone)
  f.done()
})

const pinS={mode:'S' as const,certSha256:'ca'.repeat(32),spki:'3f9a0c'.repeat(10)+'ab12',issuer:'Synology Inc. CA',notAfter:'2030-06-30T00:00:00Z'}
const dsmHandlers=()=>({'apps.inspectdsm':()=>({scope:'public',dsm:{port:5001,pin:pinS},token:'dsm-review'}),'apps.setdsm':()=>({approved:true})})
async function approveDsm(f:ReturnType<typeof fixture>,where:HTMLElement){
 const review=where.querySelector<HTMLElement>('[data-dsm-review]')!
 expect(review.textContent).toContain(a('dsmReview.title'))
 expect(review.textContent).toContain(a('dsmReview.line'))
 review.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.inspectdsm',{scope:'public'})
 const d=dialog()!
 expect(d.hasAttribute('data-dsm-only')).toBe(true)
 expect(d.textContent).toContain(a('approve.dsmOnly'))
 expect(d.querySelector('[data-approve-dsm]')?.textContent).toContain(a('approve.dsmSection').replace('{port}','5001'))
 expect(d.querySelector('[data-approve-dsm]')?.textContent).toContain(fingerprint(pinS.certSha256))
 expect(d.querySelector('[data-approve-dsm]')?.textContent).toContain('Synology Inc. CA')
 expect(d.querySelector('[data-dsm-change-reason]')?.textContent).toBe(a('dsmReview.line'))
 expect(d.querySelector('[data-approve-notes]')).toBeNull()
 // App-only lines stay out of a DSM-only approval.
 for(const only of ['[data-approve-app]','[data-approve-notes]'])expect(d.querySelector(only),only).toBeNull()
 expect(f.call.mock.calls.map(c=>c[0])).not.toContain('apps.setdsm')
 d.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.setdsm',{scope:'public',dsm:{port:5001,pin:pinS},token:'dsm-review'})
}

test('Home: a domain that needs the DSM certificate reviewed is under Needs attention, and the review sends what was shown',async()=>{
 const f=fixture(base(),dsmHandlers(),[needsDsm]);await settle()
 const overview=f.root.querySelector<HTMLElement>('[data-page="status"]')!
 expect(overview.querySelector('[data-lane-reason]')).toBeNull()
 expect(overview.querySelectorAll('[data-needs-attention] [data-dsm-review]')).toHaveLength(1)
 await approveDsm(f,overview)
 f.done()
})

test('Cloudflare page: the DSM review replaces the raw failure and approval refreshes the status',async()=>{
 const f=fixture(base(),dsmHandlers(),[needsDsm]);await settle()
 ;[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')!.click();await settle()
 const cfPage=f.root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 expect(cfPage.querySelector('[data-connection-reason]')).toBeNull()
 expect(cfPage.textContent).not.toContain('pin_proxy_required ')
 const before=f.call.mock.calls.filter(c=>c[0]==='connection.status').length
 await approveDsm(f,cfPage)
 expect(f.call.mock.calls.filter(c=>c[0]==='connection.status').length).toBeGreaterThan(before)
 f.done()
})

test('an already approved DSM certificate opens no dialog, and a stale review is explained',async()=>{
 const f=fixture(base(),{'apps.inspectdsm':()=>({scope:'public'})},[needsDsm]);await settle()
 f.root.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 expect(dialog()).toBeNull()
 expect(document.body.textContent).toContain(a('dsmReview.current'))
 f.done()
 const g=fixture(base(),{...dsmHandlers(),'apps.setdsm':()=>{throw Object.assign(new Error('changed'),{code:'apps_confirmation_changed'})}},[needsDsm]);await settle()
 g.root.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 dialog()!.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 expect(document.body.textContent).toContain(a('code.apps_confirmation_changed'))
 g.done()
})

// Tailscale routes DSM through the same pinned proxy, for the private scope.
const tsNeedsDsm={provider:'tailscale',state:'failed',code:'pin_proxy_required',mappings:[]}
const privateHandlers=()=>({'apps.inspectdsm':()=>({scope:'private',dsm:{port:5001,pin:pinS},token:'dsm-private'}),'apps.setdsm':()=>({approved:true})})
async function approvePrivateDsm(f:ReturnType<typeof fixture>,where:HTMLElement){
 const review=where.querySelector<HTMLElement>('[data-dsm-review][data-dsm-review-provider="tailscale"]')!
 expect(review.textContent).toContain(a('dsmReview.private.title'))
 expect(review.textContent).toContain(a('dsmReview.private.line'))
 review.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.inspectdsm',{scope:'private'})
 const d=dialog()!
 expect(d.dataset.scope).toBe('private')
 expect(d.hasAttribute('data-dsm-only')).toBe(true)
 expect(d.textContent).toContain(a('approve.dsmOnly'))
 expect(d.querySelector('[data-approve-dsm]')?.textContent).toContain(fingerprint(pinS.certSha256))
 expect(d.querySelector('[data-dsm-change-reason]')?.textContent).toBe(a('dsmReview.line'))
 expect(d.querySelector('[data-approve-notes]')).toBeNull()
 // Cloudflare's public lines stay out of a private approval.
 for(const only of ['[data-approve-app]','[data-approve-notes]'])expect(d.querySelector(only),only).toBeNull()
 d.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.setdsm',{scope:'private',dsm:{port:5001,pin:pinS},token:'dsm-private'})
}

test('Tailscale page: the DSM review replaces the raw failure and approves the private scope',async()=>{
 const f=fixture(base(),privateHandlers(),[tsNeedsDsm]);await settle()
 ;[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Tailscale')!.click();await settle()
 const tsPage=f.root.querySelector<HTMLElement>('[data-page="tailscale"]')!
 expect(tsPage.querySelector('[data-connection-reason]')).toBeNull()
 const before=f.call.mock.calls.filter(c=>c[0]==='connection.status').length
 await approvePrivateDsm(f,tsPage)
 expect(f.call.mock.calls.filter(c=>c[0]==='connection.status').length).toBeGreaterThan(before)
 f.done()
})

test('Home and App URLs offer the private DSM review beside the Cloudflare one',async()=>{
 const f=fixture(base(),privateHandlers(),[needsDsm,tsNeedsDsm]);await settle()
 const overview=f.root.querySelector<HTMLElement>('[data-page="status"]')!
 expect([...overview.querySelectorAll<HTMLElement>('[data-needs-attention] [data-dsm-review]')].map(r=>r.dataset.dsmReviewProvider)).toEqual(['cloudflare','tailscale'])
 expect(overview.querySelector('[data-lane-reason]')).toBeNull()
 await approvePrivateDsm(f,overview)
 f.done()
})

test('a private review with DSM already approved points to the private Apps',async()=>{
 const f=fixture(base(),{'apps.inspectdsm':()=>({scope:'private'})},[tsNeedsDsm]);await settle()
 f.root.querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 expect(dialog()).toBeNull()
 expect(document.body.textContent).toContain(a('dsmReview.private.current'))
 f.done()
})

test('pin_proxy_required and every apps code are localized in all 9 locales without a trailing period',()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  for(const key of ['title','line','current']){
   const text=translate(locale,'apps.dsmReview.private.'+key)
   expect(text,`${locale} ${key}`).not.toBe('apps.dsmReview.private.'+key)
   expect(text,`${locale} ${key}`).not.toMatch(/[.。]$/)
  }
  for(const code of ['pin_proxy_required','apps_confirmation_changed','apps_unavailable','apps_state_corrupt','apps_waiting','apps_update_required','apps_cleanup_required']){
   const text=translate(locale,'apps.code.'+code)
   expect(text,`${locale} ${code}`).not.toBe('apps.code.'+code)
   expect(text,`${locale} ${code}`).not.toMatch(/[.。]$/)
  }
 }
})

test('unreadable records can be reset after a confirmation that names the backup',async()=>{
 const corrupt=()=>{const v=base();v.stateCorrupt=true;return v}
 let reset=false
 const f=fixture(base(),{'apps.list':()=>reset?base():corrupt(),'apps.resetstate':p=>{expect(p).toEqual({confirm:true});reset=true;return {backup:'apps-state.corrupt-x.json'}}})
 await settle();await open(f.root)
 page(f.root).querySelector<HTMLElement>('[data-apps-reset]')!.click();await settle()
 const dialog=document.querySelector<HTMLElement>('[data-apps-reset-dialog]')!
 expect(dialog.textContent).toContain(a('reset.backup'))
 expect(f.call).not.toHaveBeenCalledWith('apps.resetstate',expect.anything())
 document.querySelector<HTMLElement>('[data-apps-reset-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('apps.resetstate',{confirm:true})
 expect(page(f.root).querySelector('[data-apps-corrupt]')).toBeNull()
 f.done()
})

test('while connections restore the list shows, says it is starting and reads again',async()=>{
 vi.useFakeTimers()
 try{
  let reads=0
  const starting=()=>{const v=base();v.private={available:false,code:'tailscale-off'};v.public={available:false,connected:false};v.starting=true;return v}
  const f=fixture(base(),{'apps.list':()=>++reads===1?starting():base()})
  await settle();await open(f.root)
  expect(page(f.root).querySelector('[data-apps-starting]')?.textContent).toContain(a('starting'))
  expect(page(f.root).querySelector('[data-app-row="SynoPkgOn"]')).toBeTruthy()
  expect(page(f.root).querySelector('[data-apps-private-off]')).toBeNull()
  expect(page(f.root).querySelector('[data-apps-public-off]')).toBeNull()
  await vi.advanceTimersByTimeAsync(2000);await settle()
  expect(reads).toBe(2);expect(page(f.root).querySelector('[data-apps-starting]')).toBeNull()
  f.done()
 }finally{vi.useRealTimers()}
})

test('public hostname is prefilled, editable and bound to a fresh approval with inline collision recovery',async()=>{
 const original='off-a1b2c3d4.example.com',chosen='my-app.example.com'
 const f=fixture(base(),{
  'apps.inspect':p=>inspection(String(p.package),{hostname:String(p.hostname||original),token:String(p.hostname||original)}),
  'apps.setpublic':()=>{throw {code:'cloudflare_name_taken',details:{reason:'other',conflictName:chosen,suggestedName:'my-app-2.example.com',dnsUrl:'https://dash.cloudflare.com/account/example.com/dns/records'}}},
 },[serving])
 await settle();await open(f.root)
 row(f.root,'SynoPkgOff').querySelector<HTMLElement>('[data-app-pick] button, [data-app-pick]')!.click();await settle()
 page(f.root).querySelector<HTMLElement>('[data-apps-batch-create="public"]')!.click();await settle()
 let input=dialog()!.querySelector<HTMLInputElement>('[data-approve-hostname="SynoPkgOff"] input')!
 expect(input.value).toBe(original.split('.')[0])
 input.value='';input.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 expect(dialog()!.querySelector<HTMLButtonElement>('[data-approve-confirm]')!.disabled).toBe(true)
 input.value=chosen.split('.')[0]!;input.dispatchEvent(new Event('input',{bubbles:true}));await new Promise(r=>setTimeout(r,400));await settle()
 expect(f.call.mock.calls.filter(c=>c[0]==='apps.setpublic')).toHaveLength(0)
 dialog()!.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 // Editing first refreshes the certificate/confirmation for the selected authority.
 expect(f.call.mock.calls.filter(c=>c[0]==='apps.inspect').at(-1)![1]).toMatchObject({hostname:chosen})
 expect(dialog()!.querySelector('[data-name-conflict]')?.textContent).toContain('Used by another service')
 expect(dialog()!.querySelector<HTMLAnchorElement>('[data-name-dns]')?.href).toBe('https://dash.cloudflare.com/account/example.com/dns/records')
 expect(f.call.mock.calls.find(c=>c[0]==='apps.setpublic')![1]).toMatchObject({approvals:{SynoPkgOff:{hostname:chosen,token:chosen}}})
 input=dialog()!.querySelector<HTMLInputElement>('[data-approve-hostname="SynoPkgOff"] input')!
 expect(input.value).toBe(chosen.split('.')[0])
 f.done()
})

test('the first workers.dev DSM hostname can be edited with the App hostname',async()=>{
 const view=base();view.relay={connected:true,subdomain:'example'}
 const f=fixture(view,{'apps.inspect':p=>inspection(String(p.package),{hostname:'my-app.example.workers.dev',token:'reviewed',dsm:{hostname:String(p.dsmHostname||'nas-a1b2c3d4.example.workers.dev'),port:5001,pin:dsmPin}}),'apps.setrelay':()=>({results:[{package:'SynoPkgOff',ok:true}]})})
 await settle();await open(f.root)
 row(f.root,'SynoPkgOff').querySelector<HTMLElement>('[data-app-pick] button, [data-app-pick]')!.click();await settle()
 page(f.root).querySelector<HTMLElement>('[data-apps-batch-create="relay"]')!.click();await settle()
 const input=dialog()!.querySelector<HTMLInputElement>('[data-approve-hostname=""] input')!
 expect(input.value).toBe('nas-a1b2c3d4')
 input.value='my-nas';input.dispatchEvent(new Event('input',{bubbles:true}));await new Promise(r=>setTimeout(r,400));await settle()
 dialog()!.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 expect(f.call.mock.calls.find(c=>c[0]==='apps.setrelay')![1]).toMatchObject({approvals:{SynoPkgOff:{hostname:'my-app.example.workers.dev',dsm:{hostname:'my-nas.example.workers.dev'}}}})
 f.done()
})

test('trusting a changed certificate clears every review surface after fresh status',async()=>{
 const connections=[{...needsDsm},{...tsNeedsDsm}]
 const nextPin={...pinS,previous:{issuer:'Previous issuer',notAfter:'2026-01-01T00:00:00Z'}}
 const f=fixture(base(),{
  'apps.inspectdsm':()=>({scope:'public',dsm:{port:5001,pin:nextPin},token:'changed'}),
  'apps.setdsm':()=>{for(const c of connections){c.state='connected';c.code=''};return {approved:true}},
 },connections)
 await settle();await open(f.root)
 for(const pageName of ['status','apps','cloudflare','tailscale']){
  const surface=f.root.querySelector(`[data-page="${pageName}"]`)!
  expect(surface.querySelector('[data-dsm-review-open]'),pageName).not.toBeNull()
 }
 page(f.root).querySelector<HTMLElement>('[data-dsm-review-open]')!.click();await settle()
 expect(dialog()!.querySelector('[data-dsm-cert-change]')?.textContent).toContain('Previous issuer')
 expect(dialog()!.querySelector('[data-dsm-cert-change]')?.textContent).toContain(translate('en','apps.dsmReview.replacement'))
 dialog()!.querySelector<HTMLElement>('[data-approve-confirm]')!.click();await settle()
 expect(f.root.querySelector('[data-dsm-review]')).toBeNull()
 expect(dialog()).toBeNull()
 f.done()
})

test('a valid but stale DSM route is refreshed immediately when inspection returns no review',async()=>{
 const connections=[{...tsNeedsDsm}]
 const f=fixture(base(),{'apps.inspectdsm':params=>{
  expect(params).toEqual({scope:'private'})
  connections[0]!.state='connected';connections[0]!.code=''
  return {scope:'private'}
 }},connections)
 await settle()
 f.root.querySelector<HTMLElement>('[data-page="tailscale"] [data-dsm-review-open]')!.click();await settle()
 expect(f.root.querySelector('[data-dsm-review]')).toBeNull()
 expect(f.call.mock.calls.filter(c=>c[0]==='connection.status').length).toBeGreaterThan(1)
 f.done()
})


test('first-use trust result shows issuer and expiry without a review step',async()=>{
 const trustedDsm={issuer:'Local DSM issuer',notAfter:'2029-10-26T00:00:00Z'}
 const f=fixture(base(),{'connection.status':()=>({provider:'multiple',state:'connected',trustedDsm,connections:[{...needsDsm,state:'connected',code:''}]})})
 await settle()
 const details=f.root.querySelector('[data-page="cloudflare"] [data-dsm-trusted]')!
 expect(details.textContent).toContain('Local DSM issuer')
 expect(details.textContent).toContain('2029-10-26')
 expect(f.root.querySelector('[data-dsm-review-open]')).toBeNull()
 f.done()
})

test('entry and protection changes have distinct root causes',async()=>{
 const {entryChangeTitleKey}=await import('../src/connect/appUrls')
 const before={package:'ExampleMusic',port:28443},after={package:'ExampleMusic',port:5001,path:'/music/'}
 expect(entryChangeTitleKey({before,after,reason:'protection_changed'})).toBe('entry.changed')
 expect(entryChangeTitleKey({before:after,after,reason:'protection_changed'})).toBe('reason.protection_changed')
})

test('the App list is a grid: the address is flexible, columns resize, the pick box and arrow stay fixed, widths are remembered',async()=>{
 localStorage.clear()
 const f=fixture();await settle();await open(f.root)
 const grid=page(f.root).querySelector<HTMLElement>('[data-apps-table]')!
 const handle=(key:string)=>grid.querySelector(`th[data-column="${key}"] [data-resize-handle]`)
 expect(handle('app')).toBeTruthy()
 expect(handle('with')).toBeTruthy()
 expect(handle('address')).toBeTruthy()
 expect(handle('pick')).toBeNull()
 expect(handle('open')).toBeNull()
 handle('app')!.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true}));await settle()
 // The UI keys the saved view by App from 0.20.3; either form names this list.
 const saved=Object.entries(localStorage).find(([k])=>k.startsWith('syno-pkg-land-ui:data-grid:')&&k.endsWith(':apps-all'))
 expect(JSON.parse(saved![1]).columnWidths.app).toBe(210)
 f.done();localStorage.clear()
})
