package cycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
)

type committedRepo struct {
	Path  string
	SHA   string
	Files string
}

type committingRecord struct {
	Heads     map[string]any `json:"heads"`
	Committed [][]string     `json:"committed"`
}

type traceEvent struct {
	Event      string `json:"event"`
	SID        string `json:"sid"`
	ChildID    any    `json:"child_id"`
	ChildClass string `json:"child_class"`
	Code       any    `json:"code"`
}

type commandOutput struct {
	stdout []byte
	stderr []byte
	code   int
}

// CommitMessage returns the exact subject and Handoff body used by commit.
func CommitMessage(unit *format.Unit) string {
	subject := ""
	if field := unit.Field("Commit"); field != nil {
		subject = field.Value
	}
	var body []string
	if handoff := unit.Field("Handoff"); handoff != nil {
		for _, item := range handoff.Items {
			body = append(body, "- "+item.Text)
		}
	}
	if len(body) > 0 {
		return subject + "\n\n" + strings.Join(body, "\n") + "\n"
	}
	return subject + "\n"
}

func saveCommittingRecord(root, unitID string, record committingRecord) error {
	path, err := committingPath(root, unitID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	content = append(content, '\n')
	temporary := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	if err := os.WriteFile(temporary, content, 0o666); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func readCommittingRecord(root, unitID string, projectRepos func(string) ([]string, error)) (committingRecord, error) {
	path, err := committingPath(root, unitID)
	if err != nil {
		return committingRecord{}, err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		heads, err := currentHeads(root, projectRepos)
		if err != nil {
			return committingRecord{}, err
		}
		return committingRecord{Heads: heads, Committed: [][]string{}}, nil
	}
	if err != nil {
		return committingRecord{}, err
	}
	var record committingRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return committingRecord{}, err
	}
	if record.Heads == nil {
		record.Heads = map[string]any{}
	}
	if record.Committed == nil {
		record.Committed = [][]string{}
	}
	return record, nil
}

// Commit commits the verified tree in nested repositories first, then root.
func Commit(root, unitID string, projectRepos func(string) ([]string, error)) (Result, error) {
	plan, unit, err := findUnit(root, unitID)
	if err != nil {
		return Result{}, err
	}
	if unit.Status != "x" {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s is [%s], expected [x]; run docflow done\n", unit.ID, unit.Status)}, nil
	}
	saved, hasSaved, err := savedFingerprint(root, unit.ID)
	if err != nil {
		return Result{}, err
	}
	if !hasSaved {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: tree changed after verify; run docflow verify %s\n", unit.ID, unit.ID)}, nil
	}
	digest, err := fingerprint(root, plan.Path, projectRepos)
	if err != nil {
		return Result{}, err
	}
	if digest != saved {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: tree changed after verify; run docflow verify %s\n", unit.ID, unit.ID)}, nil
	}
	message := CommitMessage(unit)
	if strings.TrimSpace(message) == "" {
		return Result{}, clierrors.FormatError{Path: plan.Path, Line: unit.Line, Text: fmt.Sprintf("unit %s: no Commit", unit.ID)}
	}
	record, err := readCommittingRecord(root, unit.ID, projectRepos)
	if err != nil {
		return Result{}, err
	}
	committed := make([]committedRepo, 0, len(record.Committed))
	for _, item := range record.Committed {
		if len(item) != 3 {
			return Result{}, fmt.Errorf("invalid committing record for %s", unit.ID)
		}
		committed = append(committed, committedRepo{Path: item[0], SHA: item[1], Files: item[2]})
	}
	repos := []string{}
	if projectRepos != nil {
		repos, err = projectRepos(root)
		if err != nil {
			return Result{}, err
		}
	}
	sha, files := "", ""
	for _, repo := range append(repos, "") {
		cwd := root
		if repo != "" {
			cwd = filepath.Join(root, filepath.FromSlash(repo))
		}
		expected := message
		if repo == "" && len(committed) > 0 {
			var details strings.Builder
			for _, item := range committed {
				fmt.Fprintf(&details, "Repo: %s %s\n", item.Path, item.SHA)
			}
			expected = strings.TrimRight(message, "\n") + "\n\n" + details.String()
		}
		var recorded *committedRepo
		for i := range committed {
			if committed[i].Path == repo {
				recorded = &committed[i]
				break
			}
		}
		head := head(cwd)
		before := record.Heads[repo]
		moved := !sameHead(head, before)
		if moved && recorded == nil {
			parentsOutput := runCommand(cwd, nil, nil, "rev-list", "--parents", "-n", "1", "HEAD")
			parents := strings.Fields(string(parentsOutput.stdout))
			if len(parents) > 0 {
				parents = parents[1:]
			}
			bodyOutput := runCommand(cwd, nil, nil, "show", "-s", "--format=%B", "HEAD")
			wantParents := []string{}
			if previous, ok := before.(string); ok && previous != "" {
				wantParents = []string{previous}
			}
			if !equalStrings(parents, wantParents) || strings.TrimSpace(string(bodyOutput.stdout)) != strings.TrimSpace(expected) {
				return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: unexpected HEAD in %s; inspect the pending commit\n", unit.ID, repoOrDot(repo))}, nil
			}
			sha, files, err = committedResult(cwd)
			if err != nil {
				return Result{}, err
			}
		} else if recorded != nil {
			status, err := gitx.StatusLines(cwd)
			if err != nil {
				return Result{}, err
			}
			if head == nil || !strings.HasPrefix(*head, recorded.SHA) || len(status) != 0 {
				return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: already committed repo %s changed; inspect the pending commit\n", unit.ID, repo)}, nil
			}
			continue
		} else if repo != "" {
			status, err := gitx.StatusLines(cwd)
			if err != nil {
				return Result{}, err
			}
			if len(status) == 0 {
				continue
			}
			sha, files, err = commitRepo(cwd, expected)
			if err != nil {
				done := ""
				for _, item := range committed {
					done += fmt.Sprintf("\nRepo: %s %s", item.Path, item.SHA)
				}
				if len(committed) > 0 {
					done = "\nalready committed:" + done
				}
				return Result{}, wrapCommitError(err, fmt.Sprintf("%s%s%s", repoPrefix(repo), err.Error(), done))
			}
		} else {
			sha, files, err = commitRepo(cwd, expected)
			if err != nil {
				done := ""
				for _, item := range committed {
					done += fmt.Sprintf("\nRepo: %s %s", item.Path, item.SHA)
				}
				if len(committed) > 0 {
					done = "\nalready committed:" + done
				}
				return Result{}, wrapCommitError(err, fmt.Sprintf("%s%s%s", repoPrefix(repo), err.Error(), done))
			}
		}
		if repo == "" {
			break
		}
		committed = append(committed, committedRepo{Path: repo, SHA: sha, Files: files})
		record.Committed = record.Committed[:0]
		for _, item := range committed {
			record.Committed = append(record.Committed, []string{item.Path, item.SHA, item.Files})
		}
		if err := saveCommittingRecord(root, unit.ID, record); err != nil {
			return Result{}, err
		}
		updated, err := fingerprint(root, plan.Path, projectRepos)
		if err != nil {
			return Result{}, err
		}
		reason, err := skipReason(root, unit.ID)
		if err != nil {
			return Result{}, err
		}
		if err := saveVerified(root, unit.ID, updated, reason); err != nil {
			return Result{}, err
		}
	}
	if err := clearVerified(root, unit.ID); err != nil {
		return Result{}, err
	}
	journal, err := committingPath(root, unit.ID)
	if err != nil {
		return Result{}, err
	}
	if err := os.Remove(journal); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	var output strings.Builder
	commitField := unit.Field("Commit")
	subject := ""
	if commitField != nil {
		subject = commitField.Value
	}
	fmt.Fprintf(&output, "%s %s\n", sha, subject)
	output.WriteString(files)
	for _, item := range committed {
		fmt.Fprintf(&output, "%s %s\n", item.Path, item.SHA)
		for _, name := range splitGitFiles(item.Files) {
			fmt.Fprintf(&output, "%s/%s\n", item.Path, name)
		}
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: output.String()}, nil
}

func committedResult(root string) (string, string, error) {
	files, err := gitx.Git(root, "show", "--name-only", "--format=", "HEAD")
	if err != nil {
		return "", "", err
	}
	sha, err := gitx.Git(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(sha)), strings.ToValidUTF8(string(files), "\uFFFD"), nil
}

func splitGitFiles(files string) []string {
	files = strings.TrimSuffix(files, "\n")
	if files == "" {
		return nil
	}
	return strings.Split(files, "\n")
}

func commitRepo(root, message string) (string, string, error) {
	if _, err := gitx.Git(root, "add", "-A"); err != nil {
		return "", "", err
	}
	result, events, err := traceCommit(root, []string{"-F", "-"}, []byte(message), true)
	if err != nil {
		return "", "", err
	}
	if result.code != 0 {
		return "", "", commitError(result, events)
	}
	return committedResult(root)
}

func traceCommit(root string, args []string, stdin []byte, feedStdin bool) (commandOutput, []traceEvent, error) {
	temporary, err := os.MkdirTemp("", "docflow-commit-")
	if err != nil {
		return commandOutput{}, nil, err
	}
	defer os.RemoveAll(temporary)
	tracePath := filepath.Join(temporary, "trace.json")
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_TRACE2_EVENT=") {
			env = append(env, item)
		}
	}
	env = append(env, "GIT_TRACE2_EVENT="+tracePath)
	command := exec.Command("git", append([]string{"commit", "-q"}, args...)...)
	command.Dir = root
	command.Env = env
	if feedStdin {
		command.Stdin = bytes.NewReader(stdin)
	} else {
		// Python inherits stdin when no input was supplied.
		command.Stdin = os.Stdin
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	result := commandOutput{stdout: stdout.Bytes(), stderr: stderr.Bytes(), code: commandExitCode(err)}
	content, err := os.ReadFile(tracePath)
	if os.IsNotExist(err) {
		return result, nil, nil
	}
	if err != nil {
		return commandOutput{}, nil, err
	}
	var events []traceEvent
	for _, line := range bytes.Split(content, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event traceEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return commandOutput{}, nil, err
		}
		events = append(events, event)
	}
	return result, events, nil
}

