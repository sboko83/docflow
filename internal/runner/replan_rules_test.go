package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// codeSpan matches an inline code span of Markdown text.
var codeSpan = regexp.MustCompile("`([^`\n]+)`")

// codeSpans returns the set of inline code spans of a text, whitespace
// inside a span collapsed: wrapping a span across lines does not change it.
func codeSpans(text string) map[string]bool {
	text = strings.Join(strings.Fields(text), " ")
	set := map[string]bool{}
	for _, m := range codeSpan.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}
	return set
}

// jsonVocabulary returns the keys and string values of the result-JSON
// templates of a rule text, as `key`, `value` and `key: "value"` spans.
func jsonVocabulary(text string) map[string]bool {
	set := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(\w+)": (?:"([^"<]*)"|\[)`).FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
		if m[2] != "" {
			set[m[2]] = true
			set[m[1]+": \""+m[2]+"\""] = true
		}
	}
	return set
}

// markdownSection returns the body of the `## name` section of a Markdown
// text, up to the next second-level heading.
func markdownSection(t *testing.T, text, name string) string {
	t.Helper()
	text = strings.ReplaceAll(text, "\r\n", "\n")
	start := strings.Index(text, "\n## "+name+"\n")
	if start < 0 {
		t.Fatalf("no section ## %s", name)
	}
	body := text[start+len("\n## "+name+"\n"):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}

// The replan rules live twice (the P025 and P050 gotchas): the rule text the
// runner puts into the replan prompt and the Replan section of the
// bsv-docflow-plan skill, the same procedure by hand. The test compares
// what the rules are about, not their wording: the set of code spans —
// statuses, plan keys, sections, commands — of both texts; the skill may
// also name the keys and values of the result JSON the rules spell out
// whole. A marker, key or command named on one side only is a drift; a
// rewording of either side passes. Spans that are one-sided by design are
// listed below with the reason.
func TestReplanRulesMatchPlanSkill(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "skills", "bsv-docflow-plan", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	rules := codeSpans(replanRules)
	skill := codeSpans(markdownSection(t, string(text), "Replan"))
	ruleOnly := map[string]string{
		// The prompt points to the skill; the skill does not name itself.
		"bsv-docflow-plan": "the prompt names the skill",
		// The skill names the reference roles in words: "`Role:` of review
		// and acceptance units".
		"Role: review":     "the skill says it in words",
		"Role: acceptance": "the skill says it in words",
	}
	skillOnly := map[string]string{
		// The by-hand procedure starts from the triggers; the prompt is
		// already the runner's answer to them.
		"docflow run":       "trigger of the by-hand replan",
		"blocker: \"plan\"": "trigger of the by-hand replan",
		// The runner commits a replan itself; the prompt says "Do not commit".
		"Pxxx: replan Pxxx.N.M": "commit subject the runner writes",
		// Who records a changed Verify: or a [-] move; the prompt only
		// forbids the edits the guard rejects.
		"docflow": "the CLI records a changed check by itself",
	}
	diff := func(from, to map[string]bool, allowed map[string]string) []string {
		var out []string
		for span := range from {
			if !to[span] && allowed[span] == "" {
				out = append(out, span)
			}
		}
		sort.Strings(out)
		return out
	}
	if missing := diff(rules, skill, ruleOnly); len(missing) > 0 {
		t.Errorf("the runner replan rules name %q, the Replan section of skills/bsv-docflow-plan/SKILL.md does not: change both or list the span as rule-only with a reason", missing)
	}
	vocabulary := jsonVocabulary(replanRules)
	for span := range rules {
		vocabulary[span] = true
	}
	if missing := diff(skill, vocabulary, skillOnly); len(missing) > 0 {
		t.Errorf("the Replan section of skills/bsv-docflow-plan/SKILL.md names %q, the runner replan rules (internal/runner/replan_rules.go) do not: change both or list the span as skill-only with a reason", missing)
	}
	for _, allowed := range []map[string]string{ruleOnly, skillOnly} {
		for span := range allowed {
			if (rules[span] || vocabulary[span]) && skill[span] {
				t.Errorf("%q is in both texts now: drop it from the one-sided list", span)
			}
		}
	}
}
