package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/dirlink"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/release"
	docskills "bsv-docflow/internal/skills"
	"bsv-docflow/internal/term"
)

var stderrIsTerminal = func(stream io.Writer) bool { return term.IsTerminal(stream) }

// Personal skills install modes: global links skills into the personal agent
// directories; project keeps them clean and only project copies carry the
// skills (docflow init --skills, docflow install-skills --project).
const (
	skillsModeGlobal  = "global"
	skillsModeProject = "project"
)

// openSkillsTTY is the controlling-terminal seam for tests: without a
// terminal the mode question cannot be asked and install/update fall back to
// global.
// openSkillsTTY opens the terminal for the skills question. The question
// goes to stderr, so it is asked only when stderr is a terminal: a redirected
// stderr would hide it while the read waits, and a Windows CI runner opens
// CONIN$ of a hidden console that never answers.
var openSkillsTTY = func() (*os.File, error) {
	if !term.IsTerminal(os.Stderr) {
		return nil, errors.New("stderr is not a terminal")
	}
	return term.OpenTTY()
}

func skillsModePath(home string) string { return filepath.Join(home, "skills-mode") }

// readSkillsMode returns the stored mode, or "" when nothing valid is stored.
func readSkillsMode(home string) string {
	data, err := os.ReadFile(skillsModePath(home))
	if err != nil {
		return ""
	}
	mode := strings.TrimSpace(string(data))
	if mode != skillsModeGlobal && mode != skillsModeProject {
		return ""
	}
	return mode
}

func writeSkillsMode(home, mode string) error {
	return os.WriteFile(skillsModePath(home), []byte(mode+"\n"), 0644)
}

// resolveSkillsMode picks the skills mode: the --skills flag, else the stored
// mode, else a question on the controlling terminal, else global. chosen
// reports a mode taken from the flag or the question: the caller applies it
// and only then stores it with writeSkillsMode, so a failed command never
// leaves a stored mode the links do not follow. The no-terminal fallback
// returns one hint line instead.
func resolveSkillsMode(home, flagMode string) (mode, hint string, chosen bool, err error) {
	if flagMode == skillsModeGlobal || flagMode == skillsModeProject {
		return flagMode, "", true, nil
	}
	if stored := readSkillsMode(home); stored != "" {
		return stored, "", false, nil
	}
	tty, err := openSkillsTTY()
	if err != nil {
		return skillsModeGlobal, "skills: global (set docflow update --skills project to keep agent directories clean)\n", false, nil
	}
	defer tty.Close()
	mode, err = askSkillsMode(tty)
	if err != nil {
		return "", "", false, err
	}
	return mode, "", true, nil
}

// applyChosenSkillsMode applies the mode to the personal links and stores it
// when it was chosen by this command.
func applyChosenSkillsMode(home, mode string, chosen bool) (docskills.Result, error) {
	result, err := applySkillsMode(home, mode)
	if err != nil {
		return result, err
	}
	if chosen {
		if err := writeSkillsMode(home, mode); err != nil {
			return result, err
		}
	}
	return result, nil
}

// askSkillsMode asks on the terminal: an empty answer or EOF takes the
// capital default, an unrecognized answer repeats the question.
func askSkillsMode(tty *os.File) (string, error) {
	reader := bufio.NewReader(tty)
	for {
		fmt.Fprint(os.Stderr, "skills: link into agent directories (global) or only per project (project)? [G/p] ")
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return skillsModeGlobal, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "g", "global":
			return skillsModeGlobal, nil
		case "p", "project":
			return skillsModeProject, nil
		}
	}
}

func releaseCommand(handler handlerFunc) handlerFunc {
	return func(parsed *parsedArgs) error {
		err := handler(parsed)
		if err == nil {
			return nil
		}
		if _, ok := err.(commandResult); ok {
			return err
		}
		return clierrors.DocflowError{Message: err.Error(), Code: clierrors.EXIT_NOTHING}
	}
}

func releaseSource(executable string) (string, error) {
	resolved, err := dirlink.Resolve(executable)
	if err != nil {
		return "", err
	}
	source := filepath.Dir(resolved)
	if err := validateReleaseSource(source); err != nil {
		return "", err
	}
	return source, nil
}

func validateReleaseSource(source string) error {
	for _, name := range []string{release.BinaryName(), "LICENSE", "skills"} {
		info, err := os.Lstat(filepath.Join(source, name))
		if err != nil {
			return fmt.Errorf("incomplete release: %w", err)
		}
		valid := info.Mode().IsRegular()
		if name == "skills" {
			valid = info.IsDir()
		} else if name == release.BinaryName() && runtime.GOOS != "windows" {
			valid = valid && info.Mode().Perm()&0111 != 0
		}
		if !valid {
			return fmt.Errorf("invalid release file: %s", filepath.Join(source, name))
		}
	}
	return nil
}

// Re-running install (including from an already installed binary) repairs
// PATH and skill links without overwriting the immutable active version.
func activateRelease(home, source, next string) error {
	if current, err := release.CurrentVersion(home); err == nil && current == next {
		return validateReleaseSource(filepath.Join(home, "current"))
	}
	return release.Activate(home, source, next)
}

