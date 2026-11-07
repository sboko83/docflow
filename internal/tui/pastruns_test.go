package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func historyFixture(t *testing.T) (string, string, []string) {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	dir := filepath.Join(root, ".git", "docflow", "run")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"item.completed","item":{"id":"r1","type":"reasoning","text":"thinking"}}`,
		`{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"hello"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`,
	}
	body := strings.Join(lines, "\n") + "\n"
	header := "$ agent\n--- stdout\n"
	if err := os.WriteFile(filepath.Join(dir, "call.out"), []byte(header+body+"\n--- stderr\n\n--- exit 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20261106T010000Z.events.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	events := []map[string]any{
		{"event": "run_start", "plan": "P001"},
		{"event": "unit_start", "unit": "P001.1.1"},
		{"event": "attempt", "unit": "P001.1.1", "n": 1, "route": map[string]any{"agent": "codex"}, "log": ".git/docflow/run/call.out"},
		{"event": "stream", "unit": "P001.1.1", "attempt": 1, "log": ".git/docflow/run/call.out", "stdout": map[string]any{"path": ".git/docflow/run/call.out", "offset": len(header), "length": len(body)}},
		{"event": "route_switch", "unit": "P001.1.1", "to": map[string]any{"agent": "pi"}},
		{"event": "attempt", "unit": "P001.1.1", "n": 2, "route": map[string]any{"agent": "pi"}, "log": "missing.out"},
		{"event": "replan", "unit": "P001.1.1", "result": "done"},
		{"event": "unit_end", "unit": "P001.1.1", "status": "done", "seconds": 8},
		{"event": "unit_end", "unit": "P001.1.2", "status": "failed"},
		{"event": "unit_end", "unit": "P001.1.3", "status": "blocked"},
		{"event": "run_end", "reason": "blocked", "exit_code": 3},
	}
	for i, e := range events {
		e["v"] = 1
		e["run"] = "20261106T010000Z"
		e["ts"] = 100 + i
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.WriteString("broken json\n"); err != nil {
		t.Fatal(err)
	}
	return root, path, lines
}

func TestHistorySummary(t *testing.T) {
	root, path, _ := historyFixture(t)
	r, h, err := ReplayRun(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan != "P001" || r.Attempts != 2 || r.RouteSwitches != 1 || r.Replans != 1 || r.Done != 1 || r.Failed != 1 || r.Blocked != 1 || r.CorruptLines != 1 || !r.ExitKnown || r.ExitCode != 3 || r.Reason != "blocked" || !r.DurationKnown || r.Duration.Seconds() != 10 || !r.TokensKnown || r.Tokens != 15 {
		t.Fatalf("summary: %+v", r)
	}
	missing := false
	for _, e := range h.Entries("P001.1.1") {
		missing = missing || e.Activity.Raw == "лог недоступен"
	}
	if !missing {
		t.Fatal("missing call log did not leave an unavailable block")
	}
	newer := filepath.Join(filepath.Dir(path), "newer.events.jsonl")
	if err := os.WriteFile(newer, []byte(`{"v":1,"event":"run_start","ts":200,"plan":"P002"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runs, err := LoadPastRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Plan != "P002" || runs[0].TokensKnown || runs[0].ExitKnown || runs[0].DurationKnown {
		t.Fatalf("runs: %+v", runs)
	}
}

func TestReplayMatchesLiveBlocks(t *testing.T) {
	root, path, lines := historyFixture(t)
	_, replay, err := ReplayRun(root, path)
	if err != nil {
		t.Fatal(err)
	}
	live := NewActivityHistory(1000)
	live.Add(runner.Event{Type: "attempt", Unit: "P001.1.1", Fields: map[string]any{"n": 1, "log": ".git/docflow/run/call.out"}})
	parser := agentio.NewActivityParser(map[string]any{"stream": "codex"})
	offset := int64(len("$ agent\n--- stdout\n"))
	var want []agentio.Activity
	for _, line := range lines {
		want = append(want, parser.Feed("stdout", line, agentio.ActivityRef{Stream: "stdout", Offset: offset, Length: len(line)})...)
		offset += int64(len(line) + 1)
	}
	want = append(want, parser.Finalize(agentio.ActivityStateUnknown)...)
	for _, a := range want {
		a.Attempt = 1
		live.Add(runner.Event{Type: "activity", Unit: "P001.1.1", Data: a})
	}
	var gotBlocks, wantBlocks []agentio.Activity
	for _, e := range replay.Entries("P001.1.1") {
		if e.Type == "activity" && e.Activity.Attempt == 1 {
			a := e.Activity
			a.Seq = 0
			a.Run = ""
			a.Unit = ""
			a.Agent = ""
			gotBlocks = append(gotBlocks, a)
		}
	}
	for _, e := range live.Entries("P001.1.1") {
		if e.Type == "activity" {
			a := e.Activity
			a.Seq = 0
			wantBlocks = append(wantBlocks, a)
		}
	}
	if !reflect.DeepEqual(gotBlocks, wantBlocks) {
		t.Fatalf("replay=%+v\nlive=%+v", gotBlocks, wantBlocks)
	}
}

func TestHistoryReplayScreen(t *testing.T) {
	root, path, _ := historyFixture(t)
	r, _, err := ReplayRun(root, path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(root)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, pastRunsMsg{runs: []PastRun{r}})
	m = m.switchScreen(screenHistory)
	if !strings.Contains(m.String(), r.ID) {
		t.Fatal(m.String())
	}
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter did not load replay")
	}
	m = send(t, m, cmd())
	if m.replay == nil || m.selectedUnit() != "P001.1.1" || m.screen != screenRun {
		t.Fatalf("no replay: %+v", m.replay)
	}
	for _, key := range []string{"s", "R", "p", "S", "x", "a", "F", "T", "C"} {
		m = send(t, m, keyMsg(key))
		if !strings.Contains(m.notice, "Режим просмотра") {
			t.Fatalf("%s: %s", key, m.notice)
		}
	}
	m.notice = ""
	for _, size := range [][2]int{{32, 10}, {99, 20}, {120, 30}} {
		m = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, screen := range []screen{screenRun, screenHistory} {
			m.screen = screen
			view := m.String()
			rows := strings.Split(view, "\n")
			if len(rows) != size[1] {
				t.Fatalf("height=%d want %d", len(rows), size[1])
			}
			for _, line := range rows {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("overflow: %q", line)
				}
			}
		}
	}
	m.screen = screenRun
	live := m.liveHistory
	m = m.ApplyEvent(runner.Event{Type: "attempt", Unit: "live", Fields: map[string]any{"n": 1}})
	if len(m.history.Entries("live")) != 0 || len(m.liveHistory.Entries("live")) != 1 {
		t.Fatal("live event changed replay")
	}
	m = send(t, m, keyMsg("tab"))
	m = send(t, m, keyMsg("down"))
	if m.selectedUnit() != "P001.1.2" {
		t.Fatal("replay unit navigation failed")
	}
	m = send(t, m, keyMsg("esc"))
	if m.replay != nil || m.history != live || m.screen != screenHistory {
		t.Fatal("did not restore live history")
	}
}

