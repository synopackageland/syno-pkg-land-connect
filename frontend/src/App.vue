<script setup lang="ts">
import {AppRoot, Button, Callout, ToastProvider, type DropdownMenuEntry} from '@synopackageland/ui'
import type {Context} from '@synopackageland/sdk'
import {AppShell, isFullPage, type AppShellPage} from '@synopackageland/sdk/vue'
import {AppWindow, Cloud, Globe, Info, Network, Settings, Shield} from '@lucide/vue'
import {computed, type Component} from 'vue'
import {provideConnectState} from './connect/state'
import {translate,uiLocale} from './i18n'
import AppUrlsPage from './pages/AppUrlsPage.vue'
import BinariesPage from './pages/BinariesPage.vue'
import ToastNotices from './components/ToastNotices.vue'
import GitHubTokenAction from './components/GitHubTokenAction.vue'
import DsmReview from './components/DsmReview.vue'
import LanSwitchNotice from './components/LanSwitchNotice.vue'
import {useLanFound} from './connect/useLanFound'
import type {DsmReviewProvider} from './connect/dsmReview'
import SetupWizard from './components/wizard/SetupWizard.vue'
import CloudflarePage from './pages/CloudflarePage.vue'
import Ipv6Page from './pages/Ipv6Page.vue'
import NetBirdPage from './pages/NetBirdPage.vue'
import StatusPage from './pages/StatusPage.vue'
import TailscalePage from './pages/TailscalePage.vue'
import {shellCopy} from './shell/i18n'
import type {ShellPage} from './shell/pages'

const props = defineProps<{ctx: Context}>()
const lanFound = useLanFound(props.ctx)
const fullPage = isFullPage(props.ctx)
// One provider binding per mount; a DSM subject/package switch remounts the app.
const {busy, customAdd, error, dsmErrorProvider, page, pages, refresh, githubAction, openGitHubToken, phoneRequests} = provideConnectState(props.ctx)
const u = (key: string) => translate(props.ctx.locale, `ux.${key}`)
// Connect your phone opens the Overview's Phone section and scrolls to it.
const openPhone = () => { phoneRequests.value++ }
// Every page, Overview and Settings included, sits on the same card surface.
const pageSurface = 'card'
const icons: Record<ShellPage, Component> = {status:Info,apps:AppWindow,cloudflare:Cloud,tailscale:Network,netbird:Shield,ipv6:Globe,settings:Settings}
// Settings pages and tabs fill automatically in the SDK. The App list
// explicitly fills the page so only its table scrolls.
const shellEntries = computed<AppShellPage[]>(() => pages.map(id => ({id,title:shellCopy(props.ctx.locale)[id],icon:icons[id],...(id === 'apps' ? {fill:true} : {}),...(id === 'settings' ? {placement:'bottom' as const} : {})})))
// The SDK combines phone page actions and the account in one overflow menu.
// Desktop keeps the same actions beside the page title.
const headerActions = computed<DropdownMenuEntry[]>(() => [
 ...(page.value === 'apps' && customAdd.value ? [{key:'add-app',label:translate(props.ctx.locale,'apps.custom.addAction')}] : []),
 ...(page.value === 'status' ? [{key:'connect-phone',label:translate(props.ctx.locale,'phone.open')}] : []),
 ...(page.value !== 'ipv6' ? [{key:'refresh',label:u('refresh'),disabled:busy.value}] : []),
])
function headerAction(key:string) {
 if(key === 'add-app' && page.value === 'apps')customAdd.value?.()
 if(key === 'connect-phone' && page.value === 'status')openPhone()
 if(key === 'refresh' && page.value !== 'ipv6' && !busy.value)void refresh()
}
</script>

<template>
 <AppRoot :full-page="fullPage" :locale="uiLocale(ctx.locale)" :dialog-bounds="fullPage?'app':'viewport'" :style="fullPage?undefined:{height:'100%'}" @activate="ctx.host.activateWindow()">
 <ToastProvider>
 <AppShell :app-root="false" v-model="page" :ctx="ctx" :locale="ctx.locale" :pages="shellEntries" :header-actions="headerActions" @header-action="headerAction" page-width="fill" :page-surface="pageSurface" sidebar-storage-key="SynoPkgConnect">
    <template #actions="{phone}">
     <template v-if="!phone">
      <Button v-if="page === 'apps' && customAdd" variant="secondary" data-apps-add @click="customAdd()">{{ translate(ctx.locale, 'apps.custom.addAction') }}</Button>
      <Button v-if="page === 'status'" variant="secondary" data-phone-open @click="openPhone">{{ translate(ctx.locale, 'phone.open') }}</Button>
      <Button v-if="page !== 'ipv6'" variant="secondary" :loading="busy" :disabled="busy" data-refresh @click="refresh">{{ u('refresh') }}</Button>
     </template>
    </template>
    <template #notices>
      <DsmReview v-if="dsmErrorProvider" :key="dsmErrorProvider" global :provider="dsmErrorProvider as DsmReviewProvider" />
      <Callout v-else-if="error" :title="translate(ctx.locale, 'provider.attention')" tone="danger"><span role="alert">{{ error }}</span><template v-if="githubAction(error)" #actions><GitHubTokenAction :locale="ctx.locale" :code="githubAction(error)!" @open="openGitHubToken" /></template></Callout>
      <LanSwitchNotice v-if="lanFound" :found="lanFound" :locale="ctx.locale" />
    </template>
    <ToastNotices>
    <!-- Every page stays mounted, so polling and drafts survive a page switch;
         the one shown takes the shell's whole body. -->
    <div data-page="status" v-show="page === 'status'"><StatusPage /></div>
    <AppUrlsPage data-page="apps" v-show="page === 'apps'" />
    <div data-page="cloudflare" v-show="page === 'cloudflare'"><CloudflarePage /></div>
    <div data-page="tailscale" v-show="page === 'tailscale'"><TailscalePage /></div>
    <div data-page="ipv6" v-show="page === 'ipv6'"><Ipv6Page /></div>
    <div data-page="netbird" v-show="page === 'netbird'"><NetBirdPage /></div>
    <div data-page="settings" v-show="page === 'settings'"><BinariesPage /></div>
    <SetupWizard />
    </ToastNotices>
 </AppShell>
 </ToastProvider>
 </AppRoot>
</template>
