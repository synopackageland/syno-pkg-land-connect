import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {expect,test} from 'vitest'

const marker='spl_connect_login_ExampleMusic=1'
const target='/webman/3rdparty/ExampleMusic/sso/open.cgi?launchParam=fixture'
const key='spl_connect_fragment_ExampleMusic'
const source=readFileSync(new URL('../../backend/internal/apps/ingress_login.js',import.meta.url),'utf8').replace('%s',JSON.stringify(marker)).replace('%s',JSON.stringify(target)).replace('%s',JSON.stringify(key))
function browser(hash='',storage=new Map<string,string>(),initialCookie=''){
 let url=new URL('https://music.example.com/?launchApp=SYNO.SDS.ExampleMusic.Instance'+hash)
 let cookie=initialCookie
 const navigations:string[]=[],timers:Array<()=>void>=[]
 const location={get href(){return url.href},get origin(){return url.origin},get hash(){return url.hash},get pathname(){return url.pathname},get search(){return url.search},replace:(to:string)=>navigations.push(to)}
 class XHR {
  readyState=0;aborted=false;method='';url='';headers:Record<string,string>={};listeners:Array<(e:{stopImmediatePropagation:()=>void})=>void>=[]
  onreadystatechange:((e:unknown)=>void)|null=null
  open(method:string,url:string){this.method=method;this.url=url}
  send(){}
  addEventListener(_name:string,fn:(e:{stopImmediatePropagation:()=>void})=>void){this.listeners.push(fn)}
  getResponseHeader(name:string){return this.headers[name]??null}
  abort(){this.aborted=true}
  receive(complete:boolean){this.readyState=2;if(complete)this.headers['X-Connect-Login-Complete']='1';for(const fn of this.listeners)fn({stopImmediatePropagation(){}})}
 }
 const window={fetch:async()=>({headers:new Map<string,string>()})}
 const document={get cookie(){return cookie},set cookie(value:string){cookie=value.includes('Max-Age=0')?'':value}}
 vm.runInNewContext(source,{window,document,XMLHttpRequest:XHR,location,history:{state:null,replaceState:(_state:unknown,_title:string,to:string)=>{url=new URL(to,url)}},sessionStorage:{getItem:(k:string)=>storage.get(k)??null,setItem:(k:string,v:string)=>storage.set(k,v),removeItem:(k:string)=>storage.delete(k)},URL,setInterval:(fn:()=>void)=>{timers.push(fn);return 1},clearInterval(){}})
 return {location,navigations,XHR,storage,timers,document,window}
}

test('App fragments leave the DSM login router and return through the fixed SDK handoff',()=>{
 const b=browser('#/album/all')
 expect(b.location.hash).toBe('')
 expect(JSON.parse(b.storage.get(key)!)).toEqual({target,fragment:'#/album/all'})
 const xhr=new b.XHR();xhr.open('POST','/webapi/entry.cgi');xhr.send();xhr.receive(true)
 expect(xhr.aborted).toBe(true)
 expect(b.navigations).toEqual([target+'#/album/all'])
 expect(b.storage.size).toBe(0)
 expect(b.document.cookie).toBe('')
})

test('failed authentication and unrelated requests do not navigate',()=>{
 const b=browser('#/app')
 const failed=new b.XHR();failed.open('POST','/webapi/entry.cgi');failed.send();failed.receive(false)
 expect(failed.aborted).toBe(false)
 const other=new b.XHR();other.open('POST','/webapi/entry.cgi/SYNO.Core.Package');other.send();other.receive(true)
 expect(b.navigations).toEqual([])
 expect(b.storage.size).toBe(1)
})

test('a login page reload keeps the original App fragment rather than DSM router state',()=>{
 const first=browser('#/album/all')
 const reloaded=browser('#/login/password',first.storage)
 expect(reloaded.location.hash).toBe('')
 const xhr=new reloaded.XHR();xhr.open('POST','/webapi/auth.cgi');xhr.send();xhr.receive(true)
 expect(reloaded.navigations).toEqual([target+'#/album/all'])
})

test('a completed marker only navigates to the selected App and is consumed once',()=>{
 const b=browser('#/app',new Map(),marker)
 b.timers[0]!()
 b.timers[0]!()
 expect(b.navigations).toEqual([target+'#/app'])
 expect(b.document.cookie).toBe('')
})
