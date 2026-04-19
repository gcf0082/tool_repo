#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

OUT_DIR="${OUT_DIR:-dist}"
TOOL_CLI_VERSION="${TOOL_CLI_VERSION:-0.1.0}"

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

echo "=== building server ==="
for t in "${TARGETS[@]}"; do
  read -r os arch <<<"$t"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  out="$OUT_DIR/tool_repo-${os}-${arch}${ext}"
  echo "  $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$out" .
done

echo "=== building tool_cli and packaging ==="
for t in "${TARGETS[@]}"; do
  read -r os arch <<<"$t"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  stage="$(mktemp -d)"
  bin="$stage/tool_cli${ext}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$bin" ./cmd/tool_cli
  chmod 0755 "$bin"

  pkg_dir="$OUT_DIR/packages/tool_cli/$TOOL_CLI_VERSION/${os}-${arch}"
  mkdir -p "$pkg_dir"
  tar -czf "$pkg_dir/tool_cli.tar.gz" -C "$stage" "tool_cli${ext}"
  echo "  $pkg_dir/tool_cli.tar.gz"
  rm -rf "$stage"
done

echo
echo "=== server artifacts ==="
ls -lh "$OUT_DIR"/tool_repo-*
echo
echo "=== tool_cli packages (drop under your server's packages/ dir) ==="
find "$OUT_DIR/packages" -type f | sort
