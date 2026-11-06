package agentio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func parseFixture(t *testing.T, name string, parser *ActivityParser) []Activity {
	t.Helper()
	var out []Activity
	offset := int64(0)
	text := fixture(t, name)
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		ref := ActivityRef{Stream: "stdout", Offset: offset, Length: len(line)}
		out = append(out, parser.Feed("stdout", line, ref)...)
		offset += int64(len(line) + 1)
	}
	return out
}

func activitiesOf(activities []Activity, kind ActivityKind) []Activity {
	var out []Activity
	for _, activity := range activities {
		if activity.Kind == kind {
			out = append(out, activity)
		}
	}
	return out
}

func oneTool(t *testing.T, activities []Activity) Activity {
	t.Helper()
	tools := activitiesOf(activities, ActivityKindTool)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v; want one", tools)
	}
	return tools[0]
}

func TestActivityParserCodex(t *testing.T) {
	activities := parseFixture(t, "codex", NewActivityParser(profile(t, "codex")))
	if len(activities) < 3 {
		t.Fatalf("activities = %#v", activities)
	}
	for i := 1; i < len(activities); i++ {
		if activities[i].Seq <= activities[i-1].Seq {
			t.Fatalf("sequence is not increasing: %#v", activities)
		}
	}
	tool := oneTool(t, activities)
	if tool.State != ActivityStateRunning || tool.Tool != "Bash" || tool.Command != "echo hi\nmore" {
		t.Fatalf("tool = %#v", tool)
	}
	messages := activitiesOf(activities, ActivityKindMessage)
	if len(messages) != 2 || messages[0].Text != "first" || !strings.Contains(messages[1].Text, `"status":"done"`) {
		t.Fatalf("messages = %#v", messages)
	}
	usage := activitiesOf(activities, ActivityKindUsage)
	if len(usage) != 1 || usage[0].Usage == nil || usage[0].Usage.Input != 100 || usage[0].Usage.CacheRead != 60 || usage[0].Usage.Output != 7 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestActivityParserClaude(t *testing.T) {
	parser := NewActivityParser(profile(t, "claude"))
	activities := parseFixture(t, "claude", parser)
	tool := oneTool(t, activities)
	if tool.Tool != "Bash" || tool.State != ActivityStateRunning || tool.Command != "echo hi\nsecond" {
		t.Fatalf("tool = %#v", tool)
	}
	// A result without a tool_result leaves the Bash call unconfirmed at the
	// end of the call: it is closed as unknown, not as a success.
	activities = append(activities, parser.Finalize(ActivityStateUnknown)...)
	tools := activitiesOf(activities, ActivityKindTool)
	if len(tools) != 2 || tools[1].State != ActivityStateUnknown || tools[1].ToolID != tools[0].ToolID {
		t.Fatalf("finalized tools = %#v", tools)
	}
	usage := activitiesOf(activities, ActivityKindUsage)
	if len(usage) != 1 || usage[0].Usage == nil || usage[0].Usage.Cost == nil || *usage[0].Usage.Cost != .25 || usage[0].Usage.CacheWrite != 5 {
		t.Fatalf("usage = %#v", usage)
	}
	// StructuredOutput and the rate limit event are control channels, not
	// activity.
	for _, activity := range activities {
		if activity.Tool == "StructuredOutput" {
			t.Fatalf("StructuredOutput reported as a tool: %#v", activity)
		}
	}
}

func TestActivityParserClaudeToolResult(t *testing.T) {
	parser := NewActivityParser(profile(t, "claude"))
	star := `{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"a.go"}}]}}`
	result := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"package a"}],"is_error":false}]}}`
	failed := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"permission denied","is_error":true}]}}`
	var activities []Activity
	for i, line := range []string{star, result, failed} {
		activities = append(activities, parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: int64(i), Length: len(line)})...)
	}
	if len(activities) != 3 {
		t.Fatalf("activities = %#v", activities)
	}
	if activities[0].State != ActivityStateRunning || activities[1].State != ActivityStateSucceeded || activities[1].Result != "package a" {
		t.Fatalf("completed tool = %#v", activities[1])
	}
	if activities[2].State != ActivityStateFailed || activities[2].ToolID != "t2" {
		t.Fatalf("failed tool = %#v", activities[2])
	}
}

