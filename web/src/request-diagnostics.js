import { t } from './i18n.js'

const transports = { http: 'HTTP', http_json: 'HTTP JSON', http_sse: 'HTTP SSE', websocket: 'WebSocket' }

export function requestTransport(value, fallback = '未记录') {
  return transports[value] || t(fallback)
}
