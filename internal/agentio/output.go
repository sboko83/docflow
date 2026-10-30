// Package agentio runs coding agents and decodes their output and usage.
package agentio

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var JSONBlockRE = regexp.MustCompile("(?s)```json[ \\t]*\\n(.*?)\\n[ \\t]*```")

var CodexSilent = []string{"agent_message", "reasoning", "error"}

func ParseJSON(text string) any {
	var value any
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &value) == nil {
		return value
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if obj := ParseLine(lines[i]); obj != nil {
			return obj
		}
	}
	return nil
}

func ParseLine(line string) map[string]any {
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return nil
	}
	return obj
}

func table(value any) map[string]any { m, _ := value.(map[string]any); return m }
func str(value any) string           { s, _ := value.(string); return s }
func number(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	}
	return 0, false
}
func truth(value any) bool {
	if n, ok := number(value); ok {
		return n != 0
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func AddNumbers(total, part map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range total {
		out[k] = v
	}
	for k, v := range part {
		if n, ok := number(v); ok {
			prev, _ := number(out[k])
			out[k] = prev + n
		}
	}
	return out
}

func PiOutput(stdout string) map[string]any {
	out := map[string]any{"session": nil, "text": nil, "usage": nil, "cost": nil, "is_error": true, "error": nil}
	aborted := false
	for _, line := range strings.Split(stdout, "\n") {
		obj := ParseLine(line)
		message := table(obj["message"])
		switch obj["type"] {
		case "session":
			out["session"] = obj["id"]
		case "message_end":
			if message == nil || message["role"] != "assistant" {
				continue
			}
			var text strings.Builder
			content, _ := message["content"].([]any)
			for _, item := range content {
				b := table(item)
				if b["type"] == "text" {
					text.WriteString(str(b["text"]))
				}
			}
			out["text"] = text.String()
			if usage := table(message["usage"]); usage != nil {
				out["usage"] = AddNumbers(table(out["usage"]), usage)
				if cost := table(usage["cost"]); cost != nil {
					out["cost"] = AddNumbers(table(out["cost"]), cost)
				}
			}
			failed := message["stopReason"] == "error"
			out["error"] = nil
			if e, ok := message["errorMessage"].(string); failed && ok {
				out["error"] = e
			}
			out["is_error"] = failed
		case "agent_settled":
			if truth(obj["aborted"]) {
				aborted = true
			}
		}
	}
	out["is_error"] = truth(out["is_error"]) || aborted
	return out
}

// OpencodeOutput folds the JSON Lines of `opencode run --format json` into
// the profile's json: paths. Text parts of one assistant message share
// part.messageID and concatenate; a new messageID starts the text over, so
// the last message wins. step_finish parts carry cost and tokens to sum, and
// a provider failure arrives as an error event — the stream decides
// is_error, never the exit code.
func OpencodeOutput(stdout string) map[string]any {
	out := map[string]any{"session": nil, "text": nil, "usage": nil, "cost": nil, "is_error": false, "error": nil}
	message := ""
	var text strings.Builder
	for _, line := range strings.Split(stdout, "\n") {
		obj := ParseLine(line)
		if obj == nil {
			continue
		}
		if id := str(obj["sessionID"]); id != "" {
			out["session"] = id
		}
		part := table(obj["part"])
		switch obj["type"] {
		case "text":
			if id := str(part["messageID"]); id != message {
				message, text = id, strings.Builder{}
			}
			text.WriteString(str(part["text"]))
			out["text"] = text.String()
		case "step_finish":
			tokens := table(part["tokens"])
			cache := table(tokens["cache"])
			out["usage"] = AddNumbers(table(out["usage"]), map[string]any{
				"input": tokens["input"], "output": tokens["output"],
				"cache_read": cache["read"], "cache_write": cache["write"],
			})
			cost, _ := number(part["cost"])
			previous, _ := number(out["cost"])
			out["cost"] = previous + cost
		case "error":
			out["is_error"] = true
			out["error"] = table(obj["error"])["message"]
			if out["error"] == nil {
				out["error"] = obj["error"]
			}
		}
	}
	return out
}

func AgentOutput(agent map[string]any, stdout string) any {
	if agent["stream"] == "pi" {
		return PiOutput(stdout)
	}
	if agent["stream"] == "claude" {
		// Claude Code 2.1+ writes system events (task_summary) after the result.
		lines := strings.Split(stdout, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if obj := ParseLine(lines[i]); obj != nil && obj["type"] == "result" {
				return obj
			}
		}
	}
	if agent["stream"] == "opencode" {
		return OpencodeOutput(stdout)
	}
	if agent["stream"] != "codex" {
		return ParseJSON(stdout)
	}
	out := map[string]any{"thread_id": nil, "text": nil, "usage": nil, "is_error": false, "error": nil}
	for _, line := range strings.Split(stdout, "\n") {
		obj := ParseLine(line)
		item := table(obj["item"])
		switch obj["type"] {
		case "thread.started":
			out["thread_id"] = obj["thread_id"]
		case "item.completed":
			if item["type"] == "agent_message" {
				out["text"] = item["text"]
			}
		case "turn.completed":
			out["usage"] = obj["usage"]
		case "turn.failed", "error":
			e := obj
			if obj["type"] == "turn.failed" {
				e = table(obj["error"])
			}
			out["is_error"] = true
			out["error"] = e["message"]
		}
	}
	return out
}

func Clip(text string, limit int) string {
	text = strings.SplitN(strings.TrimSpace(text), "\n", 2)[0]
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

// QuotaWindow preserves a nullable reset value, including millisecond epochs.
type QuotaWindow struct {
	Label string
	Used  float64
	Reset any
}

// Quota keeps the insertion order of Python's dict: it is also the order in
// which the runner prints the windows. Updating a label keeps its position.
type Quota []QuotaWindow

func (q *Quota) Set(label string, used float64, reset any) {
	for i := range *q {
		if (*q)[i].Label == label {
			(*q)[i] = QuotaWindow{label, used, reset}
			return
		}
	}
	*q = append(*q, QuotaWindow{label, used, reset})
}

// Event carries the original JSON for fields whose object order is visible.
type Event struct {
	Data map[string]any
	Raw  string
}

func objectKeys(raw []byte) []string {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil
	}
	var keys []string
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil
		}
		keys = append(keys, key.(string))
	}
	return keys
}

