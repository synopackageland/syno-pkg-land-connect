// @vitest-environment jsdom
// Users read translated messages only: no page shows a backend snake_case code,
// even for a code that has no message of its own.
import {expect,test} from 'vitest'
import {fixture,connected} from './providerFixture'
import {settle} from './support'

const CODE=/\b[a-z0-9]+(?:_[a-z0-9]+)+\b/g
// Text node by text node: textContent would glue neighbours and hide word boundaries.
function codesIn(root:Node):string[]{
 const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT),found:string[]=[]
 for(let n=walker.nextNode();n;n=walker.nextNode())found.push(...(n.textContent?.match(CODE)??[]))
 return found
}

test('no rendered error shows a raw code',async()=>{
 const f=fixture([
  {provider:'cloudflare',state:'failed',mode:'domain',code:'cloudflare_brand_new_failure',simulation:false,shareUrl:''},
  {provider:'netbird',state:'failed',code:'control_unreachable',simulation:false,shareUrl:''},
  connected('tailscale',{apply:{phase:'reauth-required',code:'apply_reauth_required'}}),
 ] as never,{
  'apps.workersstatus':()=>({connected:true,invalid:true,code:'apps_relay_token_invalid',subdomain:'acme'}),
 });await settle()
 for(const [page,label] of [['status',''],['cloudflare','Cloudflare'],['netbird','NetBird'],['tailscale','Tailscale']] as const){
  if(label)await f.nav(label)
  expect(f.page(page).querySelectorAll('[data-ui-callout-code]'),page).toHaveLength(0)
 }
 // Every error surface is on screen: the Home's attention cards and each provider's callout.
 expect(f.page('status').querySelectorAll('[data-attention-item]').length).toBeGreaterThanOrEqual(3)
 expect(f.page('netbird').querySelector('[data-nb-error]')).toBeTruthy()
 expect(codesIn(document.body)).toEqual([])
 f.done()
})
