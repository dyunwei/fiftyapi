<script setup>
import { t, locale } from './i18n.js'
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
const props=defineProps({api:{type:Function,required:true},notify:{type:Function,required:true}})
const config=ref({endpoint:'',region:'auto',bucket:'',prefix:'backups/',access_key:'',secret_key:'',path_style:true,auto_enabled:false,daily_time:'03:00',retention_days:14})
const accessSet=ref(false),secretSet=ref(false),editable=ref(false),timezone=ref(''),nextRun=ref(null),busy=ref(false),loading=ref(false),error=ref(''),recordsError=ref(''),items=ref([]),total=ref(0),page=ref(1),size=ref(20),manualDays=ref(14)
const restoreDocs=computed(()=>locale.value==='en'?'https://github.com/dyunwei/fiftyapi/blob/main/docs/en/deployment.md#backups-and-restoration':'https://github.com/dyunwei/fiftyapi/blob/main/docs/deployment.md#docker-恢复推荐')
const pages=computed(()=>Math.max(1,Math.ceil(total.value/size.value)))
const running=computed(()=>items.value.some(r=>r.status==='running'))
const statusNames=computed(()=>({running:t('备份中'),completed:t('已完成'),failed:t('失败'),deleted:t('已删除'),expired:t('已过期清理')}))
const date=n=>n?new Date(n*1000).toLocaleString(locale.value,{hour12:false}):'—'
const bytes=n=>n>=1024*1024?(n/1024/1024).toFixed(2)+' MiB':n>=1024?(n/1024).toFixed(1)+' KiB':n+' B'
let timer,request=0,disposed=false
async function loadConfig(){const result=await props.api('/admin/settings/backup');if(disposed)return;config.value={...result.config,access_key:'',secret_key:''};accessSet.value=result.access_key_set;secretSet.value=result.secret_key_set;editable.value=result.editable;timezone.value=result.timezone;nextRun.value=result.next_run;manualDays.value=result.config.retention_days}
async function loadRecords(){
 const current=++request;loading.value=true;recordsError.value='';clearTimeout(timer)
 try{
  const result=await props.api(`/admin/backups?page=${page.value}&page_size=${size.value}`)
  if(disposed||current!==request)return
  items.value=result.items;total.value=result.total
  if(page.value>pages.value){page.value=pages.value;return await loadRecords()}
 }catch(e){if(!disposed&&current===request)recordsError.value=e.message}
 finally{if(!disposed&&current===request){loading.value=false;timer=setTimeout(loadRecords,running.value?3000:30000)}}
}
async function action(fn){if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}}
async function save(){await action(async()=>{await props.api('/admin/settings/backup',{method:'PUT',body:config.value});await loadConfig();props.notify(t('备份配置已保存'))})}
async function test(){await action(async()=>{await props.api('/admin/settings/backup/test',{method:'POST',body:config.value});props.notify(t('存储连接测试成功，已验证上传和删除权限'))})}
async function create(){await action(async()=>{await props.api('/admin/backups',{method:'POST',body:{retention_days:manualDays.value}});page.value=1;await loadRecords();props.notify(t('备份任务已创建，可在下方查看进度'))})}
async function remove(item){if(!confirm(t('删除备份「{0}」？远端备份文件将被永久删除。',[item.filename])))return;await action(async()=>{await props.api(`/admin/backups/${item.id}`,{method:'DELETE'});await loadRecords();props.notify(t('备份已删除'))})}
function move(delta){page.value+=delta;loadRecords()}
async function reload(){await action(async()=>{await loadConfig();await loadRecords()})}
defineExpose({reload})
onMounted(reload)
onBeforeUnmount(()=>{disposed=true;request++;clearTimeout(timer)})
</script>
<template>
 <div class="backup-settings">
  <p v-if="error" class="alert error" role="alert">{{ t(error) }}</p>
  <section class="panel backup-panel"><h2>{{ t('S3 存储配置') }}</h2><p class="subtle">{{ t('配置 S3 兼容存储，支持 Cloudflare R2。存储桶需预先创建；凭据加密保存，留空保留原值。') }}</p>
   <form @submit.prevent="save"><fieldset :disabled="busy||!editable">
    <div class="backup-fields">
     <label>{{ t('端点地址') }}<input v-model="config.endpoint" :aria-label="t('备份端点地址')" type="url" required maxlength="2048" placeholder="https://&lt;account_id&gt;.r2.cloudflarestorage.com"></label>
     <label>{{ t('区域') }}<input v-model="config.region" :aria-label="t('备份区域')" required maxlength="64" placeholder="auto"></label>
     <label>{{ t('存储桶') }}<input v-model="config.bucket" :aria-label="t('备份存储桶')" required maxlength="63" placeholder="my-backups"></label>
     <label>{{ t('Key 前缀') }}<input v-model="config.prefix" :aria-label="t('备份 Key 前缀')" maxlength="200" placeholder="backups/"></label>
     <label>Access Key ID<input v-model="config.access_key" :aria-label="t('备份 Access Key ID')" type="password" autocomplete="new-password" maxlength="256" :required="!accessSet" :placeholder="accessSet?t('已配置，留空保持原值'):t('输入 Access Key ID')"></label>
     <label>Secret Access Key<input v-model="config.secret_key" :aria-label="t('备份 Secret Access Key')" type="password" autocomplete="new-password" maxlength="4096" :required="!secretSet" :placeholder="secretSet?t('已配置，留空保持原值'):t('输入 Secret Access Key')"></label>
    </div>
    <label class="checkbox"><input v-model="config.path_style" type="checkbox">{{ t('强制路径风格（Path Style）') }}</label>
    <div class="backup-schedule"><h3>{{ t('自动备份与保留时间') }}</h3><label class="checkbox"><input v-model="config.auto_enabled" type="checkbox">{{ t('启用每天自动备份') }}</label>
     <div class="backup-fields"><label>{{ t('每天执行时间') }}<input v-model="config.daily_time" :aria-label="t('自动备份时间')" type="time" required step="60"><small class="subtle">{{ t('服务时区：') }}{{ timezone||t('正在读取') }}</small></label><label>{{ t('默认保留天数') }}<input v-model.number="config.retention_days" :aria-label="t('备份默认保留天数')" type="number" required min="1" max="3650" step="1"><small class="subtle">{{ t('适用于之后创建的备份，过期后自动删除远端文件。') }}</small></label></div>
     <p class="subtle">{{ t('已保存计划：') }}{{ nextRun?new Date(nextRun).toLocaleString(locale,{hour12:false}):t('未启用') }}<br>{{ t('每天执行一次；当天时间已过且尚未执行时，会在保存或重启后补跑一次。') }}</p>
    </div>
    <div class="backup-actions"><button type="button" class="secondary" @click="test">{{ t('测试连接') }}</button><button class="primary">{{ t('保存备份配置') }}</button></div>
    <p class="subtle">{{ t('连接测试使用当前填写的配置，会上传并删除一个小型测试文件；创建备份使用已保存的配置。') }}</p>
   </fieldset></form>
  </section>
  <section class="panel backup-panel"><div class="backup-record-heading"><div><h2>{{ t('备份记录') }}</h2><p class="subtle">{{ t('创建手动备份，查看进度并下载已有备份。') }}</p></div><form class="backup-record-actions" @submit.prevent="create"><label>{{ t('本次保留天数') }}<input v-model.number="manualDays" :aria-label="t('本次备份保留天数')" type="number" min="1" max="3650" step="1" required></label><button class="primary" :disabled="busy||running||!editable">{{ t('创建备份') }}</button><button type="button" class="secondary" :disabled="busy||loading" @click="loadRecords">{{ t('刷新记录') }}</button></form></div>
   <p class="notice">{{ t('备份包含整个数据库快照及校验清单，不包含主密钥。请单独备份原 master.key 或保管原 FIFTYAPI_MASTER_KEY，丢失后无法恢复加密凭据。文件使用单个 tar.gz 压缩包，最大 5 GiB。') }}</p>
   <p class="subtle">{{ t('按快速开始使用 Docker 部署时，主密钥位于宿主机 ./data/master.key（容器内 /data/master.key）。请单独备份此文件，恢复时使用原文件；若设置了 FIFTYAPI_MASTER_KEY，则保管该环境变量的原值。') }}</p>
   <details class="backup-restore"><summary>{{ t('如何恢复备份？') }}</summary>
    <p>{{ t('恢复需在服务器上离线执行，Docker 镜像已内置恢复命令，无需安装 Python。网页暂不提供直接还原。') }}</p>
    <ol>
     <li>{{ t('下载备份 tar.gz 文件，并准备原主密钥。') }}</li>
     <li>{{ t('停止原服务，按部署文档执行 fiftyapi restore，将备份恢复到新目录。命令会验证备份、主密钥和数据库版本，保留原数据库。') }}</li>
     <li>{{ t('将数据库路径切换到恢复目录后启动，检查用户、Key、预算、备份计划和上游授权。快照之后的数据不会恢复，已轮换的 OAuth 令牌可能需要重新授权。') }}</li>
    </ol>
    <a :href="restoreDocs" target="_blank" rel="noopener noreferrer">{{ t('查看完整恢复步骤（Docker / 命令行）') }} ↗</a>
   </details>
   <p v-if="recordsError" class="alert error" role="alert">{{ t(recordsError) }} <button type="button" class="text-button" @click="loadRecords">{{ t('重试') }}</button></p>
   <p v-if="loading" class="subtle" role="status">{{ t('正在读取备份记录…') }}</p>
   <div v-if="!loading&&!recordsError&&!items.length" class="empty">{{ t('暂无备份记录') }}</div>
   <div v-if="items.length" class="table-wrap"><table class="backup-table"><thead><tr><th>{{ t('ID / 文件名') }}</th><th>{{ t('状态') }}</th><th>{{ t('大小') }}</th><th>{{ t('过期时间') }}</th><th>{{ t('触发方式') }}</th><th>{{ t('开始时间') }}</th><th>{{ t('操作') }}</th></tr></thead><tbody><tr v-for="item in items" :key="item.id"><td><code :title="item.object_key">{{ item.filename }}</code><small class="cell-note">{{ item.id }}</small></td><td><span :class="['pill',{'bad-pill':item.status==='failed','muted-pill':['deleted','expired'].includes(item.status)}]">{{ item.status==='completed'&&item.expires_at*1000<=Date.now()?t('已过期待清理'):statusNames[item.status]||item.status }}</span><small v-if="item.error" class="cell-note error-text">{{ t(item.error) }}</small></td><td>{{ bytes(item.size_bytes) }}</td><td>{{ date(item.expires_at) }}</td><td>{{ item.trigger==='automatic'?t('自动'):t('手动') }}<small v-if="item.actor_id" class="cell-note">{{ t('管理员 #') }}{{ item.actor_id }}</small></td><td>{{ date(item.started_at) }}<small v-if="item.completed_at" class="cell-note">{{ t('完成：') }}{{ date(item.completed_at) }}</small></td><td><div class="backup-row-actions"><a v-if="item.status==='completed'&&item.expires_at*1000>Date.now()" class="text-button" :href="`/api/admin/backups/${item.id}/download`" download>{{ t('下载') }}</a><button v-if="['completed','failed'].includes(item.status)" type="button" class="danger-text" :disabled="busy" @click="remove(item)">{{ t('删除') }}</button><span v-if="['deleted','expired','running'].includes(item.status)">—</span></div></td></tr></tbody></table></div>
   <div class="model-pagination"><span>{{ t('共 {0} 项 · 第 {1} / {2} 页',[total,page,pages]) }}</span><div class="actions"><label class="page-size">{{ t('每页') }}<select v-model.number="size" :aria-label="t('备份记录每页条数')" :disabled="loading" @change="page=1;loadRecords()"><option :value="10">10</option><option :value="20">20</option><option :value="50">50</option></select></label><button class="secondary" :disabled="loading||page<=1" @click="move(-1)">{{ t('上一页') }}</button><button class="secondary" :disabled="loading||page>=pages" @click="move(1)">{{ t('下一页') }}</button></div></div>
  </section>
 </div>
