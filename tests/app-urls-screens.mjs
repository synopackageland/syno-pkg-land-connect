// Local-only screenshots of the App URLs page with mocked apps.* answers.
// No DSM, Tailscale, Cloudflare or NetBird is contacted.
// Run: node tests/app-urls-screens.mjs <output-directory> [shot-name-pattern]
/* global document */
import {mkdir} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/app-urls')
await mkdir(out,{recursive:true})
const entry='/__apps-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>App URLs</title><style>html,body{height:100%;margin:0}#app{width:100%;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext} from '@synopackageland/sdk'
const q=new URLSearchParams(location.search)
const scenario=q.get('s')||'all'
const pinW={mode:'W',names:['nas.example.com','*.example.com'],issuer:'Example Public CA R1',notAfter:'2027-01-31T00:00:00Z'}
const pinS={mode:'S',spki:'3f9a0c'.repeat(10)+'ab12',previousSpki:scenario==='reenable'?'77c1e2'.repeat(10)+'09fe':undefined,issuer:'Synology Inc. CA',notAfter:'2030-06-30T00:00:00Z'}
const apps=[
 {package:'SynoPkgHomeAssistant',name:'Home Assistant',port:28123,publicCapable:true,protection:{upstreamAccess:'public'},settingsUrl:'https://nas.example.com:5001/webman/3rdparty/SynoPkgHomeAssistant/sso/settings.cgi',private:{state:'active',url:'https://nas.tailnet.ts.net:28123/'},public:{state:'active',url:'https://ha.example.com'}},
 {package:'SynoPkgNavidrome',name:'Navidrome',port:28533,publicCapable:true,protection:{passthrough:['/rest/']},settingsUrl:'https://nas.example.com:5001/webman/3rdparty/SynoPkgNavidrome/sso/settings.cgi',private:{state:'active',url:'https://nas.tailnet.ts.net:28533/',reachable:false}},
 {package:'SynoPkgJellyfin',name:'Jellyfin',port:28096,publicCapable:true,protection:{optionalPassthrough:['/Videos/']},private:{state:'paused',reason:'route_mismatch',url:'https://nas.tailnet.ts.net:28096/'}},
 {package:'SynoPkgConvertX',name:'ConvertX',port:28300,publicCapable:true,protection:{},settingsUrl:'https://nas.example.com:5001/webman/3rdparty/SynoPkgConvertX/sso/settings.cgi'},
 {package:'SynoPkgVaultwarden',name:'Vaultwarden',port:28443,publicCapable:true,protection:{},private:{state:'paused',reason:'cert_mismatch',url:'https://nas.tailnet.ts.net:28443/'},public:{state:'active',url:'https://vault.example.com',cleanup:true}},
 {package:'SynoPkgGopeed',name:'Gopeed',port:28900,removed:true,protection:{},private:{state:'active',url:'https://nas.tailnet.ts.net:28900/'}},
 {package:'SynoPkgOpenList',name:'OpenList',port:28244,needsUpdate:true,protection:{}},
]
// Custom apps: added by URL, opened with the free link or the user's own domain only.
const custom=[
 {package:'custom.plex',name:'Plex',port:32400,publicCapable:true,protection:{},custom:{url:'http://127.0.0.1:32400',appSignIn:false,insecureTls:false,reachable:true},public:{state:'active',url:'https://plex.example.com'}},
 {package:'custom.printer',name:'Printer',port:443,publicCapable:true,protection:{},custom:{url:'https://192.168.1.30',appSignIn:true,insecureTls:true,reachable:false}},
]
const views={
 all:{apps,private:{available:true,host:'nas.tailnet.ts.net'},public:{available:true,connected:true,zone:'example.com'}},
 unavailable:{apps,private:{available:false,code:'https-off'},public:{available:true,connected:true,zone:'example.com',invalid:true}},
 nodomain:{apps:apps.slice(3,5).map(a=>({...a,private:undefined,public:undefined})),private:{available:false,code:'tailscale-off'},public:{available:false,connected:false}},
 corrupt:{apps:apps.slice(3).map(a=>({...a,private:undefined,public:undefined,removed:false})),private:{available:true},public:{available:true,connected:false},stateCorrupt:true},
 custom:{apps:[...apps.slice(0,1),...apps.slice(3,4),...custom],private:{available:true,host:'nas.tailnet.ts.net'},public:{available:true,connected:true,zone:'example.com'}},
 empty:{apps:[],private:{available:true},public:{available:true,connected:true,zone:'example.com'}},
 // An App uninstalled while on its own domain (its INFO is gone), next to DSM; nothing to pick.
 removed:{apps:[{package:'SynoPkgNavidrome',name:'Navidrome',port:28533,removed:true,protection:{},public:{state:'active',url:'https://music.example.com'}},{package:'dsm',dsm:true,name:'DSM',port:5001,publicCapable:true,protection:{upstreamAccess:'public'},public:{state:'active',url:'https://nas.example.com'}}],private:{available:true,host:'nas.tailnet.ts.net'},public:{available:true,connected:true,zone:'example.com'}},
}
const view=views[q.get('v')||'all']
// cf: the Cloudflare tunnel serving, connecting, or failing until DSM's certificate is reviewed.
const tunnels={serving:{state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}]},connecting:{state:'connecting',mode:'domain',hostname:'nas.example.com'},review:{state:'failed',code:'pin_proxy_required',mode:'domain',hostname:'nas.example.com'}}
const cloudflare={provider:'cloudflare',shareUrl:'',...tunnels[q.get('cf')||'serving']}
const dsmPin=q.get('dpin')==='W'?{mode:'W',names:['nas.example.com'],issuer:'Example Public CA R1',notAfter:'2027-01-31T00:00:00Z'}:{mode:'S',spki:'3f9a0c'.repeat(10)+'ab12',issuer:'Synology Inc. CA',notAfter:'2030-06-30T00:00:00Z'}
// The pick list: a package, services from DSM's port definitions (services.d)
// and listeners, as apps.customcandidates returns them.
const candidates=[
 {name:'Jellyfin',url:'http://127.0.0.1:8096',source:'package'},
 {name:'HTTP',url:'http://127.0.0.1:80',source:'service',service:{id:'http',title:'HTTP',desc:'Web Station',titleKey:'firewall:firewall_service_opt_http'}},
 {name:'HTTPS',url:'https://127.0.0.1:443',source:'service',service:{id:'https',title:'HTTPS',desc:'Web Station',titleKey:'firewall:firewall_service_opt_https'}},
 {name:'HTTPS',url:'https://127.0.0.1:7001',source:'service',service:{id:'SYNO.SDS.App.FileStation3.Instance_https',title:'HTTPS',desc:'File Station'}},
 {name:'Docker(TCP)',url:'http://127.0.0.1:18096',source:'service',service:{id:'media_tcp',title:'Docker(TCP)',desc:'Docker media'}},
 {name:'Radicale',url:'http://127.0.0.1:5555',source:'service',service:{id:'contacts_radicale',title:'Radicale',desc:'Contacts'}},
 {name:'HTTP',url:'http://127.0.0.1:7000',source:'service',service:{id:'SYNO.SDS.App.FileStation3.Instance_http',title:'HTTP',desc:'File Station'}},
 {name:'',url:'http://127.0.0.1:9000',source:'listening'},
]
// DSM's SYNO.Core.Service.PortInfo in the administrator's DSM language.
const portInfo={
 en:{http:['HTTP','Web Station and Web Mail'],https:['HTTPS','Web Station and Web Mail'],contacts_radicale:['Radicale','Contacts'],ssh:['SSH','Encrypted terminal service']},
 de:{http:['HTTP','Web Station und Web Mail'],https:['HTTPS','Web Station und Web Mail'],contacts_radicale:['Radicale','Kontakte'],ssh:['SSH','Verschlüsselter Terminaldienst']},
}[q.get('l')||'en']
const ports={http:'80',https:'443',contacts_radicale:'5555',ssh:'22'}
const realFetch=window.fetch.bind(window)
window.fetch=async(url,init)=>url==='/webapi/entry.cgi'&&portInfo&&new URLSearchParams(String(init?.body)).get('api')==='SYNO.Core.Service.PortInfo'
 ?new Response(JSON.stringify({success:true,data:{port_info:Object.entries(portInfo).map(([id,[name,desc]])=>({port_id:id,name,desc,dst_port:[ports[id]],protocol:'tcp',src_port:null}))}}))
 :realFetch(url,init)
