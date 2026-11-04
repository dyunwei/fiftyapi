#!/usr/bin/env python3
"""Collect installed Go/frontend dependency licenses for a local release."""
import json
from pathlib import Path
import re
import subprocess

root=Path(__file__).resolve().parents[1]
target=root/'dist'/'licenses'
target.mkdir(parents=True,exist_ok=True)
raw=subprocess.check_output(['go','list','-m','-json','all'],cwd=root,text=True)
decoder=json.JSONDecoder()
modules=[]
while raw.strip():
    item,end=decoder.raw_decode(raw.lstrip())
    modules.append(item)
    raw=raw.lstrip()[end:]
entries=[]
def collect(name,directory,version):
    if not directory:return
    directory=Path(directory)
    found=[p for p in directory.iterdir() if p.is_file() and re.match(r'^(LICENSE|LICENCE|COPYING|NOTICE)([.\-_].*)?$',p.name,re.I)]
    if not found:return
    safe=re.sub(r'[^A-Za-z0-9._-]','_',name)
    location=target/safe
    location.mkdir(exist_ok=True)
    for file in found:(location/file.name).write_bytes(file.read_bytes())
    entries.append({'name':name,'version':version,'files':[file.name for file in found]})
for module in modules:
    if not module.get('Main'):collect(module['Path'],module.get('Dir'),module.get('Version',''))
node_modules=root/'web'/'node_modules'
if node_modules.exists():
    for path in node_modules.glob('**/package.json'):
        try:
            package=json.loads(path.read_text())
            if package.get('name') and package.get('version'):collect(package['name'],path.parent,package['version'])
        except (ValueError,OSError):pass
goroot=Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
go_version=subprocess.check_output(['go','version'],text=True).strip()
collect('Go standard library',goroot,go_version)
if not any(e['name']=='Go standard library' for e in entries):
    collect('Go standard library','/usr/share/doc/golang',go_version)
for required in ['Go standard library','vue','@vue/runtime-core','@vue/runtime-dom','@vue/reactivity','@vue/shared']:
    if not any(e['name']==required for e in entries):
        raise SystemExit(f'Missing license for {required}; install locked frontend dependencies and verify the Go license location.')
(target/'index.json').write_text(json.dumps(entries,indent=2)+'\n')
print(f'Collected notices for {len(entries)} dependencies in dist/licenses')
