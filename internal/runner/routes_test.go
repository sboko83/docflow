package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mixedRunFixture(t *testing.T, probe bool) *runFixture {
	f := newRunFixture(t)
	text := loopHeader
	for i, tier := range []string{"fast", "", "strong", "max"} {
		n := i + 1
		unit := fmt.Sprintf(loopUnit, n, fmt.Sprint(n), fmt.Sprintf("u%d.txt", n), fmt.Sprintf("u%d.txt", n), n, fmt.Sprint(n))
		if tier != "" {
			unit = strings.Replace(unit, "Commit:", "Model: "+tier+"\nCommit:", 1)
		}
		text += unit
	}
	f.write(planFile, text)
	f.commit("mixed")
	fake, _ := filepath.Abs("../agentio/testdata/fake_agent.sh")
	config := "[tiers]\nfast = {agent = \"cl\", model = \"small\"}\ndefault = {agent = \"cx\", model = \"luna\", effort = \"low\"}\nstrong = {agent = \"cl\", model = \"big\", effort = \"high\"}\nmax = {agent = \"cx\", model = \"huge\", effort = \"xhigh\"}\n"
	config += "[agents.cl]\ncmd = [\"/bin/bash\", " + jsonString(fake) + ", \"cl\"]\nmodel_args = [\"--model\", \"{model}\"]\neffort_args = [\"--effort\", \"{effort}\"]\nresume_args = [\"--resume\", \"{session}\"]\nresult = \"json:structured_output\"\ntext = \"json:result\"\nsession = \"json:session_id\"\nerror = \"json:is_error\"\nstream = \"claude\"\n"
	if probe {
		config += "quota = [\"printf\", \"%s\", \"Current session: 5% used\\nCurrent week (all models): 6% used\\n\"]\n"
	}
	config += "[agents.cx]\ncmd = [\"/bin/bash\", " + jsonString(fake) + ", \"cx\"]\nmodel_args = [\"-m\", \"{model}\"]\neffort_args = [\"-c\", \"model_reasoning_effort={effort}\"]\nresume_args = [\"resume\", \"{session}\"]\nresult = \"json:text\"\ntext = \"json:text\"\nsession = \"json:thread_id\"\nerror = \"json:is_error\"\nstream = \"codex\"\n"
	if probe {
		file := filepath.Join(f.tmp, "probe.json")
		f.write(file, `{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":18,"windowDurationMins":300,"resetsAt":1791976360},"secondary":{"usedPercent":29,"windowDurationMins":10080,"resetsAt":1792476000}}}}`+"\n")
		config += "quota = [\"/bin/bash\", " + jsonString(fake) + ", \"--app-server\", " + jsonString(file) + "]\n"
	}
	f.setConfig(config)
	return f
}

func codexStep(f *runFixture, n int, five, seven float64, write bool) string {
	thread := fmt.Sprintf("t%d", n)
	rollout := filepath.Join(os.Getenv("CODEX_HOME"), "sessions/2026/10/14/rollout-x-"+thread+".jsonl")
	limits := fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":%g,"window_minutes":300,"resets_at":1791976360},"secondary":{"used_percent":%g,"window_minutes":10080,"resets_at":1792476000}}}}`, five, seven)
	step := "mkdir -p " + shellQuote(filepath.Dir(rollout)) + "\nprintf '%s\\n' " + shellQuote(limits) + " > " + shellQuote(rollout) + "\n"
	if write {
		step += fmt.Sprintf("echo a > u%d.txt\n", n)
	}
	step += emit(map[string]any{"type": "thread.started", "thread_id": thread})
	step += emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": `{"status":"done","handoff":["h"]}`}})
	step += emit(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 1}})
	return step
}

