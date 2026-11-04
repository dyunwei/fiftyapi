<script setup>
import { t, locale } from './i18n.js'
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
const props=defineProps({account:{type:Object,required:true},api:{type:Function,required:true},busy:Boolean})
const emit=defineEmits(['sync','copy'])
const search=ref(''),query=ref(''),page=ref(1),size=ref(20),total=ref(0),items=ref([]),syncedAt=ref(null),loading=ref(true),error=ref('')
const pages=computed(()=>Math.max(1,Math.ceil(total.value/size.value)))
const testModel=ref(null),testProtocol=ref(''),testRunning=ref(false),testResult=ref(null),testError=ref('')
const testRequest=computed(()=>{
 const body={model:testModel.value?.upstream_id,stream:false}
 if(testProtocol.value==='responses'){body.input='hi';body.instructions='Reply briefly to the greeting.'}
 else {body.messages=[{role:'user',content:'hi'}];if(testProtocol.value==='messages')body.max_tokens=64}
 return JSON.stringify(body,null,2)
})
function openTest(m){testModel.value=m;testProtocol.value=(m.protocols?.length?m.protocols:[m.protocol])[0];testResult.value=null;testError.value='';runTest()}
async function runTest(){
 testRunning.value=true;testResult.value=null;testError.value=''
 try{testResult.value=await props.api(`/admin/accounts/${props.account.id}/models/test`,{method:'POST',body:{model:testModel.value.id,protocol:testProtocol.value}})}
 catch(e){testError.value=e.message}
 finally{testRunning.value=false}
}
let request=0
async function load(reset=false){
 if(reset)page.value=1
 const current=++request;loading.value=true;error.value=''
 try{
  const params=new URLSearchParams({page:String(page.value),page_size:String(size.value),search:query.value})
  const result=await props.api(`/admin/accounts/${props.account.id}/models?${params}`)
  if(current!==request)return
  items.value=result.items;total.value=result.total;syncedAt.value=result.synced_at
  if(page.value>pages.value){page.value=pages.value;return await load()}
 }catch(e){if(current===request)error.value=e.message}
 finally{if(current===request)loading.value=false}
}
function applySearch(){query.value=search.value.trim();load(true)}
function move(delta){page.value+=delta;load()}
const syncTime=computed(()=>syncedAt.value?new Date(syncedAt.value*1000).toLocaleString(locale.value,{hour12:false}):t('未记录；可重新同步更新'))
onMounted(()=>load())
onBeforeUnmount(()=>request++)
</script>