func commitError(result commandOutput, events []traceEvent) error {
	hooks := map[string]map[string]bool{}
	for _, event := range events {
		if event.Event == "child_start" && event.ChildClass == "hook" {
			key := event.SID
			if hooks[key] == nil {
				hooks[key] = map[string]bool{}
			}
			hooks[key][fmt.Sprint(event.ChildID)] = true
		}
	}
	failedHook := false
	for _, event := range events {
		if event.Event == "child_exit" && hooks[event.SID][fmt.Sprint(event.ChildID)] && truthy(event.Code) {
			failedHook = true
			break
		}
	}
	message := "git commit failed: " + strings.TrimSpace(strings.ToValidUTF8(string(result.stdout)+string(result.stderr), "\uFFFD"))
	if failedHook {
		return clierrors.NewHookError(message)
	}
	return clierrors.DocflowError{Message: message, Code: clierrors.EXIT_FORMAT}
}

func commitReplan(root, planPath, unitID string) (string, error) {
	message := strings.SplitN(unitID, ".", 2)[0] + ": replan " + unitID
	result, events, err := traceCommit(root, []string{"-m", message, "--", filepath.FromSlash(planPath)}, nil, false)
	if err != nil {
		return "", err
	}
	if result.code != 0 {
		return "", commitError(result, events)
	}
	sha, err := gitx.Git(root, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(string(sha)), err
}

// CommitReplan commits only the plan file and leaves the unit's other work in place.
func CommitReplan(root, planPath, unitID string) (string, error) {
	return commitReplan(root, planPath, unitID)
}

// Finish runs verify, done, and commit in order, preserving earlier CLI output.
func Finish(root, unitID string, handoff, decisions []string, skip *string, projectRepos func(string) ([]string, error), checkVersion func(string) error) (Result, error) {
	plan, unit, err := findUnit(root, unitID)
	if err != nil {
		return Result{}, err
	}
	steps := []string{"verify", "done", "commit"}
	if unit.Status == "x" {
		pending, err := commitPending(root, plan, unit)
		if err != nil {
			return Result{}, err
		}
		if !pending {
			return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s is already committed\n", unit.ID)}, nil
		}
		steps = []string{"verify", "commit"}
	}
	var output strings.Builder
	var stderr strings.Builder
	for _, step := range steps {
		var result Result
		switch step {
		case "verify":
			result, err = Verify(root, unitID, skip, false, projectRepos, checkVersion)
		case "done":
			result, err = Done(root, unitID, handoff, decisions, projectRepos)
		case "commit":
			result, err = Commit(root, unitID, projectRepos)
		}
		if err != nil {
			return Result{Code: errorExitCode(err), Stdout: output.String(), Stderr: stderr.String() + err.Error() + "\n"}, nil
		}
		output.WriteString(result.Stdout)
		stderr.WriteString(result.Stderr)
		if result.Code != clierrors.EXIT_OK {
			return Result{Code: result.Code, Stdout: output.String(), Stderr: stderr.String()}, nil
		}
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: output.String(), Stderr: stderr.String()}, nil
}

func errorExitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return clierrors.EXIT_FORMAT
}

