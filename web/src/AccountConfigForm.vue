<script setup>
import { t, locale } from './i18n.js'
import { computed, watchEffect } from 'vue'
import GatewayFields from './GatewayFields.vue'
import HelpTip from './HelpTip.vue'
const config=defineModel({required:true})
defineProps({provider:String,creating:Boolean})
watchEffect(()=>{
 const policy=config.value.inference_policy ||= {}
 policy.max_effort ??= '';policy.max_thinking_tokens ??= 0;policy.force_standard ??= false
})
const toggleOptions=computed(()=>[{value:null,label:t('使用默认值')},{value:true,label:t('启用')},{value:false,label:t('关闭')}])
</script>
<template>
 <div class="form-help"><span>{{ t('配置帮助') }}</span><HelpTip :label="t('配置帮助')" :text="t('留空或选择默认值即可沿用平台配置。保存后需要重新验证并同步模型。代理密码显示为 ***，原样保留即可保持原密码。')"/></div>
 <GatewayFields v-if="provider==='gateway'" v-model="config" :creating="creating"/>
 <fieldset class="config-section"><legend><span class="label-heading">{{ t('连接设置') }}<HelpTip :label="t('配置帮助')+': '+t('连接设置')" :text="t('最大并发、优先级和等待队列统一在账号的「调度与健康」中设置。')"/></span></legend>
  <label v-if="provider!=='gateway'"><span class="label-heading">{{ t('推理地址') }}<HelpTip :label="t('配置帮助')+': '+t('推理地址')" :text="t('填写完整推理接口地址。只覆盖推理，授权和模型目录仍使用平台端点。')"/></span><input :aria-label="t('推理地址')" v-model="config.gateway_url" type="url" :placeholder="t('留空使用平台默认 HTTPS 地址')"></label>
  <label><span class="label-heading">{{ t('代理地址') }}<HelpTip :label="t('配置帮助')+': '+t('代理地址')" :text="t('用于授权、刷新、模型目录和推理；留空沿用部署环境代理。')"/></span><input :aria-label="t('代理地址')" v-model="config.proxy_url" :placeholder="t('http:// 或 socks5://')"></label>
  <label><span class="label-heading">{{ t('请求总超时（秒）') }}<HelpTip :label="t('配置帮助')+': '+t('请求总超时（秒）')" :text="t('0 使用默认值：180 秒')"/></span><input :aria-label="t('请求总超时（秒）')" v-model="config.timeout_seconds" type="number" min="0" max="600" step="1" required></label>
 </fieldset>
 <fieldset class="config-section"><legend>{{ t('额度保护') }}</legend>
  <label><span class="label-heading">{{ t('推理强度上限') }}<HelpTip :label="t('配置帮助')+': '+t('推理强度上限')" :text="t('保留较低档位，超过上限时降档；未指定时使用上限。适用于 Codex 和 Claude 自适应思考；旧版 Claude 显式思考请设置 token 预算。')"/><HelpTip v-if="provider==='antigravity'" :label="t('配置帮助')+': Antigravity'" :text="t('Antigravity 将 Low / Medium / High 分别映射为最多 2048 / 8192 / 24576 思考 token；与预算上限同时设置时取较小值。这是网关预算策略，不代表厂商档位等价。')"/></span><select :aria-label="t('推理强度上限')" v-model="config.inference_policy.max_effort"><option value="">{{ t('不限制') }}</option><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option></select></label>
  <label><span class="label-heading">{{ t('思考 token 预算上限') }}<HelpTip :label="t('配置帮助')+': '+t('思考 token 预算上限')" :text="t('0 不限制，启用时至少 1024。用于 Claude 显式思考和 Antigravity；Claude 自适应思考请设置强度上限，Codex 不支持独立思考 token 硬上限。')"/></span><input :aria-label="t('思考 token 预算上限')" v-model="config.inference_policy.max_thinking_tokens" type="number" min="0" max="1048576" step="1"></label>
  <label class="checkbox"><input :aria-label="t('强制标准速度')" v-model="config.inference_policy.force_standard" type="checkbox">{{ t('强制标准速度') }}<HelpTip :label="t('配置帮助')+': '+t('强制标准速度')" :text="t('覆盖 Codex 快速档位、移除 Claude Fast 并使用标准服务。Antigravity 当前转换链路不传递 Fast 参数。')"/></label>
 </fieldset>
 <fieldset v-if="provider==='antigravity'" class="config-section"><legend>Antigravity</legend>
  <label>{{ t('客户端版本') }}<input :aria-label="t('客户端版本')" v-model="config.antigravity_user_agent_version" pattern="\d+\.\d+\.\d+" :placeholder="t('默认 2.23.0')"></label>
  <label><span class="label-heading">{{ t('身份提示处理') }}<HelpTip :label="t('配置帮助')+': '+t('身份提示处理')" :text="t('默认启用，沿用 Antigravity 转换器的身份处理。')"/></span><select :aria-label="t('身份提示处理')" v-model="config.identity_patch_enabled"><option v-for="o in toggleOptions" :key="String(o.value)" :value="o.value">{{ o.label }}</option></select></label>
  <label>{{ t('自定义身份提示') }}<textarea :aria-label="t('自定义身份提示')" v-model="config.antigravity_identity_prompt" rows="3" maxlength="16384" :placeholder="t('留空使用转换器默认提示')"></textarea></label>
  <label><span class="label-heading">{{ t('备用端点') }}<HelpTip :label="t('配置帮助')+': '+t('备用端点')" :text="t('用于项目发现和模型目录；不自动重放推理请求。')"/></span><textarea :aria-label="t('备用端点')" v-model="config.fallback_urls_text" rows="2" :placeholder="t('每行一个 HTTPS 源站地址')"></textarea></label>
 </fieldset>
 <fieldset v-else-if="provider!=='gateway'" class="config-section"><legend>{{ provider==='claude'?'Claude Code':'OpenAI Codex' }}</legend>
  <label>{{ t('客户端版本') }}<input :aria-label="t('客户端版本')" v-model="config.client_version" maxlength="64" pattern="[0-9]+[.][0-9]+[.][0-9]+(-[0-9A-Za-z]+([.][0-9A-Za-z]+)*)?" :placeholder="provider==='claude'?t('默认 2.1.258'):t('默认 0.164.0')"></label>
  <label v-if="provider==='openai'"><span class="label-heading">{{ t('工作空间账号 ID（可选）') }}<HelpTip :label="t('配置帮助')+': '+t('工作空间账号 ID（可选）')" :text="t('多工作空间账号可显式指定；仅对拥有对应权限的授权有效。')"/></span><input :aria-label="t('工作空间账号 ID（可选）')" v-model="config.openai_account_id" maxlength="128" pattern="[A-Za-z0-9_-]+" :placeholder="t('留空使用授权令牌中的账号')"></label>
  <label>User-Agent<input aria-label="User-Agent" v-model="config.user_agent" :placeholder="t('留空按平台和客户端版本生成')"></label>
  <template v-if="provider==='claude'">
   <label><span class="label-heading">{{ t('透传客户端 metadata') }}<HelpTip :label="t('配置帮助')+': '+t('透传客户端 metadata')" :text="t('默认关闭，只发送规范化的 user_id。')"/></span><select :aria-label="t('透传客户端 metadata')" v-model="config.metadata_passthrough"><option v-for="o in toggleOptions" :key="String(o.value)" :value="o.value">{{ o.label }}</option></select></label>
   <label><span class="label-heading">{{ t('统一账号指纹') }}<HelpTip :label="t('配置帮助')+': '+t('统一账号指纹')" :text="t('默认启用；metadata 完全透传时不执行。')"/></span><select :aria-label="t('统一账号指纹')" v-model="config.fingerprint_unification"><option v-for="o in toggleOptions" :key="String(o.value)" :value="o.value">{{ o.label }}</option></select></label>
   <label><span class="label-heading">{{ t('补充 Claude Code 系统提示') }}<HelpTip :label="t('配置帮助')+': '+t('补充 Claude Code 系统提示')" :text="t('默认启用，保留客户端已有系统提示。')"/></span><select :aria-label="t('补充 Claude Code 系统提示')" v-model="config.claude_system_prompt_injection"><option v-for="o in toggleOptions" :key="String(o.value)" :value="o.value">{{ o.label }}</option></select></label>
   <label>{{ t('附加系统提示') }}<textarea :aria-label="t('附加系统提示')" v-model="config.claude_oauth_system_prompt" rows="3" maxlength="16384"></textarea></label>
  </template>
 </fieldset>
 <details class="config-section"><summary>{{ t('高级请求头') }}</summary><p class="subtle">{{ t('每行填写「名称: 值」，不能覆盖认证凭据或连接设置。') }}</p>
  <label>{{ t('推理请求头') }}<textarea :aria-label="t('推理请求头')" v-model="config.gateway_headers_text" rows="3" spellcheck="false" placeholder="X-Custom-Header: value"></textarea></label>
  <label>{{ t('模型目录请求头') }}<textarea :aria-label="t('模型目录请求头')" v-model="config.models_headers_text" rows="3" spellcheck="false" placeholder="X-Custom-Header: value"></textarea></label>
 </details>
</template>
