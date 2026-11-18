// Release screenshots of the real Vue UI with synthetic, public-safe answers.
// No DSM, Tailscale, Cloudflare or NetBird is contacted.
// Run: node tests/release-screens.mjs <output-directory>
import {mkdir} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/release-screens')
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
const views={
 all:{apps,private:{available:true,host:'nas.tailnet.ts.net'},public:{available:true,connected:true,zone:'example.com'}},
 unavailable:{apps,private:{available:false,code:'https-off'},public:{available:true,connected:true,zone:'example.com',invalid:true}},
 nodomain:{apps:apps.slice(3,5).map(a=>({...a,private:undefined,public:undefined})),private:{available:false,code:'tailscale-off'},public:{available:false,connected:false}},
 corrupt:{apps:apps.slice(3).map(a=>({...a,private:undefined,public:undefined,removed:false})),private:{available:true},public:{available:true,connected:false},stateCorrupt:true},
 empty:{apps:[],private:{available:true},public:{available:true,connected:true,zone:'example.com'}},
}
const view=scenario==='first'?{apps:apps.slice(0,3),private:{available:false},public:{available:false}}:views[q.get('v')||'all']
if(q.has('dsm')){
 const scope=q.get('dsm')==='free'?'relay':'public'
 const suffix=scope==='relay'?'example.workers.dev':'example.com'
 view.apps=[{package:'dsm',name:'DSM',dsm:true,port:5001,publicCapable:true,protection:{upstreamAccess:'public'},[scope]:{state:'active',url:'https://dsm.'+suffix}}, {package:'SynoPkgOpenList',name:'OpenList',port:28244,publicCapable:true,protection:{},[scope]:{state:'active',url:'https://openlist.'+suffix}}]
 view.private={available:false}
 view.public={available:scope==='public',enabled:scope==='public',connected:true,invalid:scope==='relay',zone:'example.com'}
}
// cf: the Cloudflare tunnel serving, connecting, or failing until DSM's certificate is reviewed.
const tunnels={serving:{state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}]},connecting:{state:'connecting',mode:'domain',hostname:'nas.example.com'},review:{state:'failed',code:'pin_proxy_required',mode:'domain',hostname:'nas.example.com'}}
const cloudflare=q.get('wk')==='on'?{provider:'cloudflare',state:'disconnected',shareUrl:''}:{provider:'cloudflare',shareUrl:'',...tunnels[q.get('cf')||'serving']}
const dsmPin=q.get('dpin')==='W'?{mode:'W',names:['nas.example.com'],issuer:'Example Public CA R1',notAfter:'2027-01-31T00:00:00Z'}:{mode:'S',spki:'3f9a0c'.repeat(10)+'ab12',issuer:'Synology Inc. CA',notAfter:'2030-06-30T00:00:00Z'}
const connections=[cloudflare,{provider:'tailscale',state:'connected',loggedIn:true,shareUrl:''},{provider:'netbird',state:'connected',netbirdFQDN:'nas.netbird.cloud',netbirdIP:'100.92.10.4',shareUrl:''}]
mount(document.querySelector('#app'),createStandaloneContext({displayName:'Syno Connect Center',locale:q.get('l')||'en',user:{name:'Administrator',isAdmin:true},
 async call(method,params){
  if(method==='app.status')return {admin:true,nasName:'Home NAS'}
  if(method==='apps.workersstatus')return q.get('wk')==='on'?{connected:true,subdomain:'example',dsm:{provider:'workers',url:'https://dsm.example.workers.dev'}}:{connected:false}
  if(method==='apps.customcandidates')return {candidates:[{name:'Photo library',url:'http://127.0.0.1:8080',source:'package'}]}
  if(method==='ipv6.status')return {state:'unavailable',reason:'no_global_address',addresses:[],endpoints:[]}
  if(method==='ports.list')return {detection:'ready',entries:[{id:'dsm-https',name:'DSM HTTPS',protocol:'https',port:5001,source:'dsm',enabled:true,state:'ready'}]}
  if(method==='connection.check')return {provider:params.provider,state:'ok',externalVerified:true,checkedAt:'2026-10-27T02:20:00Z'}
  if(method==='connection.status'&&q.get('ov')==='free')return {provider:'multiple',state:'connected',connections:[{provider:'cloudflare',state:'disconnected',mode:'domain',hostname:'old.example.com'},{provider:'tailscale',state:'disconnected',loggedIn:true}]}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:scenario==='first'||q.has('dsm')?connections.map(c=>({provider:c.provider,state:'disconnected'})):connections}
  if(method==='apps.list'){if(scenario==='loading')return new Promise(()=>{});return {...view,relay:{connected:q.get('wk')==='on',subdomain:'example'}}}
  if(method==='apps.inspect'&&params.package==='dsm'){const a=view.apps.find(x=>x.dsm);return {ready:true,package:'dsm',name:'DSM',port:5001,hostname:a.relay?'dsm.example.workers.dev':'dsm.example.com',pin:dsmPin,token:'fixture-rename',protection:{upstreamAccess:'public'}}}
  if(method==='apps.inspect'){const a=apps.find(x=>x.package===params.package);return {ready:true,package:a.package,name:a.name,port:a.port,protection:a.protection,pin:scenario==='reenable'||scenario==='pinS'?pinS:pinW,...(scenario==='private'?{dsm:{port:5001,pin:{...pinW,names:['nas.example.com']}}}:{})}}
  if(method==='apps.inspectdsm')return {scope:'public',dsm:{port:5001,pin:dsmPin},token:'fixture-review'}
  if(method==='apps.setdsm')return {approved:true}
  if(method==='binaries.status')return {product:params?.product,current:'1.0.0',phase:'idle'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }}))
