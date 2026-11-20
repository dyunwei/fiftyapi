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
 <section class="panel settings-panel email-settings">
  <header class="mail-heading"><h2>{{ t('邮件配置') }}</h2><p class="subtle">{{ t('用于发送成员邀请。保存后立即生效；网页配置优先于环境变量。当前来源：') }}{{ source==='database'?t('网页配置'):t('环境变量') }}。</p></header>
  <div v-if="error" role="alert" class="alert">{{ t(error) }}</div>
  <div class="mail-layout">
   <form class="mail-config" @submit.prevent="save"><fieldset :disabled="busy||!editable" class="mail-fields">
    <label class="checkbox"><input v-model="config.enabled" type="checkbox">{{ t('启用邀请邮件') }}</label>
    <label>{{ t('维护告警收件邮箱') }}
     <input v-model="config.alert_recipient" type="email" maxlength="254" :placeholder="t('留空关闭告警')">
    </label>
    <div class="mail-identity-grid">
     <label>{{ t('公共访问地址') }}<input :aria-label="t('公共访问地址')" v-model="config.public_url" type="url" :required="config.enabled" maxlength="2048" placeholder="https://api.example.com"><small class="subtle">{{ t('邀请链接使用的站点地址，不包含路径。') }}</small></label>
     <label>{{ t('发件地址') }}<input :aria-label="t('发件地址')" v-model="config.from" :required="config.enabled" maxlength="320" placeholder="FiftyAPI &lt;sender@example.com&gt;"></label>
    </div>
    <div class="mail-connection-grid">
     <label>{{ t('SMTP 主机') }}<input :aria-label="t('SMTP 主机')" v-model="config.host" :required="config.enabled" maxlength="255" placeholder="smtp.example.com"></label>
     <label>{{ t('SMTP 端口') }}<input :aria-label="t('SMTP 端口')" v-model="config.port" inputmode="numeric" pattern="[0-9]+" :required="config.enabled" maxlength="5"></label>
     <label>{{ t('SMTP 加密方式') }}<select :aria-label="t('SMTP 加密方式')" v-model="config.tls_mode"><option value="starttls">{{ t('STARTTLS（通常 587）') }}</option><option value="tls">{{ t('TLS（通常 465）') }}</option><option value="none">{{ t('明文（仅本机测试）') }}</option></select></label>
    </div>
    <div class="mail-auth-grid">
     <label>{{ t('SMTP 用户名') }}<input :aria-label="t('SMTP 用户名')" v-model="config.username" maxlength="320" autocomplete="off"></label>
     <label>{{ t('SMTP 密码') }}<input :aria-label="t('SMTP 密码')" v-model="config.password" type="password" maxlength="4096" autocomplete="new-password" :disabled="clearPassword" :placeholder="passwordSet?t('已配置，留空保持原密码'):t('输入 SMTP 密码或授权码')"></label>
    </div>
    <div class="mail-password-options"><p class="subtle">{{ t('密码加密保存，读取配置不会返回密码；留空保持原值，勾选清除后才会删除。') }}</p><label v-if="passwordSet" class="checkbox"><input v-model="clearPassword" type="checkbox" @change="config.password=''">{{ t('清除已保存密码') }}</label></div>
   </fieldset>
   <div class="mail-actions"><button class="primary" :disabled="busy||!editable">{{ t('保存邮件配置') }}</button><button type="button" class="text-button" :disabled="busy" @click="reset">{{ t('恢复环境变量配置') }}</button></div>
   </form>
   <aside class="mail-test-area"><form class="mail-test" @submit.prevent="test"><label>{{ t('测试收件邮箱') }}<input v-model="recipient" type="email" required maxlength="254" placeholder="member@example.com"></label><button class="secondary" :disabled="busy">{{ t('发送测试邮件') }}</button></form><p class="subtle">{{ t('测试使用已保存的配置，请先保存修改。') }}</p></aside>
  </div>
 </section>
</template>
<style scoped>
.email-settings{max-width:1120px;padding:18px 20px;margin-bottom:0}
.email-settings .mail-heading h2{margin-bottom:6px}
.email-settings .mail-heading p{font-size:12px;line-height:1.6;margin-bottom:16px}
.mail-layout{display:grid;grid-template-columns:minmax(0,1fr) 240px;gap:24px;align-items:start}
.email-settings .mail-fields{display:grid;gap:12px}
.email-settings label{margin:0;gap:5px;font-size:12px;min-width:0}
.email-settings input:not([type=checkbox]),.email-settings select{height:36px;min-width:0;max-width:100%;padding:7px 10px;font-size:13px}
.email-settings .subtle{font-size:12px;line-height:1.5}
.mail-identity-grid,.mail-auth-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;align-items:start}
.mail-connection-grid{display:grid;grid-template-columns:minmax(0,1fr) 80px 210px;gap:12px;align-items:start}
.mail-password-options{display:flex;flex-wrap:wrap;align-items:center;gap:8px 16px}
.email-settings .mail-password-options p{margin:0;flex:1 1 300px}
.mail-password-options .checkbox{white-space:nowrap}
.mail-actions{display:flex;align-items:center;flex-wrap:wrap;gap:10px 16px;margin-top:14px}
.email-settings .mail-actions .primary,.email-settings .mail-test .secondary{height:36px;min-height:36px;padding:7px 12px;font-size:12px}
.mail-test-area{border-left:1px solid #eaecf0;padding-left:24px;min-width:0}
.email-settings .mail-test{display:flex;flex-direction:column;align-items:stretch;gap:12px;padding:0;margin:0;border:0}
.email-settings .mail-test label{flex:none}
.email-settings .mail-test-area p{margin:12px 0 0}
@media(max-width:1199px){.mail-layout{grid-template-columns:minmax(0,1fr)}.mail-test-area{border-left:0;border-top:1px solid #eaecf0;padding:14px 0 0}.email-settings .mail-test{flex-direction:row;align-items:end}.email-settings .mail-test label{flex:0 1 320px}.email-settings .mail-test .secondary{width:auto}}
@media(max-width:700px){.email-settings{padding:16px}.mail-identity-grid,.mail-auth-grid,.mail-connection-grid{grid-template-columns:minmax(0,1fr)}.email-settings .mail-test{flex-direction:column;align-items:stretch}.email-settings .mail-test label{flex:none}.mail-actions{align-items:stretch}.mail-password-options .checkbox{white-space:normal}}
</style>
