package agentio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/testshim"
)

// opencodeQuotaFixture is the live answer of `opencode api get
// /api/experimental/usage/limits` on the go subscription, sanitized: the
// home directory and the workspace id are replaced, the numbers are the
// ones the probe saw. The console budgets are empty there, so the fixture
// pins the go allowance only.
func opencodeQuotaFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "opencode-usage-limits.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func opencodeQuotaWant() Quota {
	return Quota{
		{"rolling", 0.03, float64(1793392006000)},
		{"weekly", 0.01, float64(1793577600000)},
		{"monthly", 0.00, float64(1796033332000)},
	}
}

func TestParseOpencodeQuota(t *testing.T) {
	body := opencodeQuotaFixture(t)
	equal(t, ParseOpencodeQuota(body), opencodeQuotaWant())
	// `limited` is authoritative: an exhausted window reports Used 1 even
	// when the percentage lags, so the route is skipped until the reset.
	limited := strings.Replace(body, `"weekly":{"percent":1,"limited":false`,
		`"weekly":{"percent":1,"limited":true`, 1)
	want := opencodeQuotaWant()
	want[1].Used = 1
	equal(t, ParseOpencodeQuota(limited), want)
	// A signed-out workspace answers data: null; unknown shapes yield no
	// windows instead of an error.
	equal(t, ParseOpencodeQuota(`{"location":{"directory":"/srv/docflow-smoke"},"data":null}`), Quota{})
	equal(t, ParseOpencodeQuota(`{"data":{"budgets":[]}}`), Quota{})
	equal(t, ParseOpencodeQuota("not json"), Quota{})
}

func TestProbeQuotaOpencode(t *testing.T) {
	body := opencodeQuotaFixture(t)
	path := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_OPENCODE_LIMITS", path)
	bin := t.TempDir()
	testshim.Script(t, bin, "opencode",
		`[ "$*" = 'api get /api/experimental/usage/limits' ] || exit 9`+"\n"+`cat "$FAKE_OPENCODE_LIMITS"`+"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	agent := map[string]any{
		"stream": "opencode",
		"quota":  []any{"opencode", "api", "get", "/api/experimental/usage/limits"},
	}
	equal(t, ProbeQuota(agent, 5*time.Second), opencodeQuotaWant())
	// A failed probe yields no windows, like every other agent probe.
	testshim.Script(t, bin, "opencode", "exit 1\n")
	equal(t, ProbeQuota(agent, 5*time.Second), Quota{})
	// No quota argv: no probe at all.
	equal(t, ProbeQuota(map[string]any{"stream": "opencode"}, 5*time.Second), Quota{})
}
