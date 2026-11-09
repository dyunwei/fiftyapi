<script setup>
import { t, locale } from './i18n.js'
import { computed, ref } from 'vue'
const config=defineModel({required:true})
defineProps({creating:Boolean})
const search=ref('')
const models=computed(()=>config.value.gateway?.models||[])
const visibleModels=computed(()=>{
 const query=search.value.trim().toLowerCase()
 return models.value.map((model,index)=>({model,index})).filter(({model})=>{
  if(!query)return true
  return [model.id,model.upstream_id].some(value=>String(value||'').toLowerCase().includes(query))
 })
})
function ensureModels(){if(config.value.gateway.discovery==='manual'&&!config.value.gateway.models.length)addModel()}
function addModel(){config.value.gateway.models.push({protocols:[...config.value.gateway.protocols],id:'',upstream_id:''})}
function removeModel(index){models.value.splice(index,1)}
</script>
<template>
 <fieldset class="config-section"><legend>{{ t('API Key 网关') }}</legend>
  <label>{{ t('网关平台') }}<select :aria-label="t('网关平台')" v-model="config.gateway.platform"><option value="sub2api">Sub2Api</option><option value="fiftyapi">FiftyAPI</option><option value="new-api">New API</option><option value="custom">{{ t('其他兼容网关') }}</option></select></label>
  <label>Base URL<input aria-label="Base URL" v-model="config.gateway.base_url" type="url" required placeholder="https://gateway.example/v1"><small class="subtle">{{ t('可填写站点地址或带 /v1 的接口前缀，不要填写具体 chat/completions、messages、responses 地址。') }}</small></label>
  <label>{{ t('上游 API Key') }}<input :aria-label="t('上游 API Key')" v-model="config.api_key" type="password" :required="creating" maxlength="4096" autocomplete="new-password" :placeholder="creating?t('粘贴上游分配给你的调用 Key'):t('留空保留原 Key；填写新值可轮换')"><small class="subtle">{{ t('加密保存，不会在账号配置中返回。') }}</small></label>
  <label>{{ t('认证方式') }}<select :aria-label="t('认证方式')" v-model="config.gateway.auth_mode"><option value="bearer">Authorization: Bearer</option><option value="x-api-key">x-api-key</option></select></label>
  <span class="small-label">{{ t('上游支持的协议') }}</span><div class="protocol-options"><label class="checkbox"><input v-model="config.gateway.protocols" type="checkbox" value="chat">Chat Completions</label><label class="checkbox"><input v-model="config.gateway.protocols" type="checkbox" value="messages">Messages</label><label class="checkbox"><input v-model="config.gateway.protocols" type="checkbox" value="responses">Responses</label></div>
  <label class="checkbox"><input v-model="config.gateway.websocket" type="checkbox">{{ t('上游支持 Responses WebSocket（需确认已支持）') }}</label>
  <label class="checkbox"><input v-model="config.gateway.compact" type="checkbox">{{ t('上游支持 Responses compact') }}</label>
  <label class="checkbox"><input v-model="config.gateway.allow_http" type="checkbox">{{ t('允许 HTTP（仅用于可信内网）') }}</label>
  <p class="subtle">{{ t('平台名称用于识别；模型实际可用协议以该站点和 Key 权限为准。不执行 OAuth 专用请求改写。') }}</p>
 </fieldset>
 <fieldset class="config-section"><legend>{{ t('模型发现与映射') }}</legend>
  <label>{{ t('发现方式') }}<select :aria-label="t('发现方式')" v-model="config.gateway.discovery" @change="ensureModels"><option value="auto">{{ t('自动读取 /v1/models') }}</option><option value="manual">{{ t('手工配置，不请求模型目录') }}</option></select></label>
  <p class="subtle">{{ t('所选协议作为自动发现模型的默认能力。对外模型 ID 相同的 OAuth 账号和 API 网关会加入同一个路由池，按账号优先级和权重调度；每个来源仍使用自己的上游模型 ID。目录不证明协议权限，请按上游文档声明。') }}</p>
  <div v-if="models.length>1||search.trim()" class="mapping-search"><label>{{ t('搜索模型映射') }}<input v-model="search" type="search" :aria-label="t('搜索模型映射')" maxlength="256" :placeholder="t('按本地模型别名或上游模型 ID 搜索')"></label><span v-if="search.trim()" class="subtle">{{ t('显示 {0} / {1} 个映射',[visibleModels.length,models.length]) }}</span></div>
  <p v-if="search.trim()&&!visibleModels.length" class="subtle mapping-empty">{{ t('没有匹配的模型映射') }}</p>
  <div v-for="entry in visibleModels" :key="entry.index" class="mapping-row">
   <fieldset class="protocol-options" :aria-label="t('模型协议 ')+(entry.index+1)"><legend>{{ t('模型支持的协议（可多选）') }}</legend><label v-for="p in config.gateway.protocols" :key="p" class="checkbox"><input type="checkbox" v-model="entry.model.protocols" :value="p" :aria-label="t('模型协议 ')+(entry.index+1)+' '+p">{{ p==='chat'?'Chat Completions':p==='responses'?'Responses':'Messages' }}</label></fieldset>
   <div class="mapping-fields">
    <label>{{ t('对外模型 ID') }}<input :aria-label="t('本地模型别名 ')+(entry.index+1)" v-model="entry.model.id" maxlength="255" :placeholder="t('选填，默认使用上游模型 ID')"></label>
    <label>{{ t('上游模型 ID') }}<input :aria-label="t('上游模型 ID ')+(entry.index+1)" v-model="entry.model.upstream_id" required maxlength="255"></label>
    <button type="button" class="danger-text" :aria-label="t('移除模型 ')+(entry.index+1)" @click="removeModel(entry.index)">{{ t('移除') }}</button>
   </div>
  </div>
  <button type="button" class="secondary" @click="addModel">{{ t('添加模型映射') }}</button>
 </fieldset>
</template>
