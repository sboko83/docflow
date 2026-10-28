package agentio

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"bsv-docflow/internal/procgroup"
)

func WindowLabel(minutes any) string {
	n, ok := number(minutes)
	if !ok {
		return ""
	}
	if n == 300 {
		return "5h"
	}
	if math.Mod(n, 1440) == 0 {
		return fmt.Sprintf("%dd", int(n/1440))
	}
	return fmt.Sprintf("%.0fh", math.RoundToEven(n/60))
}

func CodexLimits(threadID string) map[string]any {
	if threadID == "" {
		return nil
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		base, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		home = filepath.Join(base, ".codex")
	}
	files, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	text, err := os.ReadFile(files[len(files)-1])
	if err != nil {
		return nil
	}
	var found map[string]any
	for _, line := range strings.Split(textOutput(string(text)), "\n") {
		if !strings.Contains(line, `"token_count"`) {
			continue
		}
		payload := table(ParseLine(line)["payload"])
		if payload["type"] == "token_count" {
			if limits := table(payload["rate_limits"]); limits != nil {
				found = limits
			}
		}
	}
	return found
}

func CodexWindows(limits map[string]any, keys ...string) Quota {
	if len(keys) == 0 {
		keys = []string{"used_percent", "window_minutes", "resets_at"}
	}
	quota := Quota{}
	for _, key := range []string{"primary", "secondary"} {
		w := table(limits[key])
		if use, ok := number(w[keys[0]]); ok {
			label := WindowLabel(w[keys[1]])
			if label == "" {
				label = key
			}
			quota.Set(label, use/100, w[keys[2]])
		}
	}
	return quota
}
func CodexQuota(threadID string) Quota { return CodexWindows(CodexLimits(threadID)) }

var UsageLineRE = regexp.MustCompile(`(?m)^Current (session|week \(all models\)): (\d+(?:\.\d+)?)% used(?: · resets (.+?))?\s*$`)
var UsageResetRE = regexp.MustCompile(`^(?:([A-Z][a-z]{2}) (\d{1,2}) at )?(\d{1,2})(?::(\d\d))?([ap]m) \(([^)]+)\)$`)
var Months = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func UsageReset(text string, now time.Time) *float64 {
	m := UsageResetRE.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	tz, err := time.LoadLocation(m[6])
	if err != nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(tz)
	month := now.Month()
	day := now.Day()
	if m[1] != "" {
		month = 0
		for i, name := range Months {
			if name == m[1] {
				month = time.Month(i + 1)
			}
		}
		if month == 0 {
			return nil
		}
		day, _ = strconv.Atoi(m[2])
	}
	hour, _ := strconv.Atoi(m[3])
	hour %= 12
	if m[5] == "pm" {
		hour += 12
	}
	minute, _ := strconv.Atoi(m[4])
	if minute > 59 {
		return nil
	}
	at := time.Date(now.Year(), month, day, hour, minute, 0, 0, tz)
	if at.Month() != month || at.Day() != day {
		return nil
	}
	if m[1] != "" && at.Before(now.Add(-24*time.Hour)) {
		at = time.Date(at.Year()+1, month, day, hour, minute, 0, 0, tz)
		if at.Month() != month || at.Day() != day {
			return nil
		}
	}
	n := float64(at.Unix())
	return &n
}

func ParseUsage(text string, now time.Time) Quota {
	quota := Quota{}
	for _, m := range UsageLineRE.FindAllStringSubmatch(text, -1) {
		key := "five_hour"
		if m[1] == "week (all models)" {
			key = "seven_day"
		}
		use, _ := strconv.ParseFloat(m[2], 64)
		var reset any
		if at := UsageReset(m[3], now); at != nil {
			reset = *at
		}
		quota.Set(key, use/100, reset)
	}
	return quota
}

// CodexProbe requests account limits without starting an inference turn.
func CodexProbe(argv []string, timeout time.Duration) map[string]any {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.WaitDelay = PipeGrace
	procgroup.Detach(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	found := make(chan map[string]any, 1)
	out := &eventWriter{callback: func(event Event) {
		obj := event.Data
		if n, ok := number(obj["id"]); ok && n == 2 {
			select {
			case found <- table(obj["result"]):
			default:
			}
		}
	}}
	cmd.Stdout = out
	if cmd.Start() != nil {
		_ = in.Close()
		return nil
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		out.flush()
		done <- err
	}()
	watch, _ := procgroup.Track(cmd.Process)
	defer watch.Close()
	defer in.Close()
	_, _ = fmt.Fprintln(in, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"docflow","version":"0"}}}`+"\n"+`{"method":"initialized"}`+"\n"+`{"id":2,"method":"account/rateLimits/read"}`)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var result map[string]any
	select {
	case result = <-found:
		_ = in.Close()
		_ = procgroup.Kill(cmd, watch, done, KillGrace)
	case <-timer.C:
		_ = in.Close()
		_ = procgroup.Kill(cmd, watch, done, KillGrace)
	case <-done:
		select {
		case result = <-found:
		default:
		}
	}
	return table(result["rateLimits"])
}

func commandList(value any) []string {
	if v, ok := value.([]string); ok {
		return v
	}
	items, _ := value.([]any)
	var argv []string
	for _, v := range items {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		argv = append(argv, s)
	}
	return argv
}

func ProbeQuota(agent map[string]any, timeout time.Duration) Quota {
	argv := commandList(agent["quota"])
	if len(argv) == 0 {
		return Quota{}
	}
	if agent["stream"] == "codex" {
		return CodexWindows(CodexProbe(argv, timeout), "usedPercent", "windowDurationMins", "resetsAt")
	}
	if agent["stream"] == "claude" {
		run, err := CallAgent("", argv, nil, "", CallOptions{Timeout: timeout})
		if err == nil && run.Code == 0 && !run.TimedOut {
			return ParseUsage(run.Stdout, time.Time{})
		}
	}
	return Quota{}
}

func Epoch(value any) *float64 {
	n, ok := number(value)
	if !ok {
		return nil
	}
	if n >= 1e12 {
		n /= 1000
	}
	return &n
}

func LimitReset(agent map[string]any, run AgentRun) *float64 {
	if run.TimedOut {
		return nil
	}
	data := AgentOutput(agent, run.Stdout)
	if JSONPath(data, str(agent["error"])) != true && run.Code == 0 {
		return nil
	}
	var resets []*float64
	switch agent["stream"] {
	case "codex":
		limits := CodexLimits(str(table(data)["thread_id"]))
		var windows, full []map[string]any
		for _, key := range []string{"primary", "secondary"} {
			if w := table(limits[key]); w != nil {
				windows = append(windows, w)
				if n, ok := number(w["used_percent"]); ok && n >= 100 {
					full = append(full, w)
				}
			}
		}
		if truth(limits["rate_limit_reached_type"]) && len(full) == 0 {
			full = windows
		}
		for _, w := range full {
			resets = append(resets, Epoch(w["resets_at"]))
		}
	case "claude":
		for _, line := range strings.Split(run.Stdout, "\n") {
			obj := ParseLine(line)
			info := table(obj["rate_limit_info"])
			if obj["type"] == "rate_limit_event" && info["status"] == "rejected" {
				resets = []*float64{Epoch(info["resetsAt"])}
			}
		}
	default:
		return nil
	}
	var max *float64
	for _, r := range resets {
		if r != nil && *r != 0 && (max == nil || *r > *max) {
			max = r
		}
	}
	return max
}
