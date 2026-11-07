package roster

import (
	"fmt"
	"os"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// SavePolicy writes the policy at path atomically and in full: the whole
// roster.toml is rendered, written through a temporary file, read back by
// the parser and only then renamed over the target, so a failed write or
// a shape the parser rejects leaves the file as it was. Comments and a
// previous hand layout are not preserved: the file is machine-writable,
// and the next save rewrites hand edits. Only the presets of the policy
// layer are rendered — the variants files keep the presets of their own
// layers, and a policy loaded with the variants merged writes the base
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

// policyText renders a policy: the accesses, the routes grouped per model,
// the role requirements and the presets of the policy layer, each section
// in policy file order. The budget key is left out at the default, a kind
// without a value is left out, and an empty allowlist or denial is not
// written, so the file carries only what differs from the defaults.
func policyText(policy *Policy) string {
	var text strings.Builder
	text.WriteString("# roster policy written by docflow: hand edits are possible, the next save rewrites them.\n")
	fmt.Fprintf(&text, "version = %d\n", PolicyVersion)
	for _, access := range policy.Accesses {
		fmt.Fprintf(&text, "\n[access.%s]\n", tomlKey(access.Name))
		fmt.Fprintf(&text, "agents = %s\n", tomlArray(access.Agents))
		if access.Kind != "" {
			fmt.Fprintf(&text, "kind = %s\n", quote(access.Kind))
		}
		if access.Budget != DefaultBudget {
			fmt.Fprintf(&text, "budget = %s\n", quote(access.Budget))
		}
		if access.Trusted {
			text.WriteString("trusted = true\n")
		}
		if access.Check {
			text.WriteString("check = true\n")
		}
		if len(access.Models) > 0 {
			fmt.Fprintf(&text, "models = %s\n", tomlArray(access.Models))
		}
		if len(access.Exclude) > 0 {
			fmt.Fprintf(&text, "exclude = %s\n", tomlArray(access.Exclude))
		}
		if !access.Expires.IsZero() {
			fmt.Fprintf(&text, "expires = %q\n", access.Expires.Format("2006-01-02"))
		}
	}
	written := ""
	for _, route := range policy.Routes {
		key := route.Access + "/" + route.Model
		if key != written {
			fmt.Fprintf(&text, "\n[route.%s]\n", quote(key))
			written = key
		}
		fmt.Fprintf(&text, "%s = %s\n", quote(route.Eff), quote(route.Class))
	}
	text.WriteString("\n[role]\n")
	for _, role := range Roles {
		fmt.Fprintf(&text, "%-10s = %s\n", role, quote(policy.Roles[role]))
	}
	for _, preset := range policy.Presets {
		if preset.Source != SourcePolicy {
			continue
		}
		fmt.Fprintf(&text, "\n[preset.%s]\n", tomlKey(preset.Name))
		writeNameList(&text, "off", preset.Off)
		writeNameList(&text, "lean", preset.Lean)
		writeNameList(&text, "minimal", preset.Minimal)
		writeNameList(&text, "prefer", preset.Prefer)
		writeNameList(&text, "order", preset.Order)
		writeModelTable(&text, preset.Models)
	}
	return text.String()
}

// clone returns a deep copy of a preset: the condition lists and the
// model narrowing are copied, so editing a variant never writes through
// to the layer it was copied from.
func (p Preset) clone() Preset {
	copied := p
	copied.Off = append([]string(nil), p.Off...)
	copied.Lean = append([]string(nil), p.Lean...)
	copied.Minimal = append([]string(nil), p.Minimal...)
	copied.Prefer = append([]string(nil), p.Prefer...)
	copied.Order = append([]string(nil), p.Order...)
	if p.Models != nil {
		copied.Models = make(map[string][]string, len(p.Models))
		for access, globs := range p.Models {
			copied.Models[access] = append([]string(nil), globs...)
		}
	}
	return copied
}
