<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { t } from './i18n.js'
import { copyText } from './clipboard.js'
import ListPagination from './ListPagination.vue'
import scriptURL from '../../scripts/migrate-sub2api.sh?url'
const props=defineProps({api:{type:Function,required:true},busy:Boolean,onboarding:Boolean})
const emit=defineEmits(['changed','finish'])
const data=ref(null),plan=ref(null),result=ref(null),error=ref(''),acceptPolicy=ref(true),cutover=ref(false)
const step=ref(1),working=ref(false),copied=ref(false),filename=ref('')
const page=ref(1),size=ref(10),checking=ref(false),checks=ref({}),checked=ref(0)
const command='curl -fsSL https://raw.githubusercontent.com/dyunwei/fiftyapi/main/scripts/migrate-sub2api.sh | sudo bash'
let epoch=0,copyTimer
const locked=computed(()=>props.busy||working.value||checking.value)
const report=computed(()=>result.value||plan.value)
const pages=computed(()=>Math.max(1,Math.ceil((report.value?.items.length||0)/size.value)))
const rows=computed(()=>report.value?.items.slice((page.value-1)*size.value,page.value*size.value)||[])
const counts=computed(()=>Object.fromEntries(['user','key','account'].map(kind=>[kind,report.value?.items.filter(i=>i.kind===kind&&i.status==='imported').length||0])))
watch(acceptPolicy,()=>{if(plan.value){plan.value=null;step.value=2};page.value=1;cutover.value=false})
async function copyCommand(){try{await copyText(command);copied.value=true;clearTimeout(copyTimer);copyTimer=setTimeout(()=>copied.value=false,2500)}catch{error.value=t('复制失败，请手动选择并复制命令')}}
async function choose(event){
 const current=++epoch;data.value=null;plan.value=null;result.value=null;checks.value={};error.value='';page.value=1;cutover.value=false;step.value=2
 const file=event.target.files?.[0];filename.value=file?.name||'';if(!file)return
 if(file.size>(31<<20)){error.value=t('迁移文件不得超过 31 MiB');return}
 try{const parsed=JSON.parse(await file.text());if(parsed.type!=='fiftyapi-sub2api-migration'||parsed.version!==1)throw new Error();if(current===epoch)data.value=parsed}
 catch{if(current===epoch)error.value=t('文件无法识别，请上传本页导出脚本生成的 JSON；普通账号备份不包含用户和 Key')}
}
async function submit(){
 if(!data.value)throw new Error(t('请先选择迁移文件'))
 if(plan.value&&!cutover.value)throw new Error(t('请先确认已停止旧站写入和令牌刷新'))
 const preview=!plan.value
 const response=await props.api('/admin/migrations/sub2api',{method:'POST',body:{data:data.value,preview,accept_key_policy:acceptPolicy.value}})
 page.value=1
 if(preview){plan.value=response;step.value=3}
 else{result.value=response;data.value=null;step.value=4;emit('changed')}
 return response
}
async function run(){if(locked.value)return;working.value=true;error.value='';try{await submit()}catch(e){error.value=e.message||t('迁移请求失败，请重试；已保存的数据不会重复创建')}finally{working.value=false}}
async function verify(){
 checking.value=true;error.value='';checked.value=0
 const current=epoch
 try{
  const pending=result.value.items.filter(i=>i.kind==='account'&&i.status==='imported'&&i.enabled&&checks.value[i.id]!=='ok')
  for(let n=0;n<pending.length&&current===epoch;n+=4){await Promise.all(pending.slice(n,n+4).map(async item=>{
   try{await props.api(`/admin/accounts/${item.id}/verify`,{method:'POST',body:{}});checks.value[item.id]='ok'}
   catch{checks.value[item.id]='failed'}finally{checked.value++}
  }))}
  if(current===epoch)emit('changed')
 }finally{checking.value=false}
}
const canVerify=computed(()=>result.value?.items.some(i=>i.kind==='account'&&i.status==='imported'&&i.enabled&&checks.value[i.id]!=='ok'))
const steps=computed(()=>[t('导出旧站数据'),t('上传迁移文件'),t('检查并导入'),t('验证与开始使用')])
function restart(){epoch++;data.value=null;plan.value=null;result.value=null;filename.value='';checks.value={};page.value=1;step.value=2;cutover.value=false;error.value=''}
onBeforeUnmount(()=>{epoch++;clearTimeout(copyTimer);data.value=null})
defineExpose({submit,done:result,preview:plan,busy:locked,ready:computed(()=>!!data.value&&(!plan.value||cutover.value))})
</script>
<template>
 <div class="sub2api-migration">
  <ol class="migration-steps" :aria-label="t('迁移步骤')"><li v-for="(label,index) in steps" :key="label" :class="{current:step===index+1,complete:step>index+1}" :aria-current="step===index+1?'step':undefined"><span>{{ step>index+1?'✓':index+1 }}</span>{{ label }}</li></ol>
  <section v-if="step===1" class="migration-stage">
   <h3>{{ t('先在旧站服务器导出数据') }}</h3>
   <p>{{ t('连接运行 sub2api 的 Docker 宿主机，复制下面的命令执行。脚本会查找 PostgreSQL 容器并生成迁移文件，无需输入数据库密码。') }}</p>
   <div class="migration-command"><pre><code>{{ command }}</code></pre><button type="button" class="secondary" :disabled="locked" @click="copyCommand">{{ copied?t('已复制'):t('复制命令') }}</button></div>
   <div class="migration-facts"><p>✓ {{ t('只读导出用户、原 Key、上游账号和代理，不停止或修改旧站。') }}</p><p>✓ {{ t('无需宿主机安装 Python 或 psql，不会将数据上传到 GitHub。') }}</p><p>✓ {{ t('有多个数据库时会提示选择；选 PostgreSQL 数据库容器，而不是 sub2api 应用容器。') }}</p></div>
   <details><summary>{{ t('GitHub 无法访问，或者容器未被自动找到？') }}</summary><p><a :href="scriptURL" download="migrate-sub2api.sh">{{ t('下载迁移脚本') }}</a>{{ t('，上传到旧服务器后执行：') }}</p><pre><code>sudo bash migrate-sub2api.sh</code></pre><p>{{ t('自定义数据库容器可执行：') }}</p><pre><code>sudo bash migrate-sub2api.sh --container YOUR_POSTGRES_CONTAINER</code></pre></details>
   <div class="notice"><strong>{{ t('正式切换前') }}</strong><p>{{ t('可以先导出并预览。确认迁移时，请先停止旧站应用的请求和令牌刷新，保持数据库容器运行，再导出最终文件。这样两套服务不会同时刷新同一份授权。') }}</p></div>
   <div class="migration-actions"><button type="button" class="primary" :disabled="locked" @click="step=2">{{ t('已导出，选择迁移文件') }} →</button><button type="button" class="text-button" :disabled="locked" @click="step=2">{{ t('我已有迁移文件') }}</button></div>
  </section>
  <section v-if="step===2" class="migration-stage">
   <h3>{{ t('把导出文件上传到这里') }}</h3>
   <p>{{ t('脚本结束时会显示文件的完整路径，通常为 /tmp/fiftyapi-sub2api-xxxxxx.json。') }}</p>
   <div class="migration-facts"><p><strong>{{ t('浏览器在另一台电脑？') }}</strong> {{ t('使用你已有的 SSH / SFTP 工具，将服务器上的 JSON 下载到这台电脑，再在下面选择文件。') }}</p><p>{{ t('如果浏览器就在旧站电脑上，直接选择脚本输出的文件即可。') }}</p></div>
   <label class="migration-upload">{{ t('迁移文件') }}<input type="file" :disabled="locked" accept=".json,application/json" @change="choose"></label><p v-if="data" class="success-text">✓ {{ t('已读取文件：{0}',[filename]) }}</p>
   <p class="subtle">{{ t('文件上限 31 MiB。凭据不会显示或写入浏览器存储；此时只做预览，不保存数据，也不联系上游。') }}</p>
   <div class="migration-policy"><h4>{{ t('迁入后的使用规则') }}</h4><p>{{ t('新迁入成员的 5 小时和 7 天预算各为 $100，原邮箱、密码和 Key 保留。余额、套餐和历史用量不迁移，旧管理员作为普通成员导入。') }}</p><label class="checkbox"><input type="checkbox" v-model="acceptPolicy" :disabled="locked">{{ t('将受限制的旧 Key 改用 fiftyapi 全局模型权限和成员预算（不保留旧分组、IP、独立额度和有效期限制）') }}</label><p class="subtle">{{ t('不勾选时，含这些限制的 Key 默认停用。勾选会扩大部分 Key 的模型权限；原本停用或已经过期的 Key 始终保持停用。重复导入不会改动已有状态。') }}</p></div>
   <div class="migration-actions"><button type="button" class="secondary" :disabled="locked" @click="step=1">{{ t('上一步') }}</button><button type="button" class="primary" :disabled="locked||!data" @click="run">{{ working?t('正在检查文件…'):t('预览迁移') }}</button></div>
  </section>
  <section v-if="step===3||step===4" class="migration-stage">
   <h3>{{ step===3?t('迁移预览（尚未保存）'):t('迁移结果') }}</h3>
   <div v-if="report" class="migration-summary"><div><strong>{{ counts.user }}</strong><span>{{ t('成员') }}</span></div><div><strong>{{ counts.key }}</strong><span>API Key</span></div><div><strong>{{ counts.account }}</strong><span>{{ t('上游账号') }}</span></div></div>
   <p v-if="report">{{ step===3?t('将导入 {0} 项，跳过 {1} 项，其中停用 {2} 项',[report.imported,report.skipped,report.disabled]):t('已导入 {0} 项，跳过 {1} 项，其中停用 {2} 项',[report.imported,report.skipped,report.disabled]) }}</p>
   <p v-if="step===3" class="subtle">{{ t('请查看下方跳过和停用的原因。重名用户不会自动合并；双因素认证用户、不支持的账号和无效代理会跳过，其他可迁移项目不受影响。') }}</p>
   <p v-if="report?.bundle_digest" class="subtle migration-digest">{{ t('服务器收到的文件校验码：') }}<code>{{ report.bundle_digest }}</code></p>
   <p v-if="report?.warning" class="alert error">{{ t(report.warning) }}</p>
   <div v-if="report" class="table-wrap"><table><thead><tr><th>{{ t('类型') }}</th><th>{{ t('名称') }}</th><th>{{ t('状态') }}</th><th>{{ t('说明') }}</th></tr></thead><tbody>
    <tr v-for="(item,index) in rows" :key="index"><td :data-label="t('类型')">{{ item.kind==='user'?t('成员'):item.kind==='key'?'API Key':t('上游账号') }} #{{ item.source_id }}</td><td :data-label="t('名称')">{{ item.name }}</td><td :data-label="t('状态')">{{ item.status==='skipped'?t('已跳过'):step===3?(item.enabled?t('将启用'):t('将停用')):item.enabled?t('已启用'):t('已停用') }}</td><td :data-label="t('说明')">{{ t(item.warning) }}<template v-if="result&&item.kind==='account'&&checks[item.id]"> · {{ checks[item.id]==='ok'?t('模型同步成功'):t('验证失败，可重试或在账号页面处理') }}</template></td></tr>
   </tbody></table></div>
   <ListPagination v-if="report" v-model:page="page" v-model:size="size" :total="report.items.length" :pages="pages" :label="t('迁移结果')"/>
   <template v-if="step===3"><label class="checkbox migration-cutover"><input type="checkbox" v-model="cutover" :disabled="locked">{{ t('已检查预览并停止旧站写入和令牌刷新，确认按上述预算和权限规则导入') }}</label><p class="subtle">{{ t('如果这是旧文件，请返回上传最终导出文件。导入期间请保持网页打开；中断后可以重复上传，不会重置已有预算。') }}</p><div class="migration-actions"><button type="button" class="secondary" :disabled="locked" @click="plan=null;step=2;cutover=false">{{ t('返回修改') }}</button><button type="button" class="primary" :disabled="locked||!cutover" @click="run">{{ working?t('正在加密保存，请稍候…'):t('确认迁移') }}</button></div></template>
   <template v-if="step===4"><div class="migration-next"><h4>{{ t('最后完成这三件事') }}</h4><ol><li>{{ t('验证并同步已启用账号的模型。失败项可重试，停用账号请到上游账号页面处理。') }}<button v-if="canVerify" type="button" class="secondary" :disabled="locked" @click="verify">{{ checking?t('正在验证并同步…'):t('验证并同步已导入账号') }}</button><span v-if="checking" role="status">{{ t('已检查 {0} 个账号',[checked]) }}</span></li><li>{{ t('在「模型与价格」检查模型和价格。没有价格的模型无法调用，旧模型别名可能需要重新配置。') }}</li><li>{{ t('用成员原邮箱登录并测试原 Key，再切换域名或客户端地址。全部确认后删除导出的 JSON 文件。') }}</li></ol></div><p class="subtle">{{ t('需要补齐失败项目？重新上传即可；已保存的用户、预算和 Key 状态会保留。') }}</p><div class="migration-actions"><button type="button" class="secondary" :disabled="locked" @click="restart">{{ t('重新上传迁移文件') }}</button><button type="button" class="primary" :disabled="locked" @click="emit('finish','models')">{{ t('检查模型与价格') }} →</button><button v-if="onboarding" type="button" class="text-button" :disabled="locked" @click="emit('finish','overview')">{{ t('进入管理控制台') }}</button></div></template>
  </section>
  <p v-if="error" class="alert error" role="alert">{{ t(error) }}</p>
 </div>
