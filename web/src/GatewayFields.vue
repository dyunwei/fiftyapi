<script setup>
import { t, locale } from './i18n.js'
const config=defineModel({required:true})
defineProps({creating:Boolean})
function ensureModels(){if(config.value.gateway.discovery==='manual'&&!config.value.gateway.models.length)addModel()}
function addModel(){config.value.gateway.models.push({protocols:[...config.value.gateway.protocols],id:'',upstream_id:''})}
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
  <p class="subtle">{{ t('所选协议作为自动发现模型的默认能力；同一个模型 ID 可支持多种协议并共用价格。目录不证明协议权限，请按上游文档声明。下方映射会覆盖该模型的默认协议；也可补充模型。现有 ID 和价格保留，旧协议专属 ID 可继续使用。') }}</p>
  <div v-for="(m,index) in config.gateway.models" :key="index" class="mapping-row">
   <fieldset class="protocol-options" :aria-label="t('模型协议 ')+(index+1)"><legend>{{ t('模型支持的协议（可多选）') }}</legend><label v-for="p in config.gateway.protocols" :key="p" class="checkbox"><input type="checkbox" v-model="m.protocols" :value="p" :aria-label="t('模型协议 ')+(index+1)+' '+p">{{ p==='chat'?'Chat Completions':p==='responses'?'Responses':'Messages' }}</label></fieldset>
   <label>{{ t('本地模型别名') }}<input :aria-label="t('本地模型别名 ')+(index+1)" v-model="m.id" maxlength="255" :placeholder="t('选填，默认按来源区分')"></label>
   <label>{{ t('上游模型 ID') }}<input :aria-label="t('上游模型 ID ')+(index+1)" v-model="m.upstream_id" required maxlength="255"></label>
   <button type="button" class="danger-text" :aria-label="t('移除模型 ')+(index+1)" @click="config.gateway.models.splice(index,1)">{{ t('移除') }}</button>
  </div>
  <button type="button" class="secondary" @click="addModel">{{ t('添加模型映射') }}</button>
 </fieldset>
</template>
