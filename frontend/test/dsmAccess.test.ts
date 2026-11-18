// @vitest-environment jsdom
// DSM's own QuickConnect and DDNS on the Home: read only, in the
// administrator's session, after the Home's own status; one lane each, all
// DDNS records merged into one hub; nothing drawn when off or unreadable.
import {afterEach,expect,test,vi} from 'vitest'
import {ddnsLane,ddnsOf,ddnsUrl,dsmHttpsPort,quickConnectLane,quickConnectOf,readDsmAccess} from '../src/connect/dsmAccess'
import {settle} from './support'
import {connected,fixture,t} from './providerFixture'
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals()})

const files={token:()=>'fixture-token'} as never
const record=(hostname:string,extra:Record<string,unknown>={})=>({enable:true,hostname,id:'Synology',provider:'Synology',status:'service_ddns_normal',ip:'203.0.113.7',ipv6:'',...extra})

function dsm(replies:Record<string,unknown>){
 const fetch=vi.fn(async(url:string,init:RequestInit)=>{
  expect(url).toBe('/webapi/entry.cgi')
  expect(init.signal).toBeUndefined()
  const form=new URLSearchParams(String(init.body))
  expect(new Headers(init.headers).get('X-SYNO-TOKEN')).toBe('fixture-token')
  const reply=replies[`${form.get('api')} ${form.get('method')} ${form.get('version')}`]
  return {ok:true,json:async()=>reply??{success:false,error:{code:105}}}
 })
 vi.stubGlobal('fetch',fetch)
 return fetch
}
const qc=(enabled:boolean,status='connected')=>({'SYNO.Core.QuickConnect get 2':{success:true,data:{enabled,server_alias:'my-nas',domain:'quickconnect.to'}},'SYNO.Core.QuickConnect status 1':{success:true,data:{status,alias_status:'success'}}})
const ddns=(...records:unknown[])=>({'SYNO.Core.DDNS.Record list 1':{success:true,data:{next_update_time:'',records}}})
const ports=(https:number,external:Record<string,unknown>={hostname:''})=>({'SYNO.Core.Web.DSM get 2':{success:true,data:{http_port:5000,https_port:https}},'SYNO.Core.Web.DSM.External get 1':{success:true,data:external}})

test('QuickConnect counts only while DSM has it on, with its ID and its connection to Synology',()=>{
 expect(quickConnectOf({enabled:false,server_alias:'x'},{status:'connected'})).toBeUndefined()
 expect(quickConnectOf(undefined,undefined)).toBeUndefined()
 expect(quickConnectOf({enabled:true,server_alias:'my-nas'},{status:'connected'})).toEqual({id:'my-nas',status:'connected'})
 // Its address is the ID under the domain DSM names, never a guessed one.
 expect(quickConnectOf({enabled:true,server_alias:'My-NAS',domain:'quickconnect.to'},{status:'connected'})?.url).toBe('https://my-nas.quickconnect.to')
 expect(quickConnectOf({enabled:true,server_alias:'my-nas',domain:'quickconnect.cn'},{status:'connected'})?.url).toBe('https://my-nas.quickconnect.cn')
 expect(quickConnectOf({enabled:true,server_alias:'my-nas',domain:''},{status:'connected'})?.url).toBeUndefined()
 expect(quickConnectOf({enabled:true,server_alias:'bad id/x',domain:'quickconnect.to'},{status:'connected'})?.url).toBeUndefined()
 expect(quickConnectLane({id:'my-nas',status:'connected',url:'https://my-nas.quickconnect.to'})?.addresses).toEqual(['https://my-nas.quickconnect.to'])
 expect(quickConnectLane({id:'my-nas',status:'connected'})?.addresses).toEqual([])
 expect(quickConnectOf({enabled:true,server_alias:'my-nas'},{status:'not_running'})?.status).toBe('not-connected')
 expect(quickConnectOf({enabled:true,server_alias:'my-nas'},undefined)?.status).toBe('unknown')
 expect(quickConnectLane({id:'my-nas',status:'connected'})?.links[1]).toEqual({state:'active',label:'overview.dsm.qcConnected'})
 expect(quickConnectLane({id:'my-nas',status:'not-connected'})?.links[1].state).toBe('error')
 // Nobody checked it from outside.
 expect(quickConnectLane({id:'my-nas',status:'connected'})?.links[0].state).toBe('unknown')
 expect(quickConnectLane(undefined)).toBeUndefined()
})

test('only DDNS records DSM keeps up to date count, read as Control Panel reads their status',()=>{
 const records=ddnsOf({records:[
  record('a.synology.me'),
  record('off.example.com',{enable:false}),
  record('disabled.example.com',{status:'disabled'}),
  record('b.example.com',{provider:'NoIP.com',id:'NoIP.com',status:'invalid login'}),
  record('c.example.com',{status:'ip_not_ready',ip:'',ipv6:'2001:db8::7'}),
  record('d.example.com',{status:'2026-11-6 21:50'}),
  {enable:true,hostname:''},
 ]})
 expect(records).toEqual([
  {hostname:'a.synology.me',provider:'Synology',status:'normal',ip:'203.0.113.7'},
  {hostname:'b.example.com',provider:'NoIP.com',status:'failed',ip:'203.0.113.7'},
  {hostname:'c.example.com',provider:'Synology',status:'updating',ip:'2001:db8::7'},
  {hostname:'d.example.com',provider:'Synology',status:'normal',ip:'203.0.113.7'},
 ])
 expect(ddnsOf(undefined)).toEqual([])
 // One lane for all of them: a failure wins, then one still updating.
 expect(ddnsLane(records)?.links[1]).toEqual({state:'error',label:'overview.dsm.ddnsFailed'})
 expect(ddnsLane(records.filter(r=>r.status!=='failed'))?.links[1].state).toBe('pending')
 expect(ddnsLane(records.filter(r=>r.status==='normal'))?.links[1]).toEqual({state:'active',label:'overview.dsm.ddnsNormal'})
 expect(ddnsLane([])).toBeUndefined()
})

