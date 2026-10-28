package runner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// zaiQuotaBody mirrors the live quota/limit response: unit 3 counts hours,
// unit 6 counts weeks, nextResetTime is in milliseconds.
func zaiQuotaBody(five, seven float64, fiveReset, sevenReset int64) string {
	return `{"code":200,"msg":"ok","success":true,"data":{"level":"pro","limits":[` +
		fmt.Sprintf(`{"type":"CREDIT_LIMIT","unit":3,"number":5,"percentage":%g,"nextResetTime":%d},`, five, fiveReset*1000) +
		fmt.Sprintf(`{"type":"CREDIT_LIMIT","unit":6,"number":1,"percentage":%g,"nextResetTime":%d}]}}`, seven, sevenReset*1000)
}

func zaiQuotaBodyWithoutResets(five, seven float64) string {
	return `{"code":200,"msg":"ok","success":true,"data":{"level":"pro","limits":[` +
		fmt.Sprintf(`{"type":"CREDIT_LIMIT","unit":3,"number":5,"percentage":%g},`, five) +
		fmt.Sprintf(`{"type":"CREDIT_LIMIT","unit":6,"number":1,"percentage":%g}]}}`, seven)
}

func zaiMinuteFloor(at time.Time) int64 { return at.Unix() / 60 * 60 }

// zaiQuotaServer serves the queued bodies in order (the last one repeats)
// with status when set, and counts requests, so tests assert what the
// runner probed and when.
type zaiQuotaServer struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies []string
	served int
	authed bool
	status int
}

func (s *zaiQuotaServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served
}

func (s *zaiQuotaServer) authorized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authed
}

// zaiRunFixture wires the zai probe to a local server: the pi stub prints a
// key (FAKE_ZAI_KEY) and DOCFLOW_ZAI_QUOTA_URL points at the server.
func zaiRunFixture(t *testing.T) (*runFixture, *zaiQuotaServer) {
	t.Helper()
	f := fallbackExecutorRunFixture(t)
	server := newZaiQuotaServer(t)
	return f, server
}

func newZaiQuotaServer(t *testing.T) *zaiQuotaServer {
	t.Helper()
	server := &zaiQuotaServer{}
	server.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.served++
		server.authed = r.Header.Get("Authorization") == "fake-zai-key"
		index := server.served - 1
		if index >= len(server.bodies) {
			index = len(server.bodies) - 1
		}
		body := server.bodies[index]
		status := server.status
		server.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.server.Close)
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.server.URL)
	t.Setenv("FAKE_ZAI_KEY", "fake-zai-key")
	return server
}

// piSuccessStep answers a pi call with a done result and the unit file.
func piSuccessStep() string {
	return "printf a > a.txt\n" +
		"printf '%s\\n' " + shellQuote(`{"type":"session","id":"pi-session"}`) + "\n" +
		emit(map[string]any{"type": "message_end", "message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": `{"status":"done","handoff":["готово"]}`}},
		}}) +
		emit(map[string]any{"type": "agent_settled", "aborted": false})
}

func TestRunZaiQuotaStartLineAndDelta(t *testing.T) {
	f, server := zaiRunFixture(t)
	reset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	server.bodies = []string{
		zaiQuotaBody(28, 30, reset, reset+86400),
		zaiQuotaBody(40, 30, reset, reset+86400),
	}
	f.steps(piSuccessStep())
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "quota: zai 5h 28% · 7d 30%", "quota: zai 5h 40% (+12) · 7d 30% (+0)", "P001.1.1 done:")
	// One start probe plus one re-probe after the pi call, no more.
	if got := server.count(); got != 2 {
		t.Fatalf("zai quota probed %d times, want 2 (start + after call)", got)
	}
	if !server.authorized() {
		t.Fatal("probe sent no key")
	}
}

func TestRunZaiQuotaExhaustedAfterFirstUnitSkipsNextRoute(t *testing.T) {
	f, server := zaiRunFixture(t)
	reset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	server.bodies = []string{
		zaiQuotaBody(28, 30, reset, reset+86400),
		zaiQuotaBody(100, 30, reset, reset+86400),
	}
	f.steps(piSuccessStep(), strings.ReplaceAll(codexStep(f, 2, 20, 30, true), "u2.txt", "b.txt"))
	r := f.cli("run", "--no-wait", "--max-units", "2")
	want := "zai: quota exhausted until " + time.Unix(reset, 0).Local().Format("15:04") + ", skipped"
	f.check(r, 0, want, "P001.1.2 done:", "via codex gpt-5.5")
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[0].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-5.5") {
		t.Fatalf("calls = %v, want pi zai then codex fallback", calls)
	}
	if got := server.count(); got != 2 {
		t.Fatalf("zai quota probed %d times, want 2 (start + after first unit)", got)
	}
}

