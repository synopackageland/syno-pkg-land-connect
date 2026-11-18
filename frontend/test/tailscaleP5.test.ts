// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {trustedLoginUrl} from '../src/connect/login'
import {fixture,connected} from './providerFixture'
import {settle} from './support'
afterEach(()=>vi.restoreAllMocks())
test('Tailscale accepts only the complete official login URL',()=>{
 for(const suffix of ['?x=1','#x','/extra','\n'])expect(trustedLoginUrl('tailscale','https://login.tailscale.com/a/token'+suffix)).toBe('')
 expect(trustedLoginUrl('tailscale','https://login.tailscale.com:443/a/token')).toBe('')
 expect(trustedLoginUrl('tailscale','https://login.tailscale.com/a/%74oken')).toBe('')
})
test('private one-click start has no public consent or credential fields',async()=>{
 const f=fixture();await settle();await f.nav('Tailscale')
 const page=f.page('tailscale'),start=page.querySelector('[data-connect-form="tailscale"]')!
 expect(start.querySelector('[data-exposure-confirm]')).toBeNull()
 // The auth key and login server are folded under Advanced settings.
 expect([...start.querySelectorAll('input')].filter(i=>!i.closest('[data-connect-advanced]'))).toHaveLength(0)
 expect(page.querySelector<HTMLButtonElement>('[data-ts-connect]')!.disabled).toBe(false)
 f.done()
})
test('all observed service URLs remain visible even with a primary share URL',async()=>{
 const state={...connected('tailscale'),shareUrl:'https://nas.fixture.ts.net',endpoints:[{url:'https://nas.fixture.ts.net',public:false,localPort:5000},{url:'https://nas.fixture.ts.net:8443',public:false,localPort:5001}]}
 const f=fixture([state]);await settle();await f.nav('Tailscale')
 expect(f.page('tailscale').querySelectorAll('[data-url-card]')).toHaveLength(2)
 f.done()
})
test('Headscale login stays within the explicitly configured origin',()=>{
 const config={loginServer:'https://headscale.example.com:443'}
 expect(trustedLoginUrl('tailscale','https://headscale.example.com:443/register/node',config)).toBe('https://headscale.example.com:443/register/node')
 for(const raw of ['https://headscale.example.com/register/node','https://id.example.com/device','https://headscale.example.com:443/register/%0bnode'])expect(trustedLoginUrl('tailscale',raw,config)).toBe('')
})
test('waiting for machine approval displays the App deadline and management action',async()=>{
 const f=fixture([{provider:'tailscale',state:'needs-machine-auth',simulation:false,shareUrl:'',loginDeadline:new Date(Date.now()+600000).toISOString()}]);await settle();await f.nav('Tailscale')
 expect(f.page('tailscale').querySelector('[data-machine-approval]')?.textContent).toContain('approve this NAS')
 expect(f.page('tailscale').querySelector('[data-login-countdown]')?.textContent).toContain('not the sign-in URL expiry')
 expect(f.page('tailscale').querySelector('[data-login-action]')).toBeNull();f.done()
})
test('missing control URL shows a network problem without a sign-in action',async()=>{
 const f=fixture([{provider:'tailscale',state:'connecting',code:'control_unreachable',simulation:false,shareUrl:''}]);await settle();await f.nav('Tailscale')
 expect(f.page('tailscale').querySelector('[data-control-unreachable]')?.textContent).toContain('cannot reach the control server')
 expect(f.page('tailscale').querySelector('[data-login-action]')).toBeNull();f.done()
})
test('blank login tab survives connecting until the trusted challenge arrives',async()=>{
 vi.useFakeTimers()
 const tab={opener:{} as unknown,location:{replace:vi.fn()},close:vi.fn()}
 vi.spyOn(window,'open').mockReturnValue(tab as unknown as Window)
 const f=fixture([],{'connection.connect':()=>({provider:'tailscale',state:'connecting',simulation:false,shareUrl:''})});await settle();await f.nav('Tailscale')
 f.page('tailscale').querySelector<HTMLButtonElement>('[data-ts-connect]')!.click();await settle()
 expect(tab.close).not.toHaveBeenCalled()
 f.setStatus([{provider:'tailscale',state:'needs-login',simulation:false,shareUrl:'',loginUrl:'https://login.tailscale.com/a/token'}])
 await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(tab.location.replace).toHaveBeenCalledWith('https://login.tailscale.com/a/token');f.done();vi.useRealTimers()
})

test('approval timeout keeps device approval visible without claiming URL expiry',async()=>{
 const f=fixture([{provider:'tailscale',state:'failed',code:'machine_approval_wait_timeout',simulation:false,shareUrl:''}]);await settle();await f.nav('Tailscale')
 expect(f.page('tailscale').querySelector('[data-machine-approval]')?.textContent).toContain('approve this NAS')
 expect(f.page('tailscale').querySelector('[data-connection-reason]')?.textContent).toContain('Waiting timed out')
 expect(f.page('tailscale').querySelector('[data-login-action]')).toBeNull();f.done()
})
