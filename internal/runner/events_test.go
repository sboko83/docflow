package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("event stream must end with a newline: %q", data)
	}
	var records []map[string]any
	for _, line := range bytes.Split(data[:len(data)-1], []byte{'\n'}) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid event line %q: %v", line, err)
		}
		id := strings.TrimSuffix(filepath.Base(path), ".events.jsonl")
		if record["v"] != float64(1) || record["run"] != id {
			t.Fatalf("invalid version or run id: %v", record)
		}
		if ts, ok := record["ts"].(float64); !ok || ts <= 0 {
			t.Fatalf("invalid timestamp: %v", record)
		}
		if _, ok := record["event"].(string); !ok {
			t.Fatalf("missing event name: %v", record)
		}
		records = append(records, record)
	}
	return records
}

func fixtureEvents(t *testing.T, f *runFixture) []map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.root, ".git/docflow/run/*.events.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("event files = %v, err = %v", files, err)
	}
	return readEvents(t, files[0])
}

func TestRunEventStream(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	f.check(f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1"), 0)
	records := fixtureEvents(t, f)
	var names []string
	for _, record := range records {
		name := record["event"].(string)
		names = append(names, name)
		if name != "run_start" && name != "run_end" && record["unit"] != "P001.1.1" {
			t.Fatalf("missing unit: %v", record)
		}
	}
	want := []string{"run_start", "unit_start", "attempt", "route_switch", "attempt", "quota", "verify", "commit", "unit_end", "run_end"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("events = %v, want %v", names, want)
	}
	if records[0]["plan"] != "P001" || records[0]["units"] != float64(2) {
		t.Fatalf("run_start = %v", records[0])
	}
	for _, i := range []int{2, 4} {
		record := records[i]
		if record["n"] != float64(1) {
			t.Fatalf("refusal consumed an attempt: %v", record)
		}
		if _, err := os.Stat(filepath.Join(f.root, record["log"].(string))); err != nil {
			t.Fatalf("attempt log: %v", err)
		}
	}
	if records[2]["log"] != ".git/docflow/run/P001.1.1.1.out" || records[4]["log"] != ".git/docflow/run/P001.1.1.2.out" {
		t.Fatalf("call log naming changed: %v, %v", records[2], records[4])
	}
	switchEvent := records[3]
	if switchEvent["kind"] != "limit" || switchEvent["from"].(map[string]any)["agent"] != "pi" || switchEvent["to"].(map[string]any)["agent"] != "codex" {
		t.Fatalf("route switch = %v", switchEvent)
	}
	if records[5]["name"] != "codex" || len(records[5]["windows"].([]any)) != 2 {
		t.Fatalf("quota = %v", records[5])
	}
	if records[6]["exit_code"] != float64(0) || records[7]["sha"] != f.git("rev-parse", "--short", "HEAD") {
		t.Fatalf("verify/commit = %v / %v", records[6], records[7])
	}
	if records[8]["status"] != "done" || records[8]["seconds"].(float64) <= 0 || records[9]["exit_code"] != float64(0) || records[9]["reason"] == "" {
		t.Fatalf("end events = %v", records[8:])
	}
}

func TestRunEventStreamDryRun(t *testing.T) {
	f := newRunFixture(t)
	f.check(f.cli("run", "--config", f.config, "--dry-run"), 0)
	files, err := filepath.Glob(filepath.Join(f.root, ".git/docflow/run/*.events.jsonl"))
	if err != nil || len(files) != 0 {
		t.Fatalf("dry-run event files = %v, err = %v", files, err)
	}
}

func TestRunEventStreamFailedVerifyAndCost(t *testing.T) {
	f := newRunFixture(t)
	f.configure("cost = \"json:total_cost_usd\"\n")
	reply := func(cost float64) string {
		return emit(map[string]any{"type": "result", "session_id": "s1", "total_cost_usd": cost, "structured_output": map[string]any{"status": "done", "handoff": []string{"done"}}})
	}
	f.check(f.run([]string{reply(.25), "echo a > a.txt\n" + reply(.5)}, "--max-units", "1"), 0)
	var codes []any
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "verify" {
			codes = append(codes, record["exit_code"])
		}
		if record["event"] == "unit_end" && record["cost"] != .75 {
			t.Fatalf("unit cost = %v, want .75", record)
		}
	}
	if !reflect.DeepEqual(codes, []any{float64(4), float64(0)}) {
		t.Fatalf("verify results = %v", codes)
	}
}