const connections=[cloudflare,{provider:'tailscale',state:'connected',loggedIn:true,shareUrl:''},{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud',netbirdIP:'100.92.10.4',shareUrl:''}]
const ctx=createStandaloneContext({locale:q.get('l')||'en',user:{name:'fixture-admin',isAdmin:true},
 async call(method,params){
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:q.get('nb')==='0'?connections.slice(0,2):connections}
  if(method==='apps.list'){if(scenario==='loading')return new Promise(()=>{});return view}
  if(method==='apps.inspect'){const a=apps.find(x=>x.package===params.package);return {ready:true,package:a.package,name:a.name,port:a.port,protection:a.protection,pin:scenario==='reenable'||scenario==='pinS'?pinS:pinW,...(scenario==='private'?{dsm:{port:5001,pin:{...pinW,names:['nas.example.com']}}}:{})}}
  if(method==='apps.inspectdsm')return {scope:'public',dsm:{port:5001,pin:dsmPin},token:'fixture-review'}
  if(method==='apps.setdsm')return {approved:true}
  if(method==='apps.customcandidates')return {candidates}
  if(method==='binaries.status')return {product:params?.product,current:'1.0.0',phase:'idle'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }})
ctx.files={token:()=>'fixture-token'}
// w=1: inside a DSM window rather than a full page.
if(q.get('w')==='1')ctx.fullPage=false
mount(document.querySelector('#app'),ctx)
`
// A row's marker sits in its App cell; the table row holds the pick box.
const rowOf=(p,attr,pkg)=>p.locator('tr',{has:p.locator(`[${attr}="${pkg}"]`)})
// Opens an App's dialog from its row.
const open=async(p,pkg)=>{await rowOf(p,'data-app-row',pkg).locator('[data-app-open]').click();await p.locator('[data-app-dialog-step]').waitFor()}
// Add app, filled in from the NAS's pick list.
const addCustom=async p=>{await p.locator('[data-apps-add]').click();await p.locator('[data-custom-dialog] [data-custom-url] input').fill('http://127.0.0.1:8096');await p.locator('[data-custom-dialog] [data-custom-name] input').fill('Jellyfin')}
// Add app with its pick list open.
const pickList=async p=>{await p.locator('[data-apps-add]').click();await p.locator('[data-custom-dialog] [data-custom-pick] [role="combobox"]').click();await p.locator('[role="option"]').first().waitFor()}
const shots=[
 {name:'01-all-statuses',q:'s=all'},
 {name:'02-all-statuses-zh-TW',q:'s=all&l=zh-TW'},
 {name:'03-loading',q:'s=loading'},
 {name:'04-app-dialog',q:'s=all',act:p=>open(p,'SynoPkgHomeAssistant'),dialog:true},
 {name:'05-private-approval-W-with-DSM',q:'s=private',act:async p=>{await open(p,'SynoPkgConvertX');await p.locator('button[role="switch"][data-dialog-switch], [data-dialog-switch] button[role="switch"]').first().click();await p.locator('[data-dialog-save]').click();await p.locator('[data-apps-approval]').waitFor()},dialog:true},
 {name:'06-reenable-approval-S-fingerprint',q:'s=reenable',act:async p=>{await open(p,'SynoPkgVaultwarden');await p.locator('[data-dialog-reenable="private"]').click();await p.locator('[data-apps-approval]').waitFor()},dialog:true},
 {name:'07-public-approval-multi',q:'s=public',act:async p=>{for(const k of ['SynoPkgNavidrome','SynoPkgConvertX'])await p.locator(`[data-app-pick="${k}"]`).click();await p.locator('[data-apps-batch-create="public"]').click();await p.locator('[data-apps-approval]').waitFor()},dialog:true},
 {name:'08-turn-off-public-confirm',q:'s=all',act:async p=>{await open(p,'SynoPkgHomeAssistant');await p.locator('[data-dialog-choice] label',{hasText:'Off'}).locator('[role="radio"]').click();await p.locator('[data-dialog-save]').click();await p.locator('[data-off-confirm]').waitFor()},dialog:true},
 {name:'10-unavailable-https-off-cf-invalid',q:'s=all&v=unavailable&nb=0'},
 {name:'11-nothing-set-up',q:'s=all&v=nodomain&nb=0'},
 {name:'12-state-corrupt',q:'s=all&v=corrupt&nb=0'},
 {name:'13-no-apps',q:'s=all&v=empty&nb=0'},
 {name:'14-paused-dialog',q:'s=all&nb=0',act:p=>open(p,'SynoPkgJellyfin'),dialog:true},
 {name:'15-min-640x420',q:'s=all',viewport:{width:640,height:420}},
 {name:'18-overview-dsm-review',q:'s=all&cf=review',nav:null},
 {name:'19-cloudflare-dsm-review',q:'s=all&cf=review',nav:/^Cloudflare$/},
 {name:'20-dsm-only-approval-S',q:'s=all&cf=review',nav:/^Cloudflare$/,act:async p=>{await p.locator('[data-page="cloudflare"] [data-dsm-review-open]').click();await p.locator('[data-apps-approval]').waitFor()},dialog:true},
 ...['light','dark'].flatMap(scheme=>[
  {name:`22-custom-list-960x600-${scheme}`,q:'s=all&v=custom&nb=0',viewport:{width:960,height:600},scheme},
  {name:`23-custom-add-960x600-${scheme}`,q:'s=all&v=custom&nb=0',viewport:{width:960,height:600},scheme,act:addCustom,dialog:true},
  {name:`24-custom-add-gate-off-960x600-${scheme}`,q:'s=all&v=custom&nb=0',viewport:{width:960,height:600},scheme,act:async p=>{await addCustom(p);await p.locator('[data-custom-gate-switch] [role="switch"], [data-custom-gate-switch][role="switch"]').first().click();await p.locator('[data-custom-risk]').waitFor()},dialog:true},
 ]),
 ...['en','de'].map(l=>({name:`25-custom-pick-dsm-services-${l}`,q:`s=all&v=custom&nb=0&l=${l}`,viewport:{width:960,height:640},act:pickList,dialog:true})),
 ...['en','zh-TW'].map(l=>({name:`26-removed-and-dsm-window-${l}`,q:`s=all&v=removed&nb=0&w=1&l=${l}`})),
 {name:'21-dsm-only-approval-W-zh-TW',q:'s=all&cf=review&dpin=W&l=zh-TW',nav:null,act:async p=>{await p.locator('[data-page="status"] [data-dsm-review-open]').click();await p.locator('[data-apps-approval]').waitFor()},dialog:true},
]
const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),server:{host:'127.0.0.1',port:0},
 plugins:[{name:'apps-local-fixture',
  resolveId(id){if(id===entry)return id},
  load(id){if(id===entry)return fixture},
  configureServer(s){s.middlewares.use(async(req,res,next)=>{
   if(!req.url?.startsWith('/__apps.html'))return next()
   try{res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml(req.url,html))}catch(e){next(e)}
  })},
 }]})
let browser
try{
 await server.listen()
 const {port}=server.httpServer.address()
 browser=await chromium.launch()
 const only=process.argv[3]?new RegExp(process.argv[3]):null
 for(const shot of shots.filter(s=>!only||only.test(s.name))){
  const page=await browser.newPage({viewport:{width:960,height:640},deviceScaleFactor:2})
  const errors=[]
  page.on('pageerror',e=>errors.push(e.message))
  await page.goto(`http://127.0.0.1:${port}/__apps.html?${shot.q}`)
  await page.locator('[data-nav-item]').first().waitFor()
  // nav: the page to open; null stays on the Overview.
  const nav=shot.nav===undefined?/App/:shot.nav
  if(nav)await page.locator('[data-nav-item]',{hasText:nav}).first().click()
  if(shot.nav===undefined){if(!shot.q.includes('loading'))await page.locator('[data-app-row], [data-apps-empty], [data-apps-none]').first().waitFor()}
  else await page.locator('[data-dsm-review]:visible').first().waitFor()
  if(shot.viewport)await page.setViewportSize(shot.viewport)
  // Dark tokens: data-ui-color-scheme on the app roots (Connect has no dark switch yet).
  if(shot.scheme==='dark')await page.evaluate(()=>{for(const n of [document.documentElement,...document.querySelectorAll('[data-ui-app]')])n.dataset.uiColorScheme='dark'})
  if(shot.act)await shot.act(page)
  await page.waitForTimeout(300)
  // The page scrolls inside the DSM-sized window; a tall viewport shows it whole.
  if(!shot.viewport&&!shot.dialog){
   const height=await page.evaluate(()=>Math.max(...[...document.querySelectorAll('*')].map(e=>e.scrollHeight)))
   await page.setViewportSize({width:960,height:Math.min(4000,Math.max(640,height+40))})
   await page.waitForTimeout(200)
  }
  await page.screenshot({path:resolve(out,shot.name+'.png')})
  if(errors.length)throw new Error(`${shot.name}: ${errors.join('; ')}`)
  await page.close()
  console.log(shot.name)
 }
}finally{
 await browser?.close()
 await server.close()
}
