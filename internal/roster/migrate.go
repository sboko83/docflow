package roster

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// localAccess is the v1 provider of the models that run on this machine;
// the v3 policy marks it with kind = "local" so the resolver keeps local
// work apart from the metered subscriptions.
const localAccess = "strata"

// migrateToV3 brings older roster text to version 3, the last format with
// classes: a v3 text is returned as it is, a v2 policy converts in place,
// and anything else is v1 or broken, which the v1 path reports.
func migrateToV3(rosterPath, text string) (string, error) {
	if root, err := agents.ParseTOML(text); err == nil {
		if version, ok := root["version"].(int64); ok && version == v3PolicyVersion {
			if _, err := parseV3Policy(rosterPath, text); err != nil {
				return "", err
			}
			return text, nil
		}
	}
	migrated, converted, err := migrateV2(rosterPath, text)
	if err != nil {
		return "", err
	}
	if converted {
		if _, err := parseV3Policy(rosterPath, migrated); err != nil {
			return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: the migrated roster is not a valid version %d policy: %v", v3PolicyVersion, err), Code: clierrors.EXIT_FORMAT}
		}
		return migrated, nil
	}
	v1, err := parseLegacy(rosterPath, text)
	if err != nil {
		return "", err
	}
	accesses, models, byProvider, err := v1.convert()
	if err != nil {
		return "", err
	}
	presets := v1.presets(accesses, byProvider)
	migrated = v1.render(accesses, models, presets)
	// The generated roster must load as a v3 policy; a shape the parser
	// rejects is a migration bug, not a file the user can fix.
	if _, err := parseV3Policy(rosterPath, migrated); err != nil {
		return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: the migrated roster is not a valid version %d policy: %v", v3PolicyVersion, err), Code: clierrors.EXIT_FORMAT}
	}
	return migrated, nil
}

// migrateV2 converts v2 policy text into v3: every [model."ACCESS/MODEL"]
// class and [effort] pin of the file becomes one [route."ACCESS/MODEL"]
// entry — the key from the pin or "-", the class from [model] or, for a
// pin without a class, the automatic class by the model name. The rest of
// the file is kept as it is (comments, budgets, presets), the version
// line moves to 3, and the caller re-parses the result as a v3 policy. A
// text that is not a v2 policy reports converted = false, so the v1
// migration decides what the text is.
func migrateV2(rosterPath, text string) (string, bool, error) {
	root, err := agents.ParseTOML(text)
	if err != nil {
		// Broken TOML is reported by the v1 path, which owns that error.
		return "", false, nil
	}
	if version, ok := root["version"].(int64); !ok || version != 2 {
		return "", false, nil
	}
	models, _ := root["model"].(map[string]any)
	efforts, _ := root["effort"].(map[string]any)
	index := scanLines(text)
	type entry struct {
		key    string
		class  string
		effort string
		line   int
	}
	known := make(map[string]*entry, len(models)+len(efforts))
	entries := make([]*entry, 0, len(models)+len(efforts))
	for _, key := range index.keysByLine("model", models) {
		e := &entry{key: key, line: index.line("model", key)}
		if section, ok := models[key].(map[string]any); ok {
			e.class, _ = section["class"].(string)
		}
		known[key] = e
		entries = append(entries, e)
	}
	for _, key := range index.keysByLine("effort", efforts) {
		level, _ := efforts[key].(string)
		if e, ok := known[key]; ok {
			e.effort = level
			continue
		}
		e := &entry{key: key, effort: level, line: index.line("effort", key)}
		known[key] = e
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return bumpVersion(text), true, nil
	}
	// The routes go where the [model] tables stood, in file order; a pin
	// without a class keeps the pin and takes the automatic class.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].line < entries[j].line })
	routes := make([]string, 0, len(entries)*3)
	for _, e := range entries {
		_, id, err := splitModelKey(e.key)
		if err != nil {
			return "", true, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: model key %q: %v", rosterPath, e.key, err), Code: clierrors.EXIT_FORMAT}
		}
		if e.class == "" {
			e.class = familyClass(id)
		}
		if e.effort == "" {
			// No pin: the model kept the class ladder effort under v2, so
			// the route key carries it instead of dropping the effort.
			e.effort = classEffort[e.class]
			if e.effort == "" {
				e.effort = "-"
			}
		}
		routes = append(routes, "", fmt.Sprintf("[route.%s]", quote(e.key)), fmt.Sprintf("%s = %s", quote(e.effort), quote(e.class)))
	}
	result, err := spliceV2Sections(text, routes)
	if err != nil {
		return "", true, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return result, true, nil
}

// bumpVersion replaces the version line of a v2 policy that has no
// [model] and no [effort] table: the format is already v3-shaped.
func bumpVersion(text string) string {
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		if isV2VersionLine(strings.TrimSpace(raw)) {
			lines[i] = fmt.Sprintf("version = %d", v3PolicyVersion)
			break
		}
	}
	return strings.Join(lines, "\n")
}

