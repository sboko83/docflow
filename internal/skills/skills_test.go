package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sourceSkills(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate skills test source")
	}
	path, err := filepath.Abs(filepath.Join(filepath.Dir(source), "..", "..", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func sourceNames(t *testing.T, source string) []string {
	t.Helper()
	dirs, err := skillDirectories(source)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(dirs))
	for i, dir := range dirs {
		names[i] = filepath.Base(dir)
	}
	return names
}

func setHome(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", path)
}

func copyAllSkills(t *testing.T, source, target string) {
	t.Helper()
	for _, name := range sourceNames(t, source) {
		if err := copyTree(filepath.Join(source, name), filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func makeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "docs", "docflow.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ndocflow: 3\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func makePathCommand(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentSkillsDirs(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	wantPersonal := map[string]string{
		"claude": filepath.Join(home, ".claude", "skills"),
		"codex":  filepath.Join(home, ".agents", "skills"),
		"pi":     filepath.Join(home, ".pi", "agent", "skills"),
	}
	wantProject := map[string]string{
		"claude": filepath.Join("project", ".claude", "skills"),
		"codex":  filepath.Join("project", ".agents", "skills"),
		"pi":     filepath.Join("project", ".pi", "skills"),
	}
	for agent := range wantPersonal {
		personal, err := AgentSkillsDir(agent, "")
		if err != nil || personal != wantPersonal[agent] {
			t.Errorf("AgentSkillsDir(%q, empty) = %q, %v; want %q", agent, personal, err, wantPersonal[agent])
		}
		project, err := AgentSkillsDir(agent, "project")
		if err != nil || project != wantProject[agent] {
			t.Errorf("AgentSkillsDir(%q, project) = %q, %v; want %q", agent, project, err, wantProject[agent])
		}
	}
}

func TestSkillDirFromExecutableFollowsSymlink(t *testing.T) {
	repo := t.TempDir()
	binDir := filepath.Join(repo, "bin")
	skillsDir := filepath.Join(repo, "skills")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "docflow")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "docflow")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	got, err := SkillDirFromExecutable(link)
	want, resolveErr := filepath.EvalSymlinks(skillsDir)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || got != want {
		t.Fatalf("SkillDirFromExecutable() = %q, %v; want %q", got, err, want)
	}
}

