package roster

import (
	"fmt"
	"os"
	"path"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// localAccess is the v1 provider of the models that run on this machine;
// the v2 policy marks it with kind = "local" so the resolver keeps local
// work apart from the metered subscriptions.
const localAccess = "strata"

// MigrateResult is the outcome of a roster migration: the resolved roster
// path, the v2 text and, after a write, the path of the v1 backup.
type MigrateResult struct {
	Path   string
	Backup string
	Text   string
}

// Migrate converts v1 roster text into v2 policy text: the spend order
// plus [provider], [role], [route] and every [profile] become
// [access.NAME], [model], [role], [effort] and [preset.NAME]. A text that
// is not a v1 roster — already version 2, another version, or not TOML —
// is an exit-code-2 error. The same input always gives the same output,
// so a migration is reproducible; the output is parsed back as a v2
// policy before it is returned.
func Migrate(text string) (string, error) { return migrateText("roster.toml", text) }

// MigrateFile converts the roster at path ("" means DefaultPath) and,
// when write is true, keeps the v1 text in roster.toml.bak and replaces
// roster.toml with the v2 text through a temporary file and a rename, so
// a failed write never leaves half a roster. Without write it only reads.
func MigrateFile(rosterPath string, write bool) (MigrateResult, error) {
	resolved := PolicyPath(rosterPath)
	text, err := os.ReadFile(resolved)
	if err != nil {
		return MigrateResult{}, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	migrated, err := migrateText(resolved, string(text))
	if err != nil {
		return MigrateResult{}, err
	}
	result := MigrateResult{Path: resolved, Text: migrated}
	if !write {
		return result, nil
	}
	result.Backup = resolved + ".bak"
	if err := os.WriteFile(result.Backup, text, 0o644); err != nil {
		return MigrateResult{}, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: backup: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	temporary := resolved + ".tmp"
	if err := os.WriteFile(temporary, []byte(migrated), 0o644); err != nil {
		_ = os.Remove(temporary)
		return MigrateResult{}, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.Rename(temporary, resolved); err != nil {
		_ = os.Remove(temporary)
		return MigrateResult{}, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return result, nil
}

func migrateText(rosterPath, text string) (string, error) {
	v1, err := parseLegacy(rosterPath, text)
	if err != nil {
		return "", err
	}
	accesses, models, byProvider, err := v1.convert()
	if err != nil {
		return "", err
	}
	presets := v1.presets(accesses, byProvider)
	migrated := v1.render(accesses, models, presets)
	// The generated roster must load as a v2 policy; a shape the parser
	// rejects is a migration bug, not a file the user can fix.
	if _, err := ParsePolicy(rosterPath, migrated); err != nil {
		return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: the migrated roster is not a valid version %d policy: %v", PolicyVersion, err), Code: clierrors.EXIT_FORMAT}
	}
	return migrated, nil
}

// legacy is a parsed v1 roster: the tables the v1 Load validates and
// every profile, because a migration converts all profiles at once.
type legacy struct {
	path     string
	index    *lineIndex
	roster   *Roster
	profiles []legacyProfile
}

// legacyProfile is one v1 [profile.NAME]: the spend order it replaces and
// the provider-flag overrides. providers holds the overrides and order
// their file order, so the migration output never depends on map
// iteration.
type legacyProfile struct {
	name      string
	spend     []string
	providers map[string]providerFlags
	order     []string
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

// modelDraft is one [model."ACCESS/MODEL"] together with its [effort]
// level: the class and the effort come from the route with the strongest
// tier that served the model, the first such route in file order.
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

// parseLegacy reads a v1 roster. Any version key means the text is not v1
// — version 2 is already migrated, another version is unknown — and the
// v1 tables are validated with the same parser Load uses, so a roster
// that Load rejects does not migrate either.
func parseLegacy(rosterPath, text string) (*legacy, error) {
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", rosterPath, err), Code: clierrors.EXIT_FORMAT}
	}
	index := scanLines(text)
	if value, ok := root["version"]; ok {
		if version, isInt := value.(int64); isInt && version == PolicyVersion {
			return nil, failAt(rosterPath, index, "", "version", "the roster is already version %d; nothing to migrate", PolicyVersion)
		}
		return nil, failAt(rosterPath, index, "", "version", "version %v is not the v1 roster format: only a roster without a version key is migrated", value)
	}
	value := &Roster{Path: rosterPath}
	if err := value.parse(index, root); err != nil {
		return nil, err
	}
	if err := value.checkRoutes(index); err != nil {
		return nil, err
	}
	profiles, err := legacyProfiles(rosterPath, index, root["profile"])
	if err != nil {
		return nil, err
	}
	return &legacy{path: rosterPath, index: index, roster: value, profiles: profiles}, nil
}

// legacyProfiles reads every [profile.NAME] in file order. Load validates
// only the profile it applies; a migration reads them all, so a broken
// profile fails here instead of disappearing from the output.
func legacyProfiles(rosterPath string, index *lineIndex, value any) ([]legacyProfile, error) {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return nil, failAt(rosterPath, index, "profile", "", "profile must be a table of [profile.<name>] sections")
	}
	profiles := make([]legacyProfile, 0, len(table))
	for _, name := range index.keysByLine("profile", table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(rosterPath, index, "profile", name, "profile %s must be a [profile.%s] table", name, name)
		}
		tablePath := "profile." + name
		profile := legacyProfile{name: name}
		for _, key := range index.keysByLine(tablePath, section) {
			switch key {
			case "spend", "provider":
			default:
				return nil, failAt(rosterPath, index, tablePath, key, "unknown key '%s' in [profile.%s]", key, name)
			}
		}
		if value, ok := section["spend"]; ok {
			spend, err := parseSpend(rosterPath, index, tablePath, "profile "+name, value)
			if err != nil {
				return nil, err
			}
			profile.spend = spend
		}
		if value, ok := section["provider"]; ok {
			providers, err := parseProviders(rosterPath, index, tablePath+".provider", "profile "+name+".provider", value)
			if err != nil {
				return nil, err
			}
			profile.providers = providers
			overrides, _ := section["provider"].(map[string]any)
			profile.order = index.keysByLine(tablePath+".provider", overrides)
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
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
		var routes []route
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
	// Group the models by access in access order, so [model] and [effort]
	// read like the [access] tables above them.
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

// render writes the v2 policy: accesses in v1 spend order, their models
// with the class and the effort of the strongest route, the role
// requirements, then the presets.
func (l *legacy) render(accesses []*accessDraft, models []*modelDraft, presets []presetDraft) string {
	var text strings.Builder
	text.WriteString("# Roster v2, converted from the v1 format by docflow roster migrate.\n")
	text.WriteString("# Model context, prices and reasoning levels come from docflow roster scan.\n")
	fmt.Fprintf(&text, "version = %d\n", PolicyVersion)
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
		fmt.Fprintf(&text, "\n[model.%s]\nclass = %s\n", quote(model.access+"/"+model.id), quote(model.class))
	}
	text.WriteString("\n[role]\n")
	for _, role := range Roles {
		fmt.Fprintf(&text, "%-10s = %s\n", role, quote(l.roster.RoleTiers[role]))
	}
	if len(models) > 0 {
		text.WriteString("\n[effort]\n")
		for _, model := range models {
			effort := model.effort
			if effort == "" {
				// v1 spelled "no effort" as "-"; the plan format keeps it.
				effort = "-"
			}
			fmt.Fprintf(&text, "%s = %s\n", quote(model.access+"/"+model.id), quote(effort))
		}
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
