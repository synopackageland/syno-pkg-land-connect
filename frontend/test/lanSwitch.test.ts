// @vitest-environment jsdom
// Switch to the local network: which pages check, which candidates count, and
// the parallel ICE checks (first to connect wins, anything uncertain offers nothing).
import {afterEach,expect,test,vi} from 'vitest'
import {createApp} from 'vue'
import LanSwitchNotice from '../src/components/LanSwitchNotice.vue'
import {answerSdp,candidateOk,findLan,privateIPv4,shouldProbe,type LanProbe} from '../src/connect/lanSwitch'
import {settle} from './support'
import {fixture,t} from './providerFixture'
afterEach(()=>{vi.restoreAllMocks();vi.useRealTimers();document.body.innerHTML=''})

const probe=(over:Partial<LanProbe>={}):LanProbe=>({enabled:true,ufrag:'abcdefgh',pwd:'abcdefghijklmnopqrstuvwxyz',https:false,candidates:[
 {address:'192.168.1.10',port:45001,url:'http://192.168.1.10:5000'},
 {address:'10.0.0.5',port:45001,url:'http://10.0.0.5:5000'},
],...over})

// A fake peer: connects (or fails) after a delay when its remote answer names an address.
class FakePeer {
 static plan:Record<string,{state:'connected'|'failed'|'never';ms:number}>={}
 static made:FakePeer[]=[]
 iceConnectionState='new'
 oniceconnectionstatechange:(()=>void)|null=null
 closed=false
 sdp=''
 constructor(){FakePeer.made.push(this)}
 createDataChannel(){return {}}
 async createOffer(){return {type:'offer',sdp:'v=0'}}
 async setLocalDescription(){}
 async setRemoteDescription(d:{sdp:string}){
  this.sdp=d.sdp
  const address=/typ host/.test(d.sdp)?d.sdp.split('a=candidate:1 1 tcp 1518280447 ')[1].split(' ')[0]:''
  const p=FakePeer.plan[address]??{state:'never',ms:0}
  if(p.state!=='never')setTimeout(()=>{if(this.closed)return;this.iceConnectionState=p.state;this.oniceconnectionstatechange?.()},p.ms)
 }
 close(){this.closed=true;this.iceConnectionState='closed'}
}
const make=()=>new FakePeer() as unknown as RTCPeerConnection

test('only an https page on a DNS name checks', () => {
 expect(shouldProbe({protocol:'https:',hostname:'nas.example.com'})).toBe(true)
 expect(shouldProbe({protocol:'https:',hostname:'abc.trycloudflare.com'})).toBe(true)
 for(const [protocol,hostname] of [['http:','nas.example.com'],['https:','192.168.1.10'],['https:','[fd00::1]'],['https:','diskstation'],['https:','diskstation.local'],['https:','nas.example.com.']])
  expect(shouldProbe({protocol,hostname}),hostname).toBe(false)
})

test('a candidate is a private IPv4 origin on its own address', () => {
 for(const a of ['10.1.2.3','172.17.0.1',...[16,31].map(n=>`172.${n}.0.1`),'192.168.0.1'])expect(privateIPv4(a),a).toBe(true)
 for(const a of ['100.64.0.1','172.32.0.1','8.8.8.8','192.169.0.1','010.0.0.1','1.2.3','256.1.1.1'])expect(privateIPv4(a),a).toBe(false)
 expect(candidateOk({address:'192.168.1.10',port:45001,url:'https://192.168.1.10:5001'})).toBe(true)
 for(const url of ['http://192.168.1.11:5000','http://192.168.1.10:5000/x','http://192.168.1.10:5000?q','http://u:p@192.168.1.10:5000','ftp://192.168.1.10','javascript:alert(1)'])
  expect(candidateOk({address:'192.168.1.10',port:45001,url}),url).toBe(false)
 expect(candidateOk({address:'192.168.1.10',port:0,url:'http://192.168.1.10:5000'})).toBe(false)
})

test('the answer offers one passive TCP host candidate with the issued credentials', () => {
 const sdp=answerSdp('abcdefgh','pw',{address:'192.168.1.10',port:45001,url:'http://192.168.1.10:5000'})
 expect(sdp).toContain('a=ice-lite')
 expect(sdp).toContain('a=ice-ufrag:abcdefgh\r\na=ice-pwd:pw')
 expect(sdp).toContain('a=candidate:1 1 tcp 1518280447 192.168.1.10 45001 typ host tcptype passive')
 expect(sdp.match(/a=candidate/g)).toHaveLength(1)
})

