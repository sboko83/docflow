package agentio

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLivenessStreams(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		live := NewLiveness("claude")
		live.Observe(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"Bash"},{"type":"tool_use","id":"t2","name":"Read"},{"type":"tool_use","id":"t3","name":"StructuredOutput"}]}}`)
		state := live.State()
		equal(t, state.Turns, 1)
		equal(t, state.Tools, 2) // StructuredOutput is the result channel, not a tool
		live.Observe(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`)
		equal(t, live.State().Tools, 1)
		live.Observe(`{"type":"result","subtype":"success"}`)
		state = live.State()
		equal(t, state.Turns, 1)
		equal(t, state.Tools, 0)
	})
	t.Run("codex", func(t *testing.T) {
		live := NewLiveness("codex")
		live.Observe(`{"type":"turn.started"}`)
		live.Observe(`{"type":"item.started","item":{"id":"i1","type":"reasoning"}}`)
		state := live.State()
		equal(t, state.Turns, 1)
		equal(t, state.Tools, 0) // reasoning is model output, not a tool call
		live.Observe(`{"type":"item.started","item":{"id":"i2","type":"command_execution","command":"ls"}}`)
		state = live.State()
		equal(t, state.Turns, 1)
		equal(t, state.Tools, 1)
		if state.OldestTool.IsZero() {
			t.Fatal("oldest in-flight tool has no start time")
		}
		live.Observe(`{"type":"item.completed","item":{"id":"i2","type":"command_execution","exit_code":0}}`)
		equal(t, live.State().Tools, 0)
	})
	t.Run("pi", func(t *testing.T) {
		live := NewLiveness("pi")
		live.Observe(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash"}`)
		equal(t, live.State().Tools, 1)
		live.Observe(`{"type":"message_end","message":{"role":"toolResult","content":[]}}`)
		equal(t, live.State().Turns, 0) // a tool result is not a turn
		live.Observe(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`)
		state := live.State()
		equal(t, state.Turns, 1)
		equal(t, state.Tools, 1)
		live.Observe(`{"type":"tool_execution_end","toolCallId":"c1"}`)
		equal(t, live.State().Tools, 0)
	})
	t.Run("non-json lines still count as events", func(t *testing.T) {
		live := NewLiveness("claude")
		before := live.State().LastLine
		time.Sleep(2 * time.Millisecond)
		live.Observe("plain text, not an event")
		state := live.State()
		if !state.LastLine.After(before) {
			t.Fatal("a non-JSON line did not refresh the last event time")
		}
		equal(t, state.Turns, 0)
	})
}

func TestCallAgentStall(t *testing.T) {
	root, argv := fakeScenario(t, "printf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\"}}'\nsleep 60\n")
	live := NewLiveness("pi")
	log := filepath.Join(root, "log")
	start := time.Now()
	run, err := CallAgent(root, argv, nil, log, CallOptions{Timeout: 60 * time.Second, Idle: 300 * time.Millisecond, Liveness: live})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.TimedOut, false)
	equal(t, run.Stalled, true)
	if runtime.GOOS != "windows" {
		equal(t, run.Code, -15)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("stall kill took %s", elapsed)
	}
	equal(t, live.State().Turns, 1)
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	order := []string{"$ ", "--- stdout", `"type":"message_end"`, "--- stderr", "--- exit -15 (stalled)"}
	at := 0
	for _, marker := range order {
		i := strings.Index(text[at:], marker)
		if i < 0 {
			t.Fatalf("log missing %q after offset %d:\n%s", marker, at, text)
		}
		at += i + len(marker)
	}
}

func TestCallAgentStallToolInFlight(t *testing.T) {
	// The stream stays silent well past the idle limit, but a tool is
	// running: the call must survive the checks and finish on its own.
	root, argv := fakeScenario(t,
		"printf '%s\\n' '{\"type\":\"tool_execution_start\",\"toolCallId\":\"c1\",\"toolName\":\"bash\"}'\n"+
			"sleep 5\n"+
			"printf '%s\\n' '{\"type\":\"tool_execution_end\",\"toolCallId\":\"c1\"}'\n")
	live := NewLiveness("pi")
	run, err := CallAgent(root, argv, nil, filepath.Join(root, "log"), CallOptions{Timeout: 60 * time.Second, Idle: 300 * time.Millisecond, Liveness: live})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.Stalled, false)
	equal(t, run.TimedOut, false)
	equal(t, run.Code, 0)
	state := live.State()
	equal(t, state.Tools, 0)
	equal(t, state.Turns, 0)
}

func TestCallAgentLogStreamsWhileRunning(t *testing.T) {
	root, argv := fakeScenario(t, "printf '%s\\n' '{\"type\":\"ready\"}'\nsleep 60\n")
	log := filepath.Join(root, "log")
	streamed := make(chan string, 1)
	options := CallOptions{Timeout: 300 * time.Millisecond, OnEvent: func(data map[string]any) {
		if data["type"] != "ready" {
			return
		}
		b, err := os.ReadFile(log)
		if err != nil {
			streamed <- "read: " + err.Error()
			return
		}
		if !strings.Contains(string(b), `"type":"ready"`) {
			streamed <- "stdout lines are not in the log while the agent runs"
			return
		}
		streamed <- ""
	}}
	run, err := CallAgent(root, argv, nil, log, options)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, run.TimedOut, true)
	if problem := <-streamed; problem != "" {
		t.Fatal(problem)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	order := []string{"$ ", "--- stdout", `"type":"ready"`, "--- stderr", "--- exit -15 (timed out)"}
	at := 0
	for _, marker := range order {
		i := strings.Index(text[at:], marker)
		if i < 0 {
			t.Fatalf("log missing %q after offset %d:\n%s", marker, at, text)
		}
		at += i + len(marker)
	}
}
