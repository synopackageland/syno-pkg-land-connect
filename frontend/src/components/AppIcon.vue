<script setup lang="ts">
// An App's own main-menu icon, as DSM serves it; a generic window icon when
// the App has none or it does not load. Decorative: the App's name is beside it.
import {ref,watch} from 'vue'
import {AppWindow,Monitor} from '@lucide/vue'
const props=defineProps<{src?:string;dsm?:boolean}>()
const failed=ref(false)
watch(()=>props.src,()=>{failed.value=false})
</script>
<template>
 <Monitor v-if="dsm" :size="24" :stroke-width="1.5" aria-hidden="true" data-app-icon="dsm" />
 <img v-else-if="src&&!failed" :src="src" alt="" width="24" height="24" data-app-icon @error="failed=true">
 <AppWindow v-else :size="24" :stroke-width="1.5" aria-hidden="true" data-app-icon="" />
</template>
<style scoped>
[data-app-icon]{flex:none;width:24px;height:24px;color:var(--ui-text-secondary)}
</style>
