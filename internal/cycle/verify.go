package cycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/gitx"
	"bsv-docflow/internal/procgroup"
)

const verifyTail = 40

// verifyKillGrace is how long a stopped verify command may take to exit.
const verifyKillGrace = 5 * time.Second

var verifyCommandRE = regexp.MustCompile("^`[^`]+`$")

// Verify runs the unit's check and records a fingerprint of the root and its
// configured nested repositories. A non-nil skip records the tree without
// running the check.
func Verify(root, unitID string, skip *string, noGate bool, projectRepos func(string) ([]string, error), checkVersion func(string) error) (Result, error) {
	plan, unit, err := findUnit(root, unitID)
	if err != nil {
		return Result{}, err
	}
	if err := checkNotFrozen(plan); err != nil {
		return Result{}, err
	}
	pending, err := commitPending(root, plan, unit)
	if err != nil {
		return Result{}, err
	}
	if pending {
		committing, err := committingPath(root, unit.ID)
		if err != nil {
			return Result{}, err
		}
		if _, err := os.Stat(committing); os.IsNotExist(err) {
			heads, err := currentHeads(root, projectRepos)
			if err != nil {
				return Result{}, err
			}
			if err := saveCommitting(root, unit.ID, heads); err != nil {
				return Result{}, err
			}
		} else if err != nil {
			return Result{}, err
		}
	}
	if skip != nil {
		reason := strings.TrimSpace(*skip)
		if reason == "" {
			return Result{}, clierrors.DocflowError{Message: "--skip needs a reason", Code: clierrors.EXIT_FORMAT}
		}
		digest, err := fingerprint(root, plan.Path, projectRepos)
		if err != nil {
			return Result{}, err
		}
		if err := saveVerified(root, unit.ID, digest, &reason); err != nil {
			return Result{}, err
		}
		return Result{Code: clierrors.EXIT_OK, Stdout: fmt.Sprintf("%s: verify skipped: %s\n", unit.ID, reason)}, nil
	}
	field := unit.Field("Verify")
	if field == nil || len(field.Items) != 0 || !verifyCommandRE.MatchString(field.Value) {
		line := unit.Line
		if field != nil {
			line = field.Line
		}
		return Result{}, clierrors.FormatError{Path: plan.Path, Line: line, Text: "Verify must be one command in backticks"}
	}
	command := field.Value[1 : len(field.Value)-1]
	if checkVersion != nil {
		if err := checkVersion(root); err != nil {
			return Result{}, err
		}
	}
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	gate := strings.TrimSpace(plan.Meta["gate"])
	checks := []verifyCheck{{kind: "verify", command: command}}
	if preflight := strings.TrimSpace(docflow.Meta["preflight"]); preflight != "" {
		checks = append([]verifyCheck{{kind: "preflight", command: preflight}}, checks...)
	}
	if gate != "" && !noGate {
		checks = append(checks, verifyCheck{kind: "gate", command: gate})
	}
	for _, check := range checks {
		output, code := runVerifyCommand(root, check.command)
		if code != 0 {
			if err := clearVerified(root, unit.ID); err != nil {
				return Result{}, err
			}
			lines := splitCommandLines(output)
			if len(lines) > verifyTail {
				lines = lines[len(lines)-verifyTail:]
			}
			var text strings.Builder
			for _, line := range lines {
				text.WriteString(line)
				text.WriteByte('\n')
			}
			fmt.Fprintf(&text, "%s: %s failed (exit %d): %s\n", unit.ID, check.kind, code, check.command)
			return Result{Code: clierrors.EXIT_VERIFY, Stdout: text.String()}, nil
		}
	}
	if gate != "" && noGate {
		if err := clearVerified(root, unit.ID); err != nil {
			return Result{}, err
		}
		return Result{Code: clierrors.EXIT_OK, Stdout: fmt.Sprintf("%s: verify passed, gate skipped, not recorded: %s\n", unit.ID, command)}, nil
	}
	digest, err := fingerprint(root, plan.Path, projectRepos)
	if err != nil {
		return Result{}, err
	}
	if err := saveVerified(root, unit.ID, digest, nil); err != nil {
		return Result{}, err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "%s: verify passed: %s\n", unit.ID, command)
	if gate != "" {
		fmt.Fprintf(&output, "%s: gate passed: %s\n", unit.ID, gate)
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: output.String()}, nil
}

type verifyCheck struct {
	kind    string
	command string
}

