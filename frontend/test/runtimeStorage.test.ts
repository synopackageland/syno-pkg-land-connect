// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {fixture,connected} from './providerFixture'
import {settle} from './support'
import {translate} from '../src/i18n'

test.each(['tailscale','netbird'])('%s eviction that fits keeps access without a restart prompt',async provider=>{
 const f=fixture([connected(provider)])
 try{
  await settle();await f.nav(provider==='tailscale'?'Tailscale':'NetBird')
  expect(f.page(provider).querySelector('[data-storage-reason]')).toBeNull()
  expect(f.page(provider).querySelector('[data-supervision-retry]')).toBeNull()
  expect(f.page(provider).querySelector('[data-connection-badge]')?.getAttribute('data-connection-state')).toBe('connected')
 }finally{f.done()}
})

test.each(['tailscale','netbird'])('%s remaining overflow names the cause and retries with saved settings',async provider=>{
 const name=provider==='tailscale'?'Tailscale':'NetBird'
 const failed=connected(provider,{state:'failed',code:'tmpfs_budget_exceeded',supervision:{state:'failed',attempts:1,lastExit:{code:-1,reason:'tmpfs_budget_exceeded',permanent:true}}})
 let attempts=0
 const f=fixture([failed],{'connection.connect':()=>{
  if(++attempts===1)throw {code:'unavailable'}
  return {provider:'multiple',state:'connected',connections:[connected(provider)]}
 }})
 try{
  await settle();await f.nav(name)
  const page=f.page(provider)
  expect(page.textContent).toContain(`${name} stopped to protect temporary storage`)
  expect(page.querySelector('[data-storage-reason]')?.textContent).toContain('still exceed the limit after cache cleanup')
  expect(page.textContent).not.toContain('tmpfs_budget_exceeded')
  expect(page.querySelectorAll('[data-supervision-retry]')).toHaveLength(1)
  const retry=()=>page.querySelector<HTMLButtonElement>('[data-supervision-retry]')!
  expect(retry().textContent).toContain(`Restart ${name}`)
  retry().click();await settle()
  expect(retry().disabled).toBe(false)
  expect(page.querySelector('[data-storage-reason]')).not.toBeNull()
  retry().click();await settle()
  const calls=f.call.mock.calls.filter(([method])=>method==='connection.connect')
  expect(calls).toHaveLength(2)
  for(const call of calls)expect(call[1]).toEqual({provider,retry:true})
  expect(f.call.mock.calls.some(([method])=>method==='connection.disconnect')).toBe(false)
  expect(page.querySelector('[data-storage-reason]')).toBeNull()
  expect(page.querySelector('[data-supervision-retry]')).toBeNull()
  expect(page.querySelector('[data-connection-badge]')?.getAttribute('data-connection-state')).toBe('connected')
 }finally{f.done()}
})

test('storage recovery copy is localized in all supported languages',()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  for(const key of ['title','body','restart','unavailable.title','unavailable.body']){
   const full='supervision.storage.'+key,text=translate(locale,full)
   expect(text).not.toBe(full)
   if(locale!=='en')expect(text).not.toBe(translate('en',full))
   expect(text).not.toMatch(/[.。．]$/)
  }
 }
})

test('an unreadable runtime directory is not reported as a size overflow',async()=>{
 const f=fixture([connected('tailscale',{state:'failed',code:'tmpfs_storage_unavailable',supervision:{state:'failed',attempts:1,lastExit:{code:-1,reason:'tmpfs_storage_unavailable',permanent:true}}})])
 try{
  await settle();await f.nav('Tailscale')
  const page=f.page('tailscale')
  expect(page.textContent).toContain('Tailscale could not check temporary storage')
  expect(page.textContent).not.toContain('still exceed')
  expect(page.querySelector('[data-supervision-retry]')).not.toBeNull()
 }finally{f.done()}
})
