package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/testshim"
)

// opencodeLimitsFixture is the same live OpenCode go allowance the agentio
// probe tests use: three windows, no console budgets.
func opencodeLimitsFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "agentio", "testdata", "opencode-usage-limits.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubOpencodeQuota puts a fake opencode first in PATH that answers every
// call with the given allowance, like the local server does.
func stubOpencodeQuota(t *testing.T, body string) map[string]any {
	t.Helper()
	t.Setenv("FAKE_OPENCODE_LIMITS", body)
	bin := t.TempDir()
	testshim.Script(t, bin, "opencode", "printf '%s' \"$FAKE_OPENCODE_LIMITS\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	config, err := agents.LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

var opencodeLimitsWant = agentio.Quota{
	{"rolling", 0.03, float64(1793392006000)},
	{"weekly", 0.01, float64(1793577600000)},
	{"monthly", 0.0, float64(1796033332000)},
}

func TestRefreshQuotaOpencode(t *testing.T) {
	config := stubOpencodeQuota(t, opencodeLimitsFixture(t))
	totals := &runTotals{quota: map[string]agentio.Quota{}, quotaStart: map[string]agentio.Quota{}}
	route := agents.Route{Agent: "opencode", Model: "opencode-go/deepseek-v4.1-flash", Effort: "low"}
	refreshQuota(totals, route, profile(config, "opencode"))
	if got := totals.quota["opencode"]; !reflect.DeepEqual(got, opencodeLimitsWant) {
		t.Fatalf("quota = %#v; want %#v", got, opencodeLimitsWant)
	}
}

func TestQuotaRefusalResetOpencode(t *testing.T) {
	route := agents.Route{Agent: "opencode", Model: "opencode-go/deepseek-v4.1-flash"}
	// No window is exhausted: the profile limit_wait stays in force.
	config := stubOpencodeQuota(t, opencodeLimitsFixture(t))
	if got := quotaRefusalReset(route, profile(config, "opencode")); got != nil {
		t.Fatalf("reset = %v; want nil without an exhausted window", *got)
	}
	// `limited` wins over the percentage: the refusal waits for that window.
	limited := strings.Replace(opencodeLimitsFixture(t), `"weekly":{"percent":1,"limited":false`,
		`"weekly":{"percent":1,"limited":true`, 1)
	config = stubOpencodeQuota(t, limited)
	got := quotaRefusalReset(route, profile(config, "opencode"))
	if got == nil || *got != 1793577600 {
		t.Fatalf("reset = %v; want the weekly window reset 1793577600", got)
	}
	// An agent without a quota command offers no reset and is not probed.
	if got := quotaRefusalReset(agents.Route{Agent: "fake"}, map[string]any{"stream": "claude"}); got != nil {
		t.Fatalf("reset = %v; want nil without a quota command", *got)
	}
}
