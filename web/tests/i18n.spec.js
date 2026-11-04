import {test,expect} from '@playwright/test'
import {spawn} from 'node:child_process'
import {mkdtempSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {randomBytes} from 'node:crypto'
import net from 'node:net'

let service,directory,baseURL
const setupToken=randomBytes(32).toString('hex'),adminPassword='registration-admin-password'
test.beforeAll(async()=>{
 directory=mkdtempSync(join(tmpdir(),'fiftyapi-i18n-'))
 const listener=net.createServer()
 await new Promise(resolve=>listener.listen(0,'127.0.0.1',resolve))
 const port=listener.address().port
 await new Promise(resolve=>listener.close(resolve))
 baseURL=`http://127.0.0.1:${port}`
 service=spawn(process.env.FIFTYAPI_TEST_BINARY||'/tmp/fiftyapi-server',['serve'],{env:{...process.env,FIFTYAPI_MASTER_KEY:randomBytes(32).toString('base64'),FIFTYAPI_SETUP_TOKEN:setupToken,FIFTYAPI_DB_PATH:join(directory,'app.db'),FIFTYAPI_LISTEN:`127.0.0.1:${port}`,FIFTYAPI_COOKIE_SECURE:'false',FIFTYAPI_PUBLIC_URL:baseURL,FIFTYAPI_SMTP_HOST:''},stdio:'ignore'})
 for(let i=0;i<100;i++){try{if((await fetch(baseURL+'/healthz')).ok)return}catch{}await new Promise(resolve=>setTimeout(resolve,100))}
 throw new Error('注册测试服务未就绪')
})
test.afterAll(async()=>{
 service?.kill('SIGTERM')
 if(service?.exitCode===null)await new Promise(resolve=>{service.once('exit',resolve);setTimeout(()=>{service.kill('SIGKILL');resolve()},12000).unref()})
 if(directory)rmSync(directory,{recursive:true,force:true})
})

test('English setup, errors, admin pages, live switch and public pages',async({browser})=>{
 const context=await browser.newContext({locale:'en-US'})
 const page=await context.newPage(),errors=[]
 page.on('pageerror',error=>errors.push(error.message))
 await page.goto(baseURL)
 await expect(page.locator('html')).toHaveAttribute('lang','en')
 await expect(page).toHaveTitle('FiftyAPI · Team model gateway')
 await page.getByLabel('Username',{exact:true}).fill('admin')
 await page.getByLabel('Password',{exact:true}).fill(adminPassword)
 await page.getByLabel('Setup token',{exact:true}).fill(setupToken)
 await page.getByRole('button',{name:'Create administrator',exact:false}).click()
 await expect(page.getByRole('heading',{name:'Sign in to workspace'})).toBeVisible()
 await page.getByLabel('Password',{exact:true}).fill('wrong-password')
 await page.getByRole('button',{name:'Sign in',exact:false}).click()
 await expect(page.locator('.alert.error')).toHaveText('Incorrect account or password')
 await page.getByLabel('Password',{exact:true}).fill(adminPassword)
 await page.getByRole('button',{name:'Sign in',exact:false}).click()
 await expect(page.getByRole('heading',{name:'Admin console',exact:true})).toBeVisible()
 await page.screenshot({path:'/tmp/fiftyapi-overview-en.png',fullPage:true,animations:'disabled'})
 for(const label of ['API keys','Request logs','Upstream accounts','Team members','Models & pricing','Runtime logs','Audit log','System settings']){
  await page.locator('.nav-item').filter({hasText:label}).click()
  await expect(page.locator('.header-page h1')).toHaveText(label)
 }
 await page.getByRole('tab',{name:'Backups',exact:true}).click()
 await expect(page.getByRole('heading',{name:'S3 storage settings'})).toBeVisible()
 await page.getByLabel('Language / 语言').selectOption('zh-CN')
 await expect(page.getByRole('tab',{name:'备份',exact:true})).toBeVisible()
 await expect(page.getByRole('heading',{name:'S3 存储配置'})).toBeVisible()
 await expect(page.getByRole('tab',{name:'邮件配置',exact:true})).toBeVisible()
 await page.reload()
 await expect(page.locator('html')).toHaveAttribute('lang','zh-CN')
 await page.getByLabel('Language / 语言').selectOption('en')
 await page.locator('.nav-item').filter({hasText:'Upstream accounts'}).click()
 await page.locator('.heading-actions').getByRole('button',{name:'+ Connect upstream API',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Connect API key gateway'})
 await expect(dialog).toBeVisible()
 await dialog.getByLabel('Gateway name',{exact:true}).fill('测试团队')
 await page.getByLabel('Language / 语言').selectOption('zh-CN')
 await expect(page.getByLabel('网关名称',{exact:true})).toHaveValue('测试团队')
 await page.getByLabel('Language / 语言').selectOption('en')
 await expect(dialog.getByLabel('Gateway name',{exact:true})).toHaveValue('测试团队')
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click()
 await page.setViewportSize({width:390,height:844})
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.screenshot({path:'/tmp/fiftyapi-english-mobile.png',fullPage:true,animations:'disabled'})
 await page.goto(baseURL+'/#invite=invalid')
 await expect(page.getByRole('heading',{name:'Set sign-in password'})).toBeVisible()
 await expect(page.getByRole('alert')).toContainText('Invitation link is invalid')
 await page.goto(baseURL+'/#contribute=invalid')
 await expect(page.getByRole('heading',{name:'Contribute account',exact:true})).toBeVisible()
 await expect(page.getByRole('alert')).toContainText('Link expired or disabled')
 expect(errors).toEqual([])
 await context.close()
})