func TestHistoryCardScroll(t *testing.T) {
	root, path, _ := historyFixture(t)
	r, _, err := ReplayRun(root, path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(root)
	m = send(t, m, tea.WindowSizeMsg{Width: 32, Height: 10})
	m = send(t, m, pastRunsMsg{runs: []PastRun{r}})
	m = m.switchScreen(screenHistory)
	m = send(t, m, keyMsg("tab"))
	before := m.String()
	for i := 0; i < 20; i++ {
		m = send(t, m, keyMsg("pgdown"))
	}
	if after := m.String(); after == before || !strings.Contains(after, "Битые строки: 1") {
		t.Fatalf("card did not scroll to end:\n%s", after)
	}
}

func TestReplayAllAgentStreams(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi", "opencode"} {
		t.Run(name, func(t *testing.T) {
			fixture := name
			if name == "opencode" {
				fixture = "opencode-result"
			}
			body, err := os.ReadFile(filepath.Join("..", "agentio", "testdata", fixture+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "call.out"), body, 0644); err != nil {
				t.Fatal(err)
			}
			event := runner.Event{Type: "attempt", Unit: "unit", Fields: map[string]any{"n": 1, "log": "call.out", "route": map[string]any{"agent": name}}}
			got, want := NewActivityHistory(1000), NewActivityHistory(1000)
			got.Add(event)
			want.Add(event)
			r := PastRun{}
			replayCall(root, "run", event, map[string]any{"stdout": map[string]any{"path": "call.out", "offset": 0, "length": len(body)}}, got, &r)
			parser := agentio.NewActivityParser(map[string]any{"stream": name})
			publish := func(items []agentio.Activity) {
				for _, a := range items {
					a.Agent = name
					a.Run = "run"
					a.Unit = "unit"
					a.Attempt = 1
					want.Add(runner.Event{Type: "activity", Unit: "unit", Data: a})
				}
			}
			offset := int64(0)
			for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
				publish(parser.Feed("stdout", line, agentio.ActivityRef{Stream: "stdout", Offset: offset, Length: len(line)}))
				offset += int64(len(line) + 1)
			}
			publish(parser.Finalize(agentio.ActivityStateUnknown))
			g, w := got.Entries("unit"), want.Entries("unit")
			if len(g) != len(w) {
				t.Fatalf("blocks: replay %d live %d", len(g), len(w))
			}
			for i := range g {
				if !reflect.DeepEqual(g[i].Activity, w[i].Activity) || g[i].Truncated != w[i].Truncated {
					t.Fatalf("block %d differs: replay %+v live %+v", i, g[i], w[i])
				}
			}
		})
	}
}

