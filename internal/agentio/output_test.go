package agentio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
)

func equal(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
func profile(t *testing.T, name string) map[string]any {
	t.Helper()
	config, err := agents.LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	return table(table(config["agents"])[name])
}
func lines(events ...any) string {
	var out strings.Builder
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			panic(err)
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	return out.String()
}
func obj(text string) map[string]any { return ParseLine(text) }
func floatPtr(n float64) *float64    { return &n }
func intPtr(n int) *int              { return &n }
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStreamParseJSON(t *testing.T) {
	for _, c := range []struct {
		in   string
		want any
	}{
		{`{"a":1}`, obj(`{"a":1}`)},
		{"{\"type\":\"a\"}\nnoise\n{\"type\":\"result\",\"n\":1}\n\n", obj(`{"type":"result","n":1}`)},
		{"garbage\nmore", nil}, {"", nil}, {"[]", []any{}}, {"null", nil}, {"3", float64(3)},
	} {
		equal(t, ParseJSON(c.in), c.want)
	}
	equal(t, ParseLine("[]"), map[string]any(nil))
}

func TestStreamQuotaAndActivity(t *testing.T) {
	agent := profile(t, "claude")
	events := strings.Split(fixture(t, "claude"), "\n")
	equal(t, StreamEvent(agent, ParseLine(events[0])), map[string]any{"quota": Quota{{"five_hour", 0.08, float64(10)}, {"seven_day", 0.07, float64(20)}}})
	equal(t, StreamEvent(agent, ParseLine(events[1])), map[string]any{"activity": "Bash echo hi"})
	long := obj(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + strings.Repeat("я", 100) + `"}}]}}`)
	activity := str(StreamEvent(agent, long)["activity"])
	if len([]rune(activity)) != 65 || !strings.HasSuffix(activity, "…") {
		t.Fatal(activity)
	}
	for _, v := range []any{ParseLine(events[2]), obj(`{"type":"result"}`), nil, []any{}} {
		equal(t, StreamEvent(agent, v), map[string]any(nil))
	}
	equal(t, StreamEvent(map[string]any{}, ParseLine(events[0])), map[string]any(nil))
	equal(t, StreamEvent(agent, obj(`{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"x":{"utilization":true}}}}`)), map[string]any(nil))
}

func TestStreamQuotaSourceOrder(t *testing.T) {
	raw := `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"seven_day":{"utilization":0.1},"five_hour":{"utilization":0.2},"other":{"utilization":0.3}}}}`
	equal(t, StreamEvent(profile(t, "claude"), Event{ParseLine(raw), raw}), map[string]any{"quota": Quota{{"seven_day", 0.1, nil}, {"five_hour", 0.2, nil}, {"other", 0.3, nil}}})
	q := Quota{}
	q.Set("a", 0.1, nil)
	q.Set("b", 0.2, nil)
	q.Set("a", 0.3, 10)
	equal(t, q, Quota{{"a", 0.3, 10}, {"b", 0.2, nil}})
}

func TestCodexFold(t *testing.T) {
	agent := profile(t, "codex")
	out := fixture(t, "codex")
	data := table(AgentOutput(agent, out))
	equal(t, data["thread_id"], "t1")
	equal(t, data["is_error"], false)
	equal(t, ExtractResult(agent, out), obj(`{"status":"done","handoff":["a"]}`))
	run := AgentRun{Stdout: out}
	equal(t, *RunTokens(agent, run), 100)
	equal(t, *RunOutputTokens(agent, run), 7)
}

func TestCodexErrors(t *testing.T) {
	agent := profile(t, "codex")
	for _, event := range []any{obj(`{"type":"turn.failed","error":{"message":"boom"}}`), obj(`{"type":"error","message":"boom"}`)} {
		data := table(AgentOutput(agent, lines(obj(`{"type":"thread.started","thread_id":"t1"}`), event)))
		equal(t, data["is_error"], true)
		equal(t, data["error"], "boom")
	}
	run := AgentRun{Code: 1, Stdout: lines(obj(`{"type":"error","message":"You hit your usage limit"}`))}
	if text := LimitText(agent, run); text == nil || !strings.Contains(*text, "usage limit") {
		t.Fatal(text)
	}
}

func TestCodexActivity(t *testing.T) {
	agent := profile(t, "codex")
	for _, c := range []struct{ item, kind, activity string }{
		{`{"type":"command_execution","command":"echo hi\nmore"}`, "item.started", "Bash echo hi"},
		{`{"type":"file_change","changes":[{"path":"a.py","kind":"update"},{"path":"b.py"}]}`, "item.started", "Edit a.py"},
		{`{"type":"mcp_tool_call"}`, "item.started", "mcp_tool_call"},
		{`{"type":"agent_message","text":"x"}`, "item.started", ""},
		{`{"type":"command_execution"}`, "item.completed", ""},
	} {
		event := StreamEvent(agent, map[string]any{"type": c.kind, "item": obj(c.item)})
		if c.activity == "" {
			equal(t, event, map[string]any(nil))
		} else {
			equal(t, event, map[string]any{"activity": c.activity})
		}
	}
	equal(t, StreamEvent(agent, obj(`{"type":"turn.completed"}`)), map[string]any(nil))
}

func TestPiStreamDecoder(t *testing.T) {
	pi := profile(t, "pi")
	stdout := fakeRun(t, fixture(t, "pi"), "", 0).Stdout
	data := AgentOutput(pi, stdout)
	for key, want := range map[string]any{"session": "sid-1", "text": "OK", "cost": 0.002, "error": false, "output_tokens": float64(3)} {
		equal(t, JSONPath(data, str(pi[key])), want)
	}
	for i, spec := range commandList(pi["tokens"]) {
		equal(t, JSONPath(data, spec), []float64{13349, 100, 20}[i])
	}
	equal(t, JSONPath(AgentOutput(pi, strings.ReplaceAll(stdout, `"aborted":false`, `"aborted":true`)), str(pi["error"])), true)
	parts := strings.Split(stdout, "\n")
	data = AgentOutput(pi, strings.Join(append(parts[:3], "not json"), "\n"))
	equal(t, JSONPath(data, str(pi["error"])), true)
	equal(t, JSONPath(data, str(pi["text"])), nil)
}

func TestPiMultiTurnSumsUsage(t *testing.T) {
	pi := profile(t, "pi")
	assistant := func(text string, n int, cost float64) any {
		return map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": text}}, "usage": map[string]any{"input": n, "output": 10, "cacheRead": 2 * n, "cacheWrite": 0, "cost": map[string]any{"total": cost}}}}
	}
	stdout := lines(obj(`{"type":"session","id":"s1"}`), assistant("tool call", 100, 0.25), obj(`{"type":"message_end","message":{"role":"toolResult","content":[]}}`), assistant("done", 300, 0.5), obj(`{"type":"agent_settled","aborted":false}`))
	stdout = fakeRun(t, stdout, "", 0).Stdout
	data := AgentOutput(pi, stdout)
	equal(t, JSONPath(data, str(pi["text"])), "done")
	equal(t, JSONPath(data, str(pi["cost"])), 0.75)
	equal(t, JSONPath(data, str(pi["output_tokens"])), float64(20))
	for i, spec := range commandList(pi["tokens"]) {
		equal(t, JSONPath(data, spec), []float64{400, 800, 0}[i])
	}
	equal(t, JSONPath(data, str(pi["error"])), false)
	equal(t, AddNumbers(map[string]any{"a": float64(1)}, map[string]any{"a": 2, "b": true, "c": "3"}), map[string]any{"a": float64(3)})
}

func TestPiAuthReason(t *testing.T) {
	run := fakeRun(t, "{\"type\":\"session\",\"id\":\"s1\"}\nNo API key found for openai.\n\nUse /login to log into a provider via OAuth or API key.\n", "", 1)
	equal(t, AuthReason(profile(t, "pi"), run), "No API key found for openai.")
}

func TestPiAPIError(t *testing.T) {
	pi := profile(t, "pi")
	errorText := `400: {"code":"1211","message":"Unknown Model, please check the model code."}`
	out := lines(obj(`{"type":"session","id":"s1"}`), map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{}, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "cost": map[string]any{"total": 0}}, "stopReason": "error", "errorMessage": errorText}}, obj(`{"type":"agent_settled","aborted":false}`))
	run := fakeRun(t, out, "Warning: Model not found\n", 0)
	equal(t, JSONPath(AgentOutput(pi, out), str(pi["error"])), true)
	equal(t, FailureReason(pi, run), errorText)
	equal(t, LimitText(pi, run), (*string)(nil))
	run.Stdout = strings.ReplaceAll(out, "400: ", "429: rate limit; ")
	if LimitText(pi, run) == nil {
		t.Fatal("missing rate limit")
	}
}

