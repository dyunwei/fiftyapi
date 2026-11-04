export const headersText = headers => Object.entries(headers || {}).map(([key,value]) => `${key}: ${value}`).join('\n')
export function parseHeaders(text) {
 const headers={}, seen=new Set()
 for(const line of (text||'').split('\n')) {
  if(!line.trim())continue
  const at=line.indexOf(':'),key=line.slice(0,at).trim(),value=line.slice(at+1).trim()
  if(at<1||!/^[-!#$%&'*+.^_`|~0-9A-Za-z]+$/.test(key)||/[\r\n]/.test(value))throw new Error('请求头请每行填写「名称: 值」')
  if(seen.has(key.toLowerCase()))throw new Error('请求头重复：'+key)
  if(['authorization','cookie','x-api-key','proxy-authorization','host','connection','content-length','transfer-encoding','x-ztokenmgr-relay-chain'].includes(key.toLowerCase()))throw new Error('请求头不能覆盖认证凭据或连接设置')
  seen.add(key.toLowerCase());headers[key]=value
 }
 return headers
}
export function toConfigForm(config) {
 return {...config,...(config.inference_policy?{inference_policy:{...config.inference_policy}}:{}),...(config.gateway?{gateway:{...config.gateway,platform:config.gateway.platform==='ztokenmgr'?'fiftyapi':config.gateway.platform,protocols:[...(config.gateway.protocols||[])],models:(config.gateway.models||[]).map(({protocol,protocols,...m})=>({...m,protocols:[...(protocols?.length?protocols:protocol?[protocol]:[])]}))}}:{}),gateway_headers_text:headersText(config.gateway_headers),models_headers_text:headersText(config.models_headers),fallback_urls_text:(config.antigravity_fallback_urls||[]).join('\n')}
}
export function fromConfigForm(form) {
 const {gateway_headers_text,models_headers_text,fallback_urls_text,max_concurrency:_legacyMaxConcurrency,...config}=form
 config.gateway_headers=parseHeaders(gateway_headers_text)
 config.models_headers=parseHeaders(models_headers_text)
 config.antigravity_fallback_urls=(fallback_urls_text||'').split('\n').map(s=>s.trim()).filter(Boolean)
 config.timeout_seconds=Number(config.timeout_seconds)||0
 if(config.inference_policy){
  config.inference_policy={...config.inference_policy,max_thinking_tokens:Number(config.inference_policy.max_thinking_tokens)||0}
 }
 return config
}

export function newGatewayConfig(){return toConfigForm({gateway:{platform:'sub2api',base_url:'',protocols:['chat'],auth_mode:'bearer',discovery:'auto',models:[],allow_http:false,compact:false,websocket:false},api_key:'',gateway_url:'',proxy_url:'',gateway_headers:{},models_headers:{},timeout_seconds:0,user_agent:''})}
