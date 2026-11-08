package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/runner"
	"bsv-docflow/internal/testshim"
)

// The same keyboard scenarios run on PTY (macOS/Linux) and ConPTY (Windows).
// Assertions use the reconstructed visible screen, never stale raw output.
type workspaceTerminal interface {
	send(string)
	screen() string
	output() string
	resize(int, int)
	quit()
}

type workspaceFixture struct {
	root, scenario, policy, bin, cache string
	env                                []string
}

func workspaceGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	// A status poll must not take index.lock while the runner commits: on
	// Windows the commit then fails on the existing lock.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newWorkspaceFixture(t *testing.T) *workspaceFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("workspace acceptance runs the built CLI in a terminal")
	}
	root, _, scenario := writeRunnableProject(t)
	f := &workspaceFixture{root: root, scenario: scenario}
	plan := strings.ReplaceAll(applyPlanText(), "P700", "P001")
	plan = strings.Split(plan, "### [ ] P001.1.2")[0]
	plan = strings.ReplaceAll(plan, "pi | codex/gpt-6-luna | high", "claude | fixture-model | high")
	plan = strings.ReplaceAll(plan, "`true`", "`printf WORKSPACE-VERIFY; test -f a.txt`")
	writeRosterTestFile(t, filepath.Join(root, "docs/plans/P001-tui.md"), plan)
	writeRosterTestFile(t, filepath.Join(root, "a.go"), "package fixture\n")
	workspaceGit(t, root, "add", "-A")
	workspaceGit(t, root, "commit", "-qm", "Prepare workspace fixture")
	home, cache, bin := t.TempDir(), t.TempDir(), t.TempDir()
	f.bin = bin
	f.cache = cache
	f.policy = filepath.Join(home, ".config/docflow/roster.toml")
	writeRosterTestFile(t, f.policy, strings.ReplaceAll(testRosterPolicy, "check  = true", "check  = false"))
	t.Setenv("XDG_CACHE_HOME", cache)
	writeRosterTestCatalog(t)
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex", "pi"} {
		testshim.Script(t, bin, agent, "#!/bin/bash\n"+
			"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then exit 0; fi\n"+
			"exec "+workspaceQuote(testshim.Bash(t))+" "+workspaceQuote(filepath.ToSlash(fake))+" \"$@\"\n")
	}
	for _, entry := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		switch key {
		case "DOCFLOW_RUNNER", "DOCFLOW_RUNNER_UNIT", "DOCFLOW_AGENTS", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "PATH", "NO_COLOR", "TERM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "DOCFLOW_NOTIFY", "DOCFLOW_SKILLS_CHECK", "FAKE_AGENT_SCENARIO":
			continue
		}
		f.env = append(f.env, entry)
	}
	f.env = append(f.env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+cache, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TERM=xterm-256color", "NO_COLOR=1", "DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"FAKE_AGENT_SCENARIO="+scenario, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	return f
}

func workspaceQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func workspaceScreen(t *testing.T, ui workspaceTerminal, present, absent string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		s := ui.screen()
		if strings.Contains(s, present) && (absent == "" || !strings.Contains(s, absent)) {
			return
		}
	}
	t.Fatalf("screen wants %q, without %q:\n%s", present, absent, ui.screen())
}

func workspaceRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWorkspaceTerminalRosterPlan(t *testing.T) {
	f := newWorkspaceFixture(t)
	policyBefore := workspaceRead(t, f.policy)
	ui := startWorkspaceTerminal(t, f, 110, 40)
	workspaceScreen(t, ui, "Queue", "")
	ui.send("\x1b[B") // Select P001, the target of A on either screen.
	ui.send("2")
	workspaceScreen(t, ui, "Доступы", "")
	ui.send("\t")
	ui.send(" ") // zai is the first access in policy order.
	workspaceScreen(t, ui, "Switch the access off", "")
	ui.send("\r")
	workspaceScreen(t, ui, "zai switched off", "")
	ui.send("n")
	workspaceScreen(t, ui, "Save the conditions as a variant", "")
	ui.send("workspace-off")
	ui.send("\t")
	workspaceScreen(t, ui, "project (docs/roster.variants.toml)", "")
	ui.send("\r")
	workspaceScreen(t, ui, "variant workspace-off saved", "")
	variantsPath := filepath.Join(f.root, "docs/roster.variants.toml")
	variants, err := roster.LoadWorkspace(f.policy, f.root)
	if err != nil {
		t.Fatal(err)
	}
	var saved *roster.Preset
	for i := range variants.Policy.Presets {
		if variants.Policy.Presets[i].Name == "workspace-off" {
			saved = &variants.Policy.Presets[i]
		}
	}
	if saved == nil || len(saved.Off) != 1 || saved.Off[0] != "zai" || !strings.Contains(workspaceRead(t, variantsPath), "[preset.workspace-off]") {
		t.Fatalf("saved variant = %#v", saved)
	}
	ui.send("\t") // roles
	ui.send("\t") // variants; save positioned the cursor on workspace-off.
	ui.send("\r")
	workspaceScreen(t, ui, "workspace-off", "")
	ui.send("A")
	workspaceScreen(t, ui, "Шапка P001 из ростера", "")
	ui.send("\r")
	workspaceScreen(t, ui, "commit: P001: apply roster", "Шапка P001 из ростера")
	planPath := filepath.Join(f.root, "docs/plans/P001-tui.md")
	doc, err := format.ParsePlan(workspaceRead(t, planPath), planPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Doc.Meta["executor"]; !strings.HasPrefix(got, "codex | ") {
		t.Fatalf("applied executor = %q\n%s", got, workspaceRead(t, planPath))
	}
	if workspaceRead(t, f.policy) != policyBefore {
		t.Fatal("TUI rewrote the hand-owned policy")
	}
	ui.send("1")
	ui.send("c")
	workspaceScreen(t, ui, "Commit docflow files", "")
	workspaceScreen(t, ui, "docs/roster.variants.toml", "")
	ui.send("\r")
	workspaceScreen(t, ui, "committed docflow files", "")
	if got := workspaceGit(t, f.root, "status", "--porcelain"); got != "" {
		t.Fatalf("TUI commit left a dirty tree:\n%s", got)
	}
	if got := workspaceGit(t, f.root, "show", "--format=", "--name-only", "HEAD"); got != "docs/plans/P001-tui.md\ndocs/roster.variants.toml" {
		t.Fatalf("TUI committed files = %q", got)
	}
	ui.quit()
}

func workspaceStream() string {
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"workspace-session"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"REASON-FIRST\n"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"REASON-MIDDLE\n"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"REASON-LAST\u001b]0;UNSAFE-TITLE\u0007"}}}`,
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"REASON-FIRST\nREASON-MIDDLE\nREASON-LAST\u001b]0;UNSAFE-TITLE\u0007"},{"type":"text","text":"WORKSPACE-MESSAGE"},{"type":"tool_use","id":"tool1","name":"Bash","input":{"command":"WORKSPACE-TOOL"}}]}}`,
	}
	var s strings.Builder
	for _, line := range lines {
		s.WriteString("printf '%s\\n' " + workspaceQuote(line) + "\n")
	}
	return s.String()
}

