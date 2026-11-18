// @vitest-environment jsdom
// Cloudflare: a temporary address with no fields, an own domain by signing in,
// or a tunnel token folded under Own domain. Each needs one public-access confirmation.
import {afterEach,expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import type {ConnectionStatus} from '../src/context'
import {needsSignIn,trustedLoginUrl} from '../src/connect/login'
import {byLabel,settle} from './support'
import {fixture,t,connected,type Handler} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

const LOGIN='https://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Ffixture'
const TOKEN='eyJhIjoiZml4dHVyZSJ9'
async function open(connections:ConnectionStatus[]=[],handlers:Record<string,Handler>={}){
 const defaults=connections.length?connections.map(c=>c.provider==='cloudflare'?{suggestedName:'nas-a1b2c3d4e5f6',...c}:c):[{provider:'cloudflare',state:'disconnected',suggestedName:'nas-a1b2c3d4e5f6',simulation:false,shareUrl:''} as ConnectionStatus]
 const f=fixture(defaults,handlers);await settle();await f.nav('Cloudflare')
 return {...f,cf:f.page('cloudflare')}
}
const button=(scope:HTMLElement,selector:string)=>scope.querySelector<HTMLButtonElement>(selector)!
async function type(field:HTMLElement,value:string){const input=field as HTMLInputElement;input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));await settle()}
// The Own domain tab starts the own domain; the temporary address has its own tab.
const domainTab=(cf:HTMLElement)=>cf.querySelector<HTMLElement>('[data-cf-domain-tab]')!
const primaries=(scope:HTMLElement)=>[...scope.querySelectorAll<HTMLButtonElement>('button[data-variant="primary"]')]
// The tunnel token folds under the Own domain tab while Cloudflare is off.
const tab=()=>({opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn(),closed:false})

test('only Cloudflare\'s own sign-in page that returns to Cloudflare is trusted',()=>{
 expect(trustedLoginUrl('cloudflare',LOGIN)).toBe(LOGIN)
 for(const bad of [
  'https://dash.cloudflare.com/argotunnel?callback=https%3A%2F%2Fevil.example%2F',
  'https://evil.example/argotunnel?callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx',
  'http://dash.cloudflare.com/argotunnel?callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx',
  'https://dash.cloudflare.com/other?callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx',
  'https://user@dash.cloudflare.com/argotunnel?callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx',
 ])expect(trustedLoginUrl('cloudflare',bad),bad).toBe('')
})

test('only a new own domain and a Tailscale or NetBird start without a key sign in through the browser',()=>{
 expect(needsSignIn({provider:'cloudflare',mode:'domain',subdomain:'nas'})).toBe(true)
 expect(needsSignIn({provider:'tailscale'})).toBe(true)
 expect(needsSignIn({provider:'netbird'})).toBe(true)
 for(const p of [{provider:'cloudflare',mode:'quick'},{provider:'cloudflare',mode:'domain'},{provider:'cloudflare',mode:'token',token:TOKEN},{provider:'tailscale',token:'key'},{provider:'netbird',token:'key'},{provider:'ipv6'}])
  expect(needsSignIn(p),JSON.stringify(p)).toBe(false)
})

const temporaryTab=(cf:HTMLElement)=>cf.querySelector<HTMLElement>('[data-tab-panel="temporary"]')!
test('a temporary address has its own tab after Free link: one click starts it, with no sign-in',async()=>{
 const win=vi.spyOn(window,'open')
 const f=await open()
 expect(domainTab(f.cf).querySelector('[data-cf-quick]')).toBeNull()
 expect([...f.cf.querySelectorAll('[data-ui-tab-value]')].map(e=>e.getAttribute('data-ui-tab-value'))).toEqual(['connection','domain','free','temporary','apps'])
 const tab=temporaryTab(f.cf)
 expect(tab.querySelector('[data-temporary-facts]')).toBeTruthy()
 expect(tab.querySelector('input')).toBeNull()
 expect(primaries(tab).map(b=>b.hasAttribute('data-cf-quick'))).toEqual([true])
 button(tab,'[data-cf-quick]').click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'quick',confirmExposure:true})
 expect(win).not.toHaveBeenCalled()
 f.done()
})