func TestResultProblems(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{`{"status":"done","handoff":["a"]}`, ""},
		{`{"status":"done"}`, "handoff"}, {`{"status":"done","handoff":[""]}`, "handoff"},
		{`{"status":"done","handoff":["a"],"decisions":"x"}`, "decisions"},
		{`{"status":"done","handoff":["a"],"unverified":null}`, "unverified"},
		{`{"status":"blocked"}`, "reason"}, {`{"status":"ok"}`, "status"}, {`null`, "no result"},
		{`{"status":"blocked","reason":"x"}`, ""},
		{`{"status":"blocked","reason":"x","blocker":"plan"}`, ""},
		{`{"status":"blocked","reason":"x","blocker":"human"}`, ""},
		{`{"status":"blocked","reason":"x","blocker":""}`, "blocker"},
		{`{"status":"done","handoff":["a"],"blocker":"plan"}`, "blocker"},
	} {
		got := ResultProblem(obj(c.text))
		if c.want == "" {
			equal(t, got, "")
		} else if !strings.Contains(got, c.want) {
			t.Fatalf("%s: %s", c.text, got)
		}
	}
}

func TestResultExtraction(t *testing.T) {
	agent := map[string]any{"result": "json:structured_output", "text": "json:result"}
	for _, c := range []struct{ out, want string }{
		{`{"result":"x\n` + "```json\\n{\\\"status\\\": \\\"done\\\"}\\n```" + `"}`, `{"status":"done"}`},
		{"```json\n{\"a\":1}\n```\n```json\n{\"b\":2}\n```\n", `{"b":2}`},
		{"Done. {\"x\":1} moved notes\n{\"status\":\"done\",\"handoff\":[\"{a}\"],\"reason\":\"\"}\n", `{"status":"done","handoff":["{a}"],"reason":""}`},
		{"{\"a\":1}\n", `null`}, {`{"status":"done"} and then more text`, `null`},
		{`{"structured_output":{"a":1}}`, `{"a":1}`},
		{`{"structured_output":"{\"a\":1}"}`, `{"a":1}`},
	} {
		equal(t, ExtractResult(agent, c.out), obj(c.want))
	}
	equal(t, JSONPath(obj(`{"a":{"b":2}}`), "json:a.b"), float64(2))
	equal(t, JSONPath(nil, "json:a"), nil)
	equal(t, JSONPath(obj(`{"a":1}`), "a"), nil)
}