func TestRunMixedRoutesAndQuotas(t *testing.T) {
	for _, mode := range []string{"routes", "failed-codex", "start-quota", "only-routed"} {
		t.Run(mode, func(t *testing.T) {
			probe := mode == "start-quota" || mode == "only-routed"
			f := mixedRunFixture(t, probe)
			steps := []string{quotaEvent(.08, .07, 1791904200) + writeReply("u1.txt"), codexStep(f, 2, 20, 30, true), quotaEvent(.11, .08, 1791904200) + writeReply("u3.txt"), codexStep(f, 4, 25, 31, true)}
			args := []string{}
			code := 0
			if mode == "failed-codex" {
				steps = steps[:2]
				steps[1] = codexStep(f, 2, 20, 30, false)
				args = []string{"--attempts", "1"}
				code = 4
			}
			if mode == "only-routed" {
				steps = steps[:1]
				args = []string{"--max-units", "1"}
			}
			r := f.run(steps, args...)
			f.check(r, code)
			lines := strings.Split(strings.TrimSpace(r.out), "\n")
			if mode == "routes" {
				want := []string{"cl --model small", "cx -m luna -c model_reasoning_effort=low", "cl --model big --effort high", "cx -m huge -c model_reasoning_effort=xhigh"}
				for i, c := range f.calls() {
					if strings.Join(c.Argv, " ") != want[i] {
						t.Fatal(f.calls())
					}
				}
				if !strings.HasSuffix(lines[len(lines)-1], "cl 5h 11% (+3) · 7d 8% (+1); cx 5h 25% (+5) · 7d 31% (+1)") {
					t.Fatal(r.out)
				}
			}
			if mode == "failed-codex" {
				f.check(r, 4, "quota: cx 5h 20% · 7d 30%", "run: 1 unit,", "cx 5h 20% (+0) · 7d 30% (+0)")
			}
			if mode == "start-quota" {
				if lines[1] != "quota: cl 5h 5% · 7d 6%; cx 5h 18% · 7d 29%" {
					t.Fatal(r.out)
				}
				f.check(r, 0, "quota: cx 5h 20% (+2) · 7d 30% (+1)", "cx 5h 25% (+7) · 7d 31% (+2)")
			}
			if mode == "only-routed" {
				if lines[1] != "quota: cl 5h 5% · 7d 6%" {
					t.Fatal(r.out)
				}
			}
		})
	}
}

func TestRunStartQuota(t *testing.T) {
	for _, mode := range []string{"probe", "missing-probe"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			reset := (time.Now().Unix()/60 + 120) * 60
			text := "Current session: 5% used · resets " + time.Unix(reset, 0).UTC().Format("Jan 2 at 3:04pm") + " (UTC)"
			cmd := `["printf", "%s", ` + jsonString(text) + `]`
			if mode == "missing-probe" {
				cmd = `["/no/such/claude"]`
			}
			f.configure("stream = \"claude\"\nquota = " + cmd + "\n")
			step := writeReply("a.txt")
			if mode == "probe" {
				step = quotaEvent(.08, .07, int(reset+30)) + step
			}
			r := f.run([]string{step, writeReply("b.txt")})
			f.check(r, 0)
			if mode == "probe" {
				f.check(r, 0, "quota: fake 5h 5%", "quota: fake 5h 8% (+3) · 7d 7%", "fake 5h 8% (+3) · 7d 7% (+0)")
			} else if strings.Contains(r.out, "quota:") {
				t.Fatal(r.out)
			}
		})
	}
}

