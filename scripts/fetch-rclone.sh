#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="1.75.2"
BASE="https://downloads.rclone.org/v${VERSION}"
mkdir -p "$ROOT/dist"
for arch in amd64 arm64; do
  filename="rclone-v${VERSION}-linux-${arch}.zip"
  case "$arch" in
    amd64) checksum="349ac8fba6ff65d6247043f1750cdcb518ec5d500ef91463a10d37c0ccdf3702";;
    arm64) checksum="7e1e8d69654941b7b7df84ee74c5f7fb09cce7d5496947fb7bf16b55ff427d10";;
  esac
  archive="$ROOT/dist/$filename"
  curl --fail --location --retry 3 --silent --show-error "$BASE/$filename" --output "$archive"
  (cd "$ROOT/dist" && printf '%s  %s\n' "$checksum" "$filename" | sha256sum --check --status)
  unzip -p "$archive" "*/rclone" > "$ROOT/dist/rclone-$arch"
  chmod 755 "$ROOT/dist/rclone-$arch"
  if [[ "$arch" == amd64 ]]; then "$ROOT/dist/rclone-$arch" version | head -1; fi
  echo "Verified rclone $VERSION for $arch"
done
