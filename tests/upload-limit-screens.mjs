// Mock screenshots of Cloudflare's upload limit line on the Cloudflare page:
// the Connection tab, Own domain, Free link and Temporary, with synthetic,
// public-safe answers. No DSM or Cloudflare is contacted.
// Run: node tests/upload-limit-screens.mjs <output-directory> [locale]
import {mkdir,readFile} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/upload-limit-screens')
const locale=process.argv[3]??'en'
await mkdir(out,{recursive:true})
const messages=JSON.parse(await readFile(fileURLToPath(new URL(`../i18n/messages/${locale}.json`,import.meta.url)),'utf8'))
const entry='/__limit-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Connect</title><style>html,body{height:100%;margin:0}#app{width:100%;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
// The own domain and the Free link both on, DSM on the own domain.
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext} from '@synopackageland/sdk'
const q=new URLSearchParams(location.search)
const apps=[{package:'SynoPkgHomeAssistant',name:'Home Assistant',port:28123,publicCapable:true,protection:{},public:{state:'active',url:'https://ha.example.com'}}]
const cloudflare={provider:'cloudflare',state:'connected',mode:'domain',hostname:'nas.example.com',endpoints:[{url:'https://nas.example.com',public:true}],shareUrl:''}
const workers={connected:true,subdomain:'example',dsm:{provider:'cloudflare',url:'https://nas.example.com'}}
mount(document.querySelector('#app'),createStandaloneContext({displayName:'Syno Connect Center',locale:q.get('l')||'en',user:{name:'Administrator',isAdmin:true},
 async call(method){
  if(method==='app.status')return {admin:true}
  if(method==='apps.workersstatus')return workers
  if(method==='connection.status')return {provider:'multiple',state:'connected',connections:[cloudflare]}
  if(method==='apps.list')return {apps,private:{available:false},public:{available:true,connected:true,zone:'example.com'},relay:{connected:true,subdomain:'example'}}
  if(method==='binaries.status')return {current:'1.0.0',phase:'idle'}
  if(method==='connection.check')return {provider:'cloudflare',state:'ok',externalVerified:true,checkedAt:'2026-01-01T08:00:00Z'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }}))
`
const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),server:{host:'127.0.0.1',port:0},
 plugins:[{name:'limit-local-fixture',
  resolveId(id){if(id===entry)return id},
  load(id){if(id===entry)return fixture},
  configureServer(s){s.middlewares.use(async(req,res,next)=>{
   if(!req.url?.startsWith('/__limit.html'))return next()
   try{res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml(req.url,html))}catch(e){next(e)}
  })},
 }]})
let browser
try{
 await server.listen()
 const {port}=server.httpServer.address()
 browser=await chromium.launch()
 // The tabs in strip order; the nav's Cloudflare entry is the third item.
 for(const name of ['connection','domain','free','temporary']){
  const page=await browser.newPage({viewport:{width:960,height:640},deviceScaleFactor:2})
  const errors=[]
  page.on('pageerror',e=>errors.push(e.message))
  await page.goto(`http://127.0.0.1:${port}/__limit.html?l=${locale}`)
  await page.locator('[data-nav-item]').nth(2).click()
  const label=messages[name==='connection'?'tab.connection':'cfpage.tab.'+name]
  await page.locator('[data-page-tabs]').getByText(label,{exact:true}).first().click()
  await page.waitForTimeout(600)
  await page.screenshot({path:resolve(out,`cloudflare-${locale}-${name}.png`)})
  if(errors.length)throw new Error(`${name}: ${errors.join('; ')}`)
  await page.close()
  console.log(`cloudflare-${locale}-${name}`)
 }
}finally{
 await browser?.close()
 await server.close()
}
