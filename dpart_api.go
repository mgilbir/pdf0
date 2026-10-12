package pdf0

import (
	"context"

	"github.com/mgilbir/pdf0/dpart"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
)

// The DPart API. The hierarchy walk lives in the dpart package and reads the
// document through a core.View; this is the boundary that starts the run and
// reports the guards that tripped while the file was read.

// ValidateDParts checks a document's DPart hierarchy against ISO 32000-2 clause
// 14.12. A document without a /DPartRoot in its catalog has no hierarchy and is
// reported as valid (nil), since the structure is optional. The checks cover:
// the DPartRoot and DPartRootNode wiring (Table 408), each node's /Type,
// required /Parent up-link and its target (14.12.2), the exclusive /DParts vs
// /Start+/End roles (Table 409), the leaf page ranges partitioning every page
// exactly once in page-tree order (14.12.2/14.12.3), page /DPart back-references
// (14.12.3), /NodeNameList depth (Table 408), and the types of DPM values
// (14.12.4.2). DPM keys are deliberately not checked: PDF/VT encodes field
// names into keys that are not XML name tokens.
//
// The walk charges the run's work meter, so it is bounded by the work budget
// (WithMaxWork) like every other validator, and a context cancelled during it
// (ValidateDPartsContext) stops it at the meter's next poll. Either way the
// result carries a finding under the rule "limit" (IsCheckerFinding), and the
// findings reported before the stop are kept.
func ValidateDParts(doc *Document) []dpart.Violation {
	return validateDParts(core.Canceler{}, doc)
}

// ValidateDPartsContext is ValidateDParts with cancellation; a cancelled run
// reports itself under the rule "limit" (see cancel.go).
func ValidateDPartsContext(ctx context.Context, doc *Document) []dpart.Violation {
	return validateDParts(core.NewCanceler(ctx), doc)
}
func validateDParts(cancel core.Canceler, doc *Document) []dpart.Violation {
	if doc == nil {
		return []dpart.Violation{{Rule: finding.LimitRule, Message: nilDocumentMessage}}
	}
	rd := beginRunCancel(doc, cancel)
	v := rd
	var out []dpart.Violation
	add := func(rule, msg string, obj int) {
		out = append(out, dpart.Violation{Rule: rule, Message: msg, Object: obj})
	}

	// The hierarchy walk is one traversal rather than a list of independent
	// checks, so it gets a single recover boundary at the entry point: a panic
	// on hostile input becomes an "internal" finding instead of crashing the
	// caller, and the findings reported before it are kept (audit C27). An
	// already-cancelled run skips it. The walk reads no content, but its cost is
	// not bounded by the page and DPart counts alone: each leaf's range is
	// walked, so it is leaves × pages (C42). It charges the run's work meter,
	// which ends it when the budget is spent or the context is cancelled; the
	// meter's unwinding is not a fault (finding.Guarded), and the trip is
	// reported below with the run's others.
	if !v.view().Cancel.Stopped() {
		finding.Guarded(add, func() { dpartValidateHierarchy(v.view(), add) })
	}

	// Guard trips are reported under their own rule, not as conformance
	// failures (see limits_report.go).
	reportLimits(rd, add)

	// The walk visits map-ordered structures, so the output order is otherwise
	// nondeterministic; sort for stable, diffable reports.
	finding.Sort(out)
	return out
}
