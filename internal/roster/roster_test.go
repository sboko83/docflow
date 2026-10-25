package roster

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// draft is the roster draft from the P036 context; the picked chains are
// the reference table of the plan.
const draft = `spend = ["zai", "codex", "claude"]

[provider.zai]
check = true

[provider.claude]
trusted = true

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"

[route.glm-flash-high]
run  = "pi | zai/glm-5.3-flash | high"
tier = "cheap"

[route.glm-flash-max]
run  = "pi | zai/glm-5.3-flash | max"
tier = "standard"

[route.glm-max]
run  = "pi | zai/glm-5.3 | max"
tier = "strong"

[route.luna-high]
run  = "codex | gpt-6-luna | high"
tier = "cheap"

[route.luna-max]
run  = "codex | gpt-6-luna | max"
tier = "standard"

[route.sol-high]
run  = "codex | gpt-6.1-sol | high"
tier = "strong"

[route.haiku]
run  = "claude | claude-haiku-5-5 | -"
tier = "cheap"

[route.sonnet]
run  = "claude | claude-sonnet-5-5 | high"
tier = "standard"

[route.opus]
run  = "claude | claude-opus-5-5 | high"
tier = "strong"
`

// minimal is the smallest valid roster: two providers, no flags.
const minimal = `spend = ["zai", "codex"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.z1]
run = "pi | zai/m | high"
tier = "cheap"
[route.zs]
run = "pi | zai/m2 | high"
tier = "strong"
[route.c1]
run = "codex | c | high"
tier = "cheap"
[route.cs]
run = "codex | c2 | high"
tier = "strong"
`

func loadText(t *testing.T, text, profile string) (*Roster, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path, profile)
}

func chainOf(r *Roster, role string) string {
	return chainText(r.Chains[role])
}

