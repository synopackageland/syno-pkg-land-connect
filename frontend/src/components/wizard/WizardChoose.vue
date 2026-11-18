<script setup lang="ts">
// Step 1: every way as a card, each with its picture, one line and the same
// five facts: Free link, My own domain and Tailscale, then NetBird and IPv6.
// The cards are one radio group (WizardWay): a click anywhere on a card, or
// the arrow keys (which cycle through all five), select it, and the wizard's
// own footer button goes on with the selected way. IPv6 asks for its risk to
// be acknowledged right under the cards before the footer button goes on.
// Three cards sit side by side from 615px of step width (DSM's default
// 960x640 window has about 680px), the last two on a second row; each path
// keeps its node words on one line so the paths and the line under them
// start level across all cards. Narrower, the cards stack in one column,
// where each is wide and short. The step body scrolls when the cards do not
// fit; a card selected with the keys is focused and so scrolled into view.
// Compare all ways is a footer action of the wizard. Under all the cards,
// while Cloudflare is free for it, one quiet row offers the temporary
// address: no card, badge or colour, since it is for a quick try (the address
// changes on every restart), one scroll down in DSM's default window.
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {Button,Checkbox,Space} from '@synopackageland/ui'
import {Timer} from '@lucide/vue'
import {CARD_WAYS,type WayState,type WizardWay} from '../../connect/wizard'
import {translate} from '../../i18n'
import WizardWayCard from './WizardWay.vue'
const props=defineProps<{locale:string;states:Record<WizardWay,WayState>;selected:WizardWay;temporary?:boolean}>()
const emit=defineEmits<{select:[way:WizardWay];temporary:[]}>()
const t=(key:string)=>translate(props.locale,'wizard.'+key)
const ipv6Ack=ref(false)
// The selected way cannot go on yet: IPv6 waits for its acknowledgement.
const blocked=computed(()=>props.selected==='ipv6'&&props.states.ipv6!=='on'&&!ipv6Ack.value)
defineExpose({blocked})
// Roving focus: the selected card is the group's one tab stop, or the first
// card while the temporary address is selected.
const tabbable=(way:WizardWay)=>way===props.selected||(!(CARD_WAYS as readonly WizardWay[]).includes(props.selected)&&way===CARD_WAYS[0])
const root=ref<InstanceType<typeof Space>>()
function move(way:WizardWay,step:number){
 const next=CARD_WAYS[(CARD_WAYS.indexOf(way as typeof CARD_WAYS[number])+step+CARD_WAYS.length)%CARD_WAYS.length]!
 emit('select',next)
 ;(root.value?.$el as HTMLElement|undefined)?.querySelector<HTMLElement>(`[data-wizard-way="${next}"]`)?.focus()
}
// A long name that wraps in one card keeps the paths level: every title row
// takes the tallest one's height, across both rows of cards.
function level(){
 const el=root.value?.$el as HTMLElement|undefined
 const heads=Array.from(el?.querySelectorAll<HTMLElement>('[data-wizard-head]')??[])
 heads.forEach(h=>{h.style.minHeight=''})
 const tallest=Math.max(...heads.map(h=>h.offsetHeight))
 heads.forEach(h=>{h.style.minHeight=`${tallest}px`})
}
let observer:InstanceType<typeof window.ResizeObserver>|undefined
onMounted(()=>{level();if(window.ResizeObserver&&root.value){observer=new window.ResizeObserver(level);observer.observe(root.value.$el)}})
onUnmounted(()=>observer?.disconnect())
watch(()=>[props.locale,props.states],()=>nextTick(level))
</script>
<template>
 <Space ref="root" direction="column" spacing="regular" class="wizard-choose" data-wizard-choose>
  <div class="wizard-ways" role="radiogroup" :aria-label="t('step.choose')">
   <WizardWayCard v-for="way in CARD_WAYS" :key="way" :way="way" :state="states[way]" :locale="locale" :checked="selected===way" :tabbable="tabbable(way)" @select="emit('select',way)" @move="move(way,$event)" />
  </div>
  <Checkbox v-if="selected==='ipv6'&&states.ipv6!=='on'" v-model="ipv6Ack" :label="t('ipv6Ack')" data-wizard-ipv6-ack />
  <div v-if="temporary" class="wizard-temporary" data-wizard-temporary>
   <Timer :size="16" aria-hidden="true" />
   <span>{{t('temporary.row')}}</span>
   <Button data-wizard-temporary-open @click="emit('temporary')">{{t(states.temporary==='on'?'temporary.show':'temporary.get')}}</Button>
  </div>
 </Space>
</template>
<style scoped>
.wizard-choose{container-type:inline-size}
.wizard-temporary{display:flex;flex-wrap:wrap;align-items:center;gap:4px 8px;color:var(--ui-text-secondary)}
.wizard-temporary > span{flex:0 1 auto;min-width:0}
.wizard-ways{display:grid;grid-template-columns:minmax(0,1fr);gap:var(--ui-space-2)}
@container (min-width:615px){.wizard-ways{grid-template-columns:repeat(3,minmax(0,1fr))}}
</style>