func TestSkillNameAndTreeComparison(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: \"sample-skill\"\ndescription: x\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "nested.md"), []byte("same bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if name, ok := SkillName(a); !ok || name != "sample-skill" {
		t.Fatalf("SkillName() = (%q, %t)", name, ok)
	}
	equal, err := SameTree(a, b)
	if err != nil || !equal {
		t.Fatalf("SameTree(equal trees) = (%t, %v)", equal, err)
	}
	if err := os.WriteFile(filepath.Join(b, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	equal, err = SameTree(a, b)
	if err != nil || equal {
		t.Fatalf("SameTree(extra file) = (%t, %v), want false", equal, err)
	}
}

func TestInstallSkillsCreatesAndReusesPersonalSymlinks(t *testing.T) {
	source := sourceSkills(t)
	home := t.TempDir()
	setHome(t, home)
	for _, agent := range []string{"claude", "codex", "pi"} {
		result, err := InstallSkills(source, t.TempDir(), InstallOptions{Agent: agent})
		if err != nil || result.Code != 0 || result.Stderr != "" {
			t.Fatalf("install %s = %#v, %v", agent, result, err)
		}
		target, err := AgentSkillsDir(agent, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range sourceNames(t, source) {
			destination := filepath.Join(target, name)
			if info, err := os.Lstat(destination); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s was not linked for %s: %v", destination, agent, err)
			}
			if !sameResolvedPath(destination, filepath.Join(source, name)) {
				t.Fatalf("%s does not resolve to source skill", destination)
			}
		}
		rerun, err := InstallSkills(source, t.TempDir(), InstallOptions{Agent: agent})
		if err != nil || rerun.Code != 0 || strings.Count(rerun.Stdout, "already linked") != len(sourceNames(t, source)) {
			t.Fatalf("rerun %s = %#v, %v", agent, rerun, err)
		}
	}
}

func TestInstallSkillsTargetAndPreflightConflicts(t *testing.T) {
	source := sourceSkills(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	result, err := InstallSkills(source, root, InstallOptions{Target: target})
	if err != nil || result.Code != 0 || !strings.Contains(result.Stdout, ": linked ") {
		t.Fatalf("target install = %#v, %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(target, sourceNames(t, source)[0])); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"directory", "foreign symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			conflictTarget := filepath.Join(t.TempDir(), "skills")
			name := sourceNames(t, source)[0]
			destination := filepath.Join(conflictTarget, name)
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(destination, 0o755); err != nil {
					t.Fatal(err)
				}
			case "foreign symlink":
				if err := os.Symlink(root, destination); err != nil {
					t.Fatal(err)
				}
			case "dangling symlink":
				if err := os.Symlink(filepath.Join(root, "missing"), destination); err != nil {
					t.Fatal(err)
				}
			}
			result, err := InstallSkills(source, root, InstallOptions{Target: conflictTarget, Agent: "codex"})
			if err != nil || result.Code != 2 || !strings.Contains(result.Stderr, "exists and is not a link") {
				t.Fatalf("conflict install = %#v, %v", result, err)
			}
			for _, other := range sourceNames(t, source)[1:] {
				if _, err := os.Lstat(filepath.Join(conflictTarget, other)); !os.IsNotExist(err) {
					t.Fatalf("installed %s despite preflight conflict: %v", other, err)
				}
			}
		})
	}
}

func TestInstallProjectCopiesUpdatesAndUsesAgentLayouts(t *testing.T) {
	source := sourceSkills(t)
	root := makeProject(t)
	for _, agent := range []string{"claude", "codex", "pi"} {
		result, err := InstallSkills(source, root, InstallOptions{Agent: agent, Project: true})
		if err != nil || result.Code != 0 || !strings.Contains(result.Stdout, ": copied ") {
			t.Fatalf("project install %s = %#v, %v", agent, result, err)
		}
		target, err := AgentSkillsDir(agent, root)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range sourceNames(t, source) {
			destination := filepath.Join(target, name)
			info, err := os.Lstat(destination)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("%s is not a project copy: %v", destination, err)
			}
			equal, err := SameTree(destination, filepath.Join(source, name))
			if err != nil || !equal {
				t.Fatalf("project copy %s differs: %v", name, err)
			}
		}
		rerun, err := InstallSkills(source, root, InstallOptions{Agent: agent, Project: true})
		if err != nil || rerun.Code != 0 || strings.Count(rerun.Stdout, ": ok ") != len(sourceNames(t, source)) || strings.Contains(rerun.Stdout, "commit ") {
			t.Fatalf("project rerun %s = %#v, %v", agent, rerun, err)
		}
		copyPath := filepath.Join(target, sourceNames(t, source)[0])
		if err := os.WriteFile(filepath.Join(copyPath, "extra.md"), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
		updated, err := InstallSkills(source, root, InstallOptions{Agent: agent, Project: true})
		if err != nil || updated.Code != 0 || !strings.Contains(updated.Stdout, ": updated ") {
			t.Fatalf("project update %s = %#v, %v", agent, updated, err)
		}
		equal, err := SameTree(copyPath, filepath.Join(source, sourceNames(t, source)[0]))
		if err != nil || !equal {
			t.Fatalf("updated project copy differs: %v", err)
		}
	}
}

func TestInstallProjectRefusesForeignDestinationsAtomically(t *testing.T) {
	source := sourceSkills(t)
	for _, kind := range []string{"directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := makeProject(t)
			target, err := AgentSkillsDir("claude", root)
			if err != nil {
				t.Fatal(err)
			}
			name := sourceNames(t, source)[0]
			destination := filepath.Join(target, name)
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(destination, 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(source, name), destination); err != nil {
					t.Fatal(err)
				}
			case "dangling symlink":
				if err := os.Symlink(filepath.Join(root, "missing"), destination); err != nil {
					t.Fatal(err)
				}
			}
			result, err := InstallSkills(source, root, InstallOptions{Project: true})
			if err != nil || result.Code != 2 || !strings.Contains(result.Stderr, "exists and is not a docflow copy") {
				t.Fatalf("project conflict = %#v, %v", result, err)
			}
			if _, err := os.Lstat(filepath.Join(target, sourceNames(t, source)[1])); !os.IsNotExist(err) {
				t.Fatalf("copied another skill despite conflict: %v", err)
			}
		})
	}
}

