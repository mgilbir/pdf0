package pdfa

import (
	"strings"
	"testing"
)

// PDF/A-4e and PDF/A-4f as levels.
//
// Everything else about the variants can be read out of the file: a document
// that declares itself a 4f gets 4f's relaxations and 4f's requirements. One
// thing cannot. A part-4 file carrying no pdfaid:conformance is a valid plain
// PDF/A-4 file — base rule 6.7.3-3 says a file conforming to neither variant
// shall not provide one — so "this should have declared E" is a question only a
// caller who asked for PDF/A-4e can pose.

func TestTheVariantLevelsKnowWhatTheyAre(t *testing.T) {
	for _, tc := range []struct {
		level Level
		name  string
		conf  string
	}{
		{PDFA4E, "PDF/A-4e", "E"},
		{PDFA4F, "PDF/A-4f", "F"},
	} {
		if got := tc.level.String(); got != tc.name {
			t.Errorf("String() = %q, want %q", got, tc.name)
		}
		if got := tc.level.variantConformance(); got != tc.conf {
			t.Errorf("%s wants conformance %q, got %q", tc.name, tc.conf, got)
		}
		if !tc.level.Is4Variant() {
			t.Errorf("%s does not report itself a PDF/A-4 variant", tc.name)
		}
		// Every base PDF/A-4 requirement applies to a variant, which is what
		// lets the rules written against the base part answer for one.
		if got := tc.level.BaseB(); got != PDFA4 {
			t.Errorf("%s.BaseB() = %v, want PDF/A-4", tc.name, got)
		}
		if tc.level.IsA() {
			t.Errorf("%s reports itself a Level A", tc.name)
		}
	}
	// And the levels that are not variants say so, including plain PDF/A-4.
	for _, l := range []Level{PDFA1b, PDFA2b, PDFA3b, PDFA4, PDFA1a, PDFA2a, PDFA3a} {
		if l.Is4Variant() || l.variantConformance() != "" {
			t.Errorf("%s reports itself a PDF/A-4 variant", l)
		}
	}
}

// TestLevelForNamesTheVariants, since the level a document claims is how a
// caller who wants "validate this as whatever it says it is" gets there.
func TestLevelForNamesTheVariants(t *testing.T) {
	for _, tc := range []struct {
		part, conf string
		want       Level
		ok         bool
	}{
		{"4", "E", PDFA4E, true},
		{"4", "e", PDFA4E, true}, // the lookup is case-insensitive; the *rule* is not
		{"4", "F", PDFA4F, true},
		{"4", "", PDFA4, true},
		{"4", "B", 0, false}, // no such thing
		{"1", "A", PDFA1a, true},
		{"2", "", PDFA2b, true},
	} {
		got, ok := LevelFor(tc.part, tc.conf)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("LevelFor(%q, %q) = (%v, %v), want (%v, %v)",
				tc.part, tc.conf, got, ok, tc.want, tc.ok)
		}
	}
}

// TestAVariantMustSayWhichVariantItIs is the rule the levels exist for, in all
// four shapes the corpus distinguishes.
func TestAVariantMustSayWhichVariantItIs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		xmp   string
		level Level
		want  string // "" means no finding
	}{
		{"a 4e that says E", `<pdfaid:conformance>E</pdfaid:conformance>`, PDFA4E, ""},
		{"a 4f that says F", `<pdfaid:conformance>F</pdfaid:conformance>`, PDFA4F, ""},
		{"a 4e that says nothing", ``, PDFA4E, "declares no pdfaid:conformance"},
		{"a 4f that says nothing", ``, PDFA4F, "declares no pdfaid:conformance"},
		{"a 4e that says F", `<pdfaid:conformance>F</pdfaid:conformance>`, PDFA4E, `is "F"`},
		// Case matters: the property is case-sensitive, and "e" is not "E".
		{"a 4e that says e", `<pdfaid:conformance>e</pdfaid:conformance>`, PDFA4E, `is "e"`},
		{"a 4e with an empty value", `<pdfaid:conformance></pdfaid:conformance>`, PDFA4E, "present but empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := xmpView(`<?xpacket begin=""?><x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
<pdfaid:part>4</pdfaid:part><pdfaid:rev>2020</pdfaid:rev>` + tc.xmp + `
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="r"?>`)

			errs := checkVariant4Conformance(doc, tc.level)
			if tc.want == "" {
				if len(errs) != 0 {
					t.Errorf("a conforming declaration was reported: %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("no finding; want one mentioning %q", tc.want)
			}
			if !strings.Contains(errs[0].Message, tc.want) {
				t.Errorf("message is %q, want it to mention %q", errs[0].Message, tc.want)
			}
			if errs[0].Level != tc.level {
				t.Errorf("finding is at level %v, want %v", errs[0].Level, tc.level)
			}
		})
	}

	// At plain PDF/A-4 the question does not arise: absent is correct there,
	// and reporting it would refuse a conforming document.
	doc := xmpView(`<?xpacket begin=""?><x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
<pdfaid:part>4</pdfaid:part><pdfaid:rev>2020</pdfaid:rev>
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="r"?>`)
	for _, l := range []Level{PDFA4, PDFA2b, PDFA1b} {
		if errs := checkVariant4Conformance(doc, l); len(errs) != 0 {
			t.Errorf("%s asked for a variant conformance: %v", l, errs)
		}
	}
}

// TestTheVariantRequirementsApplyFromTheLevelAsWellAsTheFile.
//
// Before the levels, 4f's and 4e's own requirements were gated on what the
// document declared, which cannot reach a document that declares nothing. Both
// routes work now, and the level is the one that does not depend on the file
// being honest about itself.
func TestTheVariantRequirementsApplyFromTheLevelAsWellAsTheFile(t *testing.T) {
	// No conformance declared at all.
	bare := efDoc("", nil, nil)

	if v := checkA4FEmbeddedFilesPresent(bare, PDFA4F); len(v) == 0 {
		t.Error("validating at PDF/A-4f did not apply the attachment requirement " +
			"to a document that declares nothing")
	}
	if v := checkA4FEmbeddedFilesPresent(bare, PDFA4); len(v) != 0 {
		t.Errorf("plain PDF/A-4 applied 4f's requirement to a document that never "+
			"claimed it: %v", v)
	}
	// And the file-driven route still works, which is how a document that says
	// it is a 4f is held to 4f without the caller knowing.
	if v := checkA4FEmbeddedFilesPresent(efDoc("F", nil, nil), PDFA4); len(v) == 0 {
		t.Error("a document declaring F was not held to the attachment requirement")
	}
}
