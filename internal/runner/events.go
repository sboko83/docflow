package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

// eventStream owns one run's append-only, versioned JSONL file. Failures are
// diagnostic only: after the first warning all further events are discarded.
type eventStream struct {
	mu       sync.Mutex
	file     *os.File
	id       string
	warnings io.Writer
	disabled bool
	observe  func(Event)
}

func newEventStream(root string, start time.Time, warnings io.Writer) *eventStream {
	return newObservedEventStream(root, start, warnings, nil)
}

func newObservedEventStream(root string, start time.Time, warnings io.Writer, observe func(Event)) *eventStream {
	s := &eventStream{warnings: warnings, observe: observe}
	dir, err := runDir(root)
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		s.fail(err)
		return s
	}
	base := start.UTC().Format("20060102T150405Z")
	for n := 1; ; n++ {
		s.id = base
		if n > 1 {
			s.id = fmt.Sprintf("%s-%d", base, n)
		}
		s.file, err = os.OpenFile(filepath.Join(dir, s.id+".events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			s.fail(err)
		}
		return s
	}
}

func (s *eventStream) fail(err error) {
	if !s.disabled {
		fmt.Fprintf(s.warnings, "run: warning: event stream disabled: %s\n", oneLine(err.Error()))
		flushWriter(s.warnings)
	}
	s.disabled = true
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

// RunID names the stream, which is also the run id of the events it holds.
func (s *eventStream) RunID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *eventStream) emit(event, unit string, fields map[string]any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return
	}
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["v"], fields["ts"], fields["run"], fields["event"] = 1, epoch(time.Now()), s.id, event
	if unit != "" {
		fields["unit"] = unit
	}
	data, err := json.Marshal(fields)
	if err == nil {
		_, err = s.file.Write(append(data, '\n'))
	}
	if err == nil && s.observe != nil {
		s.observe(Event{Type: event, Unit: unit, Fields: fields})
	}
	if err == nil {
		err = s.file.Sync()
	}
	if err != nil {
		s.fail(err)
	}
}

func (s *eventStream) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			s.fail(err)
		}
		s.file = nil
	}
	s.disabled = true
}

func eventRoute(route agents.Route) map[string]string {
	return map[string]string{"agent": route.Agent, "model": route.Model, "effort": route.Effort}
}

func eventStatus(code int, err error) string {
	switch {
	case err != nil:
		return "error"
	case code == 0:
		return "done"
	case code == 3:
		return "blocked"
	default:
		return "failed"
	}
}

// The CLI reports an error's own exit code, or 2 for an uncoded error,
// regardless of the code returned alongside it by Run.
func eventExitCode(code int, err error) int {
	if err != nil {
		if coded, ok := err.(interface{ ExitCode() int }); ok {
			return coded.ExitCode()
		}
		return 2
	}
	return code
}

// pauseOutcome reclassifies a deferred operation that a written Pause
// checkpoint stopped: the journal shows a pause with the success code the host sees, not
// the internal stop code of the runner.
func pauseOutcome(totals *runTotals, result string, exit int) (string, int) {
	if totals.isPaused() {
		return "paused", 0
	}
	return result, exit
}

// eventRoutes is the safe projection of a route chain: assignment only, no
// command, environment or secret of the profile.
func eventRoutes(routes []agents.Route) []map[string]string {
	out := make([]map[string]string, 0, len(routes))
	for _, route := range routes {
		out = append(out, eventRoute(route))
	}
	return out
}

// eventConfig is the configuration snapshot of a run: the plan's assignments
// and permissions, with no secret and no message body. The interactive run
// configuration of P052.3.1 extends it.
func eventConfig(plan *format.Plan) map[string]any {
	fields := map[string]any{"permissions": "auto"}
	if plan == nil {
		return fields
	}
	fields["plan"], fields["permissions"] = plan.Meta["id"], format.PlanPermissions(plan)
	for _, key := range []string{"executor", "fallback", "replan"} {
		if value := plan.Meta[key]; value != "" {
			if routes, err := agents.ChainRoutes(value); err == nil {
				fields[key] = eventRoutes(routes)
			}
		}
	}
	roles := map[string]any{}
	for _, unit := range plan.Units {
		field := unit.Field("Role")
		if field == nil {
			continue
		}
		if _, seen := roles[field.Value]; seen {
			continue
		}
		if routes, err := agents.ChainRoutes(plan.Meta[field.Value]); err == nil {
			roles[field.Value] = eventRoutes(routes)
		}
	}
	if len(roles) > 0 {
		fields["roles"] = roles
	}
	return fields
}

