#!/bin/bash
# Streaming stand-in for `pi -p --mode json` in the tui-live driver tests.
# It reads the unit prompt, states a session, starts a tool, writes the file
# the unit names, sleeps while the driver pauses, ends the tool and returns
# the runner result as the last assistant message.
set -eu
if [ "${1:-}" = "--version" ]; then printf '%s\n' 'pi 0.0.0-live-fixture'; exit 0; fi
prompt=$(cat; printf x)
prompt=${prompt%x}
unit=a
if printf '%s' "$prompt" | grep -q 'P001\.1\.2'; then unit=b; fi
content=$unit
if [ -n "${TUI_LIVE_CONTENT:-}" ] && [ "$unit" = a ]; then content=$TUI_LIVE_CONTENT; fi
sleep_for=${TUI_LIVE_SLEEP:-20}
printf '%s\n' '{"type":"session","id":"live-pi-session"}'
printf '%s\n' '{"type":"agent_start"}'
printf '%s\n' '{"type":"turn_start"}'
if [ "${TUI_LIVE_TOOL:-1}" = 1 ]; then
    printf '%s\n' '{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"write the unit file"}}'
fi
printf '%s' "$content" > "$unit.txt"
sleep "$sleep_for"
if [ "${TUI_LIVE_TOOL:-1}" = 1 ]; then
    printf '%s\n' '{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":"ok","isError":false}'
fi
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","usage":{"input":10,"output":4,"cost":{"total":0}},"content":[{"type":"text","text":"{\"status\":\"done\",\"handoff\":[\"wrote the unit file\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}]}}'
printf '%s\n' '{"type":"agent_settled","aborted":false}'
