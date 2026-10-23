package agentio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/cycle"
)

func rollout(t *testing.T, home, thread string, limits ...any) {
	t.Helper()
	path := filepath.Join(home, "sessions", "2026", "10", "14", "rollout-t-"+thread+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var events []any
	for _, limit := range limits {
		events = append(events, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "token_count", "rate_limits": limit}})
	}
	if err := os.WriteFile(path, []byte(lines(events...)), 0o644); err != nil {
		t.Fatal(err)
	}
}
func window(percent, minutes, reset float64) map[string]any {
	return map[string]any{"used_percent": percent, "window_minutes": minutes, "resets_at": reset}
}

func TestCodexQuotaLabelsAndLastEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	rollout(t, home, "t1", map[string]any{"primary": window(1, 300, 1791976360), "secondary": nil}, nil, map[string]any{"primary": window(15, 300, 1791976360), "secondary": window(40, 10080, 1791976360), "rate_limit_reached_type": nil})
	equal(t, CodexQuota("t1"), Quota{{"5h", 0.15, float64(1791976360)}, {"7d", 0.4, float64(1791976360)}})
	rollout(t, home, "t2", map[string]any{"primary": window(10, 43200, 1791976360), "secondary": window(5, 90, 1791976360)})
	equal(t, CodexQuota("t2"), Quota{{"30d", 0.1, float64(1791976360)}, {"2h", 0.05, float64(1791976360)}})
	equal(t, WindowLabel(true), "")
	equal(t, WindowLabel(150), "2h")
}

func TestCodexQuotaMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	equal(t, CodexQuota("nope"), Quota{})
	equal(t, CodexQuota(""), Quota{})
	rollout(t, home, "t3", nil)
	equal(t, CodexQuota("t3"), Quota{})
	// Only the newest sorted rollout is read, and a non-object event does not
	// replace the last valid limits in it.
	rollout(t, home, "t4", map[string]any{"primary": window(20, 300, 10)}, nil)
	path := filepath.Join(home, "sessions", "2026", "10", "14", "rollout-z-t4.jsonl")
	if err := os.WriteFile(path, []byte("noise\n[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	equal(t, CodexQuota("t4"), Quota{})
}

func codexFailed() AgentRun {
	return AgentRun{Code: 1, Stdout: lines(obj(`{"type":"thread.started","thread_id":"t1"}`), obj(`{"type":"error","message":"You hit your usage limit"}`))}
}
func TestCodexLimitReset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	agent := profile(t, "codex")
	rollout(t, home, "t1", map[string]any{"primary": window(100, 300, 1791970000), "secondary": window(100, 10080, 1791980000)})
	equal(t, *LimitReset(agent, codexFailed()), float64(1791980000))
	equal(t, LimitUntil(agent, "x", 1791900000, floatPtr(1791980000)), float64(1791980060))
	rollout(t, home, "t1", map[string]any{"primary": window(50, 300, 1791970000), "secondary": nil, "rate_limit_reached_type": "primary"})
	equal(t, *LimitReset(agent, codexFailed()), float64(1791970000))
	rollout(t, home, "t1", map[string]any{"primary": window(50, 300, 1791970000), "secondary": nil})
	equal(t, LimitReset(agent, codexFailed()), (*float64)(nil))
	run := codexFailed()
	run.TimedOut = true
	equal(t, LimitReset(agent, run), (*float64)(nil))
}

