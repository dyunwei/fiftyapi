import {test} from 'node:test'
import assert from 'node:assert/strict'
import {parseHeaders,toConfigForm,fromConfigForm} from '../src/account-config.js'
test('configuration round-trip preserves masked proxy and default/explicit booleans',()=>{
 const config={proxy_url:'https://operator:***@proxy.example',max_concurrency:0,timeout_seconds:0,identity_patch_enabled:null,metadata_passthrough:false,fingerprint_unification:true,gateway_headers:{'X-Trace':'a:b'},models_headers:{},antigravity_fallback_urls:['https://backup.example']}
 const {max_concurrency,...connection}=config
 assert.deepEqual(fromConfigForm(toConfigForm(config)),connection)
 assert.equal(Object.hasOwn(fromConfigForm({...toConfigForm(config),max_concurrency:99}),'max_concurrency'),false)
})
test('header parser rejects ambiguous or credential-bearing input',()=>{
 for(const text of ['bad header: value','no colon','X-A: a\nx-a: b','Authorization: Bearer private','Cookie: private','x-api-key: private','Connection: close'])assert.throws(()=>parseHeaders(text))
 assert.deepEqual(parseHeaders('X-Trace: a:b\n\nX-Other: value'),{'X-Trace':'a:b','X-Other':'value'})
})
test('inference policy survives editing without mutating the saved configuration',()=>{
 const saved={inference_policy:{max_effort:'medium',max_thinking_tokens:4096,force_standard:true}}
 const form=toConfigForm(saved)
 form.inference_policy.max_effort='low'
 form.inference_policy.max_thinking_tokens='2048'
 const result=fromConfigForm(form)
 assert.deepEqual(saved.inference_policy,{max_effort:'medium',max_thinking_tokens:4096,force_standard:true})
 assert.deepEqual(result.inference_policy,{max_effort:'low',max_thinking_tokens:2048,force_standard:true})
})
test('gateway preset preserves the brand without changing the source object',()=>{
 const original={gateway:{platform:'fiftyapi',protocols:['messages']},gateway_headers:{},models_headers:{},antigravity_fallback_urls:[],max_concurrency:0,timeout_seconds:0}
 const form=toConfigForm(original)
 assert.equal(form.gateway.platform,'fiftyapi');assert.equal(original.gateway.platform,'fiftyapi')
 assert.equal(fromConfigForm(form).gateway.platform,'fiftyapi')
})
test('protocol form upgrades legacy mappings without mutating saved arrays',()=>{
 const saved={gateway:{platform:'new-api',protocols:['chat','responses'],models:[{id:'legacy',upstream_id:'remote',protocol:'responses'},{id:'shared',upstream_id:'another',protocols:['chat','responses']} ]},gateway_headers:{},models_headers:{},antigravity_fallback_urls:[]}
 const form=toConfigForm(saved)
 assert.deepEqual(form.gateway.models[0].protocols,['responses'])
 assert.equal(form.gateway.models[0].protocol,undefined)
 form.gateway.models[1].protocols.pop();form.gateway.protocols.pop()
 assert.deepEqual(saved.gateway.models[1].protocols,['chat','responses'])
 assert.deepEqual(saved.gateway.protocols,['chat','responses'])
 assert.equal(saved.gateway.models[0].protocol,'responses')
 assert.deepEqual(fromConfigForm(form).gateway.models[1].protocols,['chat'])
})
