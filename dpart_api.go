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

// DPartViolation is a document-part hierarchy conformance failure.
type DPartViolation = dpart.DPartViolation

// ValidateDParts checks a document's DPart hierarchy against ISO 32000-2 clause
// 14.12. A document without a /DPartRoot in its catalog has no hierarchy and is
// reported as valid (nil), since the structure is optional. The checks cover:
// the DPartRoot and DPartRootNode wiring (Table 408), each node's /Type,
// required /Parent up-link and its target (14.12.2), the exclusive /DParts vs
// /Start+/End roles (Table 409), the leaf page ranges partitioning every page
// exactly once in page-tree order (14.12.2/14.12.3), page /DPart back-references
// (14.12.3), /NodeNameList depth (Table 408), and DPM key/value constraints
// (14.12.4.2).
func ValidateDParts(doc *Document) []DPartViolation {
	return validateDParts(core.Canceler{}, doc)
}

// ValidateDPartsContext is ValidateDParts with cancellation; a cancelled run
// reports itself under the rule "limit" (see cancel.go).
func ValidateDPartsContext(ctx context.Context, doc *Document) []DPartViolation {
	return validateDParts(core.NewCanceler(ctx), doc)
}
func validateDParts(cancel core.Canceler, doc *Document) []DPartViolation {
	rd := beginRunCancel(doc, cancel)
	v := rd
	var out []DPartViolation
	add := func(rule, msg string, obj int) {
		out = append(out, DPartViolation{Rule: rule, Message: msg, Object: obj})
	}

	// The hierarchy walk is one traversal rather than a list of independent
	// checks, so it gets a single recover boundary at the entry point: a panic
	// on hostile input becomes an "internal" finding instead of crashing the
	// caller, and the findings reported before it are kept (audit C27). Being one
	// traversal, it is also the whole of this validator's cancellation
	// granularity: an already-cancelled run skips it, and a run cancelled during
	// it completes the walk. The walk is bounded by the page and DPart counts and
	// reads no content, so that is bounded work, not an open-ended wait.
	if !v.view().Cancel.Stopped() {
		finding.Guarded(add, func() { dpart.ValidateHierarchy(v.view(), add) })
	}

	// The walk visits map-ordered structures, so the output order is otherwise
	// nondeterministic; sort for stable, diffable reports.
	// Guard trips are reported under their own rule, not as conformance
	// failures (see limits.go).
	reportLimits(rd, add)

	finding.Sort(out)
	return out
}
