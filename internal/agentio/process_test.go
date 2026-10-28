package agentio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/testshim"
)

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
func quote(text string) string { return "'" + strings.ReplaceAll(text, "'", `'"'"'`) + "'" }
func fakeScenario(t *testing.T, steps ...string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	scenario := filepath.Join(root, "steps")
	if err := os.Mkdir(scenario, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, step := range steps {
		writeFile(t, filepath.Join(scenario, fmt.Sprintf("%d.sh", i)), step)
	}
	path, err := filepath.Abs("testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGENT_SCENARIO", filepath.ToSlash(scenario))
	return root, []string{testshim.Bash(t), filepath.ToSlash(path)}
}

func fakeRun(t *testing.T, stdout, stderr string, code int) AgentRun {
	t.Helper()
	root, argv := fakeScenario(t, "printf '%s' "+quote(stdout)+"\nprintf '%s' "+quote(stderr)+" >&2\nexit "+fmt.Sprint(code)+"\n")
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "run.out"), CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCallAgentFakeStreams(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("testdata", name+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			root, argv := fakeScenario(t, "cat "+quote(path)+"\nprintf 'diagnostic' >&2\n")
			var events []map[string]any
			var decoded []map[string]any
			stdin := "prompt\n{model}\n"
			log := filepath.Join(root, "run.out")
			argv = append(argv, "--mode", "json", "space ' quote")
			run, err := CallAgent(root, argv, &stdin, log, CallOptions{OnEvent: func(e map[string]any) { events = append(events, e) }, OnStreamEvent: func(e Event) {
				if event := StreamEvent(profile(t, name), e); event != nil {
					decoded = append(decoded, event)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			equal(t, run.Code, 0)
			equal(t, run.Stdout, fixture(t, name))
			equal(t, run.Stderr, "diagnostic")
			equal(t, run.TimedOut, false)
			if len(events) == 0 {
				t.Fatal("no live events")
			}
			if name == "claude" {
				equal(t, decoded[0]["quota"], Quota{{"five_hour", 0.08, float64(10)}, {"seven_day", 0.07, float64(20)}})
			}
			data := AgentOutput(profile(t, name), run.Stdout)
			if JSONPath(data, str(profile(t, name)["session"])) == nil {
				t.Fatal(data)
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, string(b), "$ "+shellJoin(argv)+"\n--- stdout\n"+run.Stdout+"\n--- stderr\ndiagnostic\n--- exit 0\n")
			b, err = os.ReadFile(os.Getenv("FAKE_AGENT_SCENARIO") + ".log")
			if err != nil {
				t.Fatal(err)
			}
			entry := ParseLine(strings.TrimSpace(string(b)))
			equal(t, entry["stdin"], stdin)
			wantArgs := []any{}
			for _, arg := range argv[2:] {
				wantArgs = append(wantArgs, arg)
			}
			equal(t, entry["argv"], wantArgs)
		})
	}
}

func TestCallAgentLiveEventsAndCallbackFailure(t *testing.T) {
	root, argv := fakeScenario(t, "printf '%s\\n' '{\"type\":\"ready\"}'\nwhile [ ! -f continue ]; do sleep 0.01; done\nprintf '%s' '{\"type\":\"end\"}'\n")
	n := 0
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "log"), CallOptions{Timeout: 3 * time.Second, OnEvent: func(event map[string]any) {
		n++
		if event["type"] == "ready" {
			writeFile(t, filepath.Join(root, "continue"), "")
			panic("callback failure is ignored")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, n, 2)
	equal(t, run.Code, 0)
	equal(t, run.TimedOut, false)
}

func TestCallAgentLongOutputAndReplacement(t *testing.T) {
	root, argv := fakeScenario(t, "cat payload\nprintf '\\377\\376\\r\\nend\\r' >&2\nexit 3\n")
	payload := lines(map[string]any{"type": "large", "text": strings.Repeat("я", 100000)})
	writeFile(t, filepath.Join(root, "payload"), payload)
	events := 0
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "log"), CallOptions{OnEvent: func(e map[string]any) { events++ }})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.Code, 3)
	equal(t, run.Stdout, payload)
	equal(t, run.Stderr, "��\nend\n")
	equal(t, events, 1)
}

func TestCallAgentSequenceAndEnvironment(t *testing.T) {
	root, argv := fakeScenario(t, "printf '%s' \"$DOCFLOW_RUNNER_UNIT\"\n", "printf '%s' second\nexit 2\n")
	env := append(os.Environ(), RunnerEnv+"=P001.1.1")
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "one"), CallOptions{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.Stdout, "P001.1.1")
	run, err = CallAgent(root, argv, nil, filepath.Join(root, "two"), CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.Stdout, "second")
	equal(t, run.Code, 2)
	run, err = CallAgent(root, argv, nil, filepath.Join(root, "three"), CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.Code, 99)
	equal(t, run.Stderr, "fake agent: scenario exhausted\n")
}

func TestCallAgentTimeoutAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			root, argv := fakeScenario(t, "sleep 60 &\nprintf '%s\\n' '{\"type\":\"ready\"}'\nwait\n")
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			options := CallOptions{Timeout: 200 * time.Millisecond, Context: ctx}
			if cancel {
				options.Timeout = 3 * time.Second
				options.OnEvent = func(map[string]any) { stop() }
			}
			start := time.Now()
			log := filepath.Join(root, "log")
			run, err := CallAgent(root, argv, nil, log, options)
			if cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			equal(t, run.TimedOut, !cancel)
			if time.Since(start) > 3*time.Second {
				t.Fatal("group cleanup did not close child pipes")
			}
			if runtime.GOOS != "windows" {
				equal(t, run.Code, -15)
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, strings.Contains(string(b), "(timed out)"), !cancel)
		})
	}
}

func TestCallAgentKillEscalation(t *testing.T) {
	root, argv := fakeScenario(t, "trap '' TERM\nsleep 60 &\nwait\n")
	start := time.Now()
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "log"), CallOptions{Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.TimedOut, true)
	if runtime.GOOS == "windows" {
		// No signals: the TERM trap cannot be honored, the job force-kills
		// the tree at once instead of escalating.
		equal(t, run.Code, 1)
		if elapsed := time.Since(start); elapsed >= KillGrace {
			t.Fatalf("force kill took %s, want well under the grace", elapsed)
		}
		return
	}
	equal(t, run.Code, -9)
	if elapsed := time.Since(start); elapsed < KillGrace || elapsed > KillGrace+3*time.Second {
		t.Fatalf("kill escalation took %s", elapsed)
	}
}

func TestCallAgentStartError(t *testing.T) {
	_, err := CallAgent(t.TempDir(), []string{"/no/such/agent"}, nil, "", CallOptions{})
	var agentErr AgentError
	if !errors.As(err, &agentErr) || agentErr.ExitCode() != 7 || !strings.Contains(err.Error(), "run: cannot start /no/such/agent:") {
		t.Fatal(err)
	}
	equal(t, err.Error(), "run: cannot start /no/such/agent: [Errno 2] No such file or directory: '/no/such/agent'")
}

func TestUTF8Replacement(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"\xff\xfe", "��"}, {"\xe2\x82", "�"}, {"\xe2\x82x", "�x"}, {"\xed\xa0\x80", "���"}, {"\xf0\x9f\x92", "�"}, {"\xf4\x90\x80\x80", "����"}, {"aя😀\r\nx\r", "aя😀\nx\n"},
	} {
		equal(t, textOutput(c.raw), c.want)
	}
}

func TestStreamUniversalNewlines(t *testing.T) {
	var events []map[string]any
	w := &eventWriter{callback: func(e Event) { events = append(events, e.Data) }}
	// CRLF can straddle reads; CR alone also terminates a line in Python.
	for _, chunk := range []string{"{\"n\":1}\r", "\n{\"n\":2}\r{\"n\":3}\n"} {
		_, _ = w.Write([]byte(chunk))
	}
	equal(t, events, []map[string]any{obj(`{"n":1}`), obj(`{"n":2}`), obj(`{"n":3}`)})
}

