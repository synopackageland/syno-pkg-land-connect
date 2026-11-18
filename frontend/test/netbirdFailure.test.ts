// @vitest-environment jsdom
// A NetBird failure shows one Callout.
import {expect,test} from 'vitest'
import {netbirdText} from '../src/netbirdMessages'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture} from './providerFixture'

test('a NetBird failure shows one Callout: its own message and code, not the generic one too',async()=>{
 const f=fixture([{provider:'netbird',state:'failed',simulation:false,shareUrl:'',code:'login_wait_timeout'} as never]);await settle()
 const page=f.page('netbird')
 expect(page.querySelector('[data-nb-error]')?.textContent).toContain(netbirdText('en','login_wait_timeout'))
 expect(page.querySelector('[data-connection-reason]')).toBeNull()
 expect(page.textContent).not.toContain(translate('en','provider.failed'))
 f.done()
})

test('a NetBird failure NetBird has no message for keeps the shared message, without a raw code as text',async()=>{
 const f=fixture([{provider:'netbird',state:'failed',simulation:false,shareUrl:'',code:'binary_required'} as never]);await settle()
 const page=f.page('netbird')
 expect(page.querySelector('[data-connection-reason]')?.textContent).toBe(translate('en','provider.binaryRequired'))
 expect(page.querySelector('[data-nb-error]')).toBeNull()
 f.done()
})