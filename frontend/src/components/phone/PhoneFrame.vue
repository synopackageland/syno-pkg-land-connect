<script setup lang="ts">
// An upright phone holding the phone browser page as a tiny live demo: real
// English 3x screenshots of the phone page (scripts/phone-preview.mjs, a
// neutral demo NAS), with buttons laid over the parts a person would tap.
// The tab bar switches Home, Apps and Notifications. On Home, the Security
// Advisor row, the Resources card and Storage open their screen, whose back
// arrow returns Home; on Notifications, the filters show one level each; on
// Apps, Storage Manager, File Station and Control Panel open their screen,
// whose Home (top left) returns to Home. Nothing else on the picture reacts.
// Home's picture is in the bundle, so the phone shows at once; every other
// screen is a file beside the App's module (public/phone/), fetched only when
// that screen is shown, so the Overview loads no more than before.
//
// A faint tap ripple suggests the next step: the Apps tab, then an App, so
// the picture reads as something to press. It plays only while the phone is
// on screen (a folded section or one scrolled away shows nothing), a few
// times with a pause between, and again each time the phone comes back into
// view. It retires for good (in this browser) once the person has tried the
// phone: any tap other than the suggested Apps tab, or the App after it. No
// ripple and no slide under prefers-reduced-motion.
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import home from '../../assets/phone/home.webp'
import spots from '../../assets/phone/spots.json'
import {translate} from '../../i18n'
const props=defineProps<{locale:string}>()
const t=(key:string)=>translate(props.locale,key)
type Spot={left:number;top:number;width:number;height:number}
type AppId='StorageManager'|'FileStation'|'ControlPanel'
type Page='security'|'resources'|'storage'
type Filter='all'|'errors'|'warnings'|'info'
type Notices='notices'|`notices-${Exclude<Filter,'all'>}`
type View='home'|'apps'|Page|Notices|AppId
type Tab='home'|'apps'|'notices'
const APPS:{id:AppId;name:string}[]=[
 {id:'StorageManager',name:'Storage Manager'},
 {id:'FileStation',name:'File Station'},
 {id:'ControlPanel',name:'Control Panel'},
]
// What Home opens, each with its back arrow (spot 'up')
const PAGES:{id:Page;label:()=>string}[]=[
 {id:'security',label:()=>t('phone.demo.open').replace('{app}','Security Advisor')},
 {id:'resources',label:()=>t('phone.demo.resources')},
 {id:'storage',label:()=>t('phone.demo.storage')},
]
const FILTERS:{id:Filter;view:Notices}[]=[{id:'all',view:'notices'},{id:'errors',view:'notices-errors'},{id:'warnings',view:'notices-warnings'},{id:'info',view:'notices-info'}]
// The built module sits beside phone/; the dev server serves public/ at its root.
// (A variable, so the bundler leaves this URL to the browser.)
const here=import.meta.url
const shelf=import.meta.env.DEV?'/phone/':new URL('phone/',here).href
const SCREENS:Record<View,string>=Object.fromEntries((['apps','security','resources','storage','notices','notices-errors','notices-warnings','notices-info','StorageManager','FileStation','ControlPanel'] as const).map(v=>[v,`${shelf}${APPS.some(a=>a.id===v)?'app-':''}${v}.webp`])) as Record<View,string>
SCREENS.home=home
// Once a pointer or focus reaches the phone, fetch the other screens so a tap shows its screen at once
let warmed=false
function warm(){ if(warmed)return; warmed=true; for(const src of Object.values(SCREENS)) if(src!==home){ const img=document.createElement('img'); img.decoding='async'; img.src=src } }
const SPOTS=spots as Record<'home'|'apps'|'notices'|'back'|'up'|AppId|Page|`notices-${Filter}`,Spot>
const view=ref<View>('home')
const screen=computed(()=>SCREENS[view.value])
const isPage=(v:View):v is Page=>PAGES.some(p=>p.id===v)
const isNotices=(v:View):v is Notices=>FILTERS.some(f=>f.view===v)
/** The tab a screen sits under; an App's screen has no tab bar. */
const tab=computed<Tab|undefined>(()=>view.value==='home'||isPage(view.value)?'home':view.value==='apps'?'apps':isNotices(view.value)?'notices':undefined)
const TABS:{id:Tab;view:View;label:string}[]=[{id:'home',view:'home',label:'phone.demo.home'},{id:'apps',view:'apps',label:'phone.demo.apps'},{id:'notices',view:'notices',label:'phone.demo.notices'}]
// The filters are short words; their buttons reach a little past them so a finger finds them
const place=(s:Spot,pad=0)=>({left:s.left-pad+'%',top:s.top-pad/2+'%',width:s.width+2*pad+'%',height:s.height+pad+'%'})
// Tabs cross-fade; an App slides in a little, as the phone page does
const motion=ref<'fade'|'push'>('fade')

