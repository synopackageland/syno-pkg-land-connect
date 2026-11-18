<script setup lang="ts">
import {computed,nextTick,ref,watch} from 'vue'
import {Button,Link,SectionTabs,SettingsPage,SettingsSection,Space} from '@synopackageland/ui'
import BinarySettings from '../BinarySettings.vue'
import GitHubToken from '../components/GitHubToken.vue'
import {useConnectState} from '../connect/state'
import ReadyGate from './ReadyGate.vue'
import {translate} from '../i18n'
const s=useConnectState()
const binaries=ref<InstanceType<typeof BinarySettings>>()
const github=ref<InstanceType<typeof GitHubToken>>()
const t=(key:string)=>translate(s.ctx.locale,'connect.'+key)
// Settings is a DSM settings page: Connectors (each connector's version and
// its update, with help below) and GitHub token, whose Reset and Apply stay
// at the bottom. SectionTabs keep-mounted keeps both tabs mounted, so a typed
// token survives a switch.
const tab=ref('connectors')
const tabs=computed(()=>[{value:'connectors',label:t('connectors')},{value:'github-token',label:translate(s.ctx.locale,'github.title')}])
// "Add a GitHub token" anywhere in Connect opens Settings at the token field.
watch([s.page,s.guide],()=>{if(s.page.value==='settings'&&s.guide.value?.tab==='github-token'){s.guide.value=undefined;void openToken()}})
// The page header refresh reloads the connectors too; the first one is the mount's own read.
watch(s.refreshes,(_,before)=>{if(before){void binaries.value?.refresh();void github.value?.refresh()}})
async function openToken(){tab.value='github-token';await nextTick();await github.value?.focus()}
</script>
<template>
 <ReadyGate><SectionTabs v-model="tab" :items="tabs" fill keep-mounted :aria-label="translate(s.ctx.locale,'connect.page.settings')">
  <template #panel-connectors>
   <SettingsPage data-tab-panel="connectors"><Space direction="column" spacing="regular">
    <BinarySettings ref="binaries" :ctx="s.ctx" @github-token="openToken" />
    <SettingsSection :collapsible="false" :title="t('help')"><Space direction="column" spacing="regular">
     <Link href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/" external>Cloudflare Tunnel</Link>
     <Link href="https://tailscale.com/kb/1131/synology" external>Tailscale</Link>
     <Link href="https://docs.netbird.io/" external>NetBird</Link>
     <Link href="https://kb.synology.com/DSM/help/DSM/AdminCenter/connection_network_ipv6" external>{{t('ipv6')}}</Link>
    </Space></SettingsSection>
   </Space></SettingsPage>
  </template>
  <template #panel-github-token>
   <SettingsPage data-tab-panel="github-token">
    <GitHubToken ref="github" :ctx="s.ctx" />
    <template #footer>
     <Button :disabled="!github?.dirty||github.busy" data-github-token-cancel @click="github?.cancel()">{{translate(s.ctx.locale,'skel.reset')}}</Button>
     <Button variant="primary" :loading="github?.busy" :disabled="!github?.canSave" data-github-token-save @click="github?.save()">{{translate(s.ctx.locale,'skel.apply')}}</Button>
    </template>
   </SettingsPage>
  </template>
 </SectionTabs></ReadyGate>
</template>