// spliceV2Sections drops the [model.*] tables and the [effort] table,
// inserts the [route] lines where the first of them stood, and moves the
// version line to 3. A section runs from its header line to the next
// header line; the same restricted subset repeatedTable scans.
func spliceV2Sections(text string, routes []string) (string, error) {
	lines := strings.Split(text, "\n")
	skip := make(map[int]bool)
	insertAt := -1
	sectionStart := -1
	sectionRoute := false
	for i, raw := range lines {
		row := strings.TrimSpace(raw)
		header := strings.HasPrefix(row, "[")
		if header || i == len(lines)-1 {
			if sectionStart >= 0 && sectionRoute {
				for j := sectionStart; j < i; j++ {
					skip[j] = true
				}
				if insertAt < 0 {
					insertAt = sectionStart
				}
			}
			sectionStart, sectionRoute = -1, false
			if header {
				end := strings.Index(row, "]")
				if end < 0 {
					return "", fmt.Errorf("cannot find the end of the header line %q", row)
				}
				segments := headerSegments(row[1:end])
				sectionStart = i
				sectionRoute = len(segments) >= 1 && (segments[0] == "model" || segments[0] == "effort")
			}
		}
	}
	if insertAt < 0 {
		return "", fmt.Errorf("no [model] or [effort] table to replace")
	}
	out := make([]string, 0, len(lines)+len(routes))
	routed := false
	versioned := false
	for i, raw := range lines {
		if i == insertAt && !routed {
			out = append(out, routes...)
			routed = true
		}
		if skip[i] {
			continue
		}
		row := strings.TrimSpace(raw)
		if !versioned && isV2VersionLine(row) {
			out = append(out, fmt.Sprintf("version = %d", v3PolicyVersion))
			versioned = true
			continue
		}
		out = append(out, raw)
	}
	if !versioned {
		return "", fmt.Errorf("cannot find the version line")
	}
	return strings.Join(out, "\n"), nil
}

// isV2VersionLine reports whether a line sets version = 2.
func isV2VersionLine(row string) bool {
	return row == "version = 2" || row == "version=2"
}

// accessDraft is one [access.NAME] the migration builds.
type accessDraft struct {
	name      string
	agents    []string
	kind      string
	trusted   bool
	check     bool
	models    []string // model ids in v1 file order
	providers []string // v1 providers whose routes reach this access
}

// modelDraft is one [route."ACCESS/MODEL"] entry: the class and the
// effort come from the route with the strongest tier that served the
// model, the first such route in file order.
type modelDraft struct {
	access string
	id     string
	class  string
	effort string
	tier   int
}

// presetDraft is one [preset.NAME] built from a v1 profile: the accesses
// of its spend order go first, the accesses it leaves out are switched
// off, and a flag override v2 cannot express becomes a comment.
type presetDraft struct {
	name     string
	order    []string
	off      []string
	comments []string
}

