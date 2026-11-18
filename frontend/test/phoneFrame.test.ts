// @vitest-environment jsdom
// The Phone panel's phone: a tiny live demo of the phone browser page. Its
// tab bar, what Home links to, the notice filters and three Apps really
// switch the screen; a faint tap hint shows the way until the phone has been
// tried, and never under reduced motion.
import {afterEach,beforeEach,expect,test,vi} from 'vitest'
import {createApp,nextTick} from 'vue'
import PhoneFrame from '../src/components/phone/PhoneFrame.vue'

const TRIED='synopkgconnect.phoneTried'
let unmount:()=>void=()=>{}
beforeEach(()=>{vi.useFakeTimers()})
afterEach(()=>{unmount();vi.useRealTimers();vi.unstubAllGlobals();vi.restoreAllMocks();document.body.innerHTML='';localStorage.clear()})

function mount(locale='en'){
 const el=document.createElement('div');document.body.append(el)
 const app=createApp(PhoneFrame,{locale});app.mount(el)
 unmount=()=>{app.unmount();unmount=()=>{}}
 const q=(s:string)=>el.querySelector<HTMLElement>(s)
 const spot=(id:string)=>q(`[data-phone-spot="${id}"]`)
 const spots=()=>[...el.querySelectorAll<HTMLButtonElement>('[data-phone-spot]')]
 return {el,q,spot,spots,
  view:()=>q('[data-phone-view]')!.dataset.phoneView,
  tap:async(id:string)=>{spot(id)!.click();await nextTick()},
  // Which spot the hint sits on after ms more, if any
  hint:async(ms:number)=>{
   vi.advanceTimersByTime(ms);await nextTick()
   const h=q('[data-phone-hint]');if(!h)return undefined
   const s=spots().find(b=>b.style.left===h.style.left&&b.style.top===h.style.top)
   return s?.dataset.phoneSpot
  }}
}

test('the tab bar switches Home, Apps and Notifications, and three Apps open and return Home',async()=>{
 const p=mount()
 expect(p.q('[data-phone-frame]')!.getAttribute('aria-label')).toBe('Phone page preview. Try its buttons')
 expect(p.view()).toBe('home')
 expect(p.spot('home')!.getAttribute('aria-pressed')).toBe('true')
 expect(p.spots().map(b=>b.dataset.phoneSpot)).toEqual(['home','apps','notices','security','resources','storage'])
 await p.tap('apps')
 expect(p.view()).toBe('apps')
 expect(p.spot('apps')!.getAttribute('aria-pressed')).toBe('true')
 expect(p.spots().map(b=>b.getAttribute('aria-label'))).toEqual(['Home','Apps','Notifications','Open Storage Manager','Open File Station','Open Control Panel'])
 for(const b of p.spots()){expect(b.tagName).toBe('BUTTON');expect(b.type).toBe('button');expect(b.style.left).toMatch(/%$/)}
 for(const id of ['StorageManager','FileStation','ControlPanel']){
  await p.tap('apps');await p.tap(id)
  expect(p.view()).toBe(id)
  expect(p.spots().map(b=>b.dataset.phoneSpot)).toEqual(['back'])
  expect(p.spot('back')!.getAttribute('aria-label')).toBe('Back to Home')
  await p.tap('back')
  expect(p.view()).toBe('home')
 }
})

test('Security Advisor, Resources and Storage open from Home with a back arrow; the tab bar stays',async()=>{
 const p=mount()
 // The entering picture is the last; the one leaving may still be there
 const shot=()=>[...p.el.querySelectorAll('[data-phone-view] img')].at(-1)!.getAttribute('src')
 const home=shot()
 expect(p.spot('security')!.getAttribute('aria-label')).toBe('Open Security Advisor')
 for(const id of ['security','resources','storage']){
  await p.tap(id)
  expect(p.view()).toBe(id)
  expect(shot()).not.toBe(home)
  // Not in the bundle: a file beside it, fetched when shown
  expect(shot()).toBe(`/phone/${id}.webp`)
  expect(p.spots().map(b=>b.dataset.phoneSpot)).toEqual(['home','apps','notices','up'])
  expect(p.spot('home')!.getAttribute('aria-pressed')).toBe('false')
  expect(p.spot('up')!.getAttribute('aria-label')).toBe('Back to Home')
  await p.tap('up')
  expect(p.view()).toBe('home')
 }
 await p.tap('resources');await p.tap('notices')
 expect(p.view()).toBe('notices')
})

