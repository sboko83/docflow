package agentio

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// ActivityInlineLimit is how much of a tool result or message an Activity
// carries inline. Longer payloads stay in the call log and are read through
// ActivityRef, so a slow reader never loads a whole run into memory.
const ActivityInlineLimit = 4096

// ActivityKind classifies one normalized observation of an agent stream.
type ActivityKind string

const (
	ActivityKindMessage   ActivityKind = "message"
	ActivityKindReasoning ActivityKind = "reasoning"
	ActivityKindTool      ActivityKind = "tool"
	ActivityKindUsage     ActivityKind = "usage"
	ActivityKindStderr    ActivityKind = "stderr"
	ActivityKindUnknown   ActivityKind = "unknown"
)

// ActivityState tells a running tool from a confirmed result: only a result
// event turns Running into Succeeded, Failed or Cancelled. Anything the stream
// does not confirm stays Unknown, never a success.
type ActivityState string

const (
	ActivityStateRunning   ActivityState = "running"
	ActivityStateSucceeded ActivityState = "succeeded"
	ActivityStateFailed    ActivityState = "failed"
	ActivityStateCancelled ActivityState = "cancelled"
	ActivityStateUnknown   ActivityState = "unknown"
)

// ActivityRef locates the original fragment in a call log. Offset is absolute
// within the stream's file: the call log for stdout, its stderr sidecar for
// stderr. Length is the fragment's byte length, -1 when the stream does not
// know it.
type ActivityRef struct {
	Stream string `json:"stream"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
}

// FileChange is one file an agent tool changed, as the stream reports it.
type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// ActivityUsage is the usage one stream event reports. A missing value stays
// zero and the pointer fields stay nil: the UI shows them as unknown, never as
// a confirmed zero.
type ActivityUsage struct {
	Input      int      `json:"input,omitempty"`
	Output     int      `json:"output,omitempty"`
	CacheRead  int      `json:"cache_read,omitempty"`
	CacheWrite int      `json:"cache_write,omitempty"`
	Reasoning  int      `json:"reasoning,omitempty"`
	Total      int      `json:"total,omitempty"`
	Cost       *float64 `json:"cost,omitempty"`
}

// Activity is one inspectable observation linked to its call and its original
// bytes. Seq is assigned by the parser per call; the runner renumbers it across
// the run. Text, Result and Raw are capped at ActivityInlineLimit, with Ref
// holding the complete original fragment.
type Activity struct {
	Seq     int64          `json:"seq"`
	Run     string         `json:"run,omitempty"`
	Unit    string         `json:"unit,omitempty"`
	Attempt int            `json:"attempt,omitempty"`
	Agent   string         `json:"agent,omitempty"`
	Model   string         `json:"model,omitempty"`
	Effort  string         `json:"effort,omitempty"`
	Kind    ActivityKind   `json:"kind"`
	State   ActivityState  `json:"state"`
	Delta   bool           `json:"delta,omitempty"`
	Role    string         `json:"role,omitempty"`
	Message string         `json:"message_id,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	ToolID  string         `json:"tool_id,omitempty"`
	Command string         `json:"command,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Text    string         `json:"text,omitempty"`
	Result  string         `json:"result,omitempty"`
	Files   []FileChange   `json:"files,omitempty"`
	Usage   *ActivityUsage `json:"usage,omitempty"`
	Raw     string         `json:"raw,omitempty"`
	Ref     ActivityRef    `json:"ref"`
}

type messageState struct {
	emitted string
}

type toolState struct {
	name    string
	command string
	input   map[string]any
	ref     ActivityRef
}

// ActivityParser normalizes the JSON lines of one agent call. It is safe for
// the concurrent stdout and stderr writers of a process; the runner may share
// one parser per call.
type ActivityParser struct {
	mu          sync.Mutex
	stream      string
	seq         int64
	messages    map[string]*messageState
	tools       map[string]*toolState
	closed      map[string]bool
	autoTool    int
	aborted     bool
	lastMessage string
}

// NewActivityParser returns a parser for the agent profile's stream.
func NewActivityParser(agent map[string]any) *ActivityParser {
	stream := ""
	if agent != nil {
		stream = str(agent["stream"])
	}
	return &ActivityParser{stream: stream, messages: map[string]*messageState{}, tools: map[string]*toolState{}}
}

// Feed consumes one complete or partial line. text is the line after UTF-8
// replacement; ref locates the original bytes. An unparsable stdout line and
// every stderr line are kept as raw text; an unknown event type keeps its raw
// fragment too, so a new agent feature still shows up in the history.
func (p *ActivityParser) Feed(stream, text string, ref ActivityRef) []Activity {
	p.mu.Lock()
	defer p.mu.Unlock()
	if stream == "" {
		stream = "stdout"
	}
	if stream == "stderr" {
		return p.next(Activity{Kind: ActivityKindStderr, State: ActivityStateUnknown, Text: clipActivity(text), Raw: clipActivity(text), Ref: ref})
	}
	obj := ParseLine(text)
	if obj == nil {
		return p.next(Activity{Kind: ActivityKindUnknown, State: ActivityStateUnknown, Raw: clipActivity(text), Ref: ref})
	}
	var out []Activity
	switch p.stream {
	case "codex":
		out = p.codex(obj, ref, text)
	case "claude":
		out = p.claude(obj, ref, text)
	case "pi":
		out = p.pi(obj, ref, text)
	case "opencode":
		out = p.opencode(obj, ref, text)
	default:
		out = []Activity{{Kind: ActivityKindUnknown, State: ActivityStateUnknown, Raw: clipActivity(text), Ref: ref}}
	}
	out = p.next(out...)
	return out
}

// Finalize closes tools left in flight when the call ends. A killed call
// cancels them; a call that exited on its own leaves their outcome unknown,
// because no result event confirmed it.
func (p *ActivityParser) Finalize(state ActivityState) []Activity {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.tools))
	for id := range p.tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Activity, 0, len(ids))
	for _, id := range ids {
		tool := p.tools[id]
		out = append(out, Activity{
			Kind: ActivityKindTool, State: state, Tool: tool.name, ToolID: id,
			Command: clipActivity(tool.command), Input: tool.input, Ref: tool.ref,
		})
	}
	p.tools = map[string]*toolState{}
	return p.next(out...)
}

// Aborted reports whether the stream announced that the agent aborted.
func (p *ActivityParser) Aborted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aborted
}

func (p *ActivityParser) next(list ...Activity) []Activity {
	for i := range list {
		p.seq++
		list[i].Seq = p.seq
	}
	return list
}

func (p *ActivityParser) message(key, role, text string, final bool, ref ActivityRef) []Activity {
	if text == "" {
		return nil
	}
	state := p.messages[key]
	if state == nil {
		state = &messageState{}
		p.messages[key] = state
	}
	if !final {
		state.emitted += text
		return []Activity{{Kind: kindForRole(role), State: ActivityStateRunning, Delta: true, Role: role, Message: key, Text: clipActivity(text), Ref: ref}}
	}
	text = strings.TrimPrefix(text, state.emitted)
	delete(p.messages, key)
	if text == "" {
		// The deltas already carried the whole message: emitting it again
		// would duplicate it.
		return nil
	}
	return []Activity{{Kind: kindForRole(role), State: ActivityStateSucceeded, Role: role, Message: key, Text: clipActivity(text), Ref: ref}}
}

func kindForRole(role string) ActivityKind {
	if role == "reasoning" || role == "thinking" {
		return ActivityKindReasoning
	}
	return ActivityKindMessage
}

func (p *ActivityParser) toolID(name, id string) string {
	if id != "" {
		return id
	}
	p.autoTool++
	return fmt.Sprintf("%s#%d", name, p.autoTool)
}

func (p *ActivityParser) toolStart(name, id, command string, input map[string]any, ref ActivityRef) []Activity {
	id = p.toolID(name, id)
	input = clipInput(input)
	if seen := p.tools[id]; seen != nil {
		// The same call can appear in a message and in a tool event; report
		// it once.
		if command != "" {
			seen.command = command
		}
		if input != nil {
			seen.input = input
		}
		return nil
	}
	p.tools[id] = &toolState{name: name, command: command, input: input, ref: ref}
	return []Activity{{Kind: ActivityKindTool, State: ActivityStateRunning, Tool: name, ToolID: id, Command: clipActivity(command), Input: input, Ref: ref}}
}

func (p *ActivityParser) toolEnd(name, id, result string, state ActivityState, files []FileChange, ref ActivityRef) []Activity {
	id = p.toolID(name, id)
	command, input := "", map[string]any(nil)
	// Pi reports one call twice: tool_execution_end carries the result and the
	// next message_end repeats it as a toolResult. History shows the call once.
	if p.closed[id] {
		return nil
	}
	if seen := p.tools[id]; seen != nil {
		if name == "" {
			name = seen.name
		}
		command, input = seen.command, seen.input
		delete(p.tools, id)
		if p.closed == nil {
			p.closed = map[string]bool{}
		}
		p.closed[id] = true
	}
	return []Activity{{
		Kind: ActivityKindTool, State: state, Tool: name, ToolID: id,
		Command: clipActivity(command), Input: clipInput(input), Result: clipActivity(result), Files: files, Ref: ref,
	}}
}

// clipInput keeps a tool's arguments within ActivityInlineLimit bytes once
// serialized. Strings are cut first; a payload that still does not fit is
// replaced by a size marker, the complete arguments stay in the call log at
// Ref. A clipped map is already inside the limit, so applying this again to
// the copy toolEnd takes from toolStart changes nothing.
func clipInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return input
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) <= ActivityInlineLimit {
		return input
	}
	size := len(raw)
	if count := countStrings(input); count > 0 {
		// Leave a margin for keys, braces and quotes; with more values than
		// the limit allows, per is zero and the marker below takes over.
		per := (ActivityInlineLimit - 256) / count
		if per > 0 {
			if clipped, ok := clipStrings(input, per).(map[string]any); ok {
				if raw, err := json.Marshal(clipped); err == nil && len(raw) <= ActivityInlineLimit {
					return clipped
				}
			}
		}
	}
	return map[string]any{"truncated": true, "bytes": size}
}

// countStrings counts the string values of a decoded JSON tree.
func countStrings(value any) int {
	switch v := value.(type) {
	case string:
		return 1
	case map[string]any:
		n := 0
		for _, item := range v {
			n += countStrings(item)
		}
		return n
	case []any:
		n := 0
		for _, item := range v {
			n += countStrings(item)
		}
		return n
	}
	return 0
}

// clipStrings returns a copy of value with every string cut to limit runes.
func clipStrings(value any, limit int) any {
	switch v := value.(type) {
	case string:
		runes := []rune(v)
		if len(runes) <= limit {
			return v
		}
		if limit <= 1 {
			return "…"
		}
		return string(runes[:limit-1]) + "…"
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = clipStrings(item, limit)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = clipStrings(item, limit)
		}
		return out
	}
	return value
}

// clipActivity keeps the head of a payload and marks it as cut. The complete
// bytes stay at Ref.
func clipActivity(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	runes := []rune(text)
	if len(runes) <= ActivityInlineLimit {
		return text
	}
	return string(runes[:ActivityInlineLimit]) + "…"
}

func (p *ActivityParser) unknown(text string, ref ActivityRef) []Activity {
	return []Activity{{Kind: ActivityKindUnknown, State: ActivityStateUnknown, Raw: clipActivity(text), Ref: ref}}
}

func (p *ActivityParser) usage(value any, cost *float64, ref ActivityRef) []Activity {
	m := table(value)
	if m == nil && cost == nil {
		return nil
	}
	usage := usageFromMap(m, cost)
	if usage == nil {
		return nil
	}
	return []Activity{{Kind: ActivityKindUsage, State: ActivityStateSucceeded, Usage: usage, Ref: ref}}
}

func usageFromMap(m map[string]any, cost *float64) *ActivityUsage {
	u := &ActivityUsage{Cost: cost}
	found := false
	set := func(field *int, keys ...string) {
		for _, key := range keys {
			if n, ok := number(m[key]); ok {
				*field = int(n)
				found = true
				return
			}
		}
	}
	if m != nil {
		set(&u.Input, "input_tokens", "input")
		set(&u.Output, "output_tokens", "output")
		set(&u.CacheRead, "cache_read_input_tokens", "cached_input_tokens", "cacheRead", "cache_read")
		set(&u.CacheWrite, "cache_creation_input_tokens", "cacheWrite", "cache_write")
		set(&u.Reasoning, "reasoning")
		set(&u.Total, "totalTokens", "total_tokens", "total")
	}
	if !found && cost == nil {
		return nil
	}
	return u
}

func (p *ActivityParser) codex(obj map[string]any, ref ActivityRef, raw string) []Activity {
	switch str(obj["type"]) {
	case "turn.completed":
		return p.usage(obj["usage"], nil, ref)
	case "turn.failed", "error":
		return p.unknown(raw, ref)
	case "item.started", "item.updated", "item.completed":
		return p.codexItem(str(obj["type"]), table(obj["item"]), ref, raw)
	case "thread.started", "turn.started":
		return nil
	}
	return p.unknown(raw, ref)
}

func (p *ActivityParser) codexItem(kind string, item map[string]any, ref ActivityRef, raw string) []Activity {
	if item == nil {
		return p.unknown(raw, ref)
	}
	what, id := str(item["type"]), str(item["id"])
	done := kind == "item.completed"
	key := "codex:" + id
	if id == "" {
		key = "codex:" + what
	}
	switch what {
	case "agent_message":
		return p.message(key, "assistant", str(item["text"]), done, ref)
	case "reasoning":
		return p.message(key, "reasoning", itemText(item), done, ref)
	case "command_execution":
		if !done {
			return p.toolStart("Bash", id, str(item["command"]), map[string]any{"command": item["command"]}, ref)
		}
		state := ActivityStateSucceeded
		if n, ok := number(item["exit_code"]); ok && n != 0 {
			state = ActivityStateFailed
		}
		if str(item["status"]) == "failed" {
			state = ActivityStateFailed
		}
		return p.toolEnd("Bash", id, str(item["aggregated_output"]), state, nil, ref)
	case "file_change":
		files := fileChanges(item["changes"])
		if !done {
			return p.toolStart("Edit", id, "", map[string]any{"changes": item["changes"]}, ref)
		}
		state := ActivityStateSucceeded
		if str(item["status"]) == "failed" {
			state = ActivityStateFailed
		}
		return p.toolEnd("Edit", id, "", state, files, ref)
	case "mcp_tool_call":
		name := str(item["tool"])
		if name == "" {
			name = str(item["name"])
		}
		if server := str(item["server"]); server != "" {
			name = server + "." + name
		}
		if name == "" {
			name = what
		}
		if !done {
			return p.toolStart(name, id, "", table(item["arguments"]), ref)
		}
		state := ActivityStateSucceeded
		if str(item["status"]) == "failed" || truth(item["error"]) {
			state = ActivityStateFailed
		}
		result, _ := item["result"].(string)
		if result == "" && truth(item["error"]) {
			result = fmt.Sprint(item["error"])
		}
		return p.toolEnd(name, id, result, state, nil, ref)
	case "web_search":
		if !done {
			return p.toolStart("WebSearch", id, str(item["query"]), map[string]any{"query": item["query"]}, ref)
		}
		return p.toolEnd("WebSearch", id, itemText(item), ActivityStateSucceeded, nil, ref)
	}
	if done {
		return p.unknown(raw, ref)
	}
	return nil
}

func itemText(item map[string]any) string {
	var b strings.Builder
	for _, value := range listOf(item["summary"]) {
		b.WriteString(str(table(value)["text"]))
	}
	if b.Len() == 0 {
		for _, value := range listOf(item["content"]) {
			b.WriteString(str(table(value)["text"]))
		}
	}
	if b.Len() == 0 {
		b.WriteString(str(item["text"]))
	}
	return b.String()
}

func listOf(value any) []any {
	list, _ := value.([]any)
	return list
}

func fileChanges(value any) []FileChange {
	var files []FileChange
	for _, entry := range listOf(value) {
		change := table(entry)
		path := str(change["path"])
		if path == "" {
			continue
		}
		files = append(files, FileChange{Path: path, Kind: str(change["kind"])})
	}
	return files
}

func (p *ActivityParser) claude(obj map[string]any, ref ActivityRef, raw string) []Activity {
	switch str(obj["type"]) {
	case "assistant":
		message := table(obj["message"])
		if id := str(message["id"]); id != "" && p.lastMessage == "" {
			// A streaming delta can precede the message it belongs to: keep
			// the accumulated text so the finished message is not duplicated.
			for _, suffix := range []string{"", ":thinking"} {
				if state, ok := p.messages["claude:stream"+suffix]; ok {
					p.messages["claude:"+id+suffix] = state
					delete(p.messages, "claude:stream"+suffix)
				}
			}
		}
		p.lastMessage = str(message["id"])
		key := "claude:" + p.lastMessage
		var text, thinking strings.Builder
		var out []Activity
		for _, block := range contentBlocks(obj) {
			switch str(block["type"]) {
			case "text":
				text.WriteString(str(block["text"]))
			case "thinking":
				thinking.WriteString(str(block["thinking"]))
			case "tool_use":
				name := str(block["name"])
				if name == "StructuredOutput" {
					continue
				}
				out = append(out, p.toolStart(name, str(block["id"]), str(table(block["input"])["command"]), table(block["input"]), ref)...)
			}
		}
		out = append(out, p.message(key, "assistant", text.String(), true, ref)...)
		out = append(out, p.message(key+":thinking", "reasoning", thinking.String(), true, ref)...)
		return out
	case "user":
		var out []Activity
		for _, block := range contentBlocks(obj) {
			if str(block["type"]) != "tool_result" {
				continue
			}
			state := ActivityStateSucceeded
			if truth(block["is_error"]) {
				state = ActivityStateFailed
			}
			out = append(out, p.toolEnd("", str(block["tool_use_id"]), blockText(block["content"]), state, nil, ref)...)
		}
		return out
	case "result":
		var out []Activity
		var cost *float64
		if n, ok := number(obj["total_cost_usd"]); ok {
			cost = &n
		}
		out = append(out, p.usage(obj["usage"], cost, ref)...)
		if truth(obj["is_error"]) {
			out = append(out, Activity{Kind: ActivityKindUnknown, State: ActivityStateFailed, Text: clipActivity(str(obj["result"])), Ref: ref})
		}
		return out
	case "stream_event":
		return p.claudeDelta(table(obj["event"]), ref)
	case "system", "rate_limit_event":
		return nil
	}
	return p.unknown(raw, ref)
}

func (p *ActivityParser) claudeDelta(event map[string]any, ref ActivityRef) []Activity {
	if event == nil {
		return nil
	}
	delta := table(event["delta"])
	switch str(event["type"]) {
	case "content_block_delta":
		key := "claude:" + p.lastMessage
		if p.lastMessage == "" {
			key = "claude:stream"
		}
		switch str(delta["type"]) {
		case "text_delta":
			return p.message(key, "assistant", str(delta["text"]), false, ref)
		case "thinking_delta":
			return p.message(key+":thinking", "reasoning", str(delta["thinking"]), false, ref)
		}
	case "message_stop":
		return nil
	}
	return nil
}

func blockText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, entry := range v {
			b.WriteString(str(table(entry)["text"]))
		}
		return b.String()
	case map[string]any:
		// Pi wraps a tool result as {"content": [{type, text}, ...]}.
		if content, ok := v["content"]; ok {
			return blockText(content)
		}
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return fmt.Sprint(value)
}

func (p *ActivityParser) pi(obj map[string]any, ref ActivityRef, raw string) []Activity {
	switch str(obj["type"]) {
	case "message_end":
		return p.piMessage(table(obj["message"]), ref, raw)
	case "tool_execution_start":
		args := table(obj["args"])
		return p.toolStart(str(obj["toolName"]), str(obj["toolCallId"]), str(args["command"]), args, ref)
	case "tool_execution_end":
		state := ActivityStateSucceeded
		if truth(obj["isError"]) || truth(obj["is_error"]) {
			state = ActivityStateFailed
		}
		return p.toolEnd(str(obj["toolName"]), str(obj["toolCallId"]), blockText(obj["result"]), state, nil, ref)
	case "message_update":
		event := table(obj["assistantMessageEvent"])
		switch str(event["type"]) {
		case "text_delta":
			return p.message("pi:assistant", "assistant", str(event["delta"]), false, ref)
		case "thinking_delta":
			return p.message("pi:assistant:thinking", "reasoning", str(event["delta"]), false, ref)
		}
		return nil
	case "agent_settled":
		if truth(obj["aborted"]) {
			p.aborted = true
		}
		return nil
	case "session", "agent_start", "agent_end", "turn_start", "turn_end", "message_start", "queue_update", "compaction_start", "compaction_end":
		return nil
	}
	return p.unknown(raw, ref)
}

func (p *ActivityParser) piMessage(message map[string]any, ref ActivityRef, raw string) []Activity {
	if message == nil {
		return p.unknown(raw, ref)
	}
	switch str(message["role"]) {
	case "assistant":
		var text, thinking strings.Builder
		var out []Activity
		for _, block := range listOf(message["content"]) {
			item := table(block)
			switch str(item["type"]) {
			case "text":
				text.WriteString(str(item["text"]))
			case "thinking":
				thinking.WriteString(str(item["thinking"]))
			case "toolCall":
				out = append(out, p.toolStart(str(item["name"]), str(item["id"]), str(table(item["arguments"])["command"]), table(item["arguments"]), ref)...)
			}
		}
		out = append(out, p.message("pi:assistant", "assistant", text.String(), true, ref)...)
		out = append(out, p.message("pi:assistant:thinking", "reasoning", thinking.String(), true, ref)...)
		usage := table(message["usage"])
		var cost *float64
		if n, ok := number(table(usage["cost"])["total"]); ok {
			cost = &n
		}
		out = append(out, p.usage(usage, cost, ref)...)
		return out
	case "toolResult":
		state := ActivityStateSucceeded
		if truth(message["isError"]) {
			state = ActivityStateFailed
		}
		return p.toolEnd(str(message["toolName"]), str(message["toolCallId"]), blockText(message["content"]), state, nil, ref)
	}
	return nil
}

func (p *ActivityParser) opencode(obj map[string]any, ref ActivityRef, raw string) []Activity {
	part := table(obj["part"])
	switch str(obj["type"]) {
	case "text":
		return p.message("opencode:"+str(part["messageID"]), "assistant", str(part["text"]), true, ref)
	case "tool_use":
		name, id := str(part["tool"]), str(part["id"])
		state := table(part["state"])
		switch str(state["status"]) {
		case "running", "pending":
			return p.toolStart(name, id, str(table(state["input"])["command"]), table(state["input"]), ref)
		case "completed":
			return p.toolEnd(name, id, blockText(state["output"]), ActivityStateSucceeded, nil, ref)
		case "error":
			return p.toolEnd(name, id, blockText(state["error"]), ActivityStateFailed, nil, ref)
		}
		return nil
	case "step_finish":
		tokens := table(part["tokens"])
		cache := table(tokens["cache"])
		var cost *float64
		if n, ok := number(part["cost"]); ok {
			cost = &n
		}
		return p.usage(map[string]any{"input": tokens["input"], "output": tokens["output"], "cache_read": cache["read"], "cache_write": cache["write"]}, cost, ref)
	case "step_start":
		return nil
	case "error":
		message := str(table(obj["error"])["message"])
		return []Activity{{Kind: ActivityKindUnknown, State: ActivityStateFailed, Text: clipActivity(message), Raw: clipActivity(raw), Ref: ref}}
	}
	return p.unknown(raw, ref)
}

// StderrLog is the sidecar that holds a call's raw stderr while the call runs.
// The call log keeps its usual layout; the sidecar gives stderr stable byte
// offsets for paging before the call has finished.
func StderrLog(log string) string {
	return log + ".stderr"
}

// ReadLiveActivityPage returns at most limit bytes of the fragment ref locates,
// starting skip bytes into it, and its total length up to the current end of
// the file. It reads the file size at call time instead of ref.Length, so a
// call that is still running exposes the bytes appended after its start line.
// stdout reads the call log; stderr reads its sidecar.
func ReadLiveActivityPage(log string, ref ActivityRef, skip, limit int) (string, int, error) {
	path := log
	if ref.Stream == "stderr" {
		path = StderrLog(log)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	length := int(info.Size() - ref.Offset)
	if length < 0 {
		length = 0
	}
	return readActivityPageAt(path, ref.Offset, skip, limit, length)
}

// ReadActivityPage returns at most limit bytes of the fragment ref locates,
// starting skip bytes into it, and the fragment's total length. stdout reads
// the call log; stderr reads its sidecar.
func ReadActivityPage(log string, ref ActivityRef, skip, limit int) (string, int, error) {
	path := log
	if ref.Stream == "stderr" {
		path = StderrLog(log)
	}
	if skip < 0 {
		skip = 0
	}
	if limit <= 0 {
		limit = ActivityInlineLimit
	}
	length := ref.Length
	if length < 0 {
		length = limit
	}
	return readActivityPageAt(path, ref.Offset, skip, limit, length)
}

// readActivityPageAt reads one bounded window at offset+skip within a fragment
// of the given length. A short read is not an error: the file may grow or
// shrink between the size lookup and the read.
func readActivityPageAt(path string, offset int64, skip, limit, length int) (string, int, error) {
	if skip >= length {
		return "", length, nil
	}
	if rest := length - skip; limit > rest {
		limit = rest
	}
	file, err := os.Open(path)
	if err != nil {
		return "", length, err
	}
	defer file.Close()
	buf := make([]byte, limit)
	n, err := file.ReadAt(buf, offset+int64(skip))
	if err != nil && n == 0 {
		return "", length, err
	}
	return string(buf[:n]), length, nil
}