// routeAccess names the v2 access and the model id of a v1 route. Pi and
// OpenCode address a model as "provider/model", so the prefix is the
// access and the rest the model id: a route over pi and one over opencode
// then meet in one access with two channels, because the quota is the
// subscription, not the agent. Codex and Claude models carry no provider
// prefix — the agent name is the access and the model stays whole — and a
// model without "/" runs on the agent's own subscription.
func routeAccess(agent, model string) (string, string) {
	if agent == "pi" || agent == "opencode" {
		if prefix, id, found := strings.Cut(model, "/"); found && prefix != "" && id != "" {
			return prefix, id
		}
	}
	return agent, model
}

// convert builds the accesses in v1 spend order and the models they
// serve. Every route of a provider adds the route's agent as a channel
// and its model to the access allowlist; the provider check/trusted flags
// move to the access. A provider without routes is dropped: v2 requires
// agents for an access and no route tells which channel reaches it.
func (l *legacy) convert() ([]*accessDraft, []*modelDraft, map[string][]*accessDraft, error) {
	accesses := make([]*accessDraft, 0, len(l.roster.Spend))
	byName := make(map[string]*accessDraft, len(l.roster.Spend))
	byProvider := make(map[string][]*accessDraft, len(l.roster.Spend))
	for _, provider := range l.roster.Spend {
		var routes []legacyRoute
		for _, rt := range l.roster.Routes {
			if rt.provider == provider {
				routes = append(routes, rt)
			}
		}
		for _, rt := range routes {
			access, id := routeAccess(rt.triple.Agent, rt.triple.Model)
			if problem := accessNameProblem(access); problem != "" {
				return nil, nil, nil, failAt(l.path, l.index, "route."+rt.name, "run", "route %s: model %s: access name '%s' %s", rt.name, rt.triple.Model, access, problem)
			}
			if _, err := path.Match(id, ""); err != nil {
				return nil, nil, nil, failAt(l.path, l.index, "route."+rt.name, "run", "route %s: model %s cannot be a model glob: %v", rt.name, rt.triple.Model, err)
			}
			draft, seen := byName[access]
			if !seen {
				draft = &accessDraft{name: access}
				if access == localAccess {
					draft.kind = "local"
				}
				byName[access] = draft
				accesses = append(accesses, draft)
			}
			draft.providers = addUnique(draft.providers, provider)
			draft.agents = addUnique(draft.agents, rt.triple.Agent)
			draft.models = addUnique(draft.models, id)
			byProvider[provider] = addUnique(byProvider[provider], draft)
		}
	}
	for _, draft := range accesses {
		for _, provider := range draft.providers {
			flags := l.roster.Providers[provider]
			draft.check = draft.check || flags.check
			draft.trusted = draft.trusted || flags.trusted
		}
		if !draft.check || !draft.trusted {
			continue
		}
		checked, trusted := l.flagProviders(draft.providers)
		// v2 forbids one access from being both: an access that needs an
		// independent review cannot be the final judge of its own units.
		return nil, nil, nil, failAt(l.path, l.index, "provider", checked, "access %s: provider %s sets check and provider %s sets trusted; a v2 access cannot be both", draft.name, checked, trusted)
	}
	byKey := make(map[string]*modelDraft, len(l.roster.Routes))
	for _, rt := range l.roster.Routes {
		access, id := routeAccess(rt.triple.Agent, rt.triple.Model)
		key := access + "/" + id
		draft, seen := byKey[key]
		if !seen {
			draft = &modelDraft{access: access, id: id, tier: -1}
			byKey[key] = draft
		}
		if tier := tierIndex(rt.tier); tier > draft.tier {
			draft.tier = tier
			draft.class = rt.tier
			draft.effort = rt.triple.Effort
		}
	}
	// Group the models by access in access order, so [route] reads like
	// the [access] tables above it.
	ordered := make([]*modelDraft, 0, len(byKey))
	for _, draft := range accesses {
		for _, id := range draft.models {
			if model := byKey[draft.name+"/"+id]; model != nil {
				ordered = append(ordered, model)
			}
		}
	}
	return accesses, ordered, byProvider, nil
}

