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
 directory=mkdtempSync(join(tmpdir(),'fiftyapi-registration-'))
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

test('邮箱和真实姓名注册、零额度登录及管理员查看',async({page})=>{
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto(baseURL)
 await expect(page.getByRole('button',{name:'注册账号',exact:true})).toHaveCount(0)
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill(adminPassword)
 await page.getByLabel('初始化口令（Setup token）',{exact:true}).fill(setupToken)
 await page.getByRole('button',{name:'创建管理员',exact:false}).click()
 await page.getByRole('button',{name:'注册账号',exact:true}).click()
 await expect(page.getByRole('heading',{name:'注册账号',exact:true})).toBeVisible()
 for(const label of ['邮箱','真实姓名','密码'])await expect(page.getByLabel(label,{exact:true})).toHaveAttribute('required','')
 await page.getByLabel('邮箱',{exact:true}).fill('New.Member@Example.com')
 await page.getByLabel('真实姓名',{exact:true}).fill('注册测试姓名')
 await page.getByLabel('密码',{exact:true}).fill('registration-member-password')
 await expect(page.getByText('注册后初始额度为 0，请联系管理员分配使用额度。',{exact:true})).toBeVisible()
 await page.screenshot({path:'/tmp/fiftyapi-registration-desktop.png',fullPage:true})
 await page.setViewportSize({width:390,height:844})
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.screenshot({path:'/tmp/fiftyapi-registration-mobile.png',fullPage:true})
 await page.getByRole('button',{name:'注册',exact:false}).filter({hasText:/^注册\s*→/}).click()
 await expect(page.getByRole('heading',{name:'登录工作空间'})).toBeVisible()
 await expect(page.getByLabel('用户名',{exact:true})).toHaveValue('new.member@example.com')
 await expect(page.getByLabel('密码',{exact:true})).toHaveValue('')
 // Password managers can change the DOM value without Vue input events.
 await page.getByLabel('用户名',{exact:true}).evaluate(input=>{input.value='new.member@example.com'})
 await page.getByLabel('密码',{exact:true}).evaluate(input=>{input.value='registration-member-password'})
 await page.getByRole('button',{name:'登录',exact:false}).click()
 await expect(page.locator('.quota-card')).toHaveCount(2)
 const me=await (await page.request.get(baseURL+'/api/me')).json()
 expect(me.user).toMatchObject({role:'user',email:'new.member@example.com',display_name:'注册测试姓名',limit_5h_microusd:0,limit_7d_microusd:0})
 expect((await page.request.get(baseURL+'/api/admin/users')).status()).toBe(403)
 await page.setViewportSize({width:1440,height:1000})
 await page.getByRole('button',{name:'退出登录',exact:false}).click()
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill(adminPassword)
 await page.getByRole('button',{name:'登录',exact:false}).click()
 await page.locator('.nav-item').filter({hasText:'团队成员'}).click()
 const member=page.getByRole('row').filter({hasText:'new.member@example.com'})
 await expect(member).toContainText('注册测试姓名')
 await expect(member).toContainText('$0.00 / $0.00')
 await expect(member.locator('.meter')).toHaveCount(2)
 expect(errors).toEqual([])
})
