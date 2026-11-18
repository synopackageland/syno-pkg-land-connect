<script setup lang="ts">
// The last connection.check of a connected provider and when it ran. The
// daemon rate-limits and caches checks, so asking again is always safe. A
// working check names its reach: from outside, or the NAS side only. One
// row: the result, its reach, when it ran and Check again. While a new
// connection settles it reads as setting up.
import {computed,onMounted,watch} from 'vue'
import {Button,KeyValueRow,Space} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {labelled,translate} from '../i18n'
import {checkReason,settling,type CheckResult} from '../connect/checks'
import SettingUp from './SettingUp.vue'
const props=defineProps<{provider:string;relayState?:string}>()
// Each result also goes to the status card's path picture.
const emit=defineEmits<{result:[CheckResult]}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
const result=computed(()=>s.checks.value[props.provider])
const check=()=>s.check(props.provider)
watch(result,next=>{if(next)emit('result',next)},{immediate:true})
const checkedAt=computed(()=>{const at=result.value?.checkedAt;return at?new Intl.DateTimeFormat(s.ctx.locale,{dateStyle:'short',timeStyle:'short'}).format(new Date(at)):''})
const reconnecting=computed(()=>props.relayState==='reconnecting'||props.relayState==='disconnected')
const reason=computed(()=>checkReason(s.ctx.locale,result.value?.code))
const label=computed(()=>({'not-checked':'connect.check.notChecked',checking:'connect.check.checking',starting:'ux.state.connecting',ok:'connect.check.ok',failed:'connect.check.failed'})[result.value?.state??'checking'])
onMounted(()=>{if(!result.value)void check()})
</script>
<template>
 <KeyValueRow :label="t('connect.check.title')" data-connection-check :data-check-state="result?.state">
  <Space align-items="center" spacing="dense" wrap>
   <span v-if="reconnecting" role="status">{{t('overview.relay.reconnecting')}}</span>
   <SettingUp v-else-if="settling(result)" :locale="s.ctx.locale" />
   <span v-else-if="result?.code==='access_login_required'" role="status">{{t('access.loginRequired')}}</span>
   <span v-else role="status">{{result?.state==='failed'?labelled(s.ctx.locale,label,reason):t(label)}}</span>
   <span v-if="result?.state==='ok'&&!reconnecting" aria-hidden="true">·</span><span v-if="result?.state==='ok'&&!reconnecting" data-check-scope>{{t(result.externalVerified?'connect.check.external':'connect.check.nasOnly')}}</span>
   <span v-if="checkedAt&&!settling(result)" aria-hidden="true">·</span><span v-if="checkedAt&&!settling(result)">{{labelled(s.ctx.locale,'connect.check.checkedAt','')}}<time :datetime="result?.checkedAt">{{checkedAt}}</time></span>
   <Button :disabled="result?.state==='checking'" @click="check">{{t('connect.check.again')}}</Button>
  </Space>
 </KeyValueRow>
</template>
