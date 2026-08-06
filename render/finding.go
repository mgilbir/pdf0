// Package render lays HTML and CSS out onto a PDF page.
//
// This file is its guardrail vocabulary, and it exists before the layout engine
// on purpose. §9 of the rendering proposal asks for the reporting layer to land
// *with* the engine rather than after it, and gives the reason: a reporting
// layer retrofitted onto a finished engine is how it becomes decorative. The
// engine grows into this, not the other way round.
//
// # What the guardrails are for
//
// Layout degrades *silently*. That is its characteristic failure and the whole
// argument of §6. A clipped paragraph, a 3pt caption and a heading full of tofu
// all produce a valid PDF that a caller has no programmatic way to distrust —
// the file opens, the text is selectable, nothing errors. So every way this
// engine can quietly produce a document that is not what was asked for is a
// named rule with an identifier, and a caller can decide for each one whether it
// is worth failing over.
package render

import (
	"fmt"
	"sort"
	"strings"
)

// Rule identifies a guardrail.
//
// The identifiers are the ones §6 names, and they are strings rather than an
// enumeration because they travel: they are what a caller matches on, what a
// configuration file names, and what a report is grouped by. An integer would be
// none of those.
type Rule string

// The rules this engine can currently report.
//
// This is deliberately *not* the whole catalogue of §6. The geometry rules —
// min-font-size, unbreakable-overflow, overflow-page and the rest — arrive with
// the layout that can violate them, each together with the test that plants a
// violation and watches it fire. §6.5 asks for exactly that, and declaring a
// rule before anything can raise it would produce the decoration it warns
// against: an identifier in a catalogue that has never been seen to fire proves
// nothing at all. TestEveryRuleIsReachable holds that line.
const (
	// RuleUnsupportedProperty is a declaration parsed and then not applied.
	// §6.3 argues this is the highest-value guardrail for the least cost, and
	// it is: an engine implementing a subset *will* ignore declarations, and a
	// page where flex-wrap was dropped is plausible and wrong.
	RuleUnsupportedProperty Rule = "unsupported-property"
	// RuleUnsupportedElement is an element the engine does not lay out.
	RuleUnsupportedElement Rule = "unsupported-element"
	// RuleUnsupportedSelector is a selector outside the implemented subset, so
	// the rule using it never applied.
	RuleUnsupportedSelector Rule = "unsupported-selector"
	// RuleUnsupportedAtRule is an at-rule the engine does not act on.
	RuleUnsupportedAtRule Rule = "unsupported-at-rule"
	// RuleUnsupportedValue is a value that is correct CSS the engine cannot
	// resolve — a unit needing font metrics, a colour in a space needing
	// conversion.
	RuleUnsupportedValue Rule = "unsupported-value"

	// RuleInvalidMarkup and RuleInvalidCSS are input the engine refused. They
	// are not the same as the unsupported rules and must not be reported as
	// them: one says the author wrote something wrong, the other says the
	// engine does not do something. An author sent to the wrong one of those
	// looks for the wrong thing.
	RuleInvalidMarkup Rule = "invalid-markup"
	RuleInvalidCSS    Rule = "invalid-css"

	// RuleFontFallback is a requested family that was not available, so the
	// text was set in something else. The metrics and the line breaks differ,
	// and nothing about the page says so.
	RuleFontFallback Rule = "font-fallback"
	// RuleUnsupportedScript is text this engine cannot break or order
	// correctly. §6.3 makes it an error by default, and is right to: unbroken or
	// unordered text still looks like text, so the failure mode looks like
	// success.
	RuleUnsupportedScript Rule = "unsupported-script"
	// RuleGlyphMissing is a character no available face has a glyph for. Tofu is
	// the purest form of silent garbage — a box where a letter should be, which
	// a reader blames on their PDF viewer.
	RuleGlyphMissing Rule = "glyph-missing"

	// The size thresholds of §6.1, which are checkable exactly because §5's
	// scaling is geometric: the effective size of an element is its natural size
	// times one number, so a threshold is a multiplication rather than an
	// iteration.
	//
	// RuleMinScale is the blunt one and probably the most useful: if the content
	// had to be shrunk past half to fit, the document is wrong, and no
	// per-element threshold is needed to say so.
	RuleMinScale Rule = "min-scale"
	// RuleMinFontSize is text that would be set below a legible size.
	RuleMinFontSize Rule = "min-font-size"

	// RuleLimit is a resource guard that tripped, or a run that was cancelled.
	//
	// It is spelled the same as internal/finding.LimitRule, and deliberately so:
	// every other part of pdf0 already reports "we stopped short" under that
	// identifier, and a caller that distinguishes "the input is bad" from
	// "pdf0 could not finish" should not have to learn a second spelling for
	// the second one.
	RuleLimit Rule = "limit"
)

