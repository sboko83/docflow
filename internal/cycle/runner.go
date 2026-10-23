package cycle

import "bsv-docflow/internal/format"

// FindPlan and FindUnit share cycle selection with the autonomous runner.
func FindPlan(root, id string) (*format.Plan, error) { return findPlan(root, id) }
func FindUnit(root, id string) (*format.Plan, *format.Unit, error) {
	return findUnit(root, id)
}

func CommitPending(root string, plan *format.Plan, unit *format.Unit) (bool, error) {
	return commitPending(root, plan, unit)
}
