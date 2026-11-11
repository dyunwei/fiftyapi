<script setup>
import { t, locale } from './i18n.js'
import {onMounted,onBeforeUnmount,ref} from 'vue'
import { formatContributionProxyOption } from './account-config.js'
import AccountImport from './AccountImport.vue'
import AuthFileGuide from './AuthFileGuide.vue'
import ContributionOAuth from './ContributionOAuth.vue'
const props=defineProps({token:{type:String,required:true}})
const link=ref(null),busy=ref(true),error=ref(''),form=ref(null),method=ref('file'),oauthForm=ref(null),selectedProxyId=ref(0)
const controller=new AbortController()
onBeforeUnmount(()=>controller.abort())
async function api(path,options={}){
 const response=await fetch('/api'+path,{method:options.method||'POST',headers:{'Content-Type':'application/json','Accept-Language':locale.value},credentials:'omit',referrerPolicy:'no-referrer',signal:controller.signal,body:JSON.stringify(options.body)})
 const result=await response.json().catch(()=>({error:t('服务器返回了无效响应')}))
 if(response.status===410)link.value=null
 if(!response.ok)throw new Error(result.error||t('贡献操作失败'))
 return result
}
onMounted(async()=>{try{link.value=await api('/contributions/check',{body:{token:props.token}});selectedProxyId.value=Number(link.value?.default_proxy_id)||0}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}})
async function submit(){if(busy.value)return;busy.value=true;error.value='';try{await (method.value==='oauth'?oauthForm.value:form.value).submit()}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}}
</script>
<template>
 <main class="invitation-layout contribution-layout"><section class="invitation-card contribution-card panel"><a class="brand"><span class="brand-mark">F</span>FiftyAPI</a><h1>{{ t('贡献账号') }}</h1><p class="subtle">{{ t('可通过链接授权或导入授权文件贡献账号。账号默认停用，由管理员配置并启用后接入团队服务。') }}</p>
 <p v-if="busy&&!link" class="notice">{{ t('正在检查贡献链接…') }}</p>
 <form v-if="link" @submit.prevent="submit"><p class="subtle">{{ t('链接有效至') }} {{ new Date(link.expires_at*1000).toLocaleString(locale,{hour12:false}) }}</p><label v-if="(link.proxies||[]).length&&!form?.done&&!oauthForm?.done" class="contribution-proxy-select">{{ t('代理服务器') }}<select :aria-label="t('代理服务器')" v-model.number="selectedProxyId" :disabled="busy"><option :value="0">{{ t('不使用代理') }}</option><option v-for="p in link.proxies" :key="p.id" :value="p.id">{{ formatContributionProxyOption(p,t) }}</option></select></label><div v-if="!form?.done&&!oauthForm?.done" class="actions"><button type="button" :class="method==='file'?'primary':'secondary'" :aria-pressed="method==='file'" :disabled="busy" @click="method='file'">{{ t('导入授权文件') }}</button><button type="button" :class="method==='oauth'?'primary':'secondary'" :aria-pressed="method==='oauth'" :disabled="busy" @click="method='oauth'">{{ t('通过链接授权') }}</button></div><ContributionOAuth v-if="method==='oauth'" ref="oauthForm" :api="api" :busy="busy" :token="token" :proxy-id="selectedProxyId" :force-send-proxy="Boolean(link.default_proxy_id)"/><div v-else :class="['contribution-workflow',{'contribution-complete':form?.done}]"><AuthFileGuide v-if="!form?.done" :provider="link.provider"/><section class="contribution-upload"><div v-if="!form?.done" class="contribution-step-heading"><span>2</span><h2>{{ t('上传并提交账号') }}</h2></div><AccountImport ref="form" :api="api" :busy="busy" contribution :fixed-provider="link.provider" :token="token" :proxy-id="selectedProxyId" :force-send-proxy="Boolean(link.default_proxy_id)"/><button v-if="!form?.done" class="primary wide" :disabled="busy||!form?.ready()">{{ busy?t('正在提交…'):t('提交账号') }}</button><p v-if="!form?.done" class="subtle contribution-submit-note">{{ t('提交成功后，等待管理员配置并启用即可。') }}</p></section></div></form>
 <div v-if="error" class="alert error" role="alert">{{ t(error) }}</div></section></main>
</template>