func runVerifyCommand(root, command string) (string, int) {
	cmd := verifyShell(command)
	cmd.Dir = root
	procgroup.Detach(cmd)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, procgroup.StopSignals...)
	defer signal.Stop(signals)
	err := cmd.Start()
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err = <-done:
		case sig := <-signals:
			// The detached group misses terminal signals; stop it, then die
			// the way an uncaught signal would.
			_ = procgroup.Kill(cmd, done, verifyKillGrace)
			procgroup.Reraise(sig)
		}
	}
	text := strings.ToValidUTF8(output.String(), "\uFFFD")
	if err == nil {
		return text, 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return text, exit.ExitCode()
	}
	return text, 127
}

func splitCommandLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := make([]string, 0)
	start := 0
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		end := index + size
		separator := r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\x1c' || r == '\x1d' || r == '\x1e' || r == '\u0085' || r == '\u2028' || r == '\u2029'
		if !separator {
			index = end
			continue
		}
		lines = append(lines, text[start:index])
		if r == '\r' && end < len(text) && text[end] == '\n' {
			end++
		}
		start = end
		index = end
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// Fingerprint hashes the working tree excluding the plan file.
func Fingerprint(root, planPath string, projectRepos func(string) ([]string, error)) (string, error) {
	return fingerprint(root, planPath, projectRepos)
}

func fingerprint(root, planPath string, projectRepos func(string) ([]string, error)) (string, error) {
	digest := sha256.New()
	if err := hashTree(digest, root, true, ":(exclude)"+planPath); err != nil {
		return "", err
	}
	var repos []string
	var err error
	if projectRepos != nil {
		repos, err = projectRepos(root)
		if err != nil {
			return "", err
		}
	}
	for _, repo := range repos {
		digest.Write([]byte{0})
		digest.Write([]byte("repo"))
		digest.Write([]byte{0})
		digest.Write([]byte(repo))
		if err := hashTree(digest, filepath.Join(root, filepath.FromSlash(repo)), false); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashTree(digest hash.Hash, root string, hasHead bool, pathspec ...string) error {
	if !hasHead {
		_, err := gitx.Git(root, "rev-parse", "-q", "--verify", "HEAD")
		hasHead = err == nil
	}
	diffArgs := []string{"diff", "--binary"}
	if hasHead {
		diffArgs = append(diffArgs, "HEAD")
	}
	diffArgs = append(diffArgs, "--", ".")
	diffArgs = append(diffArgs, pathspec...)
	diff, err := gitx.Git(root, diffArgs...)
	if err != nil {
		return err
	}
	digest.Write(diff)
	args := []string{"ls-files", "--others", "--exclude-standard", "-z", "--", "."}
	args = append(args, pathspec...)
	listed, err := gitx.Git(root, args...)
	if err != nil {
		return err
	}
	for _, name := range sortedUntrackedNames(listed) {
		digest.Write([]byte{0})
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			digest.Write([]byte(target))
		} else if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest.Write(content)
		}
	}
	return nil
}

