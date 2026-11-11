<script setup>
import { copyText } from './clipboard.js'
import { t, locale } from './i18n.js'
import { formatProxyOption } from './account-config.js'
import {computed,onMounted,onBeforeUnmount,ref,watch} from 'vue'
const props=defineProps({api:{type:Function,required:true},proxies:{type:Array,default:()=>[]}})
const links=ref([]),provider=ref('openai'),hours=ref(24),note=ref(''),proxyId=ref(0),busy=ref(false),error=ref(''),url=ref(''),copied=ref(false),page=ref(1),filter=ref('all'),now=ref(Date.now())
const timer=setInterval(()=>now.value=Date.now(),1000)
onBeforeUnmount(()=>clearInterval(timer))
const labels={openai:'OpenAI Codex',claude:'Claude',antigravity:'Antigravity'}
const status=link=>link.revoked_at?'revoked':link.expires_at*1000<=now.value?'expired':'active'
const statusLabels=computed(()=>({active:t('有效'),revoked:t('已停用'),expired:t('已过期')}))
const filtered=computed(()=>links.value.filter(link=>filter.value==='all'||status(link)===filter.value))
const pages=computed(()=>Math.max(1,Math.ceil(filtered.value.length/10)))
const visible=computed(()=>filtered.value.slice((page.value-1)*10,page.value*10))
const proxySummary=id=>{const p=props.proxies.find(item=>item.id===id);return p?[p.location,p.note].filter(Boolean).join(' · ')||`#${p.id}`:`#${id}`}
watch(filter,()=>page.value=1)
watch(pages,value=>page.value=Math.min(page.value,value))
async function load(){links.value=await props.api('/admin/contribution-links')}
async function action(fn){if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}}
onMounted(()=>action(load))
async function create(){await action(async()=>{const r=await props.api('/admin/contribution-links',{method:'POST',body:{provider:provider.value,hours:hours.value,note:note.value,...(proxyId.value?{proxy_id:proxyId.value}:{})}});url.value=location.origin+location.pathname+r.fragment;copied.value=false;filter.value='all';page.value=1;await load()})}
async function revoke(link){await action(async()=>{await props.api(`/admin/contribution-links/${link.id}/revoke`,{method:'POST',body:{}});url.value='';await load()})}
async function copy(){copied.value=false;error.value='';try{await copyText(url.value);copied.value=true}catch{error.value=t('复制失败，请手动选择并复制链接')}}
const date=n=>new Date(n*1000).toLocaleString(locale.value,{hour12:false})
</script>
<template>
 <div class="contribution-links"><p class="notice">{{ t('把链接分享给账号所有者，由本人导入。账号默认停用，等待你配置并启用；一个链接可供多人使用。') }}</p>
 <fieldset :disabled="busy" class="config-section contribution-create"><legend>{{ t('创建临时链接') }}</legend><label>{{ t('供应商') }}<select v-model="provider"><option v-for="(label,key) in labels" :key="key" :value="key">{{ label }}</option></select></label><label>{{ t('有效期') }}<select v-model.number="hours"><option :value="1">{{ t('1 小时') }}</option><option :value="24">{{ t('24 小时') }}</option><option :value="72">{{ t('3 天') }}</option><option :value="168">{{ t('7 天') }}</option></select></label><label>{{ t('默认代理（选填）') }}<select :aria-label="t('默认代理（选填）')" v-model.number="proxyId"><option :value="0">{{ t('不预设代理') }}</option><option v-for="p in proxies" :key="p.id" :value="p.id">{{ formatProxyOption(p,t) }}</option></select></label><label>{{ t('备注（选填）') }}<input v-model="note" maxlength="60" :placeholder="t('例如：研发团队')"></label><button type="button" class="primary" @click="create">{{ busy?t('正在处理…'):t('生成贡献链接') }}</button></fieldset>
 <div v-if="url" class="notice contribution-generated"><label>{{ t('贡献链接') }}<input :value="url" readonly :aria-label="t('贡献链接')" @focus="$event.target.select()"></label><button type="button" class="secondary" @click="copy">{{ copied?t('已复制'):t('复制链接') }}</button><p class="subtle">{{ t('完整链接仅在本次创建后显示，请复制保存。') }}</p></div>
 <div v-if="error" class="alert error" role="alert">{{ t(error) }}</div>
 <div class="contribution-list-heading"><h3>{{ t('已创建的链接') }} <span class="subtle">{{ links.length }}</span></h3><label>{{ t('链接状态') }}<select v-model="filter" :disabled="busy"><option value="all">{{ t('全部状态') }}</option><option value="active">{{ t('有效') }}</option><option value="revoked">{{ t('已停用') }}</option><option value="expired">{{ t('已过期') }}</option></select></label></div>
 <p v-if="!filtered.length" class="subtle contribution-list-empty">{{ links.length?t('暂无符合条件的链接。'):t('尚未创建贡献链接。') }}</p>
 <div v-else class="contribution-list" role="list" :aria-label="t('已创建的贡献链接')"><article v-for="link in visible" :key="link.id" class="contribution-link-row" role="listitem"><div class="contribution-link-name"><strong>#{{ link.id }} · {{ labels[link.provider] }}</strong><span class="subtle">{{ link.note||t('无备注') }}<template v-if="link.proxy_id"> · {{ t('代理：') }}{{ proxySummary(link.proxy_id) }}</template></span></div><div class="contribution-link-expiry"><span class="subtle">{{ t('有效至') }}</span><time :datetime="new Date(link.expires_at*1000).toISOString()">{{ date(link.expires_at) }}</time></div><span class="contribution-link-count">{{ t('已贡献') }} {{ link.contributed_count }} {{ t('个账号') }}</span><div class="contribution-link-actions"><span :class="['pill',{'muted-pill':status(link)!=='active'}]">{{ statusLabels[status(link)] }}</span><button v-if="status(link)==='active'" type="button" class="danger-text" :disabled="busy" @click="revoke(link)">{{ t('停用链接') }}</button></div></article></div>
 <nav class="model-pagination contribution-pagination" :aria-label="t('贡献链接分页')"><span>{{ t('共 {0} 条 · 每页 10 条 · 第 {1} / {2} 页',[filtered.length,page,pages]) }}</span><div class="actions"><button type="button" class="secondary" :disabled="busy||page<=1" @click="page=1">{{ t('首页') }}</button><button type="button" class="secondary" :disabled="busy||page<=1" @click="page--">{{ t('上一页') }}</button><button type="button" class="secondary" :disabled="busy||page>=pages" @click="page++">{{ t('下一页') }}</button><button type="button" class="secondary" :disabled="busy||page>=pages" @click="page=pages">{{ t('末页') }}</button></div></nav>
 </div>
</template>
