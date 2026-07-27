package pdf0

import (
	"fmt"
	"sort"

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
