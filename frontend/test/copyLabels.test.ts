// @vitest-environment jsdom
// Copy buttons say what they copy.
import {expect,test,vi} from 'vitest'
import {translate} from '../src/i18n'
import {settle} from './support'
import {fixture,t} from './providerFixture'

const locales=['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']

test('the NetBird user code and IP copy buttons say what they copy, not "Copy link"',async()=>{
 const f=fixture([{provider:'netbird',state:'needs-login',phase:'awaiting-login',simulation:false,shareUrl:'',userCode:'ABCD-EFGH',deadline:new Date(Date.now()+60000).toISOString()} as never]);await settle()
 const labels=(scope:ParentNode)=>[...scope.querySelectorAll('button')].map(b=>b.getAttribute('aria-label'))
 expect(labels(f.page('netbird'))).toContain(t('ux.copyCode'))
 expect(labels(f.page('netbird'))).not.toContain(t('ux.copy'))
 f.done()
 const g=fixture([{provider:'netbird',state:'connected',simulation:false,shareUrl:'',netbirdIP:'100.64.0.7'} as never]);await settle()
 expect(labels(g.page('netbird'))).toContain(t('ux.copyIp'))
 expect(labels(g.page('netbird'))).not.toContain(t('ux.copy'))
 g.done()
})

test.each(locales)('the %s copy labels for a code and an IP are their own words',locale=>{
 for(const key of ['ux.copyCode','ux.copyIp']){
  expect(translate(locale,key)).not.toBe(key)
  expect(translate(locale,key)).not.toBe(translate(locale,'ux.copy'))
 }
})

// An http DSM window has no Clipboard API; copying the IP still says Copied.
test('copying the NetBird IP over http falls back and says Copied',async()=>{
 const f=fixture([{provider:'netbird',state:'connected',simulation:false,shareUrl:'',netbirdIP:'100.64.0.7'} as never]);await settle()
 const clipboard=Object.getOwnPropertyDescriptor(navigator,'clipboard'),execCommand=Object.getOwnPropertyDescriptor(document,'execCommand')
 Object.defineProperty(navigator,'clipboard',{value:undefined,configurable:true})
 const exec=vi.fn(()=>true);Object.defineProperty(document,'execCommand',{value:exec,configurable:true})
 try{
  const copy=()=>[...f.page('netbird').querySelectorAll<HTMLButtonElement>('button')].find(b=>b.getAttribute('aria-label')===t('ux.copyIp')||b.getAttribute('aria-label')==='Copied')!
  copy().click();await settle()
  expect(exec).toHaveBeenCalledWith('copy')
  expect(copy().getAttribute('aria-label')).toBe('Copied')
 }finally{
  f.done()
  if(clipboard)Object.defineProperty(navigator,'clipboard',clipboard);else delete (navigator as {clipboard?:unknown}).clipboard
  if(execCommand)Object.defineProperty(document,'execCommand',execCommand);else delete (document as {execCommand?:unknown}).execCommand
 }
})
