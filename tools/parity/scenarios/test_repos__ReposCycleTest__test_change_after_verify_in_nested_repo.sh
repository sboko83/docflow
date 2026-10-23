#!/bin/bash
# test_repos.ReposCycleTest.test_change_after_verify_in_nested_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
e2e_cli 0 prompt
printf 'let a = 2\n' >projects/a/main.swift
e2e_cli 0 verify P001.1.1
e2e_cli 0 done P001.1.1 --handoff h
printf 'let a = 3\n' >projects/a/main.swift
e2e_cli 5 commit P001.1.1
state
