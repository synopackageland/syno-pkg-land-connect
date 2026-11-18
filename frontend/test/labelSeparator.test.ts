// @vitest-environment jsdom
// A label and its value never get a space after a full-width colon.
import {expect,test,vi} from 'vitest'
import {mount} from '../src/main'
import {createStandaloneContext} from '@synopackageland/sdk'
import {translate} from '../src/i18n'
import {settle} from './support'

const locales=['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']

test.each(locales)('the %s NetBird countdown joins its label and time with one separator',async locale=>{
 vi.useFakeTimers({toFake:['Date']});vi.setSystemTime(new Date('2026-10-14T00:00:00Z'))
 const root=document.createElement('div');document.body.append(root)
 const netbird={provider:'netbird',state:'needs-login',phase:'awaiting-login',simulation:false,shareUrl:'',deadline:'2026-10-14T00:05:51Z'}
 const call=vi.fn(async(method:string)=>method==='app.status'?{admin:true}:method==='ports.list'?{entries:[],revision:1,detection:'ready'}:{provider:'multiple',state:'disconnected',connections:[netbird]})
 const app=mount(root,createStandaloneContext({call,locale}));await settle()
 const text=root.querySelector('[data-page="netbird"] [data-nb-countdown]')!.textContent!
 expect(text).toContain('5:51')
 expect(text).not.toMatch(/： |：\s|:\s{2}|\s:\s/)
 expect(text).toMatch(locale.startsWith('zh')||locale==='ja'?/：5:51$/:/: 5:51$/)
 app.unmount();root.remove();vi.useRealTimers()
})

test.each(locales)('the %s QR code label joins its text and the address without a space after a full-width colon',locale=>{
 const label=translate(locale,'connect.url.qrLabel').replace('{url}','https://nas.example.test')
 expect(label).toContain('https://nas.example.test')
 expect(label).not.toMatch(/： /)
})