package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type benchRoute struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

type benchResult struct {
	V              int          `json:"v"`
	RunID          string       `json:"run_id"`
	TS             float64      `json:"ts"`
	Task           string       `json:"task"`
	TaskSHA        string       `json:"task_sha"`
	Arm            string       `json:"arm"`
	Route          *benchRoute  `json:"route,omitempty"`
	RosterSHA      string       `json:"roster_sha,omitempty"`
	Chains         []string     `json:"chains,omitempty"`
	DocflowVersion string       `json:"docflow_version"`
	DocflowCommit  string       `json:"docflow_commit"`
	DocflowDirty   bool         `json:"docflow_dirty"`
	Status         string       `json:"status"`
	Oracle         oracleResult `json:"oracle"`
	Seconds        float64      `json:"seconds"`
	Cost           *float64     `json:"cost"`
	Tokens         int          `json:"tokens"`
	Units          unitResult   `json:"units"`
	Attempts       int          `json:"attempts"`
	Replans        int          `json:"replans"`
	TestChanges    int          `json:"test_changes"`
	CheckChanges   int          `json:"check_changes"`
	Unverified     []string     `json:"unverified"`
	Diff           diffResult   `json:"diff"`
}

type oracleResult struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Total  int `json:"total"`
}
type unitResult struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Blocked int `json:"blocked"`
}
type diffResult struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
}

type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message }

func reportCommand(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	results := flags.String("results", "bench/results", "results directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: bench report [--results DIR]")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	dir := resolve(root, *results)
	rows, err := readResults(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		return err
	}
	readme := renderTrend(rows)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		return err
	}
	_, err = io.WriteString(out, fmt.Sprintf("wrote %s\n", filepath.ToSlash(filepath.Join(*results, "README.md"))))
	return err
}

func readResults(path string) ([]benchResult, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []benchResult
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4*1024*1024)
	for line := 1; s.Scan(); line++ {
		var row benchResult
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), line, err)
		}
		if row.V != 1 {
			return nil, fmt.Errorf("%s:%d: unsupported result version %d", filepath.Base(path), line, row.V)
		}
		rows = append(rows, row)
	}
	return rows, s.Err()
}

func renderTrend(rows []benchResult) string {
	byTask := map[string][]benchResult{}
	for _, row := range rows {
		byTask[row.Task] = append(byTask[row.Task], row)
	}
	tasks := make([]string, 0, len(byTask))
	for task := range byTask {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	var b strings.Builder
	b.WriteString("# Benchmark trend\n\nGenerated from `runs.jsonl`. Rows are newest first; deltas compare with the previous row for the same task and arm.\n")
	for _, task := range tasks {
		items := byTask[task]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].TS == items[j].TS {
				return items[i].RunID < items[j].RunID
			}
			return items[i].TS < items[j].TS
		})
		b.WriteString("\n## " + task + "\n\n")
		b.WriteString("| Date | docflow | Arm | Oracle | Δ oracle | Cost | Δ cost | Minutes | Attempts | Replans | Task |\n|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
		previous := map[string]benchResult{}
		lines := make([]string, 0, len(items))
		for _, row := range items {
			key := row.Arm
			marker := ""
			if prev, ok := previous[key]; ok {
				if prev.TaskSHA != row.TaskSHA {
					marker = " задача изменена"
				}
			}
			prev, hasPrev := previous[key]
			oracle := "—"
			if row.Oracle.Total > 0 {
				oracle = fmt.Sprintf("%.0f%%", 100*float64(row.Oracle.Passed)/float64(row.Oracle.Total))
			}
			deltaOracle, deltaCost := "—", "—"
			if hasPrev && prev.TaskSHA == row.TaskSHA {
				if row.Oracle.Total > 0 && prev.Oracle.Total > 0 {
					deltaOracle = fmt.Sprintf("%+.0f pp", 100*float64(row.Oracle.Passed)/float64(row.Oracle.Total)-100*float64(prev.Oracle.Passed)/float64(prev.Oracle.Total))
				}
				if row.Cost != nil && prev.Cost != nil {
					deltaCost = fmt.Sprintf("%+.2f", *row.Cost-*prev.Cost)
				}
			}
			cost := "—"
			if row.Cost != nil {
				cost = fmt.Sprintf("%.2f", *row.Cost)
			}
			commit := row.DocflowCommit
			if len(commit) > 8 {
				commit = commit[:8]
			}
			version := row.DocflowVersion
			if commit != "" {
				version += " / " + commit
			}
			date := time.Unix(0, int64(row.TS*float64(time.Second))).UTC().Format("2006-01-02")
			lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %.1f | %d | %d | %s%s |\n", date, escapeCell(version), escapeCell(row.Arm), oracle, deltaOracle, cost, deltaCost, row.Seconds/60, row.Attempts, row.Replans, row.TaskSHA[:min(8, len(row.TaskSHA))], marker))
			previous[key] = row
		}
		for i := len(lines) - 1; i >= 0; i-- {
			b.WriteString(lines[i])
		}
	}
	return b.String()
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func appendResult(dir string, row benchResult) error {
	if row.V == 0 {
		row.V = 1
	}
	if row.TS == 0 {
		row.TS = float64(time.Now().UnixNano()) / float64(time.Second)
	}
	if row.Cost == nil { /* null is part of the public schema */
	}
	if row.Unverified == nil {
		row.Unverified = []string{}
	} else {
		for i := range row.Unverified {
			row.Unverified[i] = redactAbsolutePaths(row.Unverified[i])
		}
	}
	if row.Chains == nil && row.Arm == "docflow" {
		row.Chains = []string{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "runs.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	encErr := json.NewEncoder(f).Encode(row)
	if encErr == nil {
		encErr = f.Sync()
	}
	closeErr := f.Close()
	if encErr != nil {
		return encErr
	}
	return closeErr
}

func resultRunID(start time.Time, resultsDir string) (string, error) {
	base := start.UTC().Format("20060102T150405Z")
	for n := 1; ; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		if _, err := os.Stat(filepath.Join(resultsDir, id)); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(filepath.Join(resultsDir, "runs.jsonl")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			rows, err := readResults(filepath.Join(resultsDir, "runs.jsonl"))
			if err != nil {
				return "", err
			}
			used := false
			for _, row := range rows {
				if row.RunID == id {
					used = true
					break
				}
			}
			if !used {
				return id, nil
			}
		} else if err != nil {
			return "", err
		}
	}
}

func ensureResultsClean(root, results string) error {
	cmd := exec.Command("git", "status", "--porcelain", "--", results)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git status results: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return &exitError{code: 2, message: fmt.Sprintf("results directory has uncommitted changes:\n%s", strings.TrimSpace(string(out)))}
	}
	return nil
}

