<script setup lang="ts">
// The action beside a GitHub refusal: Settings at the GitHub token field.
// A rejected token asks to replace it; a rate limit asks to add one.
import {Button} from '@synopackageland/ui'
import type {GitHubCode} from '../connect/github'
import {translate} from '../i18n'
const props=defineProps<{locale:string;code:GitHubCode}>()
const emit=defineEmits<{open:[]}>()
</script>
<template>
 <Button data-github-token-action @click="emit('open')">{{translate(props.locale,props.code==='github_token_invalid'?'github.replaceToken':'github.addToken')}}</Button>
</template>
