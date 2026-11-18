<script setup lang="ts">
// The administrator's optional GitHub token. Connect checks connector updates
// and downloads them from GitHub, which allows 60 anonymous requests an hour
// per public address; a token raises that to the owner's own limit. The
// token is checked with GitHub once on saving and never comes back to the
// page: a saved one shows only its last four characters.
import {computed,nextTick,onMounted,ref} from 'vue'
import {Button,Form,FormItem,Input,Link,SettingsSection,Space} from '@synopackageland/ui'
import type {Context} from '@synopackageland/sdk'
import {githubTokenUrl} from '../connect/github'
import {translate,uiLocale} from '../i18n'
const props=defineProps<{ctx:Context}>()
const t=(key:string)=>translate(props.ctx.locale,'github.'+key)
interface TokenView {saved:boolean;last4?:string;limit?:number}
const view=ref<TokenView>(),token=ref(''),replacing=ref(false),busy=ref(false),error=ref(''),limit=ref(0)
const field=ref<HTMLElement>()
const codes:Record<string,string>={github_token_invalid:'invalid',github_token_malformed:'malformed',github_unavailable:'unavailable'}
async function load(){
 try{view.value=await props.ctx.call<TokenView>('github.token.status',{})}
 catch{view.value={saved:false}}
}
async function save(){
 const value=token.value.trim()
 if(!value||busy.value)return
 busy.value=true;error.value=''
 try{
  const out=await props.ctx.call<TokenView>('github.token.set',{token:value})
  token.value='';replacing.value=false;view.value=out;limit.value=out.limit??0
 }catch(e){error.value=t(codes[(e as {code?:string})?.code??'']??'unsaved')}
 finally{busy.value=false}
}
async function remove(){
 if(busy.value)return
 busy.value=true;error.value=''
 try{view.value=await props.ctx.call<TokenView>('github.token.remove',{});limit.value=0}
 catch{error.value=t('unsaved')}
 finally{busy.value=false}
}
function replace(){replacing.value=true;void focus()}
function cancel(){replacing.value=false;token.value='';error.value=''}
// Brings the field into view and focuses it, for "Add a GitHub token" actions.
async function focus(){
 await nextTick()
 field.value?.scrollIntoView?.({block:'center'})
 const input=field.value?.querySelector<HTMLElement>('input')
 if(input)input.focus()
 else field.value?.querySelector<HTMLElement>('button')?.focus()
}
onMounted(()=>void load())
// The page pins Reset and Apply for the field in its footer.
const editing=computed(()=>!!view.value&&(!view.value.saved||replacing.value))
const canSave=computed(()=>editing.value&&!busy.value&&!!token.value.trim())
const dirty=computed(()=>replacing.value||!!token.value)
defineExpose({focus,refresh:load,save,cancel,busy,editing,canSave,dirty})
</script>
<template>
 <SettingsSection :collapsible="false" :title="t('title')" data-github-token>
  <div ref="field">
   <Form>
    <FormItem :label="t('label')" :text-only="!!view?.saved&&!replacing" :help="error||undefined" :validate-status="error?'error':undefined">
     <span v-if="view?.saved&&!replacing" data-github-token-saved>{{t('saved').replace('{last4}',view.last4??'')}}</span>
     <Input v-else v-model="token" type="password" autocomplete="off" :disabled="busy||!view" data-github-token-input @keydown.enter.prevent="save" />
     <template #after>
      <Space v-if="view?.saved&&!replacing" spacing="compact">
       <Button :disabled="busy" data-github-token-replace @click="replace">{{t('replace')}}</Button>
       <Button :loading="busy" :disabled="busy" data-github-token-remove @click="remove">{{t('remove')}}</Button>
      </Space>
      <Link v-else :href="githubTokenUrl" external data-github-token-create>{{t('create')}}</Link>
     </template>
     <template #description><Space direction="column" spacing="dense"><span>{{t('help')}}</span><span v-if="limit" role="status" data-github-token-limit>{{t('limit').replace('{limit}',new Intl.NumberFormat(uiLocale(ctx.locale)).format(limit))}}</span></Space></template>
    </FormItem>
   </Form>
  </div>
 </SettingsSection>
</template>
