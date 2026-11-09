package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

type replayOptions struct {
	repo, from, to, plan, oracle string
	backlog                      int
}

func (r *replayOptions) flags(flags *flag.FlagSet) {
	flags.StringVar(&r.repo, "replay", "", "source repository for a closed-plan replay")
	flags.StringVar(&r.from, "from", "", "source commit before the plan was added")
	flags.StringVar(&r.to, "to", "", "source commit that closed the plan")
	flags.StringVar(&r.plan, "plan", "", "historical path docs/plans/Pxxx-slug.md")
	flags.IntVar(&r.backlog, "backlog", 0, "plan from this 1-based Backlog item at --from instead of the historical plan")
	flags.StringVar(&r.oracle, "oracle-command", "go test ./...", "replay check after overlaying the closing commit's test files")
}

var replayPlanRE = regexp.MustCompile(`^docs/plans/(P[0-9]+)(?:-[^/]+)?\.md$`)

func (r replayOptions) check() error {
	if r.repo == "" {
		if r.from != "" || r.to != "" || r.plan != "" || r.backlog != 0 || r.oracle != "go test ./..." {
			return fmt.Errorf("--from, --to, --plan, --backlog and --oracle-command require --replay")
		}
		return nil
	}
	if r.from == "" || r.to == "" || !replayPlanRE.MatchString(r.plan) || r.backlog < 0 || strings.TrimSpace(r.oracle) == "" {
		return fmt.Errorf("replay requires --from COMMIT --to COMMIT --plan docs/plans/Pxxx-slug.md [--backlog N] [--oracle-command CMD]")
	}
	return nil
}

type replaySource struct {
	repo, from, to, plan, id, backlog string
	tests                             []string
}

// Only immutable Git objects are read. No clone, checkout, refs or future
// objects are copied into the arm's newly initialized repository.
func (r replayOptions) inspect(root string) (replaySource, error) {
	if err := r.check(); err != nil {
		return replaySource{}, err
	}
	if r.repo == "" {
		return replaySource{}, fmt.Errorf("replay repository is required")
	}
	s := replaySource{repo: resolve(root, r.repo), id: replayPlanRE.FindStringSubmatch(r.plan)[1]}
	for _, ref := range []struct {
		text string
		dest *string
	}{{r.from, &s.from}, {r.to, &s.to}} {
		sha, err := gitOutput(s.repo, "rev-parse", "--verify", "--end-of-options", ref.text+"^{commit}")
		if err != nil {
			return s, fmt.Errorf("replay commit %q: %w", ref.text, err)
		}
		*ref.dest = strings.TrimSpace(sha)
	}
	if _, err := gitOutput(s.repo, "merge-base", "--is-ancestor", s.from, s.to); err != nil {
		return s, fmt.Errorf("--from must be an ancestor of --to")
	}
	if _, err := gitOutput(s.repo, "cat-file", "-e", s.from+":"+r.plan); err == nil {
		return s, fmt.Errorf("--from already contains the plan; choose its preceding commit")
	}
	if _, err := gitOutput(s.repo, "cat-file", "-e", s.to+":"+r.plan); err == nil {
		return s, fmt.Errorf("--to still contains the plan; choose its closing commit")
	}
	archive := strings.Replace(r.plan, "docs/plans/", "docs/archive/", 1)
	if _, err := gitOutput(s.repo, "cat-file", "-e", s.to+":"+archive); err != nil {
		return s, fmt.Errorf("--to has no archive entry %s", archive)
	}
	added, err := gitOutput(s.repo, "log", "--reverse", "--format=%H", "--diff-filter=A", s.from+".."+s.to, "--", r.plan)
	if err != nil || strings.TrimSpace(added) == "" {
		return s, fmt.Errorf("plan was not added between --from and --to")
	}
	plan, err := gitOutput(s.repo, "show", strings.Fields(added)[0]+":"+r.plan)
	if err != nil {
		return s, err
	}
	p, err := format.ParsePlan(plan, r.plan)
	if err != nil {
		return s, err
	}
	if p.Meta["id"] != s.id || len(p.Units) == 0 {
		return s, fmt.Errorf("historical plan has no units or mismatched id")
	}
	for _, unit := range p.Units {
		if unit.Status != " " {
			return s, fmt.Errorf("first plan snapshot already has unit progress: %s", unit.ID)
		}
	}
	// Historical executor choices are not experimental inputs. Apply the
	// benchmark roster later, and always preserve sandboxed execution.
	var lines []string
	front := false
	for i, line := range strings.Split(strings.ReplaceAll(plan, "\r\n", "\n"), "\n") {
		if line == "---" {
			if i == 0 {
				front = true
			} else {
				front = false
			}
			lines = append(lines, line)
			continue
		}
		if front {
			key, _, _ := strings.Cut(line, ":")
			if key == "status" {
				line = "status: active"
			}
			if key == "permissions" {
				line = "permissions: auto"
			}
			if key == "executor" || key == "fallback" || key == "cheap" || key == "heavy" || key == "review" || key == "acceptance" || key == "replan" {
				continue
			}
		}
		if strings.HasPrefix(line, "Executor:") || strings.HasPrefix(line, "Model:") {
			continue
		}
		lines = append(lines, line)
	}
	s.plan = strings.Join(lines, "\n")
	changed, err := gitOutput(s.repo, "diff", "--no-renames", "--name-only", "--diff-filter=ACM", "-z", s.from, s.to, "--")
	if err != nil {
		return s, err
	}
	for _, path := range strings.Split(changed, "\x00") {
		if cycle.IsTestPath(path) {
			s.tests = append(s.tests, path)
		}
	}
	if len(s.tests) == 0 {
		return s, fmt.Errorf("no added or modified test files between --from and --to")
	}
	if r.backlog > 0 {
		text, err := gitOutput(s.repo, "show", s.from+":"+format.DocflowFilePath)
		if err != nil {
			return s, err
		}
		d, err := format.ParseDocflow(text, format.DocflowFilePath)
		if err != nil {
			return s, err
		}
		s.backlog, err = d.BacklogText(r.backlog - 1)
		if err != nil {
			return s, err
		}
	}
	return s, nil
}

