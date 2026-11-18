<script setup lang="ts">
// The Overview's Phone section, under the topology, in three columns with no
// lead text: Scan with your phone (a menu of every way with an address,
// public ones first, Cloudflare leading, and that way's code and link); the
// live phone demo, with a short "tap to try" label and three short points on
// what the page gives; and adding the page to the Home Screen: iPhone and
// Android, each in three small steps, the phone's own platform first, then
// the result they share.
import {computed,type Component} from 'vue'
import {Select,SettingsSection} from '@synopackageland/ui'
import {Activity,Compass,EllipsisVertical,Globe,LayoutList,Pointer,ShieldCheck,Share,SquarePlus} from '@lucide/vue'
import type {PhoneRoute} from '../../connect/phoneRoutes'
import {translate} from '../../i18n'
import PhonePanel from './PhonePanel.vue'
const props=defineProps<{locale:string;routes:PhoneRoute[];route?:PhoneRoute;open:boolean}>()
const emit=defineEmits<{pick:[link:string];'update:open':[open:boolean]}>()
const t=(key:string)=>translate(props.locale,key)
const routeName=(id:string)=>id==='quickconnect'?'QuickConnect':id==='ddns'?'DDNS':id==='ipv6'?t('connect.ipv6'):id==='workers'?t('home.way.workers'):id==='local'?t('phone.route.local'):t('ux.method.'+id)
const options=computed(()=>props.routes.map(r=>({value:r.link,label:`${routeName(r.id)} · ${new URL(r.origin).host}`})))
// What the page gives, as short labels under the phone that shows it
const points:{key:string;icon:Component}[]=[{key:'layout',icon:LayoutList},{key:'same',icon:ShieldCheck},{key:'live',icon:Activity}]
type Step={key:string;icon:Component}
const platforms:{key:'iphone'|'android';name:string;steps:Step[]}[]=[
 {key:'iphone',name:'iPhone',steps:[{key:'safari',icon:Compass},{key:'share',icon:Share},{key:'add',icon:SquarePlus}]},
 {key:'android',name:'Android',steps:[{key:'chrome',icon:Globe},{key:'menu',icon:EllipsisVertical},{key:'install',icon:SquarePlus}]},
]
// Both are always shown; an Android browser sees its own steps first.
const android=typeof navigator!=='undefined'&&/Android/i.test(navigator.userAgent)
const shown=android?[...platforms].reverse():platforms
/** A step's text, split around {menu}, where Chrome's ⋮ menu shows as its icon. */
const parts=(key:string)=>t(`phone.home.${key}`).split('{menu}')
</script>
<template>
 <SettingsSection :open="open" :title="t('phone.section.title')" data-home-phone @update:open="emit('update:open',$event)">
  <PhonePanel v-if="route" :route="route" :locale="locale">
   <template #scan>
    <strong class="phone-section__heading">{{t('phone.section.scan')}}</strong>
    <label class="phone-section__picker"><span>{{t('phone.section.way')}}</span><Select :model-value="route.link" :options="options" :aria-label="t('phone.section.way')" data-phone-way @update:model-value="emit('pick',$event)" /></label>
   </template>
   <template #phone>
    <span class="phone-section__try" data-phone-try><Pointer :size="14" aria-hidden="true" />{{t('phone.demo.try')}}</span>
    <ul class="phone-section__points">
     <li v-for="p in points" :key="p.key" :data-phone-point="p.key"><component :is="p.icon" :size="14" aria-hidden="true" /><span>{{t(`phone.point.${p.key}`)}}</span></li>
    </ul>
   </template>
   <div class="phone-section__home" data-phone-home-screen>
    <strong class="phone-section__heading">{{t('phone.home.title')}}</strong>
    <div v-for="p in shown" :key="p.key" class="phone-section__platform" :data-phone-platform="p.key">
     <strong>{{p.name}}</strong>
     <ol class="phone-section__steps">
      <li v-for="(s,i) in p.steps" :key="s.key" :data-phone-step="s.key"><span class="phone-section__step-icon"><component :is="s.icon" :size="16" aria-hidden="true" /></span><span>{{i+1}}. <template v-for="(part,j) in parts(s.key)" :key="j"><EllipsisVertical v-if="j" class="phone-section__menu" :size="14" :aria-label="t('phone.home.menuIcon')" role="img" />{{part}}</template></span></li>
     </ol>
    </div>
    <span class="phone-section__note" data-phone-result>{{t('phone.home.result')}}</span>
   </div>
  </PhonePanel>
 </SettingsSection>
</template>
<style scoped>
.phone-section__heading{font-size:14px;line-height:20px}
.phone-section__picker{display:flex;flex-direction:column;gap:var(--ui-space-1);min-width:0}
.phone-section__try{display:inline-flex;align-items:center;gap:var(--ui-space-1);color:var(--ui-text-secondary);font-size:12px}
.phone-section__points{list-style:none;margin:0;padding:0;display:flex;flex-wrap:wrap;justify-content:center;gap:var(--ui-space-1) var(--ui-space-4);max-width:320px}
.phone-section__points li{display:inline-flex;align-items:center;gap:var(--ui-space-1)}
.phone-section__points li svg{flex:none;color:var(--ui-text-secondary)}
.phone-section__home{display:flex;flex-direction:column;gap:var(--ui-space-3);min-width:0}
.phone-section__platform{display:flex;flex-direction:column;gap:var(--ui-space-1);min-width:0}
.phone-section__steps{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:var(--ui-space-1)}
.phone-section__menu{display:inline-block;position:relative;top:2px;margin:0 1px;padding:0 1px;border:1px solid var(--ui-border);border-radius:4px;background:var(--ui-surface)}
.phone-section__steps li{display:flex;align-items:flex-start;gap:var(--ui-space-2)}
.phone-section__steps li>span:last-child{padding-top:2px}
.phone-section__step-icon{display:inline-flex;align-items:center;justify-content:center;flex:none;width:24px;height:24px;border-radius:var(--ui-radius-sm);border:1px solid var(--ui-border);background:var(--ui-surface)}
.phone-section__note{color:var(--ui-text-secondary);font-size:12px}
</style>
