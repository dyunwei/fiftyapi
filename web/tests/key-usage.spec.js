import {test,expect} from '@playwright/test'
import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
const root=resolve(import.meta.dirname,'../../internal/server/ui')
const token='sk-'+'a'.repeat(43)
test.beforeEach(async({page})=>{
 await page.route('http://fiftyapi.test/**',async route=>{
  const url=new URL(route.request().url())
  if(url.pathname.startsWith('/api/')){
   const data={
    '/api/version':{version:'0.1.0'},'/api/setup/status':{initialized:true},
    '/api/me':{user:{id:1,username:'member',role:'member',enabled:true},csrf_token:'fixture'},
    '/api/quota':[],
    '/api/keys':route.request().method()==='POST'?{token}:[{id:1,name:'My client',prefix:'sk-aaaaaaaaa',suffix:'aaaa',copyable:true,enabled:true,created_at:1}],
    '/api/keys/1/reveal':{token},
    '/api/models':[{id:'team/codex',protocols:['responses']},{id:'team/claude',protocols:['messages']}]
   }[url.pathname]
   if(data===undefined)throw new Error('Unexpected API route: '+url.pathname)
   return route.fulfill({json:data})
  }
  const path=url.pathname==='/'?'index.html':url.pathname.slice(1)
  return route.fulfill({body:readFileSync(resolve(root,path)),contentType:path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':'text/html'})
 })
 await page.goto('http://fiftyapi.test/#keys')
})
test('Codex guide supports OS, model, WebSocket and HTTP clipboard fallback',async({page})=>{
 await page.getByRole('button',{name:'使用说明',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'使用 API 密钥'})
 await expect(dialog).toBeVisible()
 await expect(dialog.getByRole('button',{name:'保存',exact:true})).toHaveCount(0)
 await expect(dialog.getByLabel('模型 ID')).toHaveValue('team/codex')
 await expect(dialog.locator('pre').first()).toContainText('http://fiftyapi.test/v1')
 await dialog.getByLabel('完整 API 密钥').fill(token)
 await dialog.getByLabel('操作系统').selectOption('windows')
 await expect(dialog.locator('pre').last()).toContainText('$env:FIFTYAPI_API_KEY')
 await dialog.getByLabel('启用 WebSocket（需上游支持）').check()
 await expect(dialog.locator('pre').first()).toContainText('supports_websockets = true')
 await page.bringToFront()
 await dialog.getByRole('button',{name:'复制',exact:true}).last().click()
 await expect(page.getByRole('status')).toContainText('已复制')
 await page.evaluate(()=>{const field=document.createElement('textarea');field.id='paste-check';document.body.appendChild(field);field.focus()})
 await page.keyboard.press('Control+V')
 await expect(page.locator('#paste-check')).toHaveValue(`$env:FIFTYAPI_API_KEY = '${token}'\ncodex`)
 await page.locator('#paste-check').evaluate(el=>el.remove())
 await dialog.getByRole('button',{name:'Claude Code',exact:true}).click()
 await expect(dialog.getByLabel('模型 ID')).toHaveValue('team/claude')
 await expect(dialog.locator('pre')).toContainText("ANTHROPIC_BASE_URL = 'http://fiftyapi.test'")
 await dialog.getByRole('button',{name:'通用 API',exact:true}).click()
 await expect(dialog.locator('pre')).toContainText('curl.exe')
 await dialog.getByRole('button',{name:'关闭',exact:true}).last().click()
 await page.getByRole('button',{name:'使用密钥',exact:true}).click()
 await expect(page.getByLabel('完整 API 密钥')).toHaveValue(token)
 await expect(page.getByRole('dialog').locator('pre').last()).toContainText(token)
})
test('new key is carried into usage instructions only while the dialog is open',async({page})=>{
 await page.getByRole('button',{name:'＋ 创建密钥',exact:true}).click()
 await page.getByRole('dialog').getByRole('textbox').fill('New client')
 await page.getByRole('button',{name:'保存',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'使用密钥',exact:true}).click()
 await expect(page.getByLabel('完整 API 密钥')).toHaveValue(token)
 await expect(page.getByRole('dialog')).toContainText('New client')
 await page.keyboard.press('Escape')
 await page.getByRole('button',{name:'使用说明',exact:true}).click()
 await expect(page.getByLabel('完整 API 密钥')).toHaveValue('')
})
test('guide stays within a mobile viewport',async({page})=>{
 await page.setViewportSize({width:390,height:844})
 await page.getByRole('button',{name:'使用说明',exact:true}).click()
 await expect(page.getByRole('dialog')).toBeVisible()
 expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
 const box=await page.getByRole('dialog').boundingBox()
 expect(box.width).toBeLessThanOrEqual(390)
})

test('masked list copies the full key without rendering it',async({page})=>{
 await expect(page.locator('.key-token code')).toHaveText('sk-aaa...aaaa')
 await expect(page.getByText(token,{exact:true})).toHaveCount(0)
 await page.bringToFront()
 await page.getByRole('button',{name:'复制密钥',exact:true}).click()
 await expect(page.getByRole('status')).toContainText('已复制')
 await page.evaluate(()=>{const el=document.createElement('textarea');el.id='pasted';document.body.appendChild(el);el.focus()})
 await page.keyboard.press('Control+V')
 await expect(page.locator('#pasted')).toHaveValue(token)
})
