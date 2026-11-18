import {createStandaloneContext, type Context} from '@synopackageland/sdk'
// Development transport uses the same request/envelope contract as DSM ctx.call,
// on a page of its own outside DSM.
export function httpContext(endpoint:string):Context {
 return createStandaloneContext({displayName:'Syno Connect Center',locale:'zh-TW',async call<T>(method:string,params:Record<string,unknown>={}) {
  const res=await fetch(endpoint,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({method,params})})
  const body=await res.json()
  if(!body.success)throw body.error
  return body.data as T
 }})
}
