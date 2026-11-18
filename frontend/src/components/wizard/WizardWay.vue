<script setup lang="ts">
// One way on Choose, as a radio card: the whole card selects it. Its title
// row carries the radio mark, the name and "Recommended" or "Already set up";
// when the row cannot hold the badge's words (an unseen copy of the row is
// measured), only its icon stays, with the words as its tooltip. WizardChoose
// gives every title row of a group the tallest one's height, so the paths
// below stay level when a long name wraps. The way's one line shows from 320px
// of card width; a narrower card (three side by side in the wizard dialog)
// keeps it as the path's tooltip and the card's description, so the card and
// the temporary address row under it show whole. Each fact is one line: its icon (the
// fact's name as a tooltip over the icon, which never covers the fact's
// words, and for screen readers), a coloured dot and the words, which wrap. A card 440px or wider puts the path beside its words.
import {computed,nextTick,onMounted,onUnmounted,ref,useId,watch,type Component} from 'vue'
import {Card,StatusDot,Tooltip} from '@synopackageland/ui'
import {CircleCheck,Clapperboard,Download,ShieldCheck,Star,Users,Wallet} from '@lucide/vue'
import {FACTS,FACT_TONES,PHASE,type CardWay,type Fact,type WayState} from '../../connect/wizard'
import {translate} from '../../i18n'
import WayPath from '../WayPath.vue'
const props=defineProps<{way:CardWay;state:WayState;locale:string;checked:boolean;tabbable:boolean}>()
const emit=defineEmits<{select:[];move:[step:number]}>()
const t=(key:string)=>translate(props.locale,'wizard.'+key)
const id=useId()
const FACT_ICONS:Record<Fact,Component>={cost:Wallet,who:Users,install:Download,video:Clapperboard,safety:ShieldCheck}
const badge=computed(()=>props.state==='on'?{key:'already',icon:CircleCheck}:props.way==='workers'?{key:'recommended',icon:Star}:undefined)
const label=computed(()=>[t('way.'+props.way),badge.value&&t(badge.value.key)].filter(Boolean).join(', '))
const head=ref<HTMLElement>(),probe=ref<HTMLElement>(),compact=ref(false)
const fit=()=>{if(head.value&&probe.value)compact.value=probe.value.offsetWidth>head.value.clientWidth}
let observer:InstanceType<typeof window.ResizeObserver>|undefined
onMounted(()=>{fit();if(window.ResizeObserver&&head.value){observer=new window.ResizeObserver(fit);observer.observe(head.value)}})
onUnmounted(()=>observer?.disconnect())
watch(()=>[props.locale,props.state],()=>nextTick(fit))
</script>
<template>
 <Card
  :has-header="false"
  layout-type="compact"
  role="radio"
  :aria-checked="checked"
  :aria-label="label"
  :aria-describedby="`${id}-line ${id}-facts`"
  :tabindex="tabbable?0:-1"
  :data-wizard-way="way"
  class="wizard-way"
  @click="emit('select')"
  @keydown.self.right.prevent="emit('move',1)"
  @keydown.self.down.prevent="emit('move',1)"
  @keydown.self.left.prevent="emit('move',-1)"
  @keydown.self.up.prevent="emit('move',-1)"
  @keydown.self.space.prevent="emit('select')"
  @keydown.self.enter.prevent="emit('select')"
 >
  <div class="wizard-body">
   <div ref="head" class="wizard-head" data-wizard-head>
    <span class="wizard-mark" aria-hidden="true" />
    <h3 class="wizard-title">{{t('way.'+way)}}</h3>
    <span v-if="badge" class="wizard-badge" :data-tone="badge.key" :data-compact="compact||undefined" :title="compact?t(badge.key):undefined" :data-wizard-recommended="badge.key==='recommended'||undefined" :data-wizard-already="badge.key==='already'||undefined"><component :is="badge.icon" :size="14" aria-hidden="true" /><span v-if="!compact">{{t(badge.key)}}</span></span>
    <span v-if="badge" ref="probe" class="wizard-head wizard-probe" aria-hidden="true"><span class="wizard-mark" /><span class="wizard-title">{{t('way.'+way)}}</span><span class="wizard-badge"><component :is="badge.icon" :size="14" /><span>{{t(badge.key)}}</span></span></span>
   </div>
   <WayPath :way="way" :phase="PHASE[state]" :locale="locale" :label-lines="1" :title="t('wayLine.'+way)" />
   <div class="wizard-text">
   <span :id="`${id}-line`" class="wizard-line">{{t('wayLine.'+way)}}</span>
   <div :id="`${id}-facts`" class="wizard-facts">
    <div v-for="fact in FACTS" :key="fact" class="wizard-fact" :data-wizard-fact="fact" :data-tone="FACT_TONES[way][fact]">
     <span class="wizard-fact-mark"><Tooltip :content="t('fact.'+fact)" side="top"><component :is="FACT_ICONS[fact]" :size="14" aria-hidden="true" data-wizard-fact-icon /></Tooltip><StatusDot :tone="FACT_TONES[way][fact]" /></span>
     <span><span class="wizard-sr">{{t('fact.'+fact)}}: </span>{{t(`value.${way}.${fact}`)}}</span>
    </div>
   </div>
   </div>
  </div>
 </Card>
