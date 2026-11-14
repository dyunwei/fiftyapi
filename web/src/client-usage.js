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

// Deliberately excludes the secret: it is entered through a hidden terminal prompt.
export function setupCommand({origin,client,system,model='',websocket=false}){
 const base=origin.replace(/\/+$/,'')
 const quote=system==='windows'?psQuote:shellQuote
 const args=`configure ${client} --url ${quote(base)}`+(model?` --model ${quote(model)}`:'')+(client==='codex'&&websocket?' --websocket':'')
 if(system==='windows')return `Invoke-WebRequest -Uri ${psQuote(base+'/setup.py')} -OutFile "$env:TEMP\\fiftyapi-setup.py"\nif ($?) { py -3.11 "$env:TEMP\\fiftyapi-setup.py" ${args} }`
 return `curl -fsSL ${shellQuote(base+'/setup.py')} -o fiftyapi-setup.py && python3.11 fiftyapi-setup.py ${args}`
}

export function restoreCommand({client='codex',system='unix'}){
 return system==='windows'
  ? `py -3.11 "$HOME\\.fiftyapi-helper\\helper.py" restore ${client}`
  : `python3.11 ~/.fiftyapi-helper/helper.py restore ${client}`
}
