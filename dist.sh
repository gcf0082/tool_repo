#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

OUT_DIR="${OUT_DIR:-dist}"

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

echo "=== building tool_cli (to embed into server) ==="
for t in "${TARGETS[@]}"; do
  read -r os arch <<<"$t"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  bin_dir="tool_cli_bin/${os}-${arch}"
  mkdir -p "$bin_dir"
  out="$bin_dir/tool_cli${ext}"
  echo "  $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$out" ./cmd/tool_cli
done

echo "=== building server (with embedded tool_cli binaries) ==="
for t in "${TARGETS[@]}"; do
  read -r os arch <<<"$t"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  out="$OUT_DIR/tool_repo-${os}-${arch}${ext}"
  echo "  $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$out" .
done

echo
echo "=== artifacts ==="
ls -lh "$OUT_DIR"/tool_repo-*
