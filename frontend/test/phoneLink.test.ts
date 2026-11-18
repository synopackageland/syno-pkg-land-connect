// @vitest-environment jsdom
// Connect your phone: the add-NAS QR code v1 (shared fixture with the mobile
// app), the address order and limits, and the phone browser page's address.
import sharedFixture from './qr-v1-fixture.json'
import {afterEach,expect,test,vi} from 'vitest'
import {connected} from './providerFixture'
import {browserLink,browserPath,temporaryAddress,ADD_NAS_SCHEME,MAX_PAYLOAD_BYTES,addNasLink,currentLocation,encodePayload,externalAddresses,originOf,payloadBytes,phoneCode,type AddNasPayload,type PhoneAddNasReply} from '../src/connect/phoneLink'
afterEach(()=>{vi.restoreAllMocks();document.body.innerHTML=''})

const shared=sharedFixture as {payload:AddNasPayload;qr:string}
const pin='0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
const decode=(link:string)=>JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(link.slice(ADD_NAS_SCHEME.length).replace(/-/g,'+').replace(/_/g,'/')),c=>c.charCodeAt(0))))

test('the shared v1 fixture encodes to its expected QR string and back',()=>{
 expect(addNasLink(shared.payload)).toBe(shared.qr)
 expect(shared.qr.startsWith('synopkgland://add-nas#')).toBe(true)
 expect(shared.qr).not.toMatch(/[=+/]$/)
 expect(decode(shared.qr)).toEqual(shared.payload)
 // phoneCode produces the same string from the same addresses.
 const lan={name:'DiskStation',ports:true,addresses:[{url:'https://192.168.1.10:5001',kind:'lan' as const,sha256:pin}]}
 const code=phoneCode('DiskStation',lan,[{url:'https://nas.example.com',kind:'cf',via:'cloudflare'},{url:'https://nas.netbird.example:5001',kind:'netbird',via:'netbird'}])
 expect(code?.link).toBe(shared.qr)
})

test('a name in any script is UTF-8 and base64url without padding',()=>{
 const payload:AddNasPayload={v:1,name:'客廳 NAS ü?>',urls:[{url:'https://192.168.1.2:5001',kind:'lan'}]}
 const encoded=encodePayload(payload)
 expect(encoded).toMatch(/^[A-Za-z0-9_-]+$/)
 expect(decode(ADD_NAS_SCHEME+encoded)).toEqual(payload)
})

test('origins only: path, query and credentials go, other schemes are refused',()=>{
 expect(originOf('http://100.90.1.2:5000/')).toBe('http://100.90.1.2:5000')
 expect(originOf('https://nas.example.com/path?x=1#y')).toBe('https://nas.example.com')
 expect(originOf('https://user:pw@nas.example.com')).toBeUndefined()
 expect(originOf('ftp://nas.example.com')).toBeUndefined()
 expect(originOf('not a url')).toBeUndefined()
})

test('external ways: only connected ones, names before IPs, Tailscale as other',()=>{
 const ext=externalAddresses({
  cloudflare:connected('cloudflare',{endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]}),
  netbird:connected('netbird',{endpoints:[{url:'http://100.90.1.2:5000/',public:false,localPort:5000},{url:'http://nas.netbird.cloud:5000/',public:false,localPort:5000}]}),
  tailscale:{...connected('tailscale',{endpoints:[{url:'http://100.101.1.2:5000/',public:false,localPort:5000}]}),state:'disconnected'},
  ipv6:connected('ipv6',{endpoints:[{url:'https://[2001:db8::1]:5001/',public:true,localPort:5001}]}),
 },{connected:true,relayState:'connected',dsm:{provider:'workers',url:'https://dsm.acme.workers.dev/'}})
 expect(ext).toEqual([
  {url:'https://nas.example.com',kind:'cf',via:'cloudflare'},
  {url:'https://dsm.acme.workers.dev',kind:'cf',via:'workers'},
  {url:'http://nas.netbird.cloud:5000',kind:'netbird',via:'netbird'},
  {url:'http://100.90.1.2:5000',kind:'netbird',via:'netbird'},
 ])
 const quick=externalAddresses({cloudflare:connected('cloudflare',{mode:'quick',endpoints:[{url:'https://a-b.trycloudflare.com',public:true,localPort:5000}]}),tailscale:connected('tailscale',{endpoints:[{url:'http://100.101.1.2:5000/',public:false,localPort:5000}]})},{connected:false,relayState:'disconnected'})
 // A temporary Cloudflare address changes on restart, so the phone never saves it.
 expect(quick).toEqual([{url:'http://100.101.1.2:5000',kind:'other',via:'tailscale'}])
 expect(temporaryAddress({cloudflare:connected('cloudflare',{mode:'quick',endpoints:[{url:'https://a-b.trycloudflare.com',public:true,localPort:5000}]})})).toBe('https://a-b.trycloudflare.com')
 expect(temporaryAddress({cloudflare:connected('cloudflare',{mode:'domain',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})})).toBeUndefined()
 expect(externalAddresses({cloudflare:connected('cloudflare',{endpoints:[{url:'https://x.trycloudflare.com',public:true,localPort:5000}]})})).toEqual([])
})

