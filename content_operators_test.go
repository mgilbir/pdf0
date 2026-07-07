package pdf0

import "testing"

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

func mkPageWithContentAndRes(content string, res *Dictionary) *Document {
	doc := NewPDFADocument(PDFA2b)
	page := addTestPage(doc)
	stream := &Stream{Dict: Dictionary{}, Data: []byte(content)}
	stream.Dict.Set("Length", Integer(len(content)))
	doc.Objects[21] = &IndirectObject{Number: 21, Value: stream}
	page.Set("Contents", IndirectRef{Number: 21})
	if res != nil {
		page.Set("Resources", res)
	}
	return doc
}

func TestUndefinedOperatorFlagged(t *testing.T) {
	doc := mkPageWithContentAndRes("q\nBogusOp\nQ", nil)
	if !hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
		t.Error("undefined operator must be flagged")
	}
	// Valid operators pass.
	doc = mkPageWithContentAndRes("q 1 0 0 1 0 0 cm 0 0 10 10 re f Q", nil)
	if hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
		t.Error("valid content flagged")
	}
}

func TestRenderingIntentOperator(t *testing.T) {
	doc := mkPageWithContentAndRes("/Perceptual ri", nil)
	if hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
		t.Error("standard rendering intent must pass")
	}
	doc = mkPageWithContentAndRes("/CustomIntent ri", nil)
	if !hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
		t.Error("custom rendering intent must be flagged")
	}
}

func TestAbsentResourceReference(t *testing.T) {
	// Do referencing an XObject not in resources.
	res := &Dictionary{}
	res.Set("XObject", &Dictionary{}) // empty
	doc := mkPageWithContentAndRes("q /X0 Do Q", res)
	if !hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
		t.Error("absent XObject reference must be flagged")
	}
	// cs referencing an absent colour space.
	doc = mkPageWithContentAndRes("/CS0 cs 0.5 sc 0 0 5 5 re f", &Dictionary{})
	if !hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
		t.Error("absent colour-space reference must be flagged")
	}
	// Built-in device space needs no resource.
	doc = mkPageWithContentAndRes("/DeviceRGB cs 0 0 0 sc", nil)
	if hasRuleMsg(ValidatePDFA(doc, PDFA2b), "6.2.2") {
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
