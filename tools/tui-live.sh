#!/usr/bin/env bash
# Live acceptance driver for the terminal workspace. --run creates temporary
# projects outside this checkout and calls the real Pi and Codex CLIs; --check
# only validates the saved receipt against the current HEAD and binary.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
case "${1:-}" in
    --run|--check) ;;
    *)
        echo 'usage: tools/tui-live.sh --run|--check [options]' >&2
        exit 2
        ;;
esac
cd "$root"
exec go run ./tools/tui-live "$@"
