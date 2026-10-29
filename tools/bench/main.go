package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/cycle"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "validate":
		err = validateCommand(os.Args[2:], os.Stdout)
	case "run":
		err = runCommand(os.Args[2:], os.Stdout)
	case "report":
		err = reportCommand(os.Args[2:], os.Stdout)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		var coded *exitError
		if errors.As(err, &coded) {
			os.Exit(coded.code)
		}
		os.Exit(1)
	}
}

func usage(w io.Writer) { fmt.Fprintln(w, "usage: bench {validate|run|report} [options]") }

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
			if readErr == nil && strings.Contains(string(data), "module bsv-docflow") {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find bsv-docflow go.mod above %s", wd)
		}
	}
}

func validateCommand(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	task := flags.String("task", "", "task name or all")
	tasksDir := flags.String("tasks", "bench/tasks", "task directory")
	baseDir := flags.String("base", "bench/base", "base project directory")
	noSolution := flags.Bool("no-solution", false, "skip solution validation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *task == "" {
		return fmt.Errorf("usage: bench validate --task NAME|all [--tasks DIR] [--base DIR] [--no-solution]")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	base := resolve(root, *baseDir)
	tasks := resolve(root, *tasksDir)
	names, err := taskNames(tasks, *task)
	if err != nil {
		return err
	}
	for _, name := range names {
		counts, err := validateTask(root, base, filepath.Join(tasks, name), name, *noSolution)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if *noSolution {
			fmt.Fprintf(out, "%s: base %d/%d, solution skipped, lint ok\n", name, counts.base.passed, counts.base.total)
		} else {
			fmt.Fprintf(out, "%s: base %d/%d, solution %d/%d, lint ok\n", name, counts.base.passed, counts.base.total, counts.solution.passed, counts.solution.total)
		}
	}
	return nil
}

type testCounts struct{ passed, total int }
type validationCounts struct{ base, solution testCounts }

func parseGoTestJSON(data []byte) testCounts {
	counts := testCounts{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		var event struct {
			Action string
			Test   string
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Test == "" || seen[event.Test] {
			continue
		}
		if event.Action != "pass" && event.Action != "fail" {
			continue
		}
		seen[event.Test] = true
		counts.total++
		if event.Action == "pass" {
			counts.passed++
		}
	}
	return counts
}

func resolve(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func taskNames(tasks, requested string) ([]string, error) {
	if requested != "all" {
		return []string{requested}, nil
	}
	entries, err := os.ReadDir(tasks)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no tasks found in %s", tasks)
	}
	return names, nil
}

func validateTask(root, base, taskDir, name string, noSolution bool) (validationCounts, error) {
	var counts validationCounts
	for _, file := range []string{"plan.md", "oracle", "solution"} {
		if file == "solution" && noSolution {
			continue
		}
		if _, err := os.Stat(filepath.Join(taskDir, file)); err != nil {
			return counts, fmt.Errorf("missing %s: %w", file, err)
		}
	}
	plan, err := os.ReadFile(filepath.Join(taskDir, "plan.md"))
	if err != nil {
		return counts, err
	}
	if !strings.Contains(string(plan), "id: P001") {
		return counts, fmt.Errorf("plan.md must have id P001")
	}
	if strings.Contains(string(plan), "executor:") || strings.Contains(string(plan), "fallback:") || strings.Contains(string(plan), "review:") || strings.Contains(string(plan), "acceptance:") || strings.Contains(string(plan), "replan:") {
		return counts, fmt.Errorf("plan.md must not contain route keys")
	}

	baseCopy, err := os.MkdirTemp("", "docflow-bench-base-")
	if err != nil {
		return counts, err
	}
	defer os.RemoveAll(baseCopy)
	if err := materialize(base, baseCopy); err != nil {
		return counts, fmt.Errorf("materialize base: %w", err)
	}
	if err := overlay(filepath.Join(taskDir, "oracle"), baseCopy); err != nil {
		return counts, fmt.Errorf("copy oracle: %w", err)
	}
	counts.base = runOracle(baseCopy)
	if counts.base.total == 0 {
		return counts, fmt.Errorf("oracle produced no test events (build or package failure?)")
	}
	if counts.base.passed == counts.base.total {
		return counts, fmt.Errorf("oracle passes on base (%d/%d); expected at least one failure", counts.base.passed, counts.base.total)
	}

	if !noSolution {
		solutionCopy, err := os.MkdirTemp("", "docflow-bench-solution-")
		if err != nil {
			return counts, err
		}
		defer os.RemoveAll(solutionCopy)
		if err := materialize(base, solutionCopy); err != nil {
			return counts, err
		}
		if err := overlay(filepath.Join(taskDir, "solution"), solutionCopy); err != nil {
			return counts, fmt.Errorf("copy solution: %w", err)
		}
		if err := overlay(filepath.Join(taskDir, "oracle"), solutionCopy); err != nil {
			return counts, fmt.Errorf("copy oracle: %w", err)
		}
		counts.solution = runOracle(solutionCopy)
		if counts.solution.total == 0 || counts.solution.passed != counts.solution.total {
			return counts, fmt.Errorf("oracle fails on solution (%d/%d)", counts.solution.passed, counts.solution.total)
		}
	}
	if err := validatePlan(root, base, taskDir, name, plan); err != nil {
		return counts, err
	}
	return counts, nil
}

func materialize(base, dest string) error {
	if err := copyTree(base, dest); err != nil {
		return err
	}
	for _, args := range [][]string{{"init"}, {"config", "user.name", "docflow benchmark"}, {"config", "user.email", "bench@localhost"}, {"add", "-A"}, {"commit", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dest
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(output)), err)
		}
	}
	return nil
}

func copyTree(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported: %s", rel)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func overlay(source, dest string) error { return copyTree(source, dest) }

func runOracle(root string) testCounts {
	argv := cycle.PlanShell("go test -json ./benchoracle/...")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	counts := parseGoTestJSON(output)
	if counts.total == 0 && err != nil {
		counts.total = oracleTestCount(filepath.Join(root, "benchoracle"))
	}
	return counts
}

func oracleTestCount(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "func Test") && strings.Contains(trimmed, "(") {
				count++
			}
		}
		return nil
	})
	return count
}

func validatePlan(root, base, taskDir, name string, plan []byte) error {
	dir, err := os.MkdirTemp("", "docflow-bench-lint-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := copyTree(base, dir); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(root, "cmd"), filepath.Join(dir, "cmd")); err != nil {
		return err
	}
	// Use the checkout CLI so lint behavior matches this source tree.
	bin := filepath.Join(dir, "docflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build docflow: %s: %w", strings.TrimSpace(string(output)), err)
	}
	init := exec.Command(bin, "init", "--agent", "codex")
	init.Dir = dir
	if output, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("docflow init: %s: %w", strings.TrimSpace(string(output)), err)
	}
	docflowPath := filepath.Join(dir, "docs", "docflow.md")
	docflow, err := os.ReadFile(docflowPath)
	if err != nil {
		return err
	}
	docflowText := strings.Replace(string(docflow), "## Queue\n", "## Queue\n- P001\n", 1)
	if err := os.WriteFile(docflowPath, []byte(docflowText), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "plans", "P001-"+name+".md"), plan, 0o644); err != nil {
		return err
	}
	lint := exec.Command(bin, "lint")
	lint.Dir = dir
	if output, err := lint.CombinedOutput(); err != nil {
		return fmt.Errorf("docflow lint: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}
