// Package gitx contains Git operations shared by the CLI packages.
package gitx

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git returns the stdout of a Git command run in root. It preserves the
// command's bytes and matches the CLI's shared error format on failure.
func Git(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	message := ""
	if exit, ok := err.(*exec.ExitError); ok {
		message = strings.ToValidUTF8(string(exit.Stderr), "\uFFFD")
	}
	verb := "git"
	if len(args) > 0 {
		verb = args[0]
	}
	return nil, fmt.Errorf("git %s failed: %s", verb, strings.TrimSpace(message))
}

// StatusLines returns nonempty `git status --porcelain` lines under root,
// restricted by the given pathspecs.
func StatusLines(root string, pathspec ...string) ([]string, error) {
	args := []string{"status", "--porcelain", "--", "."}
	args = append(args, pathspec...)
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		message := ""
		if exit, ok := err.(*exec.ExitError); ok {
			message = strings.TrimSpace(string(exit.Stderr))
		}
		return nil, fmt.Errorf("git status failed: %s", message)
	}
	text := strings.TrimRight(string(output), "\r\n")
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}

// DirtyFiles lists changes in root and its configured nested repositories,
// excluding one path from the root repository.
func DirtyFiles(root, exclude string, repos []string) ([]string, error) {
	lines, err := StatusLines(root, ":(exclude)"+exclude)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		nested, err := StatusLines(filepath.Join(root, filepath.FromSlash(repo)))
		if err != nil {
			return nil, err
		}
		for _, line := range nested {
			paths := strings.Split(line[3:], " -> ")
			for i, path := range paths {
				paths[i] = repo + "/" + path
			}
			lines = append(lines, line[:3]+strings.Join(paths, " -> "))
		}
	}
	return lines, nil
}
