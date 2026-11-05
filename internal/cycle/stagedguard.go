package cycle

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/gitx"
)

// commit stages the whole tree, so whatever an agent left outside .gitignore
// would land in the unit's commit. Newly added files that look like secrets or
// are large binaries stop the commit with EXIT_ORDER and a list; the index is
// reset so the tree fingerprint still matches and the human can delete, ignore or allow them.

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

// guardStaged checks the files added to the index of root.
func guardStaged(root string) error {
	if os.Getenv(AllowStagedEnv) != "" {
		return nil
	}
	out, err := gitx.Git(root, "diff", "--cached", "--no-renames", "--name-only", "--diff-filter=A", "-z")
	if err != nil {
		return err
	}
	var lines []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		reason := secretReason(name)
		if reason == "" {
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil && info.Size() > maxAddedBytes {
				reason = fmt.Sprintf("%d MiB", info.Size()>>20)
			}
		}
		if reason != "" {
			lines = append(lines, fmt.Sprintf("  %s (%s)", name, reason))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	if _, err := gitx.Git(root, "reset", "-q"); err != nil {
		return err
	}
	return clierrors.DocflowError{
		Code: clierrors.EXIT_ORDER,
		Message: "commit refused: new files look like secrets or large binaries:\n" + strings.Join(lines, "\n") +
			"\ndelete them, add to .gitignore, or set " + AllowStagedEnv + "=1 to commit anyway",
	}
}
