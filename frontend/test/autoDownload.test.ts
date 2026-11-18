// @vitest-environment jsdom
// Connecting downloads a missing program in place first.
import {afterEach,expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {shellCopy} from '../src/shell/i18n'
import {settle} from './support'
import {fixture,t,connected} from './providerFixture'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks()})

test('pages no longer send the user to Settings to download a program',async()=>{
 const f=fixture();await settle()
 for(const name of ['tailscale','netbird'])expect([...f.page(name).querySelectorAll('button')].map(b=>b.textContent).join()).not.toMatch(/download/i)
 f.done()
})

test('a missing program is downloaded in place and the connection continues',async()=>{
 vi.useFakeTimers()
 let connects=0,reads=0
 const f=fixture([],{
  'connection.connect':()=>{if(++connects===1)throw {code:'binary_required'};return connected('netbird',{netbirdIP:'100.64.0.9'})},
  'binaries.update':p=>({product:p.product,current:'',phase:'downloading',busy:true}),
  'binaries.status':p=>++reads<2?{product:p.product,current:'',phase:'verifying',busy:true}:{product:p.product,current:'0.80.0',phase:'idle',busy:false},
 });await settle();await f.nav('NetBird')
 const page=f.page('netbird')
 page.querySelector<HTMLButtonElement>('[data-connect-advanced] button[aria-expanded]')!.click();await settle()
 const input=(selector:string,value:string)=>{const el=page.querySelector<HTMLInputElement>(selector)!;el.value=value;el.dispatchEvent(new Event('input',{bubbles:true}))}
 input('input[type=password]','SETUP-KEY');await settle()
 for(const box of page.querySelectorAll<HTMLElement>('[data-nb-exposure]'))box.click()
 await settle()
 page.querySelector<HTMLButtonElement>('[data-nb-connect]')!.click();await settle()
 expect(f.call).toHaveBeenCalledWith('binaries.update',{product:'netbird'})
 expect(page.querySelector('[data-connect-section] [data-download-progress]')?.textContent).toContain(translate('en','connect.downloading'))
 await vi.advanceTimersByTimeAsync(1500);await settle()
 await vi.advanceTimersByTimeAsync(1500);await settle()
 expect(connects).toBe(2)
 expect(f.call.mock.calls.filter(c=>c[0]==='connection.connect')[1]![1]).toMatchObject({provider:'netbird',token:'SETUP-KEY'})
 expect(page.querySelector('[data-download-progress]')).toBeNull()
 expect(f.root.textContent).not.toContain(translate('en','provider.binaryRequired'))
 f.done()
})

test('a failed download stops before connecting and says so',async()=>{
 let connects=0
 const f=fixture([],{
  'connection.connect':()=>{connects++;throw {code:'binary_required'}},
  'binaries.update':()=>{throw {code:'binary_unavailable'}},
 });await settle();await f.nav('NetBird')
 const page=f.page('netbird')
 page.querySelector<HTMLButtonElement>('[data-connect-advanced] button[aria-expanded]')!.click();await settle()
 const key=page.querySelector<HTMLInputElement>('input[type=password]')!;key.value='SETUP-KEY';key.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 for(const box of page.querySelectorAll<HTMLElement>('[data-nb-exposure]'))box.click()
 await settle()
 page.querySelector<HTMLButtonElement>('[data-nb-connect]')!.click();await settle()
 expect(connects).toBe(1)
 expect(f.root.textContent).toContain(t('connect.downloadFailed'))
 expect(page.querySelector('[data-download-progress]')).toBeNull()
 f.done()
})

test('the IPv6 guide neither connects nor downloads a program',async()=>{
 const f=fixture();await settle();await f.nav(shellCopy('en').ipv6)
 const page=f.page('ipv6')
 expect(page.querySelector('[data-ipv6-connect]')).toBeNull()
 expect(f.call.mock.calls.some(([m])=>m==='connection.connect'||m==='binaries.update')).toBe(false)
 f.done()
})

test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','it','es'])('the download step is translated in %s',locale=>{
 for(const key of ['connect.downloading','connect.downloadFailed'])
  expect(translate(locale,key),`${locale} ${key}`).not.toBe(locale==='en'?key:translate('en',key))
})

test('a download GitHub rate-limits names the reset time and opens Settings at the GitHub token',async()=>{
 const f=fixture([],{
  'connection.connect':()=>{throw {code:'binary_required'}},
  'binaries.update':p=>({product:p.product,current:'',phase:'failed',busy:false,errorCode:'github_rate_limited',retryAt:'2026-10-14T17:46:40Z'}),
  'github.token.status':()=>({saved:false}),
 });await settle();await f.nav('NetBird')
 const page=f.page('netbird')
 page.querySelector<HTMLButtonElement>('[data-connect-advanced] button[aria-expanded]')!.click();await settle()
 const key=page.querySelector<HTMLInputElement>('input[type=password]')!;key.value='SETUP-KEY';key.dispatchEvent(new Event('input',{bubbles:true}));await settle()
 for(const box of page.querySelectorAll<HTMLElement>('[data-nb-exposure]'))box.click()
 await settle()
 page.querySelector<HTMLButtonElement>('[data-nb-connect]')!.click();await settle()
 const time=new Intl.DateTimeFormat('en',{dateStyle:'medium',timeStyle:'short'}).format(new Date('2026-10-14T17:46:40Z'))
 expect(f.root.textContent).toContain(t('binaries.rateLimited').replace('{time}',time))
 const action=f.root.querySelector<HTMLButtonElement>('[data-github-token-action]')!
 expect(action.textContent?.trim()).toBe(t('github.addToken'))
 action.click();await settle()
 expect(f.root.querySelector<HTMLElement>('[data-page=settings]')!.style.display).not.toBe('none')
 expect(document.activeElement?.closest('[data-github-token]')).toBeTruthy()
 f.done()
})
