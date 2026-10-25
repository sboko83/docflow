package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

const (
	NotificationTimeout = 5 * time.Second
	prerequisiteTimeout = 60 * time.Second
)

var stopNotify = map[int]string{
	clierrors.EXIT_BLOCKED: "a unit is blocked",
	clierrors.EXIT_FORMAT:  "the plan no longer matches the tree",
	clierrors.EXIT_ORDER:   "stopped on an order violation",
	clierrors.EXIT_VERIFY:  "checks kept failing",
	clierrors.EXIT_AGENT:   "the agent failed",
}

func agentCallTimeout(plan *format.Plan, agent map[string]any) time.Duration {
	if plan != nil {
		if value, ok := plan.Meta["timeout"]; ok {
			if seconds, err := strconv.Atoi(value); err == nil {
				return time.Duration(seconds) * time.Second
			}
		}
	}
	seconds, _ := numericEpoch(agent["timeout"])
	return time.Duration(seconds * float64(time.Second))
}

var currentPlatform = runtime.GOOS

// runNotificationProcess is replaceable in tests so notification behavior can
// be checked without launching desktop tools.
var runNotificationProcess = func(argv []string, timeout time.Duration) (bool, error) {
	if len(argv) == 0 {
		return false, nil
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		if isMissingExecutable(err) {
			return false, err
		}
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, argv[1:]...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err = command.Run()
	return true, err
}

func say(parts ...any) { sayTo(os.Stdout, os.Stderr, false, parts...) }

func sayError(parts ...any) { sayTo(os.Stdout, os.Stderr, true, parts...) }

func sayTo(stdout, stderr io.Writer, useStderr bool, parts ...any) {
	stream := stdout
	if useStderr {
		stream = stderr
	}
	if stream == nil {
		return
	}
	_, _ = fmt.Fprintln(stream, parts...)
	flushWriter(stream)
}

func runDir(root string) (string, error) {
	state, err := cycle.StateDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(state), "run"), nil
}

func nextRunCallLog(logs, uid string, replan bool) (string, error) {
	prefix := uid + "."
	if replan {
		prefix += "replan"
	}
	return nextCallLog(logs, prefix)
}

func nextCallLog(logs, prefix string) (string, error) {
	entries, err := os.ReadDir(logs)
	if err != nil {
		return "", err
	}
	max := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".out") {
			continue
		}
		number, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".out"))
		if err == nil && number > max {
			max = number
		}
	}
	return filepath.Join(logs, fmt.Sprintf("%s%d.out", prefix, max+1)), nil
}

func stopFile(root string) (string, error) {
	directory, err := runDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "stop"), nil
}

// runLock allows one runner for a repository and returns the release action.
func runLock(root string) (func(), error) {
	directory, err := runDir(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "run.lock")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	if text, err := os.ReadFile(path); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(text)))
		if parseErr == nil && pid != 0 && pid != os.Getpid() && pidAlive(pid) {
			return nil, clierrors.DocflowError{
				Message: fmt.Sprintf("run: another runner is active (pid %d, %s)", pid, path),
				Code:    clierrors.EXIT_FORMAT,
			}
		}
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o666); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func requestStop(root string, stdout, stderr io.Writer) (int, error) {
	directory, err := runDir(root)
	if err != nil {
		return clierrors.EXIT_FORMAT, err
	}
	lockPath := filepath.Join(directory, "run.lock")
	data, readErr := os.ReadFile(lockPath)
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if readErr != nil || parseErr != nil || pid <= 0 || !pidAlive(pid) {
		return clierrors.EXIT_NOTHING, clierrors.DocflowError{
			Message: "run: no active runner", Code: clierrors.EXIT_NOTHING,
		}
	}
	path := filepath.Join(directory, "stop")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return clierrors.EXIT_FORMAT, err
	}
	if err := file.Close(); err != nil {
		return clierrors.EXIT_FORMAT, err
	}
	sayTo(stdout, stderr, false, fmt.Sprintf("run: runner %d stops after its current unit", pid))
	return clierrors.EXIT_OK, nil
}

func watchKeys(root string, stream io.Reader, progress *Progress, totals map[string]any, currentUnit ...func() string) {
	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) == "s" {
			if path, pathErr := stopFile(root); pathErr == nil {
				_ = os.MkdirAll(filepath.Dir(path), 0o755)
				file, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o666)
				if openErr == nil {
					_ = file.Close()
				}
			}
			unit, _ := totals["unit"].(string)
			if len(currentUnit) > 0 {
				unit = currentUnit[0]()
			}
			if unit == "" {
				unit = "the current unit"
			}
			progress.line("run: s + Enter received; the plan stops after "+unit+" (Ctrl-C stops now)", false, "yellow")
			break
		}
		if err != nil {
			return
		}
	}
	for {
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
	}
}

// noEcho disables terminal echo until the returned restore function is called.
func noEcho(stream io.Reader) func() {
	file, ok := stream.(*os.File)
	if !ok {
		return func() {}
	}
	return disableEcho(file)
}

func runnerPrompt(root string, plan *format.Plan, unit *format.Unit, agent map[string]any, permissions string) (string, error) {
	var repos []string
	if role := unit.Field("Role"); role != nil && role.Value == "review" {
		var err error
		repos, err = lint.ProjectRepos(root)
		if err != nil {
			return "", err
		}
	}
	text, err := cycle.PromptText(root, plan, unit, repos)
	if err != nil {
		return "", err
	}
	if start := strings.LastIndex(text, "## Cycle\n"); start >= 0 {
		text = text[:start] + strings.ReplaceAll(agents.RunnerCycle, "{id}", unit.ID)
	}
	if permissions == "" {
		permissions = "auto"
	}
	instructions, _ := agent["instructions"].(string)
	if instructions != "" && permissions == "auto" {
		text += "\n" + strings.TrimRightFunc(instructions, unicode.IsSpace) + "\n"
	}
	return text, nil
}