func TestActivityParserPi(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	activities := parseFixture(t, "pi", parser)
	messages := activitiesOf(activities, ActivityKindMessage)
	if len(messages) != 1 || messages[0].Text != "OK" || messages[0].Role != "assistant" {
		t.Fatalf("messages = %#v", messages)
	}
	reasoning := activitiesOf(activities, ActivityKindReasoning)
	if len(reasoning) != 1 || reasoning[0].Text != "hm" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	usage := activitiesOf(activities, ActivityKindUsage)
	if len(usage) != 1 || usage[0].Usage == nil || usage[0].Usage.Input != 13349 || usage[0].Usage.Reasoning != 9 ||
		usage[0].Usage.Cost == nil || *usage[0].Usage.Cost != .002 {
		t.Fatalf("usage = %#v", usage)
	}
	unknown := activitiesOf(activities, ActivityKindUnknown)
	if len(unknown) != 1 || unknown[0].Raw != "not json" {
		t.Fatalf("unknown = %#v", unknown)
	}
}

func TestActivityParserPiToolPair(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	lines := []string{
		`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"go test"}}`,
		`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":"ok","isError":false}`,
	}
	var activities []Activity
	for i, line := range lines {
		activities = append(activities, parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: int64(i), Length: len(line)})...)
	}
	if len(activities) != 2 || activities[0].State != ActivityStateRunning || activities[1].State != ActivityStateSucceeded {
		t.Fatalf("activities = %#v", activities)
	}
	if activities[1].Tool != "bash" || activities[1].Result != "ok" || activities[1].Command != "go test" {
		t.Fatalf("result = %#v", activities[1])
	}
	if got := parser.Finalize(ActivityStateCancelled); len(got) != 0 {
		t.Fatalf("finalized a completed tool: %#v", got)
	}
}

// A live Pi call reports its tool twice: tool_execution_end carries the
// result and the following message_end repeats it as a toolResult. The
// history shows one named row, not a second unnamed one.
func TestActivityParserPiToolResultNotDuplicated(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	lines := []string{
		`{"type":"tool_execution_start","toolCallId":"call_1","toolName":"bash","args":{"command":"ls"}}`,
		`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"bash","result":{"content":[{"type":"text","text":"ok"}]},"isError":false}`,
		`{"type":"message_end","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}`,
	}
	var activities []Activity
	for i, line := range lines {
		activities = append(activities, parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: int64(i), Length: len(line)})...)
	}
	if len(activities) != 2 {
		t.Fatalf("activities = %#v", activities)
	}
	for _, activity := range activities {
		if activity.Tool != "bash" {
			t.Fatalf("unnamed tool row = %#v", activity)
		}
	}
	if activities[1].State != ActivityStateSucceeded || activities[1].Result != "ok" || activities[1].Command != "ls" {
		t.Fatalf("result = %#v", activities[1])
	}
}

// A toolResult whose execution event never arrived still names its tool, so a
// killed call does not leave a blank row in the history.
func TestActivityParserPiLoneToolResult(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	line := `{"type":"message_end","message":{"role":"toolResult","toolCallId":"call_2","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}`
	activities := parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: 0, Length: len(line)})
	tool := oneTool(t, activities)
	if tool.Tool != "bash" || tool.State != ActivityStateSucceeded || tool.Result != "ok" {
		t.Fatalf("lone result = %#v", tool)
	}
}

func TestActivityParserFileChange(t *testing.T) {
	parser := NewActivityParser(profile(t, "codex"))
	line := `{"type":"item.completed","item":{"id":"f1","type":"file_change","status":"completed","changes":[{"path":"a.go","kind":"update"},{"path":"b.go","kind":"add"}]}}`
	activities := parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: 0, Length: len(line)})
	tool := oneTool(t, activities)
	if tool.State != ActivityStateSucceeded || len(tool.Files) != 2 || tool.Files[0].Path != "a.go" || tool.Files[1].Kind != "add" {
		t.Fatalf("file change = %#v", tool)
	}
}