func sortedUntrackedNames(listed []byte) []string {
	parts := strings.Split(string(listed), "\x00")
	names := make([]string, 0, len(parts))
	for _, name := range parts {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func savedFingerprint(root, unitID string) (string, bool, error) {
	dir, err := stateDir(root)
	if err != nil {
		return "", false, err
	}
	text, ok, err := readStateFile(filepath.Join(dir, unitID))
	if err != nil || !ok {
		return "", false, err
	}
	return strings.TrimSpace(text), true, nil
}

func skipReason(root, unitID string) (*string, error) {
	dir, err := stateDir(root)
	if err != nil {
		return nil, err
	}
	text, ok, err := readStateFile(filepath.Join(dir, unitID+".skip"))
	if err != nil || !ok {
		return nil, err
	}
	text = strings.TrimSpace(text)
	return &text, nil
}

func readStateFile(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if !utf8.Valid(content) {
		return "", false, fmt.Errorf("%s is not valid UTF-8", path)
	}
	return string(content), true, nil
}

func clearVerified(root, unitID string) error {
	dir, err := stateDir(root)
	if err != nil {
		return err
	}
	for _, name := range []string{unitID, unitID + ".skip"} {
		err := os.Remove(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func saveVerified(root, unitID, digest string, reason *string) error {
	dir, err := stateDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := clearVerified(root, unitID); err != nil {
		return err
	}
	if reason != nil {
		if err := os.WriteFile(filepath.Join(dir, unitID+".skip"), []byte(*reason+"\n"), 0o666); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, unitID), []byte(digest+"\n"), 0o666)
}

func committingPath(root, unitID string) (string, error) {
	dir, err := stateDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "committing", unitID+".json"), nil
}

func currentHeads(root string, projectRepos func(string) ([]string, error)) (map[string]any, error) {
	heads := map[string]any{"": currentHead(root)}
	if projectRepos == nil {
		return heads, nil
	}
	repos, err := projectRepos(root)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		heads[repo] = currentHead(filepath.Join(root, filepath.FromSlash(repo)))
	}
	return heads, nil
}

func currentHead(root string) any {
	output, err := gitx.Git(root, "rev-parse", "-q", "--verify", "HEAD")
	if err != nil {
		return nil
	}
	if head := strings.TrimSpace(string(output)); head != "" {
		return head
	}
	return nil
}

func saveCommitting(root, unitID string, heads map[string]any) error {
	path, err := committingPath(root, unitID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Heads     map[string]any `json:"heads"`
		Committed []any          `json:"committed"`
	}{Heads: heads, Committed: []any{}})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	if err := os.WriteFile(temporary, data, 0o666); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// Done records the unit's handoff and moves it to done after checking the
// fingerprint saved by Verify.
func Done(root, unitID string, handoff, decisions []string, projectRepos func(string) ([]string, error)) (Result, error) {
	plan, unit, err := findUnit(root, unitID)
	if err != nil {
		return Result{}, err
	}
	if err := checkNotFrozen(plan); err != nil {
		return Result{}, err
	}
	if unit.Status != "~" {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s is [%s], expected [~]\n", unit.ID, unit.Status)}, nil
	}
	saved, hasSaved, err := savedFingerprint(root, unit.ID)
	if err != nil {
		return Result{}, err
	}
	if !hasSaved {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: not verified; run docflow verify %s\n", unit.ID, unit.ID)}, nil
	}
	digest, err := fingerprint(root, plan.Path, projectRepos)
	if err != nil {
		return Result{}, err
	}
	if digest != saved {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: tree changed after verify; run docflow verify %s\n", unit.ID, unit.ID)}, nil
	}
	items := oneLineItems(handoff)
	if len(items) == 0 {
		return Result{}, clierrors.DocflowError{Message: "--handoff must not be empty", Code: clierrors.EXIT_FORMAT}
	}
	if reason, err := skipReason(root, unit.ID); err != nil {
		return Result{}, err
	} else if reason != nil && *reason != "" {
		items = append(items, "Verify skipped: "+oneLine(*reason))
	}
	decisions = oneLineItems(decisions)
	if len(decisions) > 0 && plan.Section("Decisions") == nil {
		return Result{}, clierrors.FormatError{Path: plan.Path, Line: 1, Text: "missing section 'Decisions'"}
	}
	heads, err := currentHeads(root, projectRepos)
	if err != nil {
		return Result{}, err
	}
	if err := saveCommitting(root, unit.ID, heads); err != nil {
		return Result{}, err
	}
	if err := plan.SetStatus(unit.ID, "x"); err != nil {
		return Result{}, err
	}
	if err := plan.AppendItems(unit.ID, "Handoff", items); err != nil {
		return Result{}, err
	}
	if len(decisions) > 0 {
		if err := plan.AppendSectionItems("Decisions", decisions); err != nil {
			return Result{}, err
		}
	}
	if err := plan.Save(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: unit.ID + ": done\n"}, nil
}

// Block appends the block reason to the unit's handoff and marks it blocked.
func Block(root, unitID, reason, needs string) (Result, error) {
	plan, unit, err := findUnit(root, unitID)
	if err != nil {
		return Result{}, err
	}
	if unit.Status != " " && unit.Status != "~" {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s is [%s], expected [ ] or [~]\n", unit.ID, unit.Status)}, nil
	}
	reason = oneLine(reason)
	if reason == "" {
		return Result{}, clierrors.DocflowError{Message: "--reason must not be empty", Code: clierrors.EXIT_FORMAT}
	}
	if err := plan.SetStatus(unit.ID, "!"); err != nil {
		return Result{}, err
	}
	if err := plan.AppendItems(unit.ID, "Handoff", []string{fmt.Sprintf("Blocked (%s): %s", needs, reason)}); err != nil {
		return Result{}, err
	}
	if err := plan.Save(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: fmt.Sprintf("%s: blocked (%s): %s\n", unit.ID, needs, reason)}, nil
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func oneLineItems(values []string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			items = append(items, oneLine(value))
		}
	}
	return items
}
