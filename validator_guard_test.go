package pdf0

import (
	"reflect"
	"strings"
	"testing"
)

// corruptCatalogDoc returns a document whose catalog is an internally
// inconsistent Dictionary: it declares keys for which no value slot exists.
// Dictionary.Get indexes Values by the matching key's slot, so any lookup of a
// declared key beyond the last value panics with an index-out-of-range. The
// parser cannot produce such a dictionary, but Document/Dictionary are public
// types a caller builds by hand (the builders in this package do), so this is a
// value the exported validators can be handed — and it stands in for any bug or
// hostile structure that makes a check panic mid-run.
func corruptCatalogDoc() *Document {
	cat := &Dictionary{
		// Only /Type has a value; every later key indexes past the end.
		Keys: []Name{
			"Type", "MarkInfo", "StructTreeRoot", "Lang", "ViewerPreferences",
			"Metadata", "Pages", "OutputIntents", "AF", "DPartRoot", "AA",
			"Names", "OCProperties", "Perms", "AcroForm", "PageLayout", "PageMode",
			"Version",
		},
		Values: []Object{Name("Catalog")},
	}
	doc := &Document{
		Version: "2.0",
		Objects: map[int]*IndirectObject{1: {Number: 1, Value: cat}},
		Trailer: Dictionary{},
	}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})
	return doc
}