func (r replayOptions) prepare(root, dest, name string) (taskInput, error) {
	s, err := r.inspect(root)
	if err != nil {
		return taskInput{}, err
	}
	base, solution, oracle := filepath.Join(dest, "base"), filepath.Join(dest, "solution"), filepath.Join(dest, "oracle")
	if err := snapshotGit(s.repo, s.from, base); err != nil {
		return taskInput{}, err
	}
	if err := snapshotGit(s.repo, s.to, solution); err != nil {
		return taskInput{}, err
	}
	for _, path := range s.tests {
		data, err := os.ReadFile(filepath.Join(solution, filepath.FromSlash(path)))
		if err != nil {
			return taskInput{}, err
		}
		target := filepath.Join(oracle, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return taskInput{}, err
		}
		info, err := os.Stat(filepath.Join(solution, filepath.FromSlash(path)))
		if err != nil {
			return taskInput{}, err
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return taskInput{}, err
		}
	}
	// Existing archives stay as context; only the replay plan enters the
	// queue. Other pending plans would confound the experiment.
	if data, err := os.ReadFile(filepath.Join(base, "docs", "docflow.md")); err == nil {
		d, err := format.ParseDocflow(string(data), format.DocflowFilePath)
		if err != nil {
			return taskInput{}, err
		}
		if len(d.Queue) > 0 {
			return taskInput{}, fmt.Errorf("--from has queued plans; choose a clean plan boundary")
		}
	}
	h := sha256.Sum256([]byte(s.from + "\x00" + s.to + "\x00" + s.plan + "\x00" + s.backlog + "\x00" + r.oracle + "\x00" + strings.Join(s.tests, "\n")))
	return taskInput{name: name, dir: dest, base: base, plan: s.plan, digest: hex.EncodeToString(h[:]), oracleCommand: r.oracle, backlog: s.backlog, planID: s.id, planPath: r.plan}, nil
}

func snapshotGit(repo, commit, dest string) error {
	// Gitlinks are absent from git archive. Refuse rather than silently
	// benchmarking an incomplete multi-repository project.
	tree, err := gitOutput(repo, "ls-tree", "-r", commit)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(tree, "\n") {
		if strings.HasPrefix(line, "160000 ") {
			return fmt.Errorf("replay does not support submodules; snapshot each repository explicitly")
		}
	}
	cmd := exec.Command("git", "archive", "--format=tar", commit)
	cmd.Dir = repo
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		path := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		for _, part := range strings.Split(filepath.ToSlash(path), "/") {
			if strings.EqualFold(part, ".git") {
				return fmt.Errorf("archive contains Git metadata")
			}
		}
		target := filepath.Join(dest, path)
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry %q (symlinks are not supported)", header.Name)
		}
	}
}

