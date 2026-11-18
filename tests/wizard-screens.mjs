// Local-only screenshots of the setup wizard dialog with mocked answers:
// the Overview's first visit (a picture and Set up), Choose (en, zh-TW, IPv6
// selected with its risk checkbox, a way already on, at 1100/820/640, and at
// DSM's default window and a phone width with the last card selected), Connect for three ways, Choose
// what opens with it, the approval, Ready for the Free link and IPv6, Choose
// at 640x420, the window sizes (DSM's default 960x640 window leaves 960x600
// for the app, 1280x800, 640x600) without page scroll, Choose's selected card
// and footer action in view at those sizes in all nine languages (also for
// the second row's cards, selected by click and by the arrow keys), opened from
// the Overview and from App URLs, the temporary address (its row under the
// cards, its confirm, the address it gives and the Overview's note), light
// and dark at 960x600, and the returning Home's lanes with a long workers.dev
// name in all nine languages. No DSM, Tailscale, Cloudflare or NetBird is
// contacted. The header and footer stay put while the step scrolls, and the
// footer has no Close.
// Run: node tests/wizard-screens.mjs <output-directory>
/* global document, innerHeight, innerWidth, getComputedStyle */
import {mkdir} from 'node:fs/promises'
import {resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'
import {createServer} from '../frontend/node_modules/vite/dist/node/index.js'

const root=fileURLToPath(new URL('../frontend',import.meta.url))
const out=resolve(process.argv[2]??'dist/wizard')
await mkdir(out,{recursive:true})
const entry='/__wizard-fixture.js'
const html='<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Connect</title><style>html,body{height:100%;margin:0}#app{width:100%;height:100%}</style></head><body><div id="app"></div><script type="module" src="'+entry+'"></script></body></html>'
const fixture=`
import {mount} from '/src/main.ts'
import {createStandaloneContext} from '@synopackageland/sdk'
const q=new URLSearchParams(location.search)
const s=q.get('s')||'first'
const pin={mode:'W',names:['example.workers.dev'],issuer:'Example CA',notAfter:'2030-01-01T00:00:00Z'}
const ports={detection:'ready',mappingState:'ok',revision:1,entries:[{id:'dsm-https',name:'DSM (HTTPS)',protocol:'https',port:5001,source:'dsm',enabled:false,state:'disabled'}]}
const apps=[{package:'SynoPkgConvertX',name:'ConvertX',port:28300,publicCapable:true,protection:{}},{package:'SynoPkgJellyfin',name:'Jellyfin',port:28096,publicCapable:true,media:'high',protection:{}},{package:'SynoPkgVaultwarden',name:'Vaultwarden',port:28443,publicCapable:true,protection:{}}]
let approved=false
const off=p=>({provider:p,state:'disconnected',shareUrl:''})
const quick={provider:'cloudflare',state:'connected',mode:'quick',endpoints:[{url:'https://fixture-quick.trycloudflare.com',public:true,localPort:5000}],shareUrl:'https://fixture-quick.trycloudflare.com'}
const conns=s==='ipv6'?[off('cloudflare'),off('tailscale'),off('netbird'),{provider:'ipv6',state:'connected',endpoints:[{url:'https://[2001:db8::10]:5001',public:true,localPort:5001}],shareUrl:''}]:s==='temporary'?[quick,off('tailscale'),off('netbird'),off('ipv6')]:[off('cloudflare'),off('tailscale'),off('netbird'),off('ipv6')]
const relay=s==='relay'?{connected:true,subdomain:q.get('sub')||'acme'}:{connected:false}
mount(document.querySelector('#app'),createStandaloneContext({locale:q.get('l')||'en',user:{name:'fixture-admin',isAdmin:true},
 async call(method,params){
  if(method==='app.status')return {admin:true}
  if(method==='connection.status')return {provider:'multiple',state:'disconnected',connections:conns}
  if(method==='connection.connect'&&params.mode==='quick'){conns[0]=quick;return {provider:'multiple',state:'disconnected',connections:conns}}
  if(method==='ports.list')return ports
  if(method==='apps.workersstatus')return relay
  if(method==='apps.list')return {apps:approved?apps.map((a,i)=>i===0?{...a,relay:{state:'active',url:'https://convertx.acme.workers.dev'}}:i===2?{...a,relay:{state:'active',url:'https://vaultwarden.acme.workers.dev'}}:a):apps,private:{available:false},public:{available:false,connected:false},relay}
  if(method==='apps.inspectdsm')return {scope:'public',dsm:{hostname:'dsm.acme.workers.dev',port:5001,pin},token:'d'}
  if(method==='apps.inspect'){const a=apps.find(x=>x.package===params.package);return {ready:true,package:a.package,name:a.name,port:a.port,protection:{},pin,token:'t',hostname:a.name.toLowerCase()+'.acme.workers.dev',dsm:{hostname:'dsm.acme.workers.dev',port:5001,pin}}}
  if(method==='apps.setrelay'){approved=true;return {results:params.packages.map(p=>({package:p,ok:true}))}}
  if(method==='binaries.status')return {product:params?.product,current:'1.0.0',latest:'1.0.0',phase:'idle'}
  if(method==='ipv6.status')return {state:'ready',reason:'',addresses:[]}
  if(method==='connection.check')return {provider:params.provider,state:'ok',externalVerified:true,checkedAt:'2026-10-26T08:00:00Z'}
  throw {code:'unavailable'}
 }}))
`
const server=await createServer({root,logLevel:'error',server:{port:0},plugins:[{name:'wizard-fixture',configureServer(s){s.middlewares.use((req,res,next)=>{if(req.url.startsWith('/__wizard.html')){res.setHeader('content-type','text/html');s.transformIndexHtml(req.url,html).then(h=>res.end(h));return}next()})},resolveId(id){return id===entry?id:undefined},load(id){return id===entry?fixture:undefined}}]})
await server.listen()
const base=server.resolvedUrls.local[0]
const browser=await chromium.launch()
async function page(scenario,locale='en',viewport={width:1100,height:900},scheme='light'){
 const p=await browser.newPage({viewport,deviceScaleFactor:2,colorScheme:scheme})
 await p.emulateMedia({reducedMotion:'reduce'})
 await p.goto(`${base}__wizard.html?s=${scenario}&l=${locale}`)
 await p.locator('[data-page="status"]').waitFor()
 if(scheme==='dark')await dark(p)
 return p
}
// Dark tokens: data-ui-color-scheme on the app root, which carries the light
// roles itself (Connect has no dark switch yet; this checks the ui theme).
const dark=p=>p.evaluate(()=>{for(const n of [document.documentElement,...document.querySelectorAll('[data-ui-app]')])n.dataset.uiColorScheme='dark'})
// The wizard opens from the Overview (Set up, or Add a way once a way is on)
// or from App URLs (Set up a way while nothing is set up).
async function open(scenario,locale='en',viewport={width:1100,height:900},from='home',scheme='light'){
 const p=await page(scenario,locale,viewport,scheme)
 // A narrow window folds the sidebar into the mobile menu: it opens first.
 if(from==='apps'){if(!await p.locator('[data-nav-item]').count())await p.locator('[data-ui-mobile-menu]').click();await p.locator('[data-nav-item]').nth(1).evaluate(e=>e.click());await p.locator('[data-page="apps"] [data-apps-setup]').click()}
 else await p.locator('[data-page="status"]').locator('[data-home-set-up],[data-home-add-way]').first().click()
 await p.locator('[data-setup-wizard]').waitFor()
 if(scheme==='dark')await dark(p)
 await p.waitForTimeout(400)
 return p
}
// The whole dialog shows inside the window: its band, step and footer, and
// only its step area scrolls; the page behind it does not scroll.
async function fits(p,name){
 const m=await p.evaluate(()=>{
  const dialog=document.querySelector('[data-setup-wizard]').closest('[role="dialog"]').getBoundingClientRect()
  const footer=document.querySelector('[data-wizard-footer]')?.getBoundingClientRect()
  const scroller=document.scrollingElement
  return {dialogInView:dialog.top>=-0.5&&dialog.bottom<=innerHeight+0.5&&dialog.left>=-0.5&&dialog.right<=innerWidth+0.5,footerVisible:!!footer&&footer.bottom<=innerHeight+0.5&&footer.top>=0,pageScroll:scroller.scrollTop}
 })
 console.log(name,JSON.stringify(m))
 if(!m.dialogInView||!m.footerVisible||m.pageScroll)process.exitCode=1
 return m
}
// One element wholly inside the window and every scroll area around it.
async function inView(p,selector,name){
 const m=await p.evaluate(sel=>{
  const el=document.querySelector(sel)
  let view={top:0,bottom:innerHeight,left:0,right:innerWidth}
  for(let e=el?.parentElement;e;e=e.parentElement){
   const o=getComputedStyle(e).overflowY
   if(o==='auto'||o==='scroll'||o==='hidden'){const r=e.getBoundingClientRect();view={top:Math.max(view.top,r.top),bottom:Math.min(view.bottom,r.bottom),left:Math.max(view.left,r.left),right:Math.min(view.right,r.right)}}
  }
  const r=el?.getBoundingClientRect()
  return {found:!!el,inView:!!r&&r.top>=view.top-0.5&&r.bottom<=view.bottom+0.5&&r.left>=view.left-0.5&&r.right<=view.right+0.5}
 },selector)
 console.log(name,JSON.stringify(m))
 if(!m.found||!m.inView)process.exitCode=1
 return m
}
// Choose needs no scrolling: the selected card and the footer's main action
// are wholly inside the window and the step's visible area.
async function choosable(p,name){
 const m=await p.evaluate(()=>{
  const card=document.querySelector('[data-wizard-choose] [role="radio"][aria-checked="true"]')
  const next=document.querySelector('[data-wizard-footer] [data-wizard-next]')
  let view={top:0,bottom:innerHeight,left:0,right:innerWidth}
  for(let e=card?.parentElement;e;e=e.parentElement){
   const o=getComputedStyle(e).overflowY
   if(o==='auto'||o==='scroll'||o==='hidden'){const r=e.getBoundingClientRect();view={top:Math.max(view.top,r.top),bottom:Math.min(view.bottom,r.bottom),left:Math.max(view.left,r.left),right:Math.min(view.right,r.right)}}
  }
  const inside=(r,v)=>!!r&&r.top>=v.top-0.5&&r.bottom<=v.bottom+0.5&&r.left>=v.left-0.5&&r.right<=v.right+0.5
  const win={top:0,bottom:innerHeight,left:0,right:innerWidth}
  const c=card?.getBoundingClientRect(),n=next?.getBoundingClientRect()
  return {way:card?.dataset.wizardWay,card:c&&[Math.round(c.top),Math.round(c.bottom)],view:[Math.round(view.top),Math.round(view.bottom)],cardInView:inside(c,view),nextInView:!!next&&!next.disabled&&inside(n,win)}
 })
 console.log(name,JSON.stringify(m))
 if(!m.cardInView||!m.nextInView)process.exitCode=1
 return m
}
// Select a way's card, then go on with the footer's main action.
async function use(p,way){
 await p.locator(`[data-wizard-way="${way}"]`).click()
 await p.locator('[data-wizard-footer] [data-wizard-next]').click()
}
async function shot(p,name){
 await p.waitForTimeout(300)
 await p.screenshot({path:`${out}/${name}.png`,fullPage:true})
 console.log(name)
}
const click=(p,sel)=>p.locator(sel).first().click()
for(const locale of ['en','zh-TW']){
 const p=await open('first',locale)
 await shot(p,`01-choose-${locale}`)
 await click(p,'[data-wizard-way="ipv6"]');await shot(p,`02-choose-ipv6-${locale}`)
 await p.close()
}
// Every way shows at once. In DSM's default window (960x600 for the app) and
// at a phone's width, IPv6 (the last card) selected: the card, its risk
// checkbox and the footer's button are each reachable, the step body
// scrolling to them while the footer stays put. The window view is the one
// the user sees; the full-page shot is not used since only the step scrolls.
for(const locale of ['en','zh-TW'])for(const [kind,width,height] of [['window',960,600],['phone',390,844]]){
 const p=await open('first',locale,{width,height})
 const name=`80-${kind}-${locale}`
 await p.screenshot({path:`${out}/${name}-top.png`});console.log(`${name}-top`)
 await click(p,'[data-wizard-way="ipv6"]');await p.waitForTimeout(200)
 await inView(p,'[data-wizard-way="ipv6"]',`${name}-ipv6-card`)
 await p.locator('[data-wizard-ipv6-ack]').scrollIntoViewIfNeeded()
 await inView(p,'[data-wizard-ipv6-ack]',`${name}-ipv6-ack`)
 await inView(p,'[data-wizard-footer] [data-wizard-next]',`${name}-next`)
 await p.screenshot({path:`${out}/${name}-ipv6.png`});console.log(`${name}-ipv6`)
 await p.locator('[data-wizard-ipv6-ack]').click();await p.waitForTimeout(100)
 await choosable(p,`${name}-ipv6-ack-choose`)
 await p.screenshot({path:`${out}/${name}-ipv6-ack.png`});console.log(`${name}-ipv6-ack`)
 await p.close()
}
// Step 1 at three widths: the three cards keep their compact topology on one line.
for(const width of [1100,820,640]){
 const p=await open('first','en',{width,height:900})
 await shot(p,`05-choose-${width}`)
 await p.close()
}
{
 const p=await open('first','en',{width:640,height:420})
 await shot(p,'03-choose-640x420')
 await p.close()
}
// The window sizes: the whole dialog shows, the page itself does not scroll.
for(const [width,height] of [[960,600],[1280,800],[640,600]]){
 const p=await open('first','en',{width,height})
 await fits(p,`06-fit-${width}x${height}`)
 await p.screenshot({path:`${out}/06-fit-${width}x${height}.png`});console.log(`06-fit-${width}x${height}`)
 await use(p,'workers');await p.waitForTimeout(300)
 await fits(p,`07-fit-connect-${width}x${height}`)
 await p.screenshot({path:`${out}/07-fit-connect-${width}x${height}.png`});console.log(`07-fit-connect-${width}x${height}`)
 await p.close()
}
// Choosing needs no scroll at the window sizes in every language, opened
// from the Overview and from App URLs, with the Free link preselected and
// with another card selected. The temporary address row follows all five
// cards: one scroll down of the step body at every size, never folded.
for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
 for(const [width,height] of [[960,600],[1280,800],[640,600]]){
  for(const from of ['home','apps']){
   const p=await open('first',locale,{width,height},from)
   await fits(p,`09-fit-${from}-${locale}-${width}x${height}`)
   await choosable(p,`09-choose-${from}-${locale}-${width}x${height}`)
   await p.locator('[data-wizard-temporary]').scrollIntoViewIfNeeded()
   await inView(p,'[data-wizard-temporary]',`09-temporary-row-${from}-${locale}-${width}x${height}`)
   await fits(p,`09-fit-scrolled-${from}-${locale}-${width}x${height}`)
   await p.locator('[data-wizard-way="tailscale"]').click();await p.waitForTimeout(100)
   await choosable(p,`09-choose-${from}-${locale}-${width}x${height}-tailscale`)
   // The arrow keys reach the second row and bring its card into view.
   await p.locator('[data-wizard-way="tailscale"]').press('ArrowRight');await p.waitForTimeout(100)
   await choosable(p,`09-choose-${from}-${locale}-${width}x${height}-netbird-key`)
   await p.close()
  }
 }
}
// The temporary address at the window sizes: its confirm shows the facts and
// Start without scrolling, and Start gives the address, its QR and the hint.
for(const [width,height] of [[960,600],[1280,800],[640,600]]){
 const p=await open('first','en',{width,height})
 await p.locator('[data-wizard-temporary-open]').click();await p.waitForTimeout(300)
 await fits(p,`50-temporary-confirm-fit-${width}x${height}`)
 await inView(p,'[data-setup-wizard] [data-temporary-facts]',`50-temporary-facts-${width}x${height}`)
 await inView(p,'[data-wizard-footer] [data-wizard-next]',`50-temporary-start-${width}x${height}`)
 await p.locator('[data-wizard-footer] [data-wizard-next]').click()
 await p.locator('[data-wizard-temporary-hint]').waitFor();await p.waitForTimeout(300)
 await inView(p,'[data-wizard-address]',`51-temporary-address-${width}x${height}`)
 await fits(p,`51-temporary-on-fit-${width}x${height}`)
 await p.close()
}
// The pictures for review, light and dark at 960x600: the Overview's first
// visit, Choose with the temporary row, the temporary confirm, the address
// it gave and the Overview while it runs.
for(const scheme of ['light','dark']){
 const size={width:960,height:600}
 {const p=await page('first','en',size,scheme);await p.locator('[data-home-set-up]').waitFor();await shot(p,`60-home-empty-${scheme}`);await p.close()}
 {
  const p=await open('first','en',size,'home',scheme)
  await shot(p,`61-wizard-choose-${scheme}`)
  await p.locator('[data-wizard-temporary-open]').click();await shot(p,`62-temporary-confirm-${scheme}`)
  await p.locator('[data-wizard-footer] [data-wizard-next]').click();await p.locator('[data-wizard-temporary-hint]').waitFor()
  await shot(p,`63-temporary-on-wizard-${scheme}`)
  await p.close()
 }
 {const p=await page('temporary','en',size,scheme);await p.locator('[data-lane-temporary]').waitFor();await shot(p,`64-temporary-on-overview-${scheme}`);await p.close()}
}
// DSM's wizard window at the window sizes: the header (the step's title and
// progress) and the footer stay put while the step scrolls, and the footer
// has no Close: the wizard closes from its header.
for(const [width,height] of [[960,600],[1280,800],[640,600]]){
 const p=await open('first','en',{width,height})
 const m=await p.evaluate(async()=>{
  const box=sel=>document.querySelector(sel)?.getBoundingClientRect()
  const body=document.querySelector('[data-setup-wizard] [data-wizard-body]')
  const before={header:box('[data-wizard-header]'),footer:box('[data-wizard-footer]')}
  body.scrollTop=body.scrollHeight
  await new Promise(r=>globalThis.requestAnimationFrame(()=>globalThis.requestAnimationFrame(r)))
  const after={header:box('[data-wizard-header]'),footer:box('[data-wizard-footer]')}
  const footer=document.querySelector('[data-wizard-footer]')
  const still=(a,b)=>!!a&&!!b&&Math.abs(a.top-b.top)<0.5&&Math.abs(a.bottom-b.bottom)<0.5
  return {scrolled:body.scrollTop>0,headerFixed:still(before.header,after.header),footerFixed:still(before.footer,after.footer),footerClose:footer.querySelectorAll('[data-dialog-close],[data-wizard-cancel]').length+[...footer.querySelectorAll('button')].filter(b=>b.textContent.trim()==='Close').length,headerClose:!!document.querySelector('[data-setup-wizard]').closest('[role="dialog"]').querySelector('header [data-dialog-close]')}
 })
 console.log(`65-fixed-${width}x${height}`,JSON.stringify(m))
 if(!m.scrolled||!m.headerFixed||!m.footerFixed||m.footerClose||!m.headerClose)process.exitCode=1
 await p.close()
}
// English and German (long words), light and dark, at 960x600 and 1280x800:
// Choose, and Connect for Tailscale while it waits for the sign-in. The
// wizard is a window over the app (the app shows around it), a mouse-opened
// wizard rings nothing, and the path on Connect's stage is three even boxes
// whose words keep clear of their borders.
async function windowed(p,name){
 const m=await p.evaluate(()=>{
  const r=document.querySelector('[data-setup-wizard]').closest('[role="dialog"]').getBoundingClientRect()
  const a=document.activeElement
  return {width:Math.round(r.width),height:Math.round(r.height),ring:!!a&&a!==document.body&&a.matches(':focus-visible')}
 })
 console.log(`72-window-${name}`,JSON.stringify(m))
 const [w,h]=[p.viewportSize().width,p.viewportSize().height]
 if(m.width>Math.min(900,w*0.78)||m.height>h-40||m.ring)process.exitCode=1
}
async function staged(p,name){
 const m=await p.evaluate(()=>{
  const topo=document.querySelector('[data-wizard-step][data-active] .ui-wizard-stage .ui-topology')
  const nodes=[...(topo?.querySelectorAll('.ui-topology-node')??[])].filter(n=>n.offsetParent)
  const inset=nodes.map(n=>{const b=n.getBoundingClientRect(),l=n.querySelector('.ui-topology-node-label').getBoundingClientRect();return Math.round(Math.min(l.left-b.left,b.right-l.right,l.top-b.top,b.bottom-l.bottom))})
  const widths=nodes.map(n=>Math.round(n.getBoundingClientRect().width))
  return {dense:topo?.hasAttribute('data-dense'),nodes:nodes.length,inset,widths}
 })
 console.log(`73-stage-${name}`,JSON.stringify(m))
 if(m.dense||m.nodes!==3||Math.min(...m.inset)<6||Math.max(...m.widths)-Math.min(...m.widths)>2)process.exitCode=1
}
for(const locale of ['en','de'])for(const [width,height] of [[960,600],[1280,800]])for(const scheme of ['light','dark']){
 const name=`${locale}-${scheme}-${width}x${height}`
 const p=await open('first',locale,{width,height},'home',scheme)
 await windowed(p,name)
 await shot(p,`70-step1-choose-${name}`)
 await use(p,'tailscale');await p.waitForTimeout(300)
 await staged(p,name)
 await shot(p,`71-step2-tailscale-${name}`)
 await p.close()
}
// Own domain is selected and the Free link card keyboard-focused, dark.
{
 const p=await open('first','en',{width:960,height:600},'home','dark')
 await p.locator('[data-wizard-way="workers"]').focus();await p.keyboard.press('ArrowRight')
 await p.waitForTimeout(200)
 await p.screenshot({path:`${out}/08-fit-960x600-dark.png`});console.log('08-fit-960x600-dark')
 await p.close()
}
// The returning Home: the lane words and the workers.dev name stay readable
// (no word is cut) in every language.
for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
 for(const width of [960,640]){
  const p=await browser.newPage({viewport:{width,height:600},deviceScaleFactor:2})
  await p.emulateMedia({reducedMotion:'reduce'})
  await p.goto(`${base}__wizard.html?s=relay&l=${locale}&sub=connect-4d053d09`)
  await p.locator('[data-home-add-way]').waitFor();await p.waitForTimeout(500)
  const cut=await p.evaluate(()=>[...document.querySelectorAll('[data-topology-text]')].filter(e=>e.offsetParent&&(e.scrollHeight>e.clientHeight+1||e.scrollWidth>e.clientWidth+1)).map(e=>e.textContent))
  if(cut.length){console.log('cut',locale,width,cut);process.exitCode=1}
  await p.screenshot({path:`${out}/40-home-lanes-${locale}-${width}.png`});console.log(`40-home-lanes-${locale}-${width}`)
  await p.close()
 }
}
{
 const p=await open('first')
 await use(p,'workers');await shot(p,'10-connect-free-link')
 await click(p,'[data-wizard-back]');await use(p,'cloudflare-domain');await shot(p,'11-connect-own-domain')
 await click(p,'[data-wizard-back]');await use(p,'tailscale');await shot(p,'12-connect-tailscale')
 await p.close()
}
// ?s=relay and ?s=ipv6: a way is on, so the Home is the returning view and Add a way opens the wizard.
{
 const p=await open('relay')
 await shot(p,'04-choose-free-link-on')
 await use(p,'workers');await shot(p,'20-apps')
 await click(p,'[data-wizard-footer] [data-wizard-next]');await p.locator('[data-apps-approval]').waitFor();await shot(p,'21-approval')
 await click(p,'[data-approve-confirm]');await p.locator('[data-wizard-address]').first().waitFor()
 await shot(p,'30-ready-free-link')
 await p.close()
}
{
 const p=await open('ipv6')
 await use(p,'ipv6')
 await shot(p,'31-ready-ipv6')
 await p.close()
}
await browser.close();await server.close()
