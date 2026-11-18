<script setup lang="ts">
// A way's status word in the one vocabulary every page uses: Off, Connecting,
// On, Needs attention, Failed, each with its theme colour.
import {Tag} from '@synopackageland/ui'
import {PHASE_TAG,type WayPhase} from '../connect/wayPath'
import {translate} from '../i18n'
defineProps<{phase:WayPhase;locale:string}>()
</script>
<template>
 <Tag :label="translate(locale,PHASE_TAG[phase].label)" :appearance="PHASE_TAG[phase].tone" data-way-status :data-phase="phase" />
</template>
