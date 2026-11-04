<script setup>
import { t, locale } from './i18n.js'
import {ref,onMounted} from 'vue'
const props=defineProps({api:Function,notify:Function,email:String})
const config=ref({enabled:false,public_url:'',host:'',port:'587',from:'',username:'',password:'',tls_mode:'starttls'})
const passwordSet=ref(false),clearPassword=ref(false),source=ref('environment'),editable=ref(false),busy=ref(false),error=ref(''),recipient=ref(props.email||'')
async function load(){const r=await props.api('/admin/settings/email');config.value={...r.config,password:''};passwordSet.value=r.password_set;source.value=r.source;editable.value=r.editable;clearPassword.value=false}
async function action(fn){if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{busy.value=false}}
async function save(){await action(async()=>{await props.api('/admin/settings/email',{method:'PUT',body:{...config.value,clear_password:clearPassword.value}});config.value.password='';await load();props.notify(t('邮件配置已保存并生效'))})}
async function reset(){if(!confirm(t('恢复环境变量邮件配置？网页保存的配置将被删除。')))return;await action(async()=>{await props.api('/admin/settings/email',{method:'DELETE'});await load();props.notify(t('已恢复环境变量配置'))})}
async function test(){await action(async()=>{await props.api('/admin/settings/email/test',{method:'POST',body:{email:recipient.value}});props.notify(t('SMTP 已接受测试邮件，请检查收件情况'))})}
defineExpose({reload:()=>action(load)})
onMounted(()=>action(load))
</script>
<template>
 <section class="panel settings-panel email-settings"><h2>{{ t('邮件配置') }}</h2><p class="subtle">{{ t('用于发送成员邀请。保存后立即生效；网页配置优先于环境变量。当前来源：') }}{{ source==='database'?t('网页配置'):t('环境变量') }}。</p>
 <div v-if="error" role="alert" class="alert">{{ t(error) }}</div>
 <form @submit.prevent="save"><fieldset :disabled="busy||!editable" class="mail-fields">
  <label class="checkbox"><input v-model="config.enabled" type="checkbox">{{ t('启用邀请邮件') }}</label>
  <label>{{ t('公共访问地址') }}<input :aria-label="t('公共访问地址')" v-model="config.public_url" type="url" :required="config.enabled" maxlength="2048" placeholder="https://api.example.com"><small class="subtle">{{ t('邀请链接使用的站点地址，不包含路径。') }}</small></label>
  <div class="two-columns"><label>{{ t('SMTP 主机') }}<input :aria-label="t('SMTP 主机')" v-model="config.host" :required="config.enabled" maxlength="255" placeholder="smtp.example.com"></label><label>{{ t('SMTP 端口') }}<input :aria-label="t('SMTP 端口')" v-model="config.port" inputmode="numeric" pattern="[0-9]+" :required="config.enabled" maxlength="5"></label></div>
  <label>{{ t('发件地址') }}<input :aria-label="t('发件地址')" v-model="config.from" :required="config.enabled" maxlength="320" placeholder="FiftyAPI &lt;sender@example.com&gt;"></label>
  <div class="two-columns"><label>{{ t('SMTP 用户名') }}<input :aria-label="t('SMTP 用户名')" v-model="config.username" maxlength="320" autocomplete="off"></label><label>{{ t('SMTP 密码') }}<input :aria-label="t('SMTP 密码')" v-model="config.password" type="password" maxlength="4096" autocomplete="new-password" :disabled="clearPassword" :placeholder="passwordSet?t('已配置，留空保持原密码'):t('输入 SMTP 密码或授权码')"></label></div>
  <label v-if="passwordSet" class="checkbox"><input v-model="clearPassword" type="checkbox" @change="config.password=''">{{ t('清除已保存密码') }}</label>
  <label>{{ t('SMTP 加密方式') }}<select :aria-label="t('SMTP 加密方式')" v-model="config.tls_mode"><option value="starttls">{{ t('STARTTLS（通常 587）') }}</option><option value="tls">{{ t('TLS（通常 465）') }}</option><option value="none">{{ t('明文（仅本机测试）') }}</option></select></label>
  <p class="subtle">{{ t('密码加密保存，读取配置不会返回密码；留空保持原值，勾选清除后才会删除。') }}</p>
  <button class="primary" :disabled="busy||!editable">{{ t('保存邮件配置') }}</button>
 </fieldset></form>
 <button type="button" class="text-button" :disabled="busy" @click="reset">{{ t('恢复环境变量配置') }}</button>
 <form class="mail-test" @submit.prevent="test"><label>{{ t('测试收件邮箱') }}<input v-model="recipient" type="email" required maxlength="254" placeholder="member@example.com"></label><button class="secondary" :disabled="busy">{{ t('发送测试邮件') }}</button></form><p class="subtle">{{ t('测试使用已保存的配置，请先保存修改。') }}</p>
 </section>
</template>
