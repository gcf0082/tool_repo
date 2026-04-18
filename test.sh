#!/usr/bin/env bash
# End-to-end test: starts the built binary against testdata and asserts HTTP
# responses for each example in the plan.
set -uo pipefail

cd "$(dirname "$0")"

HOST="127.0.0.1"
PORT="${PORT:-18090}"
FWD_PORT="${FWD_PORT:-18091}"
BASE="http://${HOST}:${PORT}"
FWD_BASE="http://${HOST}:${FWD_PORT}"
DATA_DIR="./testdata/packages"
LOG="$(mktemp)"
FWD_LOG="$(mktemp)"
PASS_FILE="$(mktemp)"
FAIL_FILE="$(mktemp)"
PID=""
FWD_PID=""

cleanup() {
  [[ -n "$PID" ]] && kill "$PID" 2>/dev/null || true
  [[ -n "$FWD_PID" ]] && kill "$FWD_PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  wait "$FWD_PID" 2>/dev/null || true
  rm -f "$LOG" "$FWD_LOG" "$PASS_FILE" "$FAIL_FILE"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
ok()  { echo x >> "$PASS_FILE"; log "  PASS $*"; }
ko()  { echo x >> "$FAIL_FILE"; log "  FAIL $*"; }

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

# ---- forwarder scenario ----
log "== start forwarder on ${HOST}:${FWD_PORT} (upstream ${BASE}) =="
./tool_repo -host "$HOST" -port "$FWD_PORT" -upstream "$BASE" >"$FWD_LOG" 2>&1 &
FWD_PID=$!

for _ in {1..30}; do
  if curl -sS -o /dev/null "${FWD_BASE}/" 2>/dev/null; then
    break
  fi
  sleep 0.1
done

log "== forwarder get_tool passthrough =="
fwd_assert_status() {
  local url="$1" want="$2" want_body="${3-}"
  local tmp status body
  tmp="$(mktemp)"
  status="$(curl -sS -o "$tmp" -w '%{http_code}' "${FWD_BASE}${url}")"
  body="$(cat "$tmp")"
  rm -f "$tmp"
  if [[ "$status" != "$want" ]]; then
    ko "FWD ${url} status=${status} want=${want}"
    return
  fi
  if [[ -n "$want_body" && "$body" != "$want_body" ]]; then
    ko "FWD ${url} body=${body@Q} want=${want_body@Q}"
    return
  fi
  ok "FWD ${url} -> ${status}"
}

fwd_assert_status "/get_tool?name=fzf&os=linux&arch=amd64"       200 "fzf-binary"
fwd_assert_status "/get_tool?name=ripgrep&os=linux&arch=amd64"   200 "rg-14.1.0-linux"
fwd_assert_status "/get_tool?name=nope"                          404

log "== forwarder install_tool BASE points at forwarder =="
fwd_body="$(curl -sS "${FWD_BASE}/install_tool?name=fzf")"
if [[ "$fwd_body" == *"BASE=\"${FWD_BASE}\""* ]]; then
  ok "install_tool BASE is forwarder ${FWD_BASE}"
else
  ko "install_tool BASE wrong; body:\n$fwd_body"
fi
if [[ "$fwd_body" == *"BASE=\"${BASE}\""* ]]; then
  ko "install_tool BASE leaked upstream ${BASE}"
else
  ok "install_tool BASE does not leak upstream"
fi

# Upstream mode skips existence check — even nope should yield 200
fwd_nope_status="$(curl -sS -o /dev/null -w '%{http_code}' "${FWD_BASE}/install_tool?name=nope")"
if [[ "$fwd_nope_status" == "200" ]]; then
  ok "FWD /install_tool?name=nope -> 200 (existence check skipped)"
else
  ko "FWD /install_tool?name=nope status=${fwd_nope_status} want=200"
fi

log "== forwarder install_tool real run =="
tmp="$(mktemp -d)"
(
  cd "$tmp"
  if curl -fsSL "${FWD_BASE}/install_tool?name=fzf" | sh >/dev/null 2>&1; then
    if [[ -x ./fzf && "$(cat ./fzf)" == "fzf-binary" ]]; then
      ok "FWD install real run produced executable ./fzf via proxy"
    else
      ko "FWD install produced wrong file"
    fi
  else
    ko "FWD install pipe|sh failed"
  fi
)
rm -rf "$tmp"

# ---- tool_cli scenario ----
log "== tool_cli =="
CLI_HOME="$(mktemp -d)"
trap 'rm -rf "$CLI_HOME"' RETURN 2>/dev/null || true
export HOME="$CLI_HOME"

# set-url writes config
if ./tool_cli set-url "$BASE" >/dev/null; then
  ok "tool_cli set-url"
else
  ko "tool_cli set-url failed"
fi

# config file has the url
if [[ -f "$CLI_HOME/.tool_cli/config.json" ]] && grep -q "\"url\": \"$BASE\"" "$CLI_HOME/.tool_cli/config.json"; then
  ok "config.json written with url=$BASE"
else
  ko "config.json missing or wrong: $(cat $CLI_HOME/.tool_cli/config.json 2>&1)"
fi

# url command reads it back
cli_url="$(./tool_cli url)"
if [[ "$cli_url" == "$BASE" ]]; then
  ok "tool_cli url -> $cli_url"
else
  ko "tool_cli url -> $cli_url (want $BASE)"
fi

# get downloads fzf
dl_tmp="$(mktemp -d)"
(
  cd "$dl_tmp"
  if "$OLDPWD/tool_cli" get fzf --os linux --arch amd64 >/dev/null 2>&1; then
    if [[ -f ./linux-amd64 && "$(cat ./linux-amd64)" == "fzf-binary" ]]; then
      ok "tool_cli get fzf downloaded correct file"
    else
      ko "tool_cli get fzf wrong file: $(ls)"
    fi
  else
    ko "tool_cli get fzf command failed"
  fi
)
rm -rf "$dl_tmp"

# get with explicit version (ripgrep 14.0.3)
dl_tmp="$(mktemp -d)"
(
  cd "$dl_tmp"
  if "$OLDPWD/tool_cli" get ripgrep --os linux --arch amd64 --version 14.0.3 >/dev/null 2>&1; then
    if [[ -f ./linux-amd64.tar.gz && "$(cat ./linux-amd64.tar.gz)" == "rg-14.0.3-linux" ]]; then
      ok "tool_cli get ripgrep --version 14.0.3"
    else
      ko "tool_cli get ripgrep pinned-version wrong file"
    fi
  else
    ko "tool_cli get ripgrep --version failed"
  fi
)
rm -rf "$dl_tmp"

# install runs pipe|sh
dl_tmp="$(mktemp -d)"
(
  cd "$dl_tmp"
  if "$OLDPWD/tool_cli" install fzf >/dev/null 2>&1; then
    if [[ -x ./fzf && "$(cat ./fzf)" == "fzf-binary" ]]; then
      ok "tool_cli install fzf produced executable ./fzf"
    else
      ko "tool_cli install fzf wrong file: $(ls)"
    fi
  else
    ko "tool_cli install fzf command failed"
  fi
)
rm -rf "$dl_tmp"

# TOOL_CLI_URL env override works even without config
rm -rf "$CLI_HOME/.tool_cli"
cli_url_env="$(TOOL_CLI_URL="$BASE" ./tool_cli url)"
if [[ "$cli_url_env" == "$BASE" ]]; then
  ok "TOOL_CLI_URL env override"
else
  ko "TOOL_CLI_URL env override: got $cli_url_env"
fi

# ping: success against real server
if TOOL_CLI_URL="$BASE" ./tool_cli ping >/dev/null 2>&1; then
  ok "tool_cli ping -> configured server"
else
  ko "tool_cli ping failed against $BASE"
fi

# ping: failure against dead URL
if TOOL_CLI_URL="http://127.0.0.1:1" ./tool_cli ping >/dev/null 2>&1; then
  ko "tool_cli ping should have failed against unreachable URL"
else
  ok "tool_cli ping failed as expected against unreachable URL"
fi

rm -rf "$CLI_HOME"
unset HOME; export HOME="/root"

# ---- /install_tool_cli bootstrap ----
log "== install_tool_cli bootstrap =="
boot_tmp="$(mktemp -d)"
export HOME="$(mktemp -d)"
DEST_PATH="$boot_tmp/tool_cli"
if curl -fsSL "${BASE}/install_tool_cli" | DEST="$DEST_PATH" sh >/dev/null 2>&1; then
  if [[ -x "$DEST_PATH" ]]; then
    ok "install_tool_cli wrote executable $DEST_PATH (DEST override honored)"
  else
    ko "install_tool_cli did not produce $DEST_PATH"
  fi
  cfg="$HOME/.tool_cli/config.json"
  if [[ -f "$cfg" ]] && grep -q "\"url\": \"$BASE\"" "$cfg"; then
    ok "install_tool_cli auto-configured URL=$BASE"
  else
    ko "install_tool_cli config wrong: $(cat $cfg 2>&1)"
  fi
  if "$DEST_PATH" ping >/dev/null 2>&1; then
    ok "bootstrapped tool_cli can ping server"
  else
    ko "bootstrapped tool_cli ping failed"
  fi
else
  ko "install_tool_cli pipe|sh failed"
fi
# verify default destination string is present in the served script
default_script="$(curl -sS "${BASE}/install_tool_cli")"
if [[ "$default_script" == *"/usr/local/bin/tool_cli"* ]]; then
  ok "install_tool_cli default DEST=/usr/local/bin/tool_cli"
else
  ko "install_tool_cli default DEST missing /usr/local/bin/tool_cli"
fi
rm -rf "$boot_tmp" "$HOME"
export HOME="/root"

pass="$(wc -l < "$PASS_FILE" | tr -d ' ')"
fail="$(wc -l < "$FAIL_FILE" | tr -d ' ')"

log ""
log "== summary: ${pass} passed, ${fail} failed =="
[[ "$fail" -eq 0 ]]