func TestRunZaiQuotaExhaustedSkipsRoute(t *testing.T) {
	f, server := zaiRunFixture(t)
	reset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	server.bodies = []string{zaiQuotaBody(100, 30, reset, reset+86400)}
	f.steps(codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	want := "zai: quota exhausted until " + time.Unix(reset, 0).Local().Format("15:04") + ", skipped"
	f.check(r, 0, want, "via codex gpt-5.5")
	calls := f.calls()
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), "gpt-5.5") {
		t.Fatalf("calls = %v, want one codex call", calls)
	}
}

func TestRunPiWithoutZaiModelIsNotProbed(t *testing.T) {
	f, server := zaiRunFixture(t)
	f.replace(planFile, "executor: pi | zai/glm-5.3 | max", "executor: pi | glm-5.3 | max")
	f.steps(piSuccessStep())
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	if strings.Contains(r.out, "quota exhausted") || strings.Contains(r.out, "quota: zai") {
		t.Fatalf("non-zai pi route was skipped or shown:\n%s", r.out)
	}
	if got := server.count(); got != 0 {
		t.Fatalf("zai quota probed %d times, want 0", got)
	}
	calls := f.calls()
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), "glm-5.3") {
		t.Fatalf("calls = %v, want one pi call", calls)
	}
}

func TestRunZaiLimitWithoutResetWaitsForQuotaReset(t *testing.T) {
	f, server := zaiRunFixture(t)
	calmed := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	latest := zaiMinuteFloor(time.Now().Add(3 * time.Hour))
	server.bodies = []string{
		zaiQuotaBody(28, 30, calmed, calmed+86400),
		zaiQuotaBody(28, 30, calmed, calmed+86400),
		zaiQuotaBody(100, 100, zaiMinuteFloor(time.Now().Add(90*time.Minute)), latest),
	}
	// A limit refusal whose text names no reset time.
	f.steps(piFailureStep(`429: {"code":"1310","message":"Credit Limit Exhausted"}`), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	want := time.Unix(latest+60, 0).Local().Format("15:04")
	f.check(r, 0, "limit until "+want+"; switching to codex gpt-5.5", "via codex gpt-5.5")
	if got := server.count(); got != 3 {
		t.Fatalf("zai quota probed %d times, want 3 (start, after call, refusal)", got)
	}
}

func TestReplanZaiQuotaProbesAfterCall(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nreplan: pi | zai/glm-5.3 | max")
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFakePi(t, f, fake)
	server := newZaiQuotaServer(t)
	reset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	server.bodies = []string{zaiQuotaBody(28, 30, reset, reset+86400)}
	replan := editPlan("### [!] P001.1.1", "### [~] P001.1.1") + strings.Replace(piSuccessStep(), "printf a > a.txt\n", "", 1)
	f.steps(blockReply(), replan, writeReply("a.txt"))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "quota: zai 5h 28% · 7d 30%", "quota: zai 5h 28% (+0) · 7d 30% (+0)", "P001.1.1 done:")
	if got := server.count(); got != 2 {
		t.Fatalf("zai quota probed %d times, want 2 (start + after replan call)", got)
	}
	calls := f.calls()
	if len(calls) != 3 || !strings.Contains(strings.Join(calls[1].Argv, " "), "zai/glm-5.3") {
		t.Fatalf("calls = %v, want Claude block, pi replan, Claude retry", calls)
	}
}

