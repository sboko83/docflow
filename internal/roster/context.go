package roster

import (
	"fmt"
	"math"
)

// The unit read budget is a planning recommendation, not a plan key and
// not a lint check: it tells the planner how large a unit of a role may
// get, and the plan itself stays limited to the routes it carries. The
// numbers are tokens, never bytes — a byte count would inflate the budget
// on the Russian documents this project plans in.
const (
	// MinReadBudget and MaxReadBudget clamp the recommendation: below the
	// floor a unit is too small to be worth a session, above the ceiling
	// the context is no longer what limits the model.
	MinReadBudget = 20000
	MaxReadBudget = 250000

	// ReservePercent is the part of the context kept for the prompt, the
	// tools and the reply beyond max_out: a request that fills the window
	// to max_out has no room left to answer in.
	ReservePercent = 10

	// MinReserve is the smallest reserve in tokens: ten percent of a small
	// window is not enough for the fixed part of a session.
	MinReserve = 32000
)

// readShares maps the roles that read the repository to the share of the
// usable context one unit of the role may spend: a cheap unit is small, a
// heavy unit is large. The three judge roles are absent on purpose: they
// read a diff and test output, not the repository.
var readShares = map[string]float64{
	"cheap":    0.25,
	"executor": 0.35,
	"heavy":    0.45,
}

// UnitBudget is the read budget recommendation for one unit of one role,
// computed from the primary route of its chain. A zero Context, Usable or
// Target means unknown: a model whose window the catalog does not know
// gets no budget instead of a guess, and the brief says "context
// unknown".
type UnitBudget struct {
	Role    string `json:"role"`
	Route   string `json:"route"`
	Context int    `json:"context,omitempty"`
	MaxOut  int    `json:"max_out,omitempty"`
	Reserve int    `json:"reserve,omitempty"`
	Usable  int    `json:"usable,omitempty"`
	Target  int    `json:"target,omitempty"`
	Why     string `json:"why"`
}

// roleReads reports whether a unit of the role reads the repository: only
// these three roles get a read budget.
func roleReads(role string) bool {
	_, ok := readShares[role]
	return ok
}

// unitBudget computes the read budget of a role from its primary route:
//
//	usable = context - max_out - reserve
//	target = clamp(usable * share, MinReadBudget, MaxReadBudget)
//
// A judge role has no read budget, so it returns nil. An unknown context
// or a context that leaves nothing usable returns a budget with a zero
// Target and the reason in Why: the planner must not read a missing
// number as a real one.
func unitBudget(role string, route Candidate) *UnitBudget {
	if !roleReads(role) {
		return nil
	}
	budget := &UnitBudget{
		Role:    role,
		Route:   modelAtEffort(route),
		Context: route.Context,
		MaxOut:  route.MaxOut,
	}
	if budget.Context <= 0 {
		budget.Why = "context unknown: no read budget for a model whose window the catalog does not know"
		return budget
	}
	budget.Reserve = budget.Context * ReservePercent / 100
	if budget.Reserve < MinReserve {
		budget.Reserve = MinReserve
	}
	budget.Usable = budget.Context - budget.MaxOut - budget.Reserve
	if budget.Usable <= 0 {
		budget.Why = fmt.Sprintf("no usable context: context %d - max-out %d - reserve %d is not positive", budget.Context, budget.MaxOut, budget.Reserve)
		return budget
	}
	share := readShares[role]
	raw := int(math.Round(float64(budget.Usable) * share))
	budget.Target = clampReadBudget(raw)
	note := ""
	if budget.MaxOut <= 0 {
		note = ", max-out unknown and counted as 0"
	}
	budget.Why = fmt.Sprintf(
		"budget %d: usable %d = context %d - max-out %d - reserve %d; * share %s (%s) = %d, clamped to %d..%d%s",
		budget.Target, budget.Usable, budget.Context, budget.MaxOut, budget.Reserve,
		levelText(share), role, raw, MinReadBudget, MaxReadBudget, note,
	)
	return budget
}

// clampReadBudget keeps a budget inside the recommendation bounds.
func clampReadBudget(target int) int {
	switch {
	case target < MinReadBudget:
		return MinReadBudget
	case target > MaxReadBudget:
		return MaxReadBudget
	}
	return target
}

// levelText prints a share with two decimals and without an
// exponent, so the explanation reads the same in every locale.
func levelText(value float64) string {
	return fmt.Sprintf("%.2f", value)
}
