// @vitest-environment jsdom
// Slow-NAS acceptance: no repeated headings, the same controls on every waiting row.
import {expect,test} from 'vitest'
import {fixture,connected,t} from './providerFixture'
import {settle} from './support'

const count=(text:string,part:string)=>text.split(part).length-1

test('a NetBird sign-in shows its heading once, with the wait and the code',async()=>{
 const f=fixture([{provider:'netbird',state:'needs-login',phase:'awaiting-login',simulation:false,shareUrl:'',userCode:'ABCD-EFGH',deadline:new Date(Date.now()+60000).toISOString()} as never])
 await settle();await f.nav('NetBird')
 const page=f.page('netbird')
 expect(count(page.textContent??'',t('attention.signIn'))).toBe(1)
 expect(page.querySelector('[data-nb-awaiting]')).toBeTruthy()
 expect(page.querySelector('[data-nb-awaiting] [data-nb-code]')).toBeNull()
 expect(page.querySelector('[data-nb-code]')).toBeTruthy()
 f.done()
})

test('an apply callout whose code says the same as its title says it once',async()=>{
 const f=fixture([connected('netbird',{apply:{phase:'reauth-required',code:'apply_reauth_required'}})])
 await settle();await f.nav('NetBird')
 const callout=f.page('netbird').querySelector('[data-apply-status]')!
 expect(count(callout.textContent??'',t('apply.phase.reauth-required'))).toBe(1)
 expect(callout.textContent).not.toContain('apply_reauth_required')
 f.done()
})
