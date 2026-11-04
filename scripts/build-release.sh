#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
npm --prefix web ci --no-audit --no-fund
npm --prefix web run build
mkdir -p dist
# Distribution toolchains may default to a newer x86-64 CPU baseline (e.g. v2).
# Keep release binaries portable; callers may explicitly select a newer baseline.
export GOAMD64="${GOAMD64:-v1}"
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -X fiftyapi/internal/buildinfo.Commit=${FIFTYAPI_BUILD_COMMIT:-${ZTOKENMGR_BUILD_COMMIT:-unknown}}" -o dist/fiftyapi ./cmd/fiftyapi
cp LICENSE COPYING.GPL-3 THIRD_PARTY_NOTICES.md README.md README.zh-CN.md CHANGELOG.md SECURITY.md CONTRIBUTING.md dist/
sed 's#(en/release-quickstart.md)#(QUICKSTART.en.md)#g' docs/release-quickstart.md > dist/QUICKSTART.md
sed 's#(../release-quickstart.md)#(QUICKSTART.md)#g' docs/en/release-quickstart.md > dist/QUICKSTART.en.md
mkdir -p dist/docs dist/scripts
cp -R docs/. dist/docs/
cp scripts/backup-offline.py dist/scripts/
python3 scripts/dependency-notices.py
(cd dist && sha256sum fiftyapi > SHA256SUMS)
printf '%s\n' 'Local release files are ready in dist/. Nothing has been published.'
