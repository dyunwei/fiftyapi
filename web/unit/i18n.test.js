import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { parse } from '@vue/compiler-sfc'
import { parse as parseJS } from '@babel/parser'
import { locale, resolveLocale, setLocale, t } from '../src/i18n.js'
const en = JSON.parse(readFileSync(new URL('../src/locales/en.json', import.meta.url)))
const diagnostics = JSON.parse(readFileSync(new URL('../../internal/i18n/en.json', import.meta.url)))

test('language selection uses saved preference, browser preferences and English fallback', () => {
 assert.equal(resolveLocale('zh-CN', ['en-US']), 'zh-CN')
 assert.equal(resolveLocale('en', ['zh-CN']), 'en')
 assert.equal(resolveLocale('invalid', ['fr', 'zh-TW', 'en']), 'zh-CN')
 assert.equal(resolveLocale(null, ['en-GB', 'zh']), 'en')
 assert.equal(resolveLocale(null, ['fr']), 'en')
})
test('translation interpolates without modifying unknown upstream text', () => {
 setLocale('en')
 assert.equal(t('网关 #{0} · {1}', [3, 'team']), 'Gateway #3 · team')
 assert.equal(t('账号或密码错误'), 'Incorrect account or password')
 assert.equal(t('每日 OpenAI 测试下次执行：2026-11-04'), 'Next daily OpenAI test:2026-11-04')
 assert.equal(t('S3 操作失败（HTTP 403），请检查区域、存储桶和读写删除权限'), 'S3 operation failed (HTTP 403). Check region, bucket and read/write/delete permissions.')
 assert.equal(t('user model-id / custom upstream message'), 'user model-id / custom upstream message')
 setLocale('zh-CN')
 assert.equal(t('网关 #{0} · {1}', [3, 'team']), '网关 #3 · team')
 assert.equal(locale.value, 'zh-CN')
})
test('catalogs preserve interpolation placeholders and contain English translations', () => {
 for (const [key, value] of Object.entries({...diagnostics, ...en})) {
  assert.ok(value.length > 0, key)
  assert.doesNotMatch(value, /[\u3400-\u9fff]/, key)
  assert.deepEqual([...key.matchAll(/\{\d+\}/g)].map(m=>m[0]).sort(), [...value.matchAll(/\{\d+\}/g)].map(m=>m[0]).sort(), key)
 }
})
test('every static t() key in Vue components has an English translation', () => {
 for (const file of readdirSync(new URL('../src/', import.meta.url)).filter(name=>name.endsWith('.vue'))) {
  const source=readFileSync(new URL('../src/'+file,import.meta.url),'utf8')
  // Parse scripts; template expressions are validated by the production build.
  const {descriptor}=parse(source)
  if(descriptor.scriptSetup)parseJS(descriptor.scriptSetup.content,{sourceType:'module'})
  for(const match of source.matchAll(/\bt\('((?:[^'\\]|\\.)*)'/g)) {
   const key=match[1].replace(/\\'/g,"'").replace(/\\\\/g,'\\')
   assert.ok(Object.hasOwn(en,key)||Object.hasOwn(diagnostics,key),`${file}: ${key}`)
  }
 }
})
