package runner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The z.ai acceptance tests drive the built docflow binary on a plan whose
// executor is pi | zai/glm-5.3 | max: a pi stub in PATH prints the key on
// auth print-api-key and otherwise runs the fake agent, and a local server
// answers the quota endpoint in the shape of the live response.

// liveZaiQuotaBody mirrors the live quota/limit response field for field;
// resets are in milliseconds, as z.ai sends them.
func liveZaiQuotaBody(five, seven float64, fiveResetMs, sevenResetMs int64) string {
	return `{"code":200,"msg":"操作成功","success":true,"data":{"level":"pro","limits":[` +
		fmt.Sprintf(`{"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":2000,"currentValue":561,"remaining":1438,"percentage":%g,"nextResetTime":%d},`, five, fiveResetMs) +
		fmt.Sprintf(`{"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":10000,"currentValue":3072,"remaining":6927,"percentage":%g,"nextResetTime":%d}]}}`, seven, sevenResetMs)
}

// zaiResetMs is a reset time off the second boundary, like the live one.
func zaiResetMs(after time.Duration) int64 {
	return time.Now().Add(after).UnixMilli()/1000*1000 + 249
}

func zaiClock(ms int64) string { return time.Unix(ms/1000, 0).Local().Format("15:04") }

func TestAcceptanceZaiQuotaStartAndDelta(t *testing.T) {
	f, server := zaiRunFixture(t)
	five, seven := zaiResetMs(2*time.Hour), zaiResetMs(6*24*time.Hour)
	server.bodies = []string{liveZaiQuotaBody(28, 30, five, seven), liveZaiQuotaBody(31, 30, five, seven)}
	f.steps(piSuccessStep())
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "quota: zai 5h 28% · 7d 30%", "zai 5h 31% (+3) · 7d 30% (+0)", "P001.1.1 done:")
}

func TestAcceptanceZaiExhaustedRouteSkipped(t *testing.T) {
	f, server := zaiRunFixture(t)
	five := zaiResetMs(2 * time.Hour)
	server.bodies = []string{liveZaiQuotaBody(100, 30, five, zaiResetMs(6*24*time.Hour))}
	f.steps(codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "zai: quota exhausted until "+zaiClock(five)+", skipped", "via codex gpt-5.5", "P001.1.1 done:")
	if routes := callRoutes(f); strings.Join(routes, ",") != "codex" {
		t.Fatalf("routes = %v, want codex only", routes)
	}
}

func TestAcceptanceZaiProbeFailureKeepsRunning(t *testing.T) {
	for _, mode := range []string{"http-500", "no-key"} {
		t.Run(mode, func(t *testing.T) {
			f, server := zaiRunFixture(t)
			server.bodies = []string{liveZaiQuotaBody(28, 30, zaiResetMs(2*time.Hour), zaiResetMs(6*24*time.Hour))}
			wantRequests := 2
			if mode == "http-500" {
				server.status = 500
			} else {
				t.Setenv("FAKE_ZAI_KEY", "")
				wantRequests = 0
			}
			f.steps(piSuccessStep())
			r := f.cli("run", "--no-wait", "--max-units", "1")
			f.check(r, 0, "P001.1.1 done:")
			if strings.Contains(r.out+r.err, "quota: zai") || strings.Contains(r.out+r.err, "zai 5h") {
				t.Fatalf("failed probe printed a zai quota:\n%s%s", r.out, r.err)
			}
			if got := server.count(); got != wantRequests {
				t.Fatalf("zai quota requests = %d, want %d", got, wantRequests)
			}
		})
	}
}

func TestAcceptanceNonZaiPiRouteNotProbed(t *testing.T) {
	f, server := zaiRunFixture(t)
	f.replace(planFile, "executor: pi | zai/glm-5.3 | max", "executor: pi | local/x | -")
	server.bodies = []string{liveZaiQuotaBody(100, 100, zaiResetMs(2*time.Hour), zaiResetMs(6*24*time.Hour))}
	f.steps(piSuccessStep())
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	if got := server.count(); got != 0 {
		t.Fatalf("zai quota requests = %d, want 0", got)
	}
	if calls := f.calls(); len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), "local/x") {
		t.Fatalf("calls = %v, want one pi local/x call", calls)
	}
}

func TestAcceptanceZaiLimitWaitsForServerReset(t *testing.T) {
	f, server := zaiRunFixture(t)
	five, seven := zaiResetMs(2*time.Hour), zaiResetMs(6*24*time.Hour)
	exhausted := zaiResetMs(90 * time.Minute)
	server.bodies = []string{
		liveZaiQuotaBody(28, 30, five, seven),
		liveZaiQuotaBody(28, 30, five, seven),
		liveZaiQuotaBody(100, 30, exhausted, seven),
	}
	f.steps(piFailureStep(`429: {"code":"1310","message":"Credit Limit Exhausted"}`), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	// LimitUntil adds a minute of margin to the reset it is given.
	want := time.Unix(exhausted/1000+60, 0).Local().Format("15:04")
	f.check(r, 0, "limit until "+want+"; switching to codex gpt-5.5", "via codex gpt-5.5", "P001.1.1 done:")
	if routes := callRoutes(f); strings.Join(routes, ",") != "pi,codex" {
		t.Fatalf("routes = %v, want pi then codex", routes)
	}
}

func TestAcceptanceZaiKeyNeverLeaks(t *testing.T) {
	f, server := zaiRunFixture(t)
	const key = "zai-secret-key-7f3a"
	t.Setenv("FAKE_ZAI_KEY", key)
	five, seven := zaiResetMs(2*time.Hour), zaiResetMs(6*24*time.Hour)
	server.bodies = []string{
		liveZaiQuotaBody(28, 30, five, seven),
		liveZaiQuotaBody(28, 30, five, seven),
		liveZaiQuotaBody(100, 30, five, seven),
	}
	// A refusal exercises the start probe, the probe after the call and
	// the refusal probe; the codex fallback finishes the unit.
	f.steps(piFailureStep(`429: {"code":"1310","message":"Credit Limit Exhausted"}`), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "quota: zai 5h 28%", "P001.1.1 done:")
	if got := server.count(); got != 3 {
		t.Fatalf("zai quota requests = %d, want 3", got)
	}
	if strings.Contains(r.out+r.err, key) {
		t.Fatalf("key in run output:\n%s%s", r.out, r.err)
	}
	if b, _ := os.ReadFile(f.scenario + ".log"); strings.Contains(string(b), key) {
		t.Fatal("key in agent argv or prompt")
	}
	logs := filepath.Join(f.root, ".git", "docflow", "run")
	files := 0
	err := filepath.WalkDir(logs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(b), key) {
			t.Errorf("key in %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatalf("no call logs under %s", logs)
	}
}