// Severity is what a rule does when it fires.
type Severity uint8

const (
	// Ignore drops the finding entirely. It is not the default for anything —
	// a caller has to ask for silence.
	Ignore Severity = iota
	// Warn records the finding and lets the render finish.
	Warn
	// Error records the finding and makes the render fail, so no document is
	// returned. This is for the cases where a produced document would be worse
	// than none: one that looks finished and is not.
	Error
)

func (s Severity) String() string {
	switch s {
	case Ignore:
		return "ignore"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "unknown"
}

// defaultSeverity is what each rule does unless a caller says otherwise.
//
// Most are Warn: an unsupported property produces a page that is wrong in a way
// the author can see and decide about. The two that are Error are the ones §6.3
// names, where the wrongness is invisible — text in the wrong order, or a row of
// boxes where letters should be, both of which a reader blames on their viewer
// rather than on the document. The remaining Error defaults belong to the size
// thresholds of §6.1 and arrive with the layout that can produce them.
var defaultSeverity = map[Rule]Severity{
	RuleUnsupportedProperty: Warn,
	RuleUnsupportedElement:  Warn,
	RuleUnsupportedSelector: Warn,
	RuleUnsupportedAtRule:   Warn,
	RuleUnsupportedValue:    Warn,
	RuleFontFallback:        Warn,
	// The two errors. Both produce a page that looks finished and is not, which
	// is the case where returning no document is better than returning one.
	RuleUnsupportedScript: Error,
	RuleGlyphMissing:      Error,
	// A document that only fitted by being made illegible is one where no
	// document is better than the document.
	RuleMinScale:      Error,
	RuleMinFontSize:   Error,
	RuleInvalidMarkup: Warn,
	RuleInvalidCSS:    Warn,
	RuleLimit:         Warn,
}

// AllRules returns every rule this engine can report, in a fixed order.
//
// It exists so that a caller can enumerate what it might be told, and so that
// the tests can require each one to have been seen to fire.
func AllRules() []Rule {
	out := make([]Rule, 0, len(defaultSeverity))
	for r := range defaultSeverity {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Policy is a caller's choice of severity per rule. A rule absent from it keeps
// its default.
type Policy map[Rule]Severity

// severityOf returns the severity a policy gives a rule.
func (p Policy) severityOf(r Rule) Severity {
	if s, ok := p[r]; ok {
		return s
	}
	if s, ok := defaultSeverity[r]; ok {
		return s
	}
	// A rule with no default is one nothing declared. Warning is the safe
	// answer: silence would hide it, and failing would make adding a rule a
	// breaking change.
	return Warn
}

// Source says where in the input a finding came from.
//
// Both offsets are byte offsets into the document the caller supplied, and both
// are -1 when they do not apply. That is what lets a caller point an author at
// the markup or the stylesheet rather than at a description of it, and it cannot
// be recovered afterwards — which is why the html, css and style packages carry
// offsets at all.
type Source struct {
	// HTMLOffset is a byte offset into the HTML, or -1.
	HTMLOffset int
	// CSSOffset is a byte offset into the stylesheet, or -1.
	CSSOffset int
	// Sheet names which stylesheet CSSOffset is in, when there is more than
	// one. It is empty for the document's own.
	Sheet string
}

// NoSource is a finding that is not tied to a place in the input.
var NoSource = Source{HTMLOffset: -1, CSSOffset: -1}

// AtHTML and AtCSS build the two common cases.
func AtHTML(offset int) Source { return Source{HTMLOffset: offset, CSSOffset: -1} }

func AtCSS(offset int) Source { return Source{HTMLOffset: -1, CSSOffset: offset} }

// Finding is one guardrail firing.
//
// It satisfies pdf0's Violation interface — error, RuleID and ObjectNum — so
// findings from a render collect into one slice alongside those from
// ValidatePDFA and ValidatePDFUA, which is the whole point of that interface.
// The interface is satisfied structurally and is not imported here, so this
// package does not depend on the one that documents it.
//
// ObjectNum is always 0, which the interface already documents as "not tied to a
// specific object": a layout finding is about a paragraph in the source, not
// about an object in the file it became.
type Finding struct {
	// Rule is which guardrail fired.
	Rule Rule
	// Severity is what it did, after the caller's policy was applied.
	Severity Severity
	// Message says what happened, in terms of the author's input.
	Message string
	// Source is where in the input it happened.
	Source Source

	// Path is the DOM path of the element concerned, such as
	// "html > body > div > p", or empty. It is what makes a finding actionable
	// when the offset points at a stylesheet shared by many elements.
	Path string
	// Selector is the selector responsible, or empty.
	Selector string
	// Property is the declaration responsible, or empty.
	Property string
}

// Error renders the finding for a person, leading with the rule so that a list
// of them can be read down.
func (f Finding) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", f.Rule, f.Message)
	if f.Path != "" {
		fmt.Fprintf(&b, " (at %s)", f.Path)
	}
	switch {
	case f.Source.HTMLOffset >= 0:
		fmt.Fprintf(&b, " [html byte %d]", f.Source.HTMLOffset)
	case f.Source.CSSOffset >= 0:
		if f.Source.Sheet != "" {
			fmt.Fprintf(&b, " [%s byte %d]", f.Source.Sheet, f.Source.CSSOffset)
		} else {
			fmt.Fprintf(&b, " [css byte %d]", f.Source.CSSOffset)
		}
	}
	return b.String()
}

// RuleID is the identifier of the violated rule, for pdf0.Violation.
func (f Finding) RuleID() string { return string(f.Rule) }

// ObjectNum is 0: a layout finding is not tied to a PDF object.
func (f Finding) ObjectNum() int { return 0 }

// maxFindings bounds one render's report. A document that trips a rule on every
// element would otherwise produce a list nobody can read, and a report nobody
// reads is not a report.
const maxFindings = 500

// Recorder collects findings under a policy.
//
// It applies the policy at the point of recording rather than at the end, so a
// rule set to Ignore costs nothing to raise — which matters because the callers
// are the inner loops of layout, and a guardrail that is expensive to check is
// one that gets checked less often than it should.
type Recorder struct {
	policy Policy

	findings []Finding
	// counts is how many times each rule fired, including the ones dropped for
	// being duplicates or past the bound. It is what lets a report say "and 4000
	// more" rather than implying there were 500.
	counts map[Rule]int
	// seen suppresses repeats of the same rule and message. A stylesheet using
	// one unimplemented property four hundred times is one thing to be told.
	seen map[string]bool
	// failed records that something fired at Error severity.
	failed bool
	// truncated records that the bound was reached.
	truncated bool
}

// NewRecorder prepares to collect findings under a policy. A nil policy uses the
// defaults.
func NewRecorder(p Policy) *Recorder {
	return &Recorder{
		policy: p,
		counts: map[Rule]int{},
		seen:   map[string]bool{},
	}
}

// Report records a finding, applying the policy.
//
// It reports whether the finding was at Error severity, which is what a caller
// in a position to stop early wants to know.
func (r *Recorder) Report(rule Rule, src Source, message string) bool {
	return r.ReportDetail(Finding{Rule: rule, Source: src, Message: message})
}

// ReportDetail records a finding that carries more than a message.
//
// The Severity field of f is ignored — it is filled in from the policy, because
// a caller raising a finding should not be able to decide how serious it is.
// That decision belongs to whoever is rendering.
func (r *Recorder) ReportDetail(f Finding) bool {
	severity := r.policy.severityOf(f.Rule)
	r.counts[f.Rule]++
	if severity == Ignore {
		return false
	}
	if severity == Error {
		r.failed = true
	}
	f.Severity = severity

	// Deduplicate on everything a reader would use to tell two findings apart.
	// Two identical messages about two different elements are two findings; two
	// identical messages about the same place are one.
	key := string(f.Rule) + "\x00" + f.Message + "\x00" + f.Path + "\x00" +
		f.Property + "\x00" + f.Selector
	if r.seen[key] {
		return severity == Error
	}
	r.seen[key] = true

	if len(r.findings) >= maxFindings {
		r.truncated = true
		return severity == Error
	}
	r.findings = append(r.findings, f)
	return severity == Error
}

// Findings returns what was recorded, in a deterministic order.
//
// The order is by rule, then by where in the input the finding came from, then
// by message — the same shape internal/finding.Sort gives every validator,
// because two runs over the same document must produce the same slice and
// several of the stages above range over maps.
func (r *Recorder) Findings() []Finding {
	out := append([]Finding(nil), r.findings...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.Source.HTMLOffset != b.Source.HTMLOffset {
			return a.Source.HTMLOffset < b.Source.HTMLOffset
		}
		if a.Source.CSSOffset != b.Source.CSSOffset {
			return a.Source.CSSOffset < b.Source.CSSOffset
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})
	return out
}

// Failed reports whether anything fired at Error severity.
func (r *Recorder) Failed() bool { return r.failed }

// Truncated reports whether the bound stopped findings being recorded, so a
// caller never presents a cut list as a complete one.
func (r *Recorder) Truncated() bool { return r.truncated }

// Count returns how many times a rule fired, including occurrences that were
// deduplicated or dropped past the bound.
//
// This is what lets a report say "flex-wrap was dropped 412 times" while showing
// the finding once, which is more useful than either the one or the four hundred
// on their own.
func (r *Recorder) Count(rule Rule) int { return r.counts[rule] }
