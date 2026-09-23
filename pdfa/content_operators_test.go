package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
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
	if !hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
		t.Error("undefined operator must be flagged")
	}
	// Valid operators pass.
	doc = mkPageWithContentAndRes("q 1 0 0 1 0 0 cm 0 0 10 10 re f Q", nil)
	if hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
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
	for _, v := range ValidateView(doc, PDFA2b, nil) {
		if v.Rule == "6.2.2" {
			got = append(got, v)
		}
	}
	if len(got) != 1 || got[0].Object != 44 {
		t.Errorf("the pattern's undefined operator: want one 6.2.2 finding at object 44, got %v", got)
	}
}

func TestRenderingIntentOperator(t *testing.T) {
	doc := mkPageWithContentAndRes("/Perceptual ri", nil)
	if hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
		t.Error("standard rendering intent must pass")
	}
	doc = mkPageWithContentAndRes("/CustomIntent ri", nil)
	if !hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
		t.Error("custom rendering intent must be flagged")
	}
}

func TestAbsentResourceReference(t *testing.T) {
	// Do referencing an XObject not in resources.
	res := &object.Dictionary{}
	res.Set("XObject", &object.Dictionary{}) // empty
	doc := mkPageWithContentAndRes("q /X0 Do Q", res)
	if !hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
		t.Error("absent XObject reference must be flagged")
	}
	// cs referencing an absent colour space.
	doc = mkPageWithContentAndRes("/CS0 cs 0.5 sc 0 0 5 5 re f", &object.Dictionary{})
	if !hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
		t.Error("absent colour-space reference must be flagged")
	}
	// Built-in device space needs no resource.
	doc = mkPageWithContentAndRes("/DeviceRGB cs 0 0 0 sc", nil)
	if hasRuleMsg(ValidateView(doc, PDFA2b, nil), "6.2.2") {
		t.Error("built-in device colour space must not be flagged")
	}
}

func TestInlineImageIntent(t *testing.T) {
	if got := inlineImageIntents([]byte("BI /W 1 /Intent /Perceptual ID xx EI")); len(got) != 1 || got[0] != "Perceptual" {
		t.Errorf("intent not extracted: %v", got)
	}
	if got := inlineImageIntents([]byte("BI /W 1 /Intent /Custom ID xx EI")); len(got) != 1 || got[0] != "Custom" {
		t.Errorf("custom intent not extracted: %v", got)
	}
}
