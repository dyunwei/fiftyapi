import {test} from 'node:test'
import assert from 'node:assert/strict'
import {clientSnippets} from '../src/client-usage.js'
test('Codex config uses Responses, gateway key auth and one v1 suffix',()=>{
 const [config,run]=clientSnippets({origin:'http://192.0.2.1:8080/',token:'sk-fixture',model:'team/codex'})
 assert.match(config.code,/model = "team\/codex"/)
 assert.match(config.code,/base_url = "http:\/\/192.0.2.1:8080\/v1"/)
 assert.match(config.code,/wire_api = "responses"/)
 assert.match(config.code,/requires_openai_auth = false/)
 assert.match(config.code,/supports_websockets = false/)
 assert.equal(run.code,"export FIFTYAPI_API_KEY='sk-fixture'\ncodex")
 assert.ok(!config.code.includes('sk-fixture'))
})
test('Windows instructions use PowerShell and preserve quoted values',()=>{
 const [config,run]=clientSnippets({origin:'https://example.test',system:'windows',token:"key'quoted",model:'model"quoted',websocket:true})
 assert.equal(config.title,'%USERPROFILE%\\.codex\\config.toml')
 assert.match(config.code,/model = "model\\"quoted"/)
 assert.match(config.code,/supports_websockets = true/)
 assert.equal(run.code,"$env:FIFTYAPI_API_KEY = 'key''quoted'\ncodex")
})
test('missing saved key uses a clear placeholder, never a masked prefix',()=>{
 const [config,run]=clientSnippets({origin:'https://example.test'})
 assert.match(config.code,/MODEL_ID_FROM_CATALOG/)
 assert.match(run.code,/YOUR_FIFTYAPI_KEY/)
})
test('Messages uses origin without v1 and generic Windows API uses curl.exe',()=>{
 const [claude]=clientSnippets({origin:'https://example.test/',client:'claude',model:'team/claude'})
 assert.match(claude.code,/ANTHROPIC_BASE_URL='https:\/\/example.test'/)
 assert.match(claude.code,/ANTHROPIC_MODEL='team\/claude'/)
 const [api]=clientSnippets({origin:'https://example.test',client:'api',system:'windows'})
 assert.match(api.code,/curl.exe 'https:\/\/example.test\/v1\/models'/)
 assert.match(api.code,/Bearer \$env:FIFTYAPI_API_KEY/)
})