type PhoneHint='apps'|'StorageManager'
const TRIED_KEY='synopkgconnect.phoneTried'
function triedBefore(){ try { return globalThis.localStorage?.getItem(TRIED_KEY)==='1' } catch { return false } }
function rememberTried(){ try { globalThis.localStorage?.setItem(TRIED_KEY,'1') } catch { /* storage off: the hint shows again next time */ } }
const reduced=()=>typeof window.matchMedia==='function'&&window.matchMedia('(prefers-reduced-motion: reduce)').matches
const hintStep=ref<PhoneHint>()
const pulsing=ref(false)
let timers:number[]=[]
const clear=()=>{ timers.forEach(id=>window.clearTimeout(id)); timers=[]; pulsing.value=false }
const later=(ms:number,fn:()=>void)=>{ timers.push(window.setTimeout(fn,ms)) }
// One play is two soft ripples; then a pause, three plays each time the phone is seen
const PLAY=2200, IDLE=3200, PLAYS=3
function playHint(step:PhoneHint,delay:number){
 clear(); hintStep.value=step
 for(let i=0;i<PLAYS;i++){
  later(delay+i*(PLAY+IDLE),()=>{ pulsing.value=true })
  later(delay+i*(PLAY+IDLE)+PLAY,()=>{ pulsing.value=false })
 }
}
function retire(){ if(hintStep.value===undefined)return; clear(); io?.disconnect(); hintStep.value=undefined; rememberTried() }
function go(next:View){
 const step=hintStep.value
 if(step==='apps'&&next==='apps') playHint('StorageManager',700)
 else retire()
 // A screen opened from a tab slides in; tabs, filters and the way back cross-fade
 motion.value=isPage(next)||APPS.some(a=>a.id===next)?'push':'fade'
 view.value=next
}
const root=ref<HTMLElement>()
let io:{disconnect():void}|undefined
/** Plays the pending step's ripple unless it is already playing. */
function show(){ if(hintStep.value&&!timers.length) playHint(hintStep.value,hintStep.value==='apps'?900:700) }
onMounted(()=>{
 if(triedBefore()||reduced())return
 hintStep.value='apps'
 const Observer=globalThis.IntersectionObserver
 if(typeof Observer!=='function'||!root.value){ show(); return }
 // Nearly the whole phone, so the tab bar the ripple points at is on screen too.
 const seen=new Observer(([e])=>{ if(e&&e.isIntersecting&&e.intersectionRatio>=.9)show(); else clear() },{threshold:[0,.9,1]})
 seen.observe(root.value); io=seen
})
onBeforeUnmount(()=>{ clear(); io?.disconnect() })
const hintSpot=computed(()=>hintStep.value&&pulsing.value&&(hintStep.value==='apps'?tab.value!==undefined:view.value==='apps')?SPOTS[hintStep.value]:undefined)
</script>
<template>
 <div ref="root" class="phone-frame" role="group" :aria-label="t('phone.demo.label')" data-phone-frame @pointerenter="warm" @focusin="warm">
  <div class="phone-frame__edge">
   <div class="phone-frame__body">
    <div class="phone-frame__screen">
     <div class="phone-frame__status" aria-hidden="true">
      <span class="phone-frame__time">9:41</span>
      <span class="phone-frame__island" />
      <svg class="phone-frame__icons" viewBox="0 0 34 10"><rect x="0" y="6" width="2" height="4" rx=".5" /><rect x="3" y="4.5" width="2" height="5.5" rx=".5" /><rect x="6" y="3" width="2" height="7" rx=".5" /><rect x="9" y="1.5" width="2" height="8.5" rx=".5" /><path d="M14 4.2a6.5 6.5 0 0 1 8.4 0l-1 1.1a5 5 0 0 0-6.4 0zM15.7 6.1a4 4 0 0 1 5 0l-1 1.1a2.5 2.5 0 0 0-3 0zM18.2 9.6l-1.2-1.3a1.7 1.7 0 0 1 2.4 0z" /><rect x="24.5" y="1.5" width="8" height="7" rx="2" fill="none" stroke="currentColor" stroke-width="1" opacity=".45" /><rect x="26" y="3" width="5" height="4" rx="1" /><rect x="33" y="3.8" width="1" height="2.4" rx=".5" opacity=".45" /></svg>
     </div>
     <div class="phone-frame__view" :data-phone-view="view">
      <Transition :name="'phone-'+motion">
       <img :key="view" class="phone-frame__shot" :src="screen" alt="" draggable="false">
      </Transition>
      <template v-if="tab">
       <button v-for="b in TABS" :key="b.id" type="button" class="phone-frame__spot" :data-phone-spot="b.id" :style="place(SPOTS[b.id])" :aria-label="t(b.label)" :aria-pressed="view===b.view" @click="go(b.view)" />
      </template>
      <template v-if="view==='home'">
       <button v-for="p in PAGES" :key="p.id" type="button" class="phone-frame__spot" :data-phone-spot="p.id" :style="place(SPOTS[p.id])" :aria-label="p.label()" @click="go(p.id)" />
      </template>
      <button v-else-if="isPage(view)" type="button" class="phone-frame__spot" data-phone-spot="up" :style="place(SPOTS.up)" :aria-label="t('phone.demo.back')" @click="go('home')" />
      <template v-else-if="isNotices(view)">
       <button v-for="f in FILTERS" :key="f.id" type="button" class="phone-frame__spot" :data-phone-spot="'notices-'+f.id" :style="place(SPOTS[`notices-${f.id}`],2)" :aria-label="t('phone.demo.filter.'+f.id)" :aria-pressed="view===f.view" @click="go(f.view)" />
      </template>
      <template v-else-if="view==='apps'">
       <button v-for="a in APPS" :key="a.id" type="button" class="phone-frame__spot" :data-phone-spot="a.id" :style="place(SPOTS[a.id])" :aria-label="t('phone.demo.open').replace('{app}',a.name)" @click="go(a.id)" />
      </template>
      <button v-else type="button" class="phone-frame__spot" data-phone-spot="back" :style="place(SPOTS.back)" :aria-label="t('phone.demo.back')" @click="go('home')" />
      <span v-if="hintSpot" class="phone-frame__hint" :style="place(hintSpot)" aria-hidden="true" data-phone-hint><span /></span>
     </div>
    </div>
   </div>
  </div>
 </div>
