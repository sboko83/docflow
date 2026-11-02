#!/bin/bash
# Streaming stand-in for `codex exec --json` in the tui-live driver tests.
set -eu
if [ "${1:-}" = 'app-server' ] || [ "${1:-}" = '--app-server' ]; then
    while IFS= read -r request; do
        case "$request" in
            *'"id":1'*) printf '%s\n' '{"id":1,"result":{}}' ;;
            *'"id":2'*)
                if [ -n "${2:-}" ] && [ -f "${2:-}" ]; then
                    cat "$2"
                else
                    printf '%s\n' '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":5,"windowDurationMins":300,"resetsAt":1791976360}}}}'
                fi
                exit 0
                ;;
        esac
    done
    exit 0
fi
if [ "${1:-}" = "--version" ]; then printf '%s\n' 'codex 0.0.0-live-fixture'; exit 0; fi
prompt=$(cat; printf x)
prompt=${prompt%x}
unit=a
if printf '%s' "$prompt" | grep -q 'P001\.1\.2'; then unit=b; fi
content=$unit
if [ -n "${TUI_LIVE_CONTENT:-}" ] && [ "$unit" = a ]; then content=$TUI_LIVE_CONTENT; fi
sleep_for=${TUI_LIVE_SLEEP:-20}
printf '%s\n' '{"type":"thread.started","thread_id":"live-codex-thread"}'
printf '%s\n' '{"type":"turn.started"}'
if [ "${TUI_LIVE_TOOL:-1}" = 1 ]; then
    printf '%s\n' '{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"write the unit file"}}'
fi
printf '%s' "$content" > "$unit.txt"
sleep "$sleep_for"
if [ "${TUI_LIVE_TOOL:-1}" = 1 ]; then
    printf '%s\n' '{"type":"item.completed","item":{"id":"c1","type":"command_execution","aggregated_output":"ok","exit_code":0}}'
fi
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"done\",\"handoff\":[\"wrote the unit file\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":4}}'
