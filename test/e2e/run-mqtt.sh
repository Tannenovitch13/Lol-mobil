#!/usr/bin/env bash
# Remote access through an MQTT broker (the zero-setup mode): mock client
# with a local broker standing in for the public ones, companion with
# -brokers, and the interface served as static files (like the Android app
# or GitHub do).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
OUT="${OUT:-$WORK/screenshots}"
mkdir -p "$OUT" "$WORK/cfg"
cleanup() { kill ${MOCK_PID:-} ${COMP_PID:-} ${WEB_PID:-} 2>/dev/null || true; }
trap cleanup EXIT

cd "$ROOT"
go build -o "$WORK/mock-lcu" ./cmd/mock-lcu
go build -o "$WORK/companion" ./cmd/lolmobil-companion

"$WORK/mock-lcu" -step 1s -mqtt 127.0.0.1:1884 > "$WORK/mock.log" 2>&1 &
MOCK_PID=$!
python3 -m http.server 8798 --bind 127.0.0.1 --directory "$ROOT/web" > "$WORK/web.log" 2>&1 &
WEB_PID=$!
sleep 1
LOLMOBIL_CONFIG_DIR="$WORK/cfg" "$WORK/companion" -lcu-url http://127.0.0.1:29999 -lcu-password test \
  -no-browser -no-plugin-install -no-rose -brokers "${BROKERS:-ws://127.0.0.1:1884/mqtt}" \
  -web-url http://127.0.0.1:8798/index.html > "$WORK/companion.log" 2>&1 &
COMP_PID=$!
for _ in $(seq 1 50); do
  grep -q "Remote access ready" "$WORK/companion.log" 2>/dev/null && break
  sleep 0.2
done

LINK="$(grep -o 'http://127.0.0.1:8798/index.html#k=[^ ]*' "$WORK/companion.log" | head -1)"
OUT="$OUT" LINK="$LINK" node "$ROOT/test/e2e/remote.js" || {
  echo "---- companion.log"; cat "$WORK/companion.log"; echo "---- mock.log"; tail -40 "$WORK/mock.log"; exit 1;
}