func TestInstallProjectTargetConflictRootRequirementAndDynamicSkills(t *testing.T) {
	source := sourceSkills(t)
	result, err := InstallSkills(source, t.TempDir(), InstallOptions{Project: true, Target: "somewhere"})
	if err != nil || result.Code != 2 || !strings.Contains(result.Stderr, "--project and --target") {
		t.Fatalf("project plus target = %#v, %v", result, err)
	}
	root := t.TempDir()
	result, err = InstallSkills(source, root, InstallOptions{Project: true})
	if err == nil || result.Code != 0 || !strings.Contains(err.Error(), "docflow init") {
		t.Fatalf("project install without root = %#v, %v", result, err)
	}

	synthetic := t.TempDir()
	writeSkill := func(name string) {
		t.Helper()
		dir := filepath.Join(synthetic, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: Test skill.\n---\n", name)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("first-skill")
	project := makeProject(t)
	first, err := InstallProject(synthetic, project, "codex")
	if err != nil || first.Code != 0 {
		t.Fatalf("first dynamic install = %#v, %v", first, err)
	}
	writeSkill("new-skill")
	second, err := InstallProject(synthetic, project, "codex")
	if err != nil || second.Code != 0 || !strings.Contains(second.Stdout, "new-skill: copied") {
		t.Fatalf("second dynamic install = %#v, %v", second, err)
	}
}

func doctor(t *testing.T, source, cwd, executable, target, agent string) Result {
	t.Helper()
	result, err := Doctor(source, cwd, executable, DoctorOptions{Target: target, Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDoctorMatchingCopiesAndDefaultDirectories(t *testing.T) {
	source := sourceSkills(t)
	home := t.TempDir()
	setHome(t, home)
	tools := t.TempDir()
	executable := makePathCommand(t, tools, "docflow", "exit 0\n")
	t.Setenv("PATH", tools)
	target := filepath.Join(t.TempDir(), "skills")
	copyAllSkills(t, source, target)
	result := doctor(t, source, t.TempDir(), executable, target, "claude")
	if result.Code != 0 || !strings.Contains(result.Stdout, "docflow: ok ") {
		t.Fatalf("doctor with matching copies = %#v", result)
	}
	for _, name := range sourceNames(t, source) {
		if !strings.Contains(result.Stdout, name+": ok "+filepath.Join(target, name)) {
			t.Errorf("doctor did not report matching skill %s: %s", name, result.Stdout)
		}
	}

	for _, agent := range []string{"codex", "pi"} {
		personal, err := AgentSkillsDir(agent, "")
		if err != nil {
			t.Fatal(err)
		}
		copyAllSkills(t, source, personal)
		result := doctor(t, source, t.TempDir(), executable, "", agent)
		if result.Code != 0 || !strings.Contains(result.Stdout, filepath.Join(personal, sourceNames(t, source)[0])) {
			t.Fatalf("doctor personal %s = %#v", agent, result)
		}
	}
}

func TestDoctorDetectsChangedAndAdditionalBytesAndQuotesTarget(t *testing.T) {
	source := sourceSkills(t)
	home := t.TempDir()
	setHome(t, home)
	tools := t.TempDir()
	executable := makePathCommand(t, tools, "docflow", "exit 0\n")
	t.Setenv("PATH", tools)
	target := filepath.Join(t.TempDir(), "custom skills 'quoted'")
	name := sourceNames(t, source)[0]
	destination := filepath.Join(target, name)
	if err := copyTree(filepath.Join(source, name), destination); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(destination, "SKILL.md")
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	result := doctor(t, source, t.TempDir(), executable, target, "codex")
	if result.Code != 6 || !strings.Contains(result.Stdout, name+": stale "+destination) || !strings.Contains(result.Stdout, "--target "+shellQuote(target)) {
		t.Fatalf("doctor stale hint = %#v", result)
	}
	if err := os.Remove(filepath.Join(destination, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(source, name), destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	result = doctor(t, source, t.TempDir(), executable, target, "claude")
	if result.Code != 6 || !strings.Contains(result.Stdout, name+": stale "+destination) {
		t.Fatalf("doctor extra file = %#v", result)
	}
}

func TestDoctorNameComesFromFrontmatterAndLinksAreOkay(t *testing.T) {
	source := sourceSkills(t)
	target := filepath.Join(t.TempDir(), "skills")
	name := sourceNames(t, source)[0]
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, name), filepath.Join(target, "renamed-2")); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	executable := makePathCommand(t, tools, "docflow", "exit 0\n")
	t.Setenv("PATH", tools)
	result := doctor(t, source, t.TempDir(), executable, target, "claude")
	if result.Code != 0 || !strings.Contains(result.Stdout, name+": ok "+filepath.Join(target, "renamed-2")) {
		t.Fatalf("doctor renamed symlink = %#v", result)
	}
}

func TestDoctorMissingSkillsDoNotMakeStaleButMissingOrDifferentCLIDoes(t *testing.T) {
	source := sourceSkills(t)
	home := t.TempDir()
	setHome(t, home)
	missingPath := t.TempDir()
	t.Setenv("PATH", missingPath)
	executable := filepath.Join(t.TempDir(), "docflow")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := doctor(t, source, t.TempDir(), executable, filepath.Join(t.TempDir(), "skills"), "claude")
	if result.Code != 6 || !strings.Contains(result.Stdout, "docflow: not on PATH") {
		t.Fatalf("doctor without PATH CLI = %#v", result)
	}
	for _, name := range sourceNames(t, source) {
		if !strings.Contains(result.Stdout, name+": not installed") {
			t.Errorf("doctor missing %s = %s", name, result.Stdout)
		}
	}

	tools := t.TempDir()
	other := makePathCommand(t, tools, "docflow", "exit 0\n")
	t.Setenv("PATH", tools)
	result = doctor(t, source, t.TempDir(), executable, filepath.Join(t.TempDir(), "skills"), "claude")
	resolvedExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 6 || !strings.Contains(result.Stdout, other+" is not "+resolvedExecutable) {
		t.Fatalf("doctor different CLI = %#v", result)
	}

	t.Setenv("PATH", tools)
	result = doctor(t, source, t.TempDir(), other, filepath.Join(t.TempDir(), "skills"), "claude")
	if result.Code != 0 || !strings.Contains(result.Stdout, "docflow: ok "+other) {
		t.Fatalf("doctor missing copies = %#v", result)
	}
}

func TestDoctorChecksProjectCopiesAndUsesProjectHint(t *testing.T) {
	source := sourceSkills(t)
	root := makeProject(t)
	projectSkills, err := AgentSkillsDir("claude", root)
	if err != nil {
		t.Fatal(err)
	}
	name := sourceNames(t, source)[0]
	if err := copyTree(filepath.Join(source, name), filepath.Join(projectSkills, name)); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	executable := makePathCommand(t, tools, "docflow", "exit 0\n")
	t.Setenv("PATH", tools)
	setHome(t, t.TempDir())
	result := doctor(t, source, filepath.Join(root, "docs"), executable, filepath.Join(t.TempDir(), "personal"), "claude")
	if result.Code != 0 || !strings.Contains(result.Stdout, name+": ok ") || !strings.Contains(result.Stdout, "/.claude/skills/"+name) || strings.Contains(result.Stdout, ProjectStaleHint) {
		t.Fatalf("doctor project copy = %#v", result)
	}
	if strings.Count(result.Stdout, ": not installed") != len(sourceNames(t, source)) {
		t.Fatalf("doctor personal missing count = %#v", result)
	}

	if err := os.WriteFile(filepath.Join(projectSkills, name, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	result = doctor(t, source, root, executable, filepath.Join(t.TempDir(), "personal"), "claude")
	if result.Code != 6 || !strings.Contains(result.Stdout, name+": stale ") || strings.Count(result.Stdout, ProjectStaleHint) != 1 || strings.Contains(result.Stdout, StaleHintDefault) {
		t.Fatalf("doctor stale project copy = %#v", result)
	}

	codexTarget, err := AgentSkillsDir("codex", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(source, name), filepath.Join(codexTarget, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexTarget, name, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	result = doctor(t, source, root, executable, filepath.Join(t.TempDir(), "personal"), "codex")
	if result.Code != 6 || !strings.Contains(result.Stdout, "docflow install-skills --project --agent codex") || strings.Contains(result.Stdout, ".claude/skills") {
		t.Fatalf("doctor codex stale project copy = %#v", result)
	}

	otherProject := makeProject(t)
	result = doctor(t, source, otherProject, executable, filepath.Join(t.TempDir(), "personal"), "claude")
	if result.Code != 0 || strings.Contains(result.Stdout, otherProject) || strings.Contains(result.Stdout, ProjectStaleHint) {
		t.Fatalf("doctor without project copies = %#v", result)
	}
}

func TestDoctorPiVersionAndHints(t *testing.T) {
	source := sourceSkills(t)
	home := t.TempDir()
	setHome(t, home)
	personal, err := AgentSkillsDir("pi", "")
	if err != nil {
		t.Fatal(err)
	}
	copyAllSkills(t, source, personal)
	tools := t.TempDir()
	executable := makePathCommand(t, tools, "docflow", "exit 0\n")
	makePathCommand(t, tools, "pi", "echo pi-version-1.3\n")
	t.Setenv("PATH", tools)
	result := doctor(t, source, t.TempDir(), executable, "", "pi")
	if result.Code != 0 || !strings.Contains(result.Stdout, "pi: pi-version-1.3 "+filepath.Join(tools, "pi")) {
		t.Fatalf("doctor pi = %#v", result)
	}
	t.Setenv("PATH", t.TempDir())
	if got := PiVersionLine(); got != "pi: not on PATH" {
		t.Fatalf("PiVersionLine() without pi = %q", got)
	}

	root := makeProject(t)
	projectTarget, err := AgentSkillsDir("pi", root)
	if err != nil {
		t.Fatal(err)
	}
	name := sourceNames(t, source)[0]
	if err := copyTree(filepath.Join(source, name), filepath.Join(projectTarget, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectTarget, name, "extra.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools = t.TempDir()
	executable = makePathCommand(t, tools, "docflow", "exit 0\n")
	t.Setenv("PATH", tools)
	result = doctor(t, source, root, executable, filepath.Join(t.TempDir(), "personal"), "pi")
	if result.Code != 6 || !strings.Contains(result.Stdout, "docflow install-skills --project --agent pi") {
		t.Fatalf("doctor pi project hint = %#v", result)
	}
}

func TestSkillsWarningsMissingStaleAgentAndDisableSwitch(t *testing.T) {
	source := sourceSkills(t)
	home := t.TempDir()
	setHome(t, home)
	piDir, err := AgentSkillsDir("pi", "")
	if err != nil {
		t.Fatal(err)
	}
	runSkill := filepath.Join(source, "bsv-docflow-run")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runSkill, filepath.Join(piDir, "bsv-docflow-run")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(source, "bsv-changelog"), filepath.Join(piDir, "bsv-changelog")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(source, "bsv-docs"), filepath.Join(piDir, "docs-copy")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(piDir, "docs-copy", "SKILL.md")
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(content, []byte("old\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := copyTree(filepath.Join(source, "bsv-docflow-plan"), filepath.Join(project, ".pi", "skills", "bsv-docflow-plan")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCFLOW_SKILLS_CHECK", "1")
	lines, err := SkillsWarnings(source, project, []string{"pi", "fake"})
	want := "skills: pi: not installed bsv-docflow-close; stale bsv-docs; update: docflow install-skills --agent pi"
	if err != nil || len(lines) != 1 || lines[0] != want {
		t.Fatalf("SkillsWarnings() = %#v, %v; want %q", lines, err, want)
	}
	for _, name := range sourceNames(t, source) {
		directory := filepath.Join(piDir, name)
		if err := os.RemoveAll(directory); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(source, name), directory); err != nil {
			t.Fatal(err)
		}
	}
	lines, err = SkillsWarnings(source, project, []string{"pi"})
	if err != nil || len(lines) != 0 {
		t.Fatalf("SkillsWarnings(all linked) = %#v, %v", lines, err)
	}
	t.Setenv("DOCFLOW_SKILLS_CHECK", "0")
	lines, err = SkillsWarnings(source, project, []string{"claude"})
	if err != nil || len(lines) != 0 {
		t.Fatalf("SkillsWarnings(disabled) = %#v, %v", lines, err)
	}
	if got := ProjectStaleHintFor("claude"); got != ProjectStaleHint {
		t.Fatalf("ProjectStaleHintFor(claude) = %q", got)
	}
	if got := StaleHint("claude", nil); got != StaleHintDefault {
		t.Fatalf("StaleHint(claude) = %q", got)
	}
}
