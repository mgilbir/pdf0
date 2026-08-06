package render

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The guardrail framework.
//
// §6.5 of the rendering proposal asks that every rule have a test which plants a
// violation, watches the finding appear, and then checks the compliant version
// produces none — because "a threshold that has only ever been observed passing
// is decoration".
//
// That is easy to satisfy today and easy to let rot as the catalogue grows, so
// it is enforced rather than remembered: TestMain requires every rule in
// AllRules to have been raised by some test in this package. Adding a rule
// without a test that fires it fails the build, which is the only version of
// §6.5 that survives contact with a year of changes.

// fired records which rules the tests in this package have raised, so the
// coverage check can require all of them. Tests run sequentially unless they
// ask otherwise, and none here does, so a plain map is safe.
var fired = map[Rule]bool{}

// raise is how a test raises a finding, so that the coverage check sees it.
func raise(t *testing.T, r *Recorder, rule Rule, src Source, msg string) bool {
	t.Helper()
	fired[rule] = true
	return r.Report(rule, src, msg)
}

// TestMain runs the §6.5 coverage check after every other test.
//
// It is here rather than in a test of its own because it has to happen last,
// and Go guarantees no ordering between tests — the first attempt at this was
// an ordinary test placed at the top of the file, which duly reported that
// nothing had fired because nothing had run yet.
//
// It is skipped when -run selected a subset, since checking the coverage of
// tests that were not run would fail for the wrong reason.
func TestMain(m *testing.M) {
	code := m.Run()

	if code == 0 && !subsetSelected() {
		var missing []string
		for _, rule := range AllRules() {
			if !fired[rule] {
				missing = append(missing, string(rule))
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr,
				"no test raises %s, so nothing has ever seen those rules fire; "+
					"\u00a76.5 asks for a test that plants a violation of every rule\n",
				strings.Join(missing, ", "))
			code = 1
		}
	}
	os.Exit(code)
}

func subsetSelected() bool {
	f := flag.Lookup("test.run")
	return f != nil && f.Value.String() != ""
}

// TestSeverityDefaults pins what each rule does when a caller says nothing. A
// default of Ignore anywhere would be a rule that exists and never speaks.
func TestSeverityDefaults(t *testing.T) {
	for _, rule := range AllRules() {
		s := Policy(nil).severityOf(rule)
		if s == Ignore {
			t.Errorf("%q defaults to being ignored, so it would never be reported", rule)
		}
	}
	// A rule nothing declared still warns rather than vanishing, so adding one
	// is not a silent change.
	if got := Policy(nil).severityOf(Rule("not-a-rule")); got != Warn {
		t.Errorf("an undeclared rule defaults to %v, want warn", got)
	}
}

// TestPolicyOverrides pins that a caller's choice is honoured, in both
// directions.
func TestPolicyOverrides(t *testing.T) {
	rule := RuleUnsupportedProperty

	// Ignore drops it entirely.
	r := NewRecorder(Policy{rule: Ignore})
	if raise(t, r, rule, NoSource, "dropped") {
		t.Error("an ignored rule reported an error")
	}
	if n := len(r.Findings()); n != 0 {
		t.Errorf("an ignored rule left %d findings", n)
	}
	if r.Failed() {
		t.Error("an ignored rule failed the render")
	}
	// It is still counted, so a caller can ask how often it happened even when
	// it chose not to be told each time.
	if r.Count(rule) != 1 {
		t.Errorf("an ignored rule was counted %d times, want 1", r.Count(rule))
	}

	// Error fails the render.
	r = NewRecorder(Policy{rule: Error})
	if !raise(t, r, rule, NoSource, "fatal") {
		t.Error("a rule set to Error did not report as one")
	}
	if !r.Failed() {
		t.Error("a rule set to Error did not fail the render")
	}
	if got := r.Findings()[0].Severity; got != Error {
		t.Errorf("the finding records severity %v, want error", got)
	}

	// Warn records without failing.
	r = NewRecorder(Policy{rule: Warn})
	if raise(t, r, rule, NoSource, "noted") {
		t.Error("a warning reported as an error")
	}
	if r.Failed() {
		t.Error("a warning failed the render")
	}
	if n := len(r.Findings()); n != 1 {
		t.Errorf("a warning left %d findings, want 1", n)
	}
}

