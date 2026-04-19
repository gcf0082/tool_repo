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
DATA_DIR="./testdata"
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
go build -o ./tool_cli ./cmd/tool_cli || { log "tool_cli build failed"; exit 1; }

# Publish tool_cli into testdata/packages so /install_tool_cli can find it.
tc_pkg_dir="./testdata/packages/tool_cli/0.1.0/linux-amd64"
mkdir -p "$tc_pkg_dir"
tar -C "$(dirname "$(realpath ./tool_cli)")" -czf "$tc_pkg_dir/tool_cli.tar.gz" tool_cli

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
assert_status "/get_tool?name=ripgrep&os=darwin&arch=arm64"               200 "rg-14.1.0-darwin"
assert_status "/get_tool?name=ripgrep&os=linux&arch=arm64"                404
assert_status "/get_tool?name=fzf&os=linux&arch=amd64"                    200  # body is gzipped tar, skip exact content check
assert_status "/get_tool?name=fzf"                                        400
assert_status "/get_tool?name=fzf&os=linux"                               400
assert_status "/get_tool?name=nope&os=linux&arch=amd64"                   404
assert_status "/get_tool?name=..%2Fetc&os=linux&arch=amd64"               400

assert_header "/get_tool?name=ripgrep&os=linux&arch=amd64" "Content-Type" "application/gzip"
assert_header "/get_tool?name=fzf&os=linux&arch=amd64"    "Content-Type" "application/gzip"

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

assert_status        "/tool_cli_help" 200
assert_body_contains "/tool_cli_help" "tool_cli — client wrapper"
assert_body_contains "/tool_cli_help" "run remote"
assert_body_contains "/tool_cli_help" "push <local>"

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
    if [[ -f ./fzf.tar.gz ]] && cmp -s ./fzf.tar.gz "$OLDPWD/testdata/packages/fzf/0.1.0/linux-amd64/fzf.tar.gz"; then
      ok "install_tool real run saved archive ./fzf.tar.gz"
    else
      ko "install_tool wrong file: $(ls -la)"
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

fwd_assert_status "/get_tool?name=fzf&os=linux&arch=amd64"       200
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
    if [[ -f ./fzf.tar.gz ]] && cmp -s ./fzf.tar.gz "$OLDPWD/testdata/packages/fzf/0.1.0/linux-amd64/fzf.tar.gz"; then
      ok "FWD install real run saved archive ./fzf.tar.gz via proxy"
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
  # 'get' saves raw archive; verify content matches original tarball
  if "$OLDPWD/tool_cli" get fzf --os linux --arch amd64 >/dev/null 2>&1; then
    if [[ -f ./fzf.tar.gz ]] && cmp -s ./fzf.tar.gz "$OLDPWD/testdata/packages/fzf/0.1.0/linux-amd64/fzf.tar.gz"; then
      ok "tool_cli get fzf downloaded correct file"
    else
      ko "tool_cli get fzf wrong file: $(ls)"
    fi
  else
    ko "tool_cli get fzf command failed"
  fi
)
rm -rf "$dl_tmp"

# get with explicit version (ripgrep 14.0.3; plain-text test fixture)
dl_tmp="$(mktemp -d)"
(
  cd "$dl_tmp"
  if "$OLDPWD/tool_cli" get ripgrep --os linux --arch amd64 --version 14.0.3 >/dev/null 2>&1; then
    if [[ -f ./ripgrep.tar.gz && "$(cat ./ripgrep.tar.gz)" == "rg-14.0.3-linux" ]]; then
      ok "tool_cli get ripgrep --version 14.0.3"
    else
      ko "tool_cli get ripgrep pinned-version wrong file"
    fi
  else
    ko "tool_cli get ripgrep --version failed"
  fi
)
rm -rf "$dl_tmp"

# install extracts archive into target dir
TARGET_DIR="$(mktemp -d)/bin"
if ./tool_cli install fzf --dir "$TARGET_DIR" >/dev/null 2>&1; then
  if [[ -x "$TARGET_DIR/fzf" && "$(cat "$TARGET_DIR/fzf")" == "fzf-binary-real-content" ]]; then
    ok "tool_cli install fzf --dir extracts binary"
  else
    ko "tool_cli install --dir wrong: $(ls -la "$TARGET_DIR" 2>&1; cat "$TARGET_DIR"/fzf 2>&1)"
  fi
else
  ko "tool_cli install --dir command failed"
fi
rm -rf "$(dirname "$TARGET_DIR")"

# get with --dir downloads into that dir (creating if needed)
TARGET_DIR="$(mktemp -d)/downloads"
if ./tool_cli get fzf --os linux --arch amd64 --dir "$TARGET_DIR" >/dev/null 2>&1; then
  if [[ -f "$TARGET_DIR/fzf.tar.gz" ]] && cmp -s "$TARGET_DIR/fzf.tar.gz" ./testdata/packages/fzf/0.1.0/linux-amd64/fzf.tar.gz; then
    ok "tool_cli get fzf --dir $TARGET_DIR"
  else
    ko "tool_cli get --dir wrong file: $(ls "$TARGET_DIR" 2>&1)"
  fi
else
  ko "tool_cli get --dir command failed"
fi
rm -rf "$(dirname "$TARGET_DIR")"

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

