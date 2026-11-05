#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
parity_repo
docflow -h
for command in init status next prompt verify done commit finish stats block unblock \
    lint new activate close freeze thaw note migrate install-skills doctor run; do
    docflow "$command" -h
done
# The error is part of the transcript, not a scenario setup failure.
docflow parity-unknown-command || [ "$?" -eq 2 ]