</template>
<style scoped>
/* About 19.5:9 with a thin even bezel, a graphite edge and a soft shadow.
   The frame stays dark in both themes, like a real phone. */
.phone-frame{--w:180px;--bezel:5px;--edge:1.5px;--r:calc(var(--w) * .2);flex:none;padding:8px 10px 18px;display:flex;justify-content:center}
.phone-frame__edge{position:relative;width:calc(var(--w) + 2 * (var(--bezel) + var(--edge)));padding:var(--edge);box-sizing:border-box;border-radius:calc(var(--r) + var(--bezel) + var(--edge));background:linear-gradient(155deg,#8b9098 0%,#3a3e45 18%,#1d2025 46%,#2c3036 74%,#767b83 100%);box-shadow:inset 0 0 0 .5px rgba(255,255,255,.28),0 1px 2px rgba(16,24,40,.08),0 10px 24px -8px rgba(16,24,40,.22),0 28px 56px -20px rgba(16,24,40,.30)}
.phone-frame__edge::before,.phone-frame__edge::after{content:"";position:absolute;width:2px;border-radius:1px;background:linear-gradient(90deg,#5b6068,#2a2d33)}
.phone-frame__edge::before{left:-2px;top:22%;height:9%}
.phone-frame__edge::after{right:-2px;top:27%;height:13%}
.phone-frame__body{padding:var(--bezel);border-radius:calc(var(--r) + var(--bezel));background:#0b0c0e}
.phone-frame__screen{position:relative;display:flex;flex-direction:column;width:var(--w);aspect-ratio:9/19.5;overflow:hidden;border-radius:var(--r);background:#fff;isolation:isolate}
.phone-frame__status{flex:1;position:relative;display:flex;align-items:center;justify-content:space-between;padding:0 calc(var(--w) * .09);color:#111;font:600 calc(var(--w) * .052)/1 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
.phone-frame__island{position:absolute;left:50%;top:50%;width:29%;height:calc(var(--w) * .082);transform:translate(-50%,-50%);border-radius:999px;background:#000}
.phone-frame__icons{width:calc(var(--w) * .17);fill:currentColor}
.phone-frame__view{position:relative;flex:none;width:100%;aspect-ratio:360/736;overflow:hidden}
.phone-frame__shot{position:absolute;inset:0;display:block;width:100%;height:100%;object-fit:cover;object-position:top;user-select:none;-webkit-user-drag:none}
.phone-frame__spot{position:absolute;z-index:1;margin:0;padding:0;border:0;border-radius:6px;background:transparent;cursor:pointer;-webkit-tap-highlight-color:transparent}
.phone-frame__spot:hover{background:rgba(10,102,255,.07);box-shadow:inset 0 0 0 1.5px rgba(10,102,255,.45)}
.phone-frame__spot:focus-visible{outline:none;box-shadow:inset 0 0 0 2px var(--ui-primary,#0a66ff);background:rgba(10,102,255,.07)}
/* The tap hint: a faint dot and one ring, in the accent color */
.phone-frame__hint{position:absolute;z-index:2;display:flex;align-items:center;justify-content:center;pointer-events:none}
.phone-frame__hint::before,.phone-frame__hint>span{content:"";position:absolute;width:calc(var(--w) * .13);aspect-ratio:1;border-radius:50%}
.phone-frame__hint::before{background:var(--ui-primary,#0a66ff);opacity:0;animation:phone-tap 1.1s ease-out 2}
.phone-frame__hint>span{border:1.5px solid var(--ui-primary,#0a66ff);opacity:0;animation:phone-ring 1.1s ease-out 2}
@keyframes phone-tap{0%{opacity:0;transform:scale(.7)}30%{opacity:.4;transform:scale(1)}100%{opacity:0;transform:scale(1)}}
@keyframes phone-ring{0%{opacity:0;transform:scale(1)}25%{opacity:.55}100%{opacity:0;transform:scale(1.9)}}
.phone-fade-enter-active,.phone-fade-leave-active{transition:opacity 160ms ease}
.phone-fade-enter-from,.phone-fade-leave-to{opacity:0}
.phone-push-enter-active{transition:opacity 180ms ease,transform 220ms cubic-bezier(.2,.8,.2,1)}
.phone-push-leave-active{transition:opacity 160ms ease}
.phone-push-enter-from{opacity:0;transform:translateX(12%)}
.phone-push-leave-to{opacity:0}
@media (prefers-reduced-motion:reduce){
 .phone-fade-enter-active,.phone-fade-leave-active,.phone-push-enter-active,.phone-push-leave-active{transition:none}
 .phone-frame__hint{display:none}
}
</style>
