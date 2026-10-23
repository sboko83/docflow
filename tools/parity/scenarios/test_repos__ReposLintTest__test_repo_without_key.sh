#!/bin/bash
# test_repos.ReposLintTest.test_repo_without_key
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
edit docs/docflow.md '/^repos:/d'
e2e_cli 0 lint
state
