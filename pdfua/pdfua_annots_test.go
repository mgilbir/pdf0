package pdfua

import (
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

func hasUAClause(v []Violation, clause string) bool {
	for _, e := range v {
		if e.Clause == clause {
			return true
		}
	}
	return false
}

// TestUASecurity flags encryption that disables accessibility extraction.
func TestUASecurity(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	enc := &object.Dictionary{}
	doc.Objects[9] = &object.IndirectObject{Number: 9, Value: enc}
	doc.Trailer.Set("Encrypt", object.IndirectRef{Number: 9})

	// No /P entry → violation.
	if !hasUAClause(checkUASecurity(doc), "7.16") {
		t.Error("missing /P not flagged")
	}
	// /P with bit 10 clear → violation.
	enc.Set("P", object.Integer(-44)) // bit 10 (0x200) clear in this value's low bits
	if p := int32(-44); uint32(p)&0x200 == 0 {
		if !hasUAClause(checkUASecurity(doc), "7.16") {
			t.Error("accessibility-disabled /P not flagged")
		}
	}
	// /P with bit 10 set → clean.
	enc.Set("P", object.Integer(int32(-1))) // all bits set
	if hasUAClause(checkUASecurity(doc), "7.16") {
		t.Error("permissive /P should not be flagged")
	}
}

// TestUATrapNet flags a TrapNet annotation.
func TestUATrapNet(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	a := &object.Dictionary{}
	a.Set("Type", object.Name("Annot"))
	a.Set("Subtype", object.Name("TrapNet"))
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}
	if !hasUAClause(checkUAAnnotations(doc), "7.18.2") {
		t.Error("TrapNet annotation not flagged")
	}
	// A hidden TrapNet is exempt.
	a.Set("F", object.Integer(2))
	if hasUAClause(checkUAAnnotations(doc), "7.18.2") {
		t.Error("hidden annotation should be exempt")
	}
}

// TestUAAnnotationTagged flags a visible, untagged annotation and accepts a
// tagged one.
func TestUAAnnotationTagged(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	a := &object.Dictionary{}
	a.Set("Type", object.Name("Annot"))
	a.Set("Subtype", object.Name("Text"))
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}
	if !hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("untagged annotation not flagged")
	}
	a.Set("StructParent", object.Integer(0))
	a.Set("Contents", object.String{Value: []byte("a note")}) // 7.18.1 also needs a description
	if hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("tagged annotation with a description should be clean")
	}
	// Hidden annotations are exempt even without /StructParent.
	a.Delete("StructParent")
	a.Set("F", object.Integer(2))
	if hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("hidden annotation should be exempt from tagging")
	}
}

// TestUALinkAltText flags a Link annotation without /Contents.
func TestUALinkAltText(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	a := &object.Dictionary{}
	a.Set("Type", object.Name("Annot"))
	a.Set("Subtype", object.Name("Link"))
	a.Set("StructParent", object.Integer(0)) // tagged, so only the alt-text rule applies
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}
	if !hasUAClause(checkUAAnnotations(doc), "7.18.5") {
		t.Error("Link without /Contents not flagged")
	}
	a.Set("Contents", object.String{Value: []byte("go to the next section")})
	if hasUAClause(checkUAAnnotations(doc), "7.18.5") {
		t.Error("Link with /Contents should be clean")
	}
}

// TestRunCheckReportsPanics pins the PDF/UA check boundary: a check that panics
// yields one internal finding carrying the panic value, rather than taking the
// run down or vanishing.
func TestRunCheckReportsPanics(t *testing.T) {
	ua := RunCheck(func() []Violation { panic("bang") })
	if len(ua) != 1 || ua[0].Clause != finding.InternalRule {
		t.Fatalf("RunCheck: got %v, want one %q finding", ua, finding.InternalRule)
	}
	if !strings.Contains(ua[0].Message, "bang") {
		t.Errorf("RunCheck: message %q does not carry the panic value", ua[0].Message)
	}
}