// TestSeverityIsNotTheReportersChoice pins that a caller raising a finding
// cannot decide how serious it is. That decision belongs to whoever is
// rendering, or a rule could quietly promote itself past a policy.
func TestSeverityIsNotTheReportersChoice(t *testing.T) {
	r := NewRecorder(Policy{RuleUnsupportedElement: Warn})
	fired[RuleUnsupportedElement] = true
	r.ReportDetail(Finding{
		Rule:     RuleUnsupportedElement,
		Severity: Error, // ignored
		Message:  "tried to promote itself",
	})
	if r.Failed() {
		t.Error("a finding promoted itself past the policy")
	}
	if got := r.Findings()[0].Severity; got != Warn {
		t.Errorf("the recorded severity is %v, want the policy's warn", got)
	}
}

// TestDuplicatesAreCollapsedButCounted pins the shape a useful report has: one
// entry for a stylesheet that used an unimplemented property four hundred times,
// and a count saying it was four hundred. Either number alone is misleading.
func TestDuplicatesAreCollapsedButCounted(t *testing.T) {
	r := NewRecorder(nil)
	for i := 0; i < 400; i++ {
		raise(t, r, RuleUnsupportedProperty, AtCSS(10), "\"flex-wrap\" is not implemented")
	}
	if n := len(r.Findings()); n != 1 {
		t.Errorf("400 identical findings became %d entries, want 1", n)
	}
	if got := r.Count(RuleUnsupportedProperty); got != 400 {
		t.Errorf("the count is %d, want 400 — a collapsed report must still say how many", got)
	}

	// Two findings that differ in where they happened are two findings, because
	// an author has two places to look.
	r = NewRecorder(nil)
	raise(t, r, RuleUnsupportedProperty, AtCSS(10), "same message")
	r.ReportDetail(Finding{
		Rule: RuleUnsupportedProperty, Source: AtCSS(10),
		Message: "same message", Path: "html > body > p",
	})
	if n := len(r.Findings()); n != 2 {
		t.Errorf("findings at two different elements collapsed to %d, want 2", n)
	}
}

// TestFindingsAreBoundedAndSayS pins that a document tripping a rule on every
// element cannot produce an unreadable list, and that a cut list is never
// presented as a complete one.
func TestFindingsAreBoundedAndSayS(t *testing.T) {
	r := NewRecorder(nil)
	for i := 0; i < maxFindings*3; i++ {
		raise(t, r, RuleInvalidCSS, AtCSS(i), "problem "+itoa(i))
	}
	if n := len(r.Findings()); n > maxFindings {
		t.Errorf("recorded %d findings, past the bound of %d", n, maxFindings)
	}
	if !r.Truncated() {
		t.Error("the list was cut and does not say so")
	}
	if got := r.Count(RuleInvalidCSS); got != maxFindings*3 {
		t.Errorf("the count is %d, want all %d occurrences", got, maxFindings*3)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestFindingsAreDeterministic pins that two runs agree. The stages that feed
// this range over maps, and Go randomises map iteration on every run, so an
// order that happens to be stable in one process is not evidence of anything.
func TestFindingsAreDeterministic(t *testing.T) {
	build := func() []Finding {
		r := NewRecorder(nil)
		raise(t, r, RuleUnsupportedAtRule, AtCSS(50), "@media is not applied yet")
		raise(t, r, RuleInvalidMarkup, AtHTML(10), "tags have to nest")
		raise(t, r, RuleUnsupportedProperty, AtCSS(20), "\"gap\" is not implemented")
		raise(t, r, RuleUnsupportedSelector, AtCSS(5), "\":hover\" cannot apply")
		raise(t, r, RuleUnsupportedValue, AtCSS(30), "\"3ex\" needs font metrics")
		raise(t, r, RuleLimit, NoSource, "matching stopped early")
		raise(t, r, RuleUnsupportedElement, AtHTML(99), "<canvas> is dropped")
		return r.Findings()
	}
	first := build()
	for i := 0; i < 20; i++ {
		again := build()
		if len(first) != len(again) {
			t.Fatalf("run %d produced %d findings, first produced %d", i, len(again), len(first))
		}
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("run %d differs at %d:\n  %v\n  %v", i, j, first[j], again[j])
			}
		}
	}

	// And the order is by rule, so a report can be read down.
	for i := 1; i < len(first); i++ {
		if first[i-1].Rule > first[i].Rule {
			t.Errorf("findings are not ordered by rule: %q then %q",
				first[i-1].Rule, first[i].Rule)
		}
	}
}

