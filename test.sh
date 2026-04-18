#!/usr/bin/env bash
# End-to-end test: starts the built binary against testdata and asserts HTTP
# responses for each example in the plan.
set -uo pipefail

cd "$(dirname "$0")"

HOST="127.0.0.1"
PORT="${PORT:-18090}"
BASE="http://${HOST}:${PORT}"
DATA_DIR="./testdata/packages"
LOG="$(mktemp)"
PID=""

pass=0
fail=0

cleanup() {
  [[ -n "$PID" ]] && kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  rm -f "$LOG"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
ok()  { pass=$((pass+1)); log "  PASS $*"; }
ko()  { fail=$((fail+1)); log "  FAIL $*"; }

# assert_status <url> <want_status> [want_body_exact]
assert_status() {
  local url="$1" want="$2" want_body="${3-}"
  local tmp status body
  tmp="$(mktemp)"
  status="$(curl -sS -o "$tmp" -w '%{http_code}' "${BASE}${url}")"
  body="$(cat "$tmp")"
  rm -f "$tmp"
  if [[ "$status" != "$want" ]]; then
    ko "${url} status=${status} want=${want}"
    return
  fi
  if [[ -n "$want_body" && "$body" != "$want_body" ]]; then
    ko "${url} body=${body@Q} want=${want_body@Q}"
    return
  fi
  ok "${url} -> ${status}"
}

# assert_header <url> <header> <want>
assert_header() {
  local url="$1" header="$2" want="$3"
  local got
  got="$(curl -sSI "${BASE}${url}" | awk -v h="${header}:" 'BEGIN{IGNORECASE=1} tolower($1)==tolower(h){sub(/\r$/,"",$0); sub("^[^:]*: *","",$0); print; exit}')"
  if [[ "$got" == "$want" ]]; then
    ok "${url} ${header}=${got}"
  else
    ko "${url} ${header}=${got@Q} want=${want@Q}"
  fi
}

# assert_body_contains <url> <needle>
assert_body_contains() {
  local url="$1" needle="$2"
  local body
  body="$(curl -sS "${BASE}${url}")"
  if [[ "$body" == *"$needle"* ]]; then
    ok "${url} body contains ${needle@Q}"
  else
    ko "${url} body missing ${needle@Q}"
  fi
}

log "== build =="
go build -o ./tool_repo . || { log "build failed"; exit 1; }

log "== start server on ${HOST}:${PORT} =="
./tool_repo -host "$HOST" -port "$PORT" -dir "$DATA_DIR" >"$LOG" 2>&1 &
PID=$!

for _ in {1..30}; do
  if curl -sS -o /dev/null "${BASE}/" 2>/dev/null; then
    break
  fi
  sleep 0.1
done

log "== get_tool assertions =="
assert_status "/get_tool?name=ripgrep&os=linux&arch=amd64"                200 "rg-14.1.0-linux"
assert_status "/get_tool?name=ripgrep&os=linux&arch=amd64&version=14.0.3" 200 "rg-14.0.3-linux"
assert_status "/get_tool?name=ripgrep&os=linux&arch=arm64"                404
assert_status "/get_tool?name=deploy-script"                              200 "deploy-1.2.0"
assert_status "/get_tool?name=deploy-script&version=1.1.0"                200 "deploy-1.1.0"
assert_status "/get_tool?name=deploy-script&os=linux&arch=amd64"          404
assert_status "/get_tool?name=fzf&os=linux&arch=amd64"                    200 "fzf-binary"
assert_status "/get_tool?name=nope"                                       404
assert_status "/get_tool?name=fzf&os=linux"                               400
assert_status "/get_tool?name=..%2Fetc"                                   400

assert_header "/get_tool?name=ripgrep&os=linux&arch=amd64" "Content-Type" "application/gzip"
assert_header "/get_tool?name=fzf&os=linux&arch=amd64"    "Content-Type" "application/octet-stream"

log "== help assertions =="
assert_status        "/" 200
assert_body_contains "/" "Endpoints:"
assert_body_contains "/" "/get_tool"
assert_body_contains "/" "/install_tool"

assert_status        "/get_tool" 200
assert_body_contains "/get_tool" "Download a package"
assert_body_contains "/get_tool" "name"

assert_status        "/install_tool" 200
assert_body_contains "/install_tool" "Return a shell script"
assert_body_contains "/install_tool" "| sh"

log "== install_tool assertions =="
assert_status        "/install_tool?name=fzf" 200
assert_body_contains "/install_tool?name=fzf" 'NAME="fzf"'
assert_body_contains "/install_tool?name=fzf" "/get_tool?name="
assert_status        "/install_tool?name=nope" 404
assert_status        "/install_tool?name=" 400

log "== install_tool real run =="
tmp="$(mktemp -d)"
(
  cd "$tmp"
  if curl -fsSL "${BASE}/install_tool?name=fzf" | sh >/dev/null 2>&1; then
    if [[ -x ./fzf && "$(cat ./fzf)" == "fzf-binary" ]]; then
      ok "install_tool real run produced executable ./fzf"
    else
      ko "install_tool produced but file wrong: $(ls -la; cat ./fzf 2>&1)"
    fi
  else
    ko "install_tool pipe|sh failed"
  fi
)
rm -rf "$tmp"

log ""
log "== summary: ${pass} passed, ${fail} failed =="
[[ $fail -eq 0 ]]
