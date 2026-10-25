package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bsv-docflow/internal/agents"
)

// eventStream owns one run's append-only, versioned JSONL file. Failures are
// diagnostic only: after the first warning all further events are discarded.
type eventStream struct {
	mu       sync.Mutex
	file     *os.File
	id       string
	warnings io.Writer
	disabled bool
}

func newEventStream(root string, start time.Time, warnings io.Writer) *eventStream {
	s := &eventStream{warnings: warnings}
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