func TestHistorySummaryCostWithoutTokens(t *testing.T) {
	root := t.TempDir()
	body := `{"type":"message_end","message":{"role":"assistant","content":[],"usage":{"cost":{"total":0.5}}}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "call.out"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	event := runner.Event{Type: "attempt", Unit: "unit", Fields: map[string]any{"n": 1, "log": "call.out", "route": map[string]any{"agent": "pi"}}}
	h := NewActivityHistory(1000)
	h.Add(event)
	r := PastRun{}
	replayCall(root, "run", event, nil, h, &r)
	if r.TokensKnown || r.Tokens != 0 {
		t.Fatalf("cost-only usage became known tokens: %+v", r)
	}
	if !strings.Contains(strings.Join(r.summaryLines(), "\n"), "Токены: ?") {
		t.Fatal(r.summaryLines())
	}
}

func TestHistoryLoadingAndCachedSummary(t *testing.T) {
	root, path, _ := historyFixture(t)
	m := New(root)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	next, cmd := m.Update(keyMsg("4"))
	m = next.(Model)
	if cmd == nil || !strings.Contains(m.String(), "чтение журналов") || strings.Contains(m.String(), "пока пусто") {
		t.Fatalf("history did not show loading:\n%s", m.String())
	}
	m = send(t, m, cmd())
	if m.pastLoading || len(m.pastRuns) != 1 || !strings.Contains(m.String(), m.pastRuns[0].ID) {
		t.Fatalf("history did not load:\n%s", m.String())
	}
	// An unchanged journal is served from the cache; a grown one is read again.
	first, err := LoadPastRuns(root)
	if err != nil || len(first) != 1 || first[0].Attempts != 2 {
		t.Fatalf("runs = %+v, %v", first, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"event":"attempt","unit":"P001.1.2","n":1,"log":"missing.out","ts":120}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	again, err := LoadPastRuns(root)
	if err != nil || len(again) != 1 || again[0].Attempts != 3 {
		t.Fatalf("grown journal not re-read: %+v, %v", again, err)
	}
}
