// Command fakeagent stands in for the pi and codex agent CLIs in the bench
// tests. It is built once per test as pi/codex (.exe on Windows), so it
// resolves by bare name on every platform. The behavior comes from the file
// "fakeagent.mode" beside the executable: empty or "ok" completes the tiny
// task, "sleep" blocks, "child" starts a sleeping child (pid in child.pid)
// and waits for it.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "--sleep-child" {
		time.Sleep(60 * time.Second)
		return
	}
	exe, _ := os.Executable()
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
	modeBytes, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "fakeagent.mode"))
	mode := strings.TrimSpace(string(modeBytes))
	args := strings.Join(os.Args[1:], " ")

	if name == "pi" && strings.Contains(args, "auth print-api-key") {
		os.Exit(1)
	}
	if name == "codex" && len(os.Args) > 1 && os.Args[1] == "app-server" {
		appServer()
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	prompt := args + "\n" + string(stdin)
	switch mode {
	case "sleep":
		time.Sleep(60 * time.Second)
		return
	case "child":
		child := exec.Command(exe, "--sleep-child", "x")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_ = os.WriteFile("child.pid", []byte(strconv.Itoa(child.Process.Pid)+"\n"), 0o644)
		_ = child.Wait()
		return
	}
	if mode == "replay" && strings.Contains(prompt, "Create only a docflow plan at ") {
		if strings.Contains(prompt, "P001.1.1") {
			fmt.Fprintln(os.Stderr, "historical plan leaked to planner")
			os.Exit(1)
		}
		_, rest, _ := strings.Cut(prompt, "Create only a docflow plan at ")
		path, _, _ := strings.Cut(rest, " with id ")
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(replayPlan), 0o644)
	} else {
		if mode == "replay" {
			if _, err := os.Stat("value_test.go"); err == nil {
				fmt.Fprintln(os.Stderr, "hidden oracle leaked")
				os.Exit(1)
			}
		}
		writeValue()
	}
	if name == "codex" {
		fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"done\",\"handoff\":[\"done\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}}`)
		fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}`)
		return
	}
	fmt.Println(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"{\"status\":\"done\",\"handoff\":[\"tiny complete\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}],"usage":{"input":3,"cost":{"total":0.25}}}}`)
}

const replayPlan = `---
id: P001
title: Change the value
type: feature
status: draft
created: 2026-11-09
---
## Goal
Value returns 2.
## Context
## Constraints
Keep its signature.
## Traps
None.
## Decisions
None.
## Phase 1: Value
### [ ] P001.1.1 Change the value
Read:
- value.go
Do:
- Change Value to return 2.
Verify: ` + "`go test ./...`" + `
Commit: P001.1.1: change value
`

// writeValue rewrites value.go in the package the task already declares.
func writeValue() {
	pkg := "package tiny"
	if data, err := os.ReadFile("value.go"); err == nil {
		if first, _, _ := strings.Cut(string(data), "\n"); strings.HasPrefix(first, "package ") {
			pkg = strings.TrimSpace(first)
		}
	}
	_ = os.WriteFile("value.go", []byte(pkg+"\n\nfunc Value() int { return 2 }\n"), 0o644)
}

// appServer answers the quota probe of the codex app-server protocol.
func appServer() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, `"id":1`):
			fmt.Println(`{"id":1,"result":{}}`)
		case strings.Contains(line, `"id":2`):
			fmt.Println(`{"id":2,"result":{"rateLimits":[]}}`)
			return
		}
	}
}
