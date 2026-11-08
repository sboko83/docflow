package agentio

import (
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
)

// dshAgent is the built-in dsh profile, so the tests hold its json: paths and
// regexes to what DshOutput produces.
func dshAgent() map[string]any {
	config, err := agents.LoadAgents("", false)
	if err != nil {
		panic(err)
	}
	return table(table(config["agents"])["dsh"])
}

func TestDshFold(t *testing.T) {
	agent := dshAgent()
	out := fixture(t, "dsh")
	data := table(AgentOutput(agent, out))
	equal(t, data["session"], "session-2410985d-4bc0-40a2-ba1f-2e0eea71471f")
	equal(t, data["is_error"], false)
	equal(t, data["error"], nil)
	if text := str(data["text"]); !strings.Contains(text, "```json") {
		t.Fatalf("text = %q", text)
	}
	equal(t, ExtractResult(agent, out), obj(`{"status":"done","handoff":["git status was clean"],"decisions":[],"unverified":[],"reason":"","blocker":""}`))
	// Two steps: 1645+183 input, 60+110 output, 5632+7168 cache read.
	equal(t, JSONPath(data, str(agent["output_tokens"])), float64(170))
	for i, spec := range commandList(agent["tokens"]) {
		equal(t, JSONPath(data, spec), []float64{1828, 12800, 0}[i])
	}
	equal(t, CallSession(agent, out), "session-2410985d-4bc0-40a2-ba1f-2e0eea71471f")
}

func TestDshErrorWithoutFinal(t *testing.T) {
	agent := dshAgent()
	out := fixture(t, "dsh-error")
	data := table(AgentOutput(agent, out))
	equal(t, data["is_error"], true)
	equal(t, data["error"], "unknown option '--bogus-flag'")
	equal(t, data["session"], nil)
	equal(t, CallSession(agent, out), "")
	if ExtractResult(agent, out) != nil {
		t.Fatal("an error event carries no result")
	}
	run := AgentRun{Stdout: out, Code: 1}
	if FailureReason(agent, run) != "unknown option '--bogus-flag'" {
		t.Fatalf("reason = %q", FailureReason(agent, run))
	}
}

func TestDshAuthFailure(t *testing.T) {
	agent := dshAgent()
	out := fixture(t, "dsh-auth")
	data := table(AgentOutput(agent, out))
	equal(t, data["is_error"], true)
	equal(t, data["error_code"], "AUTH")
	if !strings.Contains(str(data["error"]), "Authentication Fails") {
		t.Fatalf("error = %q", data["error"])
	}
	run := AgentRun{Stdout: out, Stderr: "dsh: AUTH: Authentication Fails\n", Code: 1}
	if AuthReason(agent, run) == "" {
		t.Fatal("auth refusal not recognized")
	}
}

// Success needs reason.kind "completed": any other kind, a null reason and a
// missing final all fail the call.
func TestDshFinalReason(t *testing.T) {
	agent := dshAgent()
	for _, c := range []struct {
		name   string
		stdout string
		failed bool
		error  any
	}{
		{"completed", lines(map[string]any{"type": "final", "sessionId": "s1", "reason": map[string]any{"kind": "completed"}, "text": "ok"}), false, nil},
		{"null reason", lines(map[string]any{"type": "final", "sessionId": "s1", "reason": nil, "text": "ok"}), true, "dsh final event without a reason"},
		{"aborted", lines(map[string]any{"type": "final", "sessionId": "s1", "reason": map[string]any{"kind": "aborted"}}), true, "dsh run ended: aborted"},
		{"no final", lines(map[string]any{"type": "session", "sessionId": "s1"}, map[string]any{"type": "text", "text": "partial"}), true, nil},
		{"error after text", lines(map[string]any{"type": "text", "text": "partial"}, map[string]any{"type": "error", "message": "boom"}), true, "boom"},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := table(AgentOutput(agent, c.stdout))
			equal(t, data["is_error"], c.failed)
			equal(t, data["error"], c.error)
		})
	}
	// The answer survives a final without text: the last text event stays.
	data := table(AgentOutput(agent, lines(
		map[string]any{"type": "text", "text": "answer"},
		map[string]any{"type": "final", "reason": map[string]any{"kind": "completed"}, "text": ""},
	)))
	equal(t, data["text"], "answer")
}