func executorRunFixture(t *testing.T) *runFixture {
	f := newRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nexecutor: claude | claude-opus-5-5 | high")
	f.replace(planFile, "Commit: P001.1.2:", "Executor: codex | gpt-5.5 | -\nCommit: P001.1.2:")
	bin := filepath.Join(f.tmp, "bin")
	fake, _ := filepath.Abs("../agentio/testdata/fake_agent.sh")
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(bin, name)
		f.write(path, "#!/bin/sh\ncase \"$*\" in *'/usage'*|*app-server*) printf '%s\\n' "+shellQuote(name)+"\" $*\" >> \"$PROBE_LOG\"; exit 0;; esac\nexec /bin/bash "+shellQuote(fake)+" \"$@\"\n")
		_ = os.Chmod(path, 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PROBE_LOG", filepath.Join(f.tmp, "probes"))
	t.Setenv("DOCFLOW_AGENTS", filepath.Join(f.tmp, "missing.toml"))
	f.write(filepath.Join(f.tmp, "docflow/agents.toml"), "[tiers]\nodd = {}\n")
	return f
}

func TestDryRunFallback(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -; pi | zai/glm-5.3 | high")
	r := f.cli("run", "--dry-run", "--all")
	f.check(r, 0, "fallback: codex gpt-5.5 -; pi zai/glm-5.3 high")
	fallbackAt := strings.Index(r.out, "fallback:")
	lastRouteAt := strings.LastIndex(r.out, "P001.1.2 [ ] Second unit:")
	if fallbackAt < 0 || lastRouteAt < 0 || fallbackAt < lastRouteAt {
		t.Fatalf("fallback line is not after the unit routes:\n%s", r.out)
	}

	bad := executorRunFixture(t)
	bad.replace(planFile, "status: active", "status: active\nfallback: codex")
	failed := bad.cli("run", "--dry-run", "--all")
	bad.check(failed, 2, "fallback: expected \"agent | model | effort\"")
}

func TestRunExecutor(t *testing.T) {
	for _, mode := range []string{"dry", "override", "haiku", "flags", "bad-assignment", "retry", "full-no", "full-yes", "zero", "replan-quota", "replan-off"} {
		t.Run(mode, func(t *testing.T) {
			f := executorRunFixture(t)
			args := []string{"run", "--no-wait", "--dry-run"}
			code, text := 0, "agent: claude\nmodel: claude-opus-5-5\neffort: high\n"
			switch mode {
			case "override":
				f.replace(planFile, "[ ] P001.1.1", "[x] P001.1.1")
				text = "agent: codex\nmodel: gpt-5.5\neffort: -\n"
			case "haiku":
				f.replace(planFile, "claude | claude-opus-5-5 | high", "claude | claude-haiku-5-5 | -")
				text = "--model claude-haiku-5-5 --allowedTools Write Edit Bash"
			case "flags":
				for _, flag := range []string{"--config", "--agent", "--model", "--effort"} {
					for _, dry := range []bool{true, false} {
						a := []string{"run", flag, "x"}
						if dry {
							a = append(a, "--dry-run")
						}
						f.check(f.cli(a...), 2, "assigns executors; "+flag+" would replace them")
					}
				}
				return
			case "bad-assignment":
				f.replace(planFile, "executor: claude | claude-opus-5-5 | high\n", "")
				code, text = 2, "unit P001.1.1: no executor"
			case "retry":
				f.steps(doneReply(), writeReply("a.txt"))
				args = []string{"run", "--no-wait", "--max-units", "1"}
				text = "done:"
			case "full-no", "full-yes":
				f.replace(planFile, "status: active", "status: active\npermissions: full")
				args = []string{"run", "--no-wait", "--max-units", "1"}
				code, text = 2, "--yes"
				if mode == "full-yes" {
					args = append(args, "--yes")
					code, text = 0, ", permissions full"
					f.steps(writeReply("a.txt"))
				}
			case "replan-quota", "replan-off":
				f.replace(planFile, "executor: claude | claude-opus-5-5 | high", "executor: claude | claude-opus-5-5 | high\nreplan: codex | gpt-5.5 | -")
				f.steps(writeReply("a.txt"))
				args = []string{"run", "--no-wait", "--max-units", "1"}
				if mode == "replan-off" {
					args = append(args, "--replans", "0")
				}
				text = "done:"
			case "zero":
				args = []string{"run", "--max-units", "0", "--attempts", "2"}
				code, text = 1, "nothing to do"
			}
			r := f.cli(args...)
			f.check(r, code, text)
			if mode == "retry" {
				calls := f.calls()
				if len(calls) != 2 {
					t.Fatal(calls)
				}
				for _, c := range calls {
					if !strings.Contains(strings.Join(c.Argv, " "), "--model claude-opus-5-5 --effort high") {
						t.Fatal(c)
					}
				}
				if !strings.Contains(strings.Join(calls[1].Argv, " "), "--resume s1") {
					t.Fatal(calls)
				}
				b, _ := os.ReadFile(filepath.Join(f.tmp, "probes"))
				if string(b) != "claude -p /usage\n" {
					t.Fatal(string(b))
				}
			}
			if mode == "replan-quota" || mode == "replan-off" {
				b, _ := os.ReadFile(filepath.Join(f.tmp, "probes"))
				if got := strings.Contains(string(b), "codex app-server"); got != (mode == "replan-quota") || !strings.Contains(string(b), "claude -p /usage") {
					t.Fatal(string(b))
				}
			}
			if mode == "full-yes" {
				argv := strings.Join(f.calls()[0].Argv, " ")
				if !strings.Contains(argv, "--permission-mode bypassPermissions") || strings.Contains(argv, "--permission-prompts") {
					t.Fatal(argv)
				}
			}
		})
	}
}

func TestRunLegacyConfig(t *testing.T) {
	for _, mode := range []string{"tiers", "user-config", "missing-explicit"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			f.write(filepath.Join(f.tmp, "docflow/agents.toml"), "[tiers]\nodd = {}\n")
			args := []string{"run", "--dry-run"}
			code, text := 2, "unknown tier 'odd'"
			if mode == "tiers" {
				_ = os.Remove(filepath.Join(f.tmp, "docflow/agents.toml"))
				args = append(args, "--model", "strong", "--effort", "low")
				code, text = 0, "model: opus\neffort: low"
			}
			if mode == "missing-explicit" {
				t.Setenv("DOCFLOW_AGENTS", filepath.Join(f.tmp, "missing.toml"))
				text = "missing.toml: agents config not found"
			}
			f.check(f.cli(args...), code, text)
		})
	}
}
