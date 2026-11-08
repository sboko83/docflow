package roster

import (
	"fmt"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// Chain is the routes of one role in use order: the first route is the
// primary and the rest are the fallback routes. Every route comes from a
// different access, because an access is one quota and a second route to
// it would spend the same quota twice.
type Chain struct {
	Role   string      `json:"role"`
	Routes []Candidate `json:"routes"`
}

// Review is one row of the review matrix: a check access whose units need
// an independent review and the routes that can give it. A check access
// never appears on the reviewer side, so a row is never a silent
// self-review.
type Review struct {
	Access    string      `json:"access"`
	Reviewers []Candidate `json:"reviewers"`
}

// Chains is the roster's contribution to a plan header: the chain of
// every role, the review matrix and the warnings about incomplete chains.
// The resolver owns the candidate order (the access priority) and the
// role filters; the chain builder only keeps one route per access, so the
// order has a single owner.
type Chains struct {
	Roles    []Chain  `json:"roles"`
	Reviews  []Review `json:"reviews,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Routes returns the chain of one role in use order; an unknown role has
// none.
func (c *Chains) Routes(role string) []Candidate {
	if c == nil {
		return nil
	}
	for i := range c.Roles {
		if c.Roles[i].Role == role {
			return c.Roles[i].Routes
		}
	}
	return nil
}

// BuildChains builds the role chains, the review matrix and the warnings
// from a resolution. An empty chain and a chain of one route are warnings,
// not errors: the resolver does not know which roles a plan uses, and a
// plan that needs a missing role reports it against its own key. A check
// access without an independent reviewer is an error: its units would run
// unjudged, and a silent self-review is exactly what the flag forbids.
func BuildChains(resolution *Resolution) (*Chains, error) {
	if resolution == nil {
		return nil, clierrors.DocflowError{Message: "docflow: roster: no resolution to build chains from", Code: clierrors.EXIT_FORMAT}
	}
	chains := &Chains{Roles: make([]Chain, 0, len(Roles))}
	for _, role := range Roles {
		routes := dedupeAccesses(resolution.Candidates(role))
		chains.Roles = append(chains.Roles, Chain{Role: role, Routes: routes})
		switch len(routes) {
		case 0:
			chains.Warnings = append(chains.Warnings, "no routes for "+role)
		case 1:
			chains.Warnings = append(chains.Warnings, "no fallback for "+role)
		}
	}
	chains.Reviews = reviewMatrix(resolution, chains.Routes("review"))
	for _, row := range chains.Reviews {
		if len(row.Reviewers) > 0 {
			continue
		}
		return nil, clierrors.DocflowError{Message: reviewerMessage(resolution, row.Access), Code: clierrors.EXIT_FORMAT}
	}
	return chains, nil
}

// dedupeAccesses keeps the first route of every access and drops the rest:
// a fallback repeats the access, never the agent|model pair.
func dedupeAccesses(candidates []Candidate) []Candidate {
	seen := make(map[string]bool, len(candidates))
	routes := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if seen[candidate.Access] {
			continue
		}
		seen[candidate.Access] = true
		routes = append(routes, candidate)
	}
	return routes
}

// reviewMatrix pairs every check access that serves at least one role with
// the routes that can review it. The access itself is never its own
// reviewer: independence is about the access, not the agent.
func reviewMatrix(resolution *Resolution, reviewers []Candidate) []Review {
	var rows []Review
	for _, access := range checkAccesses(resolution) {
		row := Review{Access: access}
		for _, reviewer := range reviewers {
			if reviewer.Access != access {
				row.Reviewers = append(row.Reviewers, reviewer)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// checkAccesses lists the check accesses that serve at least one role, in
// policy order. An access without a route contributes no unit, so it needs
// no reviewer: a check access switched off is not an error.
func checkAccesses(resolution *Resolution) []string {
	var names []string
	if resolution.Policy == nil {
		return names
	}
	for i := range resolution.Policy.Accesses {
		access := &resolution.Policy.Accesses[i]
		if access.Check && resolution.servesAnyRole(access.Name) {
			names = append(names, access.Name)
		}
	}
	return names
}

// servesAnyRole reports whether the access has a route in any role.
func (r *Resolution) servesAnyRole(access string) bool {
	for _, candidates := range r.Roles {
		for _, candidate := range candidates {
			if candidate.Access == access {
				return true
			}
		}
	}
	return false
}

// reviewerBlock is one access that could review but does not, with the
// reason the resolver gave.
type reviewerBlock struct {
	access string
	reason string
}

// reviewerMessage says which check access has no reviewer, which
// non-check accesses the review role skipped and why, and the three ways
// out: stop requiring review, give a non-check access a review model, or
// switch the access off.
func reviewerMessage(resolution *Resolution, access string) string {
	blocks := reviewBlocks(resolution)
	var message strings.Builder
	fmt.Fprintf(&message, "docflow: roster: check access %s has no independent reviewer", access)
	if len(blocks) > 0 {
		reasons := make([]string, 0, len(blocks))
		for _, block := range blocks {
			reasons = append(reasons, block.access+": "+block.reason)
		}
		fmt.Fprintf(&message, " (%s)", strings.Join(reasons, "; "))
	}
	fmt.Fprintf(&message, ": set check = false in [access.%s]", access)
	if len(blocks) > 0 {
		fmt.Fprintf(&message, ", give %s a review cell in roster web or [access.%s.roles]", blocks[0].access, blocks[0].access)
	} else {
		message.WriteString(", give a non-check access a review cell in roster web or its [access.NAME.roles]")
	}
	fmt.Fprintf(&message, ", or roster off %s --until DATE", access)
	return message.String()
}

// reviewBlocks lists every non-check access the review role skips and why.
// An access-level reason (switched off, expired, no catalog entry) counts
// too: such an access cannot review either.
func reviewBlocks(resolution *Resolution) []reviewerBlock {
	if resolution.Policy == nil {
		return nil
	}
	reasons := make(map[string]string, len(resolution.Skipped))
	for _, skip := range resolution.Skipped {
		if skip.Role == "" || skip.Role == "review" {
			if _, ok := reasons[skip.Access]; !ok {
				reasons[skip.Access] = skip.Reason
			}
		}
	}
	var blocks []reviewerBlock
	for i := range resolution.Policy.Accesses {
		access := &resolution.Policy.Accesses[i]
		if access.Check {
			continue
		}
		if reason, ok := reasons[access.Name]; ok {
			blocks = append(blocks, reviewerBlock{access: access.Name, reason: reason})
		}
	}
	return blocks
}