// activityObserver links one normalized activity to the run, unit, attempt and
// profile that produced it, then hands it to the interactive host. The host
// queue is bounded, so a slow reader never blocks the stream writer.
func activityObserver(totals *runTotals, host *Control, uid string, attempt int, name string, route agents.Route) func(agentio.Activity) {
	return func(activity agentio.Activity) {
		activity.Seq = atomic.AddInt64(&totals.activitySeq, 1)
		activity.Run, activity.Unit, activity.Attempt = totals.events.RunID(), uid, attempt
		activity.Agent, activity.Model, activity.Effort = name, route.Model, route.Effort
		host.Publish(Event{Type: "activity", Unit: uid, Fields: activityFields(activity), Data: activity})
	}
}

// activityFields is the metadata of one activity: where the original bytes
// are, never the payload itself.
func activityFields(activity agentio.Activity) map[string]any {
	fields := map[string]any{
		"seq": activity.Seq, "kind": string(activity.Kind), "state": string(activity.State),
		"ref":   map[string]any{"stream": activity.Ref.Stream, "offset": activity.Ref.Offset, "length": activity.Ref.Length},
		"agent": activity.Agent, "model": activity.Model, "effort": activity.Effort, "attempt": activity.Attempt,
	}
	for key, value := range map[string]string{
		"message": activity.Message, "tool": activity.Tool, "tool_id": activity.ToolID, "command": activity.Command,
	} {
		if value != "" {
			fields[key] = value
		}
	}
	if activity.Delta {
		fields["delta"] = true
	}
	if activity.Usage != nil {
		fields["usage"] = activity.Usage
	}
	if len(activity.Files) > 0 {
		fields["files"] = activity.Files
	}
	return fields
}

// relativeLog spells a path the way the event stream does: relative to the
// checkout, slash separated.
func relativeLog(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// callStreamFields links one call attempt to the raw bytes of its streams.
func callStreamFields(root, uid string, attempt int, log string, run agentio.AgentRun) map[string]any {
	rel, stderr := relativeLog(root, log), relativeLog(root, agentio.StderrLog(log))
	fields := map[string]any{
		"attempt": attempt,
		"log":     rel,
		"stdout":  map[string]any{"path": rel, "offset": run.StdoutOffset, "length": run.StdoutLength},
		"stderr":  map[string]any{"path": stderr, "offset": run.StderrOffset, "length": run.StderrLength},
	}
	if len(run.Stdout) == 0 && len(run.Stderr) == 0 {
		fields["empty"] = true
	}
	return fields
}

// verifyStreamFields writes a verify/gate result to its own log and links the
// streams the same way a call log is linked. A failure to write the log is
// diagnostic only: the verify event keeps its exit code.
func verifyStreamFields(root, uid string, res cycle.Result) map[string]any {
	logs, err := runDir(root)
	if err != nil {
		return nil
	}
	path, err := nextCallLog(logs, "verify."+uid+".")
	if err != nil {
		return nil
	}
	body := "--- stdout\n" + res.Stdout + "\n--- stderr\n" + res.Stderr
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil
	}
	rel := relativeLog(root, path)
	stdoutOffset := int64(len("--- stdout\n"))
	stderrOffset := stdoutOffset + int64(len(res.Stdout)) + int64(len("\n--- stderr\n"))
	return map[string]any{
		"stage": verifyStage(res.Stdout),
		"log":   rel,
		"stdout": map[string]any{
			"path": rel, "offset": stdoutOffset, "length": len(res.Stdout),
		},
		"stderr": map[string]any{
			"path": rel, "offset": stderrOffset, "length": len(res.Stderr),
		},
	}
}

func verifyStage(stdout string) string {
	for _, stage := range []string{"preflight", "gate"} {
		if strings.Contains(stdout, ": "+stage+" failed (exit") {
			return stage
		}
	}
	return "verify"
}
