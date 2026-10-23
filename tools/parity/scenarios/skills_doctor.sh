#!/bin/bash
parity_repo

git_dir="$(dirname "$(command -v git)")"
export PATH="$git_dir:/usr/bin:/bin"

docflow init --skills
docflow install-skills --agent claude
docflow install-skills --agent codex
docflow install-skills --agent pi
docflow install-skills --target "$HOME/custom target"
docflow install-skills --project --agent claude
docflow install-skills --project --agent codex
docflow install-skills --project --agent pi

docflow doctor --agent claude || [ "$?" -eq 6 ]
HOME="$HOME/empty-home" docflow doctor --agent claude || [ "$?" -eq 6 ]

mkdir -p "$HOME/no-project/docs"
printf '%s\n' '---' 'docflow: 2' '---' >"$HOME/no-project/docs/docflow.md"
cd "$HOME/no-project"
HOME="$HOME/empty-home" docflow doctor --agent claude || [ "$?" -eq 6 ]
