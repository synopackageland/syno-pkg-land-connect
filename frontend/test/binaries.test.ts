// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {readFileSync} from 'node:fs'
import {createApp,nextTick} from 'vue'
import BinarySettings from '../src/BinarySettings.vue'
import {createStandaloneContext} from '@synopackageland/sdk'
import en from '../../i18n/messages/en.json'
async function settle(){for(let i=0;i<20;i++){await Promise.resolve();await nextTick()}}
test('shows candidate versions and sends one update without activation',async()=>{
 const call=vi.fn(async(_method:string,p?:Record<string,unknown>)=>({product:p?.product,current:'1.0.0',latest:'1.1.0',previous:'',updateAvailable:true,phase:'ready',verification:'distsign',checkedAt:'2026-10-04T00:00:00Z',error:''}))
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 expect(root.textContent).toContain('1.0.0');expect(root.textContent).toContain('1.1.0');expect(root.textContent).toContain(en['binaries.verified.distsign'])
 const b=[...root.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Update to 1.1.0')!;b.click();b.click();await settle()
 expect(call.mock.calls.filter(c=>c[0]==='binaries.update')).toHaveLength(1)
 expect(call.mock.calls.some(c=>c[0].includes('connect'))).toBe(false)
 app.unmount();root.remove()
})
test('keeps actions disabled while daemon job runs and polls to completion',async()=>{
 vi.useFakeTimers()
 let running=false
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{if(method==='binaries.update')running=true;return {product:p?.product,current:running?'':'1.0.0',latest:'1.1.0',updateAvailable:!running,busy:running,phase:running?'downloading':'ready'}})
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root)
 try{await settle();const card=root.querySelector('[aria-label=cloudflared]')!;const check=[...card.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Check updates')!;[...card.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Update to 1.1.0')!.click();await settle();expect(check.disabled).toBe(true);running=false;await vi.advanceTimersByTimeAsync(1600);await settle();expect(check.disabled).toBe(false);expect(root.textContent).toContain('1.0.0')}
 finally{app.unmount();root.remove();vi.useRealTimers()}
})
test('an idle connector says nothing about tasks',async()=>{
 const call=vi.fn(async(_m:string,p?:Record<string,unknown>)=>({product:p?.product,current:'1.0.0',phase:'idle'}))
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 expect(root.textContent).not.toContain('No task is running')
 app.unmount();root.remove()
})
test('long version strings are plain KeyValueRow content that the row itself wraps',async()=>{
 const call=vi.fn(async(_m:string,p?:Record<string,unknown>)=>({product:p?.product,current:'1.0.0',latest:'2026.9.3-synopkgconnect-sealed',phase:'ready'}))
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 const value=[...root.querySelectorAll('dd')].find(d=>d.textContent?.trim()==='2026.9.3-synopkgconnect-sealed')!
 expect(value).toBeTruthy()
 expect(value.querySelector('.connect-value')).toBeNull()
 expect(readFileSync('src/style.css','utf8')).not.toContain('connect-value')
 app.unmount();root.remove()
})
test('rollback is offered only with a previous version and a vanished one gets its own message',async()=>{
 let previous='1.0.0'
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{if(method==='binaries.rollback'){previous='';throw {code:'binary_no_previous'}};return {product:p?.product,current:'1.1.0',latest:'1.1.0',previous:p?.product==='tailscale'?previous:'',updateAvailable:false,phase:'ready',verification:'distsign',checkedAt:'',error:''}})
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 const rollbacks=()=>root.querySelectorAll<HTMLElement>('[data-binary-rollback]')
 expect(rollbacks()).toHaveLength(1)
 // A ghost button reads as plain text; roll back looks like its neighbour Check updates.
 const check=[...rollbacks()[0]!.closest('[aria-label]')!.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Check updates')!
 expect(rollbacks()[0]!.className).toBe(check.className)
 rollbacks()[0]!.click();await settle()
 expect(root.querySelector('[role=alert]')?.textContent).toBe(en['binaries.noPrevious'])
 expect(rollbacks()).toHaveLength(0)
 app.unmount();root.remove()
})
test('a running job disables only its own card and polls only that product',async()=>{
 vi.useFakeTimers()
 let running=false
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{if(method==='binaries.update')running=true;const busy=running&&p?.product==='cloudflared';return {product:p?.product,current:'1.0.0',latest:'1.1.0',updateAvailable:true,busy,phase:busy?'downloading':'ready'}})
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root)
 try{
  await settle()
  const card=(product:string)=>root.querySelector(`[aria-label=${product}]`)!
  const button=(product:string,label:string)=>[...card(product).querySelectorAll('button')].find(b=>b.textContent?.trim()===label)!
  button('cloudflared','Update to 1.1.0').click();await settle()
  expect(button('cloudflared','Check updates').disabled).toBe(true)
  expect(button('tailscale','Check updates').disabled).toBe(false)
  expect(card('tailscale').getAttribute('aria-busy')).not.toBe('true')
  call.mockClear();await vi.advanceTimersByTimeAsync(1600);await settle()
  expect(call.mock.calls.map(c=>c[1]?.product)).toEqual(['cloudflared'])
  button('tailscale','Check updates').click();await settle()
  expect(call.mock.calls.some(c=>c[0]==='binaries.check'&&c[1]?.product==='tailscale')).toBe(true)
 }finally{app.unmount();root.remove();vi.useRealTimers()}
})
test('an action error stays in the card it came from',async()=>{
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{if(method==='binaries.check'&&p?.product==='netbird')throw {code:'download_failed'};return {product:p?.product,current:'1.0.0',latest:'1.0.0',phase:'ready'}})
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 const card=(product:string)=>root.querySelector<HTMLElement>(`[aria-label=${product}]`)!
 ;[...card('netbird').querySelectorAll('button')].find(b=>b.textContent?.trim()==='Check updates')!.click();await settle()
 expect(card('netbird').querySelector('[role=alert]')?.textContent).toBe(en['binaries.error'])
 expect(card('tailscale').querySelector('[role=alert]')).toBeNull()
 expect(root.querySelectorAll('[role=alert]')).toHaveLength(1)
 app.unmount();root.remove()
})
test('each card loads on its own until its first status arrives',async()=>{
 let release:()=>void=()=>{}
 const call=vi.fn(async(_m:string,p?:Record<string,unknown>)=>{if(p?.product==='tailscale')await new Promise<void>(r=>{release=r});return {product:p?.product,current:'1.0.0',phase:'idle'}})
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 const card=(product:string)=>root.querySelector<HTMLElement>(`[aria-label=${product}]`)!
 expect(card('cloudflared').querySelector('[aria-busy=true],[data-section-skeleton],[role=progressbar]')).toBeNull()
 expect(card('tailscale').textContent).not.toContain('In use 1.0.0')
 expect(card('netbird').textContent).toContain('In use 1.0.0')
 release();await settle()
 expect(card('tailscale').textContent).toContain('In use 1.0.0')
 app.unmount();root.remove()
})
// GitHub's unauthenticated quota (60 an hour) is not a network fault.
test.each([
 ['github_rate_limited','2026-10-14T17:46:40Z','binaries.rateLimited'],
 ['github_rate_limited','','binaries.rateLimitedLater'],
 ['github_refused','','binaries.refused'],
])('a %s check failure (reset %s) says so instead of "check the network"',async(errorCode,retryAt,key)=>{
 const call=vi.fn(async(_m:string,p?:Record<string,unknown>)=>({product:p?.product,current:'1.0.0',latest:'',phase:'failed',error:'release feed: '+errorCode,errorCode,retryAt}))
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root);await settle()
 const alert=root.querySelector('[aria-label=netbird] [role=alert]')?.textContent??''
 const time=retryAt?new Intl.DateTimeFormat('en',{dateStyle:'medium',timeStyle:'short'}).format(new Date(retryAt)):''
 expect(alert).toBe((en as Record<string,string>)[key]!.replace('{time}',time))
 expect(alert).not.toBe(en['binaries.error'])
 app.unmount();root.remove()
})
test.each(['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])('%s names the GitHub limits in its own words',async locale=>{
 const messages=(await import(`../../i18n/messages/${locale}.json`)).default as Record<string,string>
 for(const key of ['binaries.rateLimited','binaries.rateLimitedLater','binaries.refused']){
  expect(messages[key],`${locale} ${key}`).toBeTruthy()
  if(locale!=='en')expect(messages[key]).not.toBe((en as Record<string,string>)[key])
 }
 expect(messages['binaries.rateLimited']).toContain('{time}')
})

