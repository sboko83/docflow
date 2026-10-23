#!/bin/bash
# test_repos.ReposLintTest.test_glob_outside_root
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
edit docs/docflow.md 's@repos: projects/\*@repos: ../other@'
e2e_cli 2 lint
state
