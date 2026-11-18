// Local-only DSM full-page mock. No DSM endpoint or credentials are used.
// Run: node tests/iphone-fullpage.mjs <evidence-directory>
// The page callbacks run in WebKit, where these are browser globals.
/* global window, document, innerWidth, requestAnimationFrame, URL */
import assert from 'node:assert/strict'
import {mkdir, writeFile} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {webkit, devices} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/iphone-fullpage')
await mkdir(out,{recursive:true})
const entry='/__iphone-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Syno Connect Center</title><style>html,body{height:100%;margin:0}#app{width:980px;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext,createStandaloneHost} from '@synopackageland/sdk'
const root=document.querySelector('#app')
window.iphoneEvidence={beforeViewport:innerWidth,beforeContainer:root.getBoundingClientRect().width,refits:0}
// CLI prepareFullPage has already changed the viewport before AppShell mounts,
// but DSM's inline-sized window is still 980px until a host resize refits it.
const meta=document.createElement('meta');meta.name='viewport';meta.content='width=device-width, initial-scale=1';document.head.append(meta)
window.addEventListener('resize',()=>{root.style.width=innerWidth+'px';window.iphoneEvidence.refits++})
mount(root,createStandaloneContext({fullPage:true,displayName:'Syno Connect Center',locale:'en',
 user:{name:'fixture-admin',isAdmin:true},host:createStandaloneHost({host:'dsm',dsmStandalone:true}),
 async call(method,params){
  if(method==='app.status')return {admin:true}
  if(method==='binaries.status')return {product:params?.product,current:'1.0.0',phase:'idle'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }}))
`
const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),
 // Force fresh dependency processing, also allowing an older UI negative control.
 optimizeDeps:{force:true},
 resolve:process.env.CONNECT_TEST_UI?{alias:[{find:/^@synopackageland\/ui$/,replacement:resolve(process.env.CONNECT_TEST_UI)}]}:undefined,
 server:{host:'127.0.0.1',port:0},
 plugins:[{name:'iphone-local-fixture',
  resolveId(id){if(id===entry)return id},
  load(id){if(id===entry)return fixture},
  configureServer(s){s.middlewares.use(async(req,res,next)=>{
   if(req.url!=='/__iphone-fullpage.html')return next()
   try{res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml(req.url,html))}catch(e){next(e)}
  })},
 }],
})
let browser
try{
 await server.listen()
 const {port}=server.httpServer.address()
 browser=await webkit.launch()
 const context=await browser.newContext({...devices['iPhone 15']})
 await context.addInitScript(()=>{
  window.blockedTrustedResizes=0
  window.addEventListener('resize',event=>{
   if(event.isTrusted){window.blockedTrustedResizes++;event.stopImmediatePropagation()}
  },true)
 })
 const page=await context.newPage()
 const errors=[]
 page.on('pageerror',e=>errors.push(e.message))
 await page.goto(`http://127.0.0.1:${port}/__iphone-fullpage.html`)
 await page.locator('[data-ui-layout-mode]').waitFor()
 // Let the scheduled SDK refit and ResizeObserver complete, without injecting resize.
 await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>requestAnimationFrame(r)))))
 const evidence=await page.evaluate(()=>({...window.iphoneEvidence,
  blockedTrustedResizes:window.blockedTrustedResizes,innerWidth,
  scrollWidth:document.documentElement.scrollWidth,
  containerWidth:document.querySelector('#app').getBoundingClientRect().width,
  mode:document.querySelector('[data-ui-layout-mode]')?.getAttribute('data-ui-layout-mode'),
  viewport:document.querySelector('meta[name="viewport"]')?.content,
 }))
 evidence.errors=errors
 await page.screenshot({path:resolve(out,'iphone15-fullpage.png'),fullPage:true})
 await writeFile(resolve(out,'iphone15-fullpage.json'),JSON.stringify(evidence,null,2)+'\n')
 console.log(JSON.stringify(evidence,null,2))
 assert.equal(errors.length,0)
 assert.equal(evidence.beforeContainer,980)
 assert.ok(evidence.beforeViewport>=980)
 assert.ok(evidence.refits>0,'SDK must refit without a trusted resize')
 assert.equal(evidence.innerWidth,devices['iPhone 15'].viewport.width)
 assert.equal(evidence.scrollWidth,evidence.innerWidth)
 assert.equal(evidence.containerWidth,evidence.innerWidth)
 assert.equal(evidence.mode,'mobile')
}finally{
 await browser?.close()
 await server.close()
}
