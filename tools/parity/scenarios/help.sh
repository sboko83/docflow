#!/bin/bash
parity_repo
docflow -h
for command in init status next prompt verify done commit finish stats block \
    lint new activate close freeze thaw note migrate install-skills doctor run; do
    docflow "$command" -h
done
# The error is part of the transcript, not a scenario setup failure.
docflow parity-unknown-command || [ "$?" -eq 2 ]
