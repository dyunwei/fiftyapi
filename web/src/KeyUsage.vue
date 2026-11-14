<script setup>
import {computed,onMounted,ref,watch} from 'vue'
import {t} from './i18n.js'
import {clientSnippets,setupCommand,restoreCommand} from './client-usage.js'
const props=defineProps({api:{type:Function,required:true},token:{type:String,default:''},keyName:{type:String,default:''}})
defineEmits(['copy'])
const client=ref('codex'),system=ref(/win/i.test(navigator.platform)?'windows':'unix'),keyValue=ref(props.token),model=ref(''),websocket=ref(false),models=ref([]),loading=ref(true),loadError=ref('')
const setup=computed(()=>setupCommand({origin,client:client.value,system:system.value,model:model.value,websocket:websocket.value}))
const restore=computed(()=>restoreCommand({client:client.value,system:system.value}))
const origin=window.location.origin
const protocol=computed(()=>client.value==='claude'?'messages':'responses')
const available=computed(()=>models.value.filter(m=>(m.protocols?.length?m.protocols:[m.protocol]).includes(protocol.value)))
watch(available,list=>{if(!list.some(m=>m.id===model.value))model.value=list[0]?.id||''},{immediate:true})
const snippets=computed(()=>clientSnippets({origin:window.location.origin,client:client.value,system:system.value,token:keyValue.value,model:model.value,websocket:websocket.value}))
onMounted(async()=>{try{models.value=await props.api('/models')}catch(e){if(e.name!=='AbortError')loadError.value=t('模型列表加载失败，可手动填写模型 ID。')}finally{loading.value=false}})
</script>
<template>
 <div class="key-usage">
  <p class="subtle">{{ t('使用 FiftyAPI 密钥连接客户端，配置地址已按当前站点生成。') }}<template v-if="keyName"> · {{ keyName }}</template></p>
  <div class="usage-switch" role="group" :aria-label="t('客户端')"><button v-for="[id,label] in [['codex','Codex CLI'],['claude','Claude Code'],['api',t('通用 API')]]" :key="id" type="button" :aria-pressed="client===id" @click="client=id">{{ label }}</button></div>
  <div class="usage-options">
   <label>{{ t('操作系统') }}<select v-model="system"><option value="unix">macOS / Linux</option><option value="windows">Windows PowerShell</option></select></label>
   <label v-if="client!=='api'">{{ t('模型 ID') }}<input v-model="model" list="usage-models" :placeholder="loading?t('正在加载模型…'):'MODEL_ID_FROM_CATALOG'"><datalist id="usage-models"><option v-for="m in available" :key="m.id" :value="m.id"/></datalist></label>
  </div>
  <div class="usage-key-row" :class="{'with-websocket':client==='codex'}">
   <label>{{ t('完整 API 密钥') }}<input v-model="keyValue" type="password" autocomplete="off" spellcheck="false" placeholder="YOUR_FIFTYAPI_KEY"></label>
   <label v-if="client==='codex'" class="checkbox websocket-option"><input v-model="websocket" type="checkbox">{{ t('启用 WebSocket（需上游支持）') }}</label>
  </div>
  <p class="subtle">{{ t('从密钥列表进入时自动填入可复制的密钥；也可粘贴其他密钥。留空时示例使用占位符。') }}</p>
  <p v-if="loadError" class="subtle" role="status">{{ loadError }}</p>
  <p v-if="!loading&&!available.length&&client!=='api'" class="notice">{{ t('暂无匹配协议的可用模型，请联系管理员配置模型、价格与额度。') }}</p>
  <template v-if="client!=='api'">
   <section class="usage-code setup-command"><header><strong>{{ t('一键配置') }}</strong><button type="button" class="secondary" @click="$emit('copy',setup)">{{ t('复制') }}</button></header><pre><code>{{ setup }}</code></pre></section>
   <p class="notice">{{ t('在使用客户端的终端执行，按提示粘贴密钥即可自动配置，无需手动编辑文件。VS Code 用户请使用对应窗口的终端；Remote SSH 会配置远端用户。完成后重新加载窗口并新建会话。') }}</p>
   <p class="subtle">{{ t('需要 Python 3.11+，不要使用 sudo。修改前自动备份；密钥明文保存在本机，TOML 注释与排版会规范化。') }}</p>
   <details class="usage-details restore-guide">
    <summary>{{ t('恢复接入前的配置') }}</summary>
    <p class="subtle">{{ t('恢复首次通过助手接入 FiftyAPI 前的模型、服务地址与鉴权设置，保留之后修改的其他设置。如果之前使用官方服务，恢复后即可沿用原来的接入方式。此操作不会重置客户端，也不会退出原有账号。') }}</p>
    <section class="usage-code"><header><code>{{ t('恢复命令') }}</code><button type="button" class="secondary" @click="$emit('copy',restore)">{{ t('复制') }}</button></header><pre><code>{{ restore }}</code></pre></section>
    <p class="subtle">{{ t('在同一环境、同一用户下执行，然后重新加载窗口并新建会话。若接入字段已被其他工具修改，助手会提示冲突并停止。每次修改前的完整备份保存在 ~/.fiftyapi-helper/backups/；目前不支持选择某个时间点自动恢复。') }}</p>
   </details>
   <details class="usage-details manual-guide">
    <summary>{{ t('手动配置（可选）') }}</summary>
    <template v-if="client==='codex'">
     <p class="notice">{{ t('1. 编辑用户目录中的 config.toml。将 model_provider 和 model 放在文件开头、任何 [配置段] 之前，再添加或更新提供商配置，保留其他设置。') }}</p>
     <p class="subtle">{{ t('如设置了 CODEX_HOME，请使用该目录下的 config.toml。模型必须支持 Responses 协议。') }}</p>
    </template>
    <p v-else class="notice">{{ t('选择支持 Messages 协议的模型，在终端设置以下变量后启动 Claude Code。') }}</p>
    <template v-for="(snippet,index) in snippets" :key="client+index">
     <p v-if="client==='codex'&&index===1" class="notice">{{ t('2. 在启动 Codex 的同一个终端设置密钥，然后运行 codex。每次打开新终端都需要重新设置，或加入你的 shell 配置。') }}</p>
     <section class="usage-code"><header><code>{{ snippet.title }}</code><button type="button" class="secondary" @click="$emit('copy',snippet.code)">{{ t('复制') }}</button></header><pre><code>{{ snippet.code }}</code></pre></section>
    </template>
    <p v-if="client==='codex'" class="subtle">{{ t('此方式直接读取 FIFTYAPI_API_KEY，无需修改 auth.json 或执行 codex login。WebSocket 不可用时请取消勾选。') }} <a href="https://developers.openai.com/codex/config-advanced/" target="_blank" rel="noopener noreferrer">{{ t('Codex 官方配置文档') }} ↗</a></p>
   </details>
  </template>
  <template v-else>
   <p class="notice">{{ t('OpenAI 兼容客户端的 Base URL 使用下方地址，以 Bearer 方式传入 FiftyAPI 密钥。') }} <code>{{ origin }}/v1</code></p>
   <section v-for="snippet in snippets" :key="snippet.title" class="usage-code"><header><code>{{ snippet.title }}</code><button type="button" class="secondary" @click="$emit('copy',snippet.code)">{{ t('复制') }}</button></header><pre><code>{{ snippet.code }}</code></pre></section>
  </template>
  <p class="subtle">{{ t('401：检查完整密钥；429：检查额度或限流；503：检查上游可用性。请使用模型列表中的完整 ID。') }}</p>
 </div>
