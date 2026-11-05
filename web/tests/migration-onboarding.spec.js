import {test,expect} from '@playwright/test'
import {spawn,execFileSync} from 'node:child_process'
import {mkdtempSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {randomBytes} from 'node:crypto'
import net from 'node:net'

let service,directory,baseURL
const setupToken=randomBytes(32).toString('hex'),password='migration-admin-password'
test.beforeAll(async()=>{
 directory=mkdtempSync(join(tmpdir(),'fiftyapi-migration-'))
 const listener=net.createServer();await new Promise(resolve=>listener.listen(0,'127.0.0.1',resolve))
 const port=listener.address().port;await new Promise(resolve=>listener.close(resolve));baseURL=`http://127.0.0.1:${port}`
 service=spawn(process.env.FIFTYAPI_TEST_BINARY||'/tmp/fiftyapi-server',['serve'],{env:{...process.env,FIFTYAPI_MASTER_KEY:randomBytes(32).toString('base64'),FIFTYAPI_SETUP_TOKEN:setupToken,FIFTYAPI_DB_PATH:join(directory,'app.db'),FIFTYAPI_LISTEN:`127.0.0.1:${port}`,FIFTYAPI_COOKIE_SECURE:'false',FIFTYAPI_PUBLIC_URL:baseURL,FIFTYAPI_SMTP_HOST:''},stdio:'ignore'})
 for(let i=0;i<100;i++){try{if((await fetch(baseURL+'/healthz')).ok)return}catch{}await new Promise(resolve=>setTimeout(resolve,100))}
 throw new Error('Migration test server did not start')
})
test.afterAll(async()=>{
 service?.kill('SIGTERM')
 if(service?.exitCode===null)await new Promise(resolve=>{service.once('exit',resolve);setTimeout(()=>{service.kill('SIGKILL');resolve()},12000).unref()})
 if(directory)rmSync(directory,{recursive:true,force:true})
})

test('first setup guides Docker export, upload, review, import and model setup; resumes after refresh',async({page})=>{
 const errors=[];page.on('pageerror',e=>errors.push(e.message))
 await page.goto(baseURL)
 await page.getByRole('radio',{name:/从 sub2api Docker 迁移/}).check()
 await page.getByLabel('用户名',{exact:true}).fill('admin')
 await page.getByLabel('密码',{exact:true}).fill(password)
 await page.getByLabel('初始化口令（Setup token）',{exact:true}).fill(setupToken)
 await page.getByRole('button',{name:'创建管理员并开始迁移',exact:false}).click()
 await expect(page.getByRole('heading',{name:'迁移你的 sub2api 工作空间'})).toBeVisible()
 await expect(page.locator('.migration-command')).toContainText('curl -fsSL https://raw.githubusercontent.com/dyunwei/fiftyapi/main/scripts/migrate-sub2api.sh | sudo bash')
 await page.getByRole('button',{name:'复制命令',exact:true}).click()
 await expect(page.getByRole('button',{name:'已复制',exact:true})).toBeVisible()
 await page.getByText('GitHub 无法访问，或者容器未被自动找到？',{exact:true}).click()
 const scriptLink=page.getByRole('link',{name:'下载迁移脚本'})
 const download=await page.request.get(new URL(await scriptLink.getAttribute('href'),baseURL).href)
 expect(download.ok()).toBe(true)
 expect(await download.text()).toContain('#!/usr/bin/env bash')
 await page.reload()
 await expect(page.getByRole('heading',{name:'迁移你的 sub2api 工作空间'})).toBeVisible()
 await page.getByRole('button',{name:'已导出，选择迁移文件',exact:false}).click()
 await expect(page.getByText('浏览器在另一台电脑？',{exact:true})).toBeVisible()
 const memberPassword='migration-member-fixture'
 const sourceHash="$2b$04$SSJsbqEmDz5bCTHAjRKHGuD6fb0onhQRrIsnWMCS7bm.nq2lqaoce"
 const wireChecks=[]
 page.on('request',r=>{if(r.url().endsWith('/api/admin/migrations/sub2api')&&r.method()==='POST'){wireChecks.push(r.postDataJSON().data.users[0].password_hash===sourceHash)}})
 const bundle={type:'fiftyapi-sub2api-migration',version:1,source:'onboarding-source',users:[{id:1,email:'migrated@example.com',username:'迁入成员',password_hash:sourceHash,status:'active'}],api_keys:[{id:1,user_id:1,name:'原 Key',key:'onboarding-original-key-fixture',status:'active',group_id:2}],accounts:[{id:1,name:'迁入上游',platform:'openai',type:'oauth',status:'disabled',credentials:{refresh_token:'onboarding-refresh-fixture'}}],proxies:[]}
 await page.getByLabel('迁移文件',{exact:true}).setInputFiles({name:'from-docker.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(bundle))})
 await expect(page.getByRole('checkbox',{name:/将受限制的旧 Key/})).toBeChecked()
 await page.getByRole('button',{name:'预览迁移',exact:true}).click()
 await expect(page.getByRole('heading',{name:'迁移预览（尚未保存）'})).toBeVisible()
 await expect(page.getByRole('button',{name:'确认迁移',exact:true})).toBeDisabled()
 await page.setViewportSize({width:390,height:844})
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.screenshot({path:'/tmp/fiftyapi-migration-onboarding-mobile.png',fullPage:true})
 await page.getByRole('checkbox',{name:/已检查预览并停止旧站/}).check()
 await page.getByRole('button',{name:'确认迁移',exact:true}).click()
 await expect(page.getByRole('heading',{name:'迁移结果',exact:true})).toBeVisible()
 await expect(page.getByRole('heading',{name:'最后完成这三件事'})).toBeVisible()
 expect(wireChecks).toEqual([true,true])
 const preserved=execFileSync('python3',['-c',"import sqlite3,sys; db=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro',uri=True); row=db.execute('SELECT password_hash FROM users WHERE username=?',('migrated@example.com',)).fetchone(); print('yes' if row and row[0]==sys.stdin.read() else 'no')",join(directory,'app.db')],{input:sourceHash,encoding:'utf8'}).trim()
 expect(preserved).toBe('yes')
 expect(await page.locator('body').textContent()).not.toContain('onboarding-original-key-fixture')
 expect(await page.locator('body').textContent()).not.toContain('onboarding-refresh-fixture')
 const models=await page.request.get(baseURL+'/v1/models',{headers:{Authorization:'Bearer onboarding-original-key-fixture'}})
 expect(models.status()).toBe(200)
 await page.setViewportSize({width:1440,height:1000})
 await page.screenshot({path:'/tmp/fiftyapi-migration-onboarding-desktop.png',fullPage:true})
 await page.getByRole('button',{name:'检查模型与价格',exact:false}).click()
 await expect(page.getByRole('heading',{name:'迁移你的 sub2api 工作空间'})).toHaveCount(0)
 await page.reload()
 await expect(page.getByRole('heading',{name:'迁移你的 sub2api 工作空间'})).toHaveCount(0)
 await page.locator('.nav-item').filter({hasText:'团队成员'}).click()
 await expect(page.getByRole('row').filter({hasText:'migrated@example.com'})).toContainText('100')
 await expect(page.getByRole('button',{name:'从 sub2api 迁移',exact:true})).toHaveCount(0)
 await page.locator('.nav-item').filter({hasText:'上游账号'}).click()
 await expect(page.getByRole('button',{name:'从 sub2api 迁移',exact:true})).toHaveCount(0)
 const adminMe=await (await page.request.get(baseURL+'/api/me')).json()
 const closed=await page.request.post(baseURL+'/api/admin/migrations/sub2api',{data:{data:bundle,preview:true},headers:{'X-CSRF-Token':adminMe.csrf_token}})
 expect(closed.status()).toBe(403)
 await page.getByRole('button',{name:'退出登录',exact:false}).click()
 await page.getByLabel('用户名',{exact:true}).fill('migrated@example.com')
 await page.getByLabel('密码',{exact:true}).fill(memberPassword)
 await page.getByRole('button',{name:'登录',exact:false}).click()
 await expect(page.locator('.quota-card')).toHaveCount(2)
 const importedMe=await (await page.request.get(baseURL+'/api/me')).json()
 expect(importedMe.user).toMatchObject({username:'migrated@example.com',role:'user',limit_5h_microusd:100000000,limit_7d_microusd:100000000})
 expect(errors).toEqual([])
})
