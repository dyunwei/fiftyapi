<script setup>
import { t, locale } from './i18n.js'
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
const props=defineProps({api:{type:Function,required:true}})
const newFilter=()=>({level:'',module:'',account_id:'',since:'',until:''})
const filter=ref(newFilter()),applied=ref(newFilter()),items=ref([]),page=ref(1),size=ref(10),total=ref(0),loading=ref(false),error=ref('')
const pages=computed(()=>Math.max(1,Math.ceil(total.value/size.value)))
const modules=computed(()=>({server:t('服务运行'),scheduler:t('定时任务'),accounts:t('账号维护'),database:t('数据库'),backup:t('备份')}))
const levels=computed(()=>({info:t('信息'),warn:t('警告'),error:t('错误')}))
const date=n=>new Date(n*1000).toLocaleString(locale.value,{hour12:false})
let request=0
async function reload(){
 const current=++request;loading.value=true;error.value=''
 try{
  const q=new URLSearchParams({page:String(page.value),page_size:String(size.value)})
  for(const [key,value] of Object.entries(applied.value))if(String(value).trim())q.set(key,key==='since'||key==='until'?String(Math.floor(new Date(value).getTime()/1000)):String(value).trim())
  const result=await props.api('/admin/runtime-logs?'+q)
  if(current!==request)return
  items.value=result.items;total.value=result.total
  if(page.value>pages.value){page.value=pages.value;return await reload()}
 }catch(e){if(current===request)error.value=e.message}
 finally{if(current===request)loading.value=false}
}
function search(){applied.value={...filter.value};page.value=1;reload()}
function clear(){filter.value=newFilter();search()}
function move(delta){page.value+=delta;reload()}
defineExpose({reload})
onMounted(reload)
onBeforeUnmount(()=>request++)
</script>
<template>
 <section class="panel runtime-logs">
  <p class="notice">{{ t('保留最近 7 天的运行记录。同一事件在 5 分钟内合并并累计次数；耗时为最后一次执行耗时。这里只记录运行摘要，不包含密钥或请求正文。') }}</p>
  <form class="runtime-filters" @submit.prevent="search">
   <label>{{ t('级别') }}<select v-model="filter.level" :aria-label="t('级别')"><option value="">{{ t('全部级别') }}</option><option v-for="(name,key) in levels" :key="key" :value="key">{{ name }}</option></select></label>
   <label>{{ t('模块') }}<select v-model="filter.module" :aria-label="t('模块')"><option value="">{{ t('全部模块') }}</option><option v-for="(name,key) in modules" :key="key" :value="key">{{ name }}</option></select></label>
   <label>{{ t('账号 ID') }}<input v-model="filter.account_id" type="number" min="1" step="1" :placeholder="t('全部账号')"></label>
   <label>{{ t('开始时间') }}<input v-model="filter.since" type="datetime-local"></label>
   <label>{{ t('结束时间') }}<input v-model="filter.until" type="datetime-local" :min="filter.since||undefined"></label>
   <div class="runtime-filter-actions"><button class="secondary" :disabled="loading">{{ t('筛选') }}</button><button type="button" class="secondary" :disabled="loading" @click="clear">{{ t('清除') }}</button><button type="button" class="secondary" :disabled="loading" @click="reload">{{ t('刷新') }}</button></div>
  </form>
  <p v-if="error" class="alert error" role="alert">{{ t(error) }}</p>
  <p v-if="loading" class="subtle" role="status">{{ t('正在读取运行日志…') }}</p>
  <div v-if="!loading&&!error&&!items.length" class="empty"><h3>{{ t('暂无符合条件的运行日志') }}</h3></div>
  <div v-if="!error&&items.length" class="table-wrap"><table class="runtime-table"><thead><tr><th>{{ t('最近发生时间') }}</th><th>{{ t('级别') }}</th><th>{{ t('模块 / 事件') }}</th><th>{{ t('账号') }}</th><th>{{ t('运行摘要') }}</th><th>{{ t('状态 / 耗时') }}</th><th>{{ t('次数') }}</th></tr></thead><tbody><tr v-for="item in items" :key="item.id"><td :title="t('首次发生：')+date(item.created_at)">{{ date(item.last_at) }}</td><td><span :class="['pill',{'bad-pill':item.level==='error','runtime-warning':item.level==='warn'}]">{{ levels[item.level]||item.level }}</span></td><td>{{ modules[item.module]||item.module }}<small class="cell-note">{{ item.event }}</small></td><td>{{ item.account_id?'#'+item.account_id:'—' }}</td><td class="runtime-message">{{ t(item.message) }}</td><td>{{ item.status?'HTTP '+item.status:'—' }}<small class="cell-note">{{ item.duration_ms }} ms</small></td><td>{{ item.occurrences }}</td></tr></tbody></table></div>
  <div class="model-pagination"><span>{{ t('共 {0} 项 · 第 {1} / {2} 页',[total,page,pages]) }}</span><div class="actions"><label class="page-size">{{ t('每页') }}<select v-model.number="size" :aria-label="t('运行日志每页条数')" :disabled="loading" @change="page=1;reload()"><option :value="10">10</option><option :value="20">20</option><option :value="50">50</option><option :value="100">100</option></select></label><button type="button" class="secondary" :disabled="loading||page<=1" @click="move(1-page)">{{ t('首页') }}</button><button type="button" class="secondary" :disabled="loading||page<=1" @click="move(-1)">{{ t('上一页') }}</button><button type="button" class="secondary" :disabled="loading||page>=pages" @click="move(1)">{{ t('下一页') }}</button><button type="button" class="secondary" :disabled="loading||page>=pages" @click="move(pages-page)">{{ t('末页') }}</button></div></div>
 </section>
</template>
<style scoped>
.runtime-logs{padding:20px;min-width:0}
.runtime-filters{display:flex;align-items:end;flex-wrap:wrap;gap:12px;margin-bottom:20px}
.runtime-filters label{flex:1 1 140px;min-width:0;margin:0}
.runtime-filter-actions{display:flex;gap:8px;flex-wrap:wrap}
.runtime-table{table-layout:fixed;min-width:900px;white-space:normal}
.runtime-table th,.runtime-table td{padding:12px;vertical-align:top;overflow-wrap:anywhere;white-space:normal}
.runtime-table th:nth-child(1){width:160px}
.runtime-table th:nth-child(2){width:70px}
.runtime-table th:nth-child(3){width:150px}
.runtime-table th:nth-child(4){width:65px}
.runtime-table th:nth-child(6){width:110px}
.runtime-table th:nth-child(7){width:55px}
.runtime-warning{color:#b54708;background:#fffaeb}
.runtime-logs>.model-pagination{flex-wrap:wrap;gap:12px}
@media(max-width:580px){.runtime-logs{padding:12px}.runtime-logs .model-pagination>.actions{flex-wrap:wrap;justify-content:flex-start}}
</style>
