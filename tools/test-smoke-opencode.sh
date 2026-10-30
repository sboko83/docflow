#!/bin/bash
# Exercise tools/smoke-opencode.sh without spending live-agent quota: a
# fake docflow drives a fake OpenCode CLI through the same two-unit cycle —
# one controlled Verify failure in the first unit, a resumed second attempt
# via --session, evidence kept and checked — plus negative modes: a
# substituted route in the events, a missing or mismatched Handoff, a new
# session instead of the resume, and lost artifacts. Every negative case
# must fail the smoke's checks with a non-zero code. The fake streams stay
# in this sandbox; they are not live fixtures, and the real subscription is
# only spent by the live smoke in P053.2.1.
set -euo pipefail

source_root="$(cd "$(dirname "$0")/.." && pwd -P)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/checkout/tools" "$work/checkout/bin" "$work/stubs" "$work/tmp"
cp "$source_root/tools/smoke-opencode.sh" "$work/checkout/tools/"
cp "$source_root/tools/smoke-opencode-check.py" "$work/checkout/tools/"

cat > "$work/checkout/bin/docflow" <<'STUB'
#!/bin/bash
# Fake docflow: drives the fake opencode CLI the way the runner would and
# keeps the same artifacts — events.jsonl, per-attempt .out logs, plan
# updates with Handoff, unit commits. TEST_SMOKE_OCODE_MODE makes it
# misbehave on purpose; nothing here touches the network.
set -eu

model='opencode-go/deepseek-v4.1-flash'
mode="${TEST_SMOKE_OCODE_MODE:-}"
plan='docs/plans/P001-smoke.md'
events='.git/docflow/run/smoke.events.jsonl'

emit() {
    if [ "$mode" = missing-artifacts ]; then
        return
    fi
    printf '%s\n' "$1" >> "$events"
}

start_events() {
    mkdir -p .git/docflow/run
    if [ "$mode" = missing-artifacts ]; then
        return
    fi
    : > "$events"
    emit '{"v":1,"ts":1,"run":"smoke","event":"run_start","plan":"P001","units":2}'
}

