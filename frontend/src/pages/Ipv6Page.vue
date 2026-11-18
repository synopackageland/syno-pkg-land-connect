<script setup lang="ts">
// IPv6 is configured in DSM and the router; Connect only reads its prerequisites.
import {SettingsPage} from '@synopackageland/ui'
import ReadyGate from './ReadyGate.vue'
import Ipv6ConnectForm from '../components/connect-forms/Ipv6ConnectForm.vue'
</script>
<template><ReadyGate><SettingsPage><Ipv6ConnectForm /></SettingsPage></ReadyGate></template>
