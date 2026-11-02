//go:build windows

package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conPTY runs one process on a Windows pseudo console: the same console API
// a terminal window gives docflow tui, driven through pipes by the test.
type conPTY struct {
	t       *testing.T
	console windows.Handle
	process windows.Handle
	input   *os.File
	output  *os.File

	mu   sync.Mutex
	raw  bytes.Buffer
	term *acceptTerminal
	done chan struct{}
}

func startConPTY(t *testing.T, dir string, env []string, width, height int, args ...string) *conPTY {
	t.Helper()
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	var console windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(width), Y: int16(height)}, inRead, outWrite, 0, &console); err != nil {
		t.Skipf("no pseudo console on this Windows: %v", err)
	}
	// The pseudo console holds its own copies of the pipe ends it uses.
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attrs.Delete()
	// The attribute value is the console handle itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&console)), unsafe.Sizeof(console)); err != nil {
		t.Fatal(err)
	}
	var startup windows.StartupInfoEx
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Null standard handles keep the child off the test's redirected stdio:
	// its console is the pseudo console.
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.ProcThreadAttributeList = attrs.List()

	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		t.Fatal(err)
	}
	workDir, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	var info windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(nil, commandLine, nil, nil, false, flags, &block[0], workDir, &startup.StartupInfo, &info); err != nil {
		windows.ClosePseudoConsole(console)
		t.Fatal(err)
	}
	_ = windows.CloseHandle(info.Thread)

	c := &conPTY{
		t: t, console: console, process: info.Process,
		input:  os.NewFile(uintptr(inWrite), "conpty-in"),
		output: os.NewFile(uintptr(outRead), "conpty-out"),
		term:   newAcceptTerminal(width, height),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(c.done)
		buf := make([]byte, 4096)
		for {
			n, err := c.output.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.raw.Write(buf[:n])
				c.term.feed(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(c.close)
	return c
}

func (c *conPTY) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.raw.String()
}

// screen is the visible console reconstructed from the output: conhost
// redraws only changed cells, so text is read from the screen.
func (c *conPTY) screen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.term.String()
}

func (c *conPTY) reset() {
	c.mu.Lock()
	c.raw.Reset()
	c.mu.Unlock()
}

func (c *conPTY) waitFor(text string, timeout time.Duration) {
	c.t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(c.screen(), text) || strings.Contains(c.text(), text) {
			return
		}
	}
	c.t.Fatalf("console does not show %q\nscreen:\n%s", text, c.screen())
}

func (c *conPTY) send(key string) {
	c.t.Helper()
	if _, err := c.input.WriteString(key); err != nil {
		c.t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
}

// quit leaves the workspace. A run that has not published its final state yet
// opens the exit dialog; confirming it stops that run, as a person would.
func (c *conPTY) quit() {
	c.t.Helper()
	c.reset()
	c.send("q")
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(c.text(), "A run is still active") {
			c.send("\r")
			return
		}
	}
}

// wait returns the exit code once the process ends.
func (c *conPTY) wait(timeout time.Duration) uint32 {
	c.t.Helper()
	event, err := windows.WaitForSingleObject(c.process, uint32(timeout/time.Millisecond))
	if err != nil || event != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(c.process, 1)
		c.t.Fatalf("the console process did not exit (%v)\n%q", err, c.text())
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.process, &code); err != nil {
		c.t.Fatal(err)
	}
	return code
}

func (c *conPTY) close() {
	if c.console != 0 {
		// Closing the console ends its output pipe; the reader must keep
		// draining while it does, or ClosePseudoConsole blocks.
		windows.ClosePseudoConsole(c.console)
		c.console = 0
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
		}
		_ = c.input.Close()
		_ = c.output.Close()
	}
	if c.process != 0 {
		_ = windows.TerminateProcess(c.process, 1)
		_ = windows.CloseHandle(c.process)
		c.process = 0
	}
}

