package core

import (
	"github.com/mgilbir/pdf0/internal/hostile"
	"testing"
	"time"
)

// The type-4 PostScript step budget is enforced by this package, so its guard
// lives here with it.

// TestPSStepBudget is the C21 guard: a type-4 PostScript program's operators
// are counted, so an if/ifelse fan-out cannot run unbounded work. In a run they
// charge the run's work meter, which bounds every evaluation of the run
// together (audit 2026-09-22 C51); outside one, an evaluation is held to
// maxPSStepsUnmetered on its own.
func TestPSStepBudget(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		prog := []psItem{{isNum: true, num: 1}, {isNum: true, num: 2}, {op: "add"}}

		budget := psBudget{doc: View{}}
		if _, ok := psExec(prog, nil, 0, &budget); !ok || !budget.flush() {
			t.Fatal("a simple program should execute")
		}
		if budget.total != 3 {
			t.Fatalf("step count = %d, want 3", budget.total)
		}

		// Unmetered: once an evaluation has spent its own bound, even a tiny
		// program is aborted.
		budget.total = maxPSStepsUnmetered
		if _, ok := psExec(prog, nil, 0, &budget); ok && budget.flush() {
			t.Fatal("a program exceeding the unmetered step bound must be aborted")
		}

		// Metered: the steps are the run's, and a run whose budget they
		// exhaust is stopped — across evaluations, not within one.
		rec := &Recorder{}
		run, _ := NewMeteredRun(rec, 1000, Canceler{})
		v := View{Run: run}
		evals := 0
		aborted := Contain(func() {
			for i := 0; i < 1000; i++ {
				b := psBudget{doc: v}
				if _, ok := psExec(prog, nil, 0, &b); !ok || !b.flush() {
					t.Fatal("a metered evaluation within the budget failed")
				}
				evals++
			}
		})
		if !aborted || evals >= 1000 {
			t.Fatalf("1000 evaluations of 3 steps ran %d times under a 1000-unit budget (aborted=%v)", evals, aborted)
		}
		if trips := rec.Snapshot(); len(trips) != 1 || trips[0].Guard() != GuardWork {
			t.Fatalf("trips = %v, want one %s", trips, GuardWork)
		}
	})
}