// The address itself shows once, in Connection status; the tab keeps Stop.
test('a running temporary address shows once, in Connection status, and its tab offers Stop',async()=>{
 const f=await open([connected('cloudflare',{mode:'quick',endpoints:[{url:'https://fixture.trycloudflare.com',public:true,localPort:5000}]})])
 const tab=temporaryTab(f.cf)
 expect(tab.querySelector('[data-cf-quick]')).toBeNull()
 expect(tab.querySelector('[data-url-card]')).toBeNull()
 expect([...f.cf.querySelectorAll<HTMLInputElement>('[data-url-card] input')].map(i=>i.value)).toEqual(['https://fixture.trycloudflare.com'])
 expect(f.cf.querySelector('[data-url-open]')).toBeTruthy()
 expect(tab.querySelector('[data-temporary-fact="changes"]')?.textContent).toBe(t('temporary.changes'))
 button(tab,'[data-cf-temporary-stop]').click();await settle()
 document.querySelector<HTMLElement>('[data-cloudflare-off-confirm]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.disconnect',{provider:'cloudflare'})
 f.done()
})

test('a running own domain leaves the temporary tab without a start',async()=>{
 const f=await open([connected('cloudflare',{mode:'domain',hostname:'nas.example.com'})])
 const tab=temporaryTab(f.cf)
 expect(tab.querySelector('[data-cf-quick]')).toBeNull()
 expect(tab.querySelector('[data-cf-temporary-busy]')?.textContent).toBe(t('cfpage.temporary.busy'))
 f.done()
})

// The hint is a note at the foot of the Own domain tab.
test('Own domain points TCP services such as Drive Client to Only my devices',async()=>{
 const f=await open([connected('cloudflare',{mode:'domain',hostname:'nas.example.com'})])
 expect(f.cf.querySelector('[data-provider-footer] [data-cf-private-hint]')).toBeNull()
 const hint=f.cf.querySelector<HTMLElement>('[data-tab-panel="domain"] [data-cf-private-hint]')!
 expect(hint.textContent).toContain(t('cfpage.domain.privateHint'))
 expect(primaries(hint)).toEqual([])
 button(hint,'[data-cf-private-open]').click();await settle()
 expect(document.querySelector('[data-wizard-step="choose"][data-wizard-current="tailscale"]')).toBeTruthy()
 f.done()
})

test('an own domain asks only for the name and sends the sign-in tab to Cloudflare',async()=>{
 const login=tab()
 const win=vi.spyOn(window,'open').mockReturnValue(login as unknown as Window)
 const f=await open([],{'connection.connect':()=>({provider:'cloudflare',mode:'domain',state:'needs-login',loginUrl:LOGIN,simulation:false,shareUrl:''})})
 const start=domainTab(f.cf)
 const name=byLabel(start,t('cloudflare.subdomain')) as HTMLInputElement
 expect(name.value).toBe('nas-a1b2c3d4e5f6')
 expect(button(start,'[data-cf-domain]').disabled).toBe(true)
 start.querySelector<HTMLElement>('[data-cf-confirm]')!.click();await settle()
 await type(name,'photos')
 button(start,'[data-cf-domain]').click()
 expect(win).toHaveBeenCalledWith('','_blank')
 await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'photos',confirmExposure:true})
 expect(login.location.replace).toHaveBeenCalledWith(LOGIN)
 f.done()
})

test('a name that is not one DNS label cannot be submitted',async()=>{
 const f=await open()
 const start=domainTab(f.cf)
 start.querySelector<HTMLElement>('[data-cf-confirm]')!.click();await settle()
 for(const bad of ['','nas.example','-nas','na s'])
 {await type(byLabel(start,t('cloudflare.subdomain')),bad);expect(button(start,'[data-cf-domain]').disabled,bad).toBe(true)}
 f.done()
})

