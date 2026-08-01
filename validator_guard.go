package pdf0

import (
	"fmt"
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

// internalRule is the rule (or clause) identifier a validator reports when one
// of its own checks panicked. It marks a finding as "the validator broke", not
// "the file is non-conforming", so a caller can tell the two apart.
const internalRule = "internal"

// internalMessage formats the message of an internal-error finding.
func internalMessage(r any) string {
	return fmt.Sprintf("internal validator error: %v", r)
}

// runGuardedCheck runs one check of a validator that reports its findings
// through an add callback, converting a panic into a reported finding instead
// of letting it crash the caller. The validators process untrusted files, so a
// bug (or an adversarial structure) in one check must not take down the whole
// process — the rationale runCheck records for PDF/A.
//
// Findings the check already reported before panicking are kept: add appends to
// the caller's slice as the check runs. Stack overflows from unbounded
// recursion are fatal and cannot be recovered here; those are prevented at
// their source (see the seen-set in devColorScanner, audit C3).
func runGuardedCheck(add func(rule, msg string, obj int), check func()) {
	defer func() {
		if r := recover(); r != nil {
			add(internalRule, internalMessage(r), 0)
		}
	}()
	check()
}

// runUACheck is runGuardedCheck for the PDF/UA checks, which return their
// findings rather than reporting them through a callback. A panicking check
// loses its own findings but not those of its siblings.
func runUACheck(check func() []UAViolation) (out []UAViolation) {
	defer func() {
		if r := recover(); r != nil {
			out = []UAViolation{{Clause: internalRule, Message: internalMessage(r)}}
		}
	}()
	return check()
}

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
		case e.Rule == internalRule || e.Rule == limitRule:
			add(e.Rule, e.Message, e.Object)
		case e.Rule == "6.6.4" && strings.Contains(e.Message, "pdfaid:conformance"):
			// Not a container finding.
		default:
			add(prefix+e.Rule, e.Message, e.Object)
		}
	}
}

// sortViolations orders findings by rule, then object, then message, the order
// ValidatePDFABytes returns its violations in. The checks iterate map-ordered
// doc.Objects, so their concatenated output order is otherwise nondeterministic
// (audit C27). Error() is used as the last key because it embeds the message
// behind a prefix that is constant for a given rule and object.
func sortViolations[T Violation](v []T) {
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

// sortFormalisViolations is sortViolations for the Factur-X / Order-X findings,
// which use the external formalis.Violation type and so cannot satisfy the
// Violation interface this package defines.
func sortFormalisViolations(v []formalis.Violation) {
	sort.Slice(v, func(i, j int) bool {
		if v[i].Rule != v[j].Rule {
			return v[i].Rule < v[j].Rule
		}
		if v[i].Object != v[j].Object {
			return v[i].Object < v[j].Object
		}
		return v[i].Message < v[j].Message
	})
}
