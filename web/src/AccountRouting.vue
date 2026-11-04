<script setup>
import { t, locale } from './i18n.js'
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
const props=defineProps({account:{type:Object,required:true},api:{type:Function,required:true},busy:Boolean})
const emit=defineEmits(['reset','quota'])
const loading=ref(true),error=ref(''),policy=ref(null),health=ref(null),active=ref(0),waiting=ref(0)
let epoch=0
async function load(){const current=++epoch;loading.value=true;error.value='';try{const r=await props.api(`/admin/accounts/${props.account.id}/routing`);if(current!==epoch)return;policy.value=r.policy;health.value=r.health;active.value=r.active;waiting.value=r.waiting}catch(e){if(current===epoch)error.value=e.message}finally{if(current===epoch)loading.value=false}}
async function save(){if(!policy.value)throw new Error(t('调度配置尚未加载'));await props.api(`/admin/accounts/${props.account.id}/routing`,{method:'PUT',body:Object.fromEntries(Object.entries(policy.value).map(([k,v])=>[k,Number(v)]))})}
const date=n=>n?new Date(n*1000).toLocaleString(locale.value,{hour12:false}):t('未记录')
onMounted(load);onBeforeUnmount(()=>epoch++)
defineExpose({save,load,ready:computed(()=>!!policy.value&&!loading.value&&!error.value)})
</script>
<template>
 <div class="routing-settings">
  <div v-if="error" role="alert" class="alert error">{{ t(error) }} <button type="button" class="text-button" @click="load">{{ t('重试') }}</button></div>
  <p v-if="loading" class="subtle" role="status">{{ t('正在读取调度与健康信息…') }}</p>
  <template v-else-if="policy">
   <div class="notice">{{ t('优先复用健康会话；并发满时有限等待，普通完整上下文可切换账号。带签名或加密历史的会话保持原账号。已输出实际内容的请求不会换号重放。') }}</div>
   <fieldset :disabled="busy" class="config-section"><legend>{{ account.name }} {{ t('· 调度策略') }}</legend>
    <div class="two-columns"><label>{{ t('调度优先级') }}<input v-model.number="policy.priority" :aria-label="t('调度优先级')" type="number" min="0" max="100" required><small class="subtle">{{ t('0–100，数字越小越优先；优先账号不可用时选择其他账号。') }}</small></label><label>{{ t('分配权重') }}<input v-model.number="policy.weight" :aria-label="t('分配权重')" type="number" min="1" max="100" required><small class="subtle">{{ t('同优先级、相近健康与负载时，较高权重分配更多新会话。') }}</small></label></div>
    <div class="two-columns"><label>{{ t('调度最大并发') }}<input v-model.number="policy.concurrency" :aria-label="t('调度最大并发')" type="number" min="1" max="100" required><small class="subtle">{{ t('此账号同时执行的请求上限；连接配置保存不会修改此值。') }}</small></label><label>{{ t('等待队列上限') }}<input v-model.number="policy.max_queue" :aria-label="t('等待队列上限')" type="number" min="0" max="1000" required><small class="subtle">{{ t('0 不排队；当前等待') }} {{ waiting }}{{ t('，进行中') }} {{ active }}。</small></label></div>
    <label>{{ t('单账号等待时间（毫秒）') }}<input v-model.number="policy.wait_ms" :aria-label="t('单账号等待时间（毫秒）')" type="number" min="0" max="30000" required><small class="subtle">{{ t('0 不等待；一次选路的总等待最多 10 秒，客户端取消后立即退出。') }}</small></label>
   </fieldset>
   <section v-if="health" class="routing-health"><div class="panel-heading"><h3>{{ t('健康与上游额度') }}</h3><button v-if="account.provider==='openai'" type="button" class="secondary" :disabled="busy" @click="emit('quota',account)">{{ t('查询上游额度') }}</button><button type="button" class="text-button" :disabled="busy" @click="load">{{ t('刷新状态') }}</button></div><p class="subtle">{{ t('这些是上游账号的观测值，与团队成员的美元额度独立。额度仅在上游返回相关信息后显示，历史快照不保证代表当前剩余量。') }}</p>
    <dl class="settlement-details"><dt>{{ t('最近状态') }}</dt><dd>{{ health.last_status||t('尚无推理记录') }} · {{ t(health.reason||'等待实际请求观测') }}</dd><dt>{{ t('最近成功') }}</dt><dd>{{ date(health.last_success) }}</dd><dt>{{ t('最近失败') }}</dt><dd>{{ date(health.last_failure) }}</dd><dt>{{ t('连续失败') }}</dt><dd>{{ health.consecutive_failures }}</dd><dt>{{ t('近期错误率') }}</dt><dd>{{ (health.error_rate*100).toFixed(1) }}{{ t('%（平滑观测）') }}</dd><dt>{{ t('首个输出') }}</dt><dd>{{ health.first_output_ms?Math.round(health.first_output_ms)+' ms':t('未记录') }}</dd><dt>{{ t('额度观测时间') }}</dt><dd>{{ date(health.quota_observed_at) }}</dd></dl>
    <div class="routing-quota-grid"><div v-for="(window,index) in [{used:health.primary_used_percent,reset:health.primary_reset_at},{used:health.secondary_used_percent,reset:health.secondary_reset_at}]" :key="index" class="routing-quota"><strong>{{ t('上游') }}{{ index===0?t('主'):t('次') }}{{ t('窗口') }}</strong><p>{{ window.used===null?t('尚未返回额度'):t('已使用 {0}%',[window.used.toFixed(1)]) }}</p><div v-if="window.used!==null" class="meter"><span :style="{width:window.used+'%'}" :class="{warning:window.used>=85}"></span></div><small class="subtle">{{ t('重置：') }}{{ date(window.reset) }}</small></div></div>
    <div v-if="health.cooldowns.length" class="routing-cooldowns"><h4>{{ t('当前冷却') }}</h4><div v-for="c in health.cooldowns" :key="c.scope" class="notice"><strong>{{ c.scope||t('整个账号') }}</strong><br>{{ t(c.reason) }} {{ t('· 恢复时间') }} {{ date(c.until) }}</div><button type="button" class="secondary" :disabled="busy" @click="emit('reset',account)">{{ t('解除冷却并恢复调度') }}</button><p class="subtle">{{ t('此操作会记录审计，不修改上游额度或用户预算；下次请求仍可能被上游拒绝。') }}</p></div>
   </section>
  </template>
 </div>
</template>