test('a saved own domain reconnects without signing in again',async()=>{
 const win=vi.spyOn(window,'open')
 const f=await open([{provider:'cloudflare',state:'disconnected',hostname:'nas.example.com',simulation:false,shareUrl:''} as ConnectionStatus])
 const start=domainTab(f.cf)
 const again=button(start,'[data-cf-reconnect]')
 expect(start.textContent).toContain('nas.example.com')
 start.querySelector<HTMLElement>('[data-cf-confirm]')!.click();await settle()
 again.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',confirmExposure:true})
 expect(win).not.toHaveBeenCalled()
 f.done()
})

test('the other-name field defaults to the saved subdomain before the NAS suggestion',async()=>{
 const f=await open([{provider:'cloudflare',state:'disconnected',mode:'domain',hostname:'photos.example.com',suggestedName:'nas-fixture',simulation:false,shareUrl:''} as ConnectionStatus])
 button(domainTab(f.cf),'[data-cf-saved] [data-use-more]').click();await settle()
 const input=byLabel(domainTab(f.cf),t('cloudflare.subdomain')) as HTMLInputElement
 expect(input.value).toBe('photos')
 f.done()
})

test('the Own domain tab has one primary action, enabled by the consent: the saved domain, else a new one',async()=>{
 for(const connections of [[],[{provider:'cloudflare',state:'disconnected',hostname:'nas.example.com',simulation:false,shareUrl:''} as ConnectionStatus]]){
  const f=await open(connections)
  const start=domainTab(f.cf)
  expect(primaries(start).every(b=>b.disabled)).toBe(true)
  start.querySelector<HTMLElement>('[data-cf-confirm]')!.click();await settle()
  const enabled=primaries(start).filter(b=>!b.disabled)
  expect(enabled.map(b=>b.textContent?.trim())).toEqual([t(connections.length?'cloudflare.reconnect':'cloudflare.domain')])
  f.done()
 }
})

test('a stopped token connection offers the own-domain sign-in, not a token form or a reconnect',async()=>{
 const f=await open([{provider:'cloudflare',state:'disconnected',mode:'token',hostname:'nas.example.com',simulation:false,shareUrl:''} as ConnectionStatus])
 expect(f.cf.querySelector('[data-cf-reconnect]')).toBeNull()
 expect(domainTab(f.cf).querySelector('[data-cf-token],input[type=password]')).toBeNull()
 expect(domainTab(f.cf).querySelector('[data-connect-form]')).toBeTruthy()
 f.done()
})

test('a temporary address is labelled temporary and offers the own domain in place',async()=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 const f=await open([connected('cloudflare',{mode:'quick',endpoints:[{url:'https://fixture.trycloudflare.com',public:true,localPort:5000}]})])
 const temporary=f.cf.querySelector<HTMLElement>('[data-cf-temporary]')!
 expect(f.cf.querySelector('[data-cf-temporary-line]')).toBeNull()
 expect([...f.cf.querySelectorAll<HTMLInputElement>('[data-url-card] input')].map(i=>i.value)).toEqual(['https://fixture.trycloudflare.com'])
 expect(f.cf.querySelector('[data-connect-section]')).toBeNull()
 button(temporary,'[data-cf-domain]').click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'nas-a1b2c3d4e5f6',confirmExposure:true})
 f.done()
})

test('an own domain or token connection is not labelled temporary',async()=>{
 for(const mode of ['domain','token']){
  const f=await open([connected('cloudflare',{mode,endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})])
  expect(f.cf.querySelector('[data-cf-temporary]'),mode).toBeNull()
  f.done()
 }
})

test('an own domain can be changed: the name, prefilled from the address, and a new sign-in',async()=>{
 const login=tab()
 vi.spyOn(window,'open').mockReturnValue(login as unknown as Window)
 const f=await open([connected('cloudflare',{mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})],{'connection.connect':()=>({provider:'cloudflare',mode:'domain',state:'needs-login',loginUrl:LOGIN,simulation:false,shareUrl:''})})
 expect(f.cf.querySelector('[data-cf-domain]')).toBeNull()
 button(f.cf,'[data-way-change-toggle] [data-use-more]').click();await settle()
 const panel=f.cf.querySelector<HTMLElement>('[data-cf-change-panel]')!
 expect(panel.textContent).toContain(t('cloudflare.changeShort'))
 const name=byLabel(panel,t('cloudflare.subdomain'))
 expect((name as HTMLInputElement).value).toBe('nas')
 await type(name,'photos')
 button(panel,'[data-cf-domain]').click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'photos',confirmExposure:true})
 expect(login.location.replace).toHaveBeenCalledWith(LOGIN)
 f.done()
})

