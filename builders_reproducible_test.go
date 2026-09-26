package pdf0

import (
	"bytes"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Reproducible output from the builders, extending pdfa_reproducible_test.go's
// claim from the skeleton to everything that builds a document (audit
// 2026-09-22 C95).

// everyBuilderDoc builds one document through every builder this package has —
// pages with several faces and resource maps, a form, a tiling pattern, a
// gradient, links, an outline, a structure tree with a role map, document
// info — with the file identifier pinned, so nothing in it is meant to vary.
func everyBuilderDoc(t *testing.T) []byte {
	t.Helper()
	doc, err := NewPDFADocumentWith(pdfa.SkeletonOptions{
		Level: pdfa.PDFA2b, Title: "Repro", FileID: []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	composite, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	simple, err := fonts.NotoSansSimple()
	if err != nil {
		t.Fatal(err)
	}
	gs, err := Opacity(0.5, 1)
	if err != nil {
		t.Fatal(err)
	}
	stops := []Stop{{0, [3]float64{0, 0, 0}}, {0.5, [3]float64{1, 0, 0}}, {1, [3]float64{1, 1, 1}}}
	grad, err := LinearGradient(0, 0, 100, 0, stops)
	if err != nil {
		t.Fatal(err)
	}

	var cell content.Builder
	cell.SetRGB(0, 0, 1).Rect(0, 0, 4, 4).Fill()
	pattern, err := doc.AddTilingPattern(TilingPattern{BBox: [4]float64{0, 0, 8, 8}, Content: &cell,
		ExtGStates: map[object.Name]object.Object{"Z": gs, "Y": gs, "X": gs}})
	if err != nil {
		t.Fatal(err)
	}
	var formDrawing content.Builder
	formDrawing.BeginText().SetFont("FA", 10).MoveText(0, 0)
	composite.DrawShaped(&formDrawing, "form", 10)
	formDrawing.EndText()
	form, err := doc.AddForm(Form{BBox: [4]float64{0, 0, 50, 20}, Content: &formDrawing, Group: true,
		Faces: map[object.Name]*fonts.Face{"FA": composite}})
	if err != nil {
		t.Fatal(err)
	}

	var pages []object.IndirectRef
	for i, text := range []string{"First page", "Second page, more glyphs: xyzQ"} {
		var b content.Builder
		b.SetExtGState("G1").SetColorSpace("Pattern").SetPattern("P1").Rect(0, 0, 50, 50).Fill()
		b.Shading("S1").Draw("X1")
		b.BeginTagged("P", 0).BeginText().SetFont("F1", 12).MoveText(10, 100)
		composite.DrawShaped(&b, text, 12)
		b.EndText().EndMarked()
		b.BeginTagged("P", 1).BeginText().SetFont("F2", 12).MoveText(10, 80)
		simple.DrawShaped(&b, text, 12)
		b.EndText().EndMarked()
		p := Page{Width: 300, Height: 200, Content: &b,
			Faces:       map[object.Name]*fonts.Face{"F1": composite, "F2": simple},
			ExtGStates:  map[object.Name]object.Object{"G1": gs, "G9": gs, "G5": gs, "G7": gs, "G3": gs},
			Patterns:    map[object.Name]object.Object{"P1": pattern, "P9": pattern, "P4": pattern},
			Shadings:    map[object.Name]object.Object{"S1": grad, "S8": grad, "S3": grad},
			XObjects:    map[object.Name]object.Object{"X1": form, "X7": form, "X2": form, "X5": form},
			ColorSpaces: map[object.Name]object.Object{"CS3": object.Name("DeviceRGB"), "CS1": object.Name("DeviceRGB")},
		}
		if i == 1 {
			p.Links = []Link{{Rect: [4]float64{0, 0, 10, 10}, Page: &pages[0], To: Destination{Kind: AtTop, Top: 150}}}
		}
		ref, err := doc.AddPage(p)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, ref)
	}
	if err := doc.SetOutline([]OutlineItem{{Title: "One", Page: pages[0]}, {Title: "Two", Page: pages[1]}}); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetStructureTree([]StructElem{{Tag: "Chapter", Children: []StructElem{
		{Tag: "Para", Page: &pages[0], Content: []int{0, 1}},
		{Tag: "Aside2", Page: &pages[1], Content: []int{0, 1}},
	}}}, map[string]string{"Chapter": "Sect", "Para": "P", "Aside2": "Div",
		"Unused1": "Span", "Unused2": "Note", "Unused3": "Code"}); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetDocumentInfo(DocumentInfo{Title: "Repro", Author: "pdf0", Subject: "s", Keywords: "k"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestEveryBuilderIsReproducible: Go randomises map iteration, and every
// builder takes maps — resources by name, faces by name, a role map — so a
// builder that writes one in its iteration order produces a different file on
// each run. Twenty builds of the same document must be byte-identical.
func TestEveryBuilderIsReproducible(t *testing.T) {
	first := everyBuilderDoc(t)
	for i := 1; i < 20; i++ {
		if got := everyBuilderDoc(t); !bytes.Equal(got, first) {
			t.Fatalf("build %d differs from the first (%d vs %d bytes)", i, len(got), len(first))
		}
	}
}
