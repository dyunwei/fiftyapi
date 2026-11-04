import { ref } from 'vue'
import en from './locales/en.json' with { type: 'json' }
import diagnostics from '../../internal/i18n/en.json' with { type: 'json' }

export const localeKey = 'fiftyapi.locale'
export function resolveLocale(saved, languages = []) {
  if (saved === 'zh-CN' || saved === 'en') return saved
  for (const language of languages) {
    if (/^zh\b/i.test(language)) return 'zh-CN'
    if (/^en\b/i.test(language)) return 'en'
  }
  return 'en'
}
let saved
try { saved = globalThis.localStorage?.getItem(localeKey) } catch { /* Storage may be disabled. */ }
export const locale = ref(resolveLocale(saved, globalThis.navigator?.languages || [globalThis.navigator?.language || 'en']))

// Message keys use the original Chinese text. Unknown upstream messages remain verbatim.
export function t(message, values = []) {
  if (message == null) return ''
  const source = String(message)
  let translated = source
  if (locale.value === 'en') {
    translated = en[source] ?? diagnostics[source] ?? source
    if (translated === source) {
      for (const { pattern, english } of diagnosticFormats) {
        const match = source.match(pattern)
        if (match) {
          let index = 0
          translated = english.replace(/%[dswvt]/g, () => match[++index])
          break
        }
      }
    }
    if (translated === source) {
      for (const [prefix, english] of diagnosticPrefixes) {
        if (source.startsWith(prefix)) { translated = english + t(source.slice(prefix.length)); break }
      }
    }
  }
  return translated.replace(/\{(\d+)\}/g, (match, index) => values[index] === undefined ? match : String(values[index]))
}
const diagnosticPrefixes = Object.entries(diagnostics)
  .filter(([key]) => /[：（]$/.test(key))
  .sort((a, b) => b[0].length - a[0].length)

const escapePattern = value => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
const diagnosticFormats = Object.entries(diagnostics)
  .filter(([key]) => /%[dswvt]/.test(key))
  .map(([key, english]) => ({
    pattern: new RegExp('^' + key.split(/%[dswvt]/).map(escapePattern).join('([\\s\\S]*?)') + '$'), english
  }))

export function setLocale(value) {
  locale.value = resolveLocale(value)
  try { globalThis.localStorage?.setItem(localeKey, locale.value) } catch { /* Switching still works without storage. */ }
  applyDocumentLocale()
}
export function applyDocumentLocale() {
  if (typeof document === 'undefined') return
  document.documentElement.lang = locale.value
  document.title = locale.value === 'en' ? 'FiftyAPI · Team model gateway' : 'FiftyAPI · 团队模型网关'
}
applyDocumentLocale()
