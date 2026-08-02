// Package finding holds the machinery every validator shares for producing
// findings: the panic boundary each check runs behind, the deterministic order
// they are reported in, and the two rule identifiers that name the checker
// rather than the document.
//
// It deliberately does not define any finding *type*. Each standard keeps its
// own — ValidationError, pdfua.Violation, pdfx.Violation and the rest — with the
// fields and the Error formatting of that standard, because a PDF/A clause and
// a Matterhorn checkpoint are not the same thing and flattening them into one
// struct would lose which is which.
package finding

import (
	"fmt"
	"sort"

	"github.com/mgilbir/pdf0/internal/core"
)

// V is the shape of a finding: an error that can name the rule it violates and
// the object it attaches to.
//
// It is declared here rather than imported from the root package, and that is
// the point. Go satisfies interfaces structurally, so a finding type satisfies
// both this and the root package's Violation without knowing either exists —
// which lets a validator move into a package of its own without importing the
// package it is a part of. Violation stays where it is documented; this is the
// same shape, seen from below.
type V interface {
	error
	RuleID() string
	ObjectNum() int
}

// Rule identifiers that name the checker rather than the document. A finding
// under either means pdf0 could not answer, not that the file is wrong: "6.1.3"
// says the file is bad, these two say we stopped short.
const (
	// InternalRule marks a check that panicked and was recovered.
	InternalRule = "internal"
	// LimitRule marks a resource guard that tripped, or a cancelled run.
	//
	// A cancelled run reports itself here rather than under an identifier of its
	// own. A caller's deadline is not a resource guard, but it produces exactly
	// the same event — the checker stopped before it had seen everything — and so
	// calls for the same honesty. A separate identifier would have meant every
	// caller that already distinguishes "the file is bad" from "pdf0 could not
	// finish" learning a second way to spell the second one.
	LimitRule = "limit"
)

// Sort puts findings in a deterministic order: by rule, then by object number,
// then by message.
//
// Every validator sorts its output through here. Two runs over the same
// document must produce the same slice — checks execute in a fixed order but
// several of them range over maps internally, and Go randomises map iteration
// on every run.
func Sort[T V](v []T) {
	sort.Slice(v, func(i, j int) bool {
		if a, b := v[i].RuleID(), v[j].RuleID(); a != b {
			return a < b
		}
		if a, b := v[i].ObjectNum(), v[j].ObjectNum(); a != b {
			return a < b
		}
		return v[i].Error() < v[j].Error()
	})
}

// InternalMessage renders a recovered panic as a finding message.
func InternalMessage(r any) string {
	return fmt.Sprintf("internal validator error: %v", r)
}

// Guarded runs check behind a panic boundary, reporting a recovered panic
// through add as an InternalRule finding.
//
// A check that panics on hostile input must not take the run down with it, and
// must not silently vanish either: its siblings' findings stay, and the caller
// is told that one check could not be completed.
func Guarded(add func(rule, msg string, obj int), check func()) {
	defer func() {
		if r := recover(); r != nil {
			add(InternalRule, InternalMessage(r), 0)
		}
	}()
	check()
}

// ReportCancellation appends a finding when the run was cancelled, so that a
// short list of findings is never read as a clean document.
//
// It is a no-op when the findings already carry one: a cancelled run usually
// reports itself through the limit recorder, and saying so twice would make a
// caller counting checker findings double-count the single event.
func ReportCancellation[T V](cancel core.Canceler, v []T, add func(rule, msg string, obj int)) {
	err := cancel.Err()
	if err == nil {
		return
	}
	for _, e := range v {
		if e.RuleID() == LimitRule {
			return
		}
	}
	add(LimitRule, core.NewTrip(core.GuardCanceled, err.Error(), 0).Message(), 0)
}

// IsCheckerFinding reports whether a finding describes a problem in the checker
// rather than a non-conformance of the document — a recovered panic, or a
// resource guard that stopped a check short. A caller asking "is this file
// conformant?" should read either as "unknown", not as a failure.
func IsCheckerFinding(v V) bool {
	switch v.RuleID() {
	case LimitRule, InternalRule:
		return true
	}
	return false
}
