package pdf0

import (
	"github.com/mgilbir/pdf0/internal/finding"
	"sort"
	"strings"

	"github.com/mgilbir/formalis"
)

// This file gives every validator the two properties the PDF/A validator has
// had since it was written (audit C27): a panic boundary around each check, so
// a bug or an adversarial structure in one rule cannot crash the caller, and a
// deterministic output order, so reports are stable and diffable.
//
// The PDF/A twins are runCheck / runByteCheck in pdfa.go; the helpers here are
// the same idea adapted to the other validators' finding types and to the
// `add(rule, msg, obj)` reporting style they share.

// runUACheck is finding.Guarded for the PDF/UA checks, which return their
// findings rather than reporting them through a callback. A panicking check
// loses its own findings but not those of its siblings.

// adoptPDFAFindings replays the findings of a composed PDF/A validation through
// an add callback, namespacing each rule under prefix. ValidateFacturX and
// ValidateOrderX use it to fold their PDF/A-3 base into a container report.
//
// The two reserved checker identifiers keep their bare names. "internal" and
// "limit" say that *pdf0* could not finish rather than that the document is
// wrong (see IsCheckerFinding), and a wrapper that renamed them to
// "pdfa-3/limit" would leave a caller of the composed validator with no
// documented spelling for that distinction. The prefix exists to keep two rule
// *namespaces* from colliding, and these two identifiers belong to neither.
//
// The A-vs-B conformance-letter finding is dropped: pdf0 validates at level B,
// and PDF/A-3 also permits level A, which only adds tagging.
func adoptPDFAFindings(add func(rule, msg string, obj int), prefix string, errs []ValidationError) {
	for _, e := range errs {
		switch {
		case e.Rule == finding.InternalRule || e.Rule == finding.LimitRule:
			add(e.Rule, e.Message, e.Object)
		case e.Rule == "6.6.4" && strings.Contains(e.Message, "pdfaid:conformance"):
			// Not a container finding.
		default:
			add(prefix+e.Rule, e.Message, e.Object)
		}
	}
}

// sortedObjectNums returns every object number in doc.Objects in ascending
// order. Checks that must be reproducible iterate it instead of ranging the map
// directly: Go randomises map iteration order on every run, so any check whose
// output depends on WHICH object it reaches first — rather than on the set of
// objects it reaches — reported a different object number each time the same
// file was validated. Ascending object number is a total order, so it does not.
//
// Only the checks that are order-sensitive pay for the sort; the many checks
// that emit one finding per object and are sorted afterwards keep ranging the
// map directly.
func sortedObjectNums(doc *Document) []int {
	nums := make([]int, 0, len(doc.Objects))
	for num := range doc.Objects {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	return nums
}

// exampleFindings collects at most one ValidationError per distinct rule and
// message. Several rules report a single representative example rather than
// every occurrence, and their candidates arrive from a range over doc.Objects,
// doc.Offsets or collectContentStreamData — Go maps, whose iteration order is
// randomised on every run. Keeping whichever candidate the range happened to
// yield first therefore named a different object each time the same file was
// validated. Keeping the numerically smallest object number instead is a total
// order over the candidates, so the report is reproducible. The choice is
// load-bearing, not incidental: reports are diffed run against run.
//
// Emission order is deliberately not part of the contract — ValidatePDFABytes
// sorts the concatenated findings before returning them.
type exampleFindings struct {
	idx  map[string]int // rule+message -> index into errs
	errs []ValidationError
}

// add records e, or — when a finding with the same rule and message is already
// held — lowers that finding's object number to e's when e's is smaller.
func (f *exampleFindings) add(e ValidationError) {
	key := e.Rule + "\x00" + e.Message
	if i, ok := f.idx[key]; ok {
		if e.Object < f.errs[i].Object {
			f.errs[i].Object = e.Object
		}
		return
	}
	if f.idx == nil {
		f.idx = make(map[string]int)
	}
	f.idx[key] = len(f.errs)
	f.errs = append(f.errs, e)
}

// adoptInvoiceFindings replays the findings of a formalis Report through an
// adopt callback, so ValidateFacturX and ValidateOrderX can carry them in their
// own finding type alongside the container findings they made themselves.
//
// Unlike the PDF/A-3 findings adoptPDFAFindings folds in, these keep their rule
// identifiers verbatim, with no namespace prefix. There is nothing to
// disambiguate: formalis mints its identifiers in its own space (EN 16931's
// BR-*, Order-X's ORDER-*, and the reserved "limit", "profile" and "root"),
// pdf0's container identifiers are "structure", "attachment", "metadata" and the
// unreadable-XML rules, and the two sets are disjoint. What a prefix would have
// stood in for — which authority wrote the rule — is carried as data instead, in
// FacturXViolation.Source, where a caller can key on it rather than parse it out
// of a string.
//
// Two consequences of adopting verbatim are the point rather than a side effect:
//
//   - formalis.RuleLimit is "limit", the identifier pdf0 reserves for the same
//     event, so a cancelled or budget-stopped rule engine reaches the caller as a
//     finding IsCheckerFinding recognises, with no special case here. That is the
//     property adoptPDFAFindings has to preserve by hand for the PDF/A-3 half,
//     and the reason both halves must spell it the same way.
//   - formalis.RuleProfile ("profile") is deliberately not folded into that set,
//     although formalis.IsCheckerViolation covers it. It reports that the profile
//     pdf0 passed is not one formalis implements, and pdf0 only ever passes one
//     it read out of the container's XMP — so the finding arises exactly when
//     fx:ConformanceLevel is missing or unrecognised, which is a defect in the
//     document that pdf0 has already reported as a "metadata" finding of its own.
//     It is not a report that pdf0 stopped early, and classifying it as one would
//     hide a real container defect from a caller counting non-conformances.
//
// The advisory flag is what keeps the two halves of the report from being
// conflated. The rule engine reports findings at two severities its authorities
// published — CEN flags 1,168 of the two EN 16931 syntax bindings' assertions
// warning rather than fatal, and a conforming Factur-X EXTENDED invoice trips
// dozens of them by design, since they hold a document down to the EN 16931 core
// subset of CII. Those are not non-conformances and must not land in Violations:
// pdf0's Violation interface carries no severity, so a warning folded into that
// slice would be indistinguishable from a PDF/A-3 failure the moment a caller
// appended it to a mixed report — the reclassification the severity exists to
// prevent, one level up. They are kept, in a field of their own, because
// discarding them would throw away the only reading anyone has of those rules.
func adoptInvoiceFindings(adopt func(v formalis.Violation, advisory bool), rep formalis.Report) {
	for _, v := range rep.Violations {
		adopt(v, v.Severity == formalis.SeverityWarning)
	}
}