func TestActivityParserCancelledTool(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	start := `{"type":"tool_execution_start","toolCallId":"c9","toolName":"bash","args":{"command":"sleep 60"}}`
	running := parser.Feed("stdout", start, ActivityRef{Stream: "stdout", Offset: 0, Length: len(start)})
	if len(running) != 1 || running[0].State != ActivityStateRunning {
		t.Fatalf("start = %#v", running)
	}
	// A duplicate start (message_end tool call plus tool event) is reported
	// once.
	if again := parser.Feed("stdout", start, ActivityRef{Stream: "stdout", Offset: int64(len(start) + 1), Length: len(start)}); len(again) != 0 {
		t.Fatalf("duplicate start = %#v", again)
	}
	final := parser.Finalize(ActivityStateCancelled)
	if len(final) != 1 || final[0].State != ActivityStateCancelled || final[0].ToolID != "c9" || final[0].Command != "sleep 60" {
		t.Fatalf("finalize = %#v", final)
	}
	if rest := parser.Finalize(ActivityStateCancelled); len(rest) != 0 {
		t.Fatalf("second finalize = %#v", rest)
	}
}

func TestActivityParserDeltaAndFinalAreNotDuplicated(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	lines := []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hel"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"lo"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}}`,
	}
	var activities []Activity
	for i, line := range lines {
		activities = append(activities, parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: int64(i), Length: len(line)})...)
	}
	if len(activities) != 3 {
		t.Fatalf("activities = %#v, want two deltas and a final observation", activities)
	}
	for _, activity := range activities[:2] {
		if !activity.Delta || activity.State != ActivityStateRunning {
			t.Fatalf("delta = %#v", activity)
		}
	}
	if activities[2].Delta || activities[2].State != ActivityStateSucceeded || activities[2].Text != "" {
		t.Fatalf("final observation = %#v", activities[2])
	}
	if activities[0].Text+activities[1].Text != "Hello" {
		t.Fatalf("text = %q", activities[0].Text+activities[1].Text)
	}
}

func TestActivityParserUnknownTypeKeepsRaw(t *testing.T) {
	parser := NewActivityParser(profile(t, "codex"))
	line := `{"type":"brand_new_event","payload":{"n":1}}`
	activities := parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: 7, Length: len(line)})
	if len(activities) != 1 || activities[0].Kind != ActivityKindUnknown || activities[0].Raw != line || activities[0].Ref.Offset != 7 {
		t.Fatalf("unknown = %#v", activities)
	}
	partial := `{"type":"turn.completed","usage":`
	activities = parser.Feed("stdout", partial, ActivityRef{Stream: "stdout", Offset: 99, Length: len(partial)})
	if len(activities) != 1 || activities[0].Kind != ActivityKindUnknown || activities[0].Raw != partial {
		t.Fatalf("partial = %#v", activities)
	}
	// A complete event without a trailing newline still decodes: the writer
	// flushes the remainder as a line.
	complete := `{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":2}}`
	activities = parser.Feed("stdout", complete, ActivityRef{Stream: "stdout", Offset: 200, Length: len(complete)})
	if len(activities) != 1 || activities[0].Kind != ActivityKindUsage || activities[0].Usage.Input != 3 {
		t.Fatalf("flushed event = %#v", activities)
	}
}

func TestActivityStderrKeepsRawLine(t *testing.T) {
	parser := NewActivityParser(profile(t, "codex"))
	line := "warning: \x1b[31mred\x1b[0m"
	activities := parser.Feed("stderr", line, ActivityRef{Stream: "stderr", Offset: 3, Length: len(line)})
	if len(activities) != 1 || activities[0].Kind != ActivityKindStderr || activities[0].Raw != line || activities[0].Ref.Stream != "stderr" {
		t.Fatalf("stderr = %#v", activities)
	}
}

