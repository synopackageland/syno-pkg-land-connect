// @vitest-environment jsdom
// The Apps page: one row per App and the App dialog, against a mocked backend.
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {mount} from '../src/main'
import {translate} from '../src/i18n'
import {fingerprint,type AppRow,type AppsView,type Inspection} from '../src/connect/appUrls'
import {mainUrl,nothingSetUp,planChange,rowSummary,waysOf,waysReady} from '../src/connect/appWays'
import {settle} from './support'

const a=(key:string)=>translate('en','apps.'+key)
// The approval's notes are one FormNote list, one item each.
const noteTexts=(scope:HTMLElement)=>[...scope.querySelectorAll<HTMLElement>('[data-approve-notes] li')].map(li=>li.textContent?.trim())
const pin={mode:'W' as const,names:['nas.example.com'],issuer:'Example CA',notAfter:'2027-01-31T00:00:00Z'}
const base=():AppsView=>({
 private:{available:true,host:'nas.tailnet.ts.net'},
 public:{available:true,connected:true,zone:'example.com'},
 relay:{connected:true,subdomain:'example'},
 apps:[
  {package:'SynoPkgOff',name:'Off App',port:28001,publicCapable:true,protection:{}},
  {package:'SynoPkgOn',name:'On App',port:28002,publicCapable:true,protection:{upstreamAccess:'public'},private:{state:'active',url:'https://nas.tailnet.ts.net:28002/'},relay:{state:'active',url:'https://on.example.workers.dev/'}},
  {package:'SynoPkgWait',name:'Wait App',port:28003,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28003/',reachable:false}},
  {package:'SynoPkgRoute',name:'Route App',port:28004,publicCapable:true,protection:{},private:{state:'paused',reason:'route_mismatch',url:'https://nas.tailnet.ts.net:28004/'}},
  {package:'SynoPkgClean',name:'Clean App',port:28005,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28005/',cleanup:true}},
  {package:'SynoPkgGone',name:'Gone App',port:28006,removed:true,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28006/'}},
  {package:'SynoPkgDomain',name:'Domain App',port:28009,publicCapable:true,protection:{},public:{state:'active',url:'https://domain.example.com/'}},
  {package:'SynoPkgOld',name:'Old App',port:28007,needsUpdate:true,protection:{passthrough:['/api/public']}},
 ],
})
const nothing=():AppsView=>({private:{available:false,code:'tailscale-off'},public:{available:false,connected:false},relay:{connected:false},apps:base().apps.filter(r=>!r.removed).map(r=>({...r,private:undefined,public:undefined,relay:undefined}))})
const inspection=(pkg:string,over:Partial<Inspection>={}):Inspection=>({ready:true,package:pkg,name:pkg.replace('SynoPkg','')+' App',port:28001,protection:{passthrough:['/share']},pin,...over})

function fixture(view:AppsView|(()=>AppsView)=base(),handlers:Record<string,(params:Record<string,unknown>)=>unknown>={},connections:unknown[]=[]){
 const root=document.createElement('div');document.body.append(root)
 const call=vi.fn(async(method:string,params:Record<string,unknown>={})=>{
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections}
  if(method==='apps.list')return typeof view==='function'?view():view
  if(handlers[method])return handlers[method]!(params)
  return {}
 })
 const app=mount(root,createStandaloneContext({call,locale:'en'}))
 return {root,call,done(){app.unmount();root.remove()}}
}
async function open(root:HTMLElement){[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='App URLs')!.click();await settle()}
const page=(root:HTMLElement)=>root.querySelector<HTMLElement>('[data-page="apps"]')!
const row=(root:HTMLElement,pkg:string)=>page(root).querySelector<HTMLElement>(`[data-app-row="${pkg}"]`)!.closest<HTMLElement>('tr')!
const dialog=()=>document.querySelector<HTMLElement>('[data-app-dialog]')
const approval=()=>document.querySelector<HTMLElement>('[data-apps-approval]')
async function openApp(root:HTMLElement,pkg:string){page(root).querySelector<HTMLElement>(`[data-app-open="${pkg}"]`)!.click();await settle();return dialog()!}
const radio=(d:HTMLElement,label:string)=>[...d.querySelectorAll<HTMLElement>('[data-dialog-choice] label')].find(l=>l.textContent?.includes(label))!.querySelector<HTMLElement>('[role=radio]')!
const click=async(el:HTMLElement|null|undefined)=>{el!.click();await settle()}
afterEach(()=>{document.body.innerHTML='';vi.restoreAllMocks()})

test('ways, the row status and the one address follow the App',()=>{
 const ready=waysReady(base(),'nas.netbird.cloud')
 const [off,on,wait,route,clean,gone,domain,old]=base().apps as [AppRow,AppRow,AppRow,AppRow,AppRow,AppRow,AppRow,AppRow]
 expect(waysOf(on,ready).map(w=>w.way)).toEqual(['relay','private','netbird'])
 expect(waysOf(gone,ready)).toEqual([{way:'private',status:'removed'}])
 expect(waysOf(old,ready).map(w=>w.way)).toEqual(['netbird'])
 expect([off,on,wait,route,clean,gone,domain].map(r=>rowSummary(r,waysOf(r,waysReady(base()))))).toEqual(['off','on','waiting','paused','cleanup','removed','on'])
 expect(rowSummary(old,[])).toBe('needs-update')
 expect(mainUrl(on,waysOf(on,ready))).toBe('https://on.example.workers.dev/')
 // A single-origin App offers one address only: its private one when it has it.
 const single={...on,external:{single_origin:true}}
 expect(mainUrl(single,waysOf(single,ready))).toBe('https://nas.tailnet.ts.net:28002/')
 expect(mainUrl({...domain,external:{single_origin:true}},waysOf(domain,ready))).toBe('https://domain.example.com/')
})

test('a change of public way turns the old one off before the new one comes',()=>{
 const on=base().apps[1]!
 expect(planChange(on,{private:true,public:'public'})).toEqual({off:['relay'],on:['public']})
 expect(planChange(on,{private:false,public:'relay'})).toEqual({off:['private'],on:[]})
 expect(planChange(base().apps[0]!,{private:true,public:'relay'})).toEqual({off:[],on:['relay','private']})
 expect(planChange(on,{private:true,public:'relay'})).toEqual({off:[],on:[]})
})

test('nothing set up shows a picture and one way forward, not a table',async()=>{
 expect(nothingSetUp(nothing(),waysReady(nothing()))).toBe(true)
 expect(nothingSetUp(base(),waysReady(base()))).toBe(false)
 expect(nothingSetUp({...nothing(),relay:{connected:true,invalid:true}},waysReady(nothing()))).toBe(false)
 const f=fixture(nothing());await settle();await open(f.root)
 const empty=page(f.root).querySelector<HTMLElement>('[data-apps-empty]')!
 expect(empty.querySelector('[data-no-way-picture]')?.textContent).toContain(translate('en','path.node.nas'))
 expect(empty.querySelector('svg text')).toBeNull()
 expect(empty.textContent).toContain(a('page.emptyTitle'))
 expect(empty.textContent).toContain('Off App · On App')
 expect(page(f.root).querySelector('[data-apps-table]')).toBeNull()
 expect(page(f.root).querySelector('button[role="switch"],[data-app-pick]')).toBeNull()
 for(const gone of ['[data-apps-intro]','[data-apps-notes]','[data-open-setup-guide]','[data-dsm-review]','[data-relay-quota]'])expect(page(f.root).querySelector(gone),gone).toBeNull()
 await click(empty.querySelector<HTMLElement>('[data-apps-setup]'))
 expect(document.querySelector('[data-setup-wizard]')).toBeTruthy()
 f.done()
})

test('each App is one row: icon, chips for its ways, one address and its status',async()=>{
 const view=base();view.apps[0]!.icon='/webman/3rdparty/SynoPkgOff/images/app_0123456789ab_64.png'
 const f=fixture(view,{},[{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud',shareUrl:''}]);await settle();await open(f.root)
 expect(page(f.root).querySelectorAll('[data-app-row]')).toHaveLength(view.apps.length)
 expect(row(f.root,'SynoPkgOff').querySelector('img[data-app-icon]')?.getAttribute('src')).toBe(view.apps[0]!.icon)
 expect(row(f.root,'SynoPkgOn').querySelector('img')).toBeNull()
 const chips=(pkg:string)=>[...row(f.root,pkg).querySelectorAll<HTMLElement>('[data-app-way]')].map(c=>c.textContent?.trim())
 expect(chips('SynoPkgOn')).toEqual([a('way.chip.relay'),a('way.chip.private'),a('way.chip.netbird')])
 expect(chips('SynoPkgRoute')[0]).toBe(`${a('way.chip.private')} · ${a('status.paused')}`)
 expect(chips('SynoPkgDomain')[0]).toBe(a('way.chip.public'))
 expect(row(f.root,'SynoPkgOn').querySelector<HTMLInputElement>('[data-app-url] input, input[data-app-url]')?.value).toBe('https://on.example.workers.dev/')
 const lines:Record<string,[string,string]>={SynoPkgOn:['on',''],SynoPkgWait:['waiting',a('line.waiting')],SynoPkgRoute:['paused',a('reason.route_mismatch')],SynoPkgClean:['cleanup',a('line.cleanup')],SynoPkgGone:['removed',a('line.removed')]}
 for(const [pkg,[status,line]] of Object.entries(lines)){
  expect(row(f.root,pkg).querySelector<HTMLElement>('[data-status]')!.dataset.status,pkg).toBe(status)
  if(line)expect(row(f.root,pkg).textContent,pkg).toContain(line)
 }
 expect(row(f.root,'SynoPkgGone').querySelector('[data-app-status]')).toBeNull()
 expect(row(f.root,'SynoPkgGone').querySelector('[data-way-status="removed"]')).toBeTruthy()
 // Cloudflare's chip and the DSM review live on their own pages and the Overview.
 expect(page(f.root).querySelector('[data-dsm-review]')).toBeNull()
 f.done()
})

test('the dialog turns a private way on with the approval shown in place, then sends what was shown',async()=>{
 const f=fixture(base(),{'apps.inspect':p=>inspection(String(p.package),{token:'inspected-state',dsm:{port:5001,pin}})});await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgOff')
 expect(d.querySelector<HTMLButtonElement>('[data-dialog-save]')!.disabled).toBe(true)
 await click(d.querySelector<HTMLElement>('[data-dialog-switch] button[role="switch"], button[role="switch"][data-dialog-switch]'))
 await click(d.querySelector<HTMLElement>('[data-dialog-save]'))
 expect(f.call).toHaveBeenCalledWith('apps.inspect',{package:'SynoPkgOff',scope:'private'})
 expect(dialog()!.dataset.appDialogStep).toBe('approve')
 const inline=dialog()!.querySelector<HTMLElement>('[data-apps-approval]')!
 expect(document.querySelectorAll('[data-apps-approval]')).toHaveLength(1)
 expect(inline.dataset.scope).toBe('private')
 expect(inline.querySelector('[data-approve-port]')?.textContent).toBe('28001')
 expect(inline.querySelector('[data-approve-protection]')?.textContent).toContain(a('protection.passthrough').replace('{paths}','/share'))
 expect(inline.querySelector('[data-approve-cert="names"]')?.textContent).toBe('nas.example.com')
 expect(inline.querySelector('[data-approve-dsm]')).toBeTruthy()
 expect(f.call.mock.calls.map(c=>c[0])).not.toContain('apps.setprivate')
 await click(dialog()!.querySelector<HTMLElement>('[data-approve-confirm]'))
 expect(f.call).toHaveBeenCalledWith('apps.setprivate',{package:'SynoPkgOff',enabled:true,approval:{port:28001,pin,token:'inspected-state',dsm:{port:5001,pin}}})
 expect(dialog()).toBeNull()
 expect(f.call.mock.calls.filter(c=>c[0]==='apps.list').length).toBeGreaterThan(1)
 f.done()
})

test('a target that is not ready gets no approval',async()=>{
 const f=fixture(base(),{'apps.inspect':p=>({...inspection(String(p.package)),ready:false,pin:undefined})});await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgOff')
 await click(d.querySelector<HTMLElement>('button[role="switch"]'))
 await click(d.querySelector<HTMLElement>('[data-dialog-save]'))
 expect(approval()).toBeNull()
 expect(f.call.mock.calls.map(c=>c[0])).not.toContain('apps.setprivate')
 f.done()
})

test('public is one choice: switching ways confirms the old one off, then approves the new one',async()=>{
 const f=fixture(base(),{'apps.inspect':p=>inspection(String(p.package),{hostname:'on.example.com'}),'apps.setpublic':()=>({results:[{package:'SynoPkgOn',ok:true}]})});await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgOn')
 const choice=d.querySelector<HTMLElement>('[data-dialog-choice]')!
 expect(choice.textContent).toContain(a('way.relay'))
 expect(choice.textContent).toContain('https://on.example.workers.dev/')
 expect(d.querySelectorAll('[data-dialog-choice] [role=radio]')).toHaveLength(3)
 await click(radio(d,a('way.public')))
 await click(d.querySelector<HTMLElement>('[data-dialog-save]'))
 expect(dialog()!.querySelector('[data-dialog-confirm]')?.textContent).toContain(a('off.relayRule'))
 expect(f.call.mock.calls.map(c=>c[0])).not.toContain('apps.setrelay')
 await click(dialog()!.querySelector<HTMLElement>('[data-off-confirm]'))
 expect(f.call).toHaveBeenCalledWith('apps.setrelay',{packages:['SynoPkgOn'],enabled:false})
 const inline=dialog()!.querySelector<HTMLElement>('[data-apps-approval]')!
 expect(inline.dataset.scope).toBe('public')
 expect(inline.querySelector<HTMLInputElement>('[data-approve-hostname="SynoPkgOn"] input')?.value).toBe('on')
 // At most four short notes, and the rest behind Learn more.
 const notes=noteTexts(inline)
 expect(notes.length).toBeLessThanOrEqual(4)
 expect(notes).toEqual(expect.arrayContaining(['signIn','media','restart'].map(key=>a('note.'+key))))
 expect(inline.querySelector<HTMLAnchorElement>('[data-approve-more]')?.href).toContain('developers.cloudflare.com')
 await click(dialog()!.querySelector<HTMLElement>('[data-approve-confirm]'))
 expect(f.call.mock.calls.find(c=>c[0]==='apps.setpublic')![1]).toMatchObject({packages:['SynoPkgOn'],enabled:true,approvals:{SynoPkgOn:{hostname:'on.example.com'}}})
 f.done()
})

test('turning a way off and clearing a removed App are confirmed with what goes',async()=>{
 const f=fixture();await settle();await open(f.root)
 let d=await openApp(f.root,'SynoPkgDomain')
 await click(radio(d,a('status.off')))
 await click(d.querySelector<HTMLElement>('[data-dialog-save]'))
 expect(dialog()!.textContent).toContain(a('off.publicRule'))
 expect(dialog()!.textContent).toContain(a('off.dns'))
 await click(dialog()!.querySelector<HTMLElement>('[data-off-confirm]'))
 expect(f.call).toHaveBeenCalledWith('apps.setpublic',{packages:['SynoPkgDomain'],enabled:false})
 expect(dialog()).toBeNull()
 d=await openApp(f.root,'SynoPkgGone')
 expect(d.querySelector('[data-dialog-private]')).toBeNull()
 expect(d.textContent).toContain(a('line.removed'))
 await click(d.querySelector<HTMLElement>('[data-dialog-clear]'))
 expect(dialog()!.textContent).toContain(a('off.clearRule'))
 await click(dialog()!.querySelector<HTMLElement>('[data-off-confirm]'))
 expect(f.call).toHaveBeenCalledWith('apps.clear',{package:'SynoPkgGone'})
 f.done()
})

test('a paused way turns on again after an approval of the key it had',async()=>{
 const spki='ab'.repeat(32),old='cd'.repeat(32)
 const f=fixture(base(),{'apps.inspect':p=>inspection(String(p.package),{pin:{mode:'S',spki,previousSpki:old,issuer:'Synology Inc.',notAfter:'2030-01-01T00:00:00Z'}})});await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgRoute')
 expect(d.querySelector('[data-dialog-reason="private"]')?.textContent).toBe(a('reason.route_mismatch'))
 await click(d.querySelector<HTMLElement>('[data-dialog-reenable="private"]'))
 expect(f.call).toHaveBeenCalledWith('apps.inspect',{package:'SynoPkgRoute',scope:'private'})
 const inline=dialog()!.querySelector<HTMLElement>('[data-apps-approval]')!
 expect(inline.querySelector('[data-approve-cert="fingerprint"]')?.textContent).toBe(fingerprint(spki))
 expect(inline.querySelector('[data-approve-cert="previous"]')?.textContent).toBe(fingerprint(old))
 await click(dialog()!.querySelector<HTMLElement>('[data-approve-confirm]'))
 expect(f.call).toHaveBeenCalledWith('apps.reenable',expect.objectContaining({package:'SynoPkgRoute',scope:'private'}))
 f.done()
})

test('a way that is not set up offers Set up on its own page, and a private network that needs a fix says what',async()=>{
 const view=base();view.public={available:false,connected:false};view.relay={connected:false};view.private={available:false,code:'https-off'}
 const f=fixture(view);await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgOff')
 expect(d.querySelector('[data-dialog-setup="private"]')?.textContent).toContain(a('private.https-off'))
 expect(d.querySelector('[data-dialog-switch]')).toBeNull()
 expect(d.querySelector('[data-dialog-setup="all"]')?.textContent).toContain(a('dialog.publicNotSetUp'))
 for(const label of [a('way.relay'),a('way.public')])expect(radio(d,label).hasAttribute('disabled')||radio(d,label).getAttribute('data-disabled')!==null).toBe(true)
 await click(d.querySelector<HTMLElement>('[data-dialog-setup="all"] button'))
 expect(dialog()).toBeNull()
 expect(f.root.querySelector<HTMLElement>('[data-page="cloudflare"]')!.style.display).not.toBe('none')
 f.done()
})

test('a private way that is not set up opens the Tailscale page',async()=>{
 const view=base();view.private={available:false,code:'tailscale-off'}
 const f=fixture(view);await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgOff')
 expect(d.querySelector('[data-dialog-setup="private"]')?.textContent).toContain(a('dialog.notSetUp'))
 await click(d.querySelector<HTMLElement>('[data-dialog-fix="private"]'))
 expect(dialog()).toBeNull()
 expect(f.root.querySelector<HTMLElement>('[data-page="tailscale"]')!.style.display).not.toBe('none')
 f.done()
})

test('with only the own domain set up, the free link offers its own Set up on the Free link tab',async()=>{
 const view=base();view.relay={connected:false}
 const f=fixture(view);await settle();await open(f.root)
 const d=await openApp(f.root,'SynoPkgOff')
 expect(d.querySelector('[data-dialog-setup="public"]')).toBeNull()
 expect(d.querySelector('[data-dialog-setup="relay"]')?.textContent).toContain(a('way.chip.relay'))
 await click(d.querySelector<HTMLElement>('[data-dialog-setup="relay"] button'))
 const cf=f.root.querySelector<HTMLElement>('[data-page="cloudflare"]')!
 expect(cf.style.display).not.toBe('none')
 expect(cf.querySelector('[data-free-link]')).toBeTruthy()
 f.done()
})

test('NetBird shows its address without a switch; an App that needs an update cannot be turned on',async()=>{
 const f=fixture(base(),{},[{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud',shareUrl:''}]);await settle();await open(f.root)
 let d=await openApp(f.root,'SynoPkgOff')
 const nb=d.querySelector<HTMLElement>('[data-dialog-netbird]')!
 expect(nb.querySelector<HTMLInputElement>('input')?.value).toBe('https://nas.netbird.cloud:28001/')
 expect(nb.querySelector('[role="switch"]')).toBeNull()
 await click([...d.querySelectorAll<HTMLElement>('button')].find(b=>b.textContent?.trim()===a('cancel')))
 expect(dialog()).toBeNull()
 d=await openApp(f.root,'SynoPkgOld')
 expect(d.textContent).toContain(a('line.needs-update'))
 expect(d.querySelector('button[role="switch"]')?.hasAttribute('disabled')).toBe(true)
 expect([...d.querySelectorAll('[data-dialog-choice] [role=radio]')].every(r=>r.hasAttribute('disabled')||r.getAttribute('data-disabled')!==null)).toBe(true)
 f.done()
})

test('several Apps are made public together in one approval; a failed one says so on its row',async()=>{
 const f=fixture(base(),{'apps.inspect':p=>inspection(String(p.package)),'apps.setrelay':()=>({results:[{package:'SynoPkgOff',ok:false,code:'workers_failed'},{package:'SynoPkgRoute',ok:true}]})});await settle();await open(f.root)
 expect(page(f.root).querySelector('[data-apps-batch]')).toBeNull()
 // Only an App with no public way can be picked.
 expect(row(f.root,'SynoPkgOn').querySelector('[data-app-pick]')).toBeNull()
 expect(row(f.root,'SynoPkgDomain').querySelector('[data-app-pick]')).toBeNull()
 for(const pkg of ['SynoPkgOff','SynoPkgRoute'])await click(row(f.root,pkg).querySelector<HTMLElement>('[data-app-pick] button, [data-app-pick]'))
 await click(page(f.root).querySelector<HTMLElement>('[data-apps-batch-create="relay"]'))
 const d=approval()!
 expect(d.dataset.scope).toBe('relay')
 expect(d.querySelectorAll('[data-approve-app]')).toHaveLength(2)
 expect(noteTexts(d)).toContain(a('note.quota'))
 await click(d.querySelector<HTMLElement>('[data-approve-confirm]'))
 const calls=f.call.mock.calls.filter(c=>c[0]==='apps.setrelay')
 expect(calls).toHaveLength(1)
 expect(calls[0]![1]).toMatchObject({packages:['SynoPkgOff','SynoPkgRoute'],enabled:true})
 expect(row(f.root,'SynoPkgOff').textContent).toContain(a('relay.failed'))
 expect(page(f.root).querySelector('[data-apps-batch]')).toBeNull()
 f.done()
})

test('a single-origin App says it signs in at one address before it gets another',async()=>{
 const view=base();view.apps[0]={...view.apps[0]!,external:{single_origin:true},settingsUrl:'/webman/3rdparty/SynoPkgOff/sso/settings.cgi'}
 const f=fixture(view,{'apps.inspect':p=>inspection(String(p.package))});await settle();await open(f.root)
 let d=await openApp(f.root,'SynoPkgOff')
 expect(d.querySelector('[data-dialog-single-origin]')?.textContent).toContain(a('dialog.singleOrigin'))
 await click(d.querySelector<HTMLElement>('button[role="switch"]'))
 await click(d.querySelector<HTMLElement>('[data-dialog-save]'))
 d=dialog()!
 expect(d.dataset.appDialogStep).toBe('single')
 expect(d.querySelector<HTMLInputElement>('[data-single-url] input, input[data-single-url]')?.value).toBe('https://nas.tailnet.ts.net:28001')
 expect(d.querySelector('[data-single-settings]')?.getAttribute('href')).toBe('/webman/3rdparty/SynoPkgOff/sso/settings.cgi')
 expect(f.call.mock.calls.map(c=>c[0])).not.toContain('apps.inspect')
 await click(d.querySelector<HTMLElement>('[data-single-continue]'))
 expect(dialog()!.dataset.appDialogStep).toBe('approve')
 f.done()
})

test('the new Apps strings exist in all 9 locales without a trailing period',()=>{
 const keys=['page.opensWith','page.open','page.emptyTitle','page.setUp','page.makePublic.relay','page.makePublic.public','way.private','way.netbird','way.relay','way.public','way.chip.private','way.chip.netbird','way.chip.relay','way.chip.public','dialog.private','dialog.public','dialog.notSetUp','dialog.setUp','dialog.publicNotSetUp','dialog.singleOrigin','dialog.singlePublic','dialog.details','dialog.save','dialog.back']
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const key of keys){
  const text=translate(locale,'apps.'+key)
  expect(text,`${locale} ${key}`).not.toBe('apps.'+key)
  expect(text,`${locale} ${key}`).not.toMatch(/[.。]$/)
 }
})

async function providerApps(root:HTMLElement,nav:string,pageId:string){
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()===nav)!.click();await settle()
 const p=root.querySelector<HTMLElement>(`[data-page="${pageId}"]`)!
 p.querySelector<HTMLElement>('[data-ui-tab-value="apps"]')!.click();await settle()
 return p.querySelector<HTMLElement>('[data-tab-panel="apps"]')!
}

test('the Tailscale page\'s Apps tab is the same list with only the Tailscale way, and opens the same dialog',async()=>{
 const f=fixture(base(),{},[{provider:'tailscale',state:'connected',loggedIn:true,shareUrl:''},{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud',shareUrl:''}]);await settle()
 const tab=await providerApps(f.root,'Tailscale','tailscale')
 expect(tab.querySelector('[data-apps-page="private"] [data-apps-table]')).toBeTruthy()
 expect([...tab.querySelectorAll<HTMLElement>('[data-app-way]')].every(w=>w.dataset.appWay==='private')).toBe(true)
 const on=tab.querySelector<HTMLElement>('[data-app-row="SynoPkgOn"]')!.closest('tr')!
 expect(on.querySelector<HTMLInputElement>('input[data-app-url],[data-app-url] input')?.value).toBe('https://nas.tailnet.ts.net:28002/')
 // No public batch and no empty state on a provider's tab.
 expect(tab.querySelector('[data-app-pick],[data-apps-empty],[data-apps-attention]')).toBeNull()
 // A removed App stays while it still holds its Tailscale way, so it can be cleared.
 expect(tab.querySelector('[data-app-row="SynoPkgGone"]')).toBeTruthy()
 tab.querySelector<HTMLElement>('[data-app-open="SynoPkgOn"]')!.click();await settle()
 expect(dialog()?.dataset.package).toBe('SynoPkgOn')
 expect(dialog()!.querySelector('[data-dialog-choice]')).toBeTruthy()
 f.done()
})

test('the NetBird page\'s Apps tab lists each App at its NetBird address',async()=>{
 const f=fixture(base(),{},[{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud',shareUrl:''}]);await settle()
 const tab=await providerApps(f.root,'NetBird','netbird')
 expect([...tab.querySelectorAll<HTMLElement>('[data-app-way]')].every(w=>w.dataset.appWay==='netbird')).toBe(true)
 expect(tab.querySelector('[data-app-row="SynoPkgGone"]')).toBeNull()
 const on=tab.querySelector<HTMLElement>('[data-app-row="SynoPkgOn"]')!.closest('tr')!
 expect(on.querySelector<HTMLInputElement>('input[data-app-url],[data-app-url] input')?.value).toBe('https://nas.netbird.cloud:28002/')
 f.done()
})

// Each case owns a mount and its timeout; rendering five apps in one test
// made this assertion depend on the load from unrelated test files.
test.each(['off','on','app','dsm'] as const)('stale public credentials with %s usage warn only while in use',async use=>{
  const v=nothing();v.relay={connected:true};v.public={available:false,enabled:use==='on',connected:true,invalid:true}
  if(use==='app')v.apps[0]!.public={state:'active',url:'https://app.example.com'}
  if(use==='dsm')v.apps.push({package:'dsm',name:'DSM',dsm:true,port:5001,publicCapable:true,protection:{upstreamAccess:'public'},public:{state:'active',url:'https://dsm.example.com'}})
  const f=fixture(v);await settle();await open(f.root)
  try{expect(!!page(f.root).querySelector('[data-apps-attention="public"]'),use).toBe(use!=='off')}
  finally{f.done()}
})

test('stale unused relay credentials do not warn',async()=>{
 const v=nothing();v.public={available:true,connected:true};v.relay={connected:false,invalid:true}
 const f=fixture(v);await settle();await open(f.root)
 try{expect(page(f.root).querySelector('[data-apps-attention="relay"]')).toBeNull()}
 finally{f.done()}
})

for(const scope of ['relay','public'] as const)test(`DSM is an ordinary row and renames through the App approval path on ${scope}`,async()=>{
 const host=scope==='relay'?'dsm.example.workers.dev':'dsm.example.com'
 const chosen=scope==='relay'?'my-nas.example.workers.dev':'my-nas.example.com'
 const suggested=scope==='relay'?'my-nas-2.example.workers.dev':'my-nas-2.example.com'
 const v=base();v.apps.push({package:'dsm',name:'DSM',dsm:true,port:5001,publicCapable:true,protection:{upstreamAccess:'public'},[scope]:{state:'active',url:'https://'+host}})
 let conflict=true
 const f=fixture(()=>structuredClone(v),{
  'apps.inspect':p=>inspection('dsm',{name:'DSM',hostname:String(p.hostname||host),token:String(p.hostname||host),protection:{upstreamAccess:'public'}}),
  [scope==='relay'?'apps.setrelay':'apps.setpublic']:p=>{
   if(conflict)throw {code:scope==='relay'?'workers_name_conflict':'cloudflare_name_taken',details:{reason:'other',conflictName:chosen,suggestedName:suggested}}
   expect(p).toMatchObject({packages:['dsm'],enabled:true,approvals:{dsm:{hostname:suggested,token:suggested}}})
   v.apps.at(-1)![scope]!.url='https://'+suggested
   return {results:[{package:'dsm',ok:true}]}
  },
 },[{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud'}])
 await settle();await open(f.root)
 const r=row(f.root,'dsm');expect(r.querySelector('[data-app-icon="dsm"]')).toBeTruthy()
 expect(r.querySelector('[data-app-url]')?.textContent||r.querySelector('[data-app-url]')?.outerHTML).toContain(host)
 expect(r.querySelectorAll('[data-app-way]')).toHaveLength(1)
 expect(r.querySelector('[data-app-pick]')).toBeNull()
 const d=await openApp(f.root,'dsm')
 expect(d.querySelector('[data-dialog-dsm]')).toBeTruthy()
 expect(d.querySelector('[data-dialog-choice]')).toBeNull()
 expect(d.querySelector('[data-dialog-clear]')).toBeNull()
 expect(d.textContent).toContain(a('dsm.signIn'))
 const input=d.querySelector<HTMLInputElement>('[data-dialog-dsm-name] input')!
 input.value=chosen.split('.')[0]!;input.dispatchEvent(new Event('input',{bubbles:true}));await new Promise(r=>setTimeout(r,350));await settle()
 await click(d.querySelector('[data-dialog-save]'))
 expect(d.querySelector('[data-name-conflict]')?.textContent).toContain('Used by another service')
 conflict=false
 // Use the shared conflict handler's suggested name, then recheck and approve.
 const use=[...d.querySelectorAll<HTMLElement>('[data-name-conflict] button')].find(b=>b.textContent?.includes(suggested))!
 expect(use).toBeTruthy();await click(use)
 await new Promise(r=>setTimeout(r,350));await settle()
 await click(d.querySelector('[data-dialog-save]'))
 expect(dialog()).toBeNull()
 expect(row(f.root,'dsm').outerHTML).toContain(suggested)
 f.done()
})

test('a changed package entry offers the actual old and new entry and resumes normal approval',async()=>{
 const v=base(),entry={package:'SynoPkgDomain',port:5001,path:'/synopkgland/SynoPkgDomain/'}
 const r=v.apps.find(a=>a.package==='SynoPkgDomain')!
 r.entry=entry;r.port=5001;r.waiting=false;r.needsUpdate=false
 r.public={...r.public!,state:'paused',reason:'protection_changed',entryChange:{before:{package:r.package,port:28007},after:entry,reason:'protection_changed'}}
 const f=fixture(v,{'apps.inspect':p=>({...inspection(String(p.package)),entry,port:5001})});await settle();await open(f.root)
 const line=row(f.root,r.package)
 expect(line.textContent).toContain('App entry changed')
 expect(line.querySelector('[data-entry-before]')?.textContent).toContain('https://127.0.0.1:28007/')
 expect(line.querySelector('[data-entry-after]')?.textContent).toContain(entry.path)
 expect(line.textContent).not.toContain(a('line.waiting'))
 await click(line.querySelector<HTMLElement>('[data-entry-update]'))
 expect(f.call).toHaveBeenCalledWith('apps.inspect',{package:r.package,scope:'public'})
 expect(dialog()?.querySelector('[data-approve-port]')?.textContent).toBe(entry.path)
 await click(dialog()!.querySelector<HTMLElement>('[data-approve-confirm]'))
 expect(f.call).toHaveBeenCalledWith('apps.reenable',expect.objectContaining({package:r.package,scope:'public'}))
 f.done()
})

test('the pick box column shows only when a listed row can be picked',async()=>{
 const header=(root:HTMLElement)=>page(root).querySelector('[data-apps-table] th[data-column="pick"]')
 const some=fixture();await settle();await open(some.root)
 expect(header(some.root)).toBeTruthy()
 some.done()
 // An uninstalled App and an App already public: nothing to pick, no empty first column.
 const none=fixture(()=>({...base(),apps:base().apps.filter(r=>['SynoPkgGone','SynoPkgDomain'].includes(r.package))}));await settle();await open(none.root)
 expect(page(none.root).querySelector('[data-app-row="SynoPkgGone"]')).toBeTruthy()
 expect(header(none.root)).toBeNull()
 expect(page(none.root).querySelector('[data-apps-table] th[data-column]')?.getAttribute('data-column')).toBe('app')
 none.done()
})
