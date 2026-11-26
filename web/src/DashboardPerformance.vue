<script setup>
import { computed } from 'vue'
import { locale, t } from './i18n.js'

const props = defineProps({ metrics: { type: Object, required: true } })
const rows = [
  ['sqlite_wait', 'SQLite 连接等待'],
  ['account_queue', '上游账号排队'],
  ['first_output', '流式首段输出'],
]
const formatter = computed(() => new Intl.NumberFormat(locale.value, { maximumFractionDigits: 2 }))
function percentiles(sample) {
  if (!sample?.samples) return t('暂无样本')
  const ms = n => n < 0 ? '>120000' : formatter.value.format(n)
  return t('P50 {0} ms · P95 {1} ms · {2} 次', [ms(sample.p50_ms), ms(sample.p95_ms), formatter.value.format(sample.samples)])
}
function summary(sample) {
  if (!sample?.samples) return t('暂无样本')
  return t('{0} ms · {1} 次', [formatter.value.format(sample.average_ms), formatter.value.format(sample.samples)])
}
</script>

<template>
  <details class="dashboard-performance">
    <summary>{{ t('耗时指标') }}</summary>
    <div class="panel performance-values">
      <p class="subtle">{{ t('本次服务启动以来的平均耗时与样本数') }}</p>
      <dl>
        <div v-for="[key, label] in rows" :key="key">
          <dt>{{ t(label) }}</dt>
          <dd>{{ summary(metrics[key]) }}</dd>
        </div>
      </dl>
      <p class="subtle">{{ t('近一小时首段输出（近似值）') }}</p>
      <dl>
        <div><dt>SSE</dt><dd>{{ percentiles(metrics.sse_last_hour) }}</dd></div>
        <div><dt>WebSocket</dt><dd>{{ percentiles(metrics.websocket_last_hour) }}</dd></div>
      </dl>
    </div>
  </details>
</template>

<style scoped>
:global(.panel.dashboard-filter) { position: relative; overflow: visible; }
.dashboard-performance { font-size: 13px; }
summary { cursor: pointer; white-space: nowrap; }
.performance-values {
  position: absolute;
  z-index: 5;
  top: calc(100% + 0.5rem);
  right: 0;
  margin: 0;
  padding: 0.875rem;
  width: max-content;
  max-width: min(19rem, calc(100vw - 3rem));
  box-shadow: 0 6px 20px #1a2b3728;
}
dl { display: grid; gap: 0.5rem; margin: 0.65rem 0 0; }
dl > div { display: flex; gap: 0.8rem; justify-content: space-between; flex-wrap: wrap; }
dd { margin: 0; font-variant-numeric: tabular-nums; }
</style>
