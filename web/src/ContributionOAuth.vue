<script setup>
import {computed,ref} from 'vue'
import {t} from './i18n.js'
const props=defineProps({api:{type:Function,required:true},token:{type:String,required:true},busy:Boolean,proxyId:{type:Number,default:0},forceSendProxy:Boolean})
const name=ref(''),session=ref(null),result=ref(''),done=ref(false)
const ready=computed(()=>session.value?!!result.value.trim():!!name.value.trim())
async function submit(){
 if(!session.value){session.value=await props.api('/contributions/oauth/start',{body:{token:props.token,name:name.value,...((props.proxyId||props.forceSendProxy)?{proxy_id:props.proxyId}:{})}});return}
 const current=session.value
 try{
  await props.api('/contributions/oauth/complete',{body:{token:props.token,session_id:current.session_id,result:result.value}})
  done.value=true
 }finally{session.value=null;result.value=''}
}
function restart(){session.value=null;result.value=''}
defineExpose({submit,ready:()=>ready.value,done})
</script>
<template>
 <div v-if="done" class="notice">{{ t('账号已提交，等待管理员配置并启用。') }}</div>
 <fieldset v-else :disabled="busy" class="config-section">
  <legend>{{ t('通过链接授权') }}</legend>
  <template v-if="!session">
   <label>{{ t('账号名称') }}<input v-model="name" required maxlength="128" :aria-label="t('账号名称')" autocomplete="off"></label>
   <p class="subtle">{{ t('将生成授权链接，在浏览器完成后复制授权结果回到这里。') }}</p>
  </template>
  <template v-else>
   <p class="notice">{{ t('请保持此窗口，授权会话 10 分钟内有效。') }}</p>
   <a :href="session.auth_url" target="_blank" rel="noopener noreferrer" class="primary link-button">{{ t('打开授权页面 ↗') }}</a>
   <button type="button" class="text-button" @click="restart">{{ t('重新生成链接') }}</button>
   <label>{{ t('授权结果') }}<textarea v-model="result" required rows="4" autocomplete="off" :placeholder="t('粘贴完整回调 URL 或 code#state')"></textarea></label>
   <p class="subtle">{{ t('如果 localhost 回调页面无法连接，复制地址栏中的完整 URL 即可。授权结果只用于这次换码。') }}</p>
  </template>
  <button class="primary wide" :disabled="busy||!ready">{{ busy?t('正在提交…'):session?t('提交账号'):t('生成授权链接') }}</button>
 </fieldset>
</template>
