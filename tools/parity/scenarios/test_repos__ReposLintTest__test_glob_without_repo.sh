#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
# test_repos.ReposLintTest.test_glob_without_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
edit docs/docflow.md 's@repos: projects/\*@repos: projects/*, libs/*@'
e2e_cli 0 lint
state
