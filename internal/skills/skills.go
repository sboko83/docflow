// Package skills installs and checks the skills shipped with a docflow
// checkout.
package skills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/release"
)

const (
	ProjectStaleHint = "update project copies: docflow install-skills --project"
	StaleHintDefault = "update: Ruleblend update_from_sources + install, or docflow install-skills"
)

var AgentSkillDirs = map[string]string{
	"claude": filepath.Join(".claude", "skills"),
	"codex":  filepath.Join(".agents", "skills"),
	"pi":     filepath.Join(".pi", "skills"),
}

// AgentUserSkillDirs contains personal skill directories that differ from
// the corresponding project directory.
var AgentUserSkillDirs = map[string]string{
	"pi": filepath.Join(".pi", "agent", "skills"),
}

type Result struct {
	Code   int
	Stdout string
	Stderr string
	// Kept and Added count personal skill links by InstallSkills: links that
	// already pointed at the source and links created by this call.
	Kept, Added int
}

type InstallOptions struct {
	Agent   string
	Target  string
	Project bool
}

type DoctorOptions struct {
	Agent  string
	Target string
}

type ProjectCopy struct {
	Source      string
	Destination string
	Verb        string
}

type ProjectPlan struct {
	Copies   []ProjectCopy
	Stdout   string
	Stderr   string
	Conflict bool
}

// SkillDirFromExecutable keeps release skill links tied to current, while
// checkout sources are found by walking up from the resolved executable.
func SkillDirFromExecutable(executable string) (string, error) {
	if executable == "" {
		return "", fmt.Errorf("cannot locate skills: executable path is empty")
	}
	if release.InstalledExecutable(executable) {
		home, err := release.Home()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "current", "skills"), nil
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err == nil {
		executable = resolved
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	for dir := filepath.Dir(absolute); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "skills")
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", fmt.Errorf("skills directory not found relative to executable %s", absolute)
}

func CurrentSkillDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return SkillDirFromExecutable(executable)
}

func defaultAgent(agent string) string {
	if agent == "" {
		return "claude"
	}
	return agent
}

// AgentSkillsDir returns an agent's personal skill directory when root is
// empty, or its project directory under root otherwise.
func AgentSkillsDir(agent, root string) (string, error) {
	agent = defaultAgent(agent)
	relative, ok := AgentSkillDirs[agent]
	if !ok {
		return "", fmt.Errorf("unknown agent %q", agent)
	}
	if root == "" {
		if personal, exists := AgentUserSkillDirs[agent]; exists {
			relative = personal
		}
		var err error
		root, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, relative), nil
}

func expandUser(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	// "/" separates on every OS: Windows reads "~/.docflow" as ~ plus a path.
	separator := strings.IndexAny(path, "/"+string(filepath.Separator))
	userPart, rest := path[1:], ""
	if separator >= 0 {
		userPart, rest = path[1:separator], path[separator+1:]
	}
	var home string
	if userPart == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	} else {
		account, err := user.Lookup(userPart)
		if err != nil {
			return "", fmt.Errorf("user %q does not exist", userPart)
		}
		home = account.HomeDir
	}
	if rest != "" {
		return filepath.Join(home, rest), nil
	}
	return home, nil
}

func skillDirectories(sourceDir string) ([]string, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(sourceDir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		}
	}
	return dirs, nil
}

// SkillName returns the frontmatter name and whether a name was found.
func SkillName(skillDir string) (string, bool) {
	content, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimSuffix(lines[0], "\r")) != "---" {
		return "", false
	}
	for _, line := range lines[1:] {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "---" {
			break
		}
		if !strings.HasPrefix(line, "name:") {
			continue
		}
		value := strings.Trim(line[len("name:"):], " \t")
		return strings.Trim(value, "\"'"), true
	}
	return "", false
}

