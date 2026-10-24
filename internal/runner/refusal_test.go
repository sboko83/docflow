package runner

import (
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
)

func TestRefusalThrottleEscalation(t *testing.T) {
	throttles := 0
	for i := 1; i < 4; i++ {
		kind, count := retryRefusalKind(agentio.RefusalThrottle, throttles)
		if kind != agentio.RefusalThrottle || count != i {
			t.Fatalf("throttle %d: got %q, count %d", i, kind, count)
		}
		throttles = count
	}
	kind, throttles := retryRefusalKind(agentio.RefusalThrottle, throttles)
	if kind != agentio.RefusalOutage || throttles != 0 {
		t.Fatalf("fourth throttle: got %q, count %d", kind, throttles)
	}
	kind, throttles = retryRefusalKind(agentio.RefusalThrottle, throttles)
	if kind != agentio.RefusalThrottle || throttles != 1 {
		t.Fatalf("new throttle sequence: got %q, count %d", kind, throttles)
	}
	kind, throttles = retryRefusalKind(agentio.RefusalLimit, throttles)
	if kind != agentio.RefusalLimit || throttles != 0 {
		t.Fatalf("limit should reset throttle sequence: got %q, count %d", kind, throttles)
	}
}

func TestRefusalSchedule(t *testing.T) {
	now := time.Unix(1_000_000_000, 0)
	reset := float64(now.Unix() + 3600)
	tests := []struct {
		kind    string
		refusal *agentio.Refusal
		message string
		until   time.Time
	}{
		{
			kind:    agentio.RefusalLimit,
			refusal: &agentio.Refusal{Kind: agentio.RefusalLimit, Reset: &reset, Text: "usage limit"},
			message: "usage limit until " + time.Unix(int64(reset+60), 0).Local().Format("15:04"),
			until:   time.Unix(int64(reset+60), 0),
		},
		{
			kind:    agentio.RefusalOutage,
			refusal: &agentio.Refusal{Kind: agentio.RefusalOutage},
			message: "provider unavailable, retry at " + now.Add(10*time.Minute).Local().Format("15:04"),
			until:   now.Add(10 * time.Minute),
		},
		{
			kind:    agentio.RefusalThrottle,
			refusal: &agentio.Refusal{Kind: agentio.RefusalThrottle},
			message: "throttled, retry in 30 s",
			until:   now.Add(30 * time.Second),
		},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			message, until, err := refusalSchedule(map[string]any{}, test.refusal, now)
			if err != nil {
				t.Fatal(err)
			}
			if message != test.message || !until.Equal(test.until) {
				t.Fatalf("got %q at %s; want %q at %s", message, until, test.message, test.until)
			}
		})
	}
}
