package agentio

import (
	"testing"
	"time"
)

func claudeFailure(text string) string {
	return lines(map[string]any{"type": "result", "is_error": true, "result": text})
}

func codexFailure(text string) string {
	return lines(
		map[string]any{"type": "thread.started", "thread_id": "t-refusal"},
		map[string]any{"type": "error", "message": text},
	)
}

func piFailure(text string) string {
	return lines(map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": text,
	}})
}

func opencodeFailure(kind, text string) string {
	return lines(map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": text}})
}

func TestClassify(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	codexReset := float64(1791980000)
	claudeReset := float64(time.Date(2026, 10, 19, 17, 50, 0, 0, time.UTC).Unix())
	zaiReset := float64(1792937338)
	codexQuota := func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		rollout(t, home, "t-refusal", map[string]any{
			"primary":   window(100, 300, 1791970000),
			"secondary": window(100, 10080, codexReset),
		})
	}
	codexSignalWithoutFullWindow := func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		rollout(t, home, "t-refusal", map[string]any{
			"primary":                 window(55, 300, 1791970000),
			"rate_limit_reached_type": "primary",
		})
	}
	tests := []struct {
		name    string
		profile string
		run     AgentRun
		setup   func(*testing.T)
		kind    string
		reset   *float64
	}{
		{name: "Claude rejected rate_limit_event", profile: "claude", run: AgentRun{Stdout: lines(
			map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": 1791970000}},
			map[string]any{"type": "result", "is_error": true, "result": "request rejected"},
		)}, kind: RefusalLimit, reset: floatPtr(1791970000)},
		{name: "Claude rejected event without reset", profile: "claude", run: AgentRun{Stdout: lines(
			map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected"}},
			map[string]any{"type": "result", "is_error": true, "result": "request rejected"},
		)}, kind: RefusalLimit},
		{name: "Claude rate_limit_error with UsageReset", profile: "claude", run: AgentRun{Stdout: claudeFailure("API rate_limit_error; resets Oct 19 at 5:50pm (UTC)")}, kind: RefusalLimit, reset: &claudeReset},
		{name: "Claude 429", profile: "claude", run: AgentRun{Stdout: claudeFailure("HTTP 429")}, kind: RefusalLimit},
		{name: "Claude overloaded_error Synthetic", profile: "claude", run: AgentRun{Stdout: claudeFailure("overloaded_error")}, kind: RefusalOutage},
		{name: "Claude 529 Synthetic", profile: "claude", run: AgentRun{Stdout: claudeFailure("HTTP 529")}, kind: RefusalOutage},
		{name: "Claude 5xx Synthetic", profile: "claude", run: AgentRun{Stdout: claudeFailure("HTTP 503")}, kind: RefusalOutage},
		{name: "Claude api_error Synthetic", profile: "claude", run: AgentRun{Stdout: claudeFailure("api_error")}, kind: RefusalOutage},
		{name: "Codex full rollout window", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("request refused")}, setup: codexQuota, kind: RefusalLimit, reset: &codexReset},
		{name: "Codex rate_limit_reached_type rollout", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("request refused")}, setup: codexSignalWithoutFullWindow, kind: RefusalLimit, reset: floatPtr(1791970000)},
		{name: "Codex usage limit", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("usage limit reached")}, kind: RefusalLimit},
		{name: "Codex usage_limit_reached", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("usage_limit_reached")}, kind: RefusalLimit},
		{name: "Codex 429", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("HTTP 429")}, kind: RefusalLimit},
		{name: "Codex stream disconnected Synthetic", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("stream disconnected before completion")}, kind: RefusalOutage},
		{name: "Codex 5xx Synthetic", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("HTTP 503")}, kind: RefusalOutage},
		{name: "Codex overloaded Synthetic", profile: "codex", run: AgentRun{Code: 1, Stdout: codexFailure("server overloaded")}, kind: RefusalOutage},
		{name: "pi captured z.ai 1310 response", profile: "pi", run: AgentRun{Stdout: fixture(t, "pi-limit-zai")}, kind: RefusalLimit, reset: &zaiReset},
		{name: "pi z.ai 1308 reset", profile: "pi", run: AgentRun{Stdout: piFailure(`429: {"code":"1308","message":"Limit will reset at 2026-10-25 22:08:58"}`)}, kind: RefusalLimit, reset: &zaiReset},
		{name: "pi z.ai 1302 throttle", profile: "pi", run: AgentRun{Stdout: piFailure(`429: {"code":"1302","message":"Rate limit reached for requests"}`)}, kind: RefusalThrottle},
		{name: "pi nested z.ai 1302 throttle", profile: "pi", run: AgentRun{Stdout: piFailure(`429: {"error":{"code":"1302","message":"Rate limit reached for requests"}}`)}, kind: RefusalThrottle},
		{name: "pi z.ai 1305 outage Synthetic", profile: "pi", run: AgentRun{Stdout: piFailure(`429: {"code":"1305","message":"The service may be temporarily overloaded, please try again later"}`)}, kind: RefusalOutage},
		{name: "pi other 429", profile: "pi", run: AgentRun{Stdout: piFailure(`429: {"code":"1200","message":"request rejected"}`)}, kind: RefusalLimit},
		{name: "pi profile limit exhausted", profile: "pi", run: AgentRun{Stdout: piFailure(`400: {"code":"1999","message":"Subscription limit exhausted"}`)}, kind: RefusalLimit},
		{name: "pi 5xx Synthetic", profile: "pi", run: AgentRun{Stdout: piFailure("503: upstream error")}, kind: RefusalOutage},
		{name: "pi fetch failed Synthetic", profile: "pi", run: AgentRun{Stdout: piFailure("fetch failed")}, kind: RefusalOutage},
		{name: "pi ECONNREFUSED Synthetic", profile: "pi", run: AgentRun{Stdout: piFailure("connect ECONNREFUSED 127.0.0.1")}, kind: RefusalOutage},
		{name: "pi connection error Synthetic", profile: "pi", run: AgentRun{Stdout: piFailure("connection error")}, kind: RefusalOutage},
		{name: "pi terminated Synthetic", profile: "pi", run: AgentRun{Stdout: piFailure("process terminated")}, kind: RefusalOutage},
		{name: "OpenCode captured no-route", profile: "opencode", run: AgentRun{Stdout: fixture(t, "opencode-error")}, kind: RefusalOutage},
		{name: "OpenCode no-route Synthetic", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider.no-route", "Model unavailable: opencode-go/no-such-model")}, kind: RefusalOutage},
		{name: "OpenCode 429", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "HTTP 429 Too Many Requests")}, kind: RefusalLimit},
		{name: "OpenCode rate limit", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "rate limit exceeded")}, kind: RefusalLimit},
		{name: "OpenCode usage limit", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "usage limit reached")}, kind: RefusalLimit},
		{name: "OpenCode quota", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "quota exceeded")}, kind: RefusalLimit},
		{name: "OpenCode 5xx Synthetic", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "HTTP 503")}, kind: RefusalOutage},
		{name: "OpenCode overloaded Synthetic", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "server overloaded")}, kind: RefusalOutage},
		{name: "OpenCode timeout Synthetic", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "request timeout")}, kind: RefusalOutage},
		{name: "OpenCode connection Synthetic", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "connection reset by peer")}, kind: RefusalOutage},
		{name: "OpenCode quota in assistant text, outage error", profile: "opencode", run: AgentRun{Stdout: lines(
			map[string]any{"type": "text", "part": map[string]any{"messageID": "msg_1", "text": "Updated quota documentation."}},
			map[string]any{"type": "error", "error": map[string]any{"type": "provider_error", "message": "connection reset by peer"}},
		)}, kind: RefusalOutage},
		{name: "OpenCode ECONNREFUSED Synthetic", profile: "opencode", run: AgentRun{Stdout: opencodeFailure("provider_error", "connect ECONNREFUSED 127.0.0.1")}, kind: RefusalOutage},
		{name: "legacy profile regex", profile: "", run: AgentRun{Stdout: `{"is_error":true,"text":"subscription exhausted"}`}, kind: RefusalLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.setup != nil {
				test.setup(t)
			}
			var agent map[string]any
			if test.profile == "" {
				agent = map[string]any{"error": "json:is_error", "text": "json:text", "limit": "subscription exhausted"}
			} else {
				agent = profile(t, test.profile)
			}
			got := Classify(agent, test.run, now)
			if got == nil {
				t.Fatal("Classify returned nil")
			}
			equal(t, got.Kind, test.kind)
			equal(t, got.Reset, test.reset)
			if got.Text == "" {
				t.Fatal("Classify returned empty refusal text")
			}
		})
	}

	t.Run("ordinary response quotes rate limit", func(t *testing.T) {
		agent := profile(t, "claude")
		run := AgentRun{Stdout: `{"is_error":false,"result":"The file mentions a rate limit."}`}
		equal(t, Classify(agent, run, now), (*Refusal)(nil))
	})
	t.Run("ordinary failure", func(t *testing.T) {
		agent := profile(t, "claude")
		run := AgentRun{Code: 1, Stderr: "compile failed"}
		equal(t, Classify(agent, run, now), (*Refusal)(nil))
	})
	t.Run("timeout", func(t *testing.T) {
		agent := profile(t, "pi")
		run := AgentRun{TimedOut: true, Code: 124, Stderr: "429: rate limit"}
		equal(t, Classify(agent, run, now), (*Refusal)(nil))
	})
	t.Run("stalled", func(t *testing.T) {
		// The runner stopped a silent call: the killed process reads as
		// "terminated" and its output quotes a limit, while the route is healthy.
		agent := profile(t, "pi")
		run := AgentRun{Stalled: true, Code: 137, Stdout: piFailure("429: usage limit reached"), Stderr: "terminated"}
		equal(t, Classify(agent, run, now), (*Refusal)(nil))
	})
}
