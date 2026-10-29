package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

func TestContentOperatorWhitelist(t *testing.T) {
	// Every canonical Annex A operator must be recognised.
	for _, op := range []string{"q", "Q", "cm", "re", "f", "BT", "ET", "Tj", "TJ",
		"'", "\"", "Do", "sh", "gs", "BDC", "EMC", "BX", "EX", "d0", "d1", "scn", "SCN", "ri"} {
		if !contentOperators[op] {
			t.Errorf("operator %q should be recognised", op)
		}
	}
	if contentOperators["UnknownOperator"] {
		t.Error("unknown operator must not be recognised")
	}
}

func TestIsContentOperand(t *testing.T) {
	for _, s := range []string{"0", "42", "-1.5", "+3", ".5", "true", "false", "null"} {
		if !isContentOperand(s) {
			t.Errorf("%q should be an operand", s)
		}
	}
	for _, s := range []string{"q", "Tj", "re", "Do"} {
		if isContentOperand(s) {
			t.Errorf("%q should not be an operand", s)
		}
	}
}

func mkPageWithContentAndRes(content string, res *object.Dictionary) core.View {
	doc := mkPDFAView(PDFA2b)
	page := addTestPage(doc)
	stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte(content)}
	stream.Dict.Set("Length", object.Integer(len(content)))
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: stream}
	page.Set("Contents", object.IndirectRef{Number: 21})
	if res != nil {
		page.Set("Resources", res)
	}
	return doc
}

func TestUndefinedOperatorFlagged(t *testing.T) {
	doc := mkPageWithContentAndRes("q\nBogusOp\nQ", nil)
	if !hasRuleMsg(validateView(doc, PDFA2b), "6.2.2") {
		t.Error("undefined operator must be flagged")
	}
	// Valid operators pass.
	doc = mkPageWithContentAndRes("q 1 0 0 1 0 0 cm 0 0 10 10 re f Q", nil)
	if hasRuleMsg(validateView(doc, PDFA2b), "6.2.2") {
		t.Error("valid content flagged")
	}
}

// TestATilingPatternsFindingNamesThePattern: an undefined operator inside a
// tiling pattern the page paints with is reported against the pattern's own
// object, not the pattern's position in /Pattern (audit 2026-09-22 C143).
func TestATilingPatternsFindingNamesThePattern(t *testing.T) {
	pattern := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Pattern")},
		object.Entry{Key: "PatternType", Value: object.Integer(1)},
		object.Entry{Key: "PaintType", Value: object.Integer(1)},
		object.Entry{Key: "TilingType", Value: object.Integer(1)},
		object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
		object.Entry{Key: "XStep", Value: object.Integer(10)},
		object.Entry{Key: "YStep", Value: object.Integer(10)},
		object.Entry{Key: "Resources", Value: &object.Dictionary{}},
	), []byte("0 0 5 5 re BogusOp f"))
	res := object.NewDictionary(object.Entry{Key: "Pattern", Value: object.NewDictionary(
		object.Entry{Key: "P0", Value: object.IndirectRef{Number: 44}},
		object.Entry{Key: "P1", Value: object.IndirectRef{Number: 44}},
	)})
	doc := mkPageWithContentAndRes("/Pattern cs /P1 scn 0 0 10 10 re f", res)
	doc.Objects[44] = &object.IndirectObject{Number: 44, Value: pattern}
	var got []Violation
	for _, v := range validateView(doc, PDFA2b) {
		if v.Rule == "6.2.2" {
			got = append(got, v)
		}
	}
	if len(got) != 1 || got[0].Object != 44 {
		t.Errorf("the pattern's undefined operator: want one 6.2.2 finding at object 44, got %v", got)
	}
}

