#!/usr/bin/env bash
# End-to-end test: builds the companion and the mock League client, starts
# both (plus a mock Rose bridge), then drives the phone interface and the
# LolMobil-Remote plugin (in a fake client page) with Playwright.
#
#   test/e2e/run.sh            # needs Go, Node and Playwright (chromium)
#   OUT=/tmp/shots test/e2e/run.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
OUT="${OUT:-$WORK/screenshots}"
mkdir -p "$OUT" "$WORK/cfg"
cleanup() { kill ${MOCK_PID:-} ${COMP_PID:-} 2>/dev/null || true; }
trap cleanup EXIT

cd "$ROOT"
go build -o "$WORK/mock-lcu" ./cmd/mock-lcu
go build -o "$WORK/companion" ./cmd/lolmobil-companion

# -fail-champions: the champion list fails at first, the phone must retry.
# -drop-lock: the first lock-in is lost, the phone must notice and send it again.
"$WORK/mock-lcu" -rose -step 1s -fail-champions 2 -fail-accept 1 -drop-lock 1 > "$WORK/mock.log" 2>&1 &
MOCK_PID=$!
sleep 0.5
LOLMOBIL_CONFIG_DIR="$WORK/cfg" "$WORK/companion" -lcu-url http://127.0.0.1:29999 -lcu-password test -no-browser -no-plugin-install -remote off \
  -opgg-url http://127.0.0.1:29999 -blitz-url http://127.0.0.1:29999/graphql > "$WORK/companion.log" 2>&1 &
COMP_PID=$!

for _ in $(seq 1 50); do
  grep -q "Connected to Rose" "$WORK/companion.log" 2>/dev/null && grep -q "Connected to the League client" "$WORK/companion.log" && break
  sleep 0.2
done

PIN="$(node -e "console.log(require('$WORK/cfg/config.json').pin)")"
OUT="$OUT" PIN="$PIN" PLUGIN="$ROOT/pengu-plugin/LolMobil-Remote/index.js" node "$ROOT/test/e2e/flow.js" || {
  echo "---- companion.log"; cat "$WORK/companion.log"; echo "---- mock.log"; tail -50 "$WORK/mock.log"; exit 1;
}
# Interface bundled in the Android app (served elsewhere) on the Wi-Fi.
python3 -m http.server 8798 --bind 127.0.0.1 --directory "$ROOT/web" > "$WORK/web.log" 2>&1 &
WEB_PID=$!
sleep 1
TOKEN="$(node -e "console.log(require('$WORK/cfg/config.json').token)")"
TOKEN="$TOKEN" node "$ROOT/test/e2e/bundled.js" || { kill $WEB_PID; exit 1; }
kill $WEB_PID
echo "Screenshots: $OUT"
