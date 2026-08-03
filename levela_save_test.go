package pdf0

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Level A is the accessibility level, and what it is about is tagging: a
// reader must be able to follow the logical structure and find the content on
// it. A document that draws a page and describes none of it is exactly what the
// level exists to rule out.
//
// It used to be written without complaint. NewPDFADocument supplies the two
// things a presence check looks for — /MarkInfo /Marked true and a
// /StructTreeRoot — so a Level A validator that checks only those two declares
// a page of untagged drawing conforming, and Save writes a file whose metadata
// says PDF/A-1a and whose structure tree describes nothing.

// aBlackBar draws something that conforms at every level and carries no
// structure of its own.
func aBlackBar() *content.Builder {
	var b content.Builder
	b.SetRGB(0, 0, 0).Rect(10, 10, 100, 20).Fill()
	return &b
}

func TestSaveRefusesAnUntaggedLevelADocument(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1a, pdfa.PDFA2a, pdfa.PDFA3a} {
		doc := NewPDFADocument(level)
		if _, err := doc.AddPage(Page{Width: 200, Height: 200, Content: aBlackBar()}); err != nil {
			t.Fatalf("%s: adding the page: %v", level, err)
		}
		var buf bytes.Buffer
		err := doc.Save(&buf)
		if err == nil {
			t.Errorf("%s: an untagged document was written as conforming", level)
			continue
		}
		if buf.Len() != 0 {
			t.Errorf("%s: %d bytes were written despite the failure", level, buf.Len())
		}
		var ce *ConformanceError
		if !errors.As(err, &ce) {
			t.Fatalf("%s: the error is %T, want a *ConformanceError", level, err)
		}
		// The reason has to be the untagged content. A test satisfied by any
		// refusal would keep passing if the Level A rules were replaced by
		// something that rejects every document.
		found := false
		for _, v := range ce.Violations {
			if strings.Contains(v.Message, "outside any marked-content sequence") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the refusal does not name the untagged content: %v", level, ce.Violations)
		}
	}
}

// TestSaveAcceptsALevelBDocumentOfTheSameDrawing is the control: the same page
// at Level B is conforming, so the refusal above is about the tagging and not
// about the drawing.
func TestSaveAcceptsALevelBDocumentOfTheSameDrawing(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b} {
		doc := NewPDFADocument(level)
		if _, err := doc.AddPage(Page{Width: 200, Height: 200, Content: aBlackBar()}); err != nil {
			t.Fatalf("%s: adding the page: %v", level, err)
		}
		if err := doc.Save(io.Discard); err != nil {
			t.Errorf("%s: a conforming document was refused: %v", level, err)
		}
	}
}

// TestSaveAcceptsALevelADocumentWhoseContentIsMarked is the other control: the
// same drawing, marked as an artifact and hung off a structure tree that
// describes the page, is conforming — so the rule is about whether the content
// is described, not about Level A being unreachable.
func TestSaveAcceptsALevelADocumentWhoseContentIsMarked(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA1a)
	var b content.Builder
	b.BeginMarked("Artifact").SetRGB(0, 0, 0).Rect(10, 10, 100, 20).Fill().EndMarked()
	pageRef, err := doc.AddPage(Page{Width: 200, Height: 200, Content: &b})
	if err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	taggedStructTree(t, doc, pageRef)

	var buf bytes.Buffer
	if err := doc.Save(&buf); err != nil {
		t.Fatalf("a tagged document was refused: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("nothing was written")
	}
}

// taggedStructTree hangs a minimal but real structure tree — a Document holding
// a Figure that names the page — off the catalog of doc.
func taggedStructTree(t *testing.T, doc *Document, page object.IndirectRef) {
	t.Helper()
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		t.Fatal("the document has no catalog")
	}
	rootRef := doc.Add(&object.Dictionary{})
	figure := &object.Dictionary{}
	figure.Set("Type", object.Name("StructElem"))
	figure.Set("S", object.Name("Figure"))
	figure.Set("P", rootRef)
	figure.Set("Pg", page)
	figure.Set("Alt", object.String{Value: []byte("a black bar")})
	figureRef := doc.Add(figure)

	document := &object.Dictionary{}
	document.Set("Type", object.Name("StructElem"))
	document.Set("S", object.Name("Document"))
	document.Set("P", rootRef)
	document.Set("K", object.Array{figureRef})
	documentRef := doc.Add(document)

	root := doc.ResolveDict(rootRef)
	root.Set("Type", object.Name("StructTreeRoot"))
	root.Set("K", object.Array{documentRef})
	cat.Set("StructTreeRoot", rootRef)
}
