#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
parity_repo

docflow init
docflow note dropped-cache --title 'Drop cache'
peer_docflow note follow-up
