// @vitest-environment jsdom
// After an own Cloudflare address is running, Connect compares DSM's External
// Access address with it and offers one button to apply it with the
// administrator's own DSM session; the same value never asks again.
import {afterEach,expect,test,vi} from 'vitest'
import type {ConnectionStatus} from '../src/context'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals()})

const LOCALES=['en','zh-TW','zh-CN','ja','ko','de','fr','it','es']
const KEYS=['dsmExternal.notSet','dsmExternal.current','dsmExternal.suggest','dsmExternal.apply','dsmExternal.manual','dsmExternal.applied','dsmExternal.failed']
const domain=connected('cloudflare',{mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})
// DSM refuses the loopback replay: the UI must use the browser's origin.
const backend={
 'external.status':()=>{throw Error('cookie replay')},
 'external.get':()=>{throw Error('cookie replay')},
 'external.set':()=>{throw Error('cookie replay')},
}
function native(current:Record<string,unknown>={hostname:''},write:'ok'|'denied'|'unconfirmed'='ok'){
 const methods:string[]=[]
 const fetch=vi.fn(async(url:string,init:RequestInit)=>{
  expect(url).toBe('/webapi/entry.cgi')
  expect(init).toMatchObject({method:'POST',credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store'})
  const form=new URLSearchParams(String(init.body))
  // Home also reads DSM's QuickConnect and DDNS, independently of the
  // External Access suggestion. Keep those read-only requests distinct.
  if(form.get('api')==='SYNO.Core.QuickConnect'||form.get('api')==='SYNO.Core.DDNS.Record'){
   expect(form.get('method')).toBe(form.get('api')==='SYNO.Core.DDNS.Record'?'list':form.get('version')==='2'?'get':'status')
   return {ok:true,json:async()=>({success:true,data:{enabled:false,records:[]}})}
  }
  expect(form.get('api')).toBe('SYNO.Core.Web.DSM.External')
  expect(form.get('version')).toBe('1')
  expect(form.get('SynoToken')).toBe('fixture-token')
  expect(new Headers(init.headers).get('X-SYNO-TOKEN')).toBe('fixture-token')
  const method=form.get('method')!;methods.push(method)
  if(method==='set'){
   expect(form.get('hostname')).toBe(JSON.stringify('nas.example.com'))
   expect(form.get('https_port')).toBe('443')
   expect(form.has('http_port')).toBe(false)
   if(write==='denied')return {ok:true,json:async()=>({success:false,error:{code:105}})}
   if(write==='ok')current={...current,hostname:'nas.example.com',https_port:443}
  }
  return {ok:true,json:async()=>({success:true,data:current})}
 })
 vi.stubGlobal('fetch',fetch)
 return {fetch,methods}
}
async function open(connections:ConnectionStatus[],token:string|undefined='fixture-token'){
 const f=fixture(connections,backend,{endpoint:'cgi/api.cgi',token:()=>token});await settle();await f.nav('Cloudflare');await settle()
 return {...f,cf:f.page('cloudflare'),prompt:()=>f.page('cloudflare').querySelector<HTMLElement>('[data-dsm-external]')}
}
function noReplay(call:ReturnType<typeof vi.fn>){
 expect(call.mock.calls.filter(([method])=>String(method).startsWith('external.'))).toEqual([])
}

test('opening Connect reads at the browser origin and writes only on consent with confirmed readback',async()=>{
 const n=native()
 const f=await open([domain])
 try{
  const prompt=f.prompt()!
  expect(prompt?.textContent).toContain(t('dsmExternal.notSet'))
  expect(prompt.textContent).toContain(t('dsmExternal.suggest').replace('{host}','nas.example.com'))
  expect(n.methods).toEqual(['get'])
  prompt.querySelector<HTMLButtonElement>('[data-dsm-external-apply]')!.click();await settle()
  expect(n.methods).toEqual(['get','set','get'])
  // Done is a passing toast; the task leaves the page.
  expect(f.prompt()).toBeNull()
  expect(document.body.textContent).toContain(t('dsmExternal.applied').replace('{host}','nas.example.com'))
  noReplay(f.call)
 }finally{f.done()}
})

test('a different DSM address is shown and kept until the administrator chooses to apply',async()=>{
 const n=native({hostname:'old.example.net',https_port:5001})
 const f=await open([domain])
 try{
  expect(f.prompt()?.textContent).toContain(t('dsmExternal.current').replace('{current}','old.example.net · HTTPS 5001'))
  expect(n.methods).toEqual(['get']);noReplay(f.call)
 }finally{f.done()}
})

test('a DSM address that already matches asks nothing',async()=>{
 native({hostname:'nas.example.com',https_port:443})
 const f=await open([domain])
 try{expect(f.prompt()).toBeNull();noReplay(f.call)}finally{f.done()}
})

test('a temporary address is never suggested and DSM External Access is not asked',async()=>{
 const n=native()
 const f=await open([connected('cloudflare',{mode:'quick',hostname:'a-b.trycloudflare.com'})])
 try{
  expect(f.prompt()).toBeNull()
  expect(n.methods).toEqual([])
  expect(n.fetch.mock.calls.map(([,init])=>new URLSearchParams(String(init.body)).get('api'))).toEqual([
   'SYNO.Core.QuickConnect','SYNO.Core.QuickConnect','SYNO.Core.DDNS.Record',
  ])
  noReplay(f.call)
 }finally{f.done()}
})

test.each(['denied','unconfirmed'] as const)('a %s change never claims success and keeps the button',async write=>{
 native({hostname:''},write)
 const f=await open([domain])
 try{
  f.prompt()!.querySelector<HTMLButtonElement>('[data-dsm-external-apply]')!.click();await settle()
  expect(f.prompt()!.querySelector('[role=status]')?.textContent).toBe(t('dsmExternal.failed'))
  expect(f.prompt()!.querySelector('[data-dsm-external-apply]')).not.toBeNull()
  expect(f.prompt()!.textContent).not.toContain(t('dsmExternal.applied').replace('{host}','nas.example.com'))
  noReplay(f.call)
 }finally{f.done()}
})

test('malformed DSM readback shows no suggestion',async()=>{
 native({provider:'multiple',state:'connected'})
 const f=await open([domain])
 try{expect(f.prompt()).toBeNull();noReplay(f.call)}finally{f.done()}
})

test('missing SynoToken fails closed without falling back to the backend',async()=>{
 const n=native()
 const f=await open([domain],'')
 try{expect(f.prompt()).toBeNull();expect(n.fetch).not.toHaveBeenCalled();noReplay(f.call)}finally{f.done()}
})

test.each(LOCALES)('the DSM address prompt has words in %s without a trailing period',locale=>{
 for(const key of KEYS){
  const text=translate(locale,key)
  expect(text,`${locale} ${key}`).not.toBe(key)
  if(locale!=='en')expect(text,`${locale} ${key}`).not.toBe(translate('en',key))
  expect(text,`${locale} ${key}`).not.toMatch(/[.。]$/)
 }
 expect(translate(locale,'dsmExternal.suggest')).toContain('{host}')
 expect(translate(locale,'dsmExternal.suggest')).toContain('443')
 expect(translate(locale,'dsmExternal.current')).toContain('{current}')
 expect(translate(locale,'dsmExternal.applied')).toContain('{host}')
})