// TreeFiles maps relative slash-separated file names to their full paths.
func TreeFiles(top string) (map[string]string, error) {
	files := make(map[string]string)
	err := filepath.WalkDir(top, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == top || entry.IsDir() {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(top, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = path
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// SameTree reports whether two directories contain the same relative files
// with identical bytes.
func SameTree(a, b string) (bool, error) {
	filesA, err := TreeFiles(a)
	if err != nil {
		return false, err
	}
	filesB, err := TreeFiles(b)
	if err != nil {
		return false, err
	}
	if len(filesA) != len(filesB) {
		return false, nil
	}
	for name, pathA := range filesA {
		pathB, ok := filesB[name]
		if !ok {
			return false, nil
		}
		bytesA, err := os.ReadFile(pathA)
		if err != nil {
			return false, err
		}
		bytesB, err := os.ReadFile(pathB)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(bytesA, bytesB) {
			return false, nil
		}
	}
	return true, nil
}

func existsOrSymlink(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sameResolvedPath(a, b string) bool {
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return resolvedA == resolvedB
	}
	return false
}

func PlanProjectSkills(sourceDir, root, agent string) (ProjectPlan, error) {
	agent = defaultAgent(agent)
	target, err := AgentSkillsDir(agent, root)
	if err != nil {
		return ProjectPlan{}, err
	}
	sources, err := skillDirectories(sourceDir)
	if err != nil {
		return ProjectPlan{}, err
	}
	plan := ProjectPlan{Copies: make([]ProjectCopy, 0)}
	var stdout, stderr strings.Builder
	conflicts := make([]string, 0)
	for _, source := range sources {
		name := filepath.Base(source)
		destination := filepath.Join(target, name)
		if !existsOrSymlink(destination) {
			plan.Copies = append(plan.Copies, ProjectCopy{Source: source, Destination: destination, Verb: "copied"})
			continue
		}
		info, statErr := os.Lstat(destination)
		if statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			sourceName, _ := SkillName(source)
			destinationName, _ := SkillName(destination)
			if sourceName == destinationName {
				equal, err := SameTree(destination, source)
				if err != nil {
					return ProjectPlan{}, err
				}
				if equal {
					fmt.Fprintf(&stdout, "%s: ok %s\n", name, destination)
				} else {
					plan.Copies = append(plan.Copies, ProjectCopy{Source: source, Destination: destination, Verb: "updated"})
				}
				continue
			}
		}
		conflicts = append(conflicts, destination)
	}
	if len(conflicts) > 0 {
		for _, destination := range conflicts {
			fmt.Fprintf(&stderr, "%s: exists and is not a docflow copy\n", destination)
		}
		plan.Conflict = true
	}
	plan.Stdout, plan.Stderr = stdout.String(), stderr.String()
	return plan, nil
}

func WriteProjectSkills(copies []ProjectCopy, agent string) (string, error) {
	var output strings.Builder
	for _, copy := range copies {
		if err := os.MkdirAll(filepath.Dir(copy.Destination), 0o777); err != nil {
			return output.String(), err
		}
		if existsOrSymlink(copy.Destination) {
			if err := os.RemoveAll(copy.Destination); err != nil {
				return output.String(), err
			}
		}
		if err := copyTree(copy.Source, copy.Destination); err != nil {
			return output.String(), err
		}
		fmt.Fprintf(&output, "%s: %s %s\n", filepath.Base(copy.Source), copy.Verb, copy.Destination)
	}
	if len(copies) > 0 {
		agent = defaultAgent(agent)
		relative, ok := AgentSkillDirs[agent]
		if !ok {
			return output.String(), fmt.Errorf("unknown agent %q", agent)
		}
		fmt.Fprintf(&output, "commit %s/ to use the project skills\n", filepath.ToSlash(relative))
	}
	return output.String(), nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode().Perm())
	})
}