test('a DDNS address is the hostname on DSM\'s HTTPS port: the external port DSM was given, else its own',()=>{
 expect(dsmHttpsPort({https_port:5001},{hostname:''})).toBe(5001)
 expect(dsmHttpsPort({https_port:5001},{hostname:'',https_port:443})).toBe(443)
 expect(dsmHttpsPort({https_port:5001},{hostname:'',https_port:0})).toBe(5001)
 expect(dsmHttpsPort(undefined,undefined)).toBeUndefined()
 expect(ddnsUrl('A.synology.me',5001)).toBe('https://a.synology.me:5001')
 expect(ddnsUrl('a.synology.me',443)).toBe('https://a.synology.me')
 // Without DSM's port there is no address to show.
 expect(ddnsUrl('a.synology.me',undefined)).toBeUndefined()
 const records=ddnsOf({records:[record('a.synology.me'),record('b.example.com')]},5001)
 expect(records.map(r=>r.url)).toEqual(['https://a.synology.me:5001','https://b.example.com:5001'])
 expect(ddnsLane(records)?.addresses).toEqual(['https://a.synology.me:5001','https://b.example.com:5001'])
 expect(ddnsLane(ddnsOf({records:[record('a.synology.me')]}))?.addresses).toEqual([])
})

test('a failed or refused read is nothing, never an error',async()=>{
 dsm({})
 expect(await readDsmAccess({files} as never)).toEqual({ddns:[]})
 vi.stubGlobal('fetch',vi.fn(async()=>{throw new TypeError('offline')}))
 expect(await readDsmAccess({files} as never)).toEqual({ddns:[]})
 expect(await readDsmAccess({files:undefined} as never)).toEqual({ddns:[]})
})

test('the Home adds QuickConnect and one merged DDNS lane after the ways it set up, and say where to open them',async()=>{
 const fetch=dsm({...qc(true),...ports(5001,{hostname:'',https_port:8443}),...ddns(record('a.synology.me'),record('b.example.com',{provider:'NoIP.com'}),record('c.example.com',{provider:'DYNDNS.org'}))})
 const f=fixture([connected('tailscale')],{},files);await settle()
 const home=f.page('status')
 const ids=[...home.querySelectorAll('[data-overview] [data-lane-id]')].map(e=>e.getAttribute('data-lane-id'))
 expect(ids).toEqual(['tailscale','quickconnect','ddns'])
 const q=home.querySelector<HTMLElement>('[data-lane-id="quickconnect"]')!
 expect(q.textContent).toContain('QuickConnect')
 expect(q.textContent).toContain('my-nas')
 expect(q.textContent).toContain(t('overview.dsm.qcConnected'))
 // Both lanes list their addresses as every other lane does: copy and open.
 const fields=(lane:string)=>[...home.querySelectorAll<HTMLInputElement>(`[data-overview] [data-lane-id="${lane}"] [data-url-card] input`)].map(i=>i.value)
 expect(fields('quickconnect')).toEqual(['https://my-nas.quickconnect.to'])
 expect(fields('ddns')).toEqual(['https://a.synology.me:8443','https://b.example.com:8443','https://c.example.com:8443'])
 expect(home.querySelector('[data-url-phone]')).toBeNull()
 const d=home.querySelector<HTMLElement>('[data-lane-id="ddns"]')!
 expect(d.textContent).toContain(t('home.ddns.records').replace('{count}','3'))
 const table=home.querySelector<HTMLElement>('[data-lane-ddns]')!
 for(const text of ['a.synology.me','b.example.com','c.example.com','NoIP.com','DYNDNS.org','203.0.113.7',t('home.ddns.status.normal')])expect(table.textContent).toContain(text)
 // Read once per refresh, never on a timer.
 const reads=fetch.mock.calls.length
 expect(reads).toBe(5)
 await new Promise(r=>setTimeout(r,30));await settle()
 expect(fetch.mock.calls.length).toBe(reads)
 // Outside a DSM window External Access cannot open: a toast says where it is.
 home.querySelector<HTMLElement>('[data-lane-id="ddns"] [data-topology-row]')!.click();await settle()
 expect(document.body.textContent).toContain(t('home.dsm.openInDsm'))
 f.done()
})

test('QuickConnect off and no DDNS: nothing more is drawn and nothing is said',async()=>{
 dsm({...qc(false),...ddns(record('off.example.com',{enable:false}))})
 const f=fixture([connected('tailscale')],{},files);await settle()
 const home=f.page('status')
 expect([...home.querySelectorAll('[data-overview] [data-lane-id]')].map(e=>e.getAttribute('data-lane-id'))).toEqual(['tailscale'])
 expect(home.querySelector('[data-overview]')!.textContent).not.toContain('QuickConnect')
 expect(home.querySelector('[data-lane-ddns]')).toBeNull()
 f.done()
})

test('DSM\'s own ways never turn a first visit into a returning one',async()=>{
 dsm({...qc(true),...ddns(record('a.synology.me'))})
 const f=fixture([],{'apps.workersstatus':()=>({connected:false})},files);await settle()
 const home=f.page('status')
 expect(home.querySelector('[data-home-mode]')?.getAttribute('data-home-mode')).toBe('first')
 f.done()
})
