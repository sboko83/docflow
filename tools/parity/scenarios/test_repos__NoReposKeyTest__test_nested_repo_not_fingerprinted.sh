#!/bin/bash
# test_repos.NoReposKeyTest.test_nested_repo_not_fingerprinted
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
edit docs/docflow.md '/^repos:/d'
save_fixture 'no repos key'
e2e_cli 0 prompt
e2e_cli 0 verify P001.1.1
printf 'let a = 3\n' >projects/a/main.swift
e2e_cli 0 done P001.1.1 --handoff h
state