func InstallProject(sourceDir, root, agent string) (Result, error) {
	plan, err := PlanProjectSkills(sourceDir, root, agent)
	if err != nil {
		return Result{}, err
	}
	if plan.Conflict {
		return Result{Code: clierrors.EXIT_FORMAT, Stdout: plan.Stdout, Stderr: plan.Stderr}, nil
	}
	stdout, err := WriteProjectSkills(plan.Copies, agent)
	if err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: plan.Stdout + stdout}, nil
}

func InstallSkills(sourceDir, cwd string, options InstallOptions) (Result, error) {
	agent := defaultAgent(options.Agent)
	if options.Project {
		if options.Target != "" {
			return Result{Code: clierrors.EXIT_FORMAT, Stderr: "docflow: --project and --target exclude each other\n"}, nil
		}
		root, err := format.FindRoot(cwd)
		if err != nil {
			return Result{}, err
		}
		return InstallProject(sourceDir, root, agent)
	}
	target := options.Target
	if target == "" {
		target, _ = AgentSkillsDir(agent, "")
	} else {
		var err error
		target, err = expandUser(target)
		if err != nil {
			return Result{}, err
		}
	}
	sources, err := skillDirectories(sourceDir)
	if err != nil {
		return Result{}, err
	}
	var stdout, stderr strings.Builder
	var plan []string
	var conflicts []string
	kept := 0
	relink := make(map[string]string)
	docflowRoot, homeErr := expandUser("~/.docflow")
	if homeErr != nil {
		return Result{}, homeErr
	}
	resolvedDocflowRoot, _ := filepath.EvalSymlinks(docflowRoot)
	for _, source := range sources {
		destination := filepath.Join(target, filepath.Base(source))
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 && sameResolvedPath(destination, source) {
				kept++
				fmt.Fprintf(&stdout, "%s: already linked\n", filepath.Base(source))
			} else if info.Mode()&os.ModeSymlink != 0 && resolvedDocflowRoot != "" && sameResolvedPathUnder(destination, resolvedDocflowRoot) {
				oldTarget, err := os.Readlink(destination)
				if err != nil {
					return Result{}, err
				}
				relink[destination] = oldTarget
			} else {
				conflicts = append(conflicts, destination)
			}
		} else if !os.IsNotExist(err) {
			return Result{}, err
		} else {
			plan = append(plan, source)
		}
	}
	if len(conflicts) > 0 {
		for _, destination := range conflicts {
			fmt.Fprintf(&stderr, "%s: exists and is not a link to %s\n", destination, sourceDir)
		}
		return Result{Code: clierrors.EXIT_FORMAT, Stdout: stdout.String(), Stderr: stderr.String(), Kept: kept}, nil
	}
	if err := os.MkdirAll(target, 0o777); err != nil {
		return Result{}, err
	}
	for _, source := range plan {
		destination := filepath.Join(target, filepath.Base(source))
		if err := os.Symlink(source, destination); err != nil {
			return Result{}, err
		}
		fmt.Fprintf(&stdout, "%s: linked %s -> %s\n", filepath.Base(source), destination, source)
	}
	for destination, oldTarget := range relink {
		if err := os.Remove(destination); err != nil {
			return Result{}, err
		}
		name := filepath.Base(destination)
		var source string
		for _, candidate := range sources {
			if filepath.Base(candidate) == name {
				source = candidate
				break
			}
		}
		if err := os.Symlink(source, destination); err != nil {
			return Result{}, err
		}
		fmt.Fprintf(&stdout, "%s: relinked %s -> %s (was %s)\n", name, destination, source, oldTarget)
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: stdout.String(), Kept: kept, Added: len(plan)}, nil
}

func sameResolvedPathUnder(path, root string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, resolved)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func staleHint(agent string, target *string) string {
	agent = defaultAgent(agent)
	if agent == "claude" && target == nil {
		return StaleHintDefault
	}
	command := "docflow install-skills"
	if agent != "claude" {
		command += " --agent " + agent
	}
	if target != nil {
		command += " --target " + shellQuote(*target)
	}
	return "update: " + command
}