</template>
<style scoped>
.key-usage{display:grid;gap:16px;min-width:0}.key-usage p,.key-usage label{margin:0}.usage-switch{display:flex;gap:8px;flex-wrap:wrap;padding:5px;background:#f2f4f7;border-radius:12px}.usage-switch button{flex:1;padding:11px 14px;color:#667085;border-radius:9px;white-space:nowrap}.usage-switch button[aria-pressed=true]{background:#fff;color:#0d9488;box-shadow:0 1px 4px #10182812}.usage-key-row{display:grid;gap:16px;min-width:0}.usage-key-row.with-websocket{grid-template-columns:minmax(0,1fr) auto;align-items:end}.usage-key-row .websocket-option{margin-bottom:12px;white-space:nowrap}.usage-details{min-width:0;border:1px solid #e4e7ec;border-radius:12px;padding:16px}.usage-details summary{cursor:pointer;font-weight:600;color:#344054}.usage-details[open]{display:grid;gap:16px}.usage-details[open] summary{margin-bottom:4px}.usage-options{display:grid;grid-template-columns:1fr 1fr;gap:16px}.key-usage small{display:block;margin-top:6px;line-height:1.6}.usage-code{border:1px solid #263342;border-radius:12px;overflow:hidden;min-width:0;background:#101828;color:#e5e7eb}.usage-code header{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 16px;background:#1d2939}.usage-code header code{overflow-wrap:anywhere;color:#d0d5dd;font-size:12px}.usage-code header button{padding:6px 12px;flex-shrink:0}.usage-code pre{overflow:auto;margin:0;padding:20px;font-size:13px;line-height:1.8;tab-size:2}.usage-code pre code{font:inherit;color:inherit;white-space:pre}.key-usage a{color:#0d9488}.key-usage .notice{line-height:1.7}@media(max-width:580px){.usage-key-row.with-websocket{grid-template-columns:1fr}.usage-key-row .websocket-option{margin-bottom:0;white-space:normal}.usage-options{grid-template-columns:1fr}.usage-switch button{padding:10px 8px;font-size:12px}.usage-code pre{padding:14px;font-size:12px}}
</style>
