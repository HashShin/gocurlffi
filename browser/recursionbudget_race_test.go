//go:build race

package browser

import "time"

// Under the race detector the interpreter runs at roughly half speed: the same
// runaway recursion took well over 30s, which is why the budget is larger here
// than in the normal build. Without this the test failed for reasons that had
// nothing to do with the behaviour it exists to check.
const recursionBudget = 3 * time.Minute