// releaseSkillOptions lists the personal skill targets of this machine: one
// per agent CLI in PATH, plus the zcode directory when it exists.
func releaseSkillOptions() ([]docskills.InstallOptions, error) {
	var options []docskills.InstallOptions
	for _, agent := range []string{"claude", "codex", "pi"} {
		if _, err := exec.LookPath(agent); err == nil {
			options = append(options, docskills.InstallOptions{Agent: agent})
		}
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(filepath.Join(homeDir, ".zcode")); err == nil && info.IsDir() {
		options = append(options, docskills.InstallOptions{Target: filepath.Join(homeDir, ".zcode", "skills")})
	}
	return options, nil
}

// installReleaseSkills links the release skills for every agent found and
// prints one line per target: the links point into current, so they follow
// the active version and an update only adds skills new in the release.
func installReleaseSkills(home string) (docskills.Result, error) {
	var combined docskills.Result
	options, err := releaseSkillOptions()
	if err != nil {
		return combined, err
	}
	if len(options) == 0 {
		return combined, nil
	}
	source := filepath.Join(home, "current", "skills")
	var stdout strings.Builder
	fmt.Fprintf(&stdout, "skills: links follow the active version (%s)\n", displayPath(source))
	for _, option := range options {
		target := option.Target
		if target == "" {
			if target, err = docskills.AgentSkillsDir(option.Agent, ""); err != nil {
				return combined, err
			}
		}
		result, err := docskills.InstallSkills(source, "", option)
		if err != nil {
			return combined, err
		}
		line := fmt.Sprintf("  %s: %d linked", displayPath(target), result.Kept+result.Added)
		if result.Added > 0 {
			line += fmt.Sprintf(" (%d new)", result.Added)
		}
		fmt.Fprintln(&stdout, line)
		combined.Stderr += result.Stderr
		if result.Code != 0 {
			combined.Code = result.Code
		}
	}
	combined.Stdout = stdout.String()
	return combined, nil
}

// projectSkillsMode removes docflow's own personal links from every target
// the global mode would use; foreign links and project copies stay.
func projectSkillsMode(home string) (docskills.Result, error) {
	var result docskills.Result
	options, err := releaseSkillOptions()
	if err != nil {
		return result, err
	}
	removed := 0
	for _, option := range options {
		target := option.Target
		if target == "" {
			if target, err = docskills.AgentSkillsDir(option.Agent, ""); err != nil {
				return result, err
			}
		}
		count, err := removeReleaseSkillLinks(target, home)
		if err != nil {
			return result, err
		}
		removed += count
	}
	result.Stdout = fmt.Sprintf("skills: project mode — removed %d personal links; add skills to a project with docflow init --skills or docflow install-skills --project\n", removed)
	return result, nil
}

// applySkillsMode installs or removes the personal skill links for the mode.
func applySkillsMode(home, mode string) (docskills.Result, error) {
	if mode == skillsModeProject {
		return projectSkillsMode(home)
	}
	return installReleaseSkills(home)
}

// projectCopyHints returns update hints for stale docflow skill copies in the
// project containing the working directory; the update cannot see other
// projects.
func projectCopyHints(home string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root, err := format.FindRoot(cwd)
	if err != nil {
		return ""
	}
	source := filepath.Join(home, "current", "skills")
	var hints strings.Builder
	for _, agent := range []string{"claude", "codex", "pi"} {
		dir, err := docskills.AgentSkillsDir(agent, root)
		if err != nil {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		var discard strings.Builder
		if stale, err := docskills.CheckSkillsDir(source, dir, false, &discard); err == nil && stale {
			fmt.Fprintln(&hints, docskills.ProjectStaleHintFor(agent))
		}
	}
	return hints.String()
}

// displayPath shortens a path under the home directory to ~/...
func displayPath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return path
	}
	if rel, err := filepath.Rel(homeDir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if rel == "." {
			return "~"
		}
		return filepath.Join("~", rel)
	}
	return path
}

func handleInstall(parsed *parsedArgs) error {
	executable, err := currentExecutable()
	if err != nil {
		return err
	}
	source, err := releaseSource(executable)
	if err != nil {
		return err
	}
	home, err := release.Home()
	if err != nil {
		return err
	}
	if err := activateRelease(home, source, version); err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	profile, err := release.EnsurePath(home, os.Getenv("SHELL"), homeDir)
	if err != nil {
		return err
	}
	mode, hint, chosen, err := resolveSkillsMode(home, skillsFlagValue(parsed))
	if err != nil {
		return err
	}
	result, err := applyChosenSkillsMode(home, mode, chosen)
	if err != nil {
		return err
	}
	pathHint := profile
	if filepath.IsAbs(profile) {
		pathHint = fmt.Sprintf("PATH configured in %s; restart your shell or run:\nexport PATH=\"$HOME/.docflow/bin:$PATH\"", profile)
	}
	return commandResult{code: result.Code, stdout: fmt.Sprintf("installed: %s\npath: %s\n%s\n", version, release.ExecutablePath(home), pathHint) + result.Stdout + hint, stderr: result.Stderr}
}

