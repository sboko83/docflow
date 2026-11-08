#!/bin/bash
# Acceptance of `docflow roster web` on a copy of the live policy: migrate
# the personal roster to v4 in a temp directory, drive the web operations
# the browser uses, and read the result back through `roster brief`.
# Usage: bash tools/accept-roster-web.sh [LIVE_ROSTER]
set -euo pipefail

cd "$(dirname "$0")/.."
live="${1:-$HOME/.config/docflow/roster.toml}"
work="$(mktemp -d)"
trap 'rm -rf "$work" "$work/bin"; kill "$pid" 2>/dev/null || true' EXIT
mkdir -p "$work/docflow" "$work/cache/docflow/roster"

echo "== build"
go build -o "$work/docflow-bin" ./cmd/docflow

echo "== copy the live roster: $live"
cp "$live" "$work/docflow/roster.toml"
# The catalog is the scenario fixture: the personal cache is a scan of a
# given moment and the acceptance must be reproducible.
cp internal/roster/testdata/catalog-scenario.json "$work/cache/docflow/roster/catalog.json"
head -2 "$work/docflow/roster.toml"

export HOME="$work"
export USERPROFILE="$work"
export XDG_CONFIG_HOME="$work"
export XDG_CACHE_HOME="$work/cache"

echo "== migrate to v4"
"$work/docflow-bin" roster migrate --write

echo "== start roster web"
"$work/docflow-bin" roster web > "$work/web.log" 2>&1 &
pid=$!
sleep 1
url="$(sed -n 's|.*http://127.0.0.1:\([0-9]*\).*|http://127.0.0.1:\1|p' "$work/web.log")"
[ -n "$url" ] || { cat "$work/web.log"; exit 1; }
echo "   $url"

op() {
  body="$1"
  curl -s -X POST "$url/api/op" -H 'Content-Type: application/json' -d "$body" | python3 -c '
import json, sys
d = json.load(sys.stdin)
print("   op:", d.get("ok"), d.get("error", ""))
if not d.get("ok"):
    sys.exit(1)
'
}

echo "== edit through the API"
# The goal scenario: the deepseek model of the opencode access closes cheap
# at low and executor at high, opencode-go moves to the top, and a policy
# switches it off again.
model="deepseek-v4.1"
access="opencode-go"
if ! grep -q "\[access.$access\]" "$work/docflow/roster.toml"; then
  op "{\"op\":\"AddAccess\",\"name\":\"$access\",\"kind\":\"subscription\",\"agents\":[\"pi\"]}"
fi
op "{\"op\":\"SetCell\",\"access\":\"$access\",\"role\":\"cheap\",\"value\":\"$model@low\"}"
op "{\"op\":\"SetCell\",\"access\":\"$access\",\"role\":\"executor\",\"value\":\"$model@high\"}"
for _ in 1 2 3 4 5 6; do
  op "{\"op\":\"MoveAccess\",\"name\":\"$access\",\"dir\":\"up\"}"
done
op "{\"op\":\"CreateVariant\",\"name\":\"web-accept\"}"
op "{\"op\":\"SetVariantOff\",\"name\":\"web-accept\",\"access\":\"$access\",\"off\":true}"

echo "== the files"
sed -n "/\[access.$access.roles\]/,/^\$/p" "$work/docflow/roster.toml"
grep -A2 "preset.web-accept" "$work/docflow/roster.variants.toml"

echo "== roster brief sees the changes"
"$work/docflow-bin" roster brief | sed -n '/^chains:/,/^review:/p'

echo "== verdict"
brief="$("$work/docflow-bin" roster brief)"
policy="$("$work/docflow-bin" roster brief --preset web-accept)"
if grep -q "cheap  *$access/$model@low" <<< "$brief" && ! grep -q "$access/$model" <<< "$policy"; then
  echo "PASS: the editor wrote the cells and the policy; the brief resolves them"
else
  echo "FAIL: the brief does not show the edited cells"
  exit 1
fi
