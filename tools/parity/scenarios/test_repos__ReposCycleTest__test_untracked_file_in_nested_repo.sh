#!/bin/bash
# test_repos.ReposCycleTest.test_untracked_file_in_nested_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
e2e_cli 0 prompt
e2e_cli 0 verify P001.1.1
printf 'let b = 2\n' >projects/a/new.swift
e2e_cli 5 done P001.1.1 --handoff h
state