<template>
 <div class="account-models">
  <div v-if="testModel" class="model-test-panel" role="dialog" aria-modal="true" :aria-label="t('模型调用测试')" @keydown.esc.stop="!testRunning&&(testModel=null)">
   <div class="account-model-summary"><strong>{{ t('模型测试 ·') }} {{ testModel.id }}</strong><button type="button" class="secondary" :disabled="testRunning" @click="testModel=null">{{ t('关闭测试') }}</button></div>
   <p class="subtle">{{ t('固定调用当前账号，发送 hi。测试会消耗上游额度，不计入本站用户账单。下方请求为协议输入，实际发送时会应用账号适配配置。') }}</p>
   <label>{{ t('测试协议') }}<select v-model="testProtocol" :disabled="testRunning" @change="testResult=null"><option v-for="p in (testModel.protocols?.length?testModel.protocols:[testModel.protocol])" :key="p" :value="p">{{ p }}</option></select></label>
   <strong>{{ t('请求内容') }}</strong><pre class="model-test-output">{{ testRequest }}</pre>
   <p v-if="testRunning" role="status">{{ t('正在向上游发送请求，等待模型响应…') }}</p>
   <p v-if="testError" class="alert error" role="alert">{{ t(testError) }}</p>
   <template v-if="testResult"><p role="status">{{ testResult.error?t('测试失败'):t('测试完成') }} · HTTP {{ testResult.status }} · {{ testResult.duration_ms }} ms</p><p v-if="testResult.error" class="alert error">{{ t(testResult.error) }}</p><strong>{{ t('响应结果') }}</strong><pre class="model-test-output">{{ testResult.response||t('（无响应正文）') }}</pre><p v-if="testResult.truncated">{{ t('响应过长，仅显示前 256 KiB。') }}</p></template>
   <button type="button" :disabled="testRunning" @click="runTest">{{ testRunning?t('测试中…'):t('重新测试') }}</button>
  </div>
  <div class="account-model-summary"><div><strong>{{ account.name }}</strong><p class="subtle">{{ t('上次成功同步：') }}{{ syncTime }}</p></div><button type="button" class="secondary" :disabled="busy||loading||!account.enabled" @click="emit('sync',account)">{{ t('验证并同步') }}</button></div>
  <div class="notice">{{ t('这里显示此账号已保存的模型目录，包含未定价和已停用模型。目录可见不代表已经通过实际调用验证。客户端须使用列出的协议，不进行标准协议之间的自动转换。') }}</div>
  <div v-if="account.provider==='antigravity'" class="notice">{{ t('Antigravity 使用专用适配提供 Messages；不通过 Chat 或 Responses 自动转换接入。') }}</div>
  <div v-if="account.last_error" class="alert error">{{ t('最近同步未成功：') }}{{ t(account.last_error) }}{{ t('。下方保留上次成功保存的目录。') }}</div>
  <div class="account-model-search"><label>{{ t('搜索模型') }}<input v-model="search" :aria-label="t('搜索账号模型')" maxlength="256" :placeholder="t('模型 ID 或上游 ID')" @keydown.enter.prevent="applySearch"></label><button type="button" class="secondary" :disabled="loading" @click="applySearch">{{ t('搜索') }}</button></div>
  <div v-if="error" class="alert error" role="alert">{{ t(error) }} <button type="button" class="text-button" @click="load()">{{ t('重试读取') }}</button></div>
  <p v-if="loading" class="subtle" role="status">{{ t('正在读取已同步模型…') }}</p>
  <template v-else-if="!error">
   <p class="subtle">{{ query?t('匹配'):t('已保存') }} {{ total }} {{ t('个模型') }}</p>
   <div v-if="!items.length" class="empty account-model-empty">{{ query?t('没有匹配的模型'):syncedAt?t('本次同步返回空目录'):t('暂无已保存模型，请先验证并同步') }}</div>
   <div v-else class="table-wrap"><table class="account-model-table"><colgroup><col class="model-id-column"><col class="upstream-id-column"><col><col class="status-column"><col class="actions-column"></colgroup><thead><tr><th>{{ t('模型 ID') }}</th><th>{{ t('上游模型 ID') }}</th><th>{{ t('协议') }}</th><th>{{ t('配置状态') }}</th><th>{{ t('操作') }}</th></tr></thead><tbody><tr v-for="m in items" :key="m.id"><td><code :title="m.id">{{ m.id }}</code></td><td><code :title="m.upstream_id">{{ m.upstream_id }}</code></td><td>{{ (m.protocols?.length?m.protocols:[m.protocol]).map(p=>p==='chat'?'Chat Completions':p==='responses'?'Responses':'Messages').join(' / ') }}</td><td><small v-for="p in (m.protocol_status||[]).filter(p=>p.cooldown_until*1000>Date.now())" :key="p.protocol" class="cell-note">{{ p.protocol }} {{ t('冷却中 ·') }} {{ t(p.cooldown_reason) }}</small><span :class="['pill',{'muted-pill':!m.routable}]">{{ m.cooldown_until*1000>Date.now()?t('暂时冷却'):!m.enabled?t('模型已停用'):!m.priced?t('未定价'):!m.routable?t('账号不可路由'):t('已配置') }}</span><small v-if="m.cooldown_until*1000>Date.now()" class="cell-note">{{ t(m.cooldown_reason) }} · {{ new Date(m.cooldown_until*1000).toLocaleString(locale) }}</small></td><td><div class="model-row-actions"><button type="button" class="text-button" :aria-label="t('复制模型 ID {0}',[m.id])" @click="emit('copy',m.id)">{{ t('复制') }}</button> <button type="button" class="text-button" :disabled="!account.enabled||testRunning" @click="openTest(m)">{{ t('测试') }}</button></div></td></tr></tbody></table></div>
   <div class="model-pagination"><label class="page-size">{{ t('每页') }}<select v-model.number="size" :aria-label="t('账号模型每页条数')" @change="load(true)"><option :value="10">10</option><option :value="20">20</option><option :value="50">50</option><option :value="100">100</option></select></label><div class="actions"><span>{{ t('第 {0} / {1} 页',[page,pages]) }}</span><button type="button" class="secondary" :disabled="page<=1||loading" @click="move(-1)">{{ t('上一页') }}</button><button type="button" class="secondary" :disabled="page>=pages||loading" @click="move(1)">{{ t('下一页') }}</button></div></div>
  </template>
 </div>
</template>

<style scoped>
.account-models{min-width:0;max-width:100%}
.account-model-table{table-layout:fixed;width:100%;min-width:640px;white-space:normal}
.model-id-column{width:32%}
.upstream-id-column{width:25%}
.status-column{width:110px}
.actions-column{width:108px}
.account-model-table th,.account-model-table td{padding:14px 12px;white-space:normal;overflow-wrap:anywhere;vertical-align:top}
.account-model-table code{white-space:normal;overflow-wrap:anywhere}
.model-row-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
@media(max-width:580px){
 .account-model-table{min-width:600px}
 .account-model-table th,.account-model-table td{padding:12px 8px}
}

.model-test-panel{position:fixed;inset:10vh max(4vw,calc((100vw - 900px)/2));z-index:1000;background:var(--surface,#fff);color:var(--text,#222);border:1px solid #999;border-radius:12px;padding:24px;overflow:auto;box-shadow:0 0 0 100vmax #0008}
.model-test-output{white-space:pre-wrap;overflow-wrap:anywhere;background:#8881;padding:12px;max-height:35vh;overflow:auto}
</style>