// TestFindingSatisfiesViolation pins the three methods pdf0's Violation
// interface asks for. The interface is satisfied structurally and is not
// imported here — internal/finding takes the same approach and says why — so
// nothing but a test checks the shape.
func TestFindingSatisfiesViolation(t *testing.T) {
	// The same shape as pdf0.Violation, declared locally so this package does
	// not import the one that documents it.
	type violation interface {
		error
		RuleID() string
		ObjectNum() int
	}
	var v violation = Finding{Rule: RuleUnsupportedProperty, Message: "x"}

	if v.RuleID() != string(RuleUnsupportedProperty) {
		t.Errorf("RuleID is %q, want the rule identifier", v.RuleID())
	}
	// A layout finding is about a paragraph in the source, not an object in the
	// file it became.
	if v.ObjectNum() != 0 {
		t.Errorf("ObjectNum is %d, want 0", v.ObjectNum())
	}
	if v.Error() == "" {
		t.Error("the finding renders as an empty string")
	}
}

// TestFindingMessageNamesThePlace pins that a finding says where it came from.
// A report that cannot be traced back to a line of the author's input is one
// they have to guess about, which is most of the value gone.
func TestFindingMessageNamesThePlace(t *testing.T) {
	cases := []struct {
		f    Finding
		want []string
	}{
		{
			Finding{Rule: RuleUnsupportedProperty, Message: "\"gap\" is not implemented",
				Source: AtCSS(42)},
			[]string{"unsupported-property", "gap", "css byte 42"},
		},
		{
			Finding{Rule: RuleUnsupportedElement, Message: "<canvas> is dropped",
				Source: AtHTML(17), Path: "html > body > div"},
			[]string{"unsupported-element", "canvas", "html byte 17", "html > body > div"},
		},
		{
			Finding{Rule: RuleUnsupportedAtRule, Message: "@page is not applied yet",
				Source: Source{HTMLOffset: -1, CSSOffset: 3, Sheet: "theme.css"}},
			[]string{"@page", "theme.css byte 3"},
		},
		{
			Finding{Rule: RuleLimit, Message: "matching stopped early", Source: NoSource},
			[]string{"limit", "matching stopped early"},
		},
	}
	for _, tc := range cases {
		got := tc.f.Error()
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%q does not mention %q", got, want)
			}
		}
	}
	// A finding with no place does not invent one.
	plain := Finding{Rule: RuleLimit, Message: "x", Source: NoSource}.Error()
	if strings.Contains(plain, "byte") {
		t.Errorf("%q names a byte offset it does not have", plain)
	}
}

// TestLimitRuleMatchesTheRestOfPdf0 pins the spelling. Every other part of pdf0
// reports "we stopped short" under "limit", and a caller that already
// distinguishes that from "the input is bad" must not have to learn a second
// spelling for it.
func TestLimitRuleMatchesTheRestOfPdf0(t *testing.T) {
	if RuleLimit != "limit" {
		t.Errorf("the limit rule is %q; internal/finding.LimitRule is \"limit\"", RuleLimit)
	}
}

// TestEveryRuleIsRegistered closes the hole the coverage check cannot see.
//
// AllRules is derived from defaultSeverity, so a Rule declared as a constant and
// never put in that map is invisible to it — and therefore invisible to the §6.5
// coverage check too. It would still be reportable, at whatever severity the
// fallback gives, and nothing would ever ask whether a test fired it.
//
// That is not hypothetical: two rules were added in exactly that state, the
// coverage check passed because it could not see them, and this test is what
// stops it happening again. It reads the source because there is no other way to
// enumerate a Go constant.
func TestEveryRuleIsRegistered(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "finding.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing finding.go: %v", err)
	}

	registered := map[Rule]bool{}
	for _, r := range AllRules() {
		registered[r] = true
	}

	var declared int
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Rule" {
			return true
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			declared++
			if !registered[Rule(value)] {
				t.Errorf("the rule %s (%q) is declared and is not in defaultSeverity, "+
					"so AllRules cannot see it and the coverage check never asks "+
					"whether anything fires it", name.Name, value)
			}
		}
		return true
	})

	if declared == 0 {
		t.Fatal("no rule constants were found; this test is checking nothing")
	}
	if declared != len(registered) {
		t.Errorf("%d rules are declared and %d registered", declared, len(registered))
	}
}
