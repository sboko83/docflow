package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/runner"
)

// PastRun is a journal summary, independent of terminal state and plan files.
type PastRun struct {
	ID, Plan, Path, Reason                                  string
	Started, Ended                                          time.Time
	Duration                                                time.Duration
	DurationKnown                                           bool
	Done, Failed, Blocked, Attempts, RouteSwitches, Replans int
	Tokens                                                  int64
	TokensKnown                                             bool
	ExitCode                                                int
	ExitKnown                                               bool
	CorruptLines                                            int
	Units                                                   []string
}

// LoadPastRuns reads checkout-local journals without taking the checkout lock.
func LoadPastRuns(root string) ([]PastRun, error) {
	dir, err := cycle.StateDir(root)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(dir), "run", "*.events.jsonl"))
	if err != nil {
		return nil, err
	}
	var runs []PastRun
	for _, path := range paths {
		run, err := cachedSummary(root, path)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Started.Equal(runs[j].Started) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].Started.After(runs[j].Started)
	})
	return runs, nil
}

// summaryCache keeps the summaries of journals that have not changed since
// they were read: a summary parses every call log of the run, and the
// History screen reloads the list on every visit.
var summaryCache = struct {
	sync.Mutex
	entries map[string]cachedRun
}{entries: map[string]cachedRun{}}

type cachedRun struct {
	size    int64
	modTime time.Time
	run     PastRun
}

// cachedSummary returns the summary of one journal, parsing it again only
// when its size or modification time changed. A journal of a live run grows,
// so its summary is read afresh until the run ends.
func cachedSummary(root, path string) (PastRun, error) {
	info, err := os.Stat(path)
	if err != nil {
		return PastRun{}, err
	}
	key := root + "|" + path
	summaryCache.Lock()
	entry, ok := summaryCache.entries[key]
	summaryCache.Unlock()
	if ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.run, nil
	}
	run, err := replayRun(root, path, nil)
	if err != nil {
		return run, err
	}
	summaryCache.Lock()
	summaryCache.entries[key] = cachedRun{size: info.Size(), modTime: info.ModTime(), run: run}
	summaryCache.Unlock()
	return run, nil
}

// ReplayRun rebuilds activity with the same parser and block collector as a
// live run. Malformed journal lines and missing call logs are local damage.
func ReplayRun(root, path string) (PastRun, *ActivityHistory, error) {
	history := NewActivityHistory(activityHistoryLimit)
	run, err := replayRun(root, path, history)
	return run, history, err
}

// replayRun reads one journal and its call logs; a nil history only
// summarizes the run and keeps no stream.
func replayRun(root, path string, history *ActivityHistory) (PastRun, error) {
	run := PastRun{Path: path, ID: strings.TrimSuffix(filepath.Base(path), ".events.jsonl")}
	f, err := os.Open(path)
	if err != nil {
		return run, err
	}
	defer f.Close()
	var events []runner.Event
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var fields map[string]any
			if json.Unmarshal(line, &fields) != nil || strValue(fields["event"]) == "" || fields["v"] != float64(1) {
				run.CorruptLines++
			} else {
				events = append(events, runner.Event{Type: strValue(fields["event"]), Unit: strValue(fields["unit"]), Fields: fields})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return run, readErr
		}
	}
	seenUnits := map[string]bool{}
	for i, event := range events {
		// Closing a plan has calls without a unit. Give their stream a
		// selectable bucket while retaining run_start/run_end at run scope.
		if event.Unit == "" && (event.Type == "attempt" || event.Type == "stream") {
			event.Unit = "Прогон"
		}
		if history != nil {
			history.Add(event)
		}
		at := eventTime(event.Fields, func() time.Time { return time.Time{} })
		if event.Unit != "" && !seenUnits[event.Unit] {
			seenUnits[event.Unit] = true
			run.Units = append(run.Units, event.Unit)
		}
		switch event.Type {
		case "run_start":
			run.Plan, run.Started = strValue(event.Fields["plan"]), at
		case "run_end":
			run.Ended, run.Reason = at, strValue(event.Fields["reason"])
			run.ExitCode, run.ExitKnown = intValue(event.Fields["exit_code"])
		case "unit_end":
			switch strValue(event.Fields["status"]) {
			case "done":
				run.Done++
			case "failed", "error":
				run.Failed++
			case "blocked":
				run.Blocked++
			}
		case "route_switch":
			run.RouteSwitches++
		case "replan":
			run.Replans++
		case "attempt":
			run.Attempts++
			// A stream record closes this call, even when fallback reuses n.
			var stream map[string]any
			for _, next := range events[i+1:] {
				if next.Unit == "" && event.Unit == "Прогон" {
					next.Unit = "Прогон"
				}
				if next.Unit != event.Unit {
					continue
				}
				if next.Type == "attempt" {
					break
				}
				if next.Type == "stream" && strValue(next.Fields["log"]) == strValue(event.Fields["log"]) {
					stream = next.Fields
					break
				}
			}
			replayCall(root, run.ID, event, stream, history, &run)
		}
	}
	if !run.Started.IsZero() && !run.Ended.IsZero() && !run.Ended.Before(run.Started) {
		run.Duration, run.DurationKnown = run.Ended.Sub(run.Started), true
	}
	return run, nil
}

