package core

import "testing"

// The type-4 PostScript step budget is enforced by this package, so its guard
// lives here with it.

// TestPSStepBudget is the C21 guard: a type-4 PostScript program's total
// operator count is bounded, so an if/ifelse fan-out cannot run unbounded work
// per pixel.
func TestPSStepBudget(t *testing.T) {
	prog := []psItem{{isNum: true, num: 1}, {isNum: true, num: 2}, {op: "add"}}

	budget := psBudget{max: DefaultMaxPostScriptSteps}
	if _, ok := psExec(prog, nil, 0, &budget); !ok {
		t.Fatal("a simple program should execute")
	}
	if budget.steps != 3 {
		t.Fatalf("step count = %d, want 3", budget.steps)
	}

	// Once the budget is spent, even a tiny program is aborted.
	budget.steps = budget.max
	if _, ok := psExec(prog, nil, 0, &budget); ok {
		t.Fatal("a program exceeding the step budget must be aborted")
	}
}
