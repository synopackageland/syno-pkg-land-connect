// @vitest-environment jsdom
import {spawn, execFileSync, type ChildProcess} from 'node:child_process'
import {mkdtemp, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {afterAll,afterEach,beforeAll,expect,test,vi} from 'vitest'
import {nextTick} from 'vue'
import {mount} from '../src/main'
import {httpContext} from '../src/transport'
afterEach(()=>vi.restoreAllMocks())
// Node's fetch sends none of the headers by which a browser names the page
// that posts; the CGI refuses a post without them, as the DSM window's would be.
const nodeFetch=globalThis.fetch
globalThis.fetch=(input,init={})=>nodeFetch(input,{...init,headers:{...init.headers as Record<string,string>,'Sec-Fetch-Site':'same-origin'}})
let server:ChildProcess,endpoint:string,dir:string
beforeAll(async()=>{
 dir=await mkdtemp(join(tmpdir(),'connect-e2e-'))
 // CONNECT_DEV_FIXTURE: a fixture built elsewhere (scripts/frontend-check.sh).
 const fixture=process.env.CONNECT_DEV_FIXTURE||join(dir,'fixture')
 if(!process.env.CONNECT_DEV_FIXTURE)execFileSync('go',['build','-o',fixture,'./cmd/dev'],{cwd:'../backend'})
 server=spawn(fixture,['-data',dir,'-connections-fixture'])
 endpoint=await new Promise<string>((resolve,reject)=>{
  const timeout=setTimeout(()=>reject(new Error('fixture startup timeout')),20000)
  server.stdout!.once('data',data=>{clearTimeout(timeout);resolve(String(data).trim())})
  server.once('error',reject);server.stderr!.on('data',data=>{console.error(String(data))})
 })
},25000)
afterAll(async()=>{globalThis.fetch=nodeFetch;if(server && server.exitCode===null){const closed=new Promise(resolve=>server.once('exit',resolve));server.kill();await closed}if(dir)await rm(dir,{recursive:true,force:true})})
const stepTimeout=5000
async function until(ok:()=>boolean,what:string){const limit=Date.now()+stepTimeout;while(!ok()&&Date.now()<limit){await new Promise(r=>setTimeout(r,20));await nextTick()}expect(ok(),what).toBe(true)}
async function wait(root:HTMLElement,text:string){await until(()=>!!root.textContent?.includes(text),text)}
function click(root:ParentNode,label:string){const b=[...root.querySelectorAll('button')].find(b=>b.textContent?.trim()===label);expect(b,label).toBeTruthy();b!.click()}
test('Vue -> actual Go handler connects Tailscale, preserves it through reopen and disables only that connection',async()=>{
 vi.spyOn(window,'open').mockReturnValue(null)
 const root=document.createElement('div');document.body.append(root);const ctx=httpContext(endpoint);ctx.locale='en'
 let app=mount(root,ctx)
 try{
 await wait(root,'Compare all ways')
 click(root,'Tailscale');await until(()=>!(root.querySelector('[data-refresh]') as HTMLButtonElement)?.disabled,'initial refresh finished')
 root.querySelector<HTMLButtonElement>('[data-page="tailscale"] [data-ts-connect]')!.click()
 await until(()=>!!root.querySelector('[data-page="tailscale"] [data-connection-state="connected"]'),'connected')
 app.unmount();app=mount(root,ctx)
 await until(()=>!!root.querySelector('[data-lane-id="tailscale"]')?.textContent?.includes('Running on the NAS'),'reopened active connection')
 click(root,'Tailscale');await until(()=>!(root.querySelector('[data-page="tailscale"] [data-stop]') as HTMLButtonElement)?.disabled,'disable ready')
 root.querySelector<HTMLElement>('[data-page="tailscale"] [data-stop]')!.click()
 await until(()=>!!root.querySelector('[data-page="tailscale"] [data-connection-state="disconnected"]'),'disabled')
 }finally{app.unmount();root.remove()}
// Six independently bounded waits plus rendering/cleanup must fit within
// the test budget even when the other frontend files share the build host.
},7*stepTimeout)
test('actual transport denies anonymous and ordinary-user settings',async()=>{
 for(const [role,code] of [['anonymous','session_required'],['user','admin_required']]){
  const r=await fetch(endpoint,{method:'POST',headers:{'Content-Type':'application/json','X-Fixture-Role':role},body:JSON.stringify({method:'connection.connect',params:{provider:'fake',token:'demo-connect'}})})
  expect((await r.json()).error.code).toBe(code)
 }
})
