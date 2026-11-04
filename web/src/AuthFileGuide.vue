<script setup>
import { copyText } from './clipboard.js'
import { t, locale } from './i18n.js'
import {computed,ref} from 'vue'
const props=defineProps({provider:{type:String,required:true}})
const platform=typeof navigator==='undefined'?'':navigator.platform.toLowerCase()
const system=ref(platform.includes('win')?'windows':platform.includes('mac')?'macos':'linux')
const systems={windows:'Windows',macos:'macOS',linux:'Linux'}
const labels={openai:'Codex',claude:'Claude Code',antigravity:'Antigravity'}
const folders={openai:{windows:'%USERPROFILE%\\.codex',macos:'~/.codex',linux:'~/.codex'},claude:{windows:'%USERPROFILE%\\.claude',macos:'~/.claude',linux:'~/.claude'}}
const folder=computed(()=>folders[props.provider]?.[system.value]||({windows:'%USERPROFILE%\\Downloads',macos:'~/Downloads',linux:'~/Downloads'}[system.value]))
const filename=computed(()=>props.provider==='openai'?'auth.json':'.credentials.json')
const copied=ref(false),copyError=ref('')
async function copy(){copyError.value='';try{await copyText(folder.value);copied.value=true;setTimeout(()=>copied.value=false,2000)}catch{copyError.value=t('请手动选择并复制上方路径。')}}
</script>
<template>
 <aside class="auth-file-guide" :aria-label="t('授权文件操作说明')"><div class="contribution-step-heading"><span>1</span><h2>{{ t('找到你的授权文件') }}</h2></div><p class="subtle">{{ labels[provider] }} {{ t('· 选择登录账号时使用的系统') }}</p>
 <div class="auth-system-options" role="group" :aria-label="t('操作系统')"><button v-for="(label,key) in systems" :key="key" type="button" :aria-pressed="system===key" @click="system=key;copied=false;copyError=''">{{ label }}</button></div>
 <template v-if="provider==='openai'||provider==='claude'">
 <p v-if="provider==='openai'">{{ t('先在这台电脑的 Codex 中使用自己的 ChatGPT 账号登录。') }}</p><p v-else>{{ t('先在这台电脑的 Claude Code 中使用自己的 Claude 账号登录。') }}</p>
 <template v-if="provider==='claude'&&system==='macos'"><div class="notice">{{ t('macOS 通常把 Claude 授权保存在系统钥匙串中，没有可直接选择的授权文件。') }}</div><p>{{ t('如果你已有导出的授权 JSON，可直接上传。若使用的是文件存储，文件可能位于下面的目录；找不到时请联系管理员协助导出。') }}</p></template>
 <ol class="auth-file-instructions"><li v-if="system==='windows'">{{ t('按') }} <kbd>Win</kbd> + <kbd>R</kbd>{{ t('，粘贴下面的目录，按回车打开。') }}</li><li v-else-if="system==='macos'">{{ t('打开 Finder，按') }} <kbd>⌘</kbd> + <kbd>⇧</kbd> + <kbd>G</kbd>{{ t('，粘贴下面的目录，按回车。') }}</li><li v-else>{{ t('打开文件管理器，进入个人主目录，按') }} <kbd>Ctrl</kbd> + <kbd>H</kbd> {{ t('显示隐藏文件夹，再打开') }} <code>{{ provider==='openai'?'.codex':'.claude' }}</code>。</li></ol>
 <div class="auth-folder"><code>{{ folder }}</code><button type="button" class="text-button" @click="copy">{{ copied?t('已复制'):t('复制目录') }}</button></div><p v-if="copyError" class="subtle" role="status">{{ t(copyError) }}</p>
 <p>{{ t('在上传区域选择这个目录中的') }} <strong>{{ filename }}</strong>{{ t('。无需打开或修改文件内容。') }}</p><p v-if="provider==='claude'" class="subtle">{{ t('文件名以点开头。若文件选择窗口中看不到它，') }}{{ system==='macos'?t('按 ⌘ + ⇧ + . 显示隐藏文件'):system==='linux'?t('按 Ctrl + H 显示隐藏文件'):t('可在文件名输入框中直接填写 .credentials.json') }}。</p>
 <details class="auth-file-help"><summary>{{ t('找不到文件？') }}</summary><template v-if="provider==='openai'"><p>{{ t('如果设置了') }} <code>CODEX_HOME</code>{{ t('，请到该目录查找') }} <code>auth.json</code>{{ t('。在 WSL 中登录的账号，请按 Linux 说明查找 WSL 内的文件。') }}</p><p>{{ t('若授权保存在系统凭据库中，默认目录可能没有文件。可在 Codex 的') }} <code>config.toml</code> {{ t('中设置') }} <code>cli_auth_credentials_store = "file"</code>{{ t('，再运行') }} <code>codex login</code> {{ t('完成 ChatGPT 登录；不确定时请联系管理员。') }}</p><p>{{ t('仅使用 API Key 登录生成的文件不能用于此入口。') }}</p></template><template v-else><p>{{ t('如果设置了') }} <code>CLAUDE_CONFIG_DIR</code>{{ t('，请在该目录查找。macOS 默认使用钥匙串，Windows 的 WSL 登录请到 Linux 环境查找。') }}</p><p>{{ t('找不到文件或导入失败时，请联系管理员协助处理。') }}</p></template></details>
 <a class="text-button auth-doc-link" :href="provider==='openai'?'https://developers.openai.com/codex/auth/':'https://code.claude.com/docs/en/authentication'" target="_blank" rel="noopener noreferrer">{{ provider==='openai'?t('OpenAI 官方授权说明'):t('Claude 官方授权说明') }} ↗</a>
 </template>
 <template v-else><p>{{ t('Antigravity 没有可统一查找的') }} <code>auth.json</code> {{ t('文件。请从你正在使用的账号工具中导出自己的 OAuth 授权 JSON。') }}</p><ol class="auth-file-instructions"><li>{{ t('在账号工具中找到「导出账号」或「导出授权」，选择 JSON 格式。') }}</li><li>{{ t('保存文件，通常可以在「下载」目录中找到。') }}</li><li>{{ t('回到本页，选择刚导出的 JSON 文件并提交。') }}</li></ol><p class="subtle">{{ t('默认下载目录（如已更改，请到你选择的目录查找）：') }}</p><div class="auth-folder"><code>{{ folder }}</code><button type="button" class="text-button" @click="copy">{{ copied?t('已复制'):t('复制目录') }}</button></div><p v-if="copyError" class="subtle" role="status">{{ t(copyError) }}</p><div class="notice">{{ t('没有导出入口？请联系管理员协助获取可导入的文件。请勿上传设置文件或本地数据库。') }}</div></template>
 </aside>
</template>
