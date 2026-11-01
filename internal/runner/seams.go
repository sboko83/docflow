package runner

import "context"

// The operations that run outside the unit loop have no boundary a test can
// wait on: their agent call returns and the operation validates and commits
// without publishing another event. These seams name those boundaries so a
// test can request a Pause there deterministically. Production leaves them as
// no-ops, and they exist for tests only.
var (
	replanSeam = func(context.Context, string) {}
	closeSeam  = func(context.Context, string) {}
)
