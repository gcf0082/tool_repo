#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

OUT_DIR="${OUT_DIR:-dist}"
BIN_NAME="tool_repo"

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

for t in "${TARGETS[@]}"; do
  read -r os arch <<<"$t"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  out="$OUT_DIR/${BIN_NAME}-${os}-${arch}${ext}"
  echo "building $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$out" .
done

echo
echo "artifacts:"
ls -lh "$OUT_DIR"
