#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
FNPACK_BIN="${FNPACK_BIN:-$ROOT/dist/fnpack-linux-amd64}"
VERSION="$(sed -n 's/^version="\([^"]*\)"/\1/p' "$ROOT/packaging/fpk/manifest")"
[[ -n "$VERSION" ]] || { echo "missing manifest version" >&2; exit 1; }
cd "$ROOT"
mkdir -p dist
if [[ ! -d node_modules ]]; then npm ci --ignore-scripts --no-audit --no-fund; fi
npm run prepare:sdk
"$GO_BIN" test ./...
if [[ ! -x "$FNPACK_BIN" ]]; then
  echo "fnpack is missing: $FNPACK_BIN (see README)" >&2
  exit 1
fi
for platform in arm x86; do
  case "$platform" in
    arm) goarch=arm64; suffix=arm64;;
    x86) goarch=amd64; suffix=x86_64;;
  esac
  stage="$ROOT/dist/staging-$platform"
  rm -rf "$stage"
  mkdir -p "$stage/app/server"
  cp -a packaging/fpk/. "$stage/"
  sed -i -e "s/^platform=.*/platform=\"$platform\"/" "$stage/manifest"
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" "$GO_BIN" build -trimpath -ldflags="-s -w" -o "$stage/app/server/mountmgr" ./cmd/mountmgr
  chmod 755 "$stage/app/server/mountmgr"
  find "$stage/cmd" -type f -exec chmod 755 {} +
  "$FNPACK_BIN" build --directory "$stage"
  out="dist/fnos-mount-manager-${VERSION}-${suffix}.fpk"
  mv -f fnos-mount-manager.fpk "$out"
  (cd dist && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
  echo "Built: $out"
done