</template>
<style scoped>
.backup-settings{min-width:0}
.backup-restore{margin:16px 0 22px;line-height:1.7}
.backup-restore summary{cursor:pointer;font-weight:600}
.backup-restore p{margin:12px 0}
.backup-restore ol{padding-left:24px;margin:12px 0}
.backup-restore li+li{margin-top:8px}
.backup-panel{padding:24px}
.backup-panel h2{margin-bottom:8px}
.backup-panel>p.subtle{margin-bottom:22px}
.backup-panel fieldset{border:0;padding:0;margin:0;min-width:0}
.backup-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 20px}
.backup-actions,.backup-record-actions,.backup-row-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.backup-actions{margin:18px 0 12px}
.backup-schedule{padding-top:20px;border-top:1px solid var(--line);margin-top:20px}
.backup-schedule h3{margin-bottom:18px}
.backup-record-heading{display:flex;align-items:center;justify-content:space-between;gap:20px;margin-bottom:22px;flex-wrap:wrap}
.backup-record-actions{align-items:end}
.backup-record-actions label{margin:0;max-width:140px}
.backup-table{table-layout:fixed;min-width:960px;white-space:normal}
.backup-table th,.backup-table td{padding:12px;vertical-align:top;overflow-wrap:anywhere;white-space:normal}
.backup-table th:first-child{width:26%}
.backup-table th:nth-child(2){width:15%}
.backup-table th:nth-child(3){width:90px}
.backup-table th:nth-child(4),.backup-table th:nth-child(6){width:150px}
.backup-table th:nth-child(5){width:90px}
.backup-table th:last-child{width:100px}
.backup-table code{white-space:normal;overflow-wrap:anywhere}
.backup-panel>.model-pagination{flex-wrap:wrap;gap:12px}
@media(max-width:700px){.backup-fields{grid-template-columns:minmax(0,1fr)}.backup-panel{padding:16px}.backup-record-actions{width:100%}.backup-panel .model-pagination>.actions{flex-wrap:wrap;justify-content:flex-start}.backup-record-actions label{max-width:100%}}
</style>
