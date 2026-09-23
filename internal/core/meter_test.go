package core

import (
	"context"
	"errors"
	"testing"
)

// TestMeterTripsAtTheBudget: charges within the budget return; the charge that
// takes the run past it unwinds, records one trip, and every charge after it
// unwinds too, without recording again.
func TestMeterTripsAtTheBudget(t *testing.T) {
	rec := &Recorder{}
	run, cancel := NewMeteredRun(rec, 100, Canceler{})
	v := View{Run: run, Cancel: cancel}
	if Contain(func() { v.Charge(100) }) {
		t.Fatal("a charge up to the budget unwound")
	}
	if !Contain(func() { v.Charge(1) }) {
		t.Fatal("the charge past the budget returned")
	}
	if !v.Stopped() || !cancel.Stopped() {
		t.Error("a spent run does not report itself stopped")
	}
	if !Contain(func() { v.Charge(0) }) || !Contain(func() { cancel.Charge(1) }) || !Contain(v.CheckStopped) {
		t.Error("a charge after the trip returned")
	}
	trips := rec.Snapshot()
	if len(trips) != 1 || trips[0].Guard() != GuardWork {
		t.Fatalf("trips = %v, want one %s", trips, GuardWork)
	}
	if err := cancel.StopErr("x"); !errors.Is(err, ErrWorkLimit) {
		t.Errorf("StopErr = %v, want it to match ErrWorkLimit", err)
	}
}

// TestMeterPollsTheContext: a cancelled context unwinds the next charge that
// polls, without a trip of the meter's own (the entry point reports the
// cancellation from the context).
func TestMeterPollsTheContext(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	rec := &Recorder{}
	run, _ := NewMeteredRun(rec, DefaultMaxWork, NewCanceler(ctx))
	v := View{Run: run}
	stop()
	unwound := Contain(func() {
		for i := 0; i < 10*meterPoll; i++ {
			v.Charge(1)
		}
	})
	if !unwound {
		t.Fatal("charges after the context was cancelled kept returning")
	}
	if n := run.Meter.Used(); n > meterPoll+1 {
		t.Errorf("the cancellation was noticed after %d units, more than one poll (%d)", n, meterPoll)
	}
	if trips := rec.Snapshot(); len(trips) != 0 {
		t.Errorf("a cancellation recorded trips %v", trips)
	}
}

// TestUnmeteredChargeIsFree: outside a run nothing is metered and nothing
// unwinds.
func TestUnmeteredChargeIsFree(t *testing.T) {
	var v View
	if Contain(func() { v.Charge(1 << 40); v.CheckStopped(); Canceler{}.Charge(1 << 40) }) {
		t.Fatal("an unmetered charge unwound")
	}
}

// TestDescend: within MaxWalkDepth a walk goes on; past it, inside a run the
// run is stopped with a walk-depth trip, and outside one the walker is told to
// stop.
func TestDescend(t *testing.T) {
	var free View
	if !free.Descend(MaxWalkDepth) || free.Descend(MaxWalkDepth+1) {
		t.Error("outside a run: Descend is not true up to MaxWalkDepth and false past it")
	}
	rec := &Recorder{}
	run, cancel := NewMeteredRun(rec, DefaultMaxWork, Canceler{})
	v := View{Run: run, Cancel: cancel}
	if !Contain(func() { v.Descend(MaxWalkDepth + 1) }) {
		t.Fatal("inside a run, a walk past MaxWalkDepth returned")
	}
	if trips := rec.Snapshot(); len(trips) != 1 || trips[0].Guard() != GuardWalkDepth {
		t.Errorf("trips = %v, want one %s", trips, GuardWalkDepth)
	}
	if err := cancel.StopErr("x"); !errors.Is(err, ErrWorkLimit) {
		t.Errorf("StopErr = %v, want it to match ErrWorkLimit", err)
	}
}

// TestNestedRunSharesTheMeter: a run started with a canceler that already
// carries a meter — an embedded document's validation — charges that meter.
func TestNestedRunSharesTheMeter(t *testing.T) {
	outer, cancel := NewMeteredRun(&Recorder{}, 1000, Canceler{})
	inner, innerCancel := NewMeteredRun(&Recorder{}, DefaultMaxWork, cancel)
	if inner.Meter != outer.Meter || innerCancel.meter != outer.Meter {
		t.Fatal("the nested run has a budget of its own")
	}
}

// TestContainPassesOtherPanics: Contain swallows only the meter's unwinding.
func TestContainPassesOtherPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != "boom" {
			t.Errorf("recovered %v, want the original panic", r)
		}
	}()
	Contain(func() { panic("boom") })
	t.Error("a foreign panic was swallowed")
}
