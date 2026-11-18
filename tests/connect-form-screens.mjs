// Mock screenshots of the Cloudflare connect form at the DSM window size
// (1150×782): the Own domain and Free link tabs while off, and the page after
// the user turned the tunnel off on purpose. It asserts where the form's rows
// land - the consent, the button and the note on the field column - and that a
// deliberate turn-off draws no failure. No DSM or Cloudflare is contacted;
// every answer is synthetic.
// Run: node tests/connect-form-screens.mjs <output-directory>
import {mkdir,readFile} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/connect-form-screens')
await mkdir(out,{recursive:true})
const entry='/__connect-form-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Connect</title><style>html,body{height:100%;margin:0}#app{width:100%;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
// q: l locale; off 1 = turned off on purpose, with the dependency read the
// backend returns while nothing depends on the tunnel any more.
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext} from '@synopackageland/sdk'
const q=new URLSearchParams(location.search)
const off=q.get('off')==='1'
const saved=q.get('saved')==='1'
const cloudflare={provider:'cloudflare',state:'disconnected',mode:'domain',suggestedName:'nas-a1b2c3d4e5f6',...(saved?{hostname:'nas.example.com'}:{}),simulation:false,shareUrl:''}
const dependencies={enabled:false,problem:{code:'cloudflare_not_connected',piece:'tunnel',hop:'tunnel',action:'connect_cloudflare',page:'cloudflare',impact:'connection'}}
mount(document.querySelector('#app'),createStandaloneContext({displayName:'Syno Connect Center',locale:q.get('l')||'en',user:{name:'Administrator',isAdmin:true},
 async call(method){
  if(method==='app.status')return {admin:true}
  if(method==='apps.workersstatus')return {connected:false}
  if(method==='connection.status')return {provider:'multiple',state:'disconnected',simulation:false,shareUrl:'',connections:[cloudflare]}
  if(method==='connection.check')return {provider:'cloudflare',state:'not-checked'}
  if(method==='apps.list')return {apps:[],private:{available:false},public:{available:false,connected:false}}
  if(method==='binaries.status')return {current:'1.0.0',phase:'idle'}
  if(method==='netbirdserver.status')return off?{state:'off',dependencies}:{state:'off'}
  return {provider:'fake',state:'disconnected',simulation:true,shareUrl:''}
 }}))
