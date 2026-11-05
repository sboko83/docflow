#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
parity_repo

docflow init
docflow new frozen-plan
peer_docflow activate P001
docflow freeze P001 --reason 'waiting for API'
peer_docflow thaw P001
docflow status
