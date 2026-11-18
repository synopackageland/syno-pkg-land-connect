// @vitest-environment jsdom
// §4.1 card: Download or "Update to x", one "In use x" tag, a note only when
// the downloaded version is not the one in use, and "Roll back to x".
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,nextTick} from 'vue'
import BinarySettings from '../src/BinarySettings.vue'
import {createStandaloneContext} from '@synopackageland/sdk'
import {translate} from '../src/i18n'
afterEach(()=>{vi.restoreAllMocks();document.body.innerHTML=''})
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const t=(key:string,vars:Record<string,string>={})=>Object.entries(vars).reduce((s,[k,v])=>s.replace(`{${k}}`,v),translate('en','binaries.'+key))
function mount(status:(product:string)=>Record<string,unknown>,extra:Record<string,()=>unknown>={},locale='en'){
 const call=vi.fn(async(method:string,p:Record<string,unknown>={})=>{const product=String(p.product);if(extra[method])return extra[method]();return {product,phase:'idle',previous:'',latest:'',current:'',...status(product)}})
 const root=document.createElement('div');document.body.append(root)
 const app=createApp(BinarySettings,{ctx:createStandaloneContext({call,locale})});app.mount(root)
 const card=(product:string)=>root.querySelector<HTMLElement>(`[aria-label=${product}]`)!
 const buttons=(product:string)=>[...card(product).querySelectorAll('button')].map(b=>b.textContent?.trim())
 return {call,root,card,buttons,done(){app.unmount();root.remove()}}
}

test('a program that is not downloaded offers Download only',async()=>{
 const f=mount(()=>({}));await settle()
 expect(f.card('netbird').querySelector('[data-binary-in-use]')?.textContent).toContain(t('missing'))
 expect(f.buttons('netbird')).toContain(t('download'))
 expect(f.buttons('netbird').some(b=>b?.startsWith(t('updateTo',{version:''}).trim()))).toBe(false)
 ;[...f.card('netbird').querySelectorAll('button')].find(b=>b.textContent?.trim()===t('download'))!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('binaries.update',{product:'netbird'})
 f.done()
})

test('a newer version offers "Update to x" and the tag names the version in use',async()=>{
 const f=mount(()=>({current:'1.0.0',runtimeVersion:'1.0.0',latest:'1.1.0',updateAvailable:true}));await settle()
 expect(f.card('tailscale').querySelector('[data-binary-in-use]')?.textContent).toBe(t('inUse',{version:'1.0.0'}))
 expect(f.buttons('tailscale')).toContain(t('updateTo',{version:'1.1.0'}))
 expect(f.buttons('tailscale')).not.toContain(t('download'))
 expect(f.card('tailscale').textContent).not.toContain('Provider-selected version')
 expect(f.card('tailscale').textContent).not.toContain('Downloaded version')
 expect(f.card('tailscale').querySelector('[data-binary-pending]')).toBeNull()
 f.done()
})

test('an up-to-date program offers neither Download nor Update',async()=>{
 const f=mount(()=>({current:'1.1.0',latest:'1.1.0',updateAvailable:false}));await settle()
 expect(f.card('cloudflared').querySelector('[data-binary-in-use]')?.textContent).toBe(t('inUse',{version:'1.1.0'}))
 expect(f.buttons('cloudflared')).not.toContain(t('download'))
 expect(f.buttons('cloudflared').some(b=>b?.includes('1.1.0'))).toBe(false)
 expect(f.buttons('cloudflared')).toContain(t('check'))
 f.done()
})

test('a downloaded version that is not in use yet gets one extra line',async()=>{
 const f=mount(()=>({current:'2026.10.0',runtimeVersion:'2026.9.3',latest:'2026.10.0'}));await settle()
 expect(f.card('cloudflared').querySelector('[data-binary-in-use]')?.textContent).toBe(t('inUse',{version:'2026.9.3'}))
 expect(f.card('cloudflared').querySelector('[data-binary-pending]')?.textContent).toBe(t('pending',{version:'2026.10.0'}))
 f.done()
})

test('roll back is a labelled button, only with an earlier version',async()=>{
 const f=mount(product=>({current:'1.1.0',latest:'1.1.0',previous:product==='tailscale'?'1.0.0':''}));await settle()
 expect(f.card('netbird').querySelector('[data-binary-rollback]')).toBeNull()
 expect(f.root.querySelector('[data-binary-menu]')).toBeNull()
 const button=f.card('tailscale').querySelector<HTMLElement>('[data-binary-rollback]')!
 expect(button.textContent?.trim()).toBe(t('rollbackTo',{version:'1.0.0'}))
 button.click();await settle()
 expect(f.call).toHaveBeenCalledWith('binaries.rollback',{product:'tailscale'})
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the card is translated in %s',locale=>{
 for(const key of ['download','updateTo','inUse','pending','rollbackTo','previous'])
  expect(translate(locale,'binaries.'+key),`${locale} ${key}`).not.toBe(locale==='en'?'binaries.'+key:translate('en','binaries.'+key))
})

