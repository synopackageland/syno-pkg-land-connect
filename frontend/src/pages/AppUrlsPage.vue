<script setup lang="ts">
// Apps: every App with every way it opens with (AppList), read when the page opens.
// The list is the page's own root, so its table fills the page.
import {SectionSkeleton} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import DsmReview from '../components/DsmReview.vue'
import AppList from '../components/AppList.vue'
const s=useConnectState()
</script>
<template>
 <AppList v-if="s.app.value" :active="s.page.value==='apps'"><DsmReview /><DsmReview provider="tailscale" /></AppList>
 <SectionSkeleton v-else-if="s.busy.value" :lines="6" />
</template>