func TestDraftChains(t *testing.T) {
	r, err := loadText(t, draft, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := map[string]string{
		"executor":   "pi | zai/glm-5.3-flash | max; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high",
		"cheap":      "pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high; claude | claude-haiku-5-5 | -",
		"heavy":      "pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
		"review":     "codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
		"acceptance": "claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high",
		"replan":     "claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high",
	}
	for _, role := range Roles {
		if got := chainOf(r, role); got != want[role] {
			t.Errorf("role %s chain =\n%s\nwant\n%s", role, got, want[role])
		}
	}
}

func TestDraftShow(t *testing.T) {
	r, err := loadText(t, draft, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := `spend: zai, codex, claude · check: zai · trusted: claude
executor: pi | zai/glm-5.3-flash | max
fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
cheap: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high; claude | claude-haiku-5-5 | -
heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
review: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
`
	if got := r.Show(); got != want {
		t.Errorf("Show() =\n%s\nwant\n%s", got, want)
	}
}

func TestMinimalValid(t *testing.T) {
	r, err := loadText(t, minimal, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := chainOf(r, "cheap"), "pi | zai/m | high; codex | c | high"; got != want {
		t.Errorf("cheap chain = %q, want %q", got, want)
	}
	// No trusted provider: acceptance falls back to the strong routes of
	// the non-check providers in spend order.
	if got, want := chainOf(r, "acceptance"), "pi | zai/m2 | high; codex | c2 | high"; got != want {
		t.Errorf("acceptance chain = %q, want %q", got, want)
	}
}

// reviewTrustedRoster gives claude both trusted and check: under the
// trusted requirement review must drop it, while acceptance and replan
// keep the trusted provider — check excludes a provider from review
// only.
const reviewTrustedRoster = `spend = ["zai", "codex", "claude"]
[provider.claude]
trusted = true
check = true
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "trusted"
acceptance = "trusted"
replan = "trusted"
[route.z1]
run = "pi | zai/m | high"
tier = "cheap"
[route.zs]
run = "pi | zai/m2 | high"
tier = "strong"
[route.c1]
run = "codex | c | high"
tier = "cheap"
[route.cs]
run = "codex | c2 | high"
tier = "strong"
[route.ls]
run = "claude | l2 | high"
tier = "strong"
`

func TestReviewExcludesCheckUnderAnyRequirement(t *testing.T) {
	r, err := loadText(t, reviewTrustedRoster, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// review requires trusted, but the only trusted provider carries
	// check: the chain is the strong reserve of the other providers.
	if got, want := chainOf(r, "review"), "pi | zai/m2 | high; codex | c2 | high"; got != want {
		t.Errorf("review chain = %q, want %q", got, want)
	}
	// check removes a provider from review only: acceptance and replan
	// still open with the trusted provider.
	if got, want := chainOf(r, "acceptance"), "claude | l2 | high; pi | zai/m2 | high; codex | c2 | high"; got != want {
		t.Errorf("acceptance chain = %q, want %q", got, want)
	}
	if got, want := chainOf(r, "replan"), "claude | l2 | high; pi | zai/m2 | high; codex | c2 | high"; got != want {
		t.Errorf("replan chain = %q, want %q", got, want)
	}

	// The only trusted provider carries check: the review chain drops
	// below two routes and the roster refuses to load with code 2.
	short := strings.Replace(minimal, `review = "strong"`, `review = "trusted"`, 1)
	short = strings.Replace(short, "[route.z1]", "[provider.zai]\ntrusted = true\ncheck = true\n\n[route.z1]", 1)
	_, err = loadText(t, short, "")
	if err == nil {
		t.Fatal("Load() = nil error, want the short review chain error")
	}
	if !strings.Contains(err.Error(), "role review: chain has 1 route(s), need at least two providers") {
		t.Fatalf("Load() error = %q, want the short review chain error", err.Error())
	}
	coded, ok := err.(interface{ ExitCode() int })
	if !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Errorf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
	}
}

func TestTieBreaksOnFileOrder(t *testing.T) {
	first := strings.Replace(minimal, `run = "pi | zai/m | high"`, `run = "pi | zai/first | high"`, 1)
	both := strings.Replace(first, `[route.zs]`, "[route.z1b]\nrun = \"pi | zai/second | high\"\ntier = \"cheap\"\n[route.zs]", 1)
	r, err := loadText(t, both, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := r.Chains["cheap"][0].Model; got != "zai/first" {
		t.Errorf("cheap chain first model = %q, want zai/first (file order)", got)
	}
	swapped := strings.Replace(both, `[route.z1]
run = "pi | zai/first | high"
tier = "cheap"
`, "", 1)
	swapped = strings.Replace(swapped, `[route.z1b]`, `[route.z1]
run = "pi | zai/first | high"
tier = "cheap"
[route.z1b]`, 1)
	swapped = strings.Replace(swapped, `run = "pi | zai/second | high"`, `run = "pi | zai/second | high"`, 1)
	r, err = loadText(t, swapped, "")
	if err != nil {
		t.Fatalf("Load() swapped error = %v", err)
	}
	if got := r.Chains["cheap"][0].Model; got != "zai/first" {
		t.Errorf("cheap chain first model = %q, want zai/first (still the earlier table)", got)
	}
}

func TestQuotedRouteNames(t *testing.T) {
	// Two same-tier routes of one provider under quoted names: the chain
	// takes the first in file order, not the alphabetical order the
	// index falls back to without the header lines.
	text := strings.Replace(minimal, "[route.z1]", "[route.\"z-first\"]", 1)
	text = strings.Replace(text, "tier = \"cheap\"\n[route.zs]", "tier = \"cheap\"\n\n[route.\"a-second\"]\nrun = \"pi | zai/a2 | high\"\ntier = \"cheap\"\n[route.zs]", 1)
	r, err := loadText(t, text, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if r.Routes[0].name != "z-first" || r.Routes[1].name != "a-second" {
		t.Fatalf("route order = %s, %s, want z-first then a-second (file order)", r.Routes[0].name, r.Routes[1].name)
	}
	if got, want := chainOf(r, "cheap"), "pi | zai/m | high; codex | c | high"; got != want {
		t.Errorf("cheap chain = %q, want %q (first route in file)", got, want)
	}

	// An error inside a quoted section names the section's line.
	broken := strings.Replace(minimal, "[route.z1]", "[route.\"z-first\"]", 1)
	broken = strings.Replace(broken, `tier = "cheap"`, `tier = "fast"`, 1)
	_, err = loadText(t, broken, "")
	formatErr, ok := err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() error = %T, want clierrors.FormatError", err)
	}
	if !strings.Contains(err.Error(), "route z-first: tier must be cheap, standard or strong") {
		t.Fatalf("Load() error = %q, want it to name route z-first", err.Error())
	}
	if formatErr.Line != 11 {
		t.Errorf("Line = %d, want 11 (the tier key of [route.\"z-first\"])", formatErr.Line)
	}
}

func TestPiProviderWithoutModelPrefix(t *testing.T) {
	text := strings.Replace(minimal, `run = "pi | zai/m | high"`, `run = "pi | bare-model | high"`, 1)
	text = strings.Replace(text, `run = "pi | zai/m2 | high"`, `run = "pi | other-m2 | high"`, 1)
	text = strings.Replace(text, `spend = ["zai", "codex"]`, `spend = ["pi", "codex"]`, 1)
	r, err := loadText(t, text, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := chainOf(r, "cheap"), "pi | bare-model | high; codex | c | high"; got != want {
		t.Errorf("cheap chain = %q, want %q", got, want)
	}
	if got, want := chainOf(r, "acceptance"), "pi | other-m2 | high; codex | c2 | high"; got != want {
		t.Errorf("acceptance chain = %q, want %q", got, want)
	}
}

// profileRoster adds a fourth provider (pi model prefix "local") so that
// review keeps two routes while codex carries a top-level check flag that
// the profile replaces.
const profileRoster = `spend = ["zai", "codex", "claude", "local"]
[provider.zai]
check = true
[provider.codex]
check = true
[provider.claude]
trusted = true
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.zc]
run = "pi | zai/m | high"
tier = "cheap"
[route.zs]
run = "pi | zai/m2 | high"
tier = "strong"
[route.cc]
run = "codex | c | high"
tier = "cheap"
[route.cs]
run = "codex | c2 | high"
tier = "strong"
[route.lc]
run = "claude | l | -"
tier = "cheap"
[route.ls]
run = "claude | l2 | high"
tier = "strong"
[route.nc]
run = "pi | local/n | high"
tier = "cheap"
[route.ns]
run = "pi | local/n2 | high"
tier = "strong"
[profile.codex-trusted]
spend = ["codex", "claude", "zai", "local"]
[profile.codex-trusted.provider.codex]
trusted = true
`

func TestProfileReplacesSpendAndFlags(t *testing.T) {
	base, err := loadText(t, profileRoster, "")
	if err != nil {
		t.Fatalf("Load() base error = %v", err)
	}
	if got, want := chainOf(base, "cheap"), "pi | zai/m | high; codex | c | high; claude | l | -; pi | local/n | high"; got != want {
		t.Errorf("base cheap chain = %q, want %q", got, want)
	}
	// review excludes both check providers
	if got, want := chainOf(base, "review"), "claude | l2 | high; pi | local/n2 | high"; got != want {
		t.Errorf("base review chain = %q, want %q", got, want)
	}

	r, err := loadText(t, profileRoster, "codex-trusted")
	if err != nil {
		t.Fatalf("Load() profile error = %v", err)
	}
	if r.Profile != "codex-trusted" {
		t.Errorf("Profile = %q, want codex-trusted", r.Profile)
	}
	// The profile spend order shuffles the cheap chain.
	if got, want := chainOf(r, "cheap"), "codex | c | high; claude | l | -; pi | zai/m | high; pi | local/n | high"; got != want {
		t.Errorf("profile cheap chain = %q, want %q", got, want)
	}
	// trusted picks the trusted providers in spend order, then the strong
	// reserve of the remaining non-check providers.
	if got, want := chainOf(r, "acceptance"), "codex | c2 | high; claude | l2 | high; pi | local/n2 | high"; got != want {
		t.Errorf("profile acceptance chain = %q, want %q", got, want)
	}
	// The profile flag set replaces the top-level flags: codex loses
	// check and joins the review chain.
	if got, want := chainOf(r, "review"), "codex | c2 | high; claude | l2 | high; pi | local/n2 | high"; got != want {
		t.Errorf("profile review chain = %q, want %q", got, want)
	}
	if flags := r.Providers["codex"]; flags.check || !flags.trusted {
		t.Errorf("profile codex flags = %+v, want {check:false trusted:true}", flags)
	}
	// Unknown profile.
	_, err = loadText(t, profileRoster, "nope")
	if err == nil || !strings.Contains(err.Error(), "unknown profile 'nope'") {
		t.Errorf("Load() unknown profile error = %v, want unknown profile 'nope'", err)
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Errorf("unknown profile exit code = %v, want %d", err, clierrors.EXIT_FORMAT)
	}
}

func TestProfileDropsProvider(t *testing.T) {
	text := profileRoster + `[profile.no-zai]
spend = ["codex", "claude", "local"]
`
	r, err := loadText(t, text, "no-zai")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, role := range Roles {
		if chain := chainOf(r, role); strings.Contains(chain, "zai/") {
			t.Errorf("%s chain keeps the dropped provider: %q", role, chain)
		}
	}
	if got, want := chainOf(r, "cheap"), "codex | c | high; claude | l | -; pi | local/n | high"; got != want {
		t.Errorf("cheap chain = %q, want %q", got, want)
	}
}

// An unknown --profile is an argument error: the file has no line to blame,
// so the message names the roster path and the defined profiles only.
func TestUnknownProfileIsArgumentError(t *testing.T) {
	text := profileRoster + `[profile.no-zai]
spend = ["codex", "claude", "local"]
`
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, "missing")
	if err == nil {
		t.Fatal("Load() = nil error, want unknown profile")
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Errorf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
	}
	message := err.Error()
	for _, want := range []string{path, "unknown profile 'missing'", "(defined: codex-trusted, no-zai)"} {
		if !strings.Contains(message, want) {
			t.Errorf("error = %q, want it to contain %q", message, want)
		}
	}
	if regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+:`).MatchString(message) {
		t.Errorf("error = %q, want no line position", message)
	}
}

func TestRouteProviderCheckedAgainstTopLevelSpend(t *testing.T) {
	text := draft + `
[route.other]
run  = "pi | other/m | high"
tier = "cheap"

[profile.with-other]
spend = ["zai", "codex", "claude", "other"]
`
	for _, profile := range []string{"", "with-other"} {
		_, err := loadText(t, text, profile)
		if err == nil || !strings.Contains(err.Error(), "route other: provider other is not in spend") {
			t.Errorf("profile %q: error = %v, want route other not in spend", profile, err)
			continue
		}
		if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
			t.Errorf("profile %q: exit code = %v, want %d", profile, err, clierrors.EXIT_FORMAT)
		}
	}
}

// The save-claude example of docs/usage.md keeps claude as the reserve of
// the trusted roles.
func TestSaveClaudeExample(t *testing.T) {
	text := draft + `
[profile.save-claude]
[profile.save-claude.provider.codex]
trusted = true
`
	r, err := loadText(t, text, "save-claude")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, role := range []string{"acceptance", "replan"} {
		if got, want := chainOf(r, role), "codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high"; got != want {
			t.Errorf("%s chain = %q, want %q", role, got, want)
		}
	}
}

func TestProfileProviderOfAnyType(t *testing.T) {
	// A profile provider of a non-table type fails with the line of the
	// provider key instead of passing as a missing override.
	text := minimal + "\n[profile.x]\nprovider = \"typo\"\n"
	_, err := loadText(t, text, "x")
	if err == nil {
		t.Fatal("Load() = nil error, want the provider type error")
	}
	if !strings.Contains(err.Error(), "profile x.provider must be a table of [profile x.provider.<name>] sections") {
		t.Fatalf("Load() error = %q, want the profile provider type error", err.Error())
	}
	formatErr, ok := err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() error = %T, want clierrors.FormatError", err)
	}
	if formatErr.Line != 23 {
		t.Errorf("Line = %d, want 23 (the provider key of [profile.x])", formatErr.Line)
	}
	coded, ok := err.(interface{ ExitCode() int })
	if !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Errorf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
	}
}

func TestSpendErrorsNameTheSpendKeyLine(t *testing.T) {
	// A duplicate in the top-level spend names the line of the spend
	// key: a multi-line array stays on that line — the scanner knows no
	// element lines.
	text := strings.Replace(minimal, `spend = ["zai", "codex"]`, "spend = [\n\t\"zai\",\n\t\"codex\",\n\t\"zai\",\n]", 1)
	_, err := loadText(t, text, "")
	formatErr, ok := err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() error = %T, want clierrors.FormatError", err)
	}
	if !strings.Contains(err.Error(), "spend: duplicate provider 'zai'") {
		t.Fatalf("Load() error = %q, want the duplicate provider error", err.Error())
	}
	if formatErr.Line != 1 {
		t.Errorf("Line = %d, want 1 (the top-level spend key, not the element)", formatErr.Line)
	}

	// A duplicate in a profile spend names the line of the profile's
	// spend key, not the [profile.x] header.
	text = minimal + "\n[profile.x]\nspend = [\"zai\", \"codex\", \"zai\"]\n"
	_, err = loadText(t, text, "x")
	formatErr, ok = err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() profile error = %T, want clierrors.FormatError", err)
	}
	if !strings.Contains(err.Error(), "profile x: duplicate provider 'zai'") {
		t.Fatalf("Load() profile error = %q, want the duplicate provider error", err.Error())
	}
	if formatErr.Line != 23 {
		t.Errorf("Line = %d, want 23 (the spend key of [profile.x], not its header on 22)", formatErr.Line)
	}

	// A non-array spend names the spend key's line.
	text = strings.Replace(minimal, `spend = ["zai", "codex"]`, `spend = "zai"`, 1)
	_, err = loadText(t, text, "")
	formatErr, ok = err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() non-array error = %T, want clierrors.FormatError", err)
	}
	if !strings.Contains(err.Error(), "spend must be an array of provider names in spend order") {
		t.Fatalf("Load() non-array error = %q, want the array error", err.Error())
	}
	if formatErr.Line != 1 {
		t.Errorf("Line = %d, want 1 (the spend key)", formatErr.Line)
	}
}

func TestUnindexedErrorFallsBackToLineOne(t *testing.T) {
	// `profile = 1` without a [profile] header: the failing table has no
	// scan entry, and the error falls back to line 1, not 0.
	_, err := loadText(t, strings.Replace(minimal, "spend = [\"zai\", \"codex\"]\n", "spend = [\"zai\", \"codex\"]\nprofile = 1\n", 1), "x")
	formatErr, ok := err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() error = %T, want clierrors.FormatError", err)
	}
	if !strings.Contains(err.Error(), "profile must be a table of [profile.<name>] sections") {
		t.Fatalf("Load() error = %q, want the profile table error", err.Error())
	}
	if formatErr.Line != 1 {
		t.Errorf("Line = %d, want 1 (no index entry)", formatErr.Line)
	}

	// An inline-table profile parses but leaves no scan entries: the
	// spend error inside it falls back to line 1 too.
	_, err = loadText(t, strings.Replace(minimal, "spend = [\"zai\", \"codex\"]\n", "spend = [\"zai\", \"codex\"]\nprofile = { x = { spend = 1 } }\n", 1), "x")
	formatErr, ok = err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() inline error = %T, want clierrors.FormatError", err)
	}
	if !strings.Contains(err.Error(), "profile x must be an array of provider names in spend order") {
		t.Fatalf("Load() inline error = %q, want the profile spend array error", err.Error())
	}
	if formatErr.Line != 1 {
		t.Errorf("Line = %d, want 1 (no index entry)", formatErr.Line)
	}
}

func TestValidations(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "unknown top-level key",
			text: "junk = 1\n" + minimal,
			want: "unknown key 'junk'",
		},
		{
			name: "unknown provider key",
			text: minimal + "\n[provider.zai]\nweight = true\n",
			want: "unknown key 'weight' in [provider.zai]",
		},
		{
			name: "unparseable run",
			text: strings.Replace(minimal, `run = "pi | zai/m | high"`, `run = "pi zai/m high"`, 1),
			want: "route z1: run:",
		},
		{
			name: "tier outside the ladder",
			text: strings.Replace(minimal, `tier = "cheap"`, `tier = "fast"`, 1),
			want: "route z1: tier must be cheap, standard or strong",
		},
		{
			name: "requirement outside the set",
			text: strings.Replace(minimal, `cheap = "cheap"`, `cheap = "max"`, 1),
			want: "role cheap: requirement must be cheap, standard, strong or trusted",
		},
		{
			name: "unknown role",
			text: strings.Replace(minimal, `replan = "trusted"`, "replan = \"trusted\"\nultra = \"strong\"", 1),
			want: "unknown role 'ultra'",
		},
		{
			name: "missing role requirement",
			text: strings.Replace(minimal, "replan = \"trusted\"\n", "", 1),
			want: "role replan: no requirement in [role]",
		},
		{
			name: "route provider not in spend",
			text: strings.Replace(minimal, `run = "pi | zai/m | high"`, `run = "pi | other/m | high"`, 1),
			want: "route z1: provider other is not in spend",
		},
		{
			name: "duplicate spend",
			text: strings.Replace(minimal, `spend = ["zai", "codex"]`, `spend = ["zai", "codex", "zai"]`, 1),
			want: "duplicate provider 'zai'",
		},
		{
			name: "missing spend",
			text: strings.Replace(minimal, "spend = [\"zai\", \"codex\"]\n", "", 1),
			want: "spend must be an array",
		},
		{
			name: "chain shorter than two routes",
			text: strings.Replace(minimal, "spend = [\"zai\", \"codex\"]",
				"spend = [\"zai\", \"codex\"]\n[provider.zai]\ncheck = true", 1),
			want: "chain has 1 route(s), need at least two providers",
		},
		{
			name: "toml syntax error",
			text: minimal + "\nthis is not toml\n",
			want: "roster.toml",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadText(t, test.text, "")
			if err == nil {
				t.Fatalf("Load() = nil error, want %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %q, want it to contain %q", err.Error(), test.want)
			}
			coded, ok := err.(interface{ ExitCode() int })
			if !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
				t.Fatalf("Load() error %v has exit code %v, want %d", err, coded, clierrors.EXIT_FORMAT)
			}
		})
	}
}

func TestErrorNamesFileAndLine(t *testing.T) {
	// Line 10 of minimal is the run key of [route.z1].
	_, err := loadText(t, strings.Replace(minimal, `run = "pi | zai/m | high"`, `run = "pi | other/m | high"`, 1), "")
	formatErr, ok := err.(clierrors.FormatError)
	if !ok {
		t.Fatalf("Load() error = %T, want clierrors.FormatError", err)
	}
	if !strings.HasSuffix(formatErr.Path, "roster.toml") {
		t.Errorf("Path = %q, want roster.toml", formatErr.Path)
	}
	if formatErr.Line != 10 {
		t.Errorf("Line = %d, want 10", formatErr.Line)
	}
}

func TestMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.toml")
	_, err := Load(path, "")
	if err == nil {
		t.Fatal("Load() = nil error, want missing-file error")
	}
	if !strings.Contains(err.Error(), "absent.toml") {
		t.Errorf("error = %q, want it to name the path", err.Error())
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Errorf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := DefaultPath(), "/xdg/docflow/roster.toml"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := DefaultPath(), filepath.Join(home, ".config", "docflow", "roster.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestRouteProvider(t *testing.T) {
	cases := map[[2]string]string{
		{"pi", "zai/glm-5.3"}:   "zai",
		{"pi", "local-lan/llm"}: "local-lan",
		{"pi", "bare"}:          "pi",
		{"codex", "gpt-6"}:      "codex",
		{"claude", "m"}:         "claude",
	}
	for input, want := range cases {
		if got := routeProvider(input[0], input[1]); got != want {
			t.Errorf("routeProvider(%q, %q) = %q, want %q", input[0], input[1], got, want)
		}
	}
}

func TestRoutesStayInFileOrder(t *testing.T) {
	r, err := loadText(t, draft, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"glm-flash-high", "glm-flash-max", "glm-max", "luna-high", "luna-max", "sol-high", "haiku", "sonnet", "opus"}
	for i, name := range want {
		if r.Routes[i].name != name {
			t.Fatalf("Routes[%d] = %s, want %s", i, r.Routes[i].name, name)
		}
	}
}

func TestRouteTripleParsing(t *testing.T) {
	r, err := loadText(t, draft, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	haiku := agents.Route{Agent: "claude", Model: "claude-haiku-5-5"}
	if got := r.Chains["cheap"][2]; got != haiku {
		t.Errorf("cheap[2] = %+v, want %+v (no effort)", got, haiku)
	}
}

func TestLoadExpandsUserPath(t *testing.T) {
	dir := t.TempDir()
	// os.UserHomeDir under t.TempDir churn: point HOME at the temp dir.
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "cfg"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cfg", "roster.toml")
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("~/cfg/roster.toml", ""); err != nil {
		t.Fatalf("Load(~/cfg/roster.toml) error = %v", err)
	}
}
