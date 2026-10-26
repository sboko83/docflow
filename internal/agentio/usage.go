package agentio

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bsv-docflow/internal/cycle"
)

// FailureText is the output a provider refusal, a limit and a login check may
// be read from. A call the runner ended — the time cap or the idle watchdog
// killed the process — is not a provider failure: its output quotes the agent's
// own work (and the kill itself reads as "terminated"), while the route stayed
// healthy. Reading a refusal from it sends the run to a fallback for no reason.
func FailureText(agent map[string]any, run AgentRun) *string {
	if run.TimedOut || run.Stalled {
		return nil
	}
	data := AgentOutput(agent, run.Stdout)
	if JSONPath(data, str(agent["error"])) != true && run.Code == 0 {
		return nil
	}
	text := AgentText(agent, data, run.Stdout)
	if e, ok := JSONPath(data, "json:error").(string); ok {
		text += "\n" + e
	}
	if run.Code != 0 {
		text += "\n" + run.Stderr
	}
	return &text
}

func LimitText(agent map[string]any, run AgentRun) *string {
	pattern := str(agent["limit"])
	text := FailureText(agent, run)
	if pattern == "" || text == nil {
		return nil
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err == nil && re.MatchString(*text) {
		return text
	}
	return nil
}

func AuthReason(agent map[string]any, run AgentRun) string {
	pattern := str(agent["auth"])
	text := FailureText(agent, run)
	if pattern == "" || text == nil {
		return ""
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(*text, "\n") {
		if re.MatchString(line) {
			return Clip(line, 200)
		}
	}
	return ""
}

func FailureReason(agent map[string]any, run AgentRun) string {
	data := AgentOutput(agent, run.Stdout)
	for _, value := range []any{JSONPath(data, str(agent["text"])), JSONPath(data, "json:error"), run.Stderr} {
		if text, ok := value.(string); ok {
			for _, line := range strings.Split(text, "\n") {
				if strings.TrimSpace(line) != "" {
					return Clip(line, 200)
				}
			}
		}
	}
	return ""
}

// EpochRE matches digit runs; LimitUntil enforces the Python boundary and length.
var EpochRE = regexp.MustCompile(`[0-9]+`)

func LimitUntil(agent map[string]any, text string, now float64, reset *float64) float64 {
	if reset != nil && *reset != 0 && *reset > now {
		return *reset + 60
	}
	for _, m := range EpochRE.FindAllString(text, -1) {
		if len(m) != 10 && len(m) != 13 {
			continue
		}
		n, _ := strconv.ParseFloat(m, 64)
		if len(m) == 13 {
			n /= 1000
		}
		if n > now {
			return n + 60
		}
		break
	}
	wait, ok := number(agent["limit_wait"])
	if !ok {
		wait = LimitWait
	}
	return now + wait
}

type LimitProgress interface {
	Line(string, bool)
	Spin(string)
	Stop()
}

func WaitLimit(ctx context.Context, agent map[string]any, text string, progress LimitProgress, reset *float64) error {
	until := LimitUntil(agent, text, float64(time.Now().UnixNano())/1e9, reset)
	when := time.Unix(int64(until), 0).Local().Format("15:04")
	progress.Line("usage limit; waiting until "+when, true)
	progress.Spin("usage limit, until " + when)
	defer progress.Stop()
	for {
		left := time.Until(time.Unix(0, int64(until*1e9)))
		if left <= 0 {
			return nil
		}
		if left > time.Minute {
			left = time.Minute
		}
		timer := time.NewTimer(left)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func RunCost(agent map[string]any, run AgentRun) *float64 {
	n, ok := number(JSONPath(AgentOutput(agent, run.Stdout), str(agent["cost"])))
	if !ok {
		return nil
	}
	return &n
}
func RunTokens(agent map[string]any, run AgentRun) *int {
	data := AgentOutput(agent, run.Stdout)
	total := 0.0
	found := false
	for _, spec := range commandList(agent["tokens"]) {
		if n, ok := number(JSONPath(data, spec)); ok {
			total += n
			found = true
		}
	}
	if !found {
		return nil
	}
	n := int(total)
	return &n
}
func RunInt(agent map[string]any, run AgentRun, key string) *int {
	n, ok := number(JSONPath(AgentOutput(agent, run.Stdout), str(agent[key])))
	if !ok {
		return nil
	}
	v := int(n)
	return &v
}
func RunOutputTokens(agent map[string]any, run AgentRun) *int {
	return RunInt(agent, run, "output_tokens")
}
func RunTurns(agent map[string]any, run AgentRun) *int { return RunInt(agent, run, "turns") }

func RecordCall(root, uid, session string, tokens *int, cost *float64) error {
	data := cycle.ReadStats(root, uid)
	if _, ok := data["unit"]; !ok {
		data["unit"] = uid
	}
	sessions, ok := data["sessions"].([]any)
	if !ok {
		sessions = []any{}
	}
	if session != "" {
		found := false
		for _, s := range sessions {
			if s == session {
				found = true
			}
		}
		if !found {
			sessions = append(sessions, session)
		}
	}
	data["sessions"] = sessions
	if tokens != nil {
		n, _ := number(data["tokens"])
		data["tokens"] = n + float64(*tokens)
	}
	if cost != nil {
		n, _ := number(data["cost"])
		data["cost"] = n + *cost
	}
	calls, _ := number(data["calls"])
	data["calls"] = calls + 1
	return cycle.WriteStats(root, uid, data)
}

func Add(total, cost *float64) *float64 {
	if cost == nil {
		return total
	}
	n := *cost
	if total != nil {
		n += *total
	}
	return &n
}
func Duration(seconds float64) string {
	s := int(math.RoundToEven(seconds))
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
}
func Summary(seconds float64, cost *float64) string {
	text := Duration(seconds)
	if cost != nil {
		text += fmt.Sprintf(", $%.2f", *cost)
	}
	return text
}
