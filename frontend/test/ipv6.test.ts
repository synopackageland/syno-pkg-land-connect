// @vitest-environment jsdom
// IPv6 direct: an honest checklist of what the app detects and what the user
// must do, the DSM web service by default, and "Ready (unverified)" once on.
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

const dsm={detection:'ready',mappingState:'pending-provider',revision:1,entries:[
 {id:'dsm-http',name:'DSM HTTP',protocol:'http',port:5000,source:'dsm',enabled:true,state:'ready'},
 {id:'dsm-https',name:'DSM HTTPS',protocol:'https',port:5001,source:'dsm',enabled:true,state:'ready'}]}
const global={state:'available',reason:'external_unverified',addresses:[{ip:'2606:4700:1234::1',interface:'eth0',prefixBits:64}],endpoints:[],checkedAt:'2026-10-12T00:00:00Z'}
const none={state:'unavailable',reason:'no_global_address',addresses:[],endpoints:[],checkedAt:'2026-10-12T00:00:00Z'}
const item=(page:HTMLElement,name:string)=>page.querySelector<HTMLElement>(`[data-check-item="${name}"]`)!

async function open(connections=[] as Parameters<typeof fixture>[0],status:unknown=global,handlers={}){
 const f=fixture(connections,{'ports.list':()=>dsm,'ipv6.status':()=>status,...handlers});await settle()
 return {...f,ipv6:f.page('ipv6')}
}

test('the checklist says what the app detects and what the user must check',async()=>{
 const f=await open()
 expect(item(f.ipv6,'address').dataset.checkState).toBe('ok')
 expect(item(f.ipv6,'address').textContent).toContain('2606:4700:1234::1')
 for(const name of ['firewall','router'])expect(item(f.ipv6,name).dataset.checkState).toBe('confirm')
 expect(item(f.ipv6,'firewall').textContent).toContain(t('connect.ipv6.check.firewall.detail'))
 expect(item(f.ipv6,'router').textContent).toContain(t('connect.ipv6.check.router.detail'))
 expect(item(f.ipv6,'ddns').dataset.checkState).toBe('optional')
 expect(item(f.ipv6,'external').dataset.checkState).toBe('pending')
 expect(f.call).toHaveBeenCalledWith('ipv6.status')
 f.done()
})

test('each check is a ListItem: its name as the title, its state in the meta, then what it means and what to do',async()=>{
 const f=await open([],none)
 const list=f.ipv6.querySelector<HTMLElement>('[data-ipv6-checklist] [role="list"]')!
 const rows=[...list.querySelectorAll<HTMLElement>(':scope > [role="listitem"]')]
 expect(rows.map(row=>row.dataset.checkItem)).toEqual(['address','firewall','router','ddns','external'])
 const address=item(f.ipv6,'address')
 expect(address.querySelector('[data-ui-list-item-title]')?.textContent?.trim()).toBe(t('connect.ipv6.check.address.title'))
 expect(address.querySelector('[data-ui-list-item-meta]')?.textContent).toContain(t('connect.ipv6.check.missing'))
 expect(address.querySelector('[data-ui-list-item-description]')?.textContent).toContain(t('connect.ipv6.check.address.missing'))
 expect(address.querySelector('[data-ui-list-item-actions] [data-open-network]')).toBeTruthy()
 expect(item(f.ipv6,'firewall').querySelector('[data-ui-list-item-actions] [data-open-firewall]')).toBeTruthy()
 expect(address.querySelector('dt')).toBeNull()
 f.done()
})

test('without a global address the checklist says so and links to the network settings',async()=>{
 const f=await open([],none)
 const address=item(f.ipv6,'address')
 expect(address.dataset.checkState).toBe('missing')
 expect(address.textContent).toContain(t('connect.ipv6.check.address.missing'))
 expect(address.querySelector('[data-open-network]')).toBeTruthy()
 f.done()
})

test('an unreadable status leaves the address unknown, never detected',async()=>{
 const f=await open([],undefined,{'ipv6.status':()=>{throw {code:'session_required'}}})
 expect(item(f.ipv6,'address').dataset.checkState).toBe('unknown')
 f.done()
})

test('the guide checks prerequisites without enabling IPv6 or asking for consent',async()=>{
 const f=await open([],none)
 expect(f.ipv6.querySelector('[data-ipv6-connect],[data-ipv6-confirm],[data-ipv6-apply]')).toBeNull()
 expect(f.ipv6.querySelector('[data-ipv6-status]')?.textContent).toBe(t('connect.ipv6.notSetUp'))
 expect(f.ipv6.querySelector('[data-ipv6-services]')?.textContent).toContain('DSM HTTPS (5001)')
 f.ipv6.querySelector<HTMLElement>('[data-ipv6-check-again]')!.click();await settle()
 expect(f.call.mock.calls.filter(([m])=>m==='ipv6.status')).toHaveLength(2)
 expect(f.call.mock.calls.some(([m])=>m==='connection.connect'||m==='ports.prepare')).toBe(false)
 f.done()
})

test.each(['failed','connected'])('legacy %s state is neutral until an outside check succeeds',async state=>{
 const f=await open([connected('ipv6',{state,code:'provider_start_failed'})])
 expect(f.ipv6.querySelector('[data-ipv6-status]')?.textContent).toBe(t('connect.ipv6.waiting'))
 expect(item(f.ipv6,'external').dataset.checkState).toBe('pending')
 expect(f.ipv6.querySelector('[data-connection-reason],[data-stop]')).toBeNull()
 expect(f.ipv6.textContent).not.toContain(t('attention.failed'))
 f.done()
})

test('only an outside check reaching the NAS shows Working',async()=>{
 let verified=false
 const f=await open([connected('ipv6')],global,{'connection.check':()=>({provider:'ipv6',state:'ok',externalVerified:verified})})
 expect(f.ipv6.querySelector('[data-ipv6-status]')?.textContent).toBe(t('connect.ipv6.waiting'))
 verified=true
 f.ipv6.querySelector<HTMLElement>('[data-ipv6-check-again]')!.click();await settle()
 expect(f.ipv6.querySelector('[data-ipv6-status]')?.textContent).toBe(t('connect.ipv6.working'))
 expect(item(f.ipv6,'external').dataset.checkState).toBe('ok')
 f.done()
})

test('saved services are named, not counted as mappings',async()=>{
 const f=await open([{provider:'ipv6',state:'disconnected',simulation:false,shareUrl:'',serviceIds:['dsm-http','dsm-https'],mappings:[{localPort:5000,protocol:'http',publicPort:5000},{localPort:5001,protocol:'https',publicPort:5001}]}])
 expect(f.ipv6.querySelector('[data-connection-mappings]')).toBeNull()
 expect(f.ipv6.querySelector('[data-ipv6-services]')?.textContent).toContain('DSM HTTP (5000) and DSM HTTPS (5001)')
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the IPv6 checklist is translated in %s',locale=>{
 const keys=['notSetUp','waiting','working','guide','opens','check.title','check.ok','check.missing','check.unknown','check.confirm','check.optional','check.pending',
  'check.address.title','check.address.missing','check.firewall.title','check.firewall.detail','check.router.title','check.router.detail',
  'check.ddns.title','check.ddns.detail','check.external.title','check.external.phone']
 for(const key of keys.map(k=>`connect.ipv6.${k}`))
  expect(translate(locale,key),`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
})

// Every item reads the same way: the title in the label column, then its
// state, what it means and its action, all aligned to the start.
