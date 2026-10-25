package stats

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

func statsProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	logs := t.TempDir()
	t.Setenv("DOCFLOW_CLAUDE_PROJECTS", logs)
	return root, logs
}

func saveStats(t *testing.T, root, unit string, data map[string]any) {
	t.Helper()
	folder := filepath.Join(root, ".git", "docflow", "stats")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, unit+".json"), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTranscript(t *testing.T, logs, fixture, name string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, name+".jsonl"), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rowByID(t *testing.T, rows []Row, id string) Row {
	t.Helper()
	for _, row := range rows {
		if row.Unit == id {
			return row
		}
	}
	t.Fatalf("row %s missing from %#v", id, rows)
	return Row{}
}

func checkTranscriptRow(t *testing.T, row Row, requests, total, peak int64) {
	t.Helper()
	if row.Requests == nil || *row.Requests != int(requests) || row.Sum == nil || row.Sum.Int64() != total || row.Peak == nil || row.Peak.Int64() != peak || row.Source != "transcript" {
		t.Fatalf("transcript row = %#v; want requests=%d sum=%d peak=%d", row, requests, total, peak)
	}
}

func TestWindowPromptToFinish(t *testing.T) {
	root, logs := statsProject(t)
	saveStats(t, root, "P001.1.1", map[string]any{"unit": "P001.1.1", "proxy_tokens": 1000})
	copyTranscript(t, logs, "window.jsonl", "s1")
	rows, err := UnitRows(root, "")
	if err != nil {
		t.Fatal(err)
	}
	row := rowByID(t, rows, "P001.1.1")
	if row.Proxy != json.Number("1000") {
		t.Fatalf("proxy = %#v, want 1000", row.Proxy)
	}
	checkTranscriptRow(t, row, 3, 1110, 555)
}

func TestDuplicateMessageIDCountedOnce(t *testing.T) {
	root, logs := statsProject(t)
	saveStats(t, root, "P001.1.1", map[string]any{"unit": "P001.1.1", "proxy_tokens": 1000})
	copyTranscript(t, logs, "duplicate.jsonl", "s1")
	rows, err := UnitRows(root, "")
	if err != nil {
		t.Fatal(err)
	}
	checkTranscriptRow(t, rowByID(t, rows, "P001.1.1"), 2, 666, 444)
}

func TestPromptWithoutIDUsesResultID(t *testing.T) {
	root, logs := statsProject(t)
	saveStats(t, root, "P001.1.2", map[string]any{"unit": "P001.1.2", "proxy_tokens": 500})
	copyTranscript(t, logs, "prompt_without_id.jsonl", "s1")
	rows, err := UnitRows(root, "")
	if err != nil {
		t.Fatal(err)
	}
	checkTranscriptRow(t, rowByID(t, rows, "P001.1.2"), 2, 777, 666)
}

func TestRunnerSessionByID(t *testing.T) {
	root, logs := statsProject(t)
	saveStats(t, root, "P001.1.1", map[string]any{
		"unit": "P001.1.1", "proxy_tokens": 1000, "sessions": []string{"abc"}, "tokens": 42, "calls": 1,
	})
	copyTranscript(t, logs, "runner_abc.jsonl", "abc")
	copyTranscript(t, logs, "runner_other.jsonl", "other")
	rows, err := UnitRows(root, "")
	if err != nil {
		t.Fatal(err)
	}
	checkTranscriptRow(t, rowByID(t, rows, "P001.1.1"), 2, 444, 333)
}

