// Mock screenshots of the Cloudflare page's Own domain and Free link tabs
// side by side (off and on), the Free link's Apps list with DSM, and of the Overview beside another page in
// light and dark, and of Settings beside Cloudflare, from the real Vue UI with synthetic, public-safe answers.
// No DSM or Cloudflare is contacted.
// Run: node tests/cloudflare-tabs-screens.mjs <output-directory>
import {mkdir,readFile,rm} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/cloudflare-tabs-screens')
await mkdir(out,{recursive:true})
const entry='/__cf-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Connect</title><style>html,body{height:100%;margin:0}#app{width:100%;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
// q: l locale, dark 1, cf off|on (the own domain), wk off|on (the Free link, DSM on it).
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext} from '@synopackageland/sdk'
const q=new URLSearchParams(location.search)
// The embedded theme reads the scheme on the App root itself.
if(q.get('dark'))new MutationObserver(()=>document.querySelectorAll('[data-ui-app]:not([data-ui-color-scheme=dark])').forEach(e=>e.setAttribute('data-ui-color-scheme','dark'))).observe(document.body,{childList:true,subtree:true})
const cfOn=q.get('cf')==='on',wkOn=q.get('wk')==='on'
const apps=[
 {package:'SynoPkgHomeAssistant',name:'Home Assistant',port:28123,publicCapable:true,protection:{},...(cfOn?{public:{state:'active',url:'https://ha.example.com'}}:{}),...(wkOn?{relay:{state:'active',url:'https://homeassistant.example.workers.dev'}}:{})},
 {package:'SynoPkgNavidrome',name:'Navidrome',port:28533,publicCapable:true,protection:{}},
]
const cloudflare=cfOn?{provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}],shareUrl:''}:{provider:'cloudflare',state:'disconnected',shareUrl:''}
const workers=wkOn?{connected:true,subdomain:'example',dsm:{provider:'workers',url:'https://nas.example.workers.dev'}}:cfOn?{connected:false,dsm:{provider:'cloudflare',url:'https://nas.example.com'}}:{connected:false}
mount(document.querySelector('#app'),createStandaloneContext({displayName:'Syno Connect Center',locale:q.get('l')||'en',user:{name:'Administrator',isAdmin:true},
 async call(method){
  if(method==='app.status')return {admin:true}
  if(method==='apps.workersstatus')return workers
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[cloudflare,{provider:'tailscale',state:'connected',loggedIn:true,shareUrl:''}]}
  if(method==='apps.list')return {apps,private:{available:true,host:'nas.tailnet.ts.net'},public:{available:cfOn,connected:cfOn,zone:'example.com'},relay:{connected:wkOn,subdomain:'example'}}
  if(method==='binaries.status')return {current:'1.0.0',phase:'idle'}
  if(method==='connection.check')return {provider:'cloudflare',state:'ok',externalVerified:true,checkedAt:'2026-01-01T08:00:00Z'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }}))
`
const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),server:{host:'127.0.0.1',port:0},
 plugins:[{name:'cf-local-fixture',
  resolveId(id){if(id===entry)return id},
  load(id){if(id===entry)return fixture},
  configureServer(s){s.middlewares.use(async(req,res,next)=>{
   if(!req.url?.startsWith('/__cf.html'))return next()
   try{res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml(req.url,html))}catch(e){next(e)}
  })},
 }]})
let browser
// One screenshot: q, then the page (nav text) and the tab to open.
async function shoot(port,name,q,nav,tab){
 const page=await browser.newPage({viewport:{width:1100,height:900},deviceScaleFactor:1})
 const errors=[]
 page.on('pageerror',e=>errors.push(e.message))
 await page.goto(`http://127.0.0.1:${port}/__cf.html?${q}`)
 await page.locator('[data-nav-item]').first().waitFor()
 if(nav)await page.locator(`[data-nav-item]`).nth(nav).click()
 if(tab){await page.locator('[data-page-tabs]').getByText(tab,{exact:true}).first().click();await page.waitForTimeout(300)}
 await page.waitForTimeout(500)
 // Where the shared rows land, so both tabs can be compared by number too.
 if(tab)console.log(name,JSON.stringify(await page.evaluate(()=>Object.fromEntries(['[data-cf-way]','[data-cf-path]','[data-way-change-toggle]','[data-way-off]','[data-provider-footer]'].map(q=>[q,[...globalThis.document.querySelectorAll(q)].find(e=>e.offsetParent)?.getBoundingClientRect().top|0])))))
 const path=resolve(out,name+'.png')
 await page.screenshot({path})
 if(errors.length)throw new Error(`${name}: ${errors.join('; ')}`)
 await page.close()
 return path
}
// Two screenshots side by side with a caption over each.
async function pair(name,left,right){
 const page=await browser.newPage({viewport:{width:2240,height:960},deviceScaleFactor:1})
 const img=async p=>'data:image/png;base64,'+(await readFile(p.path)).toString('base64')
 const cell=async p=>`<figure style="margin:0;flex:1"><figcaption style="font:600 18px system-ui;margin:0 0 8px">${p.caption}</figcaption><img style="width:100%;border:1px solid #8884" src="${await img(p)}"></figure>`
 await page.setContent(`<body style="margin:0;padding:16px;background:#fff;display:flex;gap:16px">${await cell(left)}${await cell(right)}</body>`)
 await page.screenshot({path:resolve(out,name+'.png'),fullPage:true})
 await page.close()
 await rm(left.path);await rm(right.path)
 console.log(name)
}
try{
 await server.listen()
 const {port}=server.httpServer.address()
 browser=await chromium.launch()
 const tabs={en:['Own domain','Free link'],de:['Eigene Domain','Kostenloser Link']}
 // The nav's Cloudflare entry is the third item; Overview is the first.
 for(const l of ['en','de']){
  const [domain,free]=tabs[l]
  for(const [state,cf,wk] of [['off','off','off'],['on','on','on']]){
   const left={caption:`${l} · ${domain} · ${state}`,path:await shoot(port,`_${l}-${state}-domain`,`l=${l}&cf=${cf}&wk=off`,2,domain)}
   const right={caption:`${l} · ${free} · ${state}`,path:await shoot(port,`_${l}-${state}-free`,`l=${l}&cf=off&wk=${wk}`,2,free)}
   await pair(`cloudflare-tabs-${l}-${state}`,left,right)
  }
 }
 // The Free link on with DSM: its Apps list, DSM first.
 await shoot(port,'cloudflare-apps-en-free-dsm','l=en&cf=off&wk=on',2,'Apps');console.log('cloudflare-apps-en-free-dsm')
 for(const dark of ['','1']){
  const theme=dark?'dark':'light'
  const left={caption:`en · Overview · ${theme}`,path:await shoot(port,`_overview-${theme}`,`l=en&cf=on&dark=${dark}`,0)}
  const right={caption:`en · Cloudflare · ${theme}`,path:await shoot(port,`_cloudflare-${theme}`,`l=en&cf=on&dark=${dark}`,2)}
  await pair(`overview-${theme}`,left,right)
 }
 // Settings, the last nav item, on the same card surface as the other pages.
 const settings={caption:'en · Settings · light',path:await shoot(port,'_settings-light','l=en&cf=on',6)}
 await pair('settings-light',settings,{caption:'en · Cloudflare · light',path:await shoot(port,'_settings-cloudflare','l=en&cf=on',2)})
}finally{
 await browser?.close()
 await server.close()
}
