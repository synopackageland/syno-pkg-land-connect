// @vitest-environment jsdom
// Connect takes UI 0.9.5 and SDK 0.10.3 components instead of its own markup.
import {expect,test,vi} from 'vitest'
import {netbirdText} from '../src/netbirdMessages'
import {settle} from './support'
import {fixture,t} from './providerFixture'

test('a NetBird error shows its message without the raw code',async()=>{
 const f=fixture([{provider:'netbird',state:'failed',simulation:false,shareUrl:'',code:'control_unreachable'} as never]);await settle()
 const callout=f.page('netbird').querySelector<HTMLElement>('[data-nb-error]')!
 expect(callout.querySelector('[data-ui-callout-code]')).toBeNull()
 expect(callout.textContent).not.toContain('control_unreachable')
 expect(callout.textContent).toContain(netbirdText('en','control_unreachable'))
 f.done()
})

test('the method comparison on the overview is a table with a row per catalog option',async()=>{
 const f=fixture();await settle()
 const notes=f.page('status').querySelector<HTMLElement>('[data-choice-notes]')!
 const table=notes.querySelector<HTMLElement>('[data-advisor-table]')!
 for(const id of ['cloudflare-quick','cloudflare-domain','tailscale','netbird','quickconnect','ipv6'])expect(table.textContent).toContain(t(`advisor.option.${id}`))
 expect(notes.querySelector('[role="list"] > [role="listitem"]')).toBeNull()
 expect(notes.querySelector('[style*="auto-fit"]')).toBeNull()
 f.done()
})

test('Settings sits at the bottom of the sidebar, in a group of its own',async()=>{
 const f=fixture();await settle()
 const items=[...f.root.querySelectorAll<HTMLElement>('[data-nav-item]')]
 const group=(label:string)=>items.find(n=>n.textContent?.trim()===label)!.closest('section')
 expect(items.at(-1)?.textContent?.trim()).toBe('Settings')
 expect(group('Settings')).toBeTruthy()
 expect(group('Settings')).not.toBe(group('IPv6 direct'))
 f.done()
})

test('the NetBird and Tailscale sign-in countdowns read m:ss',async()=>{
 vi.useFakeTimers({toFake:['Date']});vi.setSystemTime(new Date('2026-10-14T00:00:00Z'))
 const deadline='2026-10-14T00:01:05Z'
 const f=fixture([
  {provider:'netbird',state:'needs-login',phase:'awaiting-login',simulation:false,shareUrl:'',deadline} as never,
  {provider:'tailscale',state:'needs-login',simulation:false,shareUrl:'',loginDeadline:deadline} as never]);await settle()
 expect(f.page('netbird').querySelector('[data-nb-countdown]')?.textContent).toContain('1:05')
 expect(f.page('tailscale').querySelector('[data-login-countdown]')?.textContent).toContain('1:05')
 f.done();vi.useRealTimers()
})