func quotaKeys(raw string, windows map[string]any) []string {
	var root, info map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &root)
	_ = json.Unmarshal(root["rate_limit_info"], &info)
	if keys := objectKeys(info["unifiedWindows"]); keys != nil {
		return keys
	}
	// Callers supplying only a Go map have no source order to preserve.
	keys := make([]string, 0, len(windows))
	for key := range windows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func StreamEvent(agent map[string]any, value any) map[string]any {
	obj := table(value)
	raw := ""
	if event, ok := value.(Event); ok {
		obj, raw = event.Data, event.Raw
	}
	if obj == nil {
		return nil
	}
	kind := obj["type"]
	if agent["stream"] == "codex" {
		item := table(obj["item"])
		if item == nil {
			return nil
		}
		what := str(item["type"])
		if kind == "item.completed" && what == "agent_message" {
			if text := str(item["text"]); text != "" {
				return map[string]any{"assistant_text": text}
			}
			return nil
		}
		if kind != "item.started" {
			return nil
		}
		activity := ""
		switch what {
		case "command_execution":
			activity = strings.TrimRight("Bash "+Clip(str(item["command"]), 60), " ")
		case "file_change":
			changes, _ := item["changes"].([]any)
			path := ""
			if len(changes) > 0 {
				path = str(table(changes[0])["path"])
			}
			activity = strings.TrimRight("Edit "+Clip(path, 60), " ")
		default:
			if _, ok := item["type"].(string); !ok {
				return nil
			}
			for _, silent := range CodexSilent {
				if what == silent {
					return nil
				}
			}
			activity = what
		}
		return map[string]any{"activity": activity}
	}
	if agent["stream"] == "pi" {
		if kind != "message_end" {
			return nil
		}
		message := table(obj["message"])
		if message == nil || message["role"] != "assistant" {
			return nil
		}
		content, _ := message["content"].([]any)
		var text strings.Builder
		for _, value := range content {
			item := table(value)
			if item["type"] == "text" {
				text.WriteString(str(item["text"]))
			}
		}
		if text.Len() == 0 {
			return nil
		}
		return map[string]any{"assistant_text": text.String()}
	}
	if agent["stream"] != "claude" {
		return nil
	}
	if kind == "rate_limit_event" {
		windows := table(table(obj["rate_limit_info"])["unifiedWindows"])
		quota := Quota{}
		for _, key := range quotaKeys(raw, windows) {
			w := table(windows[key])
			if n, ok := number(w["utilization"]); ok {
				quota.Set(key, n, w["resetsAt"])
			}
		}
		if len(quota) > 0 {
			return map[string]any{"quota": quota}
		}
		return nil
	}
	if kind == "assistant" {
		content, _ := table(obj["message"])["content"].([]any)
		var text strings.Builder
		activity := ""
		for _, v := range content {
			item := table(v)
			if item["type"] == "text" {
				text.WriteString(str(item["text"]))
				continue
			}
			if item["type"] != "tool_use" || item["name"] == "StructuredOutput" || activity != "" {
				continue
			}
			args := table(item["input"])
			arg := ""
			for _, key := range []string{"command", "file_path", "pattern", "path", "url"} {
				if truth(args[key]) {
					arg = fmt.Sprint(args[key])
					break
				}
			}
			name := "None"
			if item["name"] != nil {
				name = fmt.Sprint(item["name"])
			}
			activity = strings.TrimRight(name+" "+Clip(arg, 60), " ")
		}
		event := map[string]any{}
		if text.Len() > 0 {
			event["assistant_text"] = text.String()
		}
		if activity != "" {
			event["activity"] = activity
		}
		if len(event) > 0 {
			return event
		}
	}
	return nil
}