// flagProviders names the providers behind a check/trusted conflict.
func (l *legacy) flagProviders(providers []string) (checked, trusted string) {
	for _, provider := range providers {
		flags := l.roster.Providers[provider]
		if checked == "" && flags.check {
			checked = provider
		}
		if trusted == "" && flags.trusted {
			trusted = provider
		}
	}
	if checked == "" {
		checked = providers[0]
	}
	if trusted == "" {
		trusted = checked
	}
	return checked, trusted
}

// presets turns every v1 profile into a preset: the accesses of its spend
// order come first, the accesses the profile leaves out are switched off
// — a v1 profile spend replaces the top-level one — and a provider-flag
// override that differs from the policy becomes a comment, because a v2
// preset carries no flags.
func (l *legacy) presets(accesses []*accessDraft, byProvider map[string][]*accessDraft) []presetDraft {
	presets := make([]presetDraft, 0, len(l.profiles))
	for _, profile := range l.profiles {
		preset := presetDraft{name: profile.name}
		named := make(map[string]bool, len(accesses))
		for _, provider := range profile.spend {
			for _, access := range byProvider[provider] {
				preset.order = addUnique(preset.order, access.name)
				named[access.name] = true
			}
		}
		for _, access := range accesses {
			if !named[access.name] {
				preset.off = append(preset.off, access.name)
			}
		}
		for _, provider := range profile.order {
			flags := profile.providers[provider]
			if flags == l.roster.Providers[provider] {
				continue
			}
			preset.comments = append(preset.comments, fmt.Sprintf("# v1 profile flag not migrated: provider %s check = %t, trusted = %t (a v2 preset carries no flags)", provider, flags.check, flags.trusted))
		}
		presets = append(presets, preset)
	}
	return presets
}

// render writes the v3 policy: accesses in v1 spend order, one route per
// model — the class and the effort of the strongest route — the role
// requirements, then the presets.
func (l *legacy) render(accesses []*accessDraft, models []*modelDraft, presets []presetDraft) string {
	var text strings.Builder
	text.WriteString("# Roster v3, converted from the v1 format by docflow roster migrate.\n")
	text.WriteString("# Model context, prices and reasoning levels come from docflow roster scan.\n")
	fmt.Fprintf(&text, "version = %d\n", v3PolicyVersion)
	for _, access := range accesses {
		fmt.Fprintf(&text, "\n[access.%s]\n", tomlKey(access.name))
		fmt.Fprintf(&text, "agents = %s\n", tomlArray(access.agents))
		if access.kind != "" {
			fmt.Fprintf(&text, "kind = %s\n", quote(access.kind))
		}
		if access.check {
			text.WriteString("check = true\n")
		}
		if access.trusted {
			text.WriteString("trusted = true\n")
		}
		if len(access.models) > 0 {
			fmt.Fprintf(&text, "models = %s\n", tomlArray(access.models))
		}
	}
	for _, model := range models {
		effort := model.effort
		if effort == "" {
			// v1 spelled "no effort" as "-"; the route key keeps it.
			effort = "-"
		}
		fmt.Fprintf(&text, "\n[route.%s]\n%s = %s\n", quote(model.access+"/"+model.id), quote(effort), quote(model.class))
	}
	text.WriteString("\n[role]\n")
	for _, role := range Roles {
		fmt.Fprintf(&text, "%-10s = %s\n", role, quote(l.roster.RoleTiers[role]))
	}
	for _, preset := range presets {
		fmt.Fprintf(&text, "\n[preset.%s]\n", tomlKey(preset.name))
		for _, comment := range preset.comments {
			text.WriteString(comment + "\n")
		}
		if len(preset.order) > 0 {
			fmt.Fprintf(&text, "order = %s\n", tomlArray(preset.order))
		}
		if len(preset.off) > 0 {
			fmt.Fprintf(&text, "off = %s\n", tomlArray(preset.off))
		}
	}
	return text.String()
}

// tomlArray renders a TOML array of strings.
func tomlArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
