package pdf0

import (
	"strings"
	"testing"
)

// TestUAPrinterMark checks that a tagged PrinterMark is flagged (it must be an
// artifact) and an untagged one is accepted.
func TestUAPrinterMark(t *testing.T) {
	mk := func(tagged bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		a := &Dictionary{}
		a.Set("Type", Name("Annot"))
		a.Set("Subtype", Name("PrinterMark"))
		if tagged {
			a.Set("StructParent", Integer(0))
		}
		doc.Objects[5] = &IndirectObject{Number: 5, Value: a}
		return doc
	}
	if !hasUAClause(mk(true).checkUAAnnotations(), "7.18.8") {
		t.Error("tagged PrinterMark not flagged")
	}
	if hasUAClause(mk(false).checkUAAnnotations(), "7.18.8") {
		t.Error("artifact PrinterMark wrongly flagged")
	}
	// An untagged PrinterMark must not be flagged by the general tagging rule.
	if hasUAClause(mk(false).checkUAAnnotations(), "7.18.1") {
		t.Error("PrinterMark wrongly subjected to the tagging rule")
	}
}

// TestUAMediaClips checks that a nested media clip missing /CT or /Alt is caught.
func TestUAMediaClips(t *testing.T) {
	mk := func(ct, alt bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		mc := &Dictionary{}
		mc.Set("Type", Name("MediaClip"))
		if ct {
			mc.Set("CT", String{Value: []byte("video/mp4")})
		}
		if alt {
			mc.Set("Alt", Array{String{Value: []byte("en")}, String{Value: []byte("a video")}})
		}
		// Nest it inside a rendition action so it is not a top-level object.
		rend := &Dictionary{}
		rend.Set("Type", Name("Rendition"))
		rend.Set("C", mc)
		action := &Dictionary{}
		action.Set("R", rend)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Screen"))
		annot.Set("A", action)
		doc.Objects[5] = &IndirectObject{Number: 5, Value: annot}
		return doc
	}
	has := func(vs []UAViolation) bool {
		for _, e := range vs {
			if strings.HasPrefix(e.Clause, "7.18.6.2") {
				return true
			}
		}
		return false
	}
	if !has(mk(false, true).checkUAMediaClips()) {
		t.Error("media clip missing /CT not flagged")
	}
	if !has(mk(true, false).checkUAMediaClips()) {
		t.Error("media clip missing /Alt not flagged")
	}
	if has(mk(true, true).checkUAMediaClips()) {
		t.Error("complete media clip wrongly flagged")
	}
}
