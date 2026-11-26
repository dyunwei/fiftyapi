<script setup>
import { computed } from 'vue'
import { t } from './i18n.js'
import { requestProtocol, requestTransport } from './request-diagnostics.js'

const props = defineProps({ log: { type: Object, required: true }, date: { type: Function, required: true } })
const emit = defineEmits(['copy'])
const diagnostics = computed(() => props.log.diagnostics)
const advice = {
  protocol_mismatch: '按模型支持的 API 协议重新配置客户端。',
  model_access: '选择有权限的模型，或联系管理员调整访问权限。',
  model_unavailable: '重新读取模型列表并选择可用模型；需要此模型时请联系管理员。',
  local_quota: '检查下方受限的额度窗口。若没有重置时间或需要继续调用，请联系管理员。',
  user_concurrency: '当前请求数已达个人并发上限。等其他请求结束后再试。',
  pending_usage: '该历史请求因待核算被拒绝，可以重新发起调用。',
  upstream_rate_limit: '上游暂时限制调用。请在建议时间后重试；若持续发生，请联系管理员。',
  client_websocket_handshake: '客户端与网关的 WebSocket 握手失败。请确认客户端已启用 WebSocket，并检查反向代理的升级设置。',
  websocket_connection_limit_reached: 'WebSocket 握手时连接数已满。关闭不用的客户端会话，或等待名额释放后重试。',
  upstream_websocket_handshake: '网关未能与上游建立 WebSocket。请把诊断发给管理员检查上游配置。',
  stream_interrupted: '流式响应未完成。请先检查已有输出和结算状态，再决定是否重试。',
  client_cancelled: '连接在处理完成前已取消。若未主动取消，请检查客户端超时与连接。',
  route_unavailable: '当前模型没有可用路由。请稍后重试或联系管理员检查模型路由状态。',
  upstream_unavailable: '请求未成功发往上游。请稍后重试；若持续发生，请联系管理员。',
  upstream_error: '请求在上游阶段失败。若持续发生，请将诊断发给管理员。',
}
const fields = computed(() => {
  const d = diagnostics.value
  const rows = [
    [t('请求 ID'), props.log.id],
    [t('API 协议'), requestProtocol(d?.protocol)],
    [t('客户端 → 网关'), requestTransport(d?.client_transport)],
    [t('网关 → 上游'), requestTransport(d?.upstream_transport, d && !d.dispatched ? '未发送' : '未记录')],
    [t('状态'), props.log.status || t('未记录')],
  ]
  if (d?.connection_limit) rows.push([t('握手时的连接数 / 上限'), `${d.connection_limit.used} / ${d.connection_limit.limit}`])
  if (d?.dispatched) {
    rows.push([t('输出状态'), t(d.output_started ? '已观察到输出' : '未观察到输出')])
    rows.push([t('请求完成状态'), t(d.completed ? '已完成' : '未完成')])
  }
  if (d?.retry_at) rows.push([t('建议重试时间'), props.date(d.retry_at)])
  for (const block of d?.quota || []) {
    rows.push([t(block.window === '5h' ? '5 小时额度窗口' : '7 天额度窗口'), block.reset_at ? props.date(block.reset_at) : t('等待在途请求结束或联系管理员')])
  }
  if (props.log.settlement_status) {
    const labels = { pending: '结算异常', settled: '已结算', inflight: '处理中' }
    rows.push([t('结算状态'), t(labels[props.log.settlement_status] || '未记录')])
  }
  return rows
})
const nextStep = computed(() => advice[diagnostics.value?.code] ? t(advice[diagnostics.value.code]) : '')
function copyDiagnostics() {
  const lines = fields.value.map(([label, value]) => label + ' · ' + value)
  if (nextStep.value) lines.push(nextStep.value)
  emit('copy', lines.join('\n'))
}
</script>

<template>
  <dl class="request-diagnostics">
    <div v-for="[label, value] in fields" :key="label">
      <dt>{{ label }}</dt>
      <dd class="break">{{ value }}</dd>
    </div>
  </dl>
  <p v-if="nextStep" class="notice">{{ nextStep }}</p>
  <button type="button" class="secondary" @click="copyDiagnostics">{{ t('复制诊断') }}</button>
</template>

<style scoped>
.request-diagnostics { display: grid; gap: 0.5rem; margin: 0 0 0.75rem; }
.request-diagnostics > div { display: grid; grid-template-columns: minmax(7rem, 1fr) minmax(0, 2fr); gap: 0.75rem; }
.request-diagnostics dd { margin: 0; }
.notice { margin-bottom: 0.75rem; }
button { margin-bottom: 1rem; }
</style>
