// @vitest-environment jsdom
// A way the user turned off on purpose is off, not broken. The shared state
// words and every picture say so for each provider, and a dependency problem
// can only colour a way that is meant to be up.
import {afterEach,expect,test,vi} from 'vitest'
import type {ConnectionStatus} from '../src/context'
import {dependencyPathLinks,type DependencyReport} from '../src/connect/dependency'
import {pathLinks,statusWord,STATUS_TAG} from '../src/connect/providerStatus'
import {laneFor} from '../src/connect/topology'
import {OFF_LINK_LABEL,phaseLinkLabels,phaseLinks,wayPhase} from '../src/connect/wayPath'
import {fixture,t,connected,type Handler} from './providerFixture'
import {settle} from './support'
afterEach(()=>{vi.restoreAllMocks()})

const off=(provider:string,extra:Partial<ConnectionStatus>={})=>({provider,state:'disconnected',simulation:false,shareUrl:'',...extra}) as ConnectionStatus
const impact=(code:string)=>code==='cloudflare_reconnect_required'?'maintenance':'connection'
const problem=(code='cloudflare_not_connected',hop:'tunnel'|'bridge'='tunnel'):DependencyReport=>({enabled:true,problem:{code,piece:hop,hop,action:'connect_cloudflare',page:'cloudflare',impact:impact(code)}})

// Same words on the Overview, a provider page and the App URL page: Off is neutral.
test.each(['cloudflare','tailscale','netbird','ipv6'])('%s turned off reads as a neutral off, not a failure',provider=>{
 const status=off(provider)
 expect(statusWord(status,false)).toBe('off')
 expect(STATUS_TAG[statusWord(status,false)].tone).toBe('neutral')
 expect(wayPhase(status)).toBe('off')
 expect(phaseLinks('cloudflare-domain','off')).toEqual(['idle','idle'])
 expect(phaseLinkLabels('off')).toEqual(['',OFF_LINK_LABEL])
 // The Overview has no lane for a stopped way, and a provider page draws it idle.
 expect(laneFor(provider,status,undefined,false)).toBeUndefined()
 expect(pathLinks(provider,status,undefined,false)).toEqual(['idle','idle'])
})

test('a failure keeps its own word and colour, and no off label',()=>{
 const status=off('cloudflare',{state:'failed',code:'cloudflare_login_failed'})
 expect(statusWord(status,false)).toBe('error')
 expect(STATUS_TAG.error.tone).toBe('danger')
 expect(wayPhase(status)).toBe('error')
 expect(phaseLinks('cloudflare-domain','error')).toEqual(['idle','error'])
 expect(phaseLinkLabels('error')).toBeUndefined()
})

test('a dependency problem marks the tunnel hop only while that way should be up',()=>{
 expect(dependencyPathLinks(problem(),true)?.map(l=>l.state)).toEqual(['unknown','error'])
 expect(dependencyPathLinks(problem(),true)?.[1].label).toBe('dependency.code.cloudflare_not_connected')
 // Turned off on purpose: the way is off, whatever another way needs from it.
 expect(dependencyPathLinks(problem(),false)).toBeUndefined()
 // A dependency nothing uses is not this way's problem.
 expect(dependencyPathLinks({...problem(),enabled:false},true)).toBeUndefined()
 // A Bridge or Signal hop belongs to the server's own picture.
 expect(dependencyPathLinks(problem('workers_route_conflict','bridge'),true)).toBeUndefined()
 expect(dependencyPathLinks({enabled:true},true)).toBeUndefined()
})

// The page the report showed: off on purpose, with a stale dependency read.
const stale:Handler=()=>({state:'off',dependencies:{enabled:false,problem:{code:'cloudflare_not_connected',piece:'tunnel',hop:'tunnel',action:'connect_cloudflare',page:'cloudflare',impact:'connection'}}})
async function open(connections:ConnectionStatus[],handlers:Record<string,Handler>={}){
 const f=fixture(connections,handlers);await settle();await f.nav('Cloudflare')
 return {...f,cf:f.page('cloudflare')}
}
const path=(cf:HTMLElement)=>cf.querySelector<HTMLElement>('[data-cf-path="domain"]')!

test('a provider page draws a stopped way as turned off',async()=>{
 const f=fixture([]);await settle();await f.nav('Tailscale')
 const picture=f.page('tailscale').querySelector<HTMLElement>('[data-provider-path]')!
 expect(picture.textContent).toContain(t(OFF_LINK_LABEL))
 expect(picture.querySelector('.ui-topology-link[data-link-state="error"]')).toBeNull()
 f.done()
})

test('a Cloudflare the user turned off names no failure in its path',async()=>{
 const f=await open([off('cloudflare',{mode:'domain',suggestedName:'nas-a1b2c3d4e5f6'})],{'netbirdserver.status':stale})
 const picture=path(f.cf)
 expect(picture.querySelector('.ui-topology-link[data-link-state="error"]')).toBeNull()
 expect(picture.textContent).toContain(t(OFF_LINK_LABEL))
 expect(picture.textContent).not.toContain(t('dependency.code.cloudflare_not_connected'))
 f.done()
})

test('a running Cloudflare whose tunnel hop really fails still shows it',async()=>{
 const f=await open([connected('cloudflare',{mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]})],{'netbirdserver.status':()=>({state:'running',dependencies:problem('netbird_server_unreachable')})})
 const picture=path(f.cf)
 expect(picture.querySelector('.ui-topology-link[data-link-state="error"]')).toBeTruthy()
 expect(picture.textContent).toContain(t('dependency.code.netbird_server_unreachable'))
 expect(picture.textContent).not.toContain(t(OFF_LINK_LABEL))
 f.done()
})
