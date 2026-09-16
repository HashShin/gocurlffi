//go:build !race

package browser

import "time"

// recursionBudget bounds how long a deliberately runaway script may take to
// reach the interpreter's recursion limit and give up.
//
// The work is CPU bound, so the budget has to allow for the machine rather than
// assert a speed. goja reached the limit in about 21s on this project's
// contended single-core host, which left too little room under the 30s cap the
// test used to carry, and the race detector needs more again.
const recursionBudget = 60 * time.Second