func TestRunEventStreamAgentStartError(t *testing.T) {
	f := newRunFixture(t)
	f.setConfig("[tiers]\ndefault = {agent = \"fake\"}\n[agents.fake]\ncmd = [" + jsonString(filepath.Join(f.tmp, "missing-agent")) + "]\nresult = \"json:structured_output\"\n")
	r := f.run(nil, "--max-units", "1")
	f.check(r, 7)
	records := fixtureEvents(t, f)
	end := records[len(records)-1]
	if end["event"] != "run_end" || end["exit_code"] != float64(r.code) || end["reason"] == "" {
		t.Fatalf("error run_end = %v", end)
	}
}

func TestEventStreamCollisionAndFlush(t *testing.T) {
	f := newRunFixture(t)
	start := time.Date(2026, 10, 25, 13, 15, 0, 0, time.FixedZone("MSK", 3*3600))
	var warnings bytes.Buffer
	for _, want := range []string{"20261025T101500Z", "20261025T101500Z-2", "20261025T101500Z-3"} {
		s := newEventStream(f.root, start, &warnings)
		if s.id != want {
			t.Fatalf("run id = %s, want %s", s.id, want)
		}
		s.emit("run_start", "", map[string]any{"plan": "P001", "units": 2})
		// Read before close: each event must already be visible to a consumer.
		path := filepath.Join(f.root, ".git/docflow/run", want+".events.jsonl")
		if got := readEvents(t, path); len(got) != 1 {
			t.Fatalf("records before close = %v", got)
		}
		s.close()
	}
	if warnings.Len() != 0 {
		t.Fatal(warnings.String())
	}
}

func TestEventStreamWriteFailureWarnsOnce(t *testing.T) {
	f := newRunFixture(t)
	var warnings bytes.Buffer
	s := newEventStream(f.root, time.Now(), &warnings)
	if s.file == nil {
		t.Fatal(warnings.String())
	}
	if err := s.file.Close(); err != nil {
		t.Fatal(err)
	}
	s.emit("run_start", "", nil)
	s.emit("unit_start", "P001.1.1", nil)
	s.emit("run_end", "", nil)
	s.close()
	if strings.Count(warnings.String(), "event stream disabled:") != 1 || strings.Count(warnings.String(), "\n") != 1 {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestRunEventStreamUnwritableDirectory(t *testing.T) {
	f := newRunFixture(t)
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	dir := filepath.Join(f.root, ".git/docflow/run")
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	// The lock has already been acquired when prerequisites run. Make only
	// stream creation fail, then restore access in the initial quota probe:
	// call logs and cycle state still require a writable directory.
	f.replace(planFile, "## Traps", "## Prerequisites\n- readonly run directory: `chmod 555 .git/docflow/run`\n\n## Traps")
	f.configure("stream = \"claude\"\nquota = [\"chmod\", \"755\", " + jsonString(dir) + "]\n")
	r := f.run([]string{writeReply("a.txt")}, "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	if strings.Count(r.err, "event stream disabled:") != 1 || strings.Count(r.err, "\n") != 1 {
		t.Fatalf("warnings = %q", r.err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.events.jsonl"))
	if err != nil || len(files) != 0 {
		t.Fatalf("disabled stream was reopened: %v, err = %v", files, err)
	}
}