`
const server=await createServer({root,configFile:resolve(root,'vite.config.ts'),server:{host:'127.0.0.1',port:0},
 plugins:[{name:'connect-form-fixture',
  resolveId(id){if(id===entry)return id},
  load(id){if(id===entry)return fixture},
  configureServer(s){s.middlewares.use(async(req,res,next)=>{
   if(!req.url?.startsWith('/__connect-form.html'))return next()
   try{res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml(req.url,html))}catch(e){next(e)}
  })},
 }]})
// Where the form draws its rows, by left edge and top, from the live DOM; the
// path's own text and whether it holds a failed link.
const geometryOf=async(page,locale)=>{
 const geometry=await page.evaluate(()=>{
  const box=(el)=>el?{left:Math.round(el.getBoundingClientRect().left),top:Math.round(el.getBoundingClientRect().top),bottom:Math.round(el.getBoundingClientRect().bottom)}:null
  const visible=(selector)=>[...globalThis.document.querySelectorAll(selector)].find(e=>e.offsetParent)??null
  const picture=visible('[data-cf-path]')
  return {
   field:box(visible('[data-field-control] input')??visible('[data-cf-saved-host]')),
   consent:box(visible('[data-cf-confirm],[data-free-confirm]')),
   button:box(visible('[data-cf-domain],[data-cf-reconnect],[data-free-connect],[data-free-claim]')),
   note:box(visible('[data-cf-no-domain] p,[data-provider-footer] [data-ui-form-note] p')),
   path:box(picture),
   failure:box(picture?.querySelector('.ui-topology-link[data-link-state=error]')??null),
   pathText:picture?.textContent??'',
  }
 })
 geometry.offWord=await page.evaluate(async l=>(await import('/src/i18n.ts')).translate(l,'overview.reach.off'),locale)
 return geometry
}
// The consent, the button and the note start on the field column; of a way
// the user turned off, the picture says it is off and draws no failure.
function expectRows(name,geometry){
 const edge=geometry.field?.left
 for(const key of ['consent','button','note']){
  const row=geometry[key]
  if(!row)continue // a tab without a note of its own
  if(edge===undefined)throw new Error(`${name}: no field column to measure`)
  if(row.left!==edge)throw new Error(`${name}: ${key} starts at ${row.left}, the fields at ${edge}`)
 }
 if(name.includes('turned-off')){
  if(geometry.failure)throw new Error(`${name}: a turned-off way draws a failed link`)
  if(!geometry.pathText.includes(geometry.offWord))throw new Error(`${name}: the path does not say "${geometry.offWord}"`)
 }
}
async function shoot(port,name,q,tab,locale='en'){
 const page=await browser.newPage({viewport:{width:1150,height:782},deviceScaleFactor:1})
 const errors=[];page.on('pageerror',e=>errors.push(e.message))
 await page.goto(`http://127.0.0.1:${port}/__connect-form.html?${q}`)
 await page.locator('[data-nav-item]').first().waitFor()
 await page.locator('[data-nav-item]').nth(2).click()
 if(tab){await page.locator('[data-page-tabs]').getByText(tab,{exact:true}).first().click();await page.waitForTimeout(300)}
 // A click can scroll the window (the tab strip is sticky); capture from the top.
 await page.evaluate(()=>{for(const el of [globalThis.document.scrollingElement,...globalThis.document.querySelectorAll('*')])if(el&&'scrollTop' in el)el.scrollTop=0})
 await page.waitForTimeout(500)
 const geometry=await geometryOf(page,locale)
 const path=resolve(out,name+'.png')
 await page.screenshot({path})
 if(errors.length)throw new Error(`${name}: ${errors.join('; ')}`)
 await page.close()
 console.log(name,JSON.stringify(geometry))
 expectRows(name,geometry)
 return {path,geometry}
}
// The same page in a window too narrow for a label column.
async function shootNarrow(port,width,height){
 const page=await browser.newPage({viewport:{width,height},deviceScaleFactor:1})
 await page.goto(`http://127.0.0.1:${port}/__connect-form.html?l=en`)
 await page.locator('[data-nav-item]').first().waitFor()
 // Below the shell's breakpoint the sidebar folds; its row is still the way in.
 await page.evaluate(()=>[...globalThis.document.querySelectorAll('[data-nav-item]')].find(n=>n.textContent?.trim()==='Cloudflare')?.click())
 await page.waitForTimeout(600)
 const geometry=await geometryOf(page,'en')
 const path=resolve(out,`_${width}-domain-off.png`)
 await page.screenshot({path})
 await page.close()
 console.log(`${width}px`,JSON.stringify(geometry))
 expectRows(`${width}px`,geometry)
 return {path,geometry}
}
// Two captures side by side with a caption over each; the single files go.
async function pair(name,left,right){
 const page=await browser.newPage({viewport:{width:2320,height:860},deviceScaleFactor:1})
 const img=async p=>'data:image/png;base64,'+(await readFile(p)).toString('base64')
 const cell=async p=>`<figure style="margin:0;flex:1"><figcaption style="font:600 18px system-ui;margin:0 0 8px">${p.caption}</figcaption><img style="width:100%;border:1px solid #8884" src="${await img(p.path)}"></figure>`
 await page.setContent(`<body style="margin:0;padding:16px;background:#fff;display:flex;gap:16px">${await cell(left)}${await cell(right)}</body>`)
 await page.screenshot({path:resolve(out,name+'.png'),fullPage:true})
 await page.close()
 console.log(name)
}
let browser
try{
 await server.listen()
 const {port}=server.httpServer.address()
 browser=await chromium.launch()
 const tabs={en:'Free link','zh-TW':'免費連結'}
 for(const l of ['zh-TW','en']){
  const domain={caption:`${l} · Own domain · turned off`,...await shoot(port,`_${l}-domain-off`,`l=${l}`,undefined,l)}
  const free={caption:`${l} · Free link · turned off`,...await shoot(port,`_${l}-free-off`,`l=${l}`,tabs[l],l)}
  await pair(`connect-form-${l}-tabs`,domain,free)
  const disconnected={caption:`${l} · turned off on purpose`,...await shoot(port,`_${l}-turned-off`,`l=${l}&off=1`,undefined,l)}
  await pair(`connect-form-${l}-turned-off`,domain,disconnected)
 }
 // A saved own domain reconnects: the same consent row, with no Form around it.
 await shoot(port,'_saved-own-domain','l=en&saved=1')
 // A 640px window stacks the form: nothing steps past a label column that is gone.
 const narrow={caption:'en · Own domain · 640px window',...await shootNarrow(port,640,600)}
 await pair('connect-form-narrow',narrow,{caption:'en · Own domain · 1150px window',...await shoot(port,'_en-domain-off-wide','l=en')})
}finally{
 await browser?.close()
 await server.close()
}