test.each(['cloudflared','tailscale','netbird','netbird-server'])('%s poll failures never mix a download spinner with an error and converge without refresh',async product=>{
 vi.useFakeTimers()
 let phase='ready',pollFails=false
 const reason='ghcr.io returned HTTP 429 (rate limited); retry later'
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{
  const own=p?.product===product
  if(own&&method==='binaries.update'){phase='downloading';pollFails=true}
  if(own&&method==='binaries.status'&&pollFails)throw new Error('status temporarily unavailable')
  return {product:p?.product,current:'',latest:'0.82.0',busy:own&&phase==='downloading',phase:own?phase:'ready',error:own&&phase==='failed'?reason:''}
 })
 const root=document.createElement('div');document.body.append(root)
 const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root)
 try{
  await settle();const card=root.querySelector(`[aria-label="${product}"]`)!
  ;[...card.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Download')!.click();await settle()
  expect(card.querySelector('[role=alert]')).toBeNull()
  expect(card.querySelector('button[data-loading=true] svg')).toBeTruthy()
  expect(root.querySelector(`[aria-label="${product==='tailscale'?'netbird':'tailscale'}"] button:disabled`)).toBeNull()
  pollFails=false;phase='failed';await vi.advanceTimersByTimeAsync(1600);await settle()
  expect(card.querySelector('button[data-loading=true]')).toBeNull()
  expect(card.querySelector('[role=alert]')?.textContent).toBe(reason)
  expect([...card.querySelectorAll('button')].some(b=>b.disabled)).toBe(false)
  phase='ready';[...card.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Download')!.click();await settle()
  expect(call.mock.calls.filter(c=>c[0]==='binaries.update'&&c[1]?.product===product)).toHaveLength(2)
 }finally{app.unmount();root.remove();vi.useRealTimers()}
})