// skillsFlagValue returns the --skills choice of the command, "" when absent.
func skillsFlagValue(parsed *parsedArgs) string {
	if parsed == nil {
		return ""
	}
	value, _ := parsed.values["--skills"].(string)
	return value
}

func standaloneHome(command string) (string, error) {
	executable, err := currentExecutable()
	if err != nil {
		return "", err
	}
	if !release.InstalledExecutable(executable) {
		if _, ok := repositoryForExecutable(executable); ok {
			return "", fmt.Errorf("%s: development checkout; use git pull && bash tools/install-dev.sh", command)
		}
		return "", fmt.Errorf("%s: not a standalone installation; run docflow install from an unpacked release", command)
	}
	return release.Home()
}

func handleUpdate(parsed *parsedArgs) error {
	home, err := standaloneHome("update")
	if err != nil {
		return err
	}
	checkOnly, _ := parsed.values["--check"].(bool)
	var mode, hint string
	var chosen bool
	if !checkOnly {
		// Resolve the mode up front: a first update on a terminal asks once,
		// later updates follow the stored answer without asking again.
		mode, hint, chosen, err = resolveSkillsMode(home, skillsFlagValue(parsed))
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	latest, err := release.Latest(ctx, release.BaseURL())
	if err != nil {
		return err
	}
	if release.Compare(latest, version) <= 0 {
		stdout := fmt.Sprintf("up to date: %s\n", version)
		if !checkOnly {
			if chosen {
				// A mode chosen now (flag or answer) applies even without a
				// newer release.
				result, err := applyChosenSkillsMode(home, mode, chosen)
				if err != nil {
					return err
				}
				return commandResult{code: result.Code, stdout: stdout + result.Stdout, stderr: result.Stderr}
			}
			stdout += hint
		}
		return commandResult{stdout: stdout}
	}
	if checkOnly {
		return commandResult{stdout: fmt.Sprintf("update available: %s -> %s\n", version, latest)}
	}
	dir, err := os.MkdirTemp("", "docflow-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := release.Fetch(ctx, release.BaseURL(), latest, runtime.GOOS, runtime.GOARCH, dir); err != nil {
		return err
	}
	if err := validateReleaseSource(dir); err != nil {
		return err
	}
	if err := activateRelease(home, dir, latest); err != nil {
		return err
	}
	result, err := applyChosenSkillsMode(home, mode, chosen)
	if err != nil {
		return err
	}
	return commandResult{code: result.Code, stdout: fmt.Sprintf("updated: %s -> %s\n", version, latest) + result.Stdout + hint + projectCopyHints(home), stderr: result.Stderr}
}

func handleUninstall(_ *parsedArgs) error {
	home, err := standaloneHome("uninstall")
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var targets []string
	for _, agent := range []string{"claude", "codex", "pi"} {
		target, err := docskills.AgentSkillsDir(agent, "")
		if err != nil {
			return err
		}
		targets = append(targets, target)
	}
	targets = append(targets, filepath.Join(homeDir, ".zcode", "skills"))
	for _, target := range targets {
		if _, err := removeReleaseSkillLinks(target, home); err != nil {
			return err
		}
	}
	if err := release.RemovePath(homeDir); err != nil {
		return err
	}
	if err := release.RemoveInstallation(home); err != nil {
		return err
	}
	return commandResult{stdout: "uninstalled: " + home + "\n"}
}

// removeReleaseSkillLinks deletes the symlinks of target that point into the
// release home and returns how many it removed.
func removeReleaseSkillLinks(target, home string) (int, error) {
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		name := filepath.Join(target, entry.Name())
		link, err := os.Readlink(name)
		if err != nil {
			if entry.Type()&os.ModeSymlink != 0 {
				return removed, err
			}
			continue
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(target, link)
		}
		// The lexical check also removes dangling links from an old release.
		if !pathUnder(home, link) {
			resolved, err := dirlink.Resolve(name)
			if err != nil {
				continue
			}
			root, err := dirlink.Resolve(home)
			if err != nil || !pathUnder(root, resolved) {
				continue
			}
		}
		if err := os.Remove(name); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func pathUnder(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkReleaseUpdate(command string, stderr io.Writer) {
	if command == "install" || command == "update" || command == "uninstall" || os.Getenv("DOCFLOW_NO_UPDATE_CHECK") != "" || !stderrIsTerminal(stderr) {
		return
	}
	executable, err := currentExecutable()
	if err != nil || !release.InstalledExecutable(executable) {
		return
	}
	home, err := release.Home()
	if err != nil {
		return
	}
	now := time.Now()
	if !release.CheckDue(home, now) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	latest, err := release.Latest(ctx, release.BaseURL())
	_ = release.RecordCheck(home, now)
	if err == nil && release.Compare(latest, version) > 0 {
		fmt.Fprintf(stderr, "docflow %s available (current %s): docflow update\n", latest, version)
	}
}