func TestRenderingIntentOperator(t *testing.T) {
	// 6.2.6 is PDF/A-2's rendering-intent clause (veraPDF 6.2.6-1).
	doc := mkPageWithContentAndRes("/Perceptual ri", nil)
	if hasRuleMsg(validateView(doc, PDFA2b), "6.2.6") {
		t.Error("standard rendering intent must pass")
	}
	doc = mkPageWithContentAndRes("/CustomIntent ri", nil)
	if !hasRuleMsg(validateView(doc, PDFA2b), "6.2.6") {
		t.Error("custom rendering intent must be flagged under 6.2.6")
	}
}

// TestAbsentResourceReference: a named resource found nowhere is PDF/A-4's
// rule alone (veraPDF 6.2.2-3, its undefined resource names); PDF/A-2 and -3 have
// no such rule, and veraPDF passes the file. A built-in device colour space
// needs no resource at all.
func TestAbsentResourceReference(t *testing.T) {
	absent := func(errs []Violation) bool {
		return hasMessage(errs, "absent from the resource dictionary")
	}
	res := &object.Dictionary{}
	res.Set("XObject", &object.Dictionary{}) // empty
	for _, tc := range []struct {
		name, content string
		res           *object.Dictionary
	}{
		{"an XObject", "q /X0 Do Q", res},
		{"a colour space", "/CS0 cs 0.5 sc 0 0 5 5 re f", &object.Dictionary{}},
	} {
		if errs := validateView(mkPageWithContentAndRes(tc.content, tc.res), PDFA4); !absent(errs) || !hasRuleMsg(errs, "6.2.2") {
			t.Errorf("%s defined nowhere: want a 6.2.2 finding at PDF/A-4, got %v", tc.name, errs)
		}
		if errs := validateView(mkPageWithContentAndRes(tc.content, tc.res), PDFA2b); absent(errs) {
			t.Errorf("%s defined nowhere: PDF/A-2 has no rule for it, got %v", tc.name, errs)
		}
	}
	doc := mkPageWithContentAndRes("/DeviceRGB cs 0 0 0 sc", nil)
	if hasRuleMsg(validateView(doc, PDFA4), "6.2.2") {
		t.Error("built-in device colour space must not be flagged")
	}
}

// TestInheritedResourceReference: a form XObject with no resources of its own
// that uses a colour space the page it is drawn on defines inherits it, which
// PDF/A-2 and -4 forbid (veraPDF 6.2.2-2, PDF_A-2b 6-2-2-t04-fail-e) and
// PDF/A-1 does not. A name neither defines is undefined, not inherited.
func TestInheritedResourceReference(t *testing.T) {
	build := func(pageCS bool) core.View {
		form := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
		), []byte("/CS0 cs 0.5 sc 0 0 5 5 re f"))
		res := object.NewDictionary(object.Entry{Key: "XObject", Value: object.NewDictionary(object.Entry{Key: "X0", Value: object.IndirectRef{Number: 44}})})
		if pageCS {
			res.Set("ColorSpace", object.NewDictionary(object.Entry{Key: "CS0", Value: object.Name("DeviceGray")}))
		}
		doc := mkPageWithContentAndRes("/X0 Do", res)
		doc.Objects[44] = &object.IndirectObject{Number: 44, Value: form}
		return doc
	}
	inherited := func(errs []Violation) bool { return hasMessage(errs, "inherited from the content it is drawn from") }
	for _, level := range []Level{PDFA2b, PDFA4} {
		if errs := validateView(build(true), level); !inherited(errs) || !hasRuleMsg(errs, "6.2.2") {
			t.Errorf("%s: a form using the page's colour space: want a 6.2.2 inherited finding, got %v", level, errs)
		}
		if errs := validateView(build(false), level); inherited(errs) {
			t.Errorf("%s: a colour space defined nowhere is not inherited, got %v", level, errs)
		}
	}
	if errs := validateView(build(true), PDFA1b); inherited(errs) {
		t.Errorf("PDF/A-1 has no inheritance rule, got %v", errs)
	}
}