# finish_unit <uid> <attempts> <word> <log>: mark [x], append the Handoff
# of the successful attempt's result JSON, commit, report done.
finish_unit() {
    uid="$1"; attempts="$2"; word="$3"; log="$4"
    commit="$uid: write $word"
    if [ "$mode" = handoff-mismatch ]; then
        items='fabricated handoff item'
    elif [ "$mode" = handoff-missing ]; then
        items=''
    else
        # Fold the stream the way the runner's decoder does and print the
        # handoff and unverified items of its result JSON, one per line.
        items=$(python3 - "$log" <<'PY'
import json, re, sys
text, msg, parts = "", None, []
for line in open(sys.argv[1], encoding="utf-8"):
    line = line.strip()
    if not line:
        continue
    obj = json.loads(line)
    if obj.get("type") != "text":
        continue
    part = obj.get("part") or {}
    if part.get("messageID") != msg:
        msg, parts = part.get("messageID"), []
    parts.append(part.get("text") or "")
text = "".join(parts)
result = None
for block in reversed(re.findall(r"```json[ \t]*\n(.*?)\n[ \t]*```", text, re.S)):
    try:
        result = json.loads(block)
    except ValueError:
        continue
    break
if isinstance(result, dict):
    for h in result.get("handoff") or []:
        if " ".join(str(h).split()):
            print(" ".join(str(h).split()))
    for u in result.get("unverified") or []:
        if " ".join(str(u).split()):
            print("Unverified: " + " ".join(str(u).split()))
PY
)
    fi
    ITEMS="$items" awk -v commit="$commit" -v uid="$uid" '
        {
            if ($0 ~ "^### \\[ \\] " uid " ") sub(/^### \[ \] /, "### [x] ")
            print
            if ($0 == "Commit: " commit) {
                print "Handoff:"
                n = split(ENVIRON["ITEMS"], a, "\n")
                for (i = 1; i <= n; i++) if (a[i] != "") print "- " a[i]
            }
        }' "$plan" > "$plan.tmp"
    mv "$plan.tmp" "$plan"
    git add -A
    git commit -qm "$commit"
    sha=$(git rev-parse --short HEAD)
    emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"commit\",\"unit\":\"$uid\",\"sha\":\"$sha\",\"test_changes\":[]}"
    emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"unit_end\",\"unit\":\"$uid\",\"status\":\"done\",\"seconds\":1.5,\"test_changes\":0,\"cost\":0}"
}

# unit <uid> <title> <effort> <max-attempts> <word>: one runner unit —
# attempt calls, verify from the plan, retry on a failed verify with the
# resumed session, then finish_unit.
unit() {
    uid="$1"; title="$2"; effort="$3"; max="$4"; word="$5"
    emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"unit_start\",\"unit\":\"$uid\"}"
    printf '%s %s -> opencode | %s | %s\n' "$uid" "$title" "$model" "$effort"
    session=""
    n=0
    ok=0
    while [ "$n" -lt "$max" ]; do
        n=$((n + 1))
        log=".git/docflow/run/$uid.$n.out"
        evmodel="$model"
        if [ "$mode" = route-sub ] && [ "$uid" = 'P001.1.1' ]; then
            evmodel='other/model'
        fi
        emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"attempt\",\"unit\":\"$uid\",\"n\":$n,\"route\":{\"agent\":\"opencode\",\"model\":\"$evmodel\",\"effort\":\"$effort\"},\"log\":\"$log\"}"
        marg="$model#$effort"
        if [ "$mode" = arg-model ]; then
            # The route stays right in the plan, the events and the output;
            # only the argv the CLI really receives is wrong.
            marg="opencode-go/other-model#$effort"
        fi
        argv=(run --format json --agent build --auto --model "$marg")
        if [ -n "$session" ] && [ "$mode" != new-run ]; then
            argv+=(--session "$session")
        fi
        printf 'Runner prompt for %s attempt %s.\n' "$uid" "$n" \
            | opencode "${argv[@]}" > "$log"
        session=$(grep -o 'ses_[A-Za-z0-9_-]*' "$log" | head -1)
        vcmd=$(awk -v uid="$uid" '
            $0 ~ "^### \\[[ ~x!]\\] " uid " " { inunit = 1; next }
            /^### / { inunit = 0 }
            inunit && /^Verify: `/ {
                sub(/^Verify: `/, "")
                sub(/`[ \t\r]*$/, "")
                print
                exit
            }' "$plan")
        vrc=0
        if ! sh -c "$vcmd"; then
            vrc=4
        fi
        emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"verify\",\"unit\":\"$uid\",\"exit_code\":$vrc}"
        if [ "$vrc" -eq 0 ]; then
            ok=1
            break
        fi
    done
    if [ "$ok" -ne 1 ]; then
        emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"unit_end\",\"unit\":\"$uid\",\"status\":\"failed\",\"seconds\":1,\"test_changes\":0,\"cost\":0}"
        emit '{"v":1,"ts":1,"run":"smoke","event":"run_end","exit_code":7,"reason":"unit failed"}'
        exit 7
    fi
    finish_unit "$uid" "$n" "$word" "$log"
}

# The isolated refusal scenario: one attempt against the nonexistent
# model, refused like the live v2.2.3 CLI refuses, exit 7, no commit.
no_such_run() {
    bad='opencode-go/no-such-model'
    nsplan='docs/plans/P001-nosuch.md'
    start_events
    emit '{"v":1,"ts":1,"run":"smoke","event":"unit_start","unit":"P001.1.1"}'
    printf 'P001.1.1 Never runs -> opencode | %s | low\n' "$bad"
    emit "{\"v\":1,\"ts\":1,\"run\":\"smoke\",\"event\":\"attempt\",\"unit\":\"P001.1.1\",\"n\":1,\"route\":{\"agent\":\"opencode\",\"model\":\"$bad\",\"effort\":\"low\"},\"log\":\".git/docflow/run/P001.1.1.1.out\"}"
    printf 'Runner prompt for P001.1.1.\n' \
        | opencode run --format json --agent build --auto --model "$bad#low" \
        > .git/docflow/run/P001.1.1.1.out
    emit '{"v":1,"ts":1,"run":"smoke","event":"unit_end","unit":"P001.1.1","status":"failed","seconds":0.5,"test_changes":0,"cost":0}'
    emit '{"v":1,"ts":1,"run":"smoke","event":"run_end","exit_code":7,"reason":"refused"}'
    printf 'P001.1.1: all executors unavailable until 12:00; the unit stays [~]\n'
    awk '{ if ($0 == "### [ ] P001.1.1 Never runs") sub(/^### \[ \] /, "### [~] "); print }' \
        "$nsplan" > "$nsplan.tmp"
    mv "$nsplan.tmp" "$nsplan"
    exit 7
}

case "$*" in
    'init --agent opencode')
        mkdir -p docs/plans
        ;;
    'run --dry-run --all')
        printf 'plan: P001 OpenCode smoke\npermissions: auto\n'
        printf 'P001.1.1 [ ] Write hello: opencode | %s | low (unit)\n' "$model"
        printf 'P001.1.2 [ ] Write bye: opencode | %s | high (unit)\n' "$model"
        ;;
    'status')
        echo 'P001 ready to close'
        ;;
    'run --yes --no-wait --attempts '*)
        attempts=1
        prev=''
        for arg in "$@"; do
            if [ "$prev" = --attempts ]; then attempts="$arg"; fi
            prev="$arg"
        done
        if [ "$attempts" = 1 ]; then
            no_such_run
        fi
        start_events
        unit P001.1.1 'Write hello' low 2 hello
        unit P001.1.2 'Write bye' high 1 bye
        emit '{"v":1,"ts":1,"run":"smoke","event":"run_end","exit_code":0,"reason":"all units done"}'
        ;;
    *)
        printf 'unexpected stub command: %s\n' "$*" >&2
        exit 99
        ;;
esac
STUB

cat > "$work/stubs/opencode" <<'STUB'
#!/bin/bash
# Fake OpenCode CLI: prints canned `run --format json` streams without any
# network call. A fresh call opens its own session and does the unit's tiny
# file work; a call with --session continues that session and reports a
# different handoff, so the plan must carry the successful attempt's items.
# The nonexistent model answers with the provider.no-route error event and
# exit 0 — the exit code is never the signal, like the live refusal.
set -eu

model=''
session=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        --model) model="$2"; shift 2 ;;
        --session) session="$2"; shift 2 ;;
        *) shift ;;
    esac
done

case "$model" in
    *no-such-model*)
        cat <<'EOF'
{"type":"step_start","timestamp":1,"sessionID":"ses_dead","part":{"id":"p1","sessionID":"ses_dead","messageID":"m0","type":"step-start"}}
{"type":"error","timestamp":2,"sessionID":"ses_dead","error":{"type":"provider.no-route","message":"Model \"opencode-go/no-such-model\" is not available for this account"}}
EOF
        exit 0
        ;;
esac

case "$model" in
    *'#low')
        echo hello > hello.txt
        word='hello'
        if [ -n "$session" ]; then
            sid="$session"
            handoff='["second low handoff item", "resume worked"]'
            text='Resumed and confirmed hello.txt.'
        else
            sid="ses_low_$RANDOM"
            handoff='["first low handoff item"]'
            text='Created hello.txt.'
        fi
        ;;
    *'#high')
        echo bye > bye.txt
        word='bye'
        sid="ses_high_$RANDOM"
        handoff='["high handoff item"]'
        text='Created bye.txt.'
        ;;
    *)
        printf 'fake opencode: unexpected model: %s\n' "$model" >&2
        exit 99
        ;;
esac

result="{\"status\": \"done\", \"handoff\": $handoff, \"decisions\": [], \"unverified\": [], \"reason\": \"\", \"blocker\": \"\"}"
escaped="${result//\"/\\\"}"
{
    printf '{"type":"step_start","timestamp":1,"sessionID":"%s","part":{"id":"p1","sessionID":"%s","messageID":"m1","type":"step-start"}}\n' "$sid" "$sid"
    printf '{"type":"tool_use","timestamp":2,"sessionID":"%s","part":{"id":"p2","sessionID":"%s","messageID":"m1","type":"tool","tool":"shell","state":{"status":"completed","input":{"command":"echo %s > %s.txt"},"output":"%s\\n","title":"shell"}}}\n' "$sid" "$sid" "$word" "$word" "$word"
    printf '{"type":"step_finish","timestamp":3,"sessionID":"%s","part":{"id":"p3","sessionID":"%s","messageID":"m1","type":"step-finish","reason":"tool-calls","cost":0,"tokens":{"input":100,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}}\n' "$sid" "$sid"
    printf '{"type":"text","timestamp":4,"sessionID":"%s","part":{"id":"p4","sessionID":"%s","messageID":"m2","type":"text","text":"%s\\n\\n```json\\n%s\\n```\\n"}}\n' "$sid" "$sid" "$text" "$escaped"
    printf '{"type":"step_finish","timestamp":5,"sessionID":"%s","part":{"id":"p5","sessionID":"%s","messageID":"m2","type":"step-finish","reason":"stop","cost":0,"tokens":{"input":50,"output":20,"reasoning":0,"cache":{"read":0,"write":0}}}}\n' "$sid" "$sid"
}
STUB

chmod +x "$work/checkout/bin/docflow" "$work/stubs/opencode"
export PATH="$work/stubs:$PATH" TMPDIR="$work/tmp"

model='opencode-go/deepseek-v4.1-flash'

smoke="$work/checkout/tools/smoke-opencode.sh"

# Positive cycle: two units, one controlled Verify failure with a resumed
# attempt, the no-such-model refusal, all artifacts kept and checked.
code=0
TEST_SMOKE_OCODE_MODE=normal bash "$smoke" >"$work/out" 2>&1 || code=$?
if [ "$code" -ne 0 ]; then
    cat "$work/out" >&2
    echo "test-smoke-opencode: positive cycle: exit $code, want 0" >&2
    exit 1
fi
grep -qF 'smoke-opencode: ok' "$work/out" || {
    cat "$work/out" >&2
    echo 'test-smoke-opencode: positive cycle: no ok line' >&2
    exit 1
}
ev="$(sed -n 's/^smoke-opencode: evidence: //p' "$work/out" | head -1)"
[ -n "$ev" ] || { cat "$work/out" >&2; echo 'test-smoke-opencode: no evidence path printed' >&2; exit 1; }
[ -d "$ev/repo/.git" ] || { echo "test-smoke-opencode: $ev/repo is gone after success" >&2; exit 1; }
[ "$(cat "$ev/run.exit")" = 0 ] || { echo "test-smoke-opencode: run.exit = $(cat "$ev/run.exit")" >&2; exit 1; }
[ -f "$ev/run.events.jsonl" ] || { echo 'test-smoke-opencode: events.jsonl not kept' >&2; exit 1; }
grep -q -- '--session' "$ev/opencode-calls/call-2.argv" \
    || { echo 'test-smoke-opencode: the resume call argv was not recorded' >&2; exit 1; }
sum_check() {
    awk -F '\t' -v u="$2" -v s="$3" -v a="$4" \
        '$1 == u && $2 == s && $3 == a { found = 1 } END { exit found ? 0 : 1 }' "$1" \
        || { echo "test-smoke-opencode: $1 has no $2/$3/$4 row" >&2; exit 1; }
}
sum_check "$ev/summary.tsv" P001.1.1 done 2
sum_check "$ev/summary.tsv" P001.1.2 done 1
[ "$(cat "$ev/no-such-model/run.exit")" = 7 ] \
    || { echo "test-smoke-opencode: no-such-model run.exit = $(cat "$ev/no-such-model/run.exit")" >&2; exit 1; }
[ -f "$ev/no-such-model/run.events.jsonl" ] \
    || { echo 'test-smoke-opencode: no-such-model events.jsonl not kept' >&2; exit 1; }
grep -qF "$model#low" "$ev/opencode-calls/call-1.argv" \
    || { echo 'test-smoke-opencode: call-1 argv lost the #low model' >&2; exit 1; }
grep -qF 'opencode-go/no-such-model#low' "$ev/no-such-model/opencode-calls/call-1.argv" \
    || { echo 'test-smoke-opencode: the refusal call argv lost the bad model' >&2; exit 1; }
sum_check "$ev/no-such-model/summary.tsv" P001.1.1 failed 1
echo 'test-smoke-opencode: positive cycle ok'

# Negative modes: each must fail the smoke with a non-zero code and name
# the broken invariant.
neg() {
    mode="$1"; want="$2"; ncode=0
    TEST_SMOKE_OCODE_MODE="$mode" bash "$smoke" >"$work/out" 2>&1 || ncode=$?
    if [ "$ncode" -eq 0 ]; then
        cat "$work/out" >&2
        echo "test-smoke-opencode: $mode: the smoke passed, want failure" >&2
        exit 1
    fi
    if ! grep -qF "$want" "$work/out"; then
        cat "$work/out" >&2
        echo "test-smoke-opencode: $mode: failed without naming: $want" >&2
        exit 1
    fi
    echo "test-smoke-opencode: $mode: fails as expected (exit $ncode)"
}
neg route-sub 'does not match executor'
neg arg-model 'argv --model'
neg handoff-missing 'no Handoff items'
neg handoff-mismatch 'does not match the result JSON'
neg new-run 'does not resume'
neg missing-artifacts 'wrote no events.jsonl'

# The checker's own mandatory-artifact branch and its failed-run summary:
# a copy of the successful evidence loses one attempt artifact, the
# checker must reject it by name and still keep a summary with an
# `unknown` row for the unit it never reached.
check="$source_root/tools/smoke-opencode-check.py"
cp -R "$ev" "$work/lost"
rm -f "$work/lost/summary.tsv" "$work/lost/opencode-calls/call-1.exit"
lcode=0
python3 "$check" main "$work/lost" >"$work/out" 2>&1 || lcode=$?
if [ "$lcode" -eq 0 ]; then
    cat "$work/out" >&2
    echo 'test-smoke-opencode: lost-artifact: the checker passed, want failure' >&2
    exit 1
fi
grep -qF 'call-1.exit' "$work/out" || {
    cat "$work/out" >&2
    echo 'test-smoke-opencode: lost-artifact: the checker did not name call-1.exit' >&2
    exit 1
}
[ -f "$work/lost/summary.tsv" ] || {
    echo 'test-smoke-opencode: lost-artifact: a failed check left no summary.tsv' >&2
    exit 1
}
grep -q 'unknown' "$work/lost/summary.tsv" || {
    cat "$work/lost/summary.tsv" >&2
    echo 'test-smoke-opencode: lost-artifact: the summary has no unknown row' >&2
    exit 1
}
echo "test-smoke-opencode: lost-artifact: fails as expected (exit $lcode), summary kept"

# A checker that exits 0 without writing the summary must fail the smoke:
# summary.tsv is a mandatory artifact. The whole run repeats in a copy of
# the sandbox whose checker is replaced by a silent one.
cp -R "$work/checkout" "$work/no-summary"
printf '#!/usr/bin/env python3\nimport sys\nsys.exit(0)\n' \
    > "$work/no-summary/tools/smoke-opencode-check.py"
scode=0
TEST_SMOKE_OCODE_MODE=normal bash "$work/no-summary/tools/smoke-opencode.sh" \
    >"$work/out" 2>&1 || scode=$?
if [ "$scode" -eq 0 ]; then
    cat "$work/out" >&2
    echo 'test-smoke-opencode: no-summary: the smoke passed, want failure' >&2
    exit 1
fi
grep -qF 'wrote no summary.tsv' "$work/out" || {
    cat "$work/out" >&2
    echo 'test-smoke-opencode: no-summary: failed without naming the artifact' >&2
    exit 1
}
echo "test-smoke-opencode: no-summary: fails as expected (exit $scode)"

echo 'test-smoke-opencode: ok (positive cycle with resume, route-sub, arg-model, handoff-missing, handoff-mismatch, new-run, missing-artifacts, lost-artifact, no-summary)'