</template>
<style scoped>
.migration-steps{list-style:none;display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin:0 0 30px;padding:0}.migration-steps li{display:flex;align-items:center;gap:8px;color:#7b8492;font-size:13px;line-height:1.5}.migration-steps li span{display:grid;place-items:center;flex-shrink:0;width:28px;height:28px;border:1px solid #dce3e8;border-radius:50%;background:#f8fafb}.migration-steps .current{color:#174f43;font-weight:700}.migration-steps .current span{background:#174f43;color:white;border-color:#174f43}.migration-steps .complete span{color:#174f43;background:#e8f4ef}.migration-stage h3{font-size:22px;margin:0 0 12px}.migration-stage>p{line-height:1.8}.migration-command{display:flex;align-items:flex-start;gap:12px;padding:16px;background:#162a25;color:#edf8f2;border-radius:12px;margin:20px 0}.migration-command pre{flex:1;margin:0;min-width:0;white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.7;font-size:13px}.migration-command button{flex-shrink:0}.migration-facts{padding:4px 0 12px}.migration-facts p{line-height:1.8;margin:8px 0}.migration-stage details{margin:16px 0}.migration-stage details summary{cursor:pointer;font-weight:600}.migration-stage details pre{overflow-wrap:anywhere;white-space:pre-wrap;padding:12px;background:#f4f6f5;border-radius:8px}.migration-stage .notice p{margin:6px 0 0;line-height:1.8}.migration-actions{display:flex;flex-wrap:wrap;gap:12px;align-items:center;margin-top:24px}.migration-upload{padding:20px;border:1px dashed #9eb6ab;border-radius:12px;background:#f9fcfa}.migration-policy,.migration-next{padding:18px;background:#f6f8f7;border-radius:12px;margin:20px 0}.migration-policy h4,.migration-next h4{margin:0 0 10px;font-size:15px}.migration-policy p{line-height:1.8}.migration-policy .checkbox,.migration-cutover{align-items:flex-start;line-height:1.8}.migration-summary{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin:20px 0}.migration-summary>div{padding:16px;background:#f5f8f6;border:1px solid #e3ebe6;border-radius:12px;display:flex;align-items:baseline;gap:10px}.migration-summary strong{font-size:26px;color:#175943}.migration-summary span{color:#63736b}.migration-next li{line-height:1.9;margin:12px 0}.migration-next li button{display:block;margin:8px 0}.migration-cutover{margin-top:22px}.migration-digest code{display:block;overflow-wrap:anywhere;white-space:normal;font-size:11px}.sub2api-migration table{white-space:normal;font-size:12px}.sub2api-migration td{overflow-wrap:anywhere;vertical-align:top;padding:14px 12px;line-height:1.7}.sub2api-migration th{padding:12px}.sub2api-migration td:first-child{white-space:nowrap}.sub2api-migration td:last-child{min-width:200px}.success-text{color:#176a4c}@media(max-width:600px){.sub2api-migration .table-wrap{overflow:visible}.sub2api-migration table,.sub2api-migration tbody,.sub2api-migration tr{display:block;width:100%}.sub2api-migration thead{display:none}.sub2api-migration tr{border:1px solid #e2e9e5;border-radius:10px;margin:10px 0;padding:8px 12px}.sub2api-migration td{display:grid;grid-template-columns:56px minmax(0,1fr);gap:8px;white-space:normal;min-width:0!important;padding:8px 0;border:0}.sub2api-migration td:before{content:attr(data-label);font-weight:600;color:#54695f}.sub2api-migration td:first-child{white-space:normal}.migration-steps{grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin-bottom:22px}.migration-command{flex-direction:column}.migration-command button{align-self:flex-end}.migration-summary{gap:6px}.migration-summary>div{padding:12px;flex-direction:column;gap:0}.migration-stage h3{font-size:20px}.migration-actions button{flex-grow:1}.migration-policy,.migration-next{padding:14px}}
</style>
