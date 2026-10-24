package agentio

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	RefusalLimit    = "limit"
	RefusalOutage   = "outage"
	RefusalThrottle = "throttle"
)

type Refusal struct {
	Kind  string
	Reset *float64
	Text  string
}

var (
	claudeLimitRE    = regexp.MustCompile(`(?i)(rate_limit_error|\b429\b)`)
	claudeOutageRE   = regexp.MustCompile(`(?i)(overloaded_error|\b529\b|\b5\d\d\b|api_error)`)
	codexLimitRE     = regexp.MustCompile(`(?i)(usage limit|usage_limit_reached|\b429\b)`)
	codexOutageRE    = regexp.MustCompile(`(?i)(stream disconnected|\b5\d\d\b|overloaded)`)
	piOutageRE       = regexp.MustCompile(`(?i)(\b5\d\d\b|fetch failed|ECONNREFUSED|connection error|terminated)`)
	piHTTPErrorRE    = regexp.MustCompile(`(?s)^\s*(\d{3})\s*:\s*(.*)$`)
	piResetRE        = regexp.MustCompile(`(?i)\breset\s+at\s+(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)\b`)
	usageResetTextRE = regexp.MustCompile(`(?:[A-Z][a-z]{2} \d{1,2} at )?\d{1,2}(?::\d\d)?[ap]m \([^)]+\)`)
)

// Classify identifies provider refusals only when the agent reported an error.
func Classify(agent map[string]any, run AgentRun, now time.Time) *Refusal {
	failure := FailureText(agent, run)
	if failure == nil {
		return nil
	}
	text := *failure
	data := AgentOutput(agent, run.Stdout)

	switch str(agent["stream"]) {
	case "claude":
		if rejected, reset := claudeRejected(run.Stdout); rejected {
			return &Refusal{Kind: RefusalLimit, Reset: reset, Text: text}
		}
		if claudeLimitRE.MatchString(text) {
			return &Refusal{Kind: RefusalLimit, Reset: usageResetInText(text, now), Text: text}
		}
		if claudeOutageRE.MatchString(text) {
			return &Refusal{Kind: RefusalOutage, Text: text}
		}
	case "codex":
		if limited, reset := codexLimit(data); limited {
			return &Refusal{Kind: RefusalLimit, Reset: reset, Text: text}
		}
		if codexLimitRE.MatchString(text) {
			return &Refusal{Kind: RefusalLimit, Reset: usageResetInText(text, now), Text: text}
		}
		if codexOutageRE.MatchString(text) {
			return &Refusal{Kind: RefusalOutage, Text: text}
		}
	case "pi":
		errorText := str(JSONPath(data, "json:error"))
		status, body := piHTTPError(errorText)
		code, message := piErrorBody(body)
		switch code {
		case "1308", "1310":
			return &Refusal{Kind: RefusalLimit, Reset: piReset(errorText), Text: text}
		case "1302":
			return &Refusal{Kind: RefusalThrottle, Text: text}
		case "1305":
			return &Refusal{Kind: RefusalOutage, Text: text}
		}
		if status == 429 {
			return &Refusal{Kind: RefusalLimit, Reset: usageResetInText(errorText, now), Text: text}
		}
		if (status >= 500 && status <= 599) || piOutageRE.MatchString(errorText) || piOutageRE.MatchString(message) {
			return &Refusal{Kind: RefusalOutage, Text: text}
		}
	}

	if limit := LimitText(agent, run); limit != nil {
		return &Refusal{Kind: RefusalLimit, Reset: usageResetInText(*limit, now), Text: *limit}
	}
	return nil
}

func claudeRejected(stdout string) (bool, *float64) {
	rejected := false
	var reset *float64
	for _, line := range strings.Split(stdout, "\n") {
		obj := ParseLine(line)
		info := table(obj["rate_limit_info"])
		if obj["type"] == "rate_limit_event" && info["status"] == "rejected" {
			rejected = true
			reset = Epoch(info["resetsAt"])
		}
	}
	return rejected, reset
}

func codexLimit(data any) (bool, *float64) {
	threadID, _ := JSONPath(data, "json:thread_id").(string)
	limits := CodexLimits(threadID)
	if limits == nil {
		return false, nil
	}
	var windows, full []map[string]any
	for _, key := range []string{"primary", "secondary"} {
		if window := table(limits[key]); window != nil {
			windows = append(windows, window)
			if used, ok := number(window["used_percent"]); ok && used >= 100 {
				full = append(full, window)
			}
		}
	}
	rateLimited := truth(limits["rate_limit_reached_type"])
	if rateLimited && len(full) == 0 {
		full = windows
	}
	if len(full) == 0 {
		return false, nil
	}
	var reset *float64
	for _, window := range full {
		if at := Epoch(window["resets_at"]); at != nil && (reset == nil || *at > *reset) {
			reset = at
		}
	}
	return true, reset
}

func piHTTPError(text string) (int, string) {
	m := piHTTPErrorRE.FindStringSubmatch(text)
	if m == nil {
		return 0, text
	}
	status, _ := strconv.Atoi(m[1])
	return status, m[2]
}

func piErrorBody(body string) (string, string) {
	var value map[string]any
	if json.Unmarshal([]byte(body), &value) != nil {
		return "", body
	}
	if nested := table(value["error"]); nested != nil {
		value = nested
	}
	switch code := value["code"].(type) {
	case string:
		return code, str(value["message"])
	case float64:
		return strconv.Itoa(int(code)), str(value["message"])
	default:
		return "", str(value["message"])
	}
}

func piReset(text string) *float64 {
	m := piResetRE.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	zone := time.FixedZone("UTC+8", 8*60*60)
	at, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], zone)
	if err != nil {
		return nil
	}
	n := float64(at.Unix())
	return &n
}

func usageResetInText(text string, now time.Time) *float64 {
	for _, match := range usageResetTextRE.FindAllString(text, -1) {
		if reset := UsageReset(match, now); reset != nil {
			return reset
		}
	}
	return nil
}