# help: fetched from server, contains latest subcommands
help_out="$(TOOL_CLI_URL="$BASE" ./tool_cli help 2>&1 || true)"
if [[ "$help_out" == *"run remote"* && "$help_out" == *"push <local>"* ]]; then
  ok "tool_cli help fetched from server and includes run/push"
else
  ko "tool_cli help missing server content: ${help_out:0:200}"
fi

# help without configured URL → prints fallback, non-zero exit
unset_cfg="$(mktemp -d)"
helpfail_out="$(env -u TOOL_CLI_URL HOME="$unset_cfg" ./tool_cli help 2>&1 || true)"
if [[ "$helpfail_out" == *"unreachable"* ]]; then
  ok "tool_cli help fallback when URL unset"
else
  ko "tool_cli help fallback wrong: $helpfail_out"
fi
rm -rf "$unset_cfg"

# run: execute a remote script with args → stdout contains the expected text
run_out="$(TOOL_CLI_URL="$BASE" ./tool_cli run remote://hello.sh foo "bar baz" 2>&1 || true)"
if [[ "$run_out" == *"hello foo bar baz"* ]]; then
  ok "tool_cli run remote://hello.sh passes args"
else
  ko "tool_cli run args wrong: $run_out"
fi

# run: missing remote:// prefix → non-zero exit
if TOOL_CLI_URL="$BASE" ./tool_cli run notremote://x.sh >/dev/null 2>&1; then
  ko "tool_cli run should reject missing remote:// prefix"
else
  ok "tool_cli run rejects non-remote:// target"
fi

# run: 404 path → friendly error message + non-zero
nope_out="$(TOOL_CLI_URL="$BASE" ./tool_cli run remote://nope.sh 2>&1 || true)"
if [[ "$nope_out" == *"script not found: remote://nope.sh"* && "$nope_out" != *"(22)"* ]]; then
  ok "tool_cli run shows friendly 404 message"
else
  ko "tool_cli run 404 msg wrong: $nope_out"
fi

# push: upload then run
PUSH_LOCAL="$(mktemp)"
printf '#!/bin/sh\necho "pushed-by-cli $@"\n' > "$PUSH_LOCAL"
if TOOL_CLI_URL="$BASE" ./tool_cli push "$PUSH_LOCAL" remote://pushed-by-cli/y.sh >/dev/null 2>&1; then
  ok "tool_cli push uploaded y.sh"
else
  ko "tool_cli push failed"
fi
run_out2="$(TOOL_CLI_URL="$BASE" ./tool_cli run remote://pushed-by-cli/y.sh alpha 2>&1 || true)"
if [[ "$run_out2" == *"pushed-by-cli alpha"* ]]; then
  ok "tool_cli run picks up pushed script"
else
  ko "pushed script run wrong: $run_out2"
fi
rm -f "$PUSH_LOCAL"
rm -rf ./testdata/scripts/uploaded ./testdata/scripts/pushed-by-cli

rm -rf "$CLI_HOME"
unset HOME; export HOME="/root"

# ---- /install_tool_cli bootstrap ----
log "== get_script =="
assert_status        "/get_script?path=hello.sh" 200
assert_body_contains "/get_script?path=hello.sh" '#!/bin/sh'
assert_body_contains "/get_script?path=hello.sh" 'hello $@'
assert_header        "/get_script?path=hello.sh" "Content-Type" "application/x-shellscript; charset=utf-8"
assert_status        "/get_script?path=deploy/staging.sh" 200
assert_status        "/get_script?path=../etc/passwd" 400
assert_status        "/get_script?path=/abs" 400
assert_status        "/get_script?path=nope.sh" 404
assert_status        "/get_script" 200  # help
assert_body_contains "/get_script" "GET /get_script"

log "== put_script via curl -T =="
TMP_SH="$(mktemp)"
printf '#!/bin/sh\necho "pushed $@"\n' > "$TMP_SH"

# Create (201)
code=$(curl -sS -o /dev/null -w '%{http_code}' -T "$TMP_SH" "${BASE}/put_script?path=uploaded/x.sh")
if [[ "$code" == "201" ]]; then
  ok "PUT /put_script?path=uploaded/x.sh -> 201 (create)"
else
  ko "PUT /put_script create wrong status: $code"
fi

# Readback
assert_status        "/get_script?path=uploaded/x.sh" 200
assert_body_contains "/get_script?path=uploaded/x.sh" "pushed"

# Overwrite (200)
code=$(curl -sS -o /dev/null -w '%{http_code}' -T "$TMP_SH" "${BASE}/put_script?path=uploaded/x.sh")
if [[ "$code" == "200" ]]; then
  ok "PUT /put_script?path=uploaded/x.sh -> 200 (overwrite)"
else
  ko "PUT /put_script overwrite wrong status: $code"
fi

# Bad path → 400
code=$(curl -sS -o /dev/null -w '%{http_code}' -T "$TMP_SH" "${BASE}/put_script?path=../evil")
if [[ "$code" == "400" ]]; then
  ok "PUT /put_script?path=../evil -> 400"
else
  ko "PUT /put_script bad-path wrong status: $code"
fi

rm -f "$TMP_SH"

# Clean up uploaded/ so repeated runs start clean
rm -rf ./testdata/scripts/uploaded ./testdata/scripts/pushed-by-cli

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