func replayCall(root, id string, event runner.Event, stream map[string]any, history *ActivityHistory, run *PastRun) {
	route := routeOf(event.Fields["route"])
	parser := agentio.NewActivityParser(map[string]any{"stream": route.Agent})
	attempt, _ := intValue(event.Fields["n"])
	publish := func(items []agentio.Activity) {
		for _, activity := range items {
			activity.Run, activity.Unit, activity.Attempt = id, event.Unit, attempt
			activity.Agent, activity.Model, activity.Effort = route.Agent, route.Model, route.Effort
			if activity.Usage != nil {
				if tokens := usageTokens(activity.Usage); tokens > 0 {
					run.Tokens += tokens
					run.TokensKnown = true
				}
			}
			if history != nil {
				history.Add(runner.Event{Type: "activity", Unit: event.Unit, Fields: event.Fields, Data: activity})
			}
		}
	}
	missing := func() {
		publish([]agentio.Activity{{Kind: agentio.ActivityKindUnknown, State: agentio.ActivityStateUnknown, Raw: "лог недоступен"}})
	}
	for _, channel := range []string{"stdout", "stderr"} {
		link, _ := stream[channel].(map[string]any)
		log := strValue(link["path"])
		if log == "" {
			if channel == "stderr" {
				continue
			}
			log = strValue(event.Fields["log"])
		}
		path, err := resolveLogPath(root, log)
		if err != nil {
			missing()
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			missing()
			continue
		}
		offset, _ := numberValue(link["offset"])
		length, bounded := numberValue(link["length"])
		_, err = f.Seek(int64(offset), io.SeekStart)
		if err != nil {
			f.Close()
			missing()
			continue
		}
		var source io.Reader = f
		if bounded {
			source = io.LimitReader(f, int64(length))
		}
		reader := bufio.NewReader(source)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				// Old/incomplete journals have only attempt.log. Strip the
				// call-log envelope rather than displaying it as agent output.
				if link == nil && (strings.HasPrefix(line, "$ ") || strings.TrimSpace(line) == "--- stdout") {
					offset += float64(len(line))
					continue
				}
				if link == nil && strings.HasPrefix(line, "--- stderr") {
					break
				}
				text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
				publish(parser.Feed(channel, strings.ToValidUTF8(text, "�"), agentio.ActivityRef{Stream: channel, Offset: int64(offset), Length: len(text)}))
				offset += float64(len(line))
			}
			if err != nil {
				if err != io.EOF {
					missing()
				}
				break
			}
		}
		f.Close()
	}
	publish(parser.Finalize(agentio.ActivityStateUnknown))
}

func (r PastRun) summaryLines() []string {
	duration, tokens, exit, started, plan, reason := "?", "?", "?", "?", "?", "?"
	if !r.Started.IsZero() {
		started = r.Started.Format(time.RFC3339)
	}
	if r.Plan != "" {
		plan = Sanitize(r.Plan)
	}
	if r.Reason != "" {
		reason = Sanitize(r.Reason)
	}
	if r.DurationKnown {
		duration = formatElapsed(r.Duration)
	}
	if r.TokensKnown {
		tokens = formatTokens(r.Tokens)
	}
	if r.ExitKnown {
		exit = fmt.Sprint(r.ExitCode)
	}
	return []string{Sanitize(r.ID), "План: " + plan, "Начало: " + started, "Длительность: " + duration,
		fmt.Sprintf("Юниты: %d done · %d failed · %d blocked", r.Done, r.Failed, r.Blocked),
		fmt.Sprintf("Попытки: %d · маршруты: %d · replan: %d", r.Attempts, r.RouteSwitches, r.Replans),
		"Токены: " + tokens, "Причина: " + reason, "Код: " + exit, fmt.Sprintf("Битые строки: %d", r.CorruptLines)}
}
