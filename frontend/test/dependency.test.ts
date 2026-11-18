// @vitest-environment jsdom
import {createApp,h,reactive} from 'vue'
import {afterEach,expect,test,vi} from 'vitest'
import {createStandaloneContext} from '@synopackageland/sdk'
import {provideConnectState} from '../src/connect/state'
import ServerDependencyBanner from '../src/components/ServerDependencyBanner.vue'
import {settle} from './support'
import {dependencyLinks,type DependencyReport} from '../src/connect/dependency'
import {translate} from '../src/i18n'
afterEach(()=>{document.body.innerHTML=''})
const problem=(hop:'tunnel'|'bridge'|'signal'):DependencyReport=>({enabled:true,problem:{code:'cloudflare_not_connected',piece:hop,hop,action:'connect_cloudflare',page:'cloudflare',impact:'connection'}})
test('a lost tunnel marks the NAS hop for management and Signal; a missing Worker marks only the Signal lane',()=>{
 expect(dependencyLinks(problem('tunnel'),'management').map(l=>l.state)).toEqual(['unknown','error'])
 expect(dependencyLinks(problem('tunnel'),'signal').map(l=>l.state)).toEqual(['unknown','error'])
 expect(dependencyLinks(problem('bridge'),'management').map(l=>l.state)).toEqual(['active','active'])
 expect(dependencyLinks(problem('bridge'),'signal').map(l=>l.state)).toEqual(['error','unknown'])
 expect(dependencyLinks(problem('signal'),'signal').map(l=>l.state)).toEqual(['active','error'])
})
test('all dependency messages and actions exist without final periods in every locale',()=>{
 const keys=['cloudflare_not_connected','cloudflare_own_domain_required','cloudflare_reconnect_required','workers_not_connected','workers_token_invalid','workers_routes_permission','workers_zone_account','workers_permission_denied','workers_unavailable','workers_api_refused','workers_rate_limited','netbird_server_bridge_missing','netbird_server_signal_blocked','netbird_server_grpc_blocked','netbird_server_unreachable','cloudflare_tunnel_missing','workers_route_conflict','workers_name_conflict','workers_state_corrupt','unavailable'].map(c=>'dependency.code.'+c)
 keys.push(...['connect_cloudflare','connect_domain','authorize_cloudflare','save_token','replace_token','check_token','repair_bridge','enable_grpc','check_route','open_server','review_bridge','check_status'].map(c=>'dependency.action.'+c),'dependency.cloudflareUsed','dependency.stopOrder','dependency.stopTitle','dependency.stopLine','dependency.stopMaintenance','dependency.path.management','dependency.path.signal','dependency.path.server','dependency.path.devices')
 for(const l of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const k of keys){const v=translate(l,k);expect(v,`${l}:${k}`).not.toBe(k);expect(v).not.toMatch(/[.。．]$/)}
})

// The banner speaks only when the server's Cloudflare dependency has a problem.
async function banner(report:DependencyReport|undefined,unavailable=false){
 const root=document.createElement('div');document.body.append(root)
 const props=reactive<{report?:DependencyReport;unavailable:boolean}>({report,unavailable})
 const app=createApp({setup(){provideConnectState(createStandaloneContext({call:vi.fn(async()=>({})),locale:'en'}));return()=>h(ServerDependencyBanner,{report:props.report,unavailable:props.unavailable,path:true})}})
 app.mount(root);await settle()
 const notice=()=>root.querySelector<HTMLElement>('[data-server-dependency]')
 return {root,props,notice,async close(){notice()!.querySelector<HTMLElement>('[data-ui-callout-close]')!.click();await settle()},done(){app.unmount();root.remove()}}
}
const with_=(code:string,action='connect_cloudflare'):DependencyReport=>({enabled:true,problem:{code,piece:'tunnel',hop:'tunnel',action,page:'cloudflare',impact:'connection'}} as DependencyReport)
test('a healthy dependency shows no banner and no permanent hint',async()=>{
 const f=await banner({enabled:true})
 expect(f.notice()).toBeNull()
 expect(f.root.textContent).not.toContain(translate('en','dependency.cloudflareUsed'))
 f.done()
})
test('a problem banner can be closed and comes back only for a new or returning problem',async()=>{
 const f=await banner(with_('cloudflare_not_connected'))
 expect(f.notice()?.textContent).toContain(translate('en','dependency.code.cloudflare_not_connected'))
 expect(f.notice()?.querySelector('[data-dependency-action]')).toBeTruthy()
 await f.close();expect(f.notice()).toBeNull()
 f.props.report=with_('cloudflare_not_connected');await settle()
 expect(f.notice()).toBeNull()
 f.props.report=with_('workers_token_invalid','replace_token');await settle()
 expect(f.notice()?.textContent).toContain(translate('en','dependency.code.workers_token_invalid'))
 await f.close();expect(f.notice()).toBeNull()
 f.props.report={enabled:true};await settle()
 f.props.report=with_('workers_token_invalid','replace_token');await settle()
 expect(f.notice()?.textContent).toContain(translate('en','dependency.code.workers_token_invalid'))
 f.done()
})
test('an unreadable status keeps a closable warning with its retry action',async()=>{
 const f=await banner({enabled:true},true)
 expect(f.notice()?.textContent).toContain(translate('en','dependency.code.unavailable'))
 expect(f.notice()?.querySelector('[data-dependency-action]')?.textContent).toBe(translate('en','dependency.action.check_status'))
 await f.close();expect(f.notice()).toBeNull()
 f.done()
})
test('only dialog content and field validation keep a Callout open',async()=>{
 const {readdirSync,readFileSync}=await import('node:fs'),{join}=await import('node:path')
 const files=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?files(join(dir,e.name)):e.name.endsWith('.vue')?[join(dir,e.name)]:[])
 const kept=files('src').flatMap(f=>readFileSync(f,'utf8').split('\n').filter(l=>l.includes(':dismissible="false"')).map(l=>f.replace(/^src\//,'')+':'+(/data-[\w-]+|role="alert"/.exec(l)?.[0]??'')))
 expect(kept.sort()).toEqual(['components/AccessEmailList.vue:data-access-confirm-native','components/HostnameInput.vue:role="alert"'])
})
test('no Callout holds a form or a control outside its one action',async()=>{
 const {readdirSync,readFileSync}=await import('node:fs'),{join}=await import('node:path')
 const files=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?files(join(dir,e.name)):e.name.endsWith('.vue')?[join(dir,e.name)]:[])
 const found=files('src').flatMap(f=>[...readFileSync(f,'utf8').matchAll(/<Callout\b[^>]*?(?<!\/)>([\s\S]*?)<\/Callout>/g)]
  .map(m=>m[1]!.replace(/<template[^>]*#actions[^>]*>[\s\S]*?<\/template>/g,''))
  .filter(body=>/<(Button|Form|Input|Select|Switch|Checkbox|CopyCutField|UrlCard|NetBirdServerToken)\b/.test(body)).map(()=>f))
 expect(found).toEqual([])
})

test('dependency links stay unknown until protected checks finish',()=>{
 for(const way of ['management','signal'] as const)expect(dependencyLinks({enabled:true,checking:true},way).map(link=>link.state)).toEqual(['unknown','unknown'])
})
