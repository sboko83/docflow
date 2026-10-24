package main

import (
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
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/release"
	docskills "bsv-docflow/internal/skills"
)

var stderrIsTerminal = releaseTerminal

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
	resolved, err := filepath.EvalSymlinks(executable)
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
	for _, name := range []string{"docflow", "LICENSE", "skills"} {
		info, err := os.Lstat(filepath.Join(source, name))
		if err != nil {
			return fmt.Errorf("incomplete release: %w", err)
		}
		valid := info.Mode().IsRegular()
		if name == "skills" {
			valid = info.IsDir()
		} else if name == "docflow" {
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
	if target, err := os.Readlink(filepath.Join(home, "current")); err == nil && target == "versions/"+next {
		return validateReleaseSource(filepath.Join(home, "current"))
	}
	return release.Activate(home, source, next)
}

// installReleaseSkills links the release skills for every agent found and
// prints one line per target: the links point into current, so they follow
// the active version and an update only adds skills new in the release.
func installReleaseSkills(home string) (docskills.Result, error) {
	var combined docskills.Result
	var options []docskills.InstallOptions
	for _, agent := range []string{"claude", "codex", "pi"} {
		if _, err := exec.LookPath(agent); err == nil {
			options = append(options, docskills.InstallOptions{Agent: agent})
		}
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return combined, err
	}
	if info, err := os.Stat(filepath.Join(homeDir, ".zcode")); err == nil && info.IsDir() {
		options = append(options, docskills.InstallOptions{Target: filepath.Join(homeDir, ".zcode", "skills")})
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

func handleInstall(_ *parsedArgs) error {
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
	result, err := installReleaseSkills(home)
	if err != nil {
		return err
	}
	pathHint := profile
	if filepath.IsAbs(profile) {
		pathHint = fmt.Sprintf("PATH configured in %s; restart your shell or run:\nexport PATH=\"$HOME/.docflow/bin:$PATH\"", profile)
	}
	return commandResult{code: result.Code, stdout: fmt.Sprintf("installed: %s\npath: %s\n%s\n", version, filepath.Join(home, "bin", "docflow"), pathHint) + result.Stdout, stderr: result.Stderr}
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	latest, err := release.Latest(ctx, release.BaseURL())
	if err != nil {
		return err
	}
	if release.Compare(latest, version) <= 0 {
		return commandResult{stdout: fmt.Sprintf("up to date: %s\n", version)}
	}
	if check, _ := parsed.values["--check"].(bool); check {
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
	result, err := installReleaseSkills(home)
	if err != nil {
		return err
	}
	return commandResult{code: result.Code, stdout: fmt.Sprintf("updated: %s -> %s\n", version, latest) + result.Stdout + projectCopyHints(home), stderr: result.Stderr}
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
		if err := removeReleaseSkillLinks(target, home); err != nil {
			return err
		}
	}
	if err := release.RemovePath(homeDir); err != nil {
		return err
	}
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	return commandResult{stdout: "uninstalled: " + home + "\n"}
}

func removeReleaseSkillLinks(target, home string) error {
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := filepath.Join(target, entry.Name())
		link, err := os.Readlink(name)
		if err != nil {
			if entry.Type()&os.ModeSymlink != 0 {
				return err
			}
			continue
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(target, link)
		}
		// The lexical check also removes dangling links from an old release.
		if !pathUnder(home, link) {
			resolved, err := filepath.EvalSymlinks(name)
			if err != nil {
				continue
			}
			root, err := filepath.EvalSymlinks(home)
			if err != nil || !pathUnder(root, resolved) {
				continue
			}
		}
		if err := os.Remove(name); err != nil {
			return err
		}
	}
	return nil
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
