package finding

import (
	"context"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
)

// f is a minimal finding, standing in for the concrete types each validator
// keeps. It exists to show that satisfying V takes only the three methods —
// no import of this package, and none of the root package either.
type f struct {
	rule string
	msg  string
	obj  int
}

func (v f) Error() string     { return v.msg }
func (v f) RuleID() string    { return v.rule }
func (v f) ObjectNum() int    { return v.obj }
func mk(r, m string, o int) f { return f{r, m, o} }

// TestSortIsTotal pins the reported order: rule, then object, then message.
// Two runs over one document must produce the same slice, and several checks
// range over maps internally, so this ordering is what makes that true.
func TestSortIsTotal(t *testing.T) {
	v := []f{
		mk("6.2", "zebra", 3), mk("6.1", "beta", 9), mk("6.2", "alpha", 3),
		mk("6.1", "alpha", 9), mk("6.2", "alpha", 1),
	}
	Sort(v)
	want := []f{
		mk("6.1", "alpha", 9), mk("6.1", "beta", 9),
		mk("6.2", "alpha", 1), mk("6.2", "alpha", 3), mk("6.2", "zebra", 3),
	}
	for i := range want {
		if v[i] != want[i] {
			t.Fatalf("position %d = %+v, want %+v", i, v[i], want[i])
		}
	}
}

// TestGuardedTurnsAPanicIntoAFinding is the guard on the guard: a check that
// panics on hostile input must be reported rather than crashing the run, and
// the findings it reported before panicking must survive.
func TestGuardedTurnsAPanicIntoAFinding(t *testing.T) {
	var got []f
	add := func(rule, msg string, obj int) { got = append(got, mk(rule, msg, obj)) }

	Guarded(add, func() {
		add("6.1.3", "reported before the panic", 4)
		panic("boom")
	})

	if len(got) != 2 {
		t.Fatalf("got %d findings, want the pre-panic one and the internal one: %+v", len(got), got)
	}
	if got[0].rule != "6.1.3" {
		t.Errorf("the finding reported before the panic was lost: %+v", got[0])
	}
	if got[1].rule != InternalRule {
		t.Errorf("panic finding rule = %q, want %q", got[1].rule, InternalRule)
	}
	// The message text is part of the contract: callers read it, and nothing
	// else in the tree asserts it.
	if want := "internal validator error: boom"; got[1].msg != want {
		t.Errorf("panic message = %q, want %q", got[1].msg, want)
	}
}

func TestGuardedIsATrivialPassThroughWhenNothingPanics(t *testing.T) {
	var got []f
	Guarded(func(rule, msg string, obj int) { got = append(got, mk(rule, msg, obj)) }, func() {})
	if len(got) != 0 {
		t.Errorf("a check that did not panic reported %+v", got)
	}
}

// TestReportCancellationDoesNotDoubleCount pins the dedupe. A cancelled run
// usually reports itself through the limit recorder already; saying so twice
// would make a caller counting checker findings see two events where there was
// one.
func TestReportCancellationDoesNotDoubleCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := core.NewCanceler(ctx)

	var got []f
	add := func(rule, msg string, obj int) { got = append(got, mk(rule, msg, obj)) }

	// Already reported: nothing is added.
	ReportCancellation(c, []f{mk(LimitRule, "already said so", 0)}, add)
	if len(got) != 0 {
		t.Errorf("cancellation reported twice: %+v", got)
	}

	// Not yet reported: exactly one finding is added, under LimitRule.
	ReportCancellation(c, []f{mk("6.1.3", "an ordinary finding", 0)}, add)
	if len(got) != 1 || got[0].rule != LimitRule {
		t.Fatalf("got %+v, want one %s finding", got, LimitRule)
	}
}

func TestReportCancellationIsSilentWhenNotCancelled(t *testing.T) {
	var got []f
	ReportCancellation(core.Canceler{}, []f{}, func(rule, msg string, obj int) {
		got = append(got, mk(rule, msg, obj))
	})
	if len(got) != 0 {
		t.Errorf("an uncancelled run reported %+v", got)
	}
}
