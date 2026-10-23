#!/bin/bash
# test_repos.ReposCommitTest.test_commit_across_repos
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
nested projects/b
nested projects/c
e2e_cli 0 prompt
printf 'print(2)\n' >a.py
printf 'let a = 2\n' >projects/a/main.swift
printf 'let b = 2\n' >projects/b/new.swift
e2e_cli 0 finish P001.1.1 --handoff h
state
observe 'for repo in projects/a projects/b projects/c; do git -C "$repo" log -1 --format=%B; git -C "$repo" status --porcelain; done'
