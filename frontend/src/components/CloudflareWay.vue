<script setup lang="ts">
// The one layout of the Cloudflare page's Own domain and Free link tabs: to
// the user they differ only in the address. The same rows in the same order:
// a problem first (slot attention), then the address, or while off the
// sign-in step in its place (slot signin: Authorize in Cloudflare for the own
// domain, the API token for the Free link); the Apps it opens, DSM included,
// with Choose apps; the one row that is the way's own (slot extra: the
// Cloudflare sign-in and its zone, the free quota); the path picture; how to
// change the name (slot change; a new changeKey closes it); and Turn off
// (while on, or whenever stoppable says so, as while the tunnel starts): a
// red tool button in the action row at the end of the tab, where the
// Tailscale and NetBird pages turn off from their status card. The
// way's own row keeps one height, so everything below lines up across tabs. Both ways end HTTPS at
// Cloudflare, so the address says so in the same InfoTip, and both carry
// Cloudflare's upload limit in one File size row, on or off, so it is known
// before the way is chosen. The page's footer line holds
// the one note that differs. Every row uses the label column, so the
// buttons line up across both tabs.
import {computed} from 'vue'
import {Button,InfoTip,KeyValueRow,Space,Tag} from '@synopackageland/ui'
import CloudflarePath from './CloudflarePath.vue'
import SettingUp from './SettingUp.vue'
import ShowMore from './ShowMore.vue'
import UploadLimit from './UploadLimit.vue'
import type {LinkSpec} from '../connect/topology'
import {translate} from '../i18n'
import type {WayState} from '../connect/cloudflareWay'
const props=defineProps<{kind:'domain'|'free';state:WayState;locale:string;address?:string;apps?:number;changeTitle?:string;turnOffLabel:string;busy?:boolean;changeKey?:number;stoppable?:boolean;links?:[LinkSpec,LinkSpec]}>()
const emit=defineEmits<{chooseApps:[];turnOff:[]}>()
const t=(key:string)=>translate(props.locale,key)
const on=computed(()=>props.state==='on'||props.state==='attention')
const path=computed(()=>({on:'active',attention:'attention',pending:'pending',off:'idle',error:'error'} as const)[props.state])
const tag=computed(()=>props.state==='on'?{key:'on',tone:'success'} as const:{key:'attention',tone:'warning'} as const)
</script>
<template>
 <Space direction="column" spacing="regular" :data-cf-way="kind" :data-way-state="state">
  <slot name="attention" />
  <SettingUp v-if="state==='pending'" :locale="locale" />
  <template v-if="on">
   <KeyValueRow :label="t('ux.row.address')">
    <Space align-items="center" spacing="dense" wrap><span data-way-address>{{address||'—'}}</span><Tag :label="t('cfpage.state.'+tag.key)" :appearance="tag.tone" /><InfoTip icon="info" :content="t('advisor.httpsNote')" :aria-label="t('skel.cf.https')" data-cf-https /><slot name="address" /></Space>
   </KeyValueRow>
   <KeyValueRow :label="t('cfpage.free.apps')">
    <Space align-items="center" spacing="dense" wrap><span data-way-apps>{{apps===undefined?'—':apps}}</span><Button data-way-choose @click="emit('chooseApps')">{{t('cfpage.free.openApps')}}</Button></Space>
   </KeyValueRow>
   <div data-way-extra><slot name="extra" /></div>
  </template>
  <slot v-else name="signin" />
  <KeyValueRow :label="t('cflimit.row')" :data-cf-limit="kind"><UploadLimit :locale="locale" :way="kind==='free'?'cloudflare-workers':'cloudflare-domain'" /></KeyValueRow>
  <CloudflarePath :kind="kind" :state="path" :locale="locale" :links="links" />
  <ShowMore v-if="on&&$slots.change&&changeTitle" :key="changeKey" :title="changeTitle" data-way-change-toggle><div data-way-change><slot name="change" /></div></ShowMore>
  <Space v-if="on||stoppable" spacing="compact" data-way-actions><Button variant="secondary" :disabled="busy" data-way-off @click="emit('turnOff')">{{turnOffLabel}}</Button></Space>
 </Space>
</template>
