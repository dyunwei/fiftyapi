#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

VERSION=$(tr -d '[:space:]' < internal/buildinfo/VERSION)
COMMIT="${FIFTYAPI_BUILD_COMMIT:-unknown}"

# Ensure dist/ base files (UI built, licenses, docs, QUICKSTART) exist
./scripts/build-release.sh

OUT="dist/packages"
STAGE="dist/.stage"
rm -rf "$OUT" "$STAGE"
mkdir -p "$OUT"

TARGETS="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64"

for target in $TARGETS; do
  os="${target%/*}"
  arch="${target#*/}"
  pkg_dir="$STAGE/fiftyapi_${VERSION}_${os}_${arch}"
  rm -rf "$pkg_dir"
  mkdir -p "$pkg_dir"

  bin_name="fiftyapi"
  if [ "$os" = "windows" ]; then
    bin_name="fiftyapi.exe"
  fi

  printf 'Building %s/%s...\n' "$os" "$arch"
  if [ "$arch" = "amd64" ]; then
    GOAMD64="${GOAMD64:-v1}" GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X fiftyapi/internal/buildinfo.Commit=${COMMIT}" \
      -o "$pkg_dir/$bin_name" ./cmd/fiftyapi
  else
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X fiftyapi/internal/buildinfo.Commit=${COMMIT}" \
      -o "$pkg_dir/$bin_name" ./cmd/fiftyapi
  fi

  cp dist/LICENSE dist/COPYING.GPL-3 dist/THIRD_PARTY_NOTICES.md \
     dist/README.md dist/README.zh-CN.md dist/CHANGELOG.md \
     dist/SECURITY.md dist/CONTRIBUTING.md \
     dist/QUICKSTART.md dist/QUICKSTART.en.md "$pkg_dir/"
  cp -R dist/docs "$pkg_dir/docs"
  cp -R dist/scripts "$pkg_dir/scripts"
  cp -R dist/licenses "$pkg_dir/licenses"
  (cd "$pkg_dir" && sha256sum "$bin_name" > SHA256SUMS)

  if [ "$os" = "windows" ]; then
    archive="fiftyapi_${VERSION}_${os}_${arch}.zip"
    python3 - "$pkg_dir" "$OUT/$archive" <<'PY'
import pathlib, sys, zipfile
src = pathlib.Path(sys.argv[1])
dst = pathlib.Path(sys.argv[2])
with zipfile.ZipFile(dst, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    for p in sorted(src.rglob("*")):
        if p.is_file():
            zf.write(p, p.relative_to(src))
PY
  else
    archive="fiftyapi_${VERSION}_${os}_${arch}.tar.gz"
    tar -C "$pkg_dir" -czf "$OUT/$archive" .
  fi
done

rm -rf "$STAGE"
(
  cd "$OUT"
  sha256sum fiftyapi_${VERSION}_* > checksums.txt
)
printf 'Release archives ready in %s/:\n' "$OUT"
ls -lh "$OUT"

