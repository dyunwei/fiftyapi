<script setup>
import {ref, watch} from 'vue'
import {t, locale} from './i18n.js'
import {requestProtocol} from './request-diagnostics.js'
const props=defineProps({api:Function,model:String,initialProtocol:String})
const protocols=['responses','messages','chat','antigravity']
const protocol=ref(protocols.includes(props.initialProtocol)?props.initialProtocol:'responses')
const rows=ref([]),loading=ref(false),error=ref('')
const reasons={
 ready:'可参与调度', busy:'账号并发已满', cooldown:'冷却中', model_disabled:'模型已停用',
 account_disabled:'账号已停用', reauth_required:'需要重新授权', account_not_ready:'账号目录未就绪',
 protocol_mismatch:'该路由不支持此协议', unpriced:'缺少价格',
}
const revision=ref(0)
watch([protocol,revision],async(_,__,onCleanup)=>{
 let active=true
 onCleanup(()=>{active=false})
 loading.value=true;error.value='';rows.value=[]
 try{
  const result=await props.api('/admin/model-routes?'+new URLSearchParams({model:props.model,protocol:protocol.value}))
  if(active)rows.value=result
 }catch(e){if(active&&e.name!=='AbortError')error.value=e.message}
 finally{if(active)loading.value=false}
},{immediate:true})
</script>
<template>
 <p><strong>{{ model }}</strong></p>
 <div class="actions">
  <label>{{ t('协议') }} <select v-model="protocol"><option v-for="p in protocols" :key="p" :value="p">{{ requestProtocol(p) }}</option></select></label>
  <button type="button" class="secondary" :disabled="loading" @click="revision++">{{ t('刷新') }}</button>
 </div>
 <p v-if="loading" role="status">{{ t('正在读取') }}</p>
 <p v-else-if="error" role="alert">{{ error }}</p>
 <p v-else-if="!rows.length">{{ t('此模型暂无已同步的上游路由。') }}</p>
 <div v-else class="table-wrap"><table><thead><tr><th>{{ t('上游账号') }}</th><th>{{ t('上游模型') }}</th><th>{{ t('状态') }}</th><th>{{ t('并发') }}</th></tr></thead>
 <tbody><tr v-for="r in rows" :key="r.id"><td>{{ r.name }} · #{{ r.id }}</td><td>{{ r.upstream }}</td><td>{{ t(reasons[r.reason]||r.reason) }}<template v-if="r.until"> · {{ new Date(r.until*1000).toLocaleString(locale) }}</template></td><td>{{ r.active }} / {{ r.limit }}</td></tr></tbody></table></div>
</template>
