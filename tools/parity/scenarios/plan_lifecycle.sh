#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
parity_repo

docflow init
peer_docflow new lifecycle-check --title 'Lifecycle check' --type feature
docflow activate P001
peer_docflow close P001
docflow status
