// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest'
import {createApp,h,nextTick} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import SupervisionState from '../src/components/SupervisionState.vue'
import {statusWord} from '../src/connect/providerStatus'
import {wayPhase} from '../src/connect/wayPath'
import {validStatus} from '../src/connect/netbirdServer'
import type {ConnectionStatus,SupervisionStatus} from '../src/context'
import {translate} from '../src/i18n'

afterEach(()=>{document.body.innerHTML=''})
test.each(['waiting','backoff','failed'])('shows %s with diagnostics and enables Retry only after failure',async state=>{
 const root=document.createElement('div');document.body.append(root)
 const retry=vi.fn()
 const snapshot:SupervisionStatus={state,attempts:8,nextRetry:'2030-01-01T01:02:03Z',lastExit:{code:3,reason:'child_exit'}}
 const app=createApp({render:()=>h(AppRoot,{},()=>h(SupervisionState,{snapshot,locale:'en',onRetry:retry}))})
 app.mount(root);await nextTick()
 try{
  expect(root.querySelector('[data-supervision-state]')?.getAttribute('data-state')).toBe(state)
  expect(root.textContent).toContain(translate('en','supervision.'+state))
  expect(root.textContent).toContain('child_exit')
  const button=root.querySelector<HTMLButtonElement>('[data-supervision-retry]')
  if(state==='failed'){expect(button).toBeTruthy();button!.click();expect(retry).toHaveBeenCalledOnce()}
  else expect(button).toBeNull()
 }finally{app.unmount()}
})
test('waiting and backoff stay active in provider paths and server status',()=>{
 for(const state of ['waiting','backoff'] as const){
  const status={provider:'netbird',state} as ConnectionStatus
  expect(statusWord(status,false)).toBe('connecting')
  expect(wayPhase(status)).toBe('connecting')
  expect(validStatus({state})).toBe(true)
 }
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const key of ['waiting','backoff','failed','retry','attempts','next','exit']){
  const label=translate(locale,'supervision.'+key);expect(label).not.toBe('supervision.'+key);expect(label).not.toMatch(/[.。]$/)
 }
})
