package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// TestUAPrinterMark checks that a tagged PrinterMark is flagged (it must be an
// artifact) and an untagged one is accepted.
func TestUAPrinterMark(t *testing.T) {
	mk := func(tagged bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		a := &object.Dictionary{}
		a.Set("Type", object.Name("Annot"))
		a.Set("Subtype", object.Name("PrinterMark"))
		if tagged {
			a.Set("StructParent", object.Integer(0))
		}
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}
		return doc
	}
	if !hasUAClause(checkUAAnnotations(mk(true)), "7.18.8") {
		t.Error("tagged PrinterMark not flagged")
	}
	if hasUAClause(checkUAAnnotations(mk(false)), "7.18.8") {
		t.Error("artifact PrinterMark wrongly flagged")
	}
	// An untagged PrinterMark must not be flagged by the general tagging rule.
	if hasUAClause(checkUAAnnotations(mk(false)), "7.18.1") {
		t.Error("PrinterMark wrongly subjected to the tagging rule")
	}
}

// TestUAMediaClips checks that a nested media clip missing /CT or /Alt is caught.
func TestUAMediaClips(t *testing.T) {
	mk := func(ct, alt bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		mc := &object.Dictionary{}
		mc.Set("Type", object.Name("MediaClip"))
		if ct {
			mc.Set("CT", object.String{Value: []byte("video/mp4")})
		}
		if alt {
			mc.Set("Alt", object.Array{object.String{Value: []byte("en")}, object.String{Value: []byte("a video")}})
		}
		// Nest it inside a rendition action so it is not a top-level object.
		rend := &object.Dictionary{}
		rend.Set("Type", object.Name("Rendition"))
		rend.Set("C", mc)
		action := &object.Dictionary{}
		action.Set("R", rend)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Screen"))
		annot.Set("A", action)
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: annot}
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
	if !has(checkUAMediaClips(mk(false, true))) {
		t.Error("media clip missing /CT not flagged")
	}
	if !has(checkUAMediaClips(mk(true, false))) {
		t.Error("media clip missing /Alt not flagged")
	}
	if has(checkUAMediaClips(mk(true, true))) {
		t.Error("complete media clip wrongly flagged")
	}
}

// TestUAMediaClipEmptyAlt flags a media clip whose /Alt has no non-empty text.
func TestUAMediaClipEmptyAlt(t *testing.T) {
	mk := func(alt object.Object) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		mc := &object.Dictionary{}
		mc.Set("Type", object.Name("MediaClip"))
		mc.Set("CT", object.String{Value: []byte("video/mp4")})
		mc.Set("Alt", alt)
		rend := &object.Dictionary{}
		rend.Set("C", mc)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Screen"))
		annot.Set("A", rend)
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: annot}
		return doc
	}
	empty := object.Array{object.String{Value: []byte("")}, object.String{Value: []byte("")}}
	if !hasUAClause(checkUAMediaClips(mk(empty)), "7.18.6.2") {
		t.Error("empty /Alt not flagged")
	}
	good := object.Array{object.String{Value: []byte("")}, object.String{Value: []byte("a video")}}
	if hasUAClause(checkUAMediaClips(mk(good)), "7.18.6.2") {
		t.Error("media clip with alt text wrongly flagged")
	}
}