func JSONPath(data any, spec string) any {
	if !strings.HasPrefix(spec, "json:") {
		return nil
	}
	cur := data
	for _, key := range strings.Split(strings.TrimPrefix(spec, "json:"), ".") {
		cur = table(cur)[key]
	}
	return cur
}

func AgentText(agent map[string]any, data any, stdout string) string {
	if text, ok := JSONPath(data, str(agent["text"])).(string); ok {
		return text
	}
	if text, ok := JSONPath(data, "json:assistant_text").(string); ok {
		return text
	}
	return stdout
}

// AssistantMessages extracts assistant text messages from a line-oriented
// agent stream using the same event and text decoders as live output.
func AssistantMessages(agent map[string]any, stdout string) []string {
	var messages []string
	for _, line := range strings.Split(stdout, "\n") {
		obj := ParseLine(line)
		if obj == nil {
			continue
		}
		streamed := StreamEvent(agent, Event{Data: obj, Raw: line})
		if streamed == nil {
			continue
		}
		text := strings.TrimSpace(AgentText(agent, streamed, ""))
		if text != "" {
			messages = append(messages, text)
		}
	}
	return messages
}

func ExtractResult(agent map[string]any, stdout string) map[string]any {
	return dropBlankItems(extractResult(agent, stdout))
}

// dropBlankItems removes blank strings from the result's list fields: a
// stray "" carries nothing and must not cost a retry.
func dropBlankItems(result map[string]any) map[string]any {
	for _, key := range []string{"handoff", "decisions", "unverified"} {
		items, ok := result[key].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(items))
		for _, v := range items {
			if s, isString := v.(string); isString && strings.TrimSpace(s) == "" {
				continue
			}
			kept = append(kept, v)
		}
		result[key] = kept
	}
	return result
}

func extractResult(agent map[string]any, stdout string) map[string]any {
	data := AgentOutput(agent, stdout)
	value := JSONPath(data, str(agent["result"]))
	if text, ok := value.(string); ok {
		value = ParseJSON(text)
	}
	if obj := table(value); obj != nil {
		return obj
	}
	text := AgentText(agent, data, stdout)
	blocks := JSONBlockRE.FindAllStringSubmatch(text, -1)
	for i := len(blocks) - 1; i >= 0; i-- {
		if obj := table(ParseJSON(blocks[i][1])); obj != nil {
			return obj
		}
	}
	return TrailingResult(text)
}

func TrailingResult(text string) map[string]any {
	text = strings.TrimRightFunc(text, func(r rune) bool { return strings.TrimSpace(string(r)) == "" })
	if !strings.HasSuffix(text, "}") {
		return nil
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(text[i:]), &obj) == nil {
			if _, ok := obj["status"]; ok {
				return obj
			}
		}
	}
	return nil
}

func Strings(value any, emptyOK bool) bool {
	items, ok := value.([]any)
	if values, typed := value.([]string); typed {
		items = make([]any, len(values))
		for i, v := range values {
			items[i] = v
		}
		ok = true
	}
	if !ok || (!emptyOK && len(items) == 0) {
		return false
	}
	for _, v := range items {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return false
		}
	}
	return true
}

// ResultProblem returns an empty string for a usable runner result.
func ResultProblem(result map[string]any) string {
	if result == nil {
		return "no result JSON in the reply"
	}
	status := result["status"]
	blocker, ok := result["blocker"]
	if !ok {
		blocker = ""
		if status == "blocked" {
			blocker = "human"
		}
	}
	if status == "done" {
		if blocker != "" {
			return `result JSON: blocker must be "" when done`
		}
		if !Strings(result["handoff"], false) {
			return "result JSON: handoff must be a non-empty list of strings"
		}
		for _, key := range []string{"decisions", "unverified"} {
			value, ok := result[key]
			if !ok {
				value = []any{}
			}
			if !Strings(value, true) {
				return "result JSON: " + key + " must be a list of strings"
			}
		}
		return ""
	}
	if status == "blocked" {
		if blocker != "plan" && blocker != "human" {
			return `result JSON: blocker must be "plan" or "human"`
		}
		if strings.TrimSpace(str(result["reason"])) == "" {
			return "result JSON: a blocked result needs a reason"
		}
		return ""
	}
	return `result JSON: status must be "done" or "blocked"`
}
