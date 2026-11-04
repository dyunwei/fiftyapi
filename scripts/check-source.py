#!/usr/bin/env python3
"""Check release source without printing sensitive matches or inspecting runtime data."""
import json
from pathlib import Path
import re

root=Path(__file__).resolve().parents[1]
excluded={'.git','.local','data','bin','dist','node_modules','__pycache__','.aws','.agents','.codex','test-results','playwright-report'}
patterns={
 'private-key':rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',
 'google-access':rb'ya29\.[A-Za-z0-9_-]{20,}',
 'google-refresh':rb'1//[A-Za-z0-9_-]{40,}',
 'provider-key':rb'sk-(?:proj-|ant-)?[A-Za-z0-9_-]{25,}',
 'gateway-key':rb'sk-[A-Za-z0-9_-]{43}',
}
errors=[]
for file in root.rglob('*'):
 if not file.is_file() or file.is_symlink() or any(p in excluded for p in file.relative_to(root).parts):continue
 if file.name in {'.env','setup-token'} or file.suffix in {'.db','.session','.key'}:errors.append(f'Runtime file outside ignored directories: {file.relative_to(root)}');continue
 if file.suffix in {'.png','.jpg','.sum'}:continue
 for label,pattern in patterns.items():
  if re.search(pattern,file.read_bytes()):errors.append(f'Credential pattern {label}: {file.relative_to(root)}')
for file in [*root.glob('*.md'),*(root/'docs').rglob('*.md')]:
 for link in re.findall(r'\]\(([^)]+)\)',file.read_text()):
  if not re.match(r'\w+://|#|/',link) and not (file.parent/link.split('#')[0]).exists():errors.append(f'Broken documentation link: {file.relative_to(root)}')
version=(root/'internal/buildinfo/VERSION').read_text().strip()
if not re.fullmatch(r'\d+\.\d+\.\d+',version):errors.append('Invalid version')
for file in ['web/package.json','web/package-lock.json']:
 package=json.loads((root/file).read_text())
 if package['version']!=version:errors.append(f'Version mismatch: {file}')
 if 'packages' in package and package['packages']['']['version']!=version:errors.append(f'Package version mismatch: {file}')
if errors:raise SystemExit('\n'.join(errors))
print(f'Source checks passed: version {version}, documentation links and credential patterns. Ignored runtime directories were not inspected.')