// TestValidatorPanicContainment is the C27 guard: every non-PDF/A validator
// runs its checks under a recover boundary, so a panicking check is reported as
// an "internal" finding instead of crashing the caller — the containment
// ValidatePDFABytes has had since runCheck was written. (A stack overflow from
// unbounded recursion is still fatal and is not covered here; that class is
// prevented at its source.)
func TestValidatorPanicContainment(t *testing.T) {
	cases := []struct {
		name string
		run  func(*Document) []string // rule/clause identifiers of the findings
	}{
		// The PDF/A validator has had this containment since runCheck; Level A
		// (whose own three families were the last gap) is included as its guard.
		{"ValidatePDFA-levelA", func(d *Document) []string { return ruleIDs(ValidatePDFA(d, PDFA2a)) }},
		{"ValidatePDFUA", func(d *Document) []string { return ruleIDs(ValidatePDFUA(d)) }},
		{"ValidatePDFUA2", func(d *Document) []string { return ruleIDs(ValidatePDFUA2(d)) }},
		{"ValidatePDFX", func(d *Document) []string { return ruleIDs(ValidatePDFX(d, PDFX4)) }},
		{"ValidatePDFVT", func(d *Document) []string { return ruleIDs(ValidatePDFVT(d)) }},
		{"ValidatePDFVT2", func(d *Document) []string { return ruleIDs(ValidatePDFVT2(d)) }},
		{"ValidatePDFR", func(d *Document) []string { return ruleIDs(ValidatePDFR(d)) }},
		{"ValidateDParts", func(d *Document) []string { return ruleIDs(ValidateDParts(d)) }},
		{"ValidateFacturX", func(d *Document) []string {
			var out []string
			for _, v := range ValidateFacturX(d, nil).Violations {
				out = append(out, v.Rule)
			}
			return out
		}},
		{"ValidateOrderX", func(d *Document) []string {
			var out []string
			for _, v := range ValidateOrderX(d, nil).Violations {
				out = append(out, v.Rule)
			}
			return out
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A panic escaping the validator fails the test by crashing it.
			rules := c.run(corruptCatalogDoc())
			found := false
			for _, r := range rules {
				// The identifier is bare in every validator, including the two
				// that namespace the PDF/A-3 findings they adopt: "internal" is
				// a reserved checker identifier, not a rule in either namespace,
				// so adoptPDFAFindings passes it through unprefixed.
				if r == internalRule {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q finding reported; the panic was swallowed rather than contained (got %v)", internalRule, rules)
			}
		})
	}
}

// TestGuardHelpersReportPanics pins the two boundary helpers themselves: the
// panic value reaches the finding, and findings already reported before the
// panic survive it.
func TestGuardHelpersReportPanics(t *testing.T) {
	var out []PDFXViolation
	add := func(rule, msg string, obj int) {
		out = append(out, PDFXViolation{Rule: rule, Message: msg, Object: obj})
	}
	runGuardedCheck(add, func() {
		add("version", "reported before the panic", 3)
		panic("boom")
	})
	if len(out) != 2 || out[0].Rule != "version" || out[1].Rule != internalRule {
		t.Fatalf("runGuardedCheck: got %v, want the pre-panic finding plus an %q one", out, internalRule)
	}
	if !strings.Contains(out[1].Message, "boom") {
		t.Errorf("runGuardedCheck: message %q does not carry the panic value", out[1].Message)
	}

	ua := runUACheck(func() []UAViolation { panic("bang") })
	if len(ua) != 1 || ua[0].Clause != internalRule {
		t.Fatalf("runUACheck: got %v, want one %q finding", ua, internalRule)
	}
	if !strings.Contains(ua[0].Message, "bang") {
		t.Errorf("runUACheck: message %q does not carry the panic value", ua[0].Message)
	}
}

// TestAdoptPDFAFindingsKeepsReservedRulesBare pins the exception in
// adoptPDFAFindings. ValidateFacturX and ValidateOrderX namespace the PDF/A-3
// findings they adopt so that container rules cannot collide with invoice
// rules, but "limit" and "internal" belong to neither namespace: they say the
// checker stopped or crashed, and a caller watching for them keys on the bare
// name. Prefixing one produced "pdfa-3/limit", an identifier nothing documents
// and no predicate recognises — which hides exactly the event these identifiers
// exist to make visible.
func TestAdoptPDFAFindingsKeepsReservedRulesBare(t *testing.T) {
	var out []PDFXViolation
	add := func(rule, msg string, obj int) {
		out = append(out, PDFXViolation{Rule: rule, Message: msg, Object: obj})
	}
	adoptPDFAFindings(add, "pdfa-3/", []ValidationError{
		{Rule: "6.1.2", Message: "a real PDF/A rule"},
		{Rule: limitRule, Message: "a guard tripped"},
		{Rule: internalRule, Message: "a check panicked"},
	})

	want := []string{"pdfa-3/6.1.2", limitRule, internalRule}
	if len(out) != len(want) {
		t.Fatalf("got %d findings, want %d: %v", len(out), len(want), out)
	}
	for i, w := range want {
		if out[i].Rule != w {
			t.Errorf("finding %d: rule %q, want %q", i, out[i].Rule, w)
		}
	}
	// The point of keeping them bare is that the exported predicate still
	// recognises them after adoption.
	for _, v := range out[1:] {
		if !IsCheckerFinding(ValidationError{Rule: v.Rule, Message: v.Message}) {
			t.Errorf("adopted %q is no longer recognised as a checker finding", v.Rule)
		}
	}
}

// TestValidatorOutputDeterministic is the other half of C27: the validators
// walk map-ordered doc.Objects, so their findings must be sorted before they
// are returned or the same document reports them in a different order on every
// run. Each validator is run repeatedly on a document that violates several of
// its rules across several objects.
func TestValidatorOutputDeterministic(t *testing.T) {
	cases := []struct {
		name string
		run  func(*Document) []string
	}{
		{"ValidatePDFUA", func(d *Document) []string { return findingStrings(ValidatePDFUA(d)) }},
		{"ValidatePDFUA2", func(d *Document) []string { return findingStrings(ValidatePDFUA2(d)) }},
		{"ValidatePDFX", func(d *Document) []string { return findingStrings(ValidatePDFX(d, PDFX4)) }},
		{"ValidatePDFVT", func(d *Document) []string { return findingStrings(ValidatePDFVT(d)) }},
		{"ValidatePDFR", func(d *Document) []string { return findingStrings(ValidatePDFR(d)) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.run(violatingDoc())
			if len(want) < 2 {
				t.Fatalf("the fixture must produce several findings to pin an order; got %d", len(want))
			}
			for i := 0; i < 20; i++ {
				// A freshly built document each time: Go randomises the
				// iteration order of every map, so rebuilding is what exposes
				// an unsorted result.
				if got := c.run(violatingDoc()); !reflect.DeepEqual(got, want) {
					t.Fatalf("run %d returned a different order:\n got %v\nwant %v", i, got, want)
				}
			}
		})
	}
}

func ruleIDs[T Violation](v []T) []string {
	out := make([]string, 0, len(v))
	for _, e := range v {
		out = append(out, e.RuleID())
	}
	return out
}

func findingStrings[T Violation](v []T) []string {
	out := make([]string, 0, len(v))
	for _, e := range v {
		out = append(out, e.Error())
	}
	return out
}

// violatingDoc builds a small document that trips several rules in each
// validator, spread over several objects, so the order of the findings depends
// on the object-map iteration order. Every call allocates fresh maps, whose
// iteration order Go randomises independently.
func violatingDoc() *Document {
	doc := &Document{
		Version: "1.7", // wrong version for PDF/R and for PDF/X-4 alike
		Objects: map[int]*IndirectObject{},
		Trailer: Dictionary{},
	}
	num := 0
	add := func(v Object) IndirectRef {
		num++
		doc.Objects[num] = &IndirectObject{Number: num, Value: v}
		return IndirectRef{Number: num}
	}

	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pagesRef := add(pages)

	var kids Array
	for i := 0; i < 3; i++ {
		// Each page draws text and a vector path (non-raster operators, PDF/R),
		// carries no /TrimBox or /BleedBox (PDF/X) and uses an unembedded font
		// (PDF/UA and PDF/X), each in its own object.
		contentRef := add(&Stream{Dict: Dictionary{}, Data: []byte("BT /F1 12 Tf (hi) Tj ET 0 0 10 10 re f")})

		font := &Dictionary{}
		font.Set("Type", Name("Font"))
		font.Set("Subtype", Name("TrueType"))
		font.Set("BaseFont", Name("Helvetica"))
		fontRef := add(font)

		fonts := &Dictionary{}
		fonts.Set("F1", fontRef)
		res := &Dictionary{}
		res.Set("Font", fonts)

		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", pagesRef)
		page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(200), Integer(200)})
		page.Set("Contents", contentRef)
		page.Set("Resources", res)
		kids = append(kids, add(page))
	}
	// Objects that the whole-object-list scans report on: PDF/X walks
	// doc.Objects for forbidden features and PDF/UA for forbidden annotation
	// subtypes, so these are the findings whose order the map decides.
	for i := 0; i < 3; i++ {
		movie := &Dictionary{}
		movie.Set("Type", Name("Annot"))
		movie.Set("Subtype", Name("Movie"))
		add(movie)

		trapNet := &Dictionary{}
		trapNet.Set("Type", Name("Annot"))
		trapNet.Set("Subtype", Name("TrapNet"))
		add(trapNet)

		js := &Dictionary{}
		js.Set("S", Name("JavaScript"))
		add(js)
	}

	pages.Set("Kids", kids)
	pages.Set("Count", Integer(len(kids)))

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", add(cat))
	return doc
}
