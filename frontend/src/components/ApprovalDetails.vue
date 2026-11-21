<script setup lang="ts">
import ConnectionError from './ConnectionError.vue'

// What an App approval shows, without its dialog: the App's name and port
// (v8 C12), its protection summary (R7) and the certificate Connect will pin
// (W: names, issuer, expiry; S: the public key's SPKI SHA-256, beside the
// certificate SHA-256 a browser shows), with the comparison hint and the
// first-use disclosure (v8 C11). A public approval adds Cloudflare's limits
// (R32); a workers.dev one adds the free plan's daily quota instead of the
// tunnel restart. The notes are
// at most five short lines; the rest is
// behind "Learn more". AppApproval puts it in a dialog; the App dialog shows it in place.
// A custom app has no certificate to pin (mode C): it shows the URL Connect
// forwards to and whether DSM sign-in comes first.
import type {NameConflict} from '../connect/nameConflict'
import HostnameInput from './HostnameInput.vue'
import NameConflictRow from './NameConflictRow.vue'
import {computed,inject} from 'vue'
import {Film,KeyRound,Plug,RotateCw,ShieldCheck,Timer} from '@lucide/vue'
import {CellText,FormItem,FormNote,KeyValueRow,Link,SettingsSection,Space} from '@synopackageland/ui'
import {fingerprint,protectionLines,type Inspection,type PinView,type Scope} from '../connect/appUrls'
import {learnMoreKey} from '../connect/pageTabs'
import {translate} from '../i18n'
const props=defineProps<{locale:string;scope:Scope;items:Inspection[];reenable?:boolean;busy?:boolean;hostnames?:Record<string,string>;error?:string;conflict?:NameConflict;dsmReason?:string;dsmOnly?:{hostname?:string;port:number;pin:PinView}}>()
const emit=defineEmits<{recover:[action:'reuse'|'use'|'check',name?:string];hostname:[pkg:string,value:string]}>()
const t=(key:string)=>translate(props.locale,'apps.'+key)
const date=(iso:string)=>{const d=new Date(iso);return Number.isNaN(d.getTime())?iso:d.toLocaleDateString(props.locale.replace('_','-'),{year:'numeric',month:'short',day:'numeric'})}
const pub=computed(()=>props.scope!=='private')
// The conflict row sits under the name it is about; the first App's when none matches.
const conflictAt=computed(()=>props.items.find(i=>(props.hostnames?.[i.package]??i.hostname)===props.conflict?.conflictName)?.package??(props.items.length?props.items[0]!.package:''))
const dsm=computed(()=>props.dsmOnly??props.items.find(i=>i.dsm)?.dsm)
// Certificate notes only when a certificate is shown.
const certs=computed(()=>!!dsm.value||props.items.some(i=>i.pin&&i.pin.mode!=='C'))
// Public notes link the Cloudflare Tunnel FAQ; private ones the page's docs.
const pageDocs=inject(learnMoreKey,undefined)
const docs=computed(()=>pub.value?'https://developers.cloudflare.com/cloudflare-one/faq/cloudflare-tunnels-faq/#large-file-and-streaming-traffic-through-tunnel':pageDocs)
const notes=computed(()=>[
 ...(certs.value?[{key:'cert',icon:ShieldCheck}]:[]),
 ...(pub.value?[{key:'signIn',icon:KeyRound}]:[]),
 ...(pub.value&&props.items.length?[{key:'media',icon:Film}]:[]),
 ...(props.scope==='relay'?[{key:'quota',icon:Timer}]:pub.value?[{key:'restart',icon:RotateCw}]:[]),
 ...(!pub.value&&props.items.length?[{key:'port',icon:Plug}]:[]),
])
const noteTexts=computed(()=>notes.value.map(note=>t('note.'+note.key)))
const pins=(pin:PinView)=>pin.mode==='C'?[]:pin.mode==='W'?[
 ...(pin.previousNames?.length?[{label:t('cert.previousNames'),value:pin.previousNames.join(', '),key:'previous'}]:[]),
 {label:t('cert.names'),value:pin.names.join(', '),key:'names'},
]:[
 ...(pin.certSha256?[{label:t('cert.certificate'),value:fingerprint(pin.certSha256),key:'certificate'}]:[]),
 ...(pin.previousSpki?[{label:t('cert.previousFingerprint'),value:fingerprint(pin.previousSpki),key:'previous'}]:[]),
 {label:t('cert.fingerprint'),value:fingerprint(pin.spki),key:'fingerprint'},
]
</script>
<template>
 <Space direction="column" spacing="regular">
  <p v-if="dsmOnly" data-dsm-change-reason>{{dsmReason||t('dsmReview.line')}}</p>
  <SettingsSection v-for="item in items" :key="item.package" :collapsible="false" :title="item.name" :data-approve-app="item.package">
   <FormItem v-if="pub&&!reenable&&item.hostname" :label="t('hostname')" :data-approve-hostname="item.package"><HostnameInput :locale="locale" :hostname="item.hostname" :model-value="hostnames?.[item.package]??item.hostname" :disabled="busy" autocomplete="off" @update:model-value="emit('hostname',item.package,$event)" /></FormItem>
   <NameConflictRow v-if="conflict&&conflictAt===item.package" :locale="locale" :conflict="conflict" :disabled="busy" @reuse="emit('recover','reuse')" @use="name=>emit('recover','use',name)" @check="emit('recover','check')" />
   <template v-if="item.custom">
    <KeyValueRow :label="t('custom.url')"><span data-approve-custom-url>{{item.custom.url}}</span></KeyValueRow>
    <KeyValueRow :label="t('custom.signIn')"><span data-approve-custom-gate>{{t(item.custom.appSignIn?'custom.appSignInTag':'custom.gateTag')}}</span></KeyValueRow>
   </template>
   <KeyValueRow v-else :label="t(item.entry?.path?'entry.after':'row.port')"><span data-approve-port>{{item.entry?.path??item.port}}</span></KeyValueRow>
   <KeyValueRow v-if="!item.custom" :label="t('row.protection')"><Space direction="column" spacing="none" data-approve-protection><span v-for="line in protectionLines(item.protection,'private')" :key="line.key">{{t('protection.'+line.key).replace('{paths}',line.paths??'')}}</span></Space></KeyValueRow>
   <template v-if="item.pin&&item.pin.mode!=='C'">
    <KeyValueRow v-for="row in pins(item.pin)" :key="row.key" :label="row.label"><span :data-approve-cert="row.key">{{row.value}}</span></KeyValueRow>
    <KeyValueRow :label="t('cert.issuer')">{{item.pin.issuer}}</KeyValueRow>
    <KeyValueRow :label="t('cert.expires')">{{date(item.pin.notAfter)}}</KeyValueRow>
   </template>
  </SettingsSection>
  <SettingsSection v-if="dsm" :collapsible="false" :title="t(dsmOnly?'approve.dsmSection':'approve.dsm').replace('{port}',String(dsm.port))" data-approve-dsm>
   <FormItem v-if="scope==='relay'&&!reenable&&dsm.hostname" :label="t('hostname')" data-approve-hostname=""><HostnameInput :locale="locale" :hostname="dsm.hostname" :model-value="hostnames?.['']??dsm.hostname" :disabled="busy" autocomplete="off" @update:model-value="emit('hostname','',$event)" /></FormItem>
   <NameConflictRow v-if="conflict&&conflictAt===''" :locale="locale" :conflict="conflict" :disabled="busy" @reuse="emit('recover','reuse')" @use="name=>emit('recover','use',name)" @check="emit('recover','check')" />
   <Space v-if="dsm.pin.previous&&(dsm.pin.mode==='C'||dsm.pin.previous.certSha256!==dsm.pin.certSha256)" direction="column" spacing="dense" data-dsm-cert-change>
    <KeyValueRow :label="t('dsmReview.previous')"><CellText :text="dsm.pin.previous.issuer" :secondary="[date(dsm.pin.previous.notAfter),dsm.pin.previous.certSha256?fingerprint(dsm.pin.previous.certSha256).slice(0,23)+'…':''].filter(Boolean).join(' · ')" wrap /></KeyValueRow>
    <KeyValueRow :label="t('dsmReview.replacement')"><CellText :text="dsm.pin.issuer" :secondary="[date(dsm.pin.notAfter),dsm.pin.mode!=='C'&&dsm.pin.certSha256?fingerprint(dsm.pin.certSha256).slice(0,23)+'…':''].filter(Boolean).join(' · ')" wrap /></KeyValueRow>
   </Space>
   <KeyValueRow v-for="row in pins(dsm.pin).filter(row=>!dsmOnly||row.key==='certificate')" :key="row.key" :label="row.label"><span>{{row.value}}</span></KeyValueRow>
   <KeyValueRow :label="t('cert.issuer')">{{dsm.pin.issuer}}</KeyValueRow>
   <KeyValueRow :label="t('cert.expires')">{{date(dsm.pin.notAfter)}}</KeyValueRow>
  </SettingsSection>
  <ConnectionError v-if="!conflict&&!['required','length','format'].some(key=>error===t('hostname.'+key))" :message="error" :scope="scope" data-approve-error />
  <Space v-if="!dsmOnly&&notes.length" direction="column" spacing="dense" align-items="start" data-approve-notes>
   <FormNote :title="t('approve.check')" :items="noteTexts" />
   <Link v-if="docs" :href="docs" external data-approve-more>{{translate(locale,'ux.learnMore')}}</Link>
  </Space>
 </Space>
</template>