func TestProbeQuotaClaude(t *testing.T) {
	_, argv := fakeScenario(t, "printf '%s\\n' 'Current session: 5% used'\n", "exit 1\n")
	agent := map[string]any{"stream": "claude", "quota": argv}
	equal(t, ProbeQuota(agent, time.Second), Quota{{"five_hour", 0.05, nil}})
	equal(t, ProbeQuota(agent, time.Second), Quota{})
	equal(t, ProbeQuota(map[string]any{"stream": "claude", "quota": []string{"/no/such"}}, time.Second), Quota{})
}

func TestProbeQuotaCodex(t *testing.T) {
	root, argv := fakeScenario(t)
	response := filepath.Join(root, "response")
	requestLog := filepath.Join(root, "requests")
	t.Setenv("FAKE_PROBE_LOG", requestLog)
	writeFile(t, response, `{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":18,"windowDurationMins":300,"resetsAt":1791976360},"secondary":{"usedPercent":29,"windowDurationMins":10080,"resetsAt":1792476000}}}}`+"\n")
	agent := map[string]any{"stream": "codex", "quota": append(argv, "--app-server", response)}
	equal(t, ProbeQuota(agent, time.Second), Quota{{"5h", 0.18, float64(1791976360)}, {"7d", 0.29, float64(1792476000)}})
	b, err := os.ReadFile(requestLog)
	if err != nil {
		t.Fatal(err)
	}
	requests := strings.Split(strings.TrimSpace(string(b)), "\n")
	equal(t, len(requests), 3)
	equal(t, ParseLine(requests[0]), obj(`{"id":1,"method":"initialize","params":{"clientInfo":{"name":"docflow","version":"0"}}}`))
	equal(t, ParseLine(requests[1])["method"], "initialized")
	equal(t, ParseLine(requests[2])["method"], "account/rateLimits/read")
}

func TestProbeQuotaSilentServer(t *testing.T) {
	start := time.Now()
	equal(t, ProbeQuota(map[string]any{"stream": "codex", "quota": []string{testshim.Bash(t), "-c", "sleep 60"}}, 100*time.Millisecond), Quota{})
	if time.Since(start) > 3*time.Second {
		t.Fatal("probe failed to time out")
	}
	equal(t, ProbeQuota(map[string]any{"stream": "codex", "quota": []string{testshim.Bash(t), "-c", "exit 0"}}, time.Second), Quota{})
}

func TestProbeQuotaFinalLineWithoutNewline(t *testing.T) {
	root, argv := fakeScenario(t)
	response := filepath.Join(root, "response")
	writeFile(t, response, `{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":25,"windowDurationMins":300}}}}`)
	agent := map[string]any{"stream": "codex", "quota": append(argv, "--app-server", response, "--once")}
	equal(t, ProbeQuota(agent, time.Second), Quota{{"5h", 0.25, nil}})
}

func TestProbeQuotaNoCommandAndDefaultProfiles(t *testing.T) {
	equal(t, ProbeQuota(map[string]any{"stream": "claude"}, time.Second), Quota{})
	equal(t, ProbeQuota(map[string]any{"stream": "", "quota": []string{"true"}}, time.Second), Quota{})
	// Loaded TOML arrays use []any, unlike the explicitly supplied []string.
	for name, want := range map[string]string{"claude": `["claude","-p","/usage"]`, "codex": `["codex","app-server"]`} {
		var args any
		if err := json.Unmarshal([]byte(want), &args); err != nil {
			t.Fatal(err)
		}
		equal(t, profile(t, name)["quota"], args)
	}
}

func TestShellJoin(t *testing.T) {
	equal(t, shellJoin([]string{"a", "", "x y", "a'b", "я"}), `a '' 'x y' 'a'"'"'b' 'я'`)
}
