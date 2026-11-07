#!/bin/bash
# Acceptance of `docflow roster web` on a copy of the live policy: migrate
# the personal roster to v3 in a temp directory, drive the web operations
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

echo "== migrate to v3"
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
# The goal scenario: the deepseek model of the opencode access gets routes
# low→cheap and high→standard, then a variant. The live roster names the
# model in its allowlist; a model outside it is added first.
model=$(python3 -c 'import re,sys; text=open(sys.argv[1]).read(); m=re.search(r"deepseek-[a-z0-9.\-]+", text); print(m.group(0) if m else "deepseek-v4.1")' "$work/docflow/roster.toml")
access="opencode-go"
if ! grep -q "\[access.$access\]" "$work/docflow/roster.toml"; then
  op "{\"op\":\"AddAccess\",\"name\":\"$access\",\"kind\":\"subscription\",\"agents\":[\"pi\"]}"
fi
allowlist=$(sed -n "/\[access.$access\]/,/^\[/p" "$work/docflow/roster.toml")
if ! grep -q "\"$model\"" <<< "$allowlist"; then
  op "{\"op\":\"AddAccessModel\",\"access\":\"$access\",\"id\":\"$model\"}"
fi
# The migrated model carries one route (the strongest v1 tier); the two
# levels come from AddRouteLevel, which takes the first unused declared
# effort; the keys and classes are then fixed to the goal pair.
op "{\"op\":\"AddRouteLevel\",\"access\":\"$access\",\"model\":\"$model\"}"
op "{\"op\":\"SetRouteClass\",\"access\":\"$access\",\"model\":\"$model\",\"effort\":\"low\",\"class\":\"cheap\"}"
op "{\"op\":\"AddRouteLevel\",\"access\":\"$access\",\"model\":\"$model\"}"
op "{\"op\":\"SetRouteEffort\",\"access\":\"$access\",\"model\":\"$model\",\"effort\":\"medium\",\"next\":\"high\"}"
op "{\"op\":\"SetRouteClass\",\"access\":\"$access\",\"model\":\"$model\",\"effort\":\"high\",\"class\":\"standard\"}"
op "{\"op\":\"CreateVariant\",\"name\":\"web-accept\"}"

echo "== the files"
grep -A3 "\[route.\"$access/$model\"\]" "$work/docflow/roster.toml" || cat "$work/docflow/roster.toml"
grep -A2 "preset.web-accept" "$work/docflow/roster.variants.toml"

echo "== roster brief sees the changes"
"$work/docflow-bin" roster brief | sed -n '/^chains:/,/^review:/p'

echo "== verdict"
if grep -q "$model@low" <<< "$("$work/docflow-bin" roster brief)" && [ -f "$work/docflow/roster.variants.toml" ]; then
  echo "PASS: the editor wrote the routes and the variant; the brief resolves them"
else
  echo "FAIL: the brief does not show the edited routes"
  exit 1
fi
