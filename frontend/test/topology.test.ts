// Each row of the Overview's status → lanes table (plan §3.2): the left link
// is the reach from outside, the right link is the NAS side.
import {expect,test} from 'vitest'
import {overviewLanes,laneFor} from '../src/connect/topology'
import type {CheckResult} from '../src/connect/checks'
import type {ConnectionStatus} from '../src/context'

const status=(provider:string,state:ConnectionStatus['state'],extra:Partial<ConnectionStatus>={})=>({provider,state,simulation:false,shareUrl:'',...extra}) as ConnectionStatus
const check=(provider:string,state:CheckResult['state'],externalVerified=false):CheckResult=>({provider,state,externalVerified})
const links=(lane:ReturnType<typeof laneFor>)=>lane?.links.map(l=>[l.state,l.label,l.icon??''].join(' '))

test.each([
 ['cloudflare connected, verified from outside','cloudflare','connected',check('cloudflare','ok',true),['active overview.reach.external ','active overview.nas.running ']],
 ['cloudflare connected, not checked yet','cloudflare','connected',undefined,['unknown overview.reach.notChecked ','active overview.nas.running ']],
 ['cloudflare connected, check still running','cloudflare','connected',check('cloudflare','checking'),['unknown overview.reach.notChecked ','active overview.nas.running ']],
 ['cloudflare settling','cloudflare','connected',check('cloudflare','starting'),['pending overview.reach.settingUp ','active overview.nas.running ']],
 ['tailscale settling','tailscale','connected',check('tailscale','starting'),['pending overview.reach.settingUp ','active overview.nas.running ']],
 ['netbird settling','netbird','connected',check('netbird','starting'),['pending overview.reach.settingUp ','active overview.nas.running ']],
 ['ipv6 settling','ipv6','connected',check('ipv6','starting'),['pending overview.reach.settingUp ','active overview.nas.running ']],
 ['cloudflare connected, outside check failed','cloudflare','connected',check('cloudflare','failed'),['error overview.reach.unreachable ','active overview.nas.running ']],
 ['tailscale connected, NAS side only','tailscale','connected',check('tailscale','ok'),['unknown overview.reach.private ','active overview.nas.running ']],
 ['netbird connected, not checked yet','netbird','connected',undefined,['unknown overview.reach.private ','active overview.nas.running ']],
 ['netbird connected, NAS side check failed','netbird','connected',check('netbird','failed'),['unknown overview.reach.private ','error overview.nas.checkFailed ']],
 ['ipv6 ready, NAS side only','ipv6','connected',check('ipv6','ok'),['unknown overview.reach.unverified ','active overview.nas.running ']],
 ['connecting','tailscale','connecting',undefined,['idle overview.reach.waiting ','pending ux.state.connecting ']],
 ['stopping','cloudflare','stopping',undefined,['idle overview.reach.waiting ','pending ux.state.stopping ']],
 ['disconnecting','netbird','disconnecting',undefined,['idle overview.reach.waiting ','pending ux.state.disconnecting ']],
 ['needs sign-in','tailscale','needs-login',undefined,['idle overview.reach.waiting ','attention ux.state.needs-login key']],
 ['needs machine approval','tailscale','needs-machine-auth',undefined,['idle overview.reach.waiting ','attention overview.nas.approval ']],
 ['failed','netbird','failed',undefined,['idle overview.reach.waiting ','error ux.state.failed ']],
 ['the daemon reports unknown','tailscale','unknown',undefined,['unknown setup.unknown ','unknown setup.unknown ']],
] as const)('%s',(_,provider,state,result,expected)=>{
 const extra=state==='disconnected'?{hostname:'nas.example.com'}:{}
 expect(links(laneFor(provider,status(provider,state,extra),result,false))).toEqual(expected)
})

test('a check of a connection that is no longer connected is ignored',()=>{
 expect(links(laneFor('cloudflare',status('cloudflare','connecting'),check('cloudflare','failed'),false))).toEqual(['idle overview.reach.waiting ','pending ux.state.connecting '])
})

test('without an answer from the daemon every shown lane is unknown and lists no addresses',()=>{
 const lane=laneFor('cloudflare',status('cloudflare','connected',{shareUrl:'https://nas.example.com'}),check('cloudflare','ok',true),true)
 expect(links(lane)).toEqual(['unknown setup.unknown ','unknown setup.unknown '])
 expect(lane?.addresses).toEqual([])
})

test('only methods that are set up or running get a lane, in the sidebar order',()=>{
 const lanes=overviewLanes({
  ipv6:status('ipv6','connected'),
  netbird:status('netbird','disconnected',{managementUrl:'https://api.netbird.io:443'}),
  tailscale:status('tailscale','disconnected',{loggedIn:true}),
  cloudflare:status('cloudflare','needs-login'),
 },{},false)
 expect(lanes.map(l=>l.provider)).toEqual(['cloudflare','ipv6'])
})

test('a turned-off method without a saved address or sign-in has no lane',()=>{
 expect(laneFor('cloudflare',status('cloudflare','disconnected'),undefined,false)).toBeUndefined()
 expect(laneFor('cloudflare',undefined,undefined,false)).toBeUndefined()
 expect(overviewLanes({},{},true)).toEqual([])
})

test('every lane lists its addresses',()=>{
 const lane=laneFor('tailscale',status('tailscale','connected',{shareUrl:'https://nas.tail1.ts.net',endpoints:[{url:'https://nas.tail1.ts.net',public:false,localPort:5000},{url:'https://nas.tail1.ts.net:8443',public:false,localPort:8443}]}),undefined,false)
 expect(lane?.addresses).toEqual(['https://nas.tail1.ts.net','https://nas.tail1.ts.net:8443'])
})

test('a connected lane carries the online device count only when the daemon reported one',()=>{
 expect(laneFor('tailscale',status('tailscale','connected',{peersOnline:2}),undefined,false)?.peersOnline).toBe(2)
 expect(laneFor('netbird',status('netbird','connected',{peersOnline:0}),undefined,false)?.peersOnline).toBe(0)
 expect(laneFor('netbird',status('netbird','connected'),undefined,false)?.peersOnline).toBeUndefined()
 expect(laneFor('tailscale',status('tailscale','connecting',{peersOnline:2}),undefined,false)?.peersOnline).toBeUndefined()
 expect(laneFor('tailscale',status('tailscale','connected',{peersOnline:2}),undefined,true)?.peersOnline).toBeUndefined()
})

test('turned-off ways with saved addresses or identities have no Overview row',()=>{
 for(const provider of ['cloudflare','tailscale','netbird'])expect(laneFor(provider,status(provider,'disconnected',{hostname:'saved.example.com',loggedIn:true}),undefined,false)).toBeUndefined()
})
