#!/bin/bash
# Shell fake agent for runner tests. FAKE_AGENT_SCENARIO is a directory of
# numbered shell steps (0.sh, 1.sh, ...). Each step can write/edit/remove
# files, run Git or child commands, sleep, emit JSON events and choose its exit
# code. The shell format avoids a JSON-parser dependency in the fake agent.
# Calls are counted in <scenario>.n and logged as JSON in <scenario>.log.
set -eu

# A quota probe keeps stdin open until a response arrives. Handle its JSON-RPC
# exchange before the ordinary call recorder, which consumes stdin to EOF.
if [ "${1:-}" = '--app-server' ]; then
    while IFS= read -r request; do
        if [ -n "${FAKE_PROBE_LOG:-}" ]; then printf '%s\n' "$request" >> "$FAKE_PROBE_LOG"; fi
        case "$request" in
            *'"id":1'*) printf '%s\n' '{"id":1,"result":{}}' ;;
            *'"id":2'*)
                cat "$2"
                if [ "${3:-}" = '--once' ]; then exit 0; fi
                ;;
        esac
    done
    exit 0
fi

json_string() {
    local value="$1"
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}
    value=${value//$'\r'/\\r}
    value=${value//$'\t'/\\t}
    printf '"%s"' "$value"
}

scenario=${FAKE_AGENT_SCENARIO:?}
count=0
if [ -f "$scenario.n" ]; then read -r count < "$scenario.n" || :; fi
printf '%s\n' "$((count + 1))" > "$scenario.n"
# The marker preserves trailing newlines stripped by command substitution.
input=$(cat; printf x)
input=${input%x}
{
    printf '{"argv":['
    sep=''
    for arg in "$@"; do printf '%s' "$sep"; json_string "$arg"; sep=','; done
    printf '],"stdin":'
    json_string "$input"
    printf '}\n'
} >> "$scenario.log"
if [ ! -f "$scenario/$count.sh" ]; then
    printf '%s\n' 'fake agent: scenario exhausted' >&2
    exit 99
fi
. "$scenario/$count.sh"
