package roster

import (
	"net"
	"net/url"
	"strings"
)

// Access kinds: what spending a route of the access costs. The scan
// derives the kind of the accesses it recognizes; kind = in the policy
// overrides it.
const (
	KindSubscription = "subscription" // a plan's quota: no price per token
	KindMetered      = "metered"      // pay per token
	KindLocal        = "local"        // a model server on this machine or the LAN
)

// knownAccesses names the accesses the scan recognizes: a title the
// editor shows next to the bare provider id, and the kind of the plan
// behind the id. The ids are those of Pi, OpenCode and the built-in table:
// "opencode" is OpenCode Zen (pay per token, plus a few free models),
// "opencode-go" is the OpenCode Go subscription — the two are easy to
// confuse by name alone. An access outside the table has no title and no
// kind: a guess about someone else's plan is worse than a blank.
var knownAccesses = map[string]struct{ title, kind string }{
	"opencode":       {"OpenCode Zen", KindMetered},
	"opencode-go":    {"OpenCode Go", KindSubscription},
	"zai":            {"Z.ai Coding Plan", KindSubscription},
	"codex":          {"Codex", KindSubscription},
	"claude":         {"Claude Code", KindSubscription},
	"github-copilot": {"GitHub Copilot", KindSubscription},
	"openai-codex":   {"ChatGPT (Codex)", KindSubscription},
	"openrouter":     {"OpenRouter", KindMetered},
	"deepseek":       {"DeepSeek API", KindMetered},
}

// AccessTitle is the human name of a known access, "" otherwise.
func AccessTitle(access string) string {
	return knownAccesses[access].title
}

// knownKind is the kind of a known access, "" otherwise.
func knownKind(access string) string {
	return knownAccesses[access].kind
}

// localBaseURL reports whether a provider base URL points at this machine
// or the local network: loopback, a private or link-local address, or a
// host name without a dot or under .local/.lan/.home.arpa. Such a provider
// is a model server of the user's own, with no quota and no price.
func localBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home.arpa", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
