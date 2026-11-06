<script setup>
import { computed } from 'vue'
import { t } from './i18n.js'

const props = defineProps({ log: { type: Object, required: true } })
const reasoning = computed(() => {
  const l = props.log
  const parts = [l.reasoning_effort, l.thinking_type ? `thinking: ${l.thinking_type}` : '']
  if (l.thinking_budget_tokens != null) parts.push(t('预算 {0} tokens', [l.thinking_budget_tokens]))
  return parts.filter(Boolean).join(' · ') || t('未指定')
})
const speed = computed(() => [
  props.log.speed ? `speed: ${props.log.speed}` : '',
  props.log.service_tier ? `service_tier: ${props.log.service_tier}` : '',
].filter(Boolean).join(' · ') || t('未指定'))
</script>

<template>
  <span :title="t('客户端请求参数；账号策略可能调整，上游实际执行档位未确认。历史日志无此信息。')">
    <small class="cell-note">{{ t('请求推理') }}: {{ reasoning }}</small>
    <small class="cell-note">{{ t('请求速度') }}: {{ speed }}</small>
  </span>
</template>
