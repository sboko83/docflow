package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

// A hook abstains on permitted calls: an explicit allow would override the
// user's normal permission checks. Errors deny through the same JSON contract.
func statusHook(input io.Reader, out io.Writer) int {
	var event struct {
		Event string `json:"hook_event_name"`
		Tool  string `json:"tool_name"`
		CWD   string `json:"cwd"`
		Input struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	deny := func(reason string) int {
		_ = json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason,
		}})
		return 0
	}
	decoder := json.NewDecoder(io.LimitReader(input, 1024*1024))
	if err := decoder.Decode(&event); err != nil {
		return deny("docflow: invalid hook event: " + err.Error())
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return deny("docflow: expected exactly one hook event")
	}
	if event.Event != "PreToolUse" || (event.Tool != "Bash" && event.Tool != "PowerShell") || !directGitCommit(event.Input.Command) {
		fmt.Fprintln(out, "{}")
		return 0
	}
	if event.CWD == "" {
		return deny("docflow: hook event has no cwd")
	}
	root, err := format.FindRoot(event.CWD)
	if err != nil {
		fmt.Fprintln(out, "{}")
		return 0
	}
	paths, err := filepath.Glob(filepath.Join(root, "docs", "plans", "*.md"))
	if err != nil {
		return deny("docflow: " + err.Error())
	}
	for _, path := range paths {
		plan, err := format.LoadPlan(path)
		if err != nil {
			return deny("docflow: cannot check plan: " + err.Error())
		}
		if plan.Meta["status"] != "active" {
			continue
		}
		for _, unit := range plan.Units {
			pending := false
			if unit.Status == "x" {
				pending, err = cycle.CommitPending(root, plan, unit)
				if err != nil {
					return deny("docflow: cannot check pending commit: " + err.Error())
				}
			}
			if unit.Status == "~" || unit.Status == "!" || pending {
				return deny(fmt.Sprintf("docflow: %s has unfinished work; use docflow finish %s --handoff <item> (or docflow commit after done)", unit.ID, unit.ID))
			}
		}
	}
	fmt.Fprintln(out, "{}")
	return 0
}

// This recognizes direct commands and common shell wrappers, not arbitrary
// programs, aliases or evaluated shell code. It never executes the input.
func directGitCommit(command string) bool {
	for _, words := range hookCommands(command) {
		for len(words) > 0 {
			name := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(words[0], "\\", "/"))), ".exe")
			if strings.Contains(words[0], "=") || name == "command" || name == "exec" || name == "env" || name == "&" {
				words = words[1:]
				continue
			}
			if name == "git" {
				for i := 1; i < len(words); i++ {
					arg := words[i]
					if arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree" || arg == "--namespace" || arg == "--config-env" {
						i++
						continue
					}
					if strings.HasPrefix(arg, "-") {
						continue
					}
					if arg == "commit" {
						return true
					}
					break
				}
			}
			if name == "sh" || name == "bash" || name == "zsh" || name == "pwsh" || name == "powershell" || name == "cmd" {
				for i, arg := range words[1:] {
					if arg == "-c" || arg == "-lc" || strings.EqualFold(arg, "-Command") || strings.EqualFold(arg, "/c") {
						if directGitCommit(strings.Join(words[i+2:], " ")) {
							return true
						}
					}
				}
			}
			break
		}
	}
	return false
}

func hookCommands(text string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	flushWord := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	flushCommand := func() {
		flushWord()
		if len(words) > 0 {
			commands = append(commands, words)
			words = nil
		}
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			started = true
			continue
		}
		if ch == '\\' && i+1 < len(runes) && (unicode.IsSpace(runes[i+1]) || strings.ContainsRune("\\\"';&|()", runes[i+1])) {
			i++
			word.WriteRune(runes[i])
			started = true
			continue
		}
		if strings.ContainsRune(";&|()\n\r", ch) {
			flushCommand()
			continue
		}
		if unicode.IsSpace(ch) {
			flushWord()
			continue
		}
		if ch == '#' && !started {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			flushCommand()
			continue
		}
		word.WriteRune(ch)
		started = true
	}
	flushCommand()
	return commands
}