test('changing the address closes again, and a temporary or token connection offers no change',async()=>{
 const f=await open([connected('cloudflare',{mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})])
 button(f.cf,'[data-way-change-toggle] [data-use-more]').click();await settle()
 button(f.cf,'[data-way-change-toggle] [data-use-more]').click();await settle()
 expect(f.cf.querySelector('[data-cf-change-panel]')).toBeNull()
 expect(f.cf.querySelector('[data-way-change-toggle] [data-use-more]')).not.toBeNull()
 f.done()
 for(const mode of ['quick','token']){
  const g=await open([connected('cloudflare',{mode,endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})])
  expect(g.cf.querySelector('[data-way-change-toggle] [data-use-more]'),mode).toBeNull()
  g.done()
 }
})

test('the page offers no share link or credentials file any more',async()=>{
 const f=await open()
 expect(f.cf.querySelector('[data-cloudflare-modes],input[type=file]')).toBeNull()
 expect(f.cf.textContent).not.toMatch(/File Station|19037|credentials JSON/)
 f.done()
})

test('a failed Cloudflare start names its reason',async()=>{
 const f=await open([{provider:'cloudflare',state:'failed',code:'cloudflare_create_failed',simulation:false,shareUrl:''} as ConnectionStatus])
 expect(f.cf.querySelector('[data-connection-reason]')?.textContent).toBe(t('cloudflare.code.cloudflare_create_failed'))
 f.done()
})

test('a taken name says so without its code and leaves the way out to its row',async()=>{
 const f=await open([{provider:'cloudflare',state:'failed',mode:'domain',code:'cloudflare_name_taken',reason:'other',conflictName:'nas.example.com',hostname:'nas.example.com',suggestedName:'nas-2.example.com',simulation:false,shareUrl:''} as ConnectionStatus])
 expect(f.cf.querySelector('[data-name-taken]')?.textContent).toContain(t('cloudflare.conflict.banner'))
 expect(f.cf.querySelector('[data-connection-reason]')).toBeNull()
 expect(f.cf.textContent).not.toContain('cloudflare_name_taken')
 expect(f.cf.querySelectorAll('[data-name-conflict]')).toHaveLength(1)
 f.done()
})

test('a failed Cloudflare check names what is wrong',async()=>{
 const f=await open([connected('cloudflare',{mode:'domain',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})],{'connection.check':p=>({provider:p.provider,state:'failed',code:'cloudflare_tunnel_pending'})})
 expect(f.cf.querySelector('[data-connection-check]')?.textContent).toContain(t('cloudflare.code.cloudflare_tunnel_pending'))
 f.done()
})

const codes=['cloudflare_permission_denied','cloudflare_api_failed','cloudflare_invalid_subdomain','cloudflare_no_saved_domain','cloudflare_login_failed','cloudflare_login_timeout','cloudflare_login_interrupted','cloudflare_name_taken','cloudflare_create_failed','cloudflare_route_failed','cloudflare_start_failed','cloudflare_stopped','cloudflare_quick_address_unavailable','cloudflare_invalid_hostname','cloudflare_save_failed','cloudflare_storage_permission','cloudflare_tunnel_missing','cloudflare_invalid_request','cloudflare_not_dsm','cloudflare_tunnel_pending','cloudflare_origin_unreachable','cloudflare_dns_pending','cloudflare_tls_failed','cloudflare_unreachable']
const words=['confirm','quick','subdomain','subdomainShort','domain','reconnect',...codes.map(c=>`code.${c}`),...['leftover','other_connect','other','reuse','open','check','use','cleanup','banner','hint.leftover','hint.other_connect','hint.other'].map(k=>`conflict.${k}`)]
test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the Cloudflare page is translated in %s',locale=>{
 const own=JSON.parse(readFileSync(`../i18n/messages/${locale}.json`,'utf8')) as Record<string,string>
 for(const word of words)expect(own[`cloudflare.${word}`]?.trim(),`${locale} ${word}`).toBeTruthy()
})