// The backend reports codes (phase, verification) and an RFC 3339 time; the card
// shows each one as words and a local date in every locale.
test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('a ready card shows no raw backend value in %s',async locale=>{
 for(const verification of ['sha256','distsign']){
  const f=mount(()=>({current:'1.0.0',runtimeVersion:'1.0.0',latest:'1.0.0',phase:'ready',verification,checkedAt:'2026-10-13T08:00:00Z'}),{},locale);await settle()
  const text=f.card('netbird').textContent!
  expect(text).not.toMatch(/\bready\b|sha256|distsign|2026-10-13T|binaries\./)
  expect(text).toContain(new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeStyle:'short'}).format(new Date('2026-10-13T08:00:00Z')))
  f.done()
 }
})

// Until binaries.status answers, the tag is a placeholder, not "Not downloaded".
test('a card waiting for its status shows a placeholder tag, not "Not downloaded"',async()=>{
 const answers:((s:unknown)=>void)[]=[]
 const f=mount(()=>({}),{'binaries.status':()=>new Promise(resolve=>{answers.push(resolve)})});await settle()
 expect(f.card('tailscale').textContent).not.toContain(t('missing'))
 expect(f.card('tailscale').querySelector('[data-binary-in-use]')).toBeNull()
 expect(f.card('tailscale').querySelector('[data-binary-in-use-loading]')).not.toBeNull()
 for(const answer of answers)answer({phase:'idle',previous:'',latest:'',current:'1.0.0',runtimeVersion:'1.0.0'})
 await settle()
 expect(f.card('tailscale').querySelector('[data-binary-in-use-loading]')).toBeNull()
 expect(f.card('tailscale').querySelector('[data-binary-in-use]')?.textContent).toBe(t('inUse',{version:'1.0.0'}))
 f.done()
})

// A check in progress never shows the previous check's GitHub limit and its passed retry time.
test('checking hides the previous rate-limit error and says it is checking',async()=>{
 const old={current:'0.80.0',runtimeVersion:'0.80.0',phase:'ready',error:'rate limited',errorCode:'github_rate_limited',retryAt:'2026-10-14T18:03:00Z'}
 let reply:(s:unknown)=>void=()=>{},now:Record<string,unknown>=old
 const f=mount(()=>now,{'binaries.check':()=>new Promise(resolve=>{reply=resolve})});await settle()
 expect(f.card('netbird').querySelector('[role=alert]')).not.toBeNull()
 ;[...f.card('netbird').querySelectorAll('button')].find(b=>b.textContent?.trim()===t('check'))!.click();await settle()
 expect(f.card('netbird').querySelector('[role=alert]')).toBeNull()
 expect(f.card('netbird').querySelector('button[data-loading=true]')?.textContent).toBe(t('check'))
 // The daemon answers while the job waits its turn, still holding the old result.
 now={...old,busy:true,phase:'queued'}
 reply({...now,product:'netbird',previous:'',latest:''});await settle()
 expect(f.card('netbird').querySelector('[role=alert]')).toBeNull()
 f.done()
})

test('the NetBird server card renders with its own name and updates through binaries.update',async()=>{
 const f=mount(()=>({current:'0.60.0',runtimeVersion:'0.60.0',latest:'0.61.0',updateAvailable:true}));await settle()
 expect(f.card('netbird-server').textContent).toContain(t('name.netbird-server'))
 expect(f.card('netbird-server').querySelector('[data-binary-source]')?.textContent).toBe(t('source.netbird-server'))
 expect(f.call).toHaveBeenCalledWith('binaries.status',{product:'netbird-server'})
 ;[...f.card('netbird-server').querySelectorAll('button')].find(b=>b.textContent?.trim()===t('updateTo',{version:'0.61.0'}))!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('binaries.update',{product:'netbird-server'})
 f.done()
})

test('the NetBird server card says a pending download applies on server restart',async()=>{
 const f=mount(()=>({current:'0.61.0',runtimeVersion:'0.60.0',latest:'0.61.0'}));await settle()
 expect(f.card('netbird-server').querySelector('[data-binary-pending]')?.textContent).toBe(t('pendingServer',{version:'0.61.0'}))
 f.done()
})