// Self runs this CLI binary as a child process from root.
func Self(root string, args ...string) Result {
	executable, err := os.Executable()
	if err != nil {
		return Result{Code: 127, Stderr: err.Error() + "\n"}
	}
	command := exec.Command(executable, args...)
	command.Dir = root
	command.Stdin = nil
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	return Result{Code: commandExitCode(err), Stdout: strings.ToValidUTF8(stdout.String(), "\uFFFD"), Stderr: strings.ToValidUTF8(stderr.String(), "\uFFFD")}
}

func head(root string) *string {
	output := runCommand(root, nil, nil, "rev-parse", "-q", "--verify", "HEAD")
	value := strings.TrimSpace(string(output.stdout))
	if output.code != 0 || value == "" {
		return nil
	}
	return &value
}

// Head returns the repository's current HEAD, or nil when it has none.
func Head(root string) *string { return head(root) }

func heads(root string, projectRepos func(string) ([]string, error)) (map[string]any, error) {
	result := map[string]any{"": headAny(head(root))}
	if projectRepos == nil {
		return result, nil
	}
	repos, err := projectRepos(root)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		result[repo] = headAny(head(filepath.Join(root, filepath.FromSlash(repo))))
	}
	return result, nil
}

// Heads returns HEAD values for the root and its configured nested repositories.
func Heads(root string, projectRepos func(string) ([]string, error)) (map[string]any, error) {
	return heads(root, projectRepos)
}