func StaleHint(agent string, target *string) string { return staleHint(agent, target) }

func projectStaleHint(agent string) string {
	command := "docflow install-skills --project"
	if agent != "claude" {
		command += " --agent " + agent
	}
	return "update project copies: " + command
}

func ProjectStaleHintFor(agent string) string { return projectStaleHint(agent) }

func shellQuote(value string) string {
	if value != "" {
		safe := true
		for _, r := range value {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)) {
				safe = false
				break
			}
		}
		if safe {
			return value
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func installedSkills(target string) map[string][]string {
	installed := make(map[string][]string)
	entries, err := os.ReadDir(target)
	if err != nil {
		return installed
	}
	for _, entry := range entries {
		directory := filepath.Join(target, entry.Name())
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			continue
		}
		name, ok := SkillName(directory)
		if ok {
			installed[name] = append(installed[name], directory)
		}
	}
	return installed
}

func skillState(sourceDir, source string, installed map[string][]string) (string, error) {
	name, ok := SkillName(source)
	if !ok {
		name = filepath.Base(source)
	}
	found := installed[name]
	if len(found) == 0 {
		return "missing", nil
	}
	for _, directory := range found {
		if sameResolvedPath(directory, source) {
			return "ok", nil
		}
		equal, err := SameTree(directory, source)
		if err != nil {
			return "", err
		}
		if equal {
			return "ok", nil
		}
	}
	return "stale", nil
}

func SkillsStatus(sourceDir, agent, root string) (map[string]string, error) {
	agent = defaultAgent(agent)
	directories := make([]string, 0, 2)
	personal, err := AgentSkillsDir(agent, "")
	if err != nil {
		return nil, err
	}
	directories = append(directories, personal)
	if root != "" {
		project, err := AgentSkillsDir(agent, root)
		if err != nil {
			return nil, err
		}
		directories = append(directories, project)
	}
	installed := make(map[string][]string)
	for _, target := range directories {
		for name, paths := range installedSkills(target) {
			installed[name] = append(installed[name], paths...)
		}
	}
	sources, err := skillDirectories(sourceDir)
	if err != nil {
		return nil, err
	}
	status := make(map[string]string, len(sources))
	for _, source := range sources {
		name, ok := SkillName(source)
		if !ok {
			name = filepath.Base(source)
		}
		state, err := skillState(sourceDir, source, installed)
		if err != nil {
			return nil, err
		}
		status[name] = state
	}
	return status, nil
}

