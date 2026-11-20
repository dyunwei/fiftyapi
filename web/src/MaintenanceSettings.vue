<script setup>
import {ref,onMounted} from 'vue'
import {t} from './i18n.js'
const props=defineProps({api:Function,notify:Function})
const state=ref(null),busy=ref(false),error=ref('')
async function load(){state.value=await props.api('/admin/settings/maintenance')}
async function action(fn){if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}}
async function sync(){await action(async()=>{await props.api('/admin/settings/prices/sync',{method:'POST'});await load();props.notify(t('价格目录已同步'))})}
async function check(){await action(async()=>{await props.api('/admin/settings/codex/check',{method:'POST'});await load()})}
async function useVersion(version){state.value.identity.codex_version=version;await save()}
async function save(){await action(async()=>{await props.api('/admin/settings/maintenance',{method:'PUT',body:state.value.identity});await load();props.notify(t('客户端默认版本已保存'))})}
function date(value){return value?new Date(value*1000).toLocaleString():t('未记录')}
defineExpose({reload:()=>action(load)})
onMounted(()=>action(load))
</script>
<template>
<section class="panel settings-panel maintenance-settings"><h2>{{t('自动维护')}}</h2>
<div v-if="error" class="alert" role="alert">{{t(error)}}</div>
<template v-if="state">
<h3>{{t('价格目录')}}</h3><p class="subtle">{{t('每天同步 LiteLLM 参考价格，失败保留本地版本。手工价格优先；未知模型或档位不按普通价格放行。')}}</p>
<p>{{t('目录条目')}}: {{state.catalog_count}} · {{t('上次成功同步')}}: {{date(state.prices.last_success)}}</p>
<p v-if="state.prices.error" class="alert">{{t(state.prices.error)}}</p>
<button class="secondary" :disabled="busy" @click="sync">{{t('立即同步价格')}}</button>
<h3>{{t('客户端默认版本')}}</h3><p class="subtle">{{t('账号版本留空时继承这里的配置。每天检查 Codex 稳定版本，候选版本不会自动启用，也不代表已通过兼容验证。')}}</p>
<form @submit.prevent="save"><fieldset :disabled="busy" class="mail-fields">
<label>Codex<input v-model="state.identity.codex_version" aria-label="Codex" required maxlength="64"></label>
<label>Claude Code<input v-model="state.identity.claude_version" aria-label="Claude Code" required maxlength="64"></label>
<label>Antigravity<input v-model="state.identity.antigravity_version" aria-label="Antigravity" required maxlength="64"></label>
<button class="primary" :disabled="busy">{{t('保存默认版本')}}</button>
</fieldset></form>
<p v-if="state.candidate.version">{{t('发现的 Codex 稳定版本')}}: {{state.candidate.version}}</p>
<button v-if="state.candidate.version" class="secondary" :disabled="busy||state.candidate.version===state.identity.codex_version" @click="useVersion(state.candidate.version)">{{t('采用候选版本')}}</button>
<p v-if="state.identity.previous_codex_version">{{t('上一版本')}}: {{state.identity.previous_codex_version}} <button class="secondary" :disabled="busy" @click="useVersion(state.identity.previous_codex_version)">{{t('回退上一版本')}}</button></p>
<p v-if="state.versions.error" class="alert">{{t(state.versions.error)}}</p>
<button class="secondary" :disabled="busy" @click="check">{{t('检查 Codex 版本')}}</button>
</template>
</section>
</template>