test('persistent status loss releases the stale spinner, retries reads and recovers automatically',async()=>{
 vi.useFakeTimers()
 let working=false,unreachable=false
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{
  if(p?.product==='netbird-server'){
   if(method==='binaries.update'){working=true;unreachable=true;return {product:p.product,busy:true,phase:'downloading'}}
   if(unreachable)throw new Error('offline')
  }
  return {product:p?.product,current:working?'0.82.0':'',busy:false,phase:'ready'}
 })
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root)
 try{
  await settle();const card=root.querySelector('[aria-label="netbird-server"]')!
  ;[...card.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Download')!.click();await settle()
  await vi.advanceTimersByTimeAsync(3200);await settle()
  expect(card.querySelector('button[data-loading=true]')).toBeNull()
  expect(card.querySelector('[role=alert]')?.textContent).toBe(en['binaries.error'])
  expect([...card.querySelectorAll('button')].some(b=>b.disabled)).toBe(false)
  unreachable=false;await vi.advanceTimersByTimeAsync(1600);await settle()
  expect(card.querySelector('[role=alert]')).toBeNull()
  expect(card.textContent).toContain('0.82.0')
 }finally{app.unmount();root.remove();vi.useRealTimers()}
})

test('a lost action reply gives way to the real terminal job error',async()=>{
 let failed=false
 const reason='checksum verification failed'
 const call=vi.fn(async(method:string,p?:Record<string,unknown>)=>{
  if(method==='binaries.update'){failed=true;throw {code:'binary_failed',message:'binary operation failed; inspect status'}}
  return {product:p?.product,current:'',latest:'0.82.0',busy:false,phase:failed?'failed':'ready',error:failed?reason:''}
 })
 const root=document.createElement('div');document.body.append(root);const app=createApp(BinarySettings,{ctx:createStandaloneContext({call})});app.mount(root)
 try{
  await settle();const card=root.querySelector('[aria-label="netbird-server"]')!
  ;[...card.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Download')!.click();await settle()
  expect(card.querySelector('[role=alert]')?.textContent).toBe(reason)
  expect([...card.querySelectorAll('button')].some(b=>b.disabled)).toBe(false)
 }finally{app.unmount();root.remove()}
})
