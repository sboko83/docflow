package cycle

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/gitx"
)

// commit stages the whole tree, so whatever an agent left outside .gitignore
// would land in the unit's commit. Newly added files that look like secrets,
// are large binaries or are agent session transcripts, and added lines that
// contain a token, stop the commit with EXIT_ORDER and a list; the index is
// reset so the tree fingerprint still matches and the human can delete, ignore
// or allow them.

// AllowStagedEnv set to a non-empty value turns the guard off for one run.
const AllowStagedEnv = "DOCFLOW_ALLOW_STAGED"

// maxAddedBytes is the size above which a newly added file is refused.
const maxAddedBytes = 5 << 20

var secretNames = map[string]bool{
	".env": true, ".netrc": true, ".npmrc": true, ".pypirc": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	"credentials": true, "credentials.json": true, "secrets.json": true,
}

var secretExts = map[string]bool{
	".pem": true, ".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true,
}

// secretReason returns why a file name looks like a secret, "" if it does not.
func secretReason(name string) string {
	base := strings.ToLower(path.Base(filepath.ToSlash(name)))
	switch {
	case strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template"):
		return ""
	case secretNames[base]:
		return "secret file name"
	case strings.HasPrefix(base, ".env."):
		return "secret file name"
	case secretExts[path.Ext(base)]:
		return "key or certificate"
	}
	return ""
}

// TranscriptMarker is a phrase of the unit prompt's cycle instructions, both
// the interactive and the runner one. A newly added file that contains it is a
// transcript or export of an agent session (the prompt the agent was given),
// not the unit's work: a pilot unit committed such an HTML export through
// `git add -A`. The phrase holds no character that HTML, JSON or Markdown
// rendering escapes, so it survives in text, HTML and JSON exports. The unit
// prompt is clean-tree only at `[ ]` -> `[~]`, so a snapshot of untracked paths
// at that moment is always empty: such files arrive while the unit is `[~]`.
const TranscriptMarker = "says, no less and no more"

// transcriptReason reports whether a newly added file contains the unit
// prompt. Whitespace runs are collapsed, so a rewrapped export still matches.
func transcriptReason(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.Join(strings.Fields(string(content)), " "), TranscriptMarker) {
		return "agent session transcript"
	}
	return ""
}

// tokenPatterns match well-known credential formats in added lines. They are
// deliberately specific: a false positive stops a unit's commit.
var tokenPatterns = []struct {
	reason string
	re     *regexp.Regexp
}{
	{"private key", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----`)},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{50,})\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{20,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"API key (sk-)", regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{32,}`)},
}

// tokenReasons maps each file with a token-looking added line to the reason.
func tokenReasons(root string) (map[string]string, error) {
	// quotePath=false keeps non-ASCII names raw; a name with a quote, a
	// backslash or a control character is still C-quoted and unquoted below.
	out, err := gitx.Git(root, "-c", "core.quotePath=false", "diff", "--cached", "--no-renames", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", "-U0")
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	file := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = diffHeaderPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "+") && file != "" && found[file] == "":
			for _, p := range tokenPatterns {
				if p.re.MatchString(line) {
					found[file] = "token: " + p.reason
					break
				}
			}
		}
	}
	return found, nil
}

// diffHeaderPath returns the path of a "+++ " header without its "b/"
// prefix, "" for /dev/null. Git ends a name that contains a space with a tab
// and C-quotes a name with special characters.
func diffHeaderPath(header string) string {
	header = strings.TrimRight(header, "\r\t")
	if strings.HasPrefix(header, `"`) {
		unquoted, err := strconv.Unquote(header)
		if err != nil {
			return ""
		}
		header = unquoted
	}
	if !strings.HasPrefix(header, "b/") {
		return ""
	}
	return strings.TrimPrefix(header, "b/")
}

// guardStaged checks the files added to the index of root.
func guardStaged(root string) error {
	if os.Getenv(AllowStagedEnv) != "" {
		return nil
	}
	out, err := gitx.Git(root, "diff", "--cached", "--no-renames", "--name-only", "--diff-filter=A", "-z")
	if err != nil {
		return err
	}
	tokens, err := tokenReasons(root)
	if err != nil {
		return err
	}
	var lines []string
	listed := map[string]bool{}
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		reason := secretReason(name)
		if reason == "" {
			reason = tokens[name]
		}
		if reason == "" {
			full := filepath.Join(root, filepath.FromSlash(name))
			if info, err := os.Stat(full); err == nil && info.Size() > maxAddedBytes {
				reason = fmt.Sprintf("%d MiB", info.Size()>>20)
			} else if err == nil && info.Mode().IsRegular() {
				reason = transcriptReason(full)
			}
		}
		if reason != "" {
			lines = append(lines, fmt.Sprintf("  %s (%s)", name, reason))
			listed[name] = true
		}
	}
	for name, reason := range tokens {
		if !listed[name] {
			lines = append(lines, fmt.Sprintf("  %s (%s)", name, reason))
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return nil
	}
	if _, err := gitx.Git(root, "reset", "-q"); err != nil {
		return err
	}
	return clierrors.DocflowError{
		Code: clierrors.EXIT_ORDER,
		Message: "commit refused: new files or lines look like secrets, large binaries or agent session transcripts:\n" + strings.Join(lines, "\n") +
			"\ndelete them, add to .gitignore, or set " + AllowStagedEnv + "=1 to commit anyway",
	}
}
