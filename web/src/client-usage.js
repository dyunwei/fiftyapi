const shellQuote=value=>"'"+String(value).replaceAll("'","'\\''")+"'"
const psQuote=value=>"'"+String(value).replaceAll("'","''")+"'"

export function clientSnippets({origin,client='codex',system='unix',token='',model='',websocket=false}){
 const base=origin.replace(/\/+$/,'')
 const key=token||'YOUR_FIFTYAPI_KEY',id=model||'MODEL_ID_FROM_CATALOG'
 const quote=system==='windows'?psQuote:shellQuote
 const env=(name,value)=>system==='windows'?`$env:${name} = ${quote(value)}`:`export ${name}=${quote(value)}`
 if(client==='codex')return [
  {title:system==='windows'?'%USERPROFILE%\\.codex\\config.toml':'~/.codex/config.toml',code:`model_provider = "fiftyapi"\nmodel = ${JSON.stringify(id)}\n\n[model_providers.fiftyapi]\nname = "FiftyAPI"\nbase_url = ${JSON.stringify(base+'/v1')}\nenv_key = "FIFTYAPI_API_KEY"\nwire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = ${websocket}\n`},
  {title:system==='windows'?'PowerShell':'Terminal',code:env('FIFTYAPI_API_KEY',key)+'\ncodex'}
 ]
 if(client==='claude')return [{title:system==='windows'?'PowerShell':'Terminal',code:[system==='windows'?'Remove-Item Env:ANTHROPIC_API_KEY -ErrorAction SilentlyContinue':'unset ANTHROPIC_API_KEY',env('ANTHROPIC_BASE_URL',base),env('ANTHROPIC_AUTH_TOKEN',key),env('ANTHROPIC_MODEL',id),'claude'].join('\n')}]
 return [{title:system==='windows'?'PowerShell':'Terminal',code:env('FIFTYAPI_API_KEY',key)+'\n'+(system==='windows'?`curl.exe ${quote(base+'/v1/models')} -H "Authorization: Bearer $env:FIFTYAPI_API_KEY"`:`curl ${quote(base+'/v1/models')} -H "Authorization: Bearer $FIFTYAPI_API_KEY"`)}]
}