`
// A row's marker sits in its App cell; the table row holds the pick box.
const rowOf=(p,attr,pkg)=>p.locator('tr',{has:p.locator(`[${attr}="${pkg}"]`)})
// Opens an App's dialog from its row.
const open=async(p,pkg)=>{await rowOf(p,'data-app-row',pkg).locator('[data-app-open]').click();await p.locator('[data-app-dialog-step]').waitFor()}
const cfTab=value=>async p=>{await p.locator(`[data-ui-tab-value="${value}"]`).click();await p.locator(`[data-tab-panel="${value}"]`).waitFor({state:'visible'})}
const shots=[
 {name:'14-overview-free-link-0086-en',q:'dsm=free&wk=on&ov=free',nav:null,ready:'[data-overview]',act:async p=>{await p.locator('[data-lane-id="workers"]').waitFor();if(await p.locator('[data-lane-id="cloudflare"]').count())throw new Error('Off Own domain row');await p.getByText('Reached from outside',{exact:true}).first().waitFor()}},
 {name:'16-overview-free-link-0086-en-700',q:'dsm=free&wk=on&ov=free',nav:null,ready:'[data-overview]',viewport:{width:700,height:800},act:async p=>{await p.getByText('Reached from outside',{exact:true}).first().waitFor()}},
 {name:'15-cloudflare-working-free-link-0086-en',q:'dsm=free&wk=on&ov=free',nav:/Cloudflare/,act:cfTab('free')},
 {name:'10-app-urls-dsm-free-link-0085-en',q:'dsm=free&wk=on',act:async p=>{if(await p.locator('[data-apps-attention]').count())throw new Error('Unused own domain warning');await p.locator('[data-app-row="dsm"]').waitFor()}},
 {name:'11-dsm-dialog-free-link-0085-en',q:'dsm=free&wk=on',act:p=>open(p,'dsm')},
 {name:'12-app-urls-dsm-own-domain-0085-en',q:'dsm=own',act:async p=>{await p.locator('[data-app-row="dsm"]').waitFor()}},
 {name:'13-dsm-dialog-own-domain-0085-en',q:'dsm=own',act:p=>open(p,'dsm')},
 {name:'01-first-visit-build-0084',q:'s=first',nav:null,ready:'[data-home-first]'},
 {name:'02-setup-wizard-0084',q:'s=first',nav:null,ready:'[data-home-first]',act:async p=>{await p.locator('[data-home-set-up]').click();await p.locator('[data-setup-wizard]').waitFor()}},
 {name:'03-home-0084',q:'s=all',nav:null,ready:'[data-overview]'},
 {name:'04-apps-0084',q:'s=all',act:p=>open(p,'SynoPkgHomeAssistant'),dialog:true},
 {name:'05-custom-app-0084',q:'s=all',act:async p=>{await p.locator('[data-apps-add]').click();await p.locator('[data-custom-dialog]').waitFor()},dialog:true},
 {name:'06-own-domain-0084',q:'s=all',nav:/Cloudflare/,act:cfTab('domain')},
 {name:'07-free-link-0084',q:'s=all&wk=on',nav:/Cloudflare/,act:cfTab('free')},
 {name:'08-temporary-0084',q:'s=all',nav:/Cloudflare/,act:cfTab('temporary')},
 {name:'09-ipv6-guide-0084',q:'s=all',nav:/IPv6/,ready:'[data-ipv6-checklist]'},
]

const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),server:{host:'127.0.0.1',port:0,allowedHosts:['nas.example.com']},
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
 browser=await chromium.launch({args:["--host-resolver-rules=MAP nas.example.com 127.0.0.1"]})
 for(const shot of shots.filter(shot=>!process.env.CONNECT_SCREENS_FILTER||shot.name.includes(process.env.CONNECT_SCREENS_FILTER))){
  const page=await browser.newPage({viewport:{width:1280,height:800},deviceScaleFactor:1})
  const errors=[]
  page.on('pageerror',e=>errors.push(e.message))
  await page.goto(`http://nas.example.com:${port}/__apps.html?${shot.q}`)
  await page.locator('[data-nav-item]').first().waitFor()
  // nav: the page to open; null stays on the Overview.
  const nav=shot.nav===undefined?/App/:shot.nav
  if(nav)await page.locator('[data-nav-item]',{hasText:nav}).first().click()
  if(shot.nav===undefined){if(!shot.q.includes('loading'))await page.locator('[data-app-row], [data-apps-empty], [data-apps-none]').first().waitFor()}
  else await page.locator(shot.ready||'[data-page=cloudflare]').first().waitFor()
  if(shot.viewport)await page.setViewportSize(shot.viewport)
  if(shot.act)await shot.act(page)
  await page.waitForTimeout(300)
  await page.screenshot({path:resolve(out,shot.name+'.png')})
  if(errors.length)throw new Error(`${shot.name}: ${errors.join('; ')}`)
  await page.close()
  console.log(shot.name)
 }
}finally{
 await browser?.close()
 await server.close()
}
