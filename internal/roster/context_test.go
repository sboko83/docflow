package roster

import (
	"strings"
	"testing"
)

// TestContextBudgetScenario pins the arithmetic on the acceptance
// scenario: one million tokens of context, a 128k output limit, a reserve
// of ten percent, the executor and cheap shares and class factors. The
// unmatched value 284426 shows the clamp works, not that the formula
// returned the target.
func TestContextBudgetScenario(t *testing.T) {
	route := Candidate{Access: "opencode-go", ID: "deepseek-v4.1", Class: "standard", Context: 1048576, MaxOut: 131072}
	executor := unitBudget("executor", route)
	if executor == nil {
		t.Fatal("unitBudget(executor) = nil, want a budget")
	}
	if executor.Reserve != 104857 {
		t.Errorf("executor reserve = %d, want 104857 (ten percent of the context)", executor.Reserve)
	}
	if executor.Usable != 812647 {
		t.Errorf("executor usable = %d, want 812647 = 1048576 - 131072 - 104857", executor.Usable)
	}
	if executor.Target != MaxReadBudget {
		t.Errorf("executor target = %d, want the ceiling %d", executor.Target, MaxReadBudget)
	}

	cheapRoute := Candidate{Access: "opencode-go", ID: "deepseek-v4.1-flash", Class: "cheap", Context: 1048576, MaxOut: 131072}
	cheap := unitBudget("cheap", cheapRoute)
	if cheap.Target != 152371 {
		t.Errorf("cheap target = %d, want 152371", cheap.Target)
	}
	if cheap.Route != "opencode-go/deepseek-v4.1-flash" {
		t.Errorf("cheap route = %q, want the access and the model id", cheap.Route)
	}
}

// TestContextClamp keeps the recommendation inside its bounds whatever
// the window: the floor on a small context, the ceiling on a huge one.
func TestContextClamp(t *testing.T) {
	small := unitBudget("cheap", Candidate{Access: "a", ID: "m", Class: "cheap", Context: 100000, MaxOut: 1000})
	if small.Reserve != MinReserve {
		t.Errorf("reserve of a small context = %d, want the minimum %d", small.Reserve, MinReserve)
	}
	if small.Usable != 67000 {
		t.Errorf("usable of a small context = %d, want 67000", small.Usable)
	}
	if small.Target != MinReadBudget {
		t.Errorf("target of a small context = %d, want the floor %d", small.Target, MinReadBudget)
	}

	huge := unitBudget("heavy", Candidate{Access: "a", ID: "m", Class: "strong", Context: 2000000, MaxOut: 100000})
	if huge.Target != MaxReadBudget {
		t.Errorf("target of a huge context = %d, want the ceiling %d", huge.Target, MaxReadBudget)
	}
}

// TestContextUnknown keeps an unknown window out of the budget: a missing
// context prints as unknown, never as a guessed number, and no share is
// applied to it.
func TestContextUnknown(t *testing.T) {
	budget := unitBudget("executor", Candidate{Access: "a", ID: "m", Class: "standard"})
	if budget == nil {
		t.Fatal("unitBudget() = nil, want a budget saying unknown")
	}
	if budget.Target != 0 || budget.Usable != 0 {
		t.Errorf("budget of an unknown context = %+v, want no usable window and no target", budget)
	}
	if !strings.Contains(budget.Why, "context unknown") {
		t.Errorf("why = %q, want it to say context unknown", budget.Why)
	}
}

// TestContextNoUsable pins the second unknown case: a window that the
// output limit and the reserve consume leaves nothing to read.
func TestContextNoUsable(t *testing.T) {
	budget := unitBudget("executor", Candidate{Access: "a", ID: "m", Class: "standard", Context: 40000, MaxOut: 30000})
	if budget.Reserve != MinReserve {
		t.Errorf("reserve = %d, want the minimum %d", budget.Reserve, MinReserve)
	}
	if budget.Usable != -22000 {
		t.Errorf("usable = %d, want -22000", budget.Usable)
	}
	if budget.Target != 0 {
		t.Errorf("target = %d, want 0 when the window is exhausted", budget.Target)
	}
	if !strings.Contains(budget.Why, "no usable context") {
		t.Errorf("why = %q, want it to say no usable context", budget.Why)
	}
}

// TestContextUnknownMaxOut pins the tolerance of a missing output limit:
// it counts as zero and the explanation says so instead of hiding it.
func TestContextUnknownMaxOut(t *testing.T) {
	budget := unitBudget("executor", Candidate{Access: "a", ID: "m", Class: "standard", Context: 500000})
	if budget.Usable != 450000 {
		t.Errorf("usable = %d, want 450000 = 500000 - 0 - 50000", budget.Usable)
	}
	if budget.Target != 157500 {
		t.Errorf("target = %d, want 157500", budget.Target)
	}
	if !strings.Contains(budget.Why, "max-out unknown") {
		t.Errorf("why = %q, want it to name the unknown max-out", budget.Why)
	}
}

// TestContextJudgeRoles pins the scope rule: review, acceptance and replan
// judge a diff and the tests, so they get no read budget at all.
func TestContextJudgeRoles(t *testing.T) {
	route := Candidate{Access: "a", ID: "m", Class: "strong", Context: 500000, MaxOut: 100000}
	for _, role := range []string{"review", "acceptance", "replan"} {
		if budget := unitBudget(role, route); budget != nil {
			t.Errorf("unitBudget(%s) = %+v, want no read budget for a judge role", role, budget)
		}
	}
}
