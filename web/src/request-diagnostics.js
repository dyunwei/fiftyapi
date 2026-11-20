import { t } from './i18n.js'

const protocols = { responses: 'Responses', messages: 'Messages', chat: 'Chat Completions', antigravity: 'Antigravity v1internal' }
const transports = { http: 'HTTP', http_json: 'HTTP JSON', http_sse: 'HTTP SSE', websocket: 'WebSocket' }

export function requestTransport(value, fallback = '未记录') {
  return transports[value] || t(fallback)
}

export function requestProtocol(value) {
  return protocols[value] || t('未记录')
}

export function requestConnection(diagnostics) {
  const mode = requestTransport(diagnostics?.client_transport)
  return diagnostics?.protocol ? requestProtocol(diagnostics.protocol) + ' · ' + mode : mode
}
