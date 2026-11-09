package agentio

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/stats"
)

// Vendor snapshots: real CLI output, cut and anonymized, one directory per
// vendor, file names <cli version>_<capture date>_<kind>. The catalog and the
// rules for adding one live in testdata/vendors/README.md.
const (
	snapClaudeWindow        = "vendors/claude/2.1.309_2026-11-02_transcript-window.jsonl"
	snapClaudeSession       = "vendors/claude/2.1.317_2026-11-07_transcript-session.jsonl"
	snapCodexExec           = "vendors/codex/0.165.0_2026-11-05_exec.jsonl"
	snapCodexRollout        = "vendors/codex/0.165.0_2026-11-05_rollout.jsonl"
	snapPiZaiResult         = "vendors/pi/1.4.0_2026-10-28_zai-result.jsonl"
	snapPiStrataResult      = "vendors/pi/1.4.0_2026-10-28_strata-result.jsonl"
	snapPiZaiLimit          = "vendors/pi/1.4.0_2026-10-23_zai-limit-1310.jsonl"
	snapOpencodeResult      = "vendors/opencode/2.2.3_2026-10-30_result.jsonl"
	snapOpencodeNoRoute     = "vendors/opencode/2.2.3_2026-10-30_no-route.jsonl"
	snapOpencodeUsageLimits = "vendors/opencode/2.2.3_2026-10-30_usage-limits.json"
	snapDshResult           = "vendors/dsh/0.2.2-rc.1_2026-11-08_result.jsonl"
	snapDshAuth             = "vendors/dsh/0.2.2-rc.1_2026-11-08_auth.jsonl"
	snapDshLaunchError      = "vendors/dsh/0.2.2-rc.1_2026-11-08_launch-error.jsonl"
	snapDshTruncated        = "vendors/dsh/0.2.2-rc.1_2026-11-08_truncated.jsonl"
)

// codexSnapshotThread is the anonymized thread id the Codex exec snapshot
// and its rollout share.
const codexSnapshotThread = "00000000-0000-4000-8000-000000000001"

// contractOutcome is what docflow reads from one snapshot: the decoded call
// (session, failure, refusal class and reset, login failure, tokens, cost,
// result status), the quota windows and the stats transcript row. Fields a
// case does not touch stay zero.
type contractOutcome struct {
	Session  string
	Failed   bool
	Reason   string
	Refusal  string
	Reset    *float64
	Auth     bool
	Tokens   *int
	Output   *int
	Cost     *float64
	Status   string
	Quota    Quota
	Requests int
	Sum      int64
	Base     int64
	Peak     int64
}

type contractCase struct {
	name  string
	file  string
	parse func(t *testing.T, snapshot string) contractOutcome
	want  contractOutcome
	// drift names JSON keys the parse depends on: renaming any of them in
	// the snapshot must change the outcome, so a vendor renaming the field
	// fails the contract instead of reading as zero.
	drift []string
}