test('a browser on the temporary Cloudflare address does not bring it into the code',()=>{
 const code=phoneCode('n',{name:'n',ports:true,addresses:[{url:'https://192.168.1.20:5001',kind:'lan'},{url:'https://a-b.trycloudflare.com',kind:'other'}]},[])!
 expect(code.payload.urls).toEqual([{url:'https://192.168.1.20:5001',kind:'lan'}])
})

test('the code orders LAN, external, then the browser address, dedupes and holds no secret',()=>{
 const nas:PhoneAddNasReply={name:'DiskStation',ports:true,addresses:[
  {url:'https://192.168.1.20:5001',kind:'lan',sha256:pin},
  {url:'https://192.168.1.20:5001',kind:'lan',sha256:pin},
  {url:'https://nas.example.com',kind:'other'},
  {url:'https://ddns.example.net:5001',kind:'other',sha256:'NOT-HEX'},
 ]}
 const code=phoneCode('DiskStation',nas,[{url:'https://nas.example.com',kind:'cf',via:'cloudflare'}])!
 expect(code.payload).toEqual({v:1,name:'DiskStation',urls:[
  {url:'https://192.168.1.20:5001',kind:'lan',sha256:pin},
  {url:'https://nas.example.com',kind:'cf'},
  {url:'https://ddns.example.net:5001',kind:'other'},
 ]})
 expect(Object.keys(code.payload).sort()).toEqual(['name','urls','v'])
 for(const u of code.payload.urls)expect(Object.keys(u).every(k=>['url','kind','sha256'].includes(k))).toBe(true)
 // An http origin never carries a pin.
 expect(phoneCode('n',{name:'n',ports:true,addresses:[{url:'http://192.168.1.20:5000',kind:'lan',sha256:pin}]},[])!.payload.urls).toEqual([{url:'http://192.168.1.20:5000',kind:'lan'}])
 expect(phoneCode('n',{name:'n',ports:false,addresses:[]},[])).toBeUndefined()
})

test('at most 8 addresses and 2 KB of payload',()=>{
 const many=Array.from({length:12},(_,i)=>({url:`https://192.168.1.${i+1}:5001`,kind:'lan' as const,sha256:pin}))
 expect(phoneCode('n',{name:'n',ports:true,addresses:many},[])!.payload.urls).toHaveLength(8)
 const long=Array.from({length:8},(_,i)=>({url:`https://${'a'.repeat(60)}.${'b'.repeat(60)}.example${i}.com`,kind:'cf' as const,via:'cloudflare' as const}))
 const code=phoneCode('x'.repeat(200),{name:'',ports:true,addresses:many.slice(0,2)},long)!
 expect(payloadBytes(code.payload)).toBeLessThanOrEqual(MAX_PAYLOAD_BYTES)
 expect(code.payload.urls.length).toBeLessThan(10)
 expect(code.payload.urls[0].kind).toBe('lan')
})

test('the browser location goes to the backend without a default port',()=>{
 expect(currentLocation({protocol:'https:',hostname:'192.168.1.20',port:'5001'})).toEqual({protocol:'https:',host:'192.168.1.20',port:5001})
 expect(currentLocation({protocol:'https:',hostname:'nas.example.com',port:''})).toEqual({protocol:'https:',host:'nas.example.com'})
})

test('the phone browser page has one fixed address on DSM\'s own origin, the same after an update',()=>{
 expect(browserPath()).toBe('/m')
 expect(browserLink('https://192.168.1.20:5001/')).toBe('https://192.168.1.20:5001/m')
})
