// @vitest-environment jsdom
import {netbirdText} from '../src/netbirdMessages'
import {expect,test,vi} from 'vitest'
import {fixture,connected,t} from './providerFixture'
import {settle} from './support'
import {translate} from '../src/i18n'

test('a failed health check says check failed without suggesting a rollback happened',async()=>{
 const f=fixture([connected('netbird',{apply:{phase:'check-failed',code:'local_check_failed'}})])
 await settle();await f.nav('NetBird')
 const callout=f.page('netbird').querySelector('[data-apply-status]')!
 expect(callout.textContent).toContain('The NAS-side connection check failed')
 expect(callout.textContent).not.toContain(t('apply.phase.recovery-required'))
 expect(callout.textContent).not.toContain('local_check_failed')
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('health check failure has a translated title without a closing period in %s',locale=>{
 const key='apply.phase.check-failed',title=translate(locale,key)
 expect(title).not.toBe(key)
 if(locale!=='en')expect(title).not.toBe(translate('en',key))
 expect(title).not.toMatch(/[.。．]$/)
})

test('failed apply reports verified rollback beside the preserved DSM address',async()=>{
 const f=fixture([connected('cloudflare',{mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}],apply:{phase:'rolled-back',code:'cloudflare_permission_denied',addedPorts:[6690]}})])
 await settle();await f.nav('Cloudflare')
 expect(f.root.querySelector('[data-apply-status]')?.textContent).toContain(t('apply.phase.rolled-back'))
 expect(f.page('cloudflare').querySelector<HTMLInputElement>('[data-url-card] input')?.value).toBe('https://nas.example.com')
 expect(f.page('cloudflare').querySelector('[data-connection-badge]')?.getAttribute('data-connection-state')).toBe('connected')
 f.done()
})

test('a change committed while the window is open is a toast, not a notice that stays',async()=>{
 vi.useFakeTimers()
 const base={mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}]} as const
 const f=fixture([connected('cloudflare',{...base,apply:{phase:'committed'}})])
 await settle()
 // Committed before the window opened: nothing to say.
 expect(document.body.textContent).not.toContain(t('apply.phase.committed'))
 expect(f.root.querySelector('[data-apply-status]')).toBeNull()
 f.setStatus([connected('cloudflare',{...base,apply:{phase:'committed',addedPorts:[6690]}})])
 await vi.advanceTimersByTimeAsync(5000);await settle()
 expect(document.body.textContent).toContain(t('apply.phase.committed'))
 expect(document.body.textContent).toContain(`${t('apply.added')} 6690`)
 expect(f.root.querySelector('[data-apply-status]')).toBeNull()
 f.done();vi.useRealTimers()
})

test('NetBird recovery offers one safe retry and clears the warning when verified',async()=>{
 const status=connected('netbird',{managementUrl:'https://api.netbird.io:443',networkReady:true,apply:{phase:'recovery-required',code:'login_url_timeout'}})
 const f=fixture([status],{'connection.connect':()=>({provider:'multiple',state:'connected',connections:[connected('netbird',{networkReady:true,apply:{phase:'committed'}})]})})
 await settle();await f.nav('NetBird')
 const notice=f.page('netbird').querySelector('[data-apply-status]')!
 expect(notice.textContent).toContain(netbirdText('en','login_url_timeout'))
 const retry=notice.querySelector<HTMLButtonElement>('[data-apply-retry]')!
 expect(retry).not.toBeNull()
 retry.click();await settle()
 expect(f.call).toHaveBeenCalledWith('connection.connect',{provider:'netbird',managementUrl:'https://api.netbird.io:443'})
 expect(f.call.mock.calls.some(([method])=>method==='connection.disconnect')).toBe(false)
 expect(f.page('netbird').querySelector('[data-apply-status]')).toBeNull()
 f.done()
})

test.each(['applying','repairing','recovering','rolling-back'])('NetBird %s does not offer a competing retry',async phase=>{
 const f=fixture([connected('netbird',{apply:{phase}})])
 await settle();await f.nav('NetBird')
 expect(f.page('netbird').querySelector('[data-apply-retry]')).toBeNull()
 f.done()
})

test('automatic tunnel recreation shows a translated passing toast',async()=>{
 vi.useFakeTimers()
 const f=fixture([connected('cloudflare',{mode:'domain',hostname:'nas.example.com'})])
 await settle()
 f.setStatus([connected('cloudflare',{mode:'domain',hostname:'nas.example.com',recoveryNotice:'cloudflare_tunnel_recreated'})])
 await vi.advanceTimersByTimeAsync(5000);await settle()
 const notice=[...document.querySelectorAll('.ui-toast')].find(n=>n.textContent?.includes(t('cloudflare.tunnelRecreated')))
 expect(notice).toBeDefined()
 expect(notice?.closest('[data-ui-app]')).not.toBeNull()
 f.done();vi.useRealTimers()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('tunnel recreation notice is localized without a closing period in %s',locale=>{
 const key='cloudflare.tunnelRecreated',message=translate(locale,key)
 expect(message).not.toBe(key)
 if(locale!=='en')expect(message).not.toBe(translate('en',key))
 expect(message).not.toMatch(/[.。．]$/)
})
