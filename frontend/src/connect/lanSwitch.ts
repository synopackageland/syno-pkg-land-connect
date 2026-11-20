// Switch to the local network: when a page reached through Cloudflare can
// prove, from this browser, that it reaches the NAS directly on the local
// network, it offers DSM's own LAN address instead.
//
// The proof is a WebRTC connectivity check (ICE over TCP, RFC 6544) against
// each LAN address Connect lists, with one-use credentials Connect issued for
// this page. Only Connect's responder on the NAS holds the password, so a
// check completes only when this browser reaches that NAS at that address.
// Every address is checked in parallel; the first to complete wins and the
// rest stop. Anything uncertain (no WebRTC, no answer within the time limit,
// an error) means no offer: the page stays on Cloudflare. Unlike fetch, an ICE
// check does not make Chrome ask for local network access.

export interface LanCandidate {address:string;port:number;url:string}
export interface LanProbe {enabled:boolean;ufrag?:string;pwd?:string;https?:boolean;candidates:LanCandidate[]}
export interface LanSwitchStatus {enabled:boolean;port:number;addresses:string[]}
export interface LanFound {url:string;https:boolean;ms:number}

export const lanSwitchApi={get:'lanswitch.get',set:'lanswitch.set',probe:'phone.lanprobe'} as const

// The whole check gives up after this long and stays on Cloudflare.
export const LAN_PROBE_TIMEOUT_MS=5000

const ICE=/^[a-z2-7]{8}$/,PWD=/^[a-z2-7]{22,64}$/

// Whether this page is worth checking: an https page on a DNS name. A page
// already on an IP address, a single-label or .local name is on the local
// network or a VPN, and plain http is not how Cloudflare serves DSM.
export function shouldProbe(loc:Pick<Location,'protocol'|'hostname'>):boolean {
 const host=loc.hostname.toLowerCase()
 if(loc.protocol!=='https:'||!host.includes('.')||host.endsWith('.local')||host.endsWith('.'))return false
 if(/^[\d.]+$/.test(host)||host.includes(':')||host.startsWith('['))return false
 return true
}

// A private IPv4 address: 10/8, 172.16/12 or 192.168/16.
export function privateIPv4(a:string):boolean {
 const m=/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(a)
 if(!m)return false
 const [o1,o2,...rest]=m.slice(1).map(Number)
 if([o1,o2,...rest].some(o=>o>255)||m.slice(1).some(o=>o.length>1&&o.startsWith('0')))return false
 return o1===10||(o1===172&&o2>=16&&o2<=31)||(o1===192&&o2===168)
}

// Only an http(s) origin on the candidate's own private IPv4 address is offered.
export function candidateOk(c:LanCandidate):boolean {
 if(!privateIPv4(c.address)||!Number.isInteger(c.port)||c.port<1||c.port>65535)return false
 try{
  const u=new URL(c.url)
  return (u.protocol==='http:'||u.protocol==='https:')&&u.hostname===c.address&&u.pathname==='/'&&!u.search&&!u.hash&&!u.username&&!u.password
 }catch{return false}
}

// The answer an ICE-lite peer would send: one passive TCP host candidate.
// Nothing past ICE is ever used, so the DTLS fingerprint is a fixed placeholder.
export function answerSdp(ufrag:string,pwd:string,c:LanCandidate):string {
 const fp=Array.from({length:32},(_,i)=>((i*7+16)&255).toString(16).padStart(2,'0').toUpperCase()).join(':')
 return ['v=0','o=- 1 2 IN IP4 0.0.0.0','s=-','t=0 0','a=ice-lite','a=group:BUNDLE 0',
  'm=application 9 UDP/DTLS/SCTP webrtc-datachannel','c=IN IP4 0.0.0.0','a=mid:0',
  `a=ice-ufrag:${ufrag}`,`a=ice-pwd:${pwd}`,`a=fingerprint:sha-256 ${fp}`,'a=setup:passive','a=sctp-port:5000',
  `a=candidate:1 1 tcp 1518280447 ${c.address} ${c.port} typ host tcptype passive`,'a=end-of-candidates',''].join('\r\n')
}

type PeerFactory=()=>RTCPeerConnection

// Checks one candidate; resolves true only when ICE connects, false otherwise.
function check(make:PeerFactory,ufrag:string,pwd:string,c:LanCandidate,signal:AbortSignal):Promise<boolean> {
 return new Promise(resolve=>{
  let pc:RTCPeerConnection|undefined
  let done=false
  const finish=(ok:boolean)=>{if(done)return;done=true;signal.removeEventListener('abort',abort);try{pc?.close()}catch{/* already closed */}resolve(ok)}
  const abort=()=>finish(false)
  if(signal.aborted)return finish(false)
  signal.addEventListener('abort',abort)
  try{
   pc=make()
   const peer=pc
   peer.oniceconnectionstatechange=()=>{
    const s=peer.iceConnectionState
    if(s==='connected'||s==='completed')finish(true)
    else if(s==='failed'||s==='closed')finish(false)
   }
   peer.createDataChannel('lan')
   void peer.createOffer()
    .then(offer=>peer.setLocalDescription(offer))
    .then(()=>peer.setRemoteDescription({type:'answer',sdp:answerSdp(ufrag,pwd,c)}))
    .catch(()=>finish(false))
  }catch{finish(false)}
 })
}

// Checks every candidate in parallel and returns the first that connects,
// or undefined when none does within the time limit.
export async function findLan(probe:LanProbe|undefined,opts:{make?:PeerFactory;timeoutMs?:number;signal?:AbortSignal}={}):Promise<LanFound|undefined> {
 if(!probe?.enabled||!ICE.test(probe.ufrag??'')||!PWD.test(probe.pwd??''))return undefined
 const candidates=(probe.candidates??[]).filter(candidateOk).slice(0,8)
 const make=opts.make??(typeof RTCPeerConnection==='function'?()=>new RTCPeerConnection({iceServers:[]}):undefined)
 if(!candidates.length||!make||opts.signal?.aborted)return undefined
 const stop=new AbortController()
 const outer=opts.signal
 const onOuter=()=>stop.abort()
 outer?.addEventListener('abort',onOuter)
 const timer=setTimeout(()=>stop.abort(),opts.timeoutMs??LAN_PROBE_TIMEOUT_MS)
 const start=Date.now()
 try{
  return await new Promise<LanFound|undefined>(resolve=>{
   let left=candidates.length
   for(const c of candidates){
    void check(make,probe.ufrag!,probe.pwd!,c,stop.signal).then(ok=>{
     if(ok&&!stop.signal.aborted){stop.abort();resolve({url:c.url,https:c.url.startsWith('https:'),ms:Date.now()-start})}
     else if(--left===0||stop.signal.aborted)resolve(undefined)
    })
   }
  })
 }finally{clearTimeout(timer);outer?.removeEventListener('abort',onOuter);stop.abort()}
}