func TestReplanZaiLimitWithoutResetUsesServerQuotaReset(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -\nreplan: pi | zai/glm-5.3 | max; codex | gpt-5.5 | -")
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFakePi(t, f, fake)
	server := newZaiQuotaServer(t)
	fiveReset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	sevenReset := zaiMinuteFloor(time.Now().Add(3 * time.Hour))
	server.bodies = []string{
		zaiQuotaBody(28, 30, fiveReset, sevenReset),
		zaiQuotaBody(28, 30, fiveReset, sevenReset),
		zaiQuotaBody(100, 100, fiveReset, sevenReset),
	}
	piRefusal := piFailureStep(`429: {"code":"1310","message":"Credit Limit Exhausted"}`)
	codexReplan := editPlan("### [!] P001.1.1", "### [~] P001.1.1") + codexStep(f, 1, 20, 30, false)
	f.steps(blockReply(), piRefusal, codexReplan, writeReply("a.txt"))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	want := "P001.1.1: replan pi zai/glm-5.3: limit until " + time.Unix(sevenReset+60, 0).Local().Format("15:04") + "; switching to codex gpt-5.5"
	f.check(r, 0, want, "P001.1.1 done:")
	if got := server.count(); got != 3 {
		t.Fatalf("zai quota probed %d times, want 3 (start, after call, refusal)", got)
	}
	calls := f.calls()
	if len(calls) != 4 || !strings.Contains(strings.Join(calls[1].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[2].Argv, " "), "gpt-5.5") {
		t.Fatalf("calls = %v, want Claude block, pi refusal, codex replan, Claude retry", calls)
	}
}

func TestRunZaiLimitWithoutResetUsesProfileWait(t *testing.T) {
	f, server := zaiRunFixture(t)
	reset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	server.bodies = []string{
		zaiQuotaBody(28, 30, reset, reset+86400),
		zaiQuotaBody(28, 30, reset, reset+86400),
		zaiQuotaBodyWithoutResets(100, 100),
	}
	f.steps(piFailureStep(`429: {"code":"1310","message":"Credit Limit Exhausted"}`), codexFallbackSuccessStep(f))
	started := time.Now()
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "switching to codex gpt-5.5", "via codex gpt-5.5")
	assertZaiWaitClock(t, r.out, started, 30*time.Minute)
	if got := server.count(); got != 3 {
		t.Fatalf("zai quota probed %d times, want 3 (start, after call, refusal)", got)
	}
}

func TestRunZaiLimitWithFreshQuotaUsesProfileWait(t *testing.T) {
	f, server := zaiRunFixture(t)
	fiveReset := zaiMinuteFloor(time.Now().Add(2 * time.Hour))
	sevenReset := zaiMinuteFloor(time.Now().Add(3 * time.Hour))
	server.bodies = []string{
		zaiQuotaBody(28, 30, fiveReset, sevenReset),
		zaiQuotaBody(28, 30, fiveReset, sevenReset),
		zaiQuotaBody(28, 30, fiveReset, sevenReset),
	}
	f.steps(piFailureStep(`429: {"code":"1310","message":"Credit Limit Exhausted"}`), codexFallbackSuccessStep(f))
	started := time.Now()
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "switching to codex gpt-5.5", "via codex gpt-5.5")
	assertZaiWaitClock(t, r.out, started, 30*time.Minute)
	if got := server.count(); got != 3 {
		t.Fatalf("zai quota probed %d times, want 3 (start, after call, refusal)", got)
	}
}

func assertZaiWaitClock(t *testing.T, output string, started time.Time, wait time.Duration) {
	t.Helper()
	const marker = "limit until "
	index := strings.Index(output, marker)
	if index < 0 || len(output) < index+len(marker)+5 {
		t.Fatalf("missing limit wait time in output:\n%s", output)
	}
	clock := output[index+len(marker) : index+len(marker)+5]
	parsed, err := time.ParseInLocation("15:04", clock, time.Local)
	if err != nil {
		t.Fatalf("invalid limit wait time %q: %v", clock, err)
	}
	expected := started.Add(wait)
	base := time.Date(expected.Year(), expected.Month(), expected.Day(), parsed.Hour(), parsed.Minute(), 0, 0, expected.Location())
	best := base
	for _, candidate := range []time.Time{base.Add(-24 * time.Hour), base.Add(24 * time.Hour)} {
		if absDuration(candidate.Sub(expected)) < absDuration(best.Sub(expected)) {
			best = candidate
		}
	}
	if delta := absDuration(best.Sub(expected)); delta > 2*time.Minute {
		t.Fatalf("limit until %s is %s from start, want about %s:\n%s", clock, best.Sub(started).Round(time.Minute), wait, output)
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