// TestWindowsConsolePauseResume runs the built CLI on a real Windows pseudo
// console: the workspace starts a run, Pause stops the agent call and writes
// the checkpoint, the workspace exits cleanly, and a reopened workspace
// continues the interrupted unit to its commit.
func TestWindowsConsolePauseResume(t *testing.T) {
	if testing.Short() {
		t.Skip("console test builds the CLI")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "docflow.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = filepath.Dir(filepath.Dir(wd))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	project, config, scenario := writeRunnableProject(t)
	var env []string
	for _, entry := range os.Environ() {
		// The suite may itself run under the runner; the workspace must not
		// inherit its unit lock.
		if !strings.HasPrefix(strings.ToUpper(entry), "DOCFLOW_RUNNER_UNIT=") {
			env = append(env, entry)
		}
	}
	env = append(env,
		"NO_COLOR=1",
		"DOCFLOW_AGENTS="+config,
		"FAKE_AGENT_SCENARIO="+scenario,
		"DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
	waitFile := func(path string, present bool, timeout time.Duration, c *conPTY) {
		t.Helper()
		for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(path); present == (err == nil) {
				return
			}
		}
		t.Fatalf("file %s present=%v not reached\n%q", path, present, c.text())
	}

	console := startConPTY(t, project, env, 120, 40, bin, "tui")
	console.waitFor("Queue", 30*time.Second)
	console.send("s")
	console.waitFor("Start the current Queue run?", 20*time.Second)
	console.send("\r")
	// The fake agent counts its calls in <scenario>.n before it sleeps.
	waitFile(scenario+".n", true, 30*time.Second, console)
	console.send("p")

	checkpoint := filepath.Join(project, ".git", "docflow", "tui", "checkpoint.json")
	waitFile(checkpoint, true, 30*time.Second, console)
	data, err := os.ReadFile(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var paused struct {
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal(data, &paused); err != nil || paused.Stage != "agent" {
		t.Fatalf("pause checkpoint = %s (%v), want stage agent", data, err)
	}
	lock := filepath.Join(project, ".git", "docflow", "run", "run.lock")
	waitFile(lock, false, 20*time.Second, console)
	// The paused agent slept five seconds before writing a.txt: a surviving
	// child would write it now.
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(filepath.Join(project, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the agent kept working after Pause: %v", err)
	}
	console.quit()
	if code := console.wait(20 * time.Second); code != 0 {
		t.Fatalf("tui exit after the pause = %d\n%q", code, console.text())
	}
	console.close()

	reopened := startConPTY(t, project, env, 120, 40, bin, "tui")
	reopened.waitFor("Recover the saved run", 30*time.Second)
	reopened.send("R")
	waitFile(filepath.Join(project, "a.txt"), true, 60*time.Second, reopened)
	log := exec.Command("git", "log", "-1", "--format=%s")
	log.Dir = project
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		out, _ := log.Output()
		if strings.TrimSpace(string(out)) == "P001.1.1: live control" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the resumed unit was not committed: %q\n%q", out, reopened.text())
		}
		log = exec.Command("git", "log", "-1", "--format=%s")
		log.Dir = project
	}
	waitFile(lock, false, 20*time.Second, reopened)
	reopened.quit()
	if code := reopened.wait(20 * time.Second); code != 0 {
		t.Fatalf("tui exit after the resumed unit = %d\n%q", code, reopened.text())
	}
}

func (c *conPTY) resize(width, height int) {
	c.t.Helper()
	if err := windows.ResizePseudoConsole(c.console, windows.Coord{X: int16(width), Y: int16(height)}); err != nil {
		c.t.Fatal(err)
	}
	c.mu.Lock()
	c.term.resize(width, height)
	c.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
}

// buildConsoleCLI builds the CLI once per test and returns it with the
// environment of a workspace whose fake agent reads the given scenario.
func buildConsoleCLI(t *testing.T, config, scenario string) (string, []string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "docflow.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = filepath.Dir(filepath.Dir(wd))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "DOCFLOW_RUNNER_UNIT=") {
			env = append(env, entry)
		}
	}
	return bin, append(env,
		"NO_COLOR=1",
		"DOCFLOW_AGENTS="+config,
		"FAKE_AGENT_SCENARIO="+scenario,
		"DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
}

func editProjectFile(t *testing.T, project, rel string, edit func(string) string) {
	t.Helper()
	path := filepath.Join(project, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(data))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, project string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = project
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestWindowsConsoleWorkspaceOutcomes drives the outcomes that need a real
// runner or a real file on a Windows pseudo console: a declined start, a
// failing prerequisite, a broken plan on reload, a successful close and a
// screen that is too small and grows back.
func TestWindowsConsoleWorkspaceOutcomes(t *testing.T) {
	if testing.Short() {
		t.Skip("console test builds the CLI")
	}
	project, config, scenario := writeRunnableProject(t)
	bin, env := buildConsoleCLI(t, config, scenario)
	plan := "docs/plans/P001-tui.md"
	lock := filepath.Join(project, ".git", "docflow", "run", "run.lock")

	t.Run("declined start", func(t *testing.T) {
		console := startConPTY(t, project, env, 120, 40, bin, "tui")
		console.waitFor("Queue", 30*time.Second)
		console.send("s")
		console.waitFor("Start the current Queue run?", 20*time.Second)
		console.send("n")
		console.waitFor("start declined; nothing ran", 20*time.Second)
		console.waitFor("run:Cancelled", 20*time.Second)
		if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a declined start left the checkout lock: %v", err)
		}
		if _, err := os.Stat(scenario + ".n"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a declined start called the agent")
		}
		console.quit()
		if code := console.wait(20 * time.Second); code != 0 {
			t.Fatalf("exit = %d", code)
		}
	})

	t.Run("failing prerequisite", func(t *testing.T) {
		editProjectFile(t, project, plan, func(text string) string {
			return strings.Replace(text, "## Goal", "## Prerequisites\n\n- marker file: `test -f marker.txt`\n\n## Goal", 1)
		})
		gitIn(t, project, "commit", "-qam", "add a prerequisite")
		console := startConPTY(t, project, env, 120, 40, bin, "tui")
		console.waitFor("Queue", 30*time.Second)
		console.send("s")
		console.waitFor("Start the current Queue run?", 20*time.Second)
		console.send("\r")
		console.waitFor("prerequisite failed: marker file", 30*time.Second)
		if _, err := os.Stat(scenario + ".n"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("the agent ran past a failed prerequisite")
		}
		console.quit()
		if code := console.wait(20 * time.Second); code != 0 {
			t.Fatalf("exit = %d", code)
		}
		editProjectFile(t, project, plan, func(text string) string {
			return strings.Replace(text, "## Prerequisites\n\n- marker file: `test -f marker.txt`\n\n", "", 1)
		})
		gitIn(t, project, "commit", "-qam", "drop the prerequisite")
	})

	t.Run("broken plan on reload and small screen", func(t *testing.T) {
		console := startConPTY(t, project, env, 120, 40, bin, "tui")
		console.waitFor("Queue", 30*time.Second)
		path := filepath.Join(project, filepath.FromSlash(plan))
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// A queued plan that disappeared from disk breaks the whole snapshot.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		console.reset()
		console.send("r")
		console.waitFor("Cannot load the plan workspace.", 20*time.Second)
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		console.reset()
		console.send("r")
		console.waitFor("P001", 20*time.Second)

		console.reset()
		console.resize(30, 8)
		console.waitFor("Terminal too small", 20*time.Second)
		console.reset()
		console.resize(40, 12)
		console.waitFor("P001", 20*time.Second)
		console.quit()
		if code := console.wait(20 * time.Second); code != 0 {
			t.Fatalf("exit = %d", code)
		}
	})

	t.Run("close", func(t *testing.T) {
		editProjectFile(t, project, plan, func(text string) string {
			return strings.Replace(text, "### [ ] P001.1.1", "### [x] P001.1.1", 1)
		})
		gitIn(t, project, "commit", "-qam", "finish the unit")
		console := startConPTY(t, project, env, 120, 40, bin, "tui")
		console.waitFor("Queue", 30*time.Second)
		console.send("j")
		console.waitFor("selected:plan:P001", 20*time.Second)
		console.send("C")
		console.waitFor("docs/archive/P001-tui.md", 30*time.Second)
		if _, err := os.Stat(filepath.Join(project, "docs", "archive", "P001-tui.md")); err != nil {
			t.Fatalf("close wrote no archive entry: %v", err)
		}
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(plan))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("close kept the plan: %v", err)
		}
		if queue, _ := os.ReadFile(filepath.Join(project, "docs", "docflow.md")); strings.Contains(string(queue), "- P001") {
			t.Fatalf("close kept the Queue line:\n%s", queue)
		}
		console.quit()
		if code := console.wait(20 * time.Second); code != 0 {
			t.Fatalf("exit = %d", code)
		}
	})
}