func commitResults(root, results, runID string) error {
	add := exec.Command("git", "add", "--", results)
	add.Dir = root
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add results: %s: %w", strings.TrimSpace(string(out)), err)
	}
	commit := exec.Command("git", "commit", "-m", "bench: run "+runID, "--", results)
	commit.Dir = root
	if out, err := commit.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit results: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func writeRunSummary(dir string, rows []benchResult, oracleTails map[string][]string) error {
	var b strings.Builder
	b.WriteString("# Benchmark run " + filepath.Base(dir) + "\n\n| Task | Arm | Route / roster | Oracle | Cost | Minutes | Units | Attempts | Replans |\n|---|---|---|---:|---:|---:|---:|---:|---:|\n")
	for _, row := range rows {
		oracle := fmt.Sprintf("%d/%d", row.Oracle.Passed, row.Oracle.Total)
		cost := "—"
		if row.Cost != nil {
			cost = fmt.Sprintf("%.2f", *row.Cost)
		}
		route := "roster"
		if row.Route != nil {
			route = row.Route.Agent + " | " + row.Route.Model + " | " + row.Route.Effort
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %.1f | %d (%d done, %d blocked) | %d | %d |\n", escapeCell(row.Task), escapeCell(row.Arm), escapeCell(route), oracle, cost, row.Seconds/60, row.Units.Total, row.Units.Done, row.Units.Blocked, row.Attempts, row.Replans)
		if tail := oracleTails[row.Task+"/"+row.Arm]; len(tail) > 0 {
			b.WriteString("\n### " + row.Task + " / " + row.Arm + " oracle failures\n\n```text\n")
			if len(tail) > 40 {
				tail = tail[len(tail)-40:]
			}
			for i := range tail {
				tail[i] = redactAbsolutePaths(tail[i])
			}
			b.WriteString(strings.Join(tail, "\n"))
			b.WriteString("\n```\n")
		}
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(b.String()), 0o644)
}

var absolutePathPattern = regexp.MustCompile(`(?:[A-Za-z]:)?[/\\][^\s:]+`)

func redactAbsolutePaths(s string) string {
	return absolutePathPattern.ReplaceAllString(s, "work/bench/<run-id>/…")
}

func taskDigest(plan, oracle, solution, base string) (string, error) {
	h := sha256.New()
	for _, input := range []struct{ label, path string }{{"plan", plan}, {"oracle", oracle}, {"solution", solution}, {"base", base}} {
		fmt.Fprintf(h, "%s\x00", input.label)
		if err := hashTree(h, input.path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashTree(w io.Writer, root string) error {
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported: %s", filepath.Base(path))
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\x00%d\x00", rel, len(data))
		_, _ = w.Write(data)
	}
	return nil
}
