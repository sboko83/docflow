package agentio

import (
	"sync"
	"time"
)

// Liveness tracks what the agent's stdout says about progress: the time of
// the last line, the number of turns, and the tools in flight. The stream
// name ("claude", "codex" or "pi" from the agent profile) decides which
// event shapes count as a turn and as a tool start or end. A nil stream or
// an unknown one still tracks line times; turns and tools stay empty.
type Liveness struct {
	mu     sync.Mutex
	stream string
	last   time.Time
	turnAt time.Time
	lines  int
	turns  int
	// lastMessage is the id of the last Claude assistant message counted: one
	// message arrives as several events, one per content block.
	lastMessage string
	tools       map[string]time.Time
}

// LivenessState is an immutable snapshot of a Liveness.
type LivenessState struct {
	LastLine time.Time
	LastTurn time.Time
	// Lines counts the stdout lines the agent has produced. Zero means the
	// call has seen no event at all: LastLine then holds the call's start, so
	// a reader telling an active agent from a hung one checks Lines first.
	Lines      int
	Turns      int
	Tools      int
	OldestTool time.Time
}

// NewLiveness starts a tracker whose last line is counted from now, so an
// agent that prints nothing at all stalls after the idle limit too.
func NewLiveness(stream string) *Liveness {
	now := time.Now()
	return &Liveness{stream: stream, last: now, turnAt: now, tools: map[string]time.Time{}}
}

func (l *Liveness) beginTurn() {
	l.turns++
	l.turnAt = l.last
}

// Observe consumes one stdout line as it arrives. Every line refreshes the
// last event time; a parsed JSON event updates turns and in-flight tools
// per the stream's event shapes.
func (l *Liveness) Observe(line string) {
	if l == nil {
		return
	}
	obj := ParseLine(line)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = time.Now()
	l.lines++
	if obj == nil {
		return
	}
	switch l.stream {
	case "claude":
		l.observeClaude(obj)
	case "codex":
		l.observeCodex(obj)
	case "pi":
		l.observePi(obj)
	}
}

// State returns a snapshot of the tracker. A tool in flight keeps the call
// alive no matter how long the stream stays silent; OldestTool is the start
// time of the oldest one.
func (l *Liveness) State() LivenessState {
	if l == nil {
		return LivenessState{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := LivenessState{LastLine: l.last, LastTurn: l.turnAt, Lines: l.lines, Turns: l.turns, Tools: len(l.tools)}
	for _, start := range l.tools {
		if state.OldestTool.IsZero() || start.Before(state.OldestTool) {
			state.OldestTool = start
		}
	}
	return state
}

// contentBlocks extracts message.content from a stream event.
func contentBlocks(obj map[string]any) []map[string]any {
	values, _ := table(obj["message"])["content"].([]any)
	blocks := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if block := table(value); block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// observeClaude handles Claude stream-json: an assistant event is one model
// turn (events sharing a message id count once) and marks a tool start with a tool_use block; the following user
// event reports the matching tool_result. StructuredOutput is the result
// channel, not a tool.
func (l *Liveness) observeClaude(obj map[string]any) {
	switch obj["type"] {
	case "assistant":
		if id := str(table(obj["message"])["id"]); id == "" || id != l.lastMessage {
			l.beginTurn()
			l.lastMessage = id
		}
		for _, block := range contentBlocks(obj) {
			if block["type"] != "tool_use" || block["name"] == "StructuredOutput" {
				continue
			}
			if id := str(block["id"]); id != "" {
				l.tools[id] = time.Now()
			}
		}
	case "user":
		for _, block := range contentBlocks(obj) {
			if block["type"] == "tool_result" {
				delete(l.tools, str(block["tool_use_id"]))
			}
		}
	case "result":
		// The run is over; nothing can still be in flight.
		clear(l.tools)
	}
}

// observeCodex handles Codex: turn.started marks one model turn;
// item.started/item.completed bound a tool item by id. Every non-silent
// item is a tool in flight while started; a silent one (agent_message,
// reasoning, error) is model output, not a tool.
func (l *Liveness) observeCodex(obj map[string]any) {
	kind := str(obj["type"])
	if kind == "turn.started" {
		l.beginTurn()
		return
	}
	if kind != "item.started" && kind != "item.completed" {
		return
	}
	item := table(obj["item"])
	if item == nil {
		return
	}
	what := str(item["type"])
	for _, silent := range CodexSilent {
		if what == silent {
			return
		}
	}
	id := str(item["id"])
	if id == "" {
		return
	}
	if kind == "item.started" {
		l.tools[id] = time.Now()
	} else {
		delete(l.tools, id)
	}
}

// observePi handles Pi: tool_execution_start/end pair by toolCallId; a
// finished assistant message is one turn. toolResult and system messages
// are not turns.
func (l *Liveness) observePi(obj map[string]any) {
	switch str(obj["type"]) {
	case "tool_execution_start":
		if id := str(obj["toolCallId"]); id != "" {
			l.tools[id] = time.Now()
		}
	case "tool_execution_end":
		delete(l.tools, str(obj["toolCallId"]))
	case "message_end":
		if table(obj["message"])["role"] == "assistant" {
			l.beginTurn()
		}
	}
}