func TestWithoutTranscript(t *testing.T) {
	root, _ := statsProject(t)
	saveStats(t, root, "P001.1.1", map[string]any{"unit": "P001.1.1", "proxy_tokens": 1000, "tokens": 42})
	saveStats(t, root, "P001.1.2", map[string]any{"unit": "P001.1.2", "proxy_tokens": 700})
	rows, err := UnitRows(root, "")
	if err != nil {
		t.Fatal(err)
	}
	withTokens := rowByID(t, rows, "P001.1.1")
	if withTokens.Requests != nil || withTokens.Sum == nil || withTokens.Sum.Int64() != 42 || withTokens.BaseCtx != nil || withTokens.Peak != nil || withTokens.Source != "tokens" {
		t.Fatalf("token row = %#v", withTokens)
	}
	withoutTokens := rowByID(t, rows, "P001.1.2")
	if withoutTokens.Requests != nil || withoutTokens.Sum != nil || withoutTokens.BaseCtx != nil || withoutTokens.Peak != nil || withoutTokens.Source != "-" {
		t.Fatalf("empty row = %#v", withoutTokens)
	}
	result, err := Command(root, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "unit proxy reqs sum_ctx base_ctx peak_ctx peak/proxy grow/proxy source\n" +
		"P001.1.1 1000 - 42 - - - - tokens\n" +
		"P001.1.2 700 - - - - - - -\n" +
		"median peak/proxy: -\n" +
		"median grow/proxy: -\n"
	if result.Stdout != want {
		t.Fatalf("Command() = %q, want %q", result.Stdout, want)
	}
}

func TestPlanFilterAndCommandOutput(t *testing.T) {
	root, logs := statsProject(t)
	saveStats(t, root, "P001.1.1", map[string]any{"unit": "P001.1.1", "proxy_tokens": 100})
	saveStats(t, root, "P002.1.1", map[string]any{"unit": "P002.1.1", "proxy_tokens": 100})
	copyTranscript(t, logs, "filtered.jsonl", "s1")
	rows, err := UnitRows(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Unit != "P001.1.1" {
		t.Fatalf("filtered rows = %#v", rows)
	}
	result, err := Command(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	want := "unit proxy reqs sum_ctx base_ctx peak_ctx peak/proxy grow/proxy source\n" +
		"P001.1.1 100 2 333 111 222 2.22 1.11 transcript\n" +
		"median peak/proxy: 2.22\n" +
		"median grow/proxy: 1.11\n"
	if result.Code != 0 || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("Command() = %#v, want output %q", result, want)
	}
}

func TestStatsBaseContext(t *testing.T) {
	root, logs := statsProject(t)
	saveStats(t, root, "P001.1.1", map[string]any{"unit": "P001.1.1", "proxy_tokens": 100})
	saveStats(t, root, "P001.1.2", map[string]any{"unit": "P001.1.2", "proxy_tokens": 100})
	copyTranscript(t, logs, "base_context.jsonl", "s1")

	rows, err := UnitRows(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	first := rowByID(t, rows, "P001.1.1")
	checkTranscriptRow(t, first, 3, 450, 200)
	if first.BaseCtx == nil || first.BaseCtx.Int64() != 100 {
		t.Fatalf("first base context = %v, want 100", first.BaseCtx)
	}
	second := rowByID(t, rows, "P001.1.2")
	checkTranscriptRow(t, second, 3, 900, 400)
	if second.BaseCtx == nil || second.BaseCtx.Int64() != 200 {
		t.Fatalf("second base context = %v, want 200", second.BaseCtx)
	}

	result, err := Command(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	want := "unit proxy reqs sum_ctx base_ctx peak_ctx peak/proxy grow/proxy source\n" +
		"P001.1.1 100 3 450 100 200 2.00 1.00 transcript\n" +
		"P001.1.2 100 3 900 200 400 4.00 2.00 transcript\n" +
		"median peak/proxy: 3.00\n" +
		"median grow/proxy: 1.50\n"
	if result.Code != 0 || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("Command() = %#v, want output %q", result, want)
	}
}

func TestStatsBaseContextWithInteractiveAndRunnerSessions(t *testing.T) {
	for _, runnerID := range []string{"a-runner", "z-runner"} {
		t.Run(runnerID, func(t *testing.T) {
			root, logs := statsProject(t)
			saveStats(t, root, "P001.1.1", map[string]any{
				"unit": "P001.1.1", "proxy_tokens": 100, "sessions": []string{runnerID},
			})
			copyTranscript(t, logs, "base_context.jsonl", "interactive")
			copyTranscript(t, logs, "runner_abc.jsonl", runnerID)

			rows, err := UnitRows(root, "P001")
			if err != nil {
				t.Fatal(err)
			}
			row := rowByID(t, rows, "P001.1.1")
			checkTranscriptRow(t, row, 5, 894, 333)
			if row.BaseCtx == nil || row.BaseCtx.Int64() != 100 {
				t.Fatalf("mixed base context = %v, want interactive context 100", row.BaseCtx)
			}

			result, err := Command(root, "P001")
			if err != nil {
				t.Fatal(err)
			}
			want := "unit proxy reqs sum_ctx base_ctx peak_ctx peak/proxy grow/proxy source\n" +
				"P001.1.1 100 5 894 100 333 3.33 2.33 transcript\n" +
				"median peak/proxy: 3.33\n" +
				"median grow/proxy: 2.33\n"
			if result.Code != 0 || result.Stdout != want || result.Stderr != "" {
				t.Fatalf("Command() = %#v, want output %q", result, want)
			}
		})
	}
}

func TestCommandWithoutStats(t *testing.T) {
	root, _ := statsProject(t)
	result, err := Command(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != clierrors.EXIT_NOTHING || result.Stdout != "" || result.Stderr != "no stats: run docflow prompt first\n" {
		t.Fatalf("Command() = %#v", result)
	}
}

func TestMedian(t *testing.T) {
	if got := median([]float64{1, 5, 3}); got != 3 {
		t.Fatalf("median(odd) = %v, want 3", got)
	}
	if got := median([]float64{1, 4}); got != 2.5 {
		t.Fatalf("median(even) = %v, want 2.5", got)
	}
}

func TestClaudeProjectsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOCFLOW_CLAUDE_PROJECTS", "~/transcripts")
	if got, want := ClaudeProjectsDir("/tmp/project"), filepath.Join(home, "transcripts"); got != want {
		t.Fatalf("ClaudeProjectsDir() = %q, want %q", got, want)
	}
	if strings.Contains(ClaudeProjectsDir("/tmp/project"), "~") {
		t.Fatal("configured home prefix was not expanded")
	}
}
