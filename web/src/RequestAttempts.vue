<script setup>
import { t, locale } from './i18n.js'
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
import ListPagination from './ListPagination.vue'
const props=defineProps({id:{type:String,required:true},api:{type:Function,required:true},admin:Boolean})
const items=ref([]),loading=ref(true),error=ref(''),page=ref(1),size=ref(10);let epoch=0
const pages=computed(()=>Math.max(1,Math.ceil(items.value.length/size.value)))
const visible=computed(()=>items.value.slice((page.value-1)*size.value,page.value*size.value))
async function load(){const current=++epoch;loading.value=true;error.value='';try{const r=await props.api(`${props.admin?'/admin':''}/logs/${encodeURIComponent(props.id)}/attempts`);if(current===epoch){items.value=r;page.value=1}}catch(e){if(current===epoch)error.value=e.message}finally{if(current===epoch)loading.value=false}}
onMounted(load);onBeforeUnmount(()=>epoch++)
</script>
<template>
 <div class="route-attempts"><p class="subtle break">{{ t('请求 ID：') }}{{ id }}</p><div class="notice">{{ t('一次客户端请求只产生一笔额度结算。下方显示实际账号尝试；明确拒绝且尚未输出实际内容的请求才可能换号。这里不保存提示词、回复或凭据。') }}</div>
 <p v-if="loading" role="status" class="subtle">{{ t('正在读取选路记录…') }}</p><div v-else-if="error" role="alert" class="alert error">{{ t(error) }} <button type="button" class="text-button" @click="load">{{ t('重试') }}</button></div>
 <p v-else-if="!items.length" class="subtle">{{ t('此请求没有逐次上游记录：可能未开始调用上游，或产生于旧版本。') }}</p>
 <div v-else class="table-wrap"><table><thead><tr><th>{{ t('尝试') }}</th><th>{{ t('账号') }}</th><th>{{ t('状态') }}</th><th>{{ t('耗时') }}</th><th>{{ t('首个输出') }}</th><th>{{ t('结果') }}</th></tr></thead><tbody><tr v-for="a in visible" :key="a.attempt"><td>{{ a.attempt }}</td><td>#{{ a.account_id }}</td><td>{{ a.status }}</td><td>{{ a.duration_ms }} ms</td><td>{{ a.first_output_ms===null?'—':a.first_output_ms+' ms' }}</td><td>{{ t(a.reason) }}<small v-if="a.retryable" class="subtle route-attempt-note">{{ t('本次符合安全重试条件') }}</small></td></tr></tbody></table></div>
 <ListPagination v-if="!loading&&!error&&items.length" v-model:page="page" v-model:size="size" :total="items.length" :pages="pages" :label="t('选路记录')"/>
 </div>
</template>
