<script setup lang="ts">
import CloudflareOffDialog from './CloudflareOffDialog.vue'
import {computed,ref,onUnmounted} from 'vue'
import {tsText} from '../tailscaleMessages'
import {Button,Callout,CopyCutField,Dialog,KeyValueRow,SettingsSection,Space,Tag,formatDuration} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {translate,uiLocale} from '../i18n'
import UrlCard from './UrlCard.vue'
import ConnectionCheck from './ConnectionCheck.vue'
import ProviderPath from './ProviderPath.vue'
import {STATUS_TAG,refusedOnly,statusWord,type StatusWord} from '../connect/providerStatus'
import type {CheckResult} from '../connect/checks'
import RiskNote from './RiskNote.vue'
import {netbirdKnows,netbirdText} from '../netbirdMessages'
import {trustedLoginUrl} from '../connect/login'
import {addressesOf} from '../connect/addresses'
import {dsmNeedsReview} from '../connect/appUrls'
import {nameConflict} from '../connect/nameConflict'
import NetBirdLoginTrust from './NetBirdLoginTrust.vue'
import DsmReview from './DsmReview.vue'
import RejectedChange from './RejectedChange.vue'
import SupervisionState from './SupervisionState.vue'
import type {DsmReviewProvider} from '../connect/dsmReview'
// path:false leaves the path picture to a page that draws one per way.
// other is the word of a way on the same page that runs without this
// connection (Cloudflare's Free link): it stands in while the connection is off.
// stop:false leaves Turn off to a page whose tabs each turn their way off.
const props=withDefaults(defineProps<{provider:string;path?:boolean;other?:StatusWord;stop?:boolean}>(),{path:true,stop:true})
const s=useConnectState()
const cfOff=ref(false)
const status=computed(()=>s.connectionFor(props.provider))
const t=(key:string)=>translate(s.ctx.locale,key)
const addresses=computed(()=>addressesOf(status.value))
const x=(key:Parameters<typeof tsText>[1])=>tsText(s.ctx.locale,key)
// Tailscale lists its address by IP and by MagicDNS name, so each row says which.
const hostOf=(url:string)=>{try{return new URL(url).hostname.replace(/^\[|\]$/g,'')}catch{return ''}}
const tsKind=(url:string)=>/^[\d.]+$|:/.test(hostOf(url))?'ip':'dns'
const addressLabel=(url:string)=>props.provider==='tailscale'?x(tsKind(url)==='ip'?'ip':'magicDNS'):t('ux.row.address')
// Only the Tailscale IPs no address row already shows, such as IPv6.
const otherIPs=computed(()=>{const shown=new Set(addresses.value.map(hostOf));return (status.value?.tailscaleIPs??[]).filter(ip=>!shown.has(ip))})
const now=ref(Date.now());const clock=window.setInterval(()=>{now.value=Date.now()},1000);onUnmounted(()=>window.clearInterval(clock))
const remaining=computed(()=>Math.max(0,Math.ceil((Date.parse(status.value?.loginDeadline??'')-now.value)/1000)))
const observedState=computed(()=>s.statusUnavailable.value?'unknown':status.value?.state??'disconnected')
const loginURL=computed(()=>trustedLoginUrl(props.provider,status.value?.loginUrl,props.provider==='tailscale'||props.provider==='netbird'?status.value:status.value?.userCode,status.value?.loginOrigins))
const loginLabel=computed(()=>props.provider==='netbird'?netbirdText(s.ctx.locale,'sso'):t('provider.login.'+props.provider))
// The apply line repeats nothing: a code whose text is the phase title adds only the code.
const applyTitle=computed(()=>status.value?.apply?t('apply.phase.'+status.value.apply.phase):'')
const applyLine=computed(()=>{const code=status.value?.apply?.code;if(!code)return '';const line=props.provider==='netbird'&&netbirdKnows(code)?netbirdText(s.ctx.locale,code):s.message({code});return line===applyTitle.value?'':line})
const retryApply=computed(()=>props.provider==='netbird'&&!!status.value?.apply&&['recovery-required','reauth-required','check-failed'].includes(status.value.apply.phase))
function repair(){
 const current=status.value
 const params:Record<string,unknown>={provider:props.provider}
 if(current?.managementUrl)params.managementUrl=current.managementUrl
 if(current?.loginOrigins?.length)params.loginOrigins=current.loginOrigins
 void s.connectProvider(params)
}
function admin(url:string){window.open(url,'_blank','noopener,noreferrer')}
// The sign-in window did not open: show the address to open by hand. The sign-in stays the section's one primary action.
const signingIn=computed(()=>observedState.value==='needs-login'&&status.value?.phase!=='trust-login-origin')
const fallback=computed(()=>!!loginURL.value&&s.loginFallback.value?.provider===props.provider)
function login(){if(loginURL.value)window.open(loginURL.value,'_blank','noopener,noreferrer')}
// The shared status word (wayPath's vocabulary); a failure is red.
const check=computed(()=>s.checks.value[props.provider])
const word=computed(()=>{const own=statusWord(status.value,s.statusUnavailable.value,check.value);return own==='off'&&props.other?props.other:own})
// A passing check after a refused change ends its row.
function checked(result:CheckResult){if(result.state==='ok')s.dismissRejected(props.provider)}
const tone=computed(()=>STATUS_TAG[word.value].tone)
// A taken name only names itself here; its row under the name field holds the way out.
const nameTaken=computed(()=>observedState.value==='failed'&&!!nameConflict(status.value))
// Off keeps the device's key, so the next start resumes the same device.
// Forget deletes the key here; the device stays in the admin console until removed there.
const kept=computed(()=>(props.provider==='tailscale'||props.provider==='netbird')&&observedState.value==='disconnected'&&!!status.value?.keptIdentity)
const consoleUrl=computed(()=>{
 if(props.provider==='tailscale')return 'https://login.tailscale.com/admin/machines'
 try{const u=new URL(status.value?.managementUrl||'https://api.netbird.io');return u.hostname==='api.netbird.io'?'https://app.netbird.io/peers':u.origin+'/peers'}catch{return 'https://app.netbird.io/peers'}
})
const forgetting=ref(false),forgetOpen=ref(false),forgetFailure=ref<{code:string;text:string}>()
async function forget(){
 if(forgetting.value)return
 forgetting.value=true;forgetFailure.value=undefined
 try{await s.ctx.call('connection.forget',{provider:props.provider});forgetOpen.value=false;await s.refresh()}
 catch(e){const code=(e as {code?:string})?.code??'unavailable';forgetFailure.value={code,text:s.message({code})}}
 finally{forgetting.value=false}
}
</script>
<template>
 <SettingsSection :collapsible="false" :title="t('connect.connection')" data-status-card>
  <template #actions><Tag :label="dsmNeedsReview(status)?t('apps.dsmReview.action'):t(STATUS_TAG[word].label)" :appearance="tone" data-connection-badge :data-connection-state="observedState" :data-status-word="word" /></template>
  <Space direction="column" spacing="regular">
   <SupervisionState v-if="status?.supervision" :snapshot="status.supervision" :provider="provider" :locale="s.ctx.locale" :busy="s.busy.value" @retry="s.connectProvider({provider,retry:true})" />
   <NetBirdLoginTrust v-if="provider==='netbird'" />
   <DsmReview v-if="dsmNeedsReview(status)" :provider="provider as DsmReviewProvider" />
   <KeyValueRow v-if="s.trustedDsm.value" :label="t('apps.dsmReview.trusted')" data-dsm-trusted>{{s.trustedDsm.value.issuer}} · {{t('apps.cert.expires')}} {{s.trustedDsm.value.notAfter.slice(0,10)}}</KeyValueRow>
   <ProviderPath v-if="path" :provider="provider" :check="observedState==='connected'?check:undefined" />
   <Callout v-if="signingIn" tone="warning" :title="t('attention.signIn')" :data-login-fallback="fallback||undefined">{{t(fallback?(s.loginFallback.value?.reason==='closed'?'connect.login.closed':'connect.login.blocked'):'attention.signInLine')}}<template #actions><Button v-if="loginURL" variant="primary" size="sm" data-login-action @click="login">{{fallback?t('connect.login.open'):loginLabel}}</Button></template></Callout>
   <!-- A blocked sign-in window leaves its address on the page, not inside the closable notice. -->
   <UrlCard v-if="signingIn&&fallback" :url="loginURL" :locale="s.ctx.locale" :label="loginLabel" data-login-url />
   <Callout v-if="provider==='tailscale'&&(observedState==='needs-machine-auth'||status?.code==='machine_approval_wait_timeout')" tone="warning" :title="t('attention.approve')" data-machine-approval>{{x('approvalHelp')}}<template #actions><Button size="sm" @click="admin(status?.loginServer||'https://login.tailscale.com/admin/machines')">{{x('admin')}}</Button></template></Callout>
   <span v-if="provider==='tailscale'&&status?.loginDeadline&&['needs-login','needs-machine-auth'].includes(observedState)" data-login-countdown>{{x('wait')}} {{formatDuration(remaining,{locale:uiLocale(s.ctx.locale)})}} — {{x('waitHelp')}}</span>
   <Callout v-if="provider==='tailscale'&&status?.code==='control_unreachable'" tone="warning" :title="t('attention.control')" data-control-unreachable>{{x('control')}}</Callout>
   <Callout v-if="provider==='tailscale'&&status?.code==='serve_prerequisite_missing'" tone="warning" :title="t('attention.https')" data-https-help>{{x('https')}}<template #actions><Button size="sm" @click="admin('https://login.tailscale.com/admin/dns')">{{x('dns')}}</Button></template></Callout>
   <Callout v-if="kept" tone="info" :title="t('off.kept.title')" data-kept-identity>{{t('off.kept.'+provider)}}<template #actions><Button size="sm" data-kept-console @click="admin(consoleUrl)">{{t('off.console')}}</Button><Button size="sm" :disabled="s.busy.value" data-kept-forget @click="forgetOpen=true">{{t('off.forget')}}</Button></template></Callout>
   <Callout v-if="nameTaken" tone="warning" :title="t('cloudflare.conflict.banner')" data-name-taken />
   <Callout v-else-if="observedState==='failed' && status?.code && !['tmpfs_budget_exceeded','tmpfs_storage_unavailable'].includes(status.code) && !(provider==='netbird'&&netbirdKnows(status.code)) && !dsmNeedsReview(status)" tone="danger" :title="t('attention.failed')"><span data-connection-reason>{{provider==='tailscale'&&['login_wait_timeout','machine_approval_wait_timeout','login_url_rejected','feature_approval_required','provider_status_invalid','network_connect_timeout'].includes(status.code)?x(['login_wait_timeout','machine_approval_wait_timeout'].includes(status.code)?'timeout':status.code==='login_url_rejected'?'rejected':status.code==='feature_approval_required'?'feature':status.code==='provider_status_invalid'?'invalid':'network'):s.message({code:status.code})}}</span></Callout>
   <RiskNote v-if="status?.knownLimitation==='cloudflare_remote_config_possible'" :text="t('cloudflare.risk.token')" />
   <KeyValueRow v-if="provider==='netbird' && observedState==='connected' && status?.netbirdIP" :label="t('netbird.ip')"><CopyCutField :model-value="status.netbirdIP" :copy-label="t('ux.copyIp')" :aria-label="t('netbird.ip')" /></KeyValueRow>
   <KeyValueRow v-for="address in observedState==='unknown'?[]:addresses" :key="address" :label="addressLabel(address)" :data-ts-address="provider==='tailscale'?tsKind(address):undefined"><UrlCard :url="address" :locale="s.ctx.locale" :label="addressLabel(address)" /></KeyValueRow>
   <KeyValueRow v-if="provider==='tailscale'&&otherIPs.length" :label="x('ips')"><span data-ts-other-ips>{{otherIPs.join(', ')}}</span></KeyValueRow>
   <ConnectionCheck v-if="observedState==='connected'" :provider="provider" @result="checked" />
   <KeyValueRow v-if="status?.mappings && provider!=='ipv6' && status.state==='connected'" data-connection-mappings :label="t('ux.row.services')">{{status.mappings.length}}</KeyValueRow>
   <RejectedChange :provider="provider" />
   <Callout v-if="status?.apply&&status.apply.code!=='pin_proxy_required'&&!refusedOnly(status.apply)&&!['committed','canceled'].includes(status.apply.phase)" :dismissible="!retryApply" tone="warning" :title="applyTitle" data-apply-status><template v-if="applyLine">{{applyLine}}</template><template v-if="retryApply" #actions><Button :disabled="s.busy.value" data-apply-retry @click="repair">{{t('apply.retry')}}</Button></template></Callout>
   <slot />
   <RiskNote v-if="provider==='netbird' && observedState==='connected'" :text="t('netbird.risk.policy')" />
  </Space>
  <template v-if="stop&&status && status.state!=='disconnected'" #footer><slot name="actions" /><Button variant="secondary" :disabled="s.busy.value" data-stop @click="provider==='cloudflare'?cfOff=true:s.disconnectProvider(provider)">{{t('connect.disable')}}</Button></template>
 </SettingsSection>
 <CloudflareOffDialog v-if="provider==='cloudflare'" v-model:open="cfOff" />
 <Dialog v-if="provider==='tailscale'||provider==='netbird'" v-model:open="forgetOpen" :title="t('off.forget')" data-forget-dialog>
  <Space direction="column" spacing="regular">
   <span>{{t('off.forget.confirm')}}</span>
   <Callout v-if="forgetFailure" tone="danger" :code="forgetFailure.code" data-forget-error>{{forgetFailure.text}}</Callout>
  </Space>
  <template #footer>
   <Button :disabled="forgetting" @click="forgetOpen=false">{{t('apps.cancel')}}</Button>
   <Button variant="danger" :loading="forgetting" :disabled="forgetting" data-forget-confirm @click="forget">{{t('off.forget')}}</Button>
  </template>
 </Dialog>
</template>