test('the bell opens Notifications, whose filters show one level each',async()=>{
 const p=mount()
 await p.tap('notices')
 expect(p.view()).toBe('notices')
 expect(p.spot('notices')!.getAttribute('aria-pressed')).toBe('true')
 expect(p.spots().map(b=>b.getAttribute('aria-label'))).toEqual(['Home','Apps','Notifications','All notifications','Errors only','Warnings only','Info only'])
 expect(p.spot('notices-all')!.getAttribute('aria-pressed')).toBe('true')
 for(const level of ['errors','warnings','info']){
  await p.tap('notices-'+level)
  expect(p.view()).toBe('notices-'+level)
  expect(p.spot('notices-'+level)!.getAttribute('aria-pressed')).toBe('true')
  expect(p.spot('notices')!.getAttribute('aria-pressed')).toBe('false')
 }
 await p.tap('notices-all')
 expect(p.view()).toBe('notices')
 await p.tap('home')
 expect(p.view()).toBe('home')
})

test('the labels follow the UI language',async()=>{
 const p=mount('zh-TW')
 expect(p.q('[data-phone-frame]')!.getAttribute('aria-label')).toBe('手機頁面預覽，可以點按試用')
 await p.tap('apps')
 expect(p.spot('FileStation')!.getAttribute('aria-label')).toBe('開啟 File Station')
 await p.tap('notices')
 expect(p.spot('notices-errors')!.getAttribute('aria-label')).toBe('只看錯誤')
})

test('the tap hint points at Apps, then an App, and retires for good after the phone is tried',async()=>{
 let p=mount()
 expect(await p.hint(0)).toBeUndefined()
 expect(await p.hint(1000)).toBe('apps')
 expect(await p.hint(2500)).toBeUndefined()
 expect(await p.hint(3200)).toBe('apps')
 await p.tap('apps')
 expect(localStorage.getItem(TRIED)).toBeNull()
 expect(await p.hint(800)).toBe('StorageManager')
 await p.tap('FileStation')
 expect(localStorage.getItem(TRIED)).toBe('1')
 expect(await p.hint(20000)).toBeUndefined()
 unmount();document.body.innerHTML=''
 p=mount()
 expect(await p.hint(1000)).toBeUndefined()
})

test('any other first tap retires the hint at once',async()=>{
 const p=mount()
 expect(await p.hint(1000)).toBe('apps')
 await p.tap('home')
 expect(localStorage.getItem(TRIED)).toBe('1')
 expect(await p.hint(20000)).toBeUndefined()
})

test('no hint under prefers-reduced-motion, and the screens still switch',async()=>{
 vi.stubGlobal('matchMedia',(q:string)=>({matches:q.includes('reduce'),media:q,addEventListener(){},removeEventListener(){}}))
 const p=mount()
 expect(await p.hint(1000)).toBeUndefined()
 expect(await p.hint(6000)).toBeUndefined()
 await p.tap('apps');await p.tap('ControlPanel')
 expect(p.view()).toBe('ControlPanel')
})

test('storage that throws only means the hint may show again',async()=>{
 vi.spyOn(Storage.prototype,'getItem').mockImplementation(()=>{throw new Error('blocked')})
 vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw new Error('blocked')})
 const p=mount()
 expect(await p.hint(1000)).toBe('apps')
 await p.tap('home')
 expect(await p.hint(20000)).toBeUndefined()
 expect(p.view()).toBe('home')
})
