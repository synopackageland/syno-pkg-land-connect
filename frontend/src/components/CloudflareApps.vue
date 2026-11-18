<script setup lang="ts">
// The Cloudflare page's Apps tab: which Apps open through Cloudflare, by
// own domain or Free link, with their address and status, DSM first on the
// one way that serves it. Read only: an App's ways change on the App URLs
// page, which the page's footer line opens.
import {computed} from 'vue'
import {CellText,CopyCutField,DataGrid,Space,Tag} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {rowStatus,statusTone,type AppRow,type AppsView,type PublicScope} from '../connect/appUrls'
import {translate} from '../i18n'
const props=defineProps<{view?:AppsView;loading?:boolean}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
// One row per App and way; an App has at most one public way.
const rows=computed(()=>(props.view?.apps??[]).flatMap(app=>(['public','relay'] as PublicScope[]).filter(scope=>app[scope]).map(scope=>({id:`${scope}:${app.package}`,app,scope}))))
// As on the App URLs page: the address takes the room left, every header edge drags.
const columns=computed(()=>[
 {key:'app',label:t('apps.col.app'),width:200,minWidth:140},
 {key:'way',label:t('cfpage.apps.way'),width:130,minWidth:90},
 {key:'address',label:t('apps.row.address'),minWidth:220},
 {key:'status',label:t('cfpage.apps.status'),width:140,minWidth:90},
])
type Row={id:string;app:AppRow;scope:PublicScope}
const rowOf=(row:Record<string,unknown>)=>row as unknown as Row
</script>
<template>
 <Space direction="column" spacing="regular" data-cf-apps>
  <slot />
  <span v-if="loading&&!view" role="status">{{t('apps.loading')}}</span>
  <span v-else-if="!rows.length" data-cf-apps-empty>{{t('cfpage.apps.empty')}}</span>
  <DataGrid v-else :columns="columns" :rows="rows as unknown as Record<string,unknown>[]" row-key="id" state-key="cloudflare-apps" :footer="false" :page-size="1000" data-cf-apps-table>
   <template #cell-app="{row}"><span :data-cf-app="rowOf(row).app.package"><CellText :text="rowOf(row).app.name" /></span></template>
   <template #cell-way="{row}"><span :data-cf-app-way="rowOf(row).scope">{{t(rowOf(row).scope==='relay'?'cfpage.tab.free':'cfpage.tab.domain')}}</span></template>
   <template #cell-address="{row}"><CopyCutField v-if="rowOf(row).app[rowOf(row).scope]?.url" :model-value="rowOf(row).app[rowOf(row).scope]!.url" :copy-label="t('apps.copy')" :aria-label="`${t('apps.row.address')}: ${rowOf(row).app.name}`" /></template>
   <template #cell-status="{row}"><Tag :label="t('apps.status.'+rowStatus(rowOf(row).app,rowOf(row).scope))" :appearance="statusTone[rowStatus(rowOf(row).app,rowOf(row).scope)]" /></template>
  </DataGrid>
 </Space>
</template>
