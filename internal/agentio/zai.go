package agentio

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ZaiQuotaURL is the undocumented subscription-quota endpoint that the
// official z.ai plugin glm-plan-usage uses; it answers without invoking a
// model, so the probe never spends quota.
const ZaiQuotaURL = "https://api.z.ai/api/monitor/usage/quota/limit"

// zaiQuotaURLEnv overrides the endpoint in tests only.
const zaiQuotaURLEnv = "DOCFLOW_ZAI_QUOTA_URL"

// IsZaiModel reports whether a pi model routes through the z.ai provider:
// the subscription quota belongs to z.ai, not to the pi agent.
func IsZaiModel(model string) bool {
	return strings.HasPrefix(model, "zai/")
}

// ParseZaiQuota decodes the quota/limit response. Windows are keyed by
// unit/number, not by type (z.ai already renamed TOKENS_LIMIT to
// CREDIT_LIMIT): unit 3 counts hours, unit 6 counts weeks of 7 days.
// Unknown units are skipped. Used is a fraction (percentage/100) and Reset
// keeps nextResetTime in milliseconds, which Epoch understands.
func ParseZaiQuota(body []byte) Quota {
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return Quota{}
	}
	if parsed["success"] != true {
		return Quota{}
	}
	limits, _ := table(parsed["data"])["limits"].([]any)
	quota := Quota{}
	for _, item := range limits {
		limit := table(item)
		unit, ok := number(limit["unit"])
		if !ok {
			continue
		}
		count, ok := number(limit["number"])
		if !ok {
			continue
		}
		label := zaiWindowLabel(unit, count)
		if label == "" {
			continue
		}
		percent, ok := number(limit["percentage"])
		if !ok {
			continue
		}
		quota.Set(label, percent/100, rawEpoch(limit["nextResetTime"]))
	}
	return quota
}

// zaiWindowLabel maps a unit/number pair to the labels shared with the
// Claude and Codex quotas: deltas and reset comparisons go by label.
func zaiWindowLabel(unit, count float64) string {
	switch unit {
	case 3:
		return fmt.Sprintf("%gh", count)
	case 6:
		return fmt.Sprintf("%gd", 7*count)
	}
	return ""
}

func rawEpoch(value any) any {
	if n, ok := number(value); ok {
		return n
	}
	return nil
}

// ProbeZaiQuota asks the z.ai subscription endpoint for the remaining
// quota. The key comes only from pi; any failure — no pi, nonzero exit,
// HTTP other than 200, timeout, bad JSON, success other than true —
// yields an empty quota, like the Claude and Codex probes.
func ProbeZaiQuota(timeout time.Duration) Quota {
	if timeout <= 0 {
		return Quota{}
	}
	deadline := time.Now().Add(timeout)
	key := zaiAPIKey(timeout)
	if key == "" {
		return Quota{}
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return Quota{}
	}
	body, err := zaiQuotaGet(zaiQuotaEndpoint(), key, remaining)
	if err != nil {
		return Quota{}
	}
	return ParseZaiQuota(body)
}

// zaiQuotaEndpoint resolves the URL; the environment override is for tests.
func zaiQuotaEndpoint() string {
	if override := os.Getenv(zaiQuotaURLEnv); override != "" {
		return override
	}
	return ZaiQuotaURL
}

// zaiAPIKey lets pi resolve the z.ai key from its environment and its own
// auth store. The key stays in memory: it never reaches errors, logs or
// files (CallAgent writes nothing without a log path).
func zaiAPIKey(timeout time.Duration) string {
	run, err := CallAgent("", []string{"pi", "auth", "print-api-key", "--provider", "zai"}, nil, "", CallOptions{Timeout: timeout})
	if err != nil || run.TimedOut || run.Code != 0 {
		return ""
	}
	return strings.TrimSpace(run.Stdout)
}

func zaiQuotaGet(url, key string, timeout time.Duration) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// The endpoint takes the bare key, without the Bearer prefix.
	request.Header.Set("Authorization", key)
	request.Header.Set("Accept", "application/json")
	client := &http.Client{}
	if timeout > 0 {
		client.Timeout = timeout
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zai quota: HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 1<<20))
}
