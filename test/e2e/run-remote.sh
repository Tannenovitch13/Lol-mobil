#!/usr/bin/env bash
# Remote access test: mock client + local relay (wrangler dev) + companion
# with -relay, then the phone goes through the relay (end-to-end encrypted).
#
#   WRANGLER=/path/to/node_modules/.bin/wrangler test/e2e/run-remote.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
OUT="${OUT:-$WORK/screenshots}"
WRANGLER="${WRANGLER:-npx wrangler}"
mkdir -p "$OUT" "$WORK/cfg"
cleanup() { kill ${MOCK_PID:-} ${COMP_PID:-} ${RELAY_PID:-} 2>/dev/null || true; pkill -f "workerd.*8799" 2>/dev/null || true; }
trap cleanup EXIT

cd "$ROOT"
go build -o "$WORK/mock-lcu" ./cmd/mock-lcu
go build -o "$WORK/companion" ./cmd/lolmobil-companion

"$WORK/mock-lcu" -step 1s > "$WORK/mock.log" 2>&1 &
MOCK_PID=$!
CI=1 WRANGLER_SEND_METRICS=false $WRANGLER dev -c wrangler.jsonc --port 8799 --ip 127.0.0.1 --persist-to "$WORK/relay-state" > "$WORK/relay.log" 2>&1 &
RELAY_PID=$!
for _ in $(seq 1 120); do
  curl -sf http://127.0.0.1:8799/relay-info >/dev/null 2>&1 && break
  sleep 0.5
done
curl -sf http://127.0.0.1:8799/relay-info >/dev/null || { echo "relay did not start"; cat "$WORK/relay.log"; exit 1; }

LOLMOBIL_CONFIG_DIR="$WORK/cfg" "$WORK/companion" -lcu-url http://127.0.0.1:29999 -lcu-password test \
  -no-browser -no-plugin-install -no-rose -relay http://127.0.0.1:8799 > "$WORK/companion.log" 2>&1 &
COMP_PID=$!
for _ in $(seq 1 50); do
  grep -q "Remote access ready" "$WORK/companion.log" 2>/dev/null && break
  sleep 0.2
done

LINK="$(grep -o 'http://127.0.0.1:8799/#k=[A-Za-z0-9_-]*' "$WORK/companion.log" | head -1)"
OUT="$OUT" LINK="$LINK" node "$ROOT/test/e2e/remote.js" || {
  echo "---- companion.log"; cat "$WORK/companion.log"; echo "---- relay.log"; tail -40 "$WORK/relay.log"; exit 1;
}
