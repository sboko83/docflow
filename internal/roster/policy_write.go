package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// SavePolicy writes the policy at path atomically and in full: the whole
// roster.toml is rendered, written through a temporary file, read back by
// the parser and only then renamed over the target, so a failed write or
// a shape the parser rejects leaves the file as it was. Comments and a
// previous hand layout are not preserved: the file is machine-writable,
// and the next save rewrites hand edits. The presets live in the variants
// files, so a policy loaded with the variants merged writes only the base
// file it was loaded from.
func SavePolicy(path string, policy *Policy) error {
	if path == "" {
		return clierrors.DocflowError{Message: "docflow: roster policy: path is empty", Code: clierrors.EXIT_FORMAT}
	}
	if policy == nil {
		return clierrors.DocflowError{Message: "docflow: roster policy: no policy to save", Code: clierrors.EXIT_FORMAT}
	}
	path = expandUser(path)
	text := policyText(policy)
	parsed, err := ParsePolicy(path, text)
	if err != nil {
		// The rendered roster must load as the policy it came from; a
		// shape the parser rejects is a bug in the operation that built
		// the model, not a file the user can fix.
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster policy: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if policyText(parsed) != text {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: the written file does not read back as the policy it was given", path), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster policy: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.WriteFile(path+".tmp", []byte(text), 0o644); err != nil {
		_ = os.Remove(path + ".tmp")
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster policy: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster policy: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

// policyText renders a policy: the accesses in priority order, each with
// its row of the role table in role order. A kind without a value and an
// access without cells leave their keys out, so the file carries only
// what is set.
func policyText(policy *Policy) string {
	var text strings.Builder
	text.WriteString("# roster policy written by docflow: hand edits are possible, the next save rewrites them.\n")
	text.WriteString("# The access order is the default priority; [access.NAME.roles] is its row of the role table.\n")
	fmt.Fprintf(&text, "version = %d\n", PolicyVersion)
	for _, access := range policy.Accesses {
		fmt.Fprintf(&text, "\n[access.%s]\n", tomlKey(access.Name))
		fmt.Fprintf(&text, "agents = %s\n", tomlArray(access.Agents))
		if access.Kind != "" {
			fmt.Fprintf(&text, "kind = %s\n", quote(access.Kind))
		}
		if access.Check {
			text.WriteString("check = true\n")
		}
		if !access.Expires.IsZero() {
			fmt.Fprintf(&text, "expires = %q\n", access.Expires.Format("2006-01-02"))
		}
		if len(access.Roles) > 0 {
			fmt.Fprintf(&text, "\n[access.%s.roles]\n", tomlKey(access.Name))
			writeCells(&text, access.Roles)
		}
	}
	return text.String()
}

// writeCells writes the cells of one roles table in role order: an empty
// cell (a preset clearing the base cell) is written as "".
func writeCells(text *strings.Builder, cells map[string]Cell) {
	for _, role := range Roles {
		if cell, ok := cells[role]; ok {
			fmt.Fprintf(text, "%-10s = %s\n", role, quote(cell.String()))
		}
	}
}
