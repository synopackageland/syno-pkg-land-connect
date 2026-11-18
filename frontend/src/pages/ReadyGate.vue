<script setup lang="ts">
// Pages render only after app.status succeeded. The entry gate refuses
// everyone but administrators, and the shell shows that refusal. Until the
// first answer, each page shows a skeleton; the navigation stays usable.
import {SectionSkeleton} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'

const s = useConnectState()
</script>

<template>
  <slot v-if="s.app.value" />
  <SectionSkeleton v-else-if="s.busy.value" :lines="6" />
</template>