test('the first address to connect wins and every other check stops', async () => {
 FakePeer.made=[];FakePeer.plan={'192.168.1.10':{state:'connected',ms:40},'10.0.0.5':{state:'connected',ms:10}}
 const found=await findLan(probe(),{make})
 expect(found?.url).toBe('http://10.0.0.5:5000')
 expect(found?.https).toBe(false)
 expect(FakePeer.made).toHaveLength(2)
 expect(FakePeer.made.every(p=>p.closed)).toBe(true)
})

test('nothing is offered when no check connects in time', async () => {
 vi.useFakeTimers()
 FakePeer.made=[];FakePeer.plan={'192.168.1.10':{state:'never',ms:0},'10.0.0.5':{state:'failed',ms:5}}
 const pending=findLan(probe(),{make,timeoutMs:5000})
 await vi.advanceTimersByTimeAsync(5001)
 expect(await pending).toBeUndefined()
 expect(FakePeer.made.every(p=>p.closed)).toBe(true)
})

test('a late success after the time limit is ignored', async () => {
 vi.useFakeTimers()
 FakePeer.made=[];FakePeer.plan={'192.168.1.10':{state:'connected',ms:6000}}
 const pending=findLan(probe({candidates:[probe().candidates[0]]}),{make,timeoutMs:5000})
 await vi.advanceTimersByTimeAsync(7000)
 expect(await pending).toBeUndefined()
})

test('anything uncertain offers nothing', async () => {
 FakePeer.plan={'192.168.1.10':{state:'connected',ms:1},'10.0.0.5':{state:'connected',ms:1}}
 expect(await findLan(undefined,{make})).toBeUndefined()
 expect(await findLan(probe({enabled:false}),{make})).toBeUndefined()
 expect(await findLan(probe({ufrag:'short'}),{make})).toBeUndefined()
 expect(await findLan(probe({pwd:'has space in it padded out'}),{make})).toBeUndefined()
 expect(await findLan(probe({candidates:[{address:'8.8.8.8',port:45001,url:'http://8.8.8.8:5000'}]}),{make})).toBeUndefined()
 expect(await findLan(probe(),{make:()=>{throw new Error('no WebRTC')}})).toBeUndefined()
 const stop=new AbortController();stop.abort()
 expect(await findLan(probe(),{make,signal:stop.signal})).toBeUndefined()
})

test('the Cloudflare page turns the option on and puts it back when saving fails', async () => {
 let enabled=false,fail=false
 const f=fixture([],{
  'lanswitch.get':()=>({enabled,port:45001,addresses:[]}),
  'lanswitch.set':(p)=>{if(fail)throw Object.assign(new Error('x'),{code:'lan_switch_unavailable'});enabled=!!p.enabled;return {enabled,port:45001,addresses:['192.168.1.10']}},
 })
 await settle();await f.nav('Cloudflare');await settle()
 const row=f.page('cloudflare').querySelector<HTMLElement>('[data-cf-lan-switch]')!
 expect(row.textContent).toContain(t('lanswitch.row'))
 const toggle=()=>row.querySelector<HTMLElement>('[data-lan-switch-toggle] [role="switch"],[data-lan-switch-toggle][role="switch"],[data-lan-switch-toggle] input')!
 expect(toggle().getAttribute('aria-checked')??String((toggle() as HTMLInputElement).checked)).toBe('false')
 toggle().click();await settle()
 expect(f.call).toHaveBeenCalledWith('lanswitch.set',{enabled:true})
 expect(enabled).toBe(true)
 fail=true
 toggle().click();await settle()
 expect(enabled).toBe(true)
 expect(toggle().getAttribute('aria-checked')??String((toggle() as HTMLInputElement).checked)).toBe('true')
 f.done()
})

test('a page outside Cloudflare never asks for a probe', async () => {
 const f=fixture([])
 await settle()
 expect(f.call.mock.calls.some(([m])=>m==='phone.lanprobe')).toBe(false)
 f.done()
})

test('the notice opens the local address and says to sign in again, and to accept the certificate on https', async () => {
 for(const https of [false,true]){
  const url=https?'https://192.168.1.10:5001':'http://192.168.1.10:5000'
  const root=document.createElement('div');document.body.append(root)
  const a=createApp(LanSwitchNotice,{found:{url,https,ms:120},locale:'en'});a.mount(root);await settle()
  expect(root.querySelector('[data-lan-switch-open]')?.getAttribute('href')).toBe(url)
  expect(root.textContent).toContain(t('lanswitch.signin'))
  expect(root.textContent?.includes(t('lanswitch.certificate'))).toBe(https)
  a.unmount();root.remove()
 }
})
