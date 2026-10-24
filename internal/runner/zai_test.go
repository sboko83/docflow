package runner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
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

func zaiMinuteFloor(at time.Time) int64 { return at.Unix() / 60 * 60 }

// zaiQuotaServer serves the queued bodies in order (the last one repeats)
// and counts requests, so tests assert what the runner probed and when.
type zaiQuotaServer struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies []string
	served int
	authed bool
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
	if runtime.GOOS == "windows" {
		t.Skip("shell fake pi requires Unix")
	}
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
		server.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
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
	server.bodies = []string{zaiQuotaBody(28, 30, reset, reset+86400)}
	f.steps(piSuccessStep())
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "quota: zai 5h 28% · 7d 30%", "quota: zai 5h 28% (+0) · 7d 30% (+0)", "P001.1.1 done:")
	// One start probe plus one re-probe after the pi call, no more.
	if got := server.count(); got != 2 {
		t.Fatalf("zai quota probed %d times, want 2 (start + after call)", got)
	}
	if !server.authorized() {
		t.Fatal("probe sent no key")
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
	if runtime.GOOS == "windows" {
		t.Skip("shell fake pi requires Unix")
	}
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