type HeadMovement struct {
	Kind    string
	Reason  string
	Commits []string
}

// HeadMoved reports commits made since the snapshot, or a reason to stop when
// the history was rewritten or the plan itself was committed.
func HeadMoved(root string, before map[string]any, plan *format.Plan, unitID string, projectRepos func(string) ([]string, error)) (*HeadMovement, error) {
	now, err := heads(root, projectRepos)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for repo := range before {
		keys[repo] = true
	}
	for repo := range now {
		keys[repo] = true
	}
	repos := make([]string, 0, len(keys))
	for repo := range keys {
		repos = append(repos, repo)
	}
	sortStrings(repos)
	var lines []string
	for _, repo := range repos {
		previous, current := headValue(before[repo]), headValue(now[repo])
		if equalHeadPointers(previous, current) {
			continue
		}
		cwd := root
		if repo != "" {
			cwd = filepath.Join(root, filepath.FromSlash(repo))
		}
		where := ""
		if repo != "" {
			where = " in " + repo
		}
		if previous == nil {
			if repo == "" {
				return &HeadMovement{Kind: "stop", Reason: "the agent committed; the runner commits units itself"}, nil
			}
		} else if current == nil || runCommand(cwd, nil, nil, "merge-base", "--is-ancestor", *previous, "HEAD").code != 0 {
			return &HeadMovement{Kind: "stop", Reason: fmt.Sprintf("the agent rewrote history%s: %s is no longer an ancestor of HEAD (rebase, amend, reset); the unit's work is not committed: check the rewritten commits and the tree, then docflow finish %s", where, shortHead(*previous), unitID)}, nil
		}
		span := "HEAD"
		if previous != nil {
			span = *previous + "..HEAD"
		}
		if repo == "" && plan != nil {
			changed := runCommand(cwd, nil, nil, "diff", "--name-only", *previous, "HEAD", "--", filepath.FromSlash(plan.Path))
			if previous != nil && strings.TrimSpace(string(changed.stdout)) != "" {
				return &HeadMovement{Kind: "stop", Reason: "the agent committed the plan file; the runner commits units itself"}, nil
			}
		}
		log := runCommand(cwd, nil, nil, "log", "--format=%h %s", span)
		prefix := ""
		if repo != "" {
			prefix = repo + ": "
		}
		for _, line := range strings.Split(strings.TrimSuffix(strings.ToValidUTF8(string(log.stdout), "\uFFFD"), "\n"), "\n") {
			if line != "" {
				lines = append(lines, prefix+line)
			}
		}
	}
	if len(lines) == 0 {
		return nil, nil
	}
	return &HeadMovement{Kind: "commits", Commits: lines}, nil
}

func runCommand(root string, env []string, stdin []byte, args ...string) commandOutput {
	command := exec.Command("git", args...)
	command.Dir = root
	if env != nil {
		command.Env = env
	}
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return commandOutput{stdout: stdout.Bytes(), stderr: stderr.Bytes(), code: commandExitCode(err)}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return 127
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}

func sameHead(current *string, previous any) bool {
	if current == nil {
		return previous == nil
	}
	value, ok := previous.(string)
	return ok && *current == value
}

func headValue(value any) *string {
	if text, ok := value.(string); ok {
		return &text
	}
	return nil
}

func headAny(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func equalHeadPointers(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func shortHead(value string) string {
	if len(value) > 7 {
		return value[:7]
	}
	return value
}

func repoOrDot(repo string) string {
	if repo == "" {
		return "."
	}
	return repo
}

func repoPrefix(repo string) string {
	if repo == "" {
		return ""
	}
	return repo + ": "
}

func wrapCommitError(err error, message string) error {
	var hook clierrors.HookError
	if errors.As(err, &hook) {
		return clierrors.NewHookError(message)
	}
	var docflow clierrors.DocflowError
	if errors.As(err, &docflow) {
		return clierrors.DocflowError{Message: message, Code: docflow.Code}
	}
	return fmt.Errorf("%s", message)
}

func sortStrings(values []string) {
	sort.Strings(values)
}