func TestInlineImageIntent(t *testing.T) {
	if got := inlineImageEntries(core.Canceler{}, []byte("BI /W 1 /Intent /Perceptual ID xx EI")); len(got) != 1 || got[0]["Intent"] != "Perceptual" {
		t.Errorf("intent not extracted: %v", got)
	}
	if got := inlineImageEntries(core.Canceler{}, []byte("BI /W 1 /Intent /Custom ID xx EI")); len(got) != 1 || got[0]["Intent"] != "Custom" {
		t.Errorf("custom intent not extracted: %v", got)
	}
}

// TestAnInlineIntentIsReportedOnce: one non-standard inline /Intent is one
// finding, from the whole pipeline with the byte checks on (audit 2026-09-22
// C142 — two rules read it and both reported).
func TestAnInlineIntentIsReportedOnce(t *testing.T) {
	doc := mkPageWithContentAndRes("q BI /W 1 /H 1 /BPC 8 /CS /G /Intent /Foo ID \x00 EI Q", nil)
	var got []Violation
	for _, v := range validateView(doc, PDFA2b) {
		if strings.Contains(v.Message, "rendering intent") {
			got = append(got, v)
		}
	}
	if len(got) != 1 {
		t.Errorf("one inline /Intent /Foo: want one finding, got %v", got)
	}
}

// TestTheInlineDictionaryIsParsedAsADictionary: a nested dictionary, an
// array holding a string with a slash in it, and data after ID that looks
// like a key do not leak into the parameters.
func TestTheInlineDictionaryIsParsedAsADictionary(t *testing.T) {
	d := inlineImageEntries(core.Canceler{}, []byte("BI /DP << /Intent /Bogus /K -1 >> /CS [/Indexed /DeviceRGB 1 (/Intent) ] /Intent /Perceptual /W 1 ID\n/Intent /Nope EI"))
	if len(d) != 1 {
		t.Fatalf("want one inline image, got %v", d)
	}
	if d[0]["Intent"] != "Perceptual" || d[0]["W"] != "1" || d[0]["K"] != "" || d[0]["CS"] != "[array]" {
		t.Errorf("dictionary misread: %v", d[0])
	}
}

// TestNoIntentOrPostScriptRuleAtPDFA4: the PDF/A-4 profile has no rule for a
// non-standard rendering intent or a PostScript XObject, and veraPDF passes a
// PDF/A-4 file with either; PDF/A-2 has both (6.2.6-1, 6.2.9-3).
func TestNoIntentOrPostScriptRuleAtPDFA4(t *testing.T) {
	ps := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("PS")},
	), []byte("%!PS"))
	gs := object.NewDictionary(object.Entry{Key: "RI", Value: object.Name("Custom")})
	res := object.NewDictionary(
		object.Entry{Key: "XObject", Value: object.NewDictionary(object.Entry{Key: "P0", Value: object.IndirectRef{Number: 44}})},
		object.Entry{Key: "ExtGState", Value: object.NewDictionary(object.Entry{Key: "G0", Value: gs})},
	)
	build := func() core.View {
		doc := mkPageWithContentAndRes("/Custom ri /G0 gs /P0 Do", res)
		doc.Objects[44] = &object.IndirectObject{Number: 44, Value: ps}
		return doc
	}
	found := func(errs []Violation) (intent, postScript int) {
		for _, v := range errs {
			if strings.Contains(v.Message, "rendering intent") {
				intent++
			}
			if strings.Contains(v.Message, "PostScript") {
				postScript++
			}
		}
		return
	}
	if i, p := found(validateView(build(), PDFA4)); i != 0 || p != 0 {
		t.Errorf("PDF/A-4: %d rendering-intent and %d PostScript findings, want none", i, p)
	}
	if i, p := found(validateView(build(), PDFA2b)); i != 2 || p != 1 {
		t.Errorf("PDF/A-2b: %d rendering-intent and %d PostScript findings, want 2 (ri, /RI) and 1", i, p)
	}
}