func TestDshTruncated(t *testing.T) {
	agent := dshAgent()
	out := fixture(t, "dsh-truncated")
	long := false
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 8192 && strings.Contains(line, `"truncated":true`) {
			long = true
		}
	}
	if !long {
		t.Fatal("fixture lost its truncated event")
	}
	data := table(AgentOutput(agent, out))
	equal(t, data["is_error"], false)
	equal(t, data["text"], "ok")
	tools := activitiesOf(parseFixture(t, "dsh-truncated", NewActivityParser(agent)), ActivityKindTool)
	if len(tools) != 2 || tools[1].State != ActivityStateSucceeded || tools[1].Tool != "pwsh" || tools[1].Result == "" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestDshStreamActivity(t *testing.T) {
	agent := dshAgent()
	equal(t, StreamEvent(agent, obj(`{"type":"text","text":"ok"}`)), map[string]any{"assistant_text": "ok"})
	equal(t, StreamEvent(agent, obj(`{"type":"tool_call","callId":"c1","tool":"pwsh","input":{"command":"git status --short","description":"d"}}`)), map[string]any{"activity": "pwsh git status --short"})
	equal(t, StreamEvent(agent, obj(`{"type":"tool_call","callId":"c2","tool":"write","input":{"file_path":"b.txt","content":"x"}}`)), map[string]any{"activity": "write b.txt"})
	equal(t, StreamEvent(agent, obj(`{"type":"plan_update","items":[]}`)), map[string]any{"activity": "plan_update"})
	for _, v := range []string{
		`{"type":"session","sessionId":"s1"}`,
		`{"type":"status","phase":"step_start","turn":1,"step":1}`,
		`{"type":"thinking","text":"hidden"}`,
		`{"type":"tool_result","callId":"c1","status":"completed","result":"x"}`,
		`{"type":"final","reason":{"kind":"completed"},"text":"ok"}`,
		`{"type":"error","message":"boom"}`,
		`{"type":"text","text":""}`,
	} {
		equal(t, StreamEvent(agent, obj(v)), map[string]any(nil))
	}
	messages := AssistantMessages(agent, fixture(t, "dsh"))
	if len(messages) != 1 || !strings.Contains(messages[0], "```json") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestDshReasoning(t *testing.T) {
	blocks := Reasoning(dshAgent(), fixture(t, "dsh"))
	if len(blocks) != 1 || !strings.Contains(blocks[0], "no output = clean") {
		t.Fatalf("blocks = %#v", blocks)
	}
}

func TestActivityParserDsh(t *testing.T) {
	activities := parseFixture(t, "dsh", NewActivityParser(dshAgent()))
	tools := activitiesOf(activities, ActivityKindTool)
	if len(tools) != 2 || tools[0].State != ActivityStateRunning || tools[1].State != ActivityStateSucceeded ||
		tools[1].Command != "git status --short" || tools[1].Result != "(no output)" {
		t.Fatalf("tools = %#v", tools)
	}
	if len(activitiesOf(activities, ActivityKindReasoning)) != 1 {
		t.Fatalf("activities = %#v", activities)
	}
	messages := activitiesOf(activities, ActivityKindMessage)
	if len(messages) != 1 || !strings.Contains(messages[0].Text, "```json") {
		t.Fatalf("messages = %#v", messages)
	}
	usage := activitiesOf(activities, ActivityKindUsage)
	if len(usage) != 2 || usage[0].Usage.Input != 1645 || usage[0].Usage.CacheRead != 5632 || usage[1].Usage.Output != 110 {
		t.Fatalf("usage = %#v", usage)
	}
	if unknown := activitiesOf(activities, ActivityKindUnknown); len(unknown) != 0 {
		t.Fatalf("unknown = %#v", unknown)
	}
	failed := NewActivityParser(dshAgent()).Feed("stdout", `{"type":"tool_result","callId":"c9","tool":"pwsh","status":"error","result":"Error: denied"}`, ActivityRef{})
	if len(failed) != 1 || failed[0].State != ActivityStateFailed {
		t.Fatalf("failed = %#v", failed)
	}
	errorEvent := parseFixture(t, "dsh-error", NewActivityParser(dshAgent()))
	if len(errorEvent) != 1 || errorEvent[0].State != ActivityStateFailed || errorEvent[0].Text != "unknown option '--bogus-flag'" {
		t.Fatalf("error = %#v", errorEvent)
	}
}

func TestLivenessDsh(t *testing.T) {
	live := NewLiveness("dsh")
	live.Observe(`{"type":"status","phase":"step_start","turn":1,"step":1}`)
	live.Observe(`{"type":"tool_call","callId":"c1","tool":"pwsh","input":{"command":"Start-Sleep 90"}}`)
	state := live.State()
	equal(t, state.Turns, 1)
	equal(t, state.Tools, 1)
	if state.OldestTool.IsZero() {
		t.Fatal("silent command has no start time")
	}
	live.Observe(`{"type":"tool_result","callId":"c1","status":"completed","result":"done"}`)
	equal(t, live.State().Tools, 0)
	live.Observe(`{"type":"tool_call","callId":"c2","tool":"pwsh","input":{}}`)
	live.Observe(`{"type":"status","phase":"step_end","turn":1,"step":1}`)
	equal(t, live.State().Tools, 0)
	full := NewLiveness("dsh")
	for _, line := range strings.Split(fixture(t, "dsh"), "\n") {
		full.Observe(line)
	}
	equal(t, full.State().Turns, 2)
	equal(t, full.State().Tools, 0)
}

// A real process replays the captured streams: the success decodes with its
// session, the launch error exits 1 and reads as a failure.
func TestCallAgentFakeDsh(t *testing.T) {
	agent := dshAgent()
	for _, c := range []struct {
		name string
		code int
	}{{"dsh", 0}, {"dsh-error", 1}} {
		t.Run(c.name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("testdata", c.name+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			root, argv := fakeScenario(t, "cat "+quote(path)+"\nexit "+string(rune('0'+c.code))+"\n")
			var decoded []map[string]any
			stdin := "prompt\n"
			run, err := CallAgent(root, argv, &stdin, filepath.Join(root, "run.out"), CallOptions{OnStreamEvent: func(e Event) {
				if event := StreamEvent(agent, e); event != nil {
					decoded = append(decoded, event)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			equal(t, run.Code, c.code)
			equal(t, run.Stdout, fixture(t, c.name))
			if c.code == 0 {
				equal(t, JSONPath(AgentOutput(agent, run.Stdout), "json:session"), "session-2410985d-4bc0-40a2-ba1f-2e0eea71471f")
				if len(decoded) != 2 || decoded[0]["activity"] != "pwsh git status --short" {
					t.Fatalf("decoded = %#v", decoded)
				}
				if FailureText(agent, run) != nil {
					t.Fatal("a completed run is no failure")
				}
				return
			}
			if FailureText(agent, run) == nil {
				t.Fatal("a launch error must read as a failure")
			}
		})
	}
}