</template>
<style scoped>
.wizard-way{position:relative;container-type:inline-size;cursor:pointer;outline:none}
/* The selected and hovered borders are drawn over the card's own, which the
   surface styles of a framed page set. */
.wizard-way::after{content:"";position:absolute;inset:0;border:2px solid transparent;border-radius:inherit;pointer-events:none;transition:border-color var(--ui-motion-fast) var(--ui-motion-ease)}
.wizard-way:hover::after{border-color:var(--ui-border-strong)}
.wizard-way[aria-checked="true"]::after{border-color:var(--ui-primary)}
.wizard-way:focus-visible{outline:2px solid var(--ui-primary);outline-offset:2px}
.wizard-body,.wizard-text{display:flex;flex-direction:column;gap:8px}
/* A wide card (one column in a narrow window) puts the path beside its words:
   under 240px the path keeps its dense form, and the card stays short. */
@container (min-width:440px){
 .wizard-body{display:grid;grid-template-columns:220px minmax(0,1fr);gap:8px 16px;align-items:start}
 .wizard-head{grid-column:1/-1}
}
.wizard-head{position:relative;display:flex;align-items:flex-start;gap:8px;min-height:24px}
.wizard-probe{position:absolute;top:0;left:0;visibility:hidden;white-space:nowrap;pointer-events:none}
.wizard-title{flex:0 1 auto;min-width:0;margin:0;font-size:var(--ui-font-size-lg);font-weight:var(--ui-heading-weight,700);line-height:24px;color:var(--ui-text);overflow-wrap:anywhere}
.wizard-mark{box-sizing:border-box;width:16px;height:16px;flex:none;margin-top:4px;border:1.5px solid var(--ui-border-strong);border-radius:50%;background:var(--ui-surface);transition:border var(--ui-motion-fast) var(--ui-motion-ease)}
.wizard-way[aria-checked="true"] .wizard-mark{border:5px solid var(--ui-primary)}
.wizard-badge{box-sizing:border-box;display:inline-flex;align-items:center;gap:4px;flex:none;height:24px;margin-left:auto;padding:0 5px;border:1px solid;border-radius:var(--ui-radius-sm);font-size:var(--ui-font-size-sm);line-height:14px;white-space:nowrap}
.wizard-badge[data-tone="recommended"]{border-color:color-mix(in srgb,var(--ui-info) 35%,var(--ui-surface));background:var(--ui-info-soft);color:var(--ui-info-text)}
.wizard-badge[data-tone="already"]{border-color:color-mix(in srgb,var(--ui-success) 35%,var(--ui-surface));background:var(--ui-success-soft);color:var(--ui-success-text)}
.wizard-badge[data-compact]{width:16px;padding:0;border:0;background:none}
.wizard-line{position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%);white-space:nowrap}
@container (min-width:320px){.wizard-line{position:static;width:auto;height:auto;overflow:visible;clip-path:none;white-space:normal}}
.wizard-facts{display:grid;grid-template-columns:repeat(auto-fill,minmax(140px,1fr));gap:2px 12px}
.wizard-fact{display:flex;align-items:flex-start;gap:6px;line-height:20px}
.wizard-fact-mark{display:flex;flex:none;align-items:center;gap:6px;height:20px;color:var(--ui-text-secondary)}
.wizard-sr{position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%);white-space:nowrap}
@media (prefers-reduced-motion:reduce){.wizard-way::after,.wizard-mark{transition:none}}
</style>
