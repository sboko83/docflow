package agentio

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// fakeZaiPi puts a fake pi first in PATH; it prints a fixed key without
// touching the real one.
func fakeZaiPi(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake pi requires Unix")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho fake-zai-key\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProbeZaiQuotaHTTP(t *testing.T) {
	fakeZaiPi(t)
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
	if runtime.GOOS == "windows" {
		t.Skip("shell fake pi requires Unix")
	}
	t.Setenv("PATH", t.TempDir())
	equal(t, ProbeZaiQuota(time.Second), Quota{})
}

func TestProbeZaiQuotaHTTP500(t *testing.T) {
	fakeZaiPi(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	equal(t, ProbeZaiQuota(time.Second), Quota{})
}

func TestProbeZaiQuotaHangingServer(t *testing.T) {
	fakeZaiPi(t)
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer server.Close()
	defer close(block)
	t.Setenv("DOCFLOW_ZAI_QUOTA_URL", server.URL)
	start := time.Now()
	equal(t, ProbeZaiQuota(200*time.Millisecond), Quota{})
	if time.Since(start) > 3*time.Second {
		t.Fatal("probe failed to time out")
	}
}
