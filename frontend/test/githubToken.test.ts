// @vitest-environment jsdom
// The optional GitHub token: saved once after GitHub accepts it, never shown
// again, and offered wherever GitHub's rate limit or a rejected token stops
// an update check or a download.
import {expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {createApp,nextTick} from 'vue'
import {createStandaloneContext} from '@synopackageland/sdk'
import BinarySettings from '../src/BinarySettings.vue'
import {mount as mountApp} from '../src/main'
import {githubTokenUrl} from '../src/connect/github'
import en from '../../i18n/messages/en.json'
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
const TOKEN='not-a-real-token-for-tests-wxyz'
function mount(component:object,call:(m:string,p?:Record<string,unknown>)=>Promise<unknown>,props:Record<string,unknown>={}){
 const root=document.createElement('div');document.body.append(root)
 const app=createApp(component,{ctx:createStandaloneContext({call:call as never}),...props});app.mount(root)
 return {root,unmount:()=>{app.unmount();root.remove()}}
}
// The token's Reset and Apply sit in the Settings page footer, so these tests
// open the whole app at Settings › GitHub.
async function settings(call:(m:string,p?:Record<string,unknown>)=>Promise<unknown>){
 const root=document.createElement('div');document.body.append(root)
 const app=mountApp(root,createStandaloneContext({call:(async(m:string,p?:Record<string,unknown>)=>m==='app.status'?{admin:true}:m==='binaries.status'?{product:p?.product,current:'1.0.0',phase:'idle'}:m.startsWith('github.')?call(m,p):{provider:'cloudflare',state:'disconnected',simulation:false,shareUrl:''}) as never,locale:'en'}))
 await settle()
 ;[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()==='Settings')!.click();await settle()
 root.querySelector<HTMLElement>('[data-ui-tab-value="github-token"]')!.click();await settle()
 return {root,unmount:()=>{app.unmount();root.remove()}}
}
const input=(root:HTMLElement)=>root.querySelector<HTMLInputElement>('[data-github-token-input] input, input[data-github-token-input]')!
function type(el:HTMLInputElement,value:string){el.value=value;el.dispatchEvent(new Event('input'))}

test('the token link opens GitHub prefilled with a fine-grained token and no extra permissions',()=>{
 const u=new URL(githubTokenUrl)
 expect(u.origin+u.pathname).toBe('https://github.com/settings/personal-access-tokens/new')
 expect(u.searchParams.get('name')!.length).toBeLessThanOrEqual(40)
 expect(u.searchParams.get('description')).toBeTruthy()
 expect(Number(u.searchParams.get('expires_in'))).toBeGreaterThan(0)
 expect([...u.searchParams.keys()].sort()).toEqual(['description','expires_in','name'])
})

test('saving checks the token, clears the field and shows only its last four characters',async()=>{
 let saved=false
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{
  if(method==='github.token.set'){saved=true;return {saved:true,last4:String(p?.token).slice(-4),limit:5000,remaining:5000}}
  if(method==='github.token.remove'){saved=false;return {saved:false}}
  return saved?{saved:true,last4:'wxyz'}:{saved:false}
 })
 const {root,unmount}=await settings(call)
 await settle()
 expect(root.textContent).toContain(en['github.label'])
 expect(root.textContent).toContain(en['github.help'])
 expect(root.querySelector<HTMLAnchorElement>('[data-github-token-create]')?.getAttribute('href')).toBe(githubTokenUrl)
 type(input(root),TOKEN);await settle()
 root.querySelector<HTMLButtonElement>('[data-github-token-save]')!.click();await settle()
 expect(call).toHaveBeenCalledWith('github.token.set',{token:TOKEN})
 expect(root.querySelector('[data-github-token-saved]')?.textContent).toBe('Saved · …wxyz')
 expect(root.querySelector('[data-github-token-limit]')?.textContent).toBe('GitHub limit: 5,000 requests per hour')
 expect(root.innerHTML).not.toContain(TOKEN)
 expect(root.querySelector('[data-github-token-remove]')?.className).not.toMatch(/danger/)
 root.querySelector<HTMLButtonElement>('[data-github-token-replace]')!.click();await settle()
 expect(input(root)).toBeTruthy()
 root.querySelector<HTMLButtonElement>('[data-github-token-cancel]')!.click();await settle()
 root.querySelector<HTMLButtonElement>('[data-github-token-remove]')!.click();await settle()
 expect(call).toHaveBeenCalledWith('github.token.remove',{})
 expect(input(root)).toBeTruthy()
 unmount()
})

test('a token GitHub rejects is named and not saved',async()=>{
 const call=vi.fn(async(method:string)=>{if(method==='github.token.set')throw {code:'github_token_invalid'};return {saved:false}})
 const {root,unmount}=await settings(call)
 await settle();type(input(root),TOKEN);await settle()
 root.querySelector<HTMLButtonElement>('[data-github-token-save]')!.click();await settle()
 expect(root.textContent).toContain(en['github.invalid'])
 expect(root.querySelector('[data-github-token-saved]')).toBeNull()
 unmount()
})

test.each([
 ['github_rate_limited','2026-10-14T17:46:40Z',en['github.addToken']],
 ['github_token_invalid','',en['github.replaceToken']],
])('a %s card offers the GitHub token',async(errorCode,retryAt,label)=>{
 const call=vi.fn(async(_m:string,p?:Record<string,unknown>)=>({product:p?.product,current:'1.0.0',phase:'failed',error:'release feed: '+errorCode,errorCode,retryAt}))
 const onGithubToken=vi.fn()
 const {root,unmount}=mount(BinarySettings,call,{onGithubToken})
 await settle()
 const card=root.querySelector('[aria-label=netbird]')!
 if(errorCode==='github_token_invalid')expect(card.querySelector('[role=alert]')?.textContent).toBe(en['binaries.tokenInvalid'])
 const action=card.querySelector<HTMLButtonElement>('[data-github-token-action]')!
 expect(action.textContent?.trim()).toBe(label)
 action.click();await settle()
 expect(onGithubToken).toHaveBeenCalledOnce()
 unmount()
})

test('the GitHub copy exists in every locale without trailing periods',async()=>{
 const keys=Object.keys(en).filter(k=>k.startsWith('github.')||k==='binaries.tokenInvalid')
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  const messages=JSON.parse(readFileSync(`../i18n/messages/${locale}.json`,'utf8')) as Record<string,string>
  for(const key of keys){
   expect(messages[key],`${locale} ${key}`).toBeTruthy()
   expect(messages[key],`${locale} ${key}`).not.toMatch(/[.。]$/)
  }
 }
})