func runPrerequisites(root string, plan *format.Plan, progress *Progress) int {
	return runPrerequisitesWithTimeout(root, plan, progress, prerequisiteTimeout)
}

func runPrerequisitesWithTimeout(root string, plan *format.Plan, progress *Progress, timeout time.Duration) int {
	failed := make([]string, 0)
	planStarted := format.PlanStarted(plan)
	for _, prerequisite := range format.PlanPrerequisites(plan) {
		if prerequisite.Once && planStarted {
			progress.line(fmt.Sprintf("prerequisite skipped (once, plan started): %s", prerequisite.Text), false, "yellow")
			continue
		}
		if prerequisite.Command == nil || *prerequisite.Command == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		argv := cycle.PlanShell(*prerequisite.Command)
		command := exec.CommandContext(ctx, argv[0], argv[1:]...)
		command.Dir = root
		command.Stdin = strings.NewReader("")
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		runErr := command.Run()
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if timedOut {
			failed = append(failed, fmt.Sprintf("prerequisite failed: %s (%s, timeout)", prerequisite.Text, *prerequisite.Command))
			continue
		}
		if runErr != nil {
			combined := strings.ToValidUTF8(stdout.String()+stderr.String(), "�")
			lines := strings.Split(combined, "\n")
			last := ""
			for i := len(lines) - 1; i >= 0; i-- {
				if candidate := strings.TrimSpace(lines[i]); candidate != "" {
					last = candidate
					break
				}
			}
			message := fmt.Sprintf("prerequisite failed: %s (%s, exit %d)", prerequisite.Text, *prerequisite.Command, commandExitCode(runErr))
			if last != "" {
				message += ": " + last
			}
			failed = append(failed, message)
		}
	}
	for _, line := range failed {
		progress.line(line, true, "")
	}
	if len(failed) > 0 {
		return clierrors.EXIT_FORMAT
	}
	return clierrors.EXIT_OK
}

func commandExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func confirmRunStart(plan *format.Plan, yes bool, progress *Progress) int {
	return confirmRunStartWithIO(plan, yes, progress, os.Stdin, os.Stdout)
}

func confirmRunStartWithIO(plan *format.Plan, yes bool, progress *Progress, stdin io.Reader, stdout io.Writer) int {
	mode := format.PlanPermissions(plan)
	planStarted := format.PlanStarted(plan)
	items := make([]string, 0)
	for _, prerequisite := range format.PlanPrerequisites(plan) {
		if prerequisite.Once && planStarted {
			continue
		}
		if prerequisite.Command == nil {
			items = append(items, prerequisite.Text)
		}
	}
	if mode != "full" && len(items) == 0 {
		return clierrors.EXIT_OK
	}
	if mode == "full" {
		progress.line("permissions: full — the agent runs without a sandbox", false, "yellow")
	}
	if len(items) > 0 {
		progress.line("prerequisites only a human provides:", false, "yellow")
		for _, item := range items {
			progress.line("  - "+item, false, "yellow")
		}
	}
	if yes {
		return clierrors.EXIT_OK
	}
	if !isTerminal(stdin) {
		progress.line("run: no terminal to confirm the start; pass --yes", true, "")
		return clierrors.EXIT_FORMAT
	}
	if _, err := io.WriteString(stdout, "continue? [y/N] "); err != nil {
		return clierrors.EXIT_FORMAT
	}
	flushWriter(stdout)
	answer, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		answer = ""
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "y" || answer == "yes" {
		return clierrors.EXIT_OK
	}
	progress.line("run: not confirmed; nothing ran", true, "")
	return clierrors.EXIT_NOTHING
}

func applescript(text string) string {
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, `"`, `\"`)
	return `"` + text + `"`
}

func spawn(argv []string) bool {
	started, _ := runNotificationProcess(argv, NotificationTimeout)
	return started
}

func notify(text string, off bool, stderr io.Writer) {
	if off || os.Getenv("DOCFLOW_NOTIFY") == "0" {
		return
	}
	sent := false
	switch currentPlatform {
	case "darwin":
		sent = spawn([]string{"osascript", "-e", "display notification " + applescript(text) + ` with title "docflow"`})
	case "linux":
		sent = spawn([]string{"notify-send", "docflow", text})
	}
	if !sent && isTerminal(stderr) {
		_, _ = io.WriteString(stderr, "\a")
		flushWriter(stderr)
	}
}

func prerequisiteLines(plan *format.Plan) []string {
	prerequisites := format.PlanPrerequisites(plan)
	lines := make([]string, 0, len(prerequisites))
	for _, prerequisite := range prerequisites {
		line := prerequisite.Text
		if prerequisite.Command != nil {
			line += ": `" + *prerequisite.Command + "`"
		}
		if prerequisite.Once {
			line += " (once)"
		}
		lines = append(lines, line)
	}
	return lines
}

func printPrerequisites(plan *format.Plan, stdout io.Writer) {
	items := prerequisiteLines(plan)
	if len(items) == 0 {
		return
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	_, _ = fmt.Fprintln(stdout, "prerequisites:")
	for _, item := range items {
		_, _ = fmt.Fprintln(stdout, "  - "+item)
	}
	flushWriter(stdout)
}

func isMissingExecutable(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
}