// An arbitrary project check has one binary outcome. Go test counts cannot
// describe XCTest/Gradle failures or a package that never compiled.
func replayOracle(root, command string) (testCounts, []string) {
	argv := cycle.PlanShell(command)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil {
		return testCounts{passed: 1, total: 1}, nil
	}
	tail := strings.Split(strings.TrimSpace(redactAbsolutePaths(string(output))), "\n")
	if len(tail) > 20 {
		tail = tail[len(tail)-20:]
	}
	return testCounts{total: 1}, tail
}

func validateReplay(root, dest, name string, r replayOptions, out io.Writer) error {
	task, err := r.prepare(root, dest, name)
	if err != nil {
		return err
	}
	baseCopy := filepath.Join(dest, "base-check")
	if err := materialize(task.base, baseCopy); err != nil {
		return err
	}
	if counts, tail := replayOracle(baseCopy, r.oracle); counts.passed != 1 {
		return fmt.Errorf("base checks fail before oracle: %s", strings.Join(tail, "\n"))
	}
	if err := overlay(filepath.Join(dest, "oracle"), baseCopy); err != nil {
		return err
	}
	if counts, _ := replayOracle(baseCopy, r.oracle); counts.passed != 0 {
		return fmt.Errorf("replay oracle passes on base")
	}
	solutionCopy := filepath.Join(dest, "solution-check")
	if err := materialize(filepath.Join(dest, "solution"), solutionCopy); err != nil {
		return err
	}
	if counts, tail := replayOracle(solutionCopy, r.oracle); counts.passed != 1 {
		return fmt.Errorf("replay oracle fails on solution: %s", strings.Join(tail, "\n"))
	}
	fmt.Fprintf(out, "%s: base 0/1, solution 1/1, history isolated\n", name)
	return nil
}

func planReplay(parent context.Context, branch string, route agents.Route, task taskInput, planPath string, timeout time.Duration, result *benchResult) error {
	if !strings.HasPrefix(planPath, branch+string(filepath.Separator)) {
		return fmt.Errorf("plan path outside arm")
	}
	head, err := gitOutput(branch, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Create only a docflow plan at %s with id %s for the Backlog item below. Do not implement the task or edit other files. Use the existing docflow format and leave all units todo. Do not commit or activate. No executor overrides; the benchmark applies its roster.\n\n%s", task.planPath, task.planID, task.backlog)
	var planning benchResult
	runSinglePrompt(parent, branch, prompt, filepath.Join(branch, ".git", "bench-planning.log"), route, timeout, &planning)
	result.Cost, result.Tokens = planning.Cost, planning.Tokens
	if planning.Status != "done" {
		result.Status = planning.Status
		return fmt.Errorf("planning failed: %s", planning.Status)
	}
	after, err := gitOutput(branch, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != after {
		return fmt.Errorf("planner committed or changed HEAD")
	}
	changed, err := gitOutput(branch, "diff", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return err
	}
	untracked, err := gitOutput(branch, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for _, path := range strings.Split(changed+untracked, "\x00") {
		if path == "" {
			continue
		}
		if path != task.planPath {
			return fmt.Errorf("planner edited outside its plan: %s", path)
		}
	}
	text, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	p, err := format.ParsePlan(string(text), task.planPath)
	if err != nil {
		return err
	}
	if p.Meta["id"] != task.planID || len(p.Units) == 0 {
		return fmt.Errorf("planner did not produce the requested plan")
	}
	for _, unit := range p.Units {
		if unit.Status != " " {
			return fmt.Errorf("planner changed unit progress")
		}
		if unit.Field("Executor") != nil || unit.Field("Model") != nil {
			return fmt.Errorf("planner added a unit route override")
		}
	}
	if p.Meta["permissions"] == "full" {
		return fmt.Errorf("planner escalated permissions")
	}
	if err := p.SetMeta("status", "active"); err != nil {
		return err
	}
	return os.WriteFile(planPath, []byte(p.Text()), 0o644)
}
