// Phone section screenshots of the real Vue UI (the Overview's Phone section,
// on Connect's ways or the Local network) with synthetic, public-safe answers. No DSM,
// Cloudflare, NetBird or Tailscale is contacted. Each shot decodes nothing
// itself; tests/phone-qr-decode.py checks the codes in the PNGs.
// Run: node tests/phone-card-screens.mjs <output-directory>
import {mkdir} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/phone-card-screens')
await mkdir(out,{recursive:true})
const entry='/__phone-card-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Phone panel</title><style>html,body{height:100%;margin:0}#app{width:100%;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext} from '@synopackageland/sdk'
const q=new URLSearchParams(location.search)
const lanOnly=q.get('s')==='lan'||q.get('s')==='none'
const quick=q.get('s')==='quick'
const pin='9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08'
const cfOnly=[{provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}],shareUrl:''}]
const connections=lanOnly?[]:q.get('s')==='cf'?cfOnly:quick?[{provider:'cloudflare',state:'connected',mode:'quick',endpoints:[{url:'https://example-words-here.trycloudflare.com',public:true,localPort:5000}],shareUrl:''}]:[
 {provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true,localPort:5000}],shareUrl:''},
 {provider:'tailscale',state:'connected',loggedIn:true,endpoints:[{url:'http://192.0.2.21:5000/',public:false,localPort:5000},{url:'http://tailscale.example.com:5000/',public:false,localPort:5000}],shareUrl:''},
 {provider:'netbird',state:'connected',netbirdFQDN:'netbird.example.com',netbirdIP:'192.0.2.22',endpoints:[{url:'http://192.0.2.22:5000/',public:false,localPort:5000},{url:'http://netbird.example.com:5000/',public:false,localPort:5000}],shareUrl:''},
 {provider:'ipv6',state:'disconnected',shareUrl:''},
]
mount(document.querySelector('#app'),createStandaloneContext({displayName:'Syno Connect Center',locale:q.get('l')||'en',user:{name:'Administrator',isAdmin:true},
 async call(method,params){
  if(method==='app.status')return {admin:true,nasName:'DiskStation'}
  if(method==='phone.addnas')return {name:'DiskStation',ports:true,addresses:[{url:'https://192.0.2.20:5001',kind:'lan',sha256:pin}]}
  if(method==='apps.workersstatus'&&q.get('s')==='cf')return {connected:false}
  if(method==='apps.workersstatus')return lanOnly||quick?{connected:false}:{connected:true,relayState:'connected',subdomain:'example',dsm:{provider:'workers',url:'https://dsm.example.workers.dev'}}
  if(method==='ipv6.status')return {state:'unavailable',reason:'no_global_address',addresses:[],endpoints:[]}
  if(method==='ports.list')return {detection:'ready',entries:[{id:'dsm-https',name:'DSM HTTPS',protocol:'https',port:5001,source:'dsm',enabled:true,state:'ready'}]}
  if(method==='connection.check')return {provider:params.provider,state:'ok',externalVerified:true,checkedAt:'2026-11-07T02:20:00Z'}
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections}
  if(method==='apps.list')return {apps:[],private:{available:false},public:{available:false,connected:false}}
  if(method==='binaries.status')return {product:params?.product,current:'1.0.0',phase:'idle'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }}))
`
const narrow={width:390,height:844}
const medium={width:1000,height:900}
const shots=[
 {name:'01-local-network-en',q:'s=none'},
 {name:'02-local-network-zh-TW',q:'s=none&l=zh-TW'},
 {name:'03-local-network-en-narrow',q:'s=none',viewport:narrow},
 {name:'04-cloudflare-en',q:'s=cf'},
 {name:'05-cloudflare-zh-TW',q:'s=cf&l=zh-TW'},
 {name:'06-cloudflare-en-narrow',q:'s=cf',viewport:narrow},
 {name:'07-tailscale-en',q:'s=all',open:'Tailscale'},
 {name:'08-tailscale-zh-TW-narrow',q:'s=all&l=zh-TW',open:'Tailscale',viewport:narrow},
 {name:'09-all-routes-zh-TW-dark',q:'s=all&l=zh-TW',dark:true},
 {name:'10-cloudflare-en-medium',q:'s=cf',size:medium},
 {name:'11-cloudflare-zh-TW-medium',q:'s=cf&l=zh-TW',size:medium},
 {name:'12-cloudflare-en-wide',q:'s=cf',size:{width:1600,height:1000}},
]
const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),server:{host:'127.0.0.1',port:0,allowedHosts:['nas.example.com']},
 plugins:[{name:'phone-card-local-fixture',
  resolveId(id){if(id===entry)return id},
  load(id){if(id===entry)return fixture},
  configureServer(s){s.middlewares.use(async(req,res,next)=>{
   if(!req.url?.startsWith('/__phone.html'))return next()
   try{res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml(req.url,html))}catch(e){next(e)}
  })},
 }]})
let browser
try{
 await server.listen()
 const {port}=server.httpServer.address()
 browser=await chromium.launch({args:['--host-resolver-rules=MAP nas.example.com 127.0.0.1']})
 for(const shot of shots){
  const page=await browser.newPage({viewport:shot.viewport??shot.size??{width:1280,height:900},deviceScaleFactor:1,reducedMotion:'reduce',hasTouch:!!shot.viewport})
  const errors=[]
  page.on('pageerror',e=>errors.push(e.message))
  await page.goto(`http://nas.example.com:${port}/__phone.html?${shot.q}`)
  // Connect has no dark switch; this sets the ui theme's own attribute, as wizard-screens does.
  if(shot.dark)await page.evaluate(()=>{const d=globalThis.document;for(const n of [d.documentElement,...d.querySelectorAll('[data-ui-app]')])n.dataset.uiColorScheme='dark'})
  // The Phone section starts open, on the best route.
  await page.locator('[data-phone-panel] [data-phone-route-qr]').waitFor()
  if(shot.open){
   await page.locator('[data-phone-way][role=combobox], [data-phone-way] [role=combobox]').first().click()
   const option=page.locator('[role=option]:visible',{hasText:shot.open}).first()
   await option.waitFor()
   // The list (a sheet on a phone) opens with a short motion; pick once it has settled.
   await page.waitForTimeout(600)
   // A phone's list is a sheet, which answers a tap.
   if(shot.viewport)await option.tap();else await option.click()
   await page.locator('[data-phone-route="'+shot.open.toLowerCase()+'"]').waitFor()
  }
  // Wide: the whole section; narrow: the menu and the code under the phone.
  await page.locator(shot.viewport?'[data-phone-way]':'[data-home-phone]').first().evaluate(e=>e.scrollIntoView({block:'start'}))
  // Catalog screenshots carry only the synthetic fixture and its documentation IP range.
  for(const url of await page.locator('[data-phone-route-url]').allTextContents())
   if(!/^https?:\/\/(?:192\.0\.2\.\d+|(?:[a-z-]+\.)*example\.(?:com|workers\.dev))(?::\d+)?(?:\/m|\/webman\/3rdparty\/SynoPkgConnect\/m\/index\.html)$/.test(url))throw new Error('Non-catalog address: '+url)
  await page.waitForTimeout(400)
  await page.screenshot({path:resolve(out,shot.name+'.png')})
  if(errors.length)throw new Error(`${shot.name}: ${errors.join('; ')}`)
  await page.close()
  console.log(shot.name)
 }
}finally{
 await browser?.close()
 await server.close()
}