// TestVendorContract parses every vendor snapshot the way the runner and
// docflow stats do and pins the outcome. A failure after a vendor CLI update
// means the output format moved: capture a new snapshot (README in
// testdata/vendors) and fix the decoder, keeping the old snapshot green.
func TestVendorContract(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, c := range []contractCase{
		{
			name:  "Claude transcript, interactive prompt-to-commit window",
			file:  snapClaudeWindow,
			parse: transcriptWindow("P052.4.3"),
			want:  contractOutcome{Requests: 17, Sum: 5945845, Base: 337932, Peak: 359653},
			drift: []string{"usage", "cache_read_input_tokens", "command", "id"},
		},
		{
			name:  "Claude transcript, runner session",
			file:  snapClaudeSession,
			parse: transcriptSession,
			want:  contractOutcome{Requests: 15, Sum: 1414146, Base: 69039, Peak: 110940},
			drift: []string{"message", "input_tokens", "cache_creation_input_tokens"},
		},
		{
			name:  "Codex exec success with its rollout quota",
			file:  snapCodexExec,
			parse: codexCall(readSnapshot(snapCodexRollout), now),
			want: contractOutcome{
				Session: codexSnapshotThread, Reason: "redacted",
				Tokens: intPtr(2207996), Output: intPtr(37113),
				Quota: Quota{{"5h", 0.5, float64(1793906813)}, {"7d", 0.94, float64(1793905398)}},
			},
			drift: []string{"thread_id", "input_tokens", "output_tokens", "text"},
		},
		{
			name: "Codex rollout quota windows below the limit",
			file: snapCodexRollout,
			parse: func(t *testing.T, rollout string) contractOutcome {
				return codexCall(func(*testing.T) string { return rollout }, now)(t, readSnapshot(snapCodexExec)(t))
			},
			want: contractOutcome{
				Session: codexSnapshotThread, Reason: "redacted",
				Tokens: intPtr(2207996), Output: intPtr(37113),
				Quota: Quota{{"5h", 0.5, float64(1793906813)}, {"7d", 0.94, float64(1793905398)}},
			},
			drift: []string{"payload", "rate_limits", "used_percent", "window_minutes", "resets_at"},
		},
		{
			name:  "pi z.ai result",
			file:  snapPiZaiResult,
			parse: agentCall("pi", 0, now),
			want: contractOutcome{
				Session: "00000000-0000-4000-8000-000000000001", Reason: "redacted",
				Tokens: intPtr(62023), Output: intPtr(3093), Cost: floatPtr(0.004513949999999999), Status: "done",
			},
			drift: []string{"id", "usage", "cacheRead", "output", "total"},
		},
		{
			name:  "pi strata result without a result block",
			file:  snapPiStrataResult,
			parse: agentCall("pi", 0, now),
			want: contractOutcome{
				Session: "00000000-0000-4000-8000-000000000001", Reason: "redacted",
				Tokens: intPtr(63995), Output: intPtr(1901), Cost: floatPtr(0),
			},
			drift: []string{"input", "output", "total"},
		},
		{
			name:  "pi z.ai 1310 weekly limit",
			file:  snapPiZaiLimit,
			parse: agentCall("pi", 0, now),
			want: contractOutcome{
				Failed: true, Refusal: RefusalLimit, Reset: floatPtr(1792937338),
				Reason: `429: {"code":"1310","message":"Weekly/Monthly Limit Exhausted. Your limit will reset at 2026-10-25 22:08:58"}`,
			},
			drift: []string{"errorMessage", "stopReason"},
		},
		{
			name:  "OpenCode result",
			file:  snapOpencodeResult,
			parse: agentCall("opencode", 0, now),
			want: contractOutcome{
				Session: "ses_e7328beaaffe1gw0MKFBFL5YLK", Reason: "The command printed `docflow-fixture-ok`.",
				Tokens: intPtr(17742), Output: intPtr(110), Cost: floatPtr(0), Status: "done",
			},
			// cost stays out: the go subscription reports 0, and a missing
			// cost also sums to 0.
			drift: []string{"sessionID", "tokens", "input", "part"},
		},
		{
			name:  "OpenCode provider no-route",
			file:  snapOpencodeNoRoute,
			parse: agentCall("opencode", 1, now),
			want: contractOutcome{
				Session: "ses_e7327a596ffeA3CVqgw0opFqkQ", Failed: true, Refusal: RefusalOutage,
				Reason: "Model unavailable: opencode-go/no-such-model",
			},
			drift: []string{"error", "message"},
		},
		{
			name: "OpenCode go allowance",
			file: snapOpencodeUsageLimits,
			parse: func(t *testing.T, snapshot string) contractOutcome {
				return contractOutcome{Quota: ParseOpencodeQuota(snapshot)}
			},
			want: contractOutcome{Quota: Quota{
				{"rolling", 0.03, float64(1793392006000)},
				{"weekly", 0.01, float64(1793577600000)},
				{"monthly", 0, float64(1796033332000)},
			}},
			drift: []string{"go", "percent", "resetsAt"},
		},
		{
			name:  "dsh result",
			file:  snapDshResult,
			parse: agentCall("dsh", 0, now),
			want: contractOutcome{
				Session: "session-2410985d-4bc0-40a2-ba1f-2e0eea71471f",
				Reason:  "`git status --short` produced no output, so the working tree is clean.",
				Tokens:  intPtr(14628), Output: intPtr(170), Status: "done",
			},
			drift: []string{"sessionId", "usage", "inputTokens", "reason"},
		},
		{
			name:  "dsh truncated tool output",
			file:  snapDshTruncated,
			parse: agentCall("dsh", 0, now),
			want: contractOutcome{
				Session: "session-382ba4c0-c728-47d9-825f-bfc38289aa08", Reason: "ok",
				Tokens: intPtr(28548), Output: intPtr(201),
			},
			drift: []string{"cacheReadTokens", "outputTokens"},
		},
		{
			name:  "dsh authentication failure",
			file:  snapDshAuth,
			parse: agentCall("dsh", 1, now),
			want: contractOutcome{
				Session: "session-44f9ce5d-be61-4816-9c6c-def2a9a96021", Failed: true,
				Reason: "Authentication Fails, Your api key: ****alue is invalid (request_id: 00000000-0000-0000-0000-000000000000)",
				Auth:   true,
			},
			drift: []string{"reason", "message"},
		},
		{
			name:  "dsh launch error",
			file:  snapDshLaunchError,
			parse: agentCall("dsh", 1, now),
			want:  contractOutcome{Failed: true, Reason: "unknown option '--bogus-flag'"},
			drift: []string{"message"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			snapshot := readSnapshot(c.file)(t)
			got := c.parse(t, snapshot)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("%s:\n got  %s\n want %s", c.file, describe(got), describe(c.want))
			}
			for _, key := range c.drift {
				old := `"` + key + `":`
				if !strings.Contains(snapshot, old) {
					t.Fatalf("drift key %q is not in %s", key, c.file)
				}
				moved := c.parse(t, strings.ReplaceAll(snapshot, old, `"`+key+`_renamed":`))
				if reflect.DeepEqual(moved, c.want) {
					t.Errorf("renaming %q in %s leaves the outcome unchanged: the contract would miss that drift", key, c.file)
				}
			}
		})
	}
}

