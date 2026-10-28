package agentio

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bsv-docflow/internal/testshim"
)

// zaiFixtureBody mirrors the live response shape from the plan context.
func zaiFixtureBody(quoteType string) string {
	return `{"code":200,"msg":"操作成功","success":true,"data":{"level":"pro","limits":[
		{"type":"` + quoteType + `","unit":3,"number":5,"usage":2000,"currentValue":561,
		 "remaining":1438,"percentage":28,"nextResetTime":1792877874249},
		{"type":"` + quoteType + `","unit":6,"number":1,"usage":10000,"currentValue":3072,
		 "remaining":6927,"percentage":30,"nextResetTime":1793393326999}]}}`
}

var zaiFixtureQuota = Quota{
	{"5h", 0.28, float64(1792877874249)},
	{"7d", 0.30, float64(1793393326999)},
}

func TestParseZaiQuota(t *testing.T) {
	equal(t, ParseZaiQuota([]byte(zaiFixtureBody("CREDIT_LIMIT"))), zaiFixtureQuota)
	// Windows are keyed by unit/number: z.ai already renamed the type once.
	equal(t, ParseZaiQuota([]byte(zaiFixtureBody("TOKENS_LIMIT"))), zaiFixtureQuota)
	// Unknown unit skips the window.
	unknown := strings.Replace(zaiFixtureBody("CREDIT_LIMIT"), `"unit":3`, `"unit":9`, 1)
	equal(t, ParseZaiQuota([]byte(unknown)), zaiFixtureQuota[1:])
	equal(t, ParseZaiQuota([]byte(strings.Replace(zaiFixtureBody("CREDIT_LIMIT"), `"success":true`, `"success":false`, 1))), Quota{})
	equal(t, ParseZaiQuota([]byte("not json")), Quota{})
	equal(t, ParseZaiQuota([]byte(`{"success":true,"data":{}}`)), Quota{})
}

func TestIsZaiModel(t *testing.T) {
	equal(t, IsZaiModel("zai/glm-5.3"), true)
	equal(t, IsZaiModel("zai/"), true)
	equal(t, IsZaiModel("glm-5.3"), false)
	equal(t, IsZaiModel("xai/grok-5"), false)
	equal(t, IsZaiModel(""), false)
}

// fakeZaiPi puts a script-backed fake pi first in PATH without touching the
// real key store.
func fakeZaiPi(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	testshim.Script(t, bin, "pi", "#!/bin/sh\n"+script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProbeZaiQuotaHTTP(t *testing.T) {
	fakeZaiPi(t, "printf '  fake-zai-key  \\n'\n")
	var authorized, accepts bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized = r.Header.Get("Authorization") == "fake-zai-key"
		accepts = r.Header.Get("Accept") == "application/json"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zaiFixtureBody("CREDIT_LIMIT")))
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	equal(t, ProbeZaiQuota(time.Second), zaiFixtureQuota)
	if !authorized || !accepts {
		t.Fatal("probe sent wrong Authorization or Accept header")
	}
}

func TestProbeZaiQuotaNoPi(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	equal(t, ProbeZaiQuota(time.Second), Quota{})
}

func TestProbeZaiQuotaHTTP500(t *testing.T) {
	fakeZaiPi(t, "printf 'fake-zai-key\\n'\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	equal(t, ProbeZaiQuota(time.Second), Quota{})
}

func TestProbeZaiQuotaHangingServer(t *testing.T) {
	fakeZaiPi(t, "printf 'fake-zai-key\\n'\n")
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer server.Close()
	defer close(block)
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	start := time.Now()
	equal(t, ProbeZaiQuota(200*time.Millisecond), Quota{})
	if elapsed := time.Since(start); elapsed > 450*time.Millisecond {
		t.Fatalf("probe exceeded timeout allowance: %s", elapsed)
	}
}

func TestProbeZaiQuotaSharedTimeoutBudget(t *testing.T) {
	fakeZaiPi(t, "sleep 0.6\nprintf 'fake-zai-key\\n'\n")
	var requested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Store(true)
		time.Sleep(600 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zaiFixtureBody("CREDIT_LIMIT")))
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	start := time.Now()
	equal(t, ProbeZaiQuota(time.Second), Quota{})
	if elapsed := time.Since(start); elapsed > 1250*time.Millisecond {
		t.Fatalf("probe exceeded shared timeout allowance: %s", elapsed)
	}
	if !requested.Load() {
		t.Fatal("probe did not reach the quota server")
	}
}

func TestProbeZaiQuotaWhitespaceKey(t *testing.T) {
	fakeZaiPi(t, "printf ' \\n\\t\\n'\n")
	var requested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Store(true)
		_, _ = w.Write([]byte(zaiFixtureBody("CREDIT_LIMIT")))
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	equal(t, ProbeZaiQuota(time.Second), Quota{})
	if requested.Load() {
		t.Fatal("probe sent a request with a whitespace-only key")
	}
}

func TestProbeZaiQuotaNonzeroPiExit(t *testing.T) {
	fakeZaiPi(t, "printf 'fake-zai-key\\n'\nexit 1\n")
	var requested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Store(true)
		_, _ = w.Write([]byte(zaiFixtureBody("CREDIT_LIMIT")))
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	equal(t, ProbeZaiQuota(time.Second), Quota{})
	if requested.Load() {
		t.Fatal("probe sent a request after pi returned a nonzero exit code")
	}
}

func TestProbeZaiQuotaPiTimeout(t *testing.T) {
	fakeZaiPi(t, "exec sleep 5\n")
	var requested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Store(true)
		_, _ = w.Write([]byte(zaiFixtureBody("CREDIT_LIMIT")))
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	start := time.Now()
	equal(t, ProbeZaiQuota(150*time.Millisecond), Quota{})
	if elapsed := time.Since(start); elapsed > 650*time.Millisecond {
		t.Fatalf("probe exceeded key timeout allowance: %s", elapsed)
	}
	if requested.Load() {
		t.Fatal("probe sent a request after pi timed out")
	}
}
