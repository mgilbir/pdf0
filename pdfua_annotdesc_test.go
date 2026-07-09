package pdf0

import (
	"strings"
	"testing"
)

// TestUAAnnotDescription checks the 7.18.1 alternate-description rule for
// non-Widget annotations.
func TestUAAnnotDescription(t *testing.T) {
	mk := func(subtype Name, contents, alt string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		a := &Dictionary{}
		a.Set("Type", Name("Annot"))
		a.Set("Subtype", subtype)
		a.Set("StructParent", Integer(0)) // isolate from the tagging rule
		if contents != "" {
			a.Set("Contents", String{Value: []byte(contents)})
		}
		if alt != "" {
			a.Set("Alt", String{Value: []byte(alt)})
		}
		doc.Objects[5] = &IndirectObject{Number: 5, Value: a}
		return doc
	}
	hasDesc := func(vs []UAViolation) bool {
		for _, e := range vs {
			if strings.Contains(e.Message, "alternate description (/Contents or /Alt)") {
				return true
			}
		}
		return false
	}
	if !hasDesc(mk("Highlight", "", "").checkUAAnnotations()) {
		t.Error("Highlight without Contents/Alt not flagged")
	}
	if hasDesc(mk("Highlight", "a note", "").checkUAAnnotations()) {
		t.Error("Highlight with Contents wrongly flagged")
	}
	if hasDesc(mk("Highlight", "", "alt").checkUAAnnotations()) {
		t.Error("Highlight with Alt wrongly flagged")
	}
	// Widget and PrinterMark are exempt from this particular rule.
	if hasDesc(mk("Widget", "", "").checkUAAnnotations()) {
		t.Error("Widget wrongly subjected to Contents/Alt rule")
	}
	if hasDesc(mk("PrinterMark", "", "").checkUAAnnotations()) {
		t.Error("PrinterMark wrongly subjected to Contents/Alt rule")
	}
}