func TestClaudeRejected(t *testing.T) {
	agent := profile(t, "claude")
	event := obj(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1791970000}}`)
	result := obj(`{"type":"result","is_error":true,"result":"nope"}`)
	run := AgentRun{Code: 1, Stdout: lines(event, result)}
	equal(t, *LimitReset(agent, run), float64(1791970000))
	table(event["rate_limit_info"])["status"] = "allowed"
	run.Stdout = lines(event, result)
	equal(t, LimitReset(agent, run), (*float64)(nil))
	table(event["rate_limit_info"])["status"] = "rejected"
	result["is_error"] = false
	run.Code = 0
	run.Stdout = lines(event, result)
	equal(t, LimitReset(agent, run), (*float64)(nil))
	result["is_error"] = true
	table(event["rate_limit_info"])["resetsAt"] = float64(1791970000000)
	run.Stdout = lines(event, result)
	equal(t, *LimitReset(agent, run), float64(1791970000))
}

func TestLimitRegexFallback(t *testing.T) {
	for _, c := range []struct {
		text      string
		now, want float64
	}{
		{"resets 1791970000", 1791900000, 1791970060},
		{"resets 1791970000000", 1791900000, 1791970060},
		{"resets 11791970000000", 100, 1900},
		{"1791970000 and 1791980000", 1791975000, 1791976800},
	} {
		equal(t, LimitUntil(map[string]any{}, c.text, c.now, nil), c.want)
	}
	equal(t, LimitUntil(map[string]any{"limit_wait": 5}, "none", 100, nil), float64(105))
	equal(t, Epoch(true), (*float64)(nil))
	equal(t, *Epoch(1791970000000.0), float64(1791970000))
}

const usageText = "You are currently using your subscription\n\n" +
	"Current session: 41% used · resets Oct 19 at 5:50pm (UTC)\n" +
	"Current week (all models): 32% used · resets Oct 20 at 9am (UTC)\n" +
	"Current week (Sonnet only): 3% used · resets Oct 20 at 9am (UTC)\n"

func TestUsageSessionAndWeek(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	equal(t, ParseUsage(usageText, now), Quota{
		{"five_hour", 0.41, float64(time.Date(2026, 10, 19, 17, 50, 0, 0, time.UTC).Unix())},
		{"seven_day", 0.32, float64(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).Unix())},
	})
	equal(t, *UsageReset("9:05am (UTC)", now), float64(time.Date(2026, 10, 19, 9, 5, 0, 0, time.UTC).Unix()))
	equal(t, *UsageReset("Jan 2 at 12am (UTC)", time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC)), float64(time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC).Unix()))
	equal(t, *UsageReset("Oct 19 at 5:49pm (Europe/Moscow)", now), float64(time.Date(2026, 10, 19, 14, 49, 0, 0, time.UTC).Unix()))
	for _, text := range []string{"Oct 19 at 5pm (Nowhere/X)", "soon", "Feb 30 at 3pm (UTC)", "Oct 19 at 3:99pm (UTC)", "Bad 19 at 3pm (UTC)"} {
		equal(t, UsageReset(text, now), (*float64)(nil))
	}
	equal(t, ParseUsage("Current session: 7% used\n", now), Quota{{"five_hour", 0.07, nil}})
	equal(t, ParseUsage("Unknown command: /usage", now), Quota{})
}

func TestFailedRunsOnly(t *testing.T) {
	agent := profile(t, "claude")
	run := AgentRun{Stdout: `{"result":"usage limit; not logged in","is_error":false}`}
	equal(t, LimitText(agent, run), (*string)(nil))
	equal(t, AuthReason(agent, run), "")
	run.Stdout = `{"result":"usage limit; not logged in","is_error":true}`
	if LimitText(agent, run) == nil {
		t.Fatal("missing limit")
	}
	equal(t, AuthReason(agent, run), "usage limit; not logged in")
	run.TimedOut = true
	equal(t, FailureText(agent, run), (*string)(nil))
	run = AgentRun{Code: 1, Stderr: "\n  actual failure\nextra"}
	equal(t, FailureReason(agent, run), "actual failure")
	run = AgentRun{Code: 1, Stderr: "not logged in"}
	equal(t, AuthReason(agent, run), "not logged in")
}

func TestRunUsageNumbers(t *testing.T) {
	agent := profile(t, "claude")
	run := AgentRun{Stdout: fixture(t, "claude")}
	equal(t, *RunCost(agent, run), 0.25)
	equal(t, *RunTokens(agent, run), 125)
	equal(t, *RunOutputTokens(agent, run), 7)
	equal(t, *RunTurns(agent, run), 2)
	run.Stdout = `{"total_cost_usd":true,"usage":{"input_tokens":true,"output_tokens":false},"num_turns":"2"}`
	equal(t, RunCost(agent, run), (*float64)(nil))
	equal(t, RunTokens(agent, run), (*int)(nil))
	equal(t, RunOutputTokens(agent, run), (*int)(nil))
	equal(t, RunTurns(agent, run), (*int)(nil))
	run.Stdout = `{"usage":{"input_tokens":1.9,"cache_read_input_tokens":2.9},"num_turns":3.9}`
	equal(t, *RunTokens(agent, run), 4)
	equal(t, *RunTurns(agent, run), 3)
}

type testProgress struct {
	lines   []string
	spins   []string
	stopped bool
}

func (p *testProgress) Line(s string, isErr bool) {
	p.lines = append(p.lines, s)
	if !isErr {
		panic("expected stderr")
	}
}
func (p *testProgress) Spin(s string) { p.spins = append(p.spins, s) }
func (p *testProgress) Stop()         { p.stopped = true }
func TestWaitLimit(t *testing.T) {
	p := &testProgress{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitLimit(ctx, map[string]any{}, "none", p, nil); err != context.Canceled {
		t.Fatal(err)
	}
	if !p.stopped || len(p.lines) != 1 || !strings.HasPrefix(p.lines[0], "usage limit; waiting until ") || len(p.spins) != 1 {
		t.Fatal(p)
	}
	p = &testProgress{}
	if err := WaitLimit(context.Background(), map[string]any{"limit_wait": -1}, "", p, nil); err != nil {
		t.Fatal(err)
	}
	equal(t, p.stopped, true)
}

func TestDurationSummaryAndAdd(t *testing.T) {
	for _, c := range []struct {
		seconds float64
		want    string
	}{{0, "0s"}, {1.5, "2s"}, {2.5, "2s"}, {59.5, "1m00s"}, {61, "1m01s"}, {3599, "59m59s"}, {3600, "1h00m"}, {7320, "2h02m"}} {
		equal(t, Duration(c.seconds), c.want)
	}
	equal(t, Summary(61, nil), "1m01s")
	equal(t, Summary(61, floatPtr(0.25)), "1m01s, $0.25")
	equal(t, Add(nil, nil), (*float64)(nil))
	equal(t, *Add(nil, floatPtr(1)), float64(1))
	equal(t, *Add(floatPtr(1), floatPtr(2)), float64(3))
}

func TestRecordCall(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	if err := cycle.WriteStats(root, "P001.1.1", map[string]any{"input_tokens": 12, "sessions": "malformed", "calls": nil}); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		session string
		tokens  *int
		cost    *float64
	}{{"s1", intPtr(10), floatPtr(0.25)}, {"s1", intPtr(20), floatPtr(0.5)}, {"s2", nil, nil}, {"", nil, nil}} {
		if err := RecordCall(root, "P001.1.1", call.session, call.tokens, call.cost); err != nil {
			t.Fatal(err)
		}
	}
	equal(t, cycle.ReadStats(root, "P001.1.1"), obj(`{"unit":"P001.1.1","input_tokens":12,"sessions":["s1","s2"],"tokens":30,"cost":0.75,"calls":4}`))
	dir, err := RunDir(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, dir, filepath.Join(resolved, ".git", "docflow", "run"))
}