func TestReadActivityPage(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "call.out")
	stdout := "--- stdout\nfirst line\nsecond line"
	stderr := "err one\nerr two"
	if err := os.WriteFile(log, []byte("$ cmd\n"+stdout+"\n--- stderr\n"+stderr+"\n--- exit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StderrLog(log), []byte(stderr), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := ActivityRef{Stream: "stdout", Offset: int64(len("$ cmd\n--- stdout\n")), Length: len("first line\nsecond line")}
	page, total, err := ReadActivityPage(log, ref, 0, 5)
	if err != nil || page != "first" || total != len("first line\nsecond line") {
		t.Fatalf("first page = %q, %d, %v", page, total, err)
	}
	page, _, err = ReadActivityPage(log, ref, 11, 100)
	if err != nil || page != "second line" {
		t.Fatalf("second page = %q, %v", page, err)
	}
	errRef := ActivityRef{Stream: "stderr", Offset: int64(len("err one\n")), Length: len("err two")}
	page, total, err = ReadActivityPage(log, errRef, 0, 7)
	if err != nil || page != "err two" || total != 7 {
		t.Fatalf("stderr page = %q, %d, %v", page, total, err)
	}
}

func TestCallAgentActivitySubscription(t *testing.T) {
	root, argv := fakeScenario(t,
		"printf 'stderr before result\\n' >&2\n"+
			"sleep 0.2\n"+
			"printf '%s\\n' '{\"type\":\"item.started\",\"item\":{\"id\":\"t1\",\"type\":\"command_execution\",\"command\":\"echo hi\"}}'\n"+
			"sleep 0.1\n"+
			"printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"id\":\"t1\",\"type\":\"command_execution\",\"aggregated_output\":\"ok\",\"exit_code\":0}}'\n"+
			"printf '%s' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}'\n",
	)
	log := filepath.Join(root, "call.out")
	var activities []Activity
	run, err := CallAgent(root, argv, nil, log, CallOptions{
		Stream:     "codex",
		OnActivity: func(activity Activity) { activities = append(activities, activity) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Code != 0 {
		t.Fatalf("call = %#v", run)
	}
	if len(activities) != 4 {
		t.Fatalf("activities = %#v", activities)
	}
	if activities[0].Kind != ActivityKindStderr || activities[0].Raw != "stderr before result" {
		t.Fatalf("stderr is not visible before the result = %#v", activities)
	}
	if activities[1].Kind != ActivityKindTool || activities[1].State != ActivityStateRunning {
		t.Fatalf("start = %#v", activities[1])
	}
	if activities[2].Kind != ActivityKindTool || activities[2].State != ActivityStateSucceeded || activities[2].Result != "ok" {
		t.Fatalf("result = %#v", activities[2])
	}
	if activities[3].Kind != ActivityKindUsage || activities[3].Usage.Input != 4 {
		t.Fatalf("flushed usage = %#v", activities[3])
	}
	// The original log is unchanged; the sidecar holds stderr and every ref
	// resolves to the same bytes the parser saw.
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n--- stderr\nstderr before result") || !strings.Contains(string(data), "\n--- exit 0\n") {
		t.Fatalf("call log = %q", data)
	}
	for _, activity := range activities {
		want := "stdout"
		if activity.Kind == ActivityKindStderr {
			want = "stderr"
		}
		if activity.Ref.Stream != want {
			t.Fatalf("ref stream = %q, want %q: %#v", activity.Ref.Stream, want, activity.Ref)
		}
		page, _, err := ReadActivityPage(log, activity.Ref, 0, activity.Ref.Length)
		if err != nil {
			t.Fatalf("page %#v: %v", activity.Ref, err)
		}
		if activity.Kind == ActivityKindTool && activity.State == ActivityStateRunning && !strings.Contains(page, "item.started") {
			t.Fatalf("tool ref reads %q", page)
		}
		if activity.Kind == ActivityKindStderr && page != "stderr before result" {
			t.Fatalf("stderr ref reads %q", page)
		}
		if page == "" {
			t.Fatalf("empty page for %#v", activity)
		}
		if activity.Raw != "" && len(activity.Raw) < ActivityInlineLimit && page != activity.Raw {
			t.Fatalf("%s ref reads %q, want the original %q", want, page, activity.Raw)
		}
	}
}

func TestCallAgentActivitySlowReader(t *testing.T) {
	root, argv := fakeScenario(t, "for i in 1 2 3 4 5; do printf '%s\\n' '{\"type\":\"turn.started\"}'; done\nprintf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1}}'\n")
	var activities []Activity
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "call.out"), CallOptions{
		Stream: "codex",
		OnActivity: func(activity Activity) {
			time.Sleep(20 * time.Millisecond)
			activities = append(activities, activity)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Code != 0 || len(activities) != 1 || activities[0].Kind != ActivityKindUsage {
		t.Fatalf("call = %#v, activities = %#v", run, activities)
	}
}

func TestActivityToolInputClipped(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	big := strings.Repeat("x", 1<<20)
	line, err := json.Marshal(map[string]any{
		"type": "tool_execution_start", "toolCallId": "big", "toolName": "bash",
		"args": map[string]any{"command": big},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := string(line)
	ref := ActivityRef{Stream: "stdout", Offset: 0, Length: len(start)}
	activities := parser.Feed("stdout", start, ref)
	tool := oneTool(t, activities)
	input, err := json.Marshal(tool.Input)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) > ActivityInlineLimit {
		t.Fatalf("input is %d bytes, want <= %d", len(input), ActivityInlineLimit)
	}
	command, _ := tool.Input["command"].(string)
	if len(command) >= len(big) || !strings.HasSuffix(command, "…") {
		t.Fatalf("command was not clipped: %d bytes", len(command))
	}
	if _, ok := tool.Input["truncated"]; ok {
		t.Fatalf("a single long string should be clipped, not marked: %#v", tool.Input)
	}
	// Ref still locates the complete fragment in the original log.
	log := filepath.Join(t.TempDir(), "call.out")
	if err := os.WriteFile(log, []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	page, total, err := ReadActivityPage(log, tool.Ref, 0, tool.Ref.Length)
	if err != nil || page != start || total != len(start) {
		t.Fatalf("ref page = %d bytes, total %d, err %v; want %d", len(page), total, err, len(start))
	}

	end, err := json.Marshal(map[string]any{
		"type": "tool_execution_end", "toolCallId": "big", "toolName": "bash",
		"result": "ok", "isError": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := oneTool(t, parser.Feed("stdout", string(end), ActivityRef{Stream: "stdout", Offset: int64(len(start)) + 1, Length: len(end)}))
	if finished.State != ActivityStateSucceeded {
		t.Fatalf("end = %#v", finished)
	}
	if !reflect.DeepEqual(finished.Input, tool.Input) {
		t.Fatalf("end input = %#v; want the clipped start input %#v", finished.Input, tool.Input)
	}
}

func TestActivityToolInputSmallUnchanged(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	line := `{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"go test","timeout":5}}`
	activities := parser.Feed("stdout", line, ActivityRef{Stream: "stdout", Offset: 0, Length: len(line)})
	tool := oneTool(t, activities)
	want := map[string]any{"command": "go test", "timeout": float64(5)}
	if !reflect.DeepEqual(tool.Input, want) {
		t.Fatalf("input = %#v; want %#v", tool.Input, want)
	}
}

func TestActivityToolInputMarker(t *testing.T) {
	parser := NewActivityParser(profile(t, "pi"))
	args := map[string]any{}
	for i := 0; i < 1000; i++ {
		args[fmt.Sprintf("k%04d", i)] = strings.Repeat("v", 20)
	}
	line, err := json.Marshal(map[string]any{
		"type": "tool_execution_start", "toolCallId": "many", "toolName": "bash", "args": args,
	})
	if err != nil {
		t.Fatal(err)
	}
	activities := parser.Feed("stdout", string(line), ActivityRef{Stream: "stdout", Offset: 0, Length: len(line)})
	tool := oneTool(t, activities)
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"truncated": true, "bytes": len(raw)}
	if !reflect.DeepEqual(tool.Input, want) {
		t.Fatalf("input = %#v; want marker %#v", tool.Input, want)
	}
}
