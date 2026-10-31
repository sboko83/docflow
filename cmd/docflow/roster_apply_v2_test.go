package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The apply v2 CLI tests drive the in-process CLI against the v2 policy
// and catalog fixtures installed as the personal roster: the dispatch must
// pick the v2 path for a version 2 file and keep the v1 path untouched.

// rosterApplyV2Plan is a plan with cheap and review units and one explicit
// Executor: the v2 apply writes the role keys and leaves the unit alone.
func rosterApplyV2Plan() string {
	return `---
id: P070
title: Roster apply v2
type: feature
status: active
created: 2026-10-12
heavy: pi | zai/glm-5.3 | max
---
## Goal
Цель плана.

## Context

## Constraints
Ограничение.

## Prerequisites

## Traps
Ловушка плана.

## Decisions
- решение — почему — альтернатива

## Phase 1: First

### [ ] P070.1.1 First unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Executor: pi | zai/glm-5.3-flash | low
Commit: P070.1.1: first

### [ ] P070.1.2 Second unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: cheap
Commit: P070.1.2: second

### [ ] P070.1.3 Third unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: review
Commit: P070.1.3: third
`
}

func TestRosterApplyV2Command(t *testing.T) {
	home := rosterBriefEnv(t)
	root := makeLifecycleProject(t, []string{"P070"}, nil, map[string]string{"P070": rosterApplyV2Plan()}, false)
	initLifecycleGit(t, root)
	path := "docs/plans/P070-slug.md"
	before := readRosterFile(t, root, path)

	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P070", "--preset", "low", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("roster apply --dry-run = (%d, %q, %q)", code, stdout, stderr)
	}
	var report struct {
		Plan     string `json:"plan"`
		DryRun   bool   `json:"dry_run"`
		Changes  []struct{ Key, Old, New string }
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("dry-run JSON: %v\n%s", err, stdout)
	}
	if report.Plan != "P070" || !report.DryRun || len(report.Changes) == 0 || len(report.Warnings) == 0 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if after := readRosterFile(t, root, path); after != before {
		t.Fatalf("dry run wrote the plan:\n%s", after)
	}

	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "apply", "P070", "--preset", "low")
	if code != 0 || !strings.Contains(stdout, "commit: P070: apply roster") {
		t.Fatalf("roster apply = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "no fallback for executor") {
		t.Fatalf("roster apply stderr =\n%s\nwant the missing fallback warning", stderr)
	}
	text := readRosterFile(t, root, path)
	for _, want := range []string{
		"executor: pi | opencode-go/deepseek-v4.1 | high",
		"cheap: pi | opencode-go/deepseek-v4.1-flash | high; pi | zai/glm-5.3-flash | low",
		"review: pi | opencode-go/deepseek-v4.1 | high",
		"replan: codex | gpt-6.1-sol | medium",
		"Executor: pi | zai/glm-5.3-flash | low",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plan after apply has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "heavy:") {
		t.Errorf("apply kept a role key without units:\n%s", text)
	}
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("roster apply wrote the state file:\n%s", state)
	}
}

func TestRosterApplyV2RejectsV1Flags(t *testing.T) {
	rosterBriefEnv(t)
	root := makeLifecycleProject(t, []string{"P070"}, nil, map[string]string{"P070": rosterApplyV2Plan()}, false)
	initLifecycleGit(t, root)
	code, _, stderr := callLifecycleCLI(t, root, "roster", "apply", "P070", "--profile", "low")
	if code != 2 || !strings.Contains(stderr, "--preset NAME") {
		t.Fatalf("apply --profile = (%d, %q), want code 2 naming --preset", code, stderr)
	}
}

func TestRosterApplyV1RejectsV2Flags(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	root := rosterProject(t, rosterPlan(""))
	code, _, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001", "--preset", "low")
	if code != 2 || !strings.Contains(stderr, "roster migrate") {
		t.Fatalf("v1 apply --preset = (%d, %q), want code 2 with the migration hint", code, stderr)
	}
}
