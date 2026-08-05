package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// TestUAAnnotDescription checks the 7.18.1 alternate-description rule for
// non-Widget annotations.
func TestUAAnnotDescription(t *testing.T) {
	mk := func(subtype object.Name, contents, alt string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		a := &object.Dictionary{}
		a.Set("Type", object.Name("Annot"))
		a.Set("Subtype", subtype)
		a.Set("StructParent", object.Integer(0)) // isolate from the tagging rule
		if contents != "" {
			a.Set("Contents", object.String{Value: []byte(contents)})
		}
		if alt != "" {
			a.Set("Alt", object.String{Value: []byte(alt)})
		}
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}
		return doc
	}
	hasDesc := func(vs []Violation) bool {
		for _, e := range vs {
			if strings.Contains(e.Message, "alternate description (/Contents or /Alt)") {
				return true
			}
		}
		return false
	}
	if !hasDesc(checkUAAnnotations(mk("Highlight", "", ""))) {
		t.Error("Highlight without Contents/Alt not flagged")
	}
	if hasDesc(checkUAAnnotations(mk("Highlight", "a note", ""))) {
		t.Error("Highlight with Contents wrongly flagged")
	}
	if hasDesc(checkUAAnnotations(mk("Highlight", "", "alt"))) {
		t.Error("Highlight with Alt wrongly flagged")
	}
	// Widget and PrinterMark are exempt from this particular rule.
	if hasDesc(checkUAAnnotations(mk("Widget", "", ""))) {
		t.Error("Widget wrongly subjected to Contents/Alt rule")
	}
	if hasDesc(checkUAAnnotations(mk("PrinterMark", "", ""))) {
		t.Error("PrinterMark wrongly subjected to Contents/Alt rule")
	}
}