func readSnapshot(name string) func(*testing.T) string {
	return func(t *testing.T) string {
		t.Helper()
		return fixture(t, name)
	}
}

func describe(o contractOutcome) string {
	text := fmt.Sprintf("%+v", o)
	for _, p := range []struct {
		name string
		v    any
	}{{"Reset", o.Reset}, {"Tokens", o.Tokens}, {"Output", o.Output}, {"Cost", o.Cost}} {
		if b, err := json.Marshal(p.v); err == nil {
			text += fmt.Sprintf(" %s=%s", p.name, b)
		}
	}
	return text
}

// agentCall decodes stdout the way the runner does after a call that exited
// with code.
func agentCall(name string, code int, now time.Time) func(*testing.T, string) contractOutcome {
	return func(t *testing.T, stdout string) contractOutcome {
		agent := profile(t, name)
		run := AgentRun{Stdout: stdout, Code: code}
		out := contractOutcome{
			Session: str(JSONPath(AgentOutput(agent, stdout), str(agent["session"]))),
			Failed:  FailureText(agent, run) != nil,
			Reason:  FailureReason(agent, run),
			Auth:    AuthReason(agent, run) != "",
			Tokens:  RunTokens(agent, run),
			Output:  RunOutputTokens(agent, run),
			Cost:    RunCost(agent, run),
			Status:  str(ExtractResult(agent, stdout)["status"]),
		}
		if refusal := Classify(agent, run, now); refusal != nil {
			out.Refusal, out.Reset = refusal.Kind, refusal.Reset
		}
		return out
	}
}

// codexCall places the rollout where Codex writes it and decodes the exec
// stdout together with the quota windows of its thread.
func codexCall(rollout func(*testing.T) string, now time.Time) func(*testing.T, string) contractOutcome {
	return func(t *testing.T, stdout string) contractOutcome {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		folder := filepath.Join(home, "sessions", "2026", "11", "05")
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(folder, "rollout-2026-11-05T19-20-31-"+codexSnapshotThread+".jsonl")
		if err := os.WriteFile(path, []byte(rollout(t)), 0o644); err != nil {
			t.Fatal(err)
		}
		out := agentCall("codex", 0, now)(t, stdout)
		out.Quota = CodexQuota(out.Session)
		return out
	}
}

// transcriptProject is a Git root with docflow stats and a Claude projects
// folder holding one transcript, the way docflow stats reads them.
func transcriptProject(t *testing.T, unit string, record map[string]any, transcript, name string) stats.Row {
	t.Helper()
	root := t.TempDir()
	if output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	folder := filepath.Join(root, ".git", "docflow", "stats")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, unit+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	logs := t.TempDir()
	t.Setenv("DOCFLOW_CLAUDE_PROJECTS", logs)
	if err := os.WriteFile(filepath.Join(logs, name+".jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := stats.UnitRows(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Unit == unit {
			return row
		}
	}
	t.Fatalf("no stats row for %s in %#v", unit, rows)
	return stats.Row{}
}

func transcriptOutcome(row stats.Row) contractOutcome {
	out := contractOutcome{}
	if row.Requests != nil {
		out.Requests = *row.Requests
	}
	if row.Sum != nil && row.Source == "transcript" {
		out.Sum, out.Base, out.Peak = row.Sum.Int64(), row.BaseCtx.Int64(), row.Peak.Int64()
	}
	return out
}

// transcriptWindow reads an interactive session: the unit's window runs
// from its `docflow prompt` to its `docflow commit`.
func transcriptWindow(unit string) func(*testing.T, string) contractOutcome {
	return func(t *testing.T, transcript string) contractOutcome {
		record := map[string]any{"unit": unit, "proxy_tokens": 1}
		return transcriptOutcome(transcriptProject(t, unit, record, transcript, "interactive"))
	}
}

// transcriptSession reads a transcript the runner recorded as the unit's
// session: every request counts.
func transcriptSession(t *testing.T, transcript string) contractOutcome {
	record := map[string]any{"unit": "P001.1.1", "proxy_tokens": 1, "sessions": []string{"runner-session"}}
	return transcriptOutcome(transcriptProject(t, "P001.1.1", record, transcript, "runner-session"))
}