func TestWorkspaceTerminalRunHistory(t *testing.T) {
	f := newWorkspaceFixture(t)
	planPath := filepath.Join(f.root, "docs/plans/P001-tui.md")
	plan := strings.Replace(workspaceRead(t, planPath), "executor: claude | fixture-model | high", "executor: claude | unused-model | low\nheavy: claude | fixture-model | high", 1)
	plan = strings.Replace(plan, "Do:\n", "Role: heavy\nDo:\n", 1)
	writeRosterTestFile(t, planPath, plan)
	workspaceGit(t, f.root, "add", "-A")
	workspaceGit(t, f.root, "commit", "-qm", "Route fixture through heavy")
	result := `{"type":"result","is_error":false,"result":"ok","session_id":"workspace-session","usage":{"input_tokens":12,"output_tokens":7},"structured_output":{"status":"done","handoff":["done"],"decisions":[],"unverified":[],"reason":"","blocker":""}}`
	writeRosterTestFile(t, filepath.Join(f.scenario, "0.sh"), workspaceStream()+"sleep 60\n")
	writeRosterTestFile(t, filepath.Join(f.scenario, "1.sh"), workspaceStream()+"printf a > a.txt\nprintf '%s\\n' "+workspaceQuote(result)+"\n")
	ui := startWorkspaceTerminal(t, f, 120, 44)
	workspaceScreen(t, ui, "Queue", "")
	ui.send("s")
	workspaceScreen(t, ui, "Start the current Queue run?", "")
	ui.send("\r")
	ui.send("1")
	ui.send("/")
	ui.send("P001.1.1")
	ui.send("\r")
	ui.send("\x1b[F") // Select the sole search result and open its stream.
	ui.send("\r")
	ui.send("3")
	workspaceScreen(t, ui, "WORKSPACE-TOOL", "")
	for _, want := range []string{"REASON-FIRST", "REASON-MIDDLE", "REASON-LAST", "WORKSPACE-MESSAGE"} {
		workspaceScreen(t, ui, want, "")
	}
	ui.send("t")
	workspaceScreen(t, ui, "REASON-FIRST", "REASON-LAST")
	ui.send("t")
	workspaceScreen(t, ui, "REASON-LAST", "")
	// A live controller and its actual checkout lock must refuse together.
	if _, err := os.Stat(filepath.Join(f.root, ".git/docflow/run/run.lock")); err != nil {
		t.Fatal(err)
	}
	beforeApply := workspaceRead(t, planPath)
	ui.send("2")
	workspaceScreen(t, ui, "Доступы", "")
	ui.send("A")
	workspaceScreen(t, ui, "идёт прогон", "")
	ui.send("\r")
	workspaceScreen(t, ui, "идёт прогон", "")
	if workspaceRead(t, planPath) != beforeApply {
		t.Fatal("roster apply changed a plan under its own live run lock")
	}
	ui.send("\x1b")
	ui.send("3")
	ui.send("p")
	// "paused" reaches the stream (unit_end paused) before the host has
	// stopped; R before that is refused as "a run is already active". The
	// notice below is set only once the paused run has finished.
	workspaceScreen(t, ui, "Resume continues the interrupted unit", "")
	cp, err := runner.ReadCheckpoint(f.root)
	if err != nil || cp.Session != "workspace-session" || cp.Stage != "agent" {
		t.Fatalf("paused checkpoint = %+v, %v", cp, err)
	}
	ui.send("R")
	workspaceScreen(t, ui, "verify passed", "")
	ui.send("v")
	workspaceScreen(t, ui, "WORKSPACE-VERIFY", "")
	ui.send("\x1b")
	// Completion is established from the committed plan, not a transient notice.
	// A loaded Windows CI runner needs more than 30 s for verify, done and
	// commit through Git Bash.
	completed := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if strings.Contains(workspaceRead(t, planPath), "[x] P001.1.1") && workspaceGit(t, f.root, "status", "--porcelain") == "" {
			completed = true
			break
		}
	}
	if !completed {
		unitLine := ""
		for _, line := range strings.Split(workspaceRead(t, planPath), "\n") {
			if strings.Contains(line, "P001.1.1 ") && strings.HasPrefix(line, "###") {
				unitLine = line
			}
		}
		t.Fatalf("resumed unit was not completed and committed\nplan: %s\ngit status:\n%s\nscreen:\n%s",
			unitLine, workspaceGit(t, f.root, "status", "--porcelain"), ui.output())
	}
	// The executor header must also reach the config journal, independently
	// of the assignment table shown by the start dialog.
	files, err := filepath.Glob(filepath.Join(f.root, ".git/docflow/run/*.events.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("journals = %v, %v", files, err)
	}
	for _, path := range files {
		found := false
		for _, line := range strings.Split(workspaceRead(t, path), "\n") {
			var event struct {
				Event     string                        `json:"event"`
				Effective runner.EffectiveConfiguration `json:"effective"`
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != "config" {
				continue
			}
			for _, unit := range event.Effective.Units {
				if unit.Unit == "P001.1.1" && unit.Role == "heavy" && len(unit.Routes) == 1 && unit.Routes[0].Agent == "claude" && unit.Routes[0].Model == "fixture-model" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("executor assignment missing from config in %s", path)
		}
	}
	// The commit lands before the run journals its end; History reads the
	// journals once on entry, so wait for every run to be closed first.
	ended := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && !ended; time.Sleep(20 * time.Millisecond) {
		ended = true
		for _, path := range files {
			if !strings.Contains(workspaceRead(t, path), `"event":"run_end"`) {
				ended = false
			}
		}
	}
	if !ended {
		t.Fatal("a run journal has no run_end")
	}
	ui.send("4")
	workspaceScreen(t, ui, "Юниты: 1 done", "")
	workspaceScreen(t, ui, "План: P001", "")
	ui.send("\r")
	workspaceScreen(t, ui, "REASON-LAST", "")
	for _, want := range []string{"REASON-FIRST", "REASON-MIDDLE", "WORKSPACE-TOOL", "WORKSPACE-MESSAGE", "verify passed"} {
		workspaceScreen(t, ui, want, "")
	}
	ui.send("t")
	workspaceScreen(t, ui, "REASON-FIRST", "REASON-LAST")
	if strings.Contains(ui.output(), "\x1b]0;UNSAFE-TITLE") {
		t.Fatal("reasoning executed a terminal title escape")
	}
	runs, err := LoadPastRuns(f.root)
	if err != nil || len(runs) == 0 {
		t.Fatalf("past runs: %v, %v", runs, err)
	}
	_, history, err := ReplayRun(f.root, runs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	var reasoning []string
	for _, entry := range history.Entries("P001.1.1") {
		if entry.Activity.Kind == agentio.ActivityKindReasoning {
			reasoning = append(reasoning, Sanitize(entry.Activity.Text))
		}
	}
	if len(reasoning) != 1 || reasoning[0] != "REASON-FIRST\nREASON-MIDDLE\nREASON-LAST" {
		t.Fatalf("replayed reasoning blocks = %#v", reasoning)
	}
	ui.send("\x1b")
	workspaceScreen(t, ui, "Прогоны", "")
	ui.quit()
}

func TestWorkspaceTerminalSmallScreens(t *testing.T) {
	f := newWorkspaceFixture(t)
	// Enable styling to verify that the active tab uses reverse video in
	// the real terminal, rather than testing only the NO_COLOR brackets.
	for i, entry := range f.env {
		if entry == "NO_COLOR=1" {
			f.env[i] = "CLICOLOR_FORCE=1"
		}
	}
	ui := startWorkspaceTerminal(t, f, 120, 40)
	workspaceScreen(t, ui, "[1 Проект]", "")
	if !regexp.MustCompile(`\x1b\[[0-9;]*7m`).MatchString(ui.output()) {
		t.Fatal("active tab did not emit reverse-video styling")
	}
	ui.resize(32, 10)
	for _, screen := range []struct{ key, name string }{{"1", "Проект"}, {"2", "Ростер"}, {"3", "Прогон"}, {"4", "История"}} {
		ui.send(screen.key)
		workspaceScreen(t, ui, screen.name, "Terminal too small")
		workspaceScreen(t, ui, "? помощь", "")
		ui.send("?")
		workspaceScreen(t, ui, "Keyboard", "")
		workspaceScreen(t, ui, "and workspace", "")
		ui.send("\x1b")
	}
	ui.send("2")
	for _, pane := range []string{"Варианты", "Доступы", "Цепочки"} {
		workspaceScreen(t, ui, pane, "")
		ui.send("\t")
	}
	for _, width := range []int{100, 110, 119} {
		ui.resize(width, 40)
		for _, pane := range []string{"Варианты", "Доступы", "Цепочки"} {
			workspaceScreen(t, ui, pane, "")
		}
	}
	ui.quit()
}

func TestWorkspaceTerminalProjectMigration(t *testing.T) {
	f := newWorkspaceFixture(t)
	f.policy = filepath.Join(f.root, "docs/roster.toml")
	writeRosterTestFile(t, f.policy, testLegacyRoster)
	ui := startWorkspaceTerminal(t, f, 120, 40)
	workspaceScreen(t, ui, "Queue", "")
	ui.send("2")
	workspaceScreen(t, ui, "Ростер версии 1", "")
	ui.send("\r")
	workspaceScreen(t, ui, "Миграция ростера на версию 4", "")
	if workspaceRead(t, f.policy) != testLegacyRoster {
		t.Fatal("migration preview wrote the policy")
	}
	ui.send("\r")
	workspaceScreen(t, ui, "Доступы", "Миграция ростера на версию 4")
	if workspaceRead(t, f.policy+".bak") != testLegacyRoster {
		t.Fatal("migration lost the original policy")
	}
	if _, err := roster.LoadWorkspace(f.policy, f.root); err != nil {
		t.Fatal(err)
	}
	ui.quit()
}

func TestWorkspaceTerminalScan(t *testing.T) {
	f := newWorkspaceFixture(t)
	log := filepath.ToSlash(filepath.Join(t.TempDir(), "probes"))
	record := "printf '%s\\n' \"$*\" >> " + workspaceQuote(log) + "\n"
	testshim.Script(t, f.bin, "pi", record+`case "$1" in
  --list-models) printf '%s\n' 'provider model context max-out thinking images' 'zai glm-5.3-flash 1M 128K yes no' ;;
  auth) printf '%s\n' '{"status":"ready","provider":"zai"}' ;;
  *) exit 3 ;;
esac
`)
	testshim.Script(t, f.bin, "opencode", record+"printf '%s\\n' '{\"data\":[]}'\n")
	testshim.Script(t, f.bin, "codex", record+"printf 'Logged in using ChatGPT\\n'\n")
	testshim.Script(t, f.bin, "claude", record+"printf '2.1.0\\n'\n")
	ui := startWorkspaceTerminal(t, f, 120, 40)
	workspaceScreen(t, ui, "Queue", "")
	ui.send("2")
	workspaceScreen(t, ui, "Доступы", "")
	ui.send("u")
	workspaceScreen(t, ui, "каталог:", "scanning")
	catalog, err := roster.LoadCatalog(filepath.Join(f.cache, "docflow/roster/catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(catalog.Sources, ","), "pi-list") || !strings.Contains(strings.Join(catalog.Sources, ","), "builtin") {
		t.Fatalf("scan did not replace fixture cache: %v", catalog.Sources)
	}
	probes := workspaceRead(t, filepath.FromSlash(log))
	for _, want := range []string{"--list-models", "auth check --provider zai --json", "login status", "api get /api/model"} {
		if !strings.Contains(probes, want) {
			t.Fatalf("real scanner omitted %q: %s", want, probes)
		}
	}
	ui.quit()
}

func TestWorkspaceTerminalBacklog(t *testing.T) {
	f := newWorkspaceFixture(t)
	w, err := roster.LoadWorkspace(f.policy, f.root)
	if err != nil {
		t.Fatal(err)
	}
	route, err := w.PlanningRoute()
	if err != nil {
		t.Fatal(err)
	}
	received := filepath.Join(t.TempDir(), "prompt")
	testshim.Script(t, f.bin, route.Agent, "printf '%s\\n' \"$@\" > "+workspaceQuote(filepath.ToSlash(received))+"\n")
	ui := startWorkspaceTerminal(t, f, 80, 24)
	workspaceScreen(t, ui, "Queue", "")
	ui.send("b")
	ui.send("+")
	workspaceScreen(t, ui, "Edit Backlog task", "")
	task := "Backlog terminal task; $(literal)"
	if !keyboardTypes(task) {
		// The console of this machine cannot type $ (a Windows layout
		// without the key): the argv still passes the parentheses unexpanded.
		t.Logf("keyboard layout cannot type %q; typing it without $", task)
		task = "Backlog terminal task; (literal)"
	}
	ui.send(task)
	ui.send("\r")
	ui.send("second line")
	ui.send("\x13")
	workspaceScreen(t, ui, task, "Edit Backlog task")
	ui.send("P")
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(received); err == nil {
			break
		}
	}
	workspaceScreen(t, ui, "Queue", "Backlog terminal task")
	got := workspaceRead(t, received)
	want := "возьми строку Backlog в план по скиллу `bsv-docflow-plan`: " + task + "\nsecond line"
	if !strings.Contains(got, want) {
		t.Fatalf("interactive agent argv lost the task: %q", got)
	}
	doc := workspaceRead(t, filepath.Join(f.root, "docs/docflow.md"))
	if !strings.Contains(doc, "- "+task+"\n  second line") {
		t.Fatalf("Backlog not saved:\n%s", doc)
	}
	ui.quit()
}
