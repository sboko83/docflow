package runner

import (
	"strings"
	"testing"

	"bsv-docflow/internal/agentio"
)

func piReasoning(blocks ...string) string {
	var out strings.Builder
	for _, block := range blocks {
		out.WriteString(`{"type":"message_end","message":{"role":"assistant","content":`)
		out.WriteString(strings.ReplaceAll(`[{"type":"thinking","thinking":"\u0000"}]`, `\u0000`, block))
		out.WriteString("}}\n")
	}
	return out.String()
}

func TestExcerptFlattensAndClips(t *testing.T) {
	equal := func(got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("excerpt = %q; want %q", got, want)
		}
	}
	equal(excerpt("first\n\n  second\tthird ", 100), "first second third")
	equal(excerpt("абвгд", 3), "абв…")
	equal(excerpt("  ", 5), "")
}

func TestReasoningExcerptKeepsTheTail(t *testing.T) {
	agent := map[string]any{"stream": "pi"}
	stdout := piReasoning("one", "two", "three", "four")
	refusal := &agentio.Refusal{Kind: agentio.RefusalOutage, Text: "connection error"}

	got := reasoningExcerpt(agent, stdout, refusal)
	want := []string{"two", "three", "four"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("excerpt = %v; want %v", got, want)
	}
	if excerpted := reasoningExcerpt(agent, stdout, refusal); len(excerpted) != reasoningBlocks {
		t.Fatalf("kept %d blocks; want %d", len(excerpted), reasoningBlocks)
	}
}

func TestReasoningExcerptDropsTheRefusalBlock(t *testing.T) {
	agent := map[string]any{"stream": "pi"}
	stdout := piReasoning("plan the edit", "usage limit reached")
	refusal := &agentio.Refusal{Kind: agentio.RefusalLimit, Text: "usage limit reached"}

	got := reasoningExcerpt(agent, stdout, refusal)
	if len(got) != 1 || got[0] != "plan the edit" {
		t.Fatalf("excerpt = %v; want the block without the refusal", got)
	}
}

func TestReasoningExcerptWithoutReasoning(t *testing.T) {
	for _, stdout := range []string{"", "--- stdout\n", `{"type":"message_end","message":{"role":"assistant","content":[]}}` + "\n"} {
		if got := reasoningExcerpt(map[string]any{"stream": "pi"}, stdout, &agentio.Refusal{Kind: agentio.RefusalOutage}); len(got) != 0 {
			t.Fatalf("excerpt = %v for %q; want none", got, stdout)
		}
	}
}

func TestTranscriptPath(t *testing.T) {
	tests := []struct {
		root  string
		log   string
		want  string
		empty bool
	}{
		{root: "/repo", log: "/repo/.git/docflow/run/P001.1.1.1.out", want: ".git/docflow/run/P001.1.1.1.out"},
		{root: "/repo", log: "/elsewhere/run.out", want: "/elsewhere/run.out"},
		{root: "/repo", log: "", empty: true},
	}
	for _, test := range tests {
		if got := transcriptPath(test.root, test.log); got != test.want || (test.empty && got != "") {
			t.Fatalf("transcriptPath(%q, %q) = %q; want %q", test.root, test.log, got, test.want)
		}
	}
}