func SkillsWarnings(sourceDir, root string, agents []string) ([]string, error) {
	if os.Getenv("DOCFLOW_SKILLS_CHECK") == "0" {
		return []string{}, nil
	}
	if info, err := os.Stat(sourceDir); err != nil || !info.IsDir() {
		return []string{}, nil
	}
	unique := make(map[string]bool)
	valid := make([]string, 0, len(agents))
	for _, agent := range agents {
		if _, ok := AgentSkillDirs[agent]; ok && !unique[agent] {
			unique[agent] = true
			valid = append(valid, agent)
		}
	}
	sort.Strings(valid)
	sources, err := skillDirectories(sourceDir)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for _, agent := range valid {
		status, err := SkillsStatus(sourceDir, agent, root)
		if err != nil {
			return nil, err
		}
		parts := make([]string, 0, 2)
		for _, state := range []struct{ key, label string }{{"missing", "not installed"}, {"stale", "stale"}} {
			names := make([]string, 0)
			for _, source := range sources {
				name, ok := SkillName(source)
				if !ok {
					name = filepath.Base(source)
				}
				if status[name] == state.key {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				parts = append(parts, state.label+" "+strings.Join(names, ", "))
			}
		}
		if len(parts) > 0 {
			lines = append(lines, "skills: "+agent+": "+strings.Join(parts, "; ")+"; "+staleHint(agent, nil))
		}
	}
	return lines, nil
}

func CheckSkillsDir(sourceDir, target string, reportMissing bool, stdout *strings.Builder) (bool, error) {
	installed := installedSkills(target)
	sources, err := skillDirectories(sourceDir)
	if err != nil {
		return false, err
	}
	stale := false
	for _, source := range sources {
		name, ok := SkillName(source)
		if !ok {
			name = filepath.Base(source)
		}
		paths := installed[name]
		if len(paths) == 0 {
			if reportMissing {
				fmt.Fprintf(stdout, "%s: not installed\n", name)
			}
			continue
		}
		for _, directory := range paths {
			state := "stale"
			if sameResolvedPath(directory, source) {
				state = "ok"
			} else {
				equal, err := SameTree(directory, source)
				if err != nil {
					return stale, err
				}
				if equal {
					state = "ok"
				}
			}
			fmt.Fprintf(stdout, "%s: %s %s\n", name, state, directory)
			if state == "stale" {
				stale = true
			}
		}
	}
	return stale, nil
}

func PiVersionLine() string {
	found, err := exec.LookPath("pi")
	if err != nil {
		return "pi: not on PATH"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, found, "--version")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Sprintf("pi: version unknown %s", found)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Sprintf("pi: version unknown %s", found)
		}
	}
	text := stdout.String()
	if text == "" {
		text = stderr.String()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "version unknown"
	}
	return fmt.Sprintf("pi: %s %s", text, found)
}
func Doctor(sourceDir, cwd, executable string, options DoctorOptions) (Result, error) {
	agent := defaultAgent(options.Agent)
	var target string
	if options.Target != "" {
		var err error
		target, err = expandUser(options.Target)
		if err != nil {
			return Result{}, err
		}
	} else {
		var err error
		target, err = AgentSkillsDir(agent, "")
		if err != nil {
			return Result{}, err
		}
	}
	resolvedExecutable, err := resolveExistingPath(executable)
	if err != nil {
		return Result{}, err
	}
	var stdout strings.Builder
	stale := false
	found, err := exec.LookPath("docflow")
	if err != nil {
		fmt.Fprintln(&stdout, "docflow: not on PATH")
		stale = true
	} else {
		resolvedFound, resolveErr := resolveExistingPath(found)
		if resolveErr != nil {
			return Result{}, resolveErr
		}
		if resolvedFound != resolvedExecutable {
			fmt.Fprintf(&stdout, "docflow: %s is not %s\n", found, resolvedExecutable)
			stale = true
		} else {
			fmt.Fprintf(&stdout, "docflow: ok %s\n", found)
		}
	}
	if agent == "pi" {
		fmt.Fprintln(&stdout, PiVersionLine())
	}
	personalStale, err := CheckSkillsDir(sourceDir, target, true, &stdout)
	if err != nil {
		return Result{}, err
	}
	if personalStale {
		var hintTarget *string
		if options.Target != "" {
			hintTarget = &options.Target
		}
		fmt.Fprintln(&stdout, staleHint(agent, hintTarget))
	}
	projectRoot, findErr := format.FindRoot(cwd)
	projectStale := false
	if findErr == nil {
		project, err := AgentSkillsDir(agent, projectRoot)
		if err != nil {
			return Result{}, err
		}
		info, statErr := os.Stat(project)
		if statErr == nil && info.IsDir() {
			projectStale, err = CheckSkillsDir(sourceDir, project, false, &stdout)
			if err != nil {
				return Result{}, err
			}
			if projectStale {
				fmt.Fprintln(&stdout, projectStaleHint(agent))
			}
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return Result{}, statErr
		}
	}
	code := clierrors.EXIT_OK
	if stale || personalStale || projectStale {
		code = clierrors.EXIT_STALE
	}
	return Result{Code: code, Stdout: stdout.String()}, nil
}

func resolveExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}
