<script setup>
import { t, locale } from './i18n.js'
import {onMounted,onUnmounted,ref} from 'vue'
const props=defineProps({token:String})
const emit=defineEmits(['done','cancel'])
const invitation=ref(null),password=ref(''),confirm=ref(''),error=ref(''),busy=ref(true),invalid=ref(false)
const controller=new AbortController();onUnmounted(()=>controller.abort())
async function post(path,body){const response=await fetch('/api/invitations/'+path,{method:'POST',headers:{'Content-Type':'application/json','Accept-Language':locale.value},credentials:'omit',referrerPolicy:'no-referrer',signal:controller.signal,body:JSON.stringify(body)});const result=await response.json();if(!response.ok)throw new Error(result.error||t('邀请操作失败'));return result}
onMounted(async()=>{try{invitation.value=await post('check',{token:props.token})}catch(e){if(e.name!=='AbortError'){error.value=e.message;invalid.value=true}}finally{busy.value=false}})
async function submit(){if(busy.value)return;error.value='';if(password.value!==confirm.value){error.value=t('两次输入的密码不一致');return}if(new TextEncoder().encode(password.value).length<8||new TextEncoder().encode(password.value).length>72){error.value=t('密码需为 8–72 字节');return}busy.value=true;try{await post('accept',{token:props.token,password:password.value});password.value='';confirm.value='';emit('done',invitation.value.email)}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}}
</script>
<template>
 <main class="invitation-layout"><section class="invitation-card panel"><a class="brand"><span class="brand-mark">F</span>FiftyAPI</a><h1>{{ t('设置登录密码') }}</h1><p class="subtle">{{ t('管理员邀请你加入团队。请设置自己的密码，随后使用邮箱登录。') }}</p>
 <p v-if="busy&&!invitation" class="notice">{{ t('正在检查邀请链接…') }}</p>
 <form v-if="invitation&&!invalid" @submit.prevent="submit"><div class="notice"><b>{{ invitation.display_name||t('欢迎加入') }}</b><br>{{ invitation.email }}</div><label>{{ t('新密码') }}<input v-model="password" type="password" required minlength="8" maxlength="72" autocomplete="new-password" :placeholder="t('至少 8 个字符')"></label><label>{{ t('确认密码') }}<input v-model="confirm" type="password" required minlength="8" maxlength="72" autocomplete="new-password"></label><p class="subtle">{{ t('链接只能使用一次。设置成功后，原有登录会话将失效。') }}</p><div v-if="error" class="alert error" role="alert">{{ t(error) }}</div><button class="primary wide" :disabled="busy">{{ busy?t('正在保存…'):t('设置密码并前往登录') }}</button></form>
 <div v-else-if="error" class="alert error" role="alert">{{ t(error) }}</div><button type="button" class="text-button" :disabled="busy" @click="emit('cancel')">{{ t('返回登录') }}</button></section></main>
</template>
