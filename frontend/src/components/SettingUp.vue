<script setup lang="ts">
// A connection that was just made, reconnected or restored while its checks
// still fail: the daemon keeps retrying until it settles, so this is not a
// failure. The same row on every provider page, the Overview and the
// wizard's Connect step; it never blocks the page. The theme stills the
// spinner when the system asks for reduced motion.
import {Space,Spinner} from '@synopackageland/ui'
import {translate} from '../i18n'
defineProps<{locale:string}>()
</script>
<template>
 <Space align-items="center" spacing="dense" role="status" data-setting-up><Spinner inline :size="16" /><span>{{translate(locale,'connect.settling')}}</span></Space>
</template>
