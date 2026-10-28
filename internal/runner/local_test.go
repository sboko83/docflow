package runner

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
)

func TestLocalRouteQuotaIsolation(t *testing.T) {
	route := agents.Route{Agent: "pi", Model: "strata/swift-1.5-iq3_s", Effort: "high"}
	if quotaName(route) != localQuota {
		t.Fatal("local route shares a remote quota")
	}
	for _, other := range []agents.Route{
		{Agent: "pi", Model: "zai/glm-5.3"},
		{Agent: "pi", Model: "other/swift-1.5-iq3_s"},
		{Agent: "codex", Model: route.Model},
		{Agent: "pi", Model: "strata/"},
	} {
		if isLocalRoute(other) {
			t.Fatalf("remote or incomplete route is local: %v", other)
		}
	}
	quota := agentio.Quota{{Label: "5h", Used: 1, Reset: time.Now().Add(time.Hour).Unix()}}
	unavailable := map[string]time.Time{}
	if skips := markQuotaRoutes([]agents.Route{route}, map[string]agentio.Quota{localQuota: quota, "pi": quota, zaiQuota: quota}, unavailable, time.Now()); len(skips) != 0 || len(unavailable) != 0 {
		t.Fatal("subscription quota disabled the local route")
	}
	totals := &runTotals{}
	totals.mergeQuota(localQuota, quota)
	if len(totals.namedQuotas()) != 0 {
		t.Fatal("local stream emitted subscription quota")
	}
}

func TestLocalTerminalDisplay(t *testing.T) {
	route := agents.Route{Agent: "pi", Model: "strata/swift-1.5-iq3_s", Effort: "high"}
	for _, width := range []int{80, 30} {
		p, out := newTTYProgress(t)
		p.setColumns(width)
		p.unit("P001.1.1", "Local work", route, blockStart)
		if !strings.Contains(out.String(), "\x1b[1;38;2;0;0;0;47m Local \x1b[0m") || strings.Contains(plainTTY(out.String()), " Pi ") {
			t.Fatalf("local badge at width %d: %q", width, out.String())
		}
	}
	var out bytes.Buffer
	p := newProgress(&out, &out, false)
	p.unit("P001.1.1", "Local work", route, blockStart)
	if !strings.Contains(out.String(), "Local: pi | strata/swift-1.5-iq3_s | high") {
		t.Fatalf("plain local route: %q", out.String())
	}
}

func TestRunLocalSkipsQuotaProbes(t *testing.T) {
	f, server := zaiRunFixture(t)
	f.write(planFile, strings.ReplaceAll(f.read(planFile), "zai/glm-5.3", "strata/swift-1.5-iq3_s"))
	// Even a custom Pi quota command must not run for the local provider.
	config, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.setConfig(string(config) + "\n[agents.pi]\nquota = [\"/bin/sh\", \"-c\", \"touch local-quota-probed\"]\n")
	f.commit("use local executor")
	f.steps(piSuccessStep())
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "Local: pi | strata/swift-1.5-iq3_s | max", "P001.1.1 done:")
	_, probeErr := os.Stat(f.root + "/local-quota-probed")
	if server.count() != 0 || !os.IsNotExist(probeErr) {
		t.Fatal("local route triggered a quota probe")
	}
	if strings.Contains(r.out, "quota: pi") || strings.Contains(r.out, "quota: local") || strings.Contains(r.out, "quota: zai") {
		t.Fatalf("local quota leaked into output: %s", r.out)
	}
}