test.each([['cloudflare',LOGIN,'provider.login.cloudflare'],['tailscale','https://login.tailscale.com/a/fixture','provider.login.tailscale']])('a %s sign-in button names its own provider',async(provider,loginUrl,key)=>{
 const f=fixture([{provider,state:'needs-login',loginUrl,simulation:false,shareUrl:''} as ConnectionStatus]);await settle()
 await f.nav(provider==='cloudflare'?'Cloudflare':'Tailscale')
 expect(f.page(provider).querySelector('[data-login-action]')?.textContent?.trim()).toBe(t(key))
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('each sign-in label names its provider in %s',locale=>{
 const own=JSON.parse(readFileSync(`../i18n/messages/${locale}.json`,'utf8')) as Record<string,string>
 expect(own['provider.login']).toBeUndefined()
 expect(own['provider.login.cloudflare']).toContain('Cloudflare')
 expect(own['provider.login.tailscale']).toContain('Tailscale')
})

test.each(['leftover','other_connect','other'] as const)('the own-domain form recovers inline from %s',async reason=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 const conflict={provider:'cloudflare',state:'failed',mode:'domain',code:'cloudflare_name_taken',reason,conflictName:'nas.example.com',hostname:'nas.example.com',suggestedName:'nas-2.example.com',reuseRecord:'record-digest',dnsUrl:'https://dash.cloudflare.com/account/example.com/dns/records',simulation:false,shareUrl:''} as ConnectionStatus
 const f=await open([conflict]);const row=f.cf.querySelector<HTMLElement>('[data-name-conflict]')!
 expect(row).not.toBeNull()
 // The taken name stays until its alternative is chosen.
 expect((byLabel(f.cf,t('cloudflare.subdomain')) as HTMLInputElement).value).toBe('nas')
 if(reason==='leftover'){
  row.querySelector<HTMLButtonElement>('[data-name-reuse]')!.click();await settle()
  expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'nas',reuseName:{name:'nas.example.com',record:'record-digest'},confirmExposure:true})
 }else{
  row.querySelector<HTMLButtonElement>('[data-name-use]')!.click();await settle()
  expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'nas-2',confirmExposure:true})
  expect((byLabel(f.cf,t('cloudflare.subdomain')) as HTMLInputElement).value).toBe('nas-2')
 }
 f.done()
})

test('Check again retries the original name with no overwrite consent',async()=>{
 const f=await open([{provider:'cloudflare',state:'failed',mode:'domain',code:'cloudflare_name_taken',reason:'other',conflictName:'nas.example.com',suggestedName:'nas-2.example.com',simulation:false,shareUrl:''} as ConnectionStatus])
 f.cf.querySelector<HTMLButtonElement>('[data-name-check]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'cloudflare',mode:'domain',subdomain:'nas',confirmExposure:true})
 f.done()
})


test('a storage permission failure explains the data directory repair in the status banner',async()=>{
 const f=await open([{provider:'cloudflare',state:'failed',mode:'domain',hostname:'nas.example.com',code:'cloudflare_storage_permission',simulation:false,shareUrl:''}])
 expect(f.cf.textContent).toContain(t('cloudflare.code.cloudflare_storage_permission'))
 expect(f.cf.textContent).not.toContain('cloudflare_storage_permission')
 expect(f.cf.textContent).not.toContain(t('cloudflare.code.cloudflare_save_failed'))
 f.done()
})


test('a deleted remote tunnel explains why the user must choose another name and sign in',async()=>{
 const f=await open([{provider:'cloudflare',state:'failed',mode:'domain',hostname:'nas.example.com',code:'cloudflare_tunnel_missing',simulation:false,shareUrl:''}])
 expect(f.cf.textContent).toContain(t('cloudflare.code.cloudflare_tunnel_missing'))
 expect(f.cf.textContent).not.toContain('cloudflare_tunnel_missing')
 f.done()
})
