package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Laying text out with a standard face: the flow examples/text demonstrates,
// pinned here so a change to measurement or encoding is caught by a test rather
// than by looking at a rendered page.

// wrapGreedy fills lines to a width, measuring with the face they will be set
// in. It is the example's line breaker, kept here so the property below is
// about the same code.
func wrapGreedy(face *fonts.Face, text string, size, width float64) []string {
	var lines []string
	var line string
	for _, word := range strings.Fields(text) {
		cand := word
		if line != "" {
			cand = line + " " + word
		}
		if face.Measure(cand, size) > width && line != "" {
			lines = append(lines, line)
			line = word
			continue
		}
		line = cand
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// TestWrappedLinesFitTheirColumn is the property that makes measurement worth
// having. Every line a greedy breaker emits must fit, and a measurement that
// drifted would show up here as a line past the edge rather than as text
// spilling out of a column in a viewer.
func TestWrappedLinesFitTheirColumn(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	const width, size = 451.0, 11.0
	text := strings.Repeat("Typesetting a paragraph needs one thing a font can answer. ", 20)
	lines := wrapGreedy(face, text, size, width)
	if len(lines) < 10 {
		t.Fatalf("%d lines; the fixture does not exercise wrapping", len(lines))
	}
	for i, l := range lines {
		if w := face.Measure(l, size); w > width {
			t.Errorf("line %d is %.1fpt wide, past the %.0fpt column: %q", i, w, width, l)
		}
	}
}

// TestLaidOutTextExtracts pins the whole path end to end: measure, break,
// encode, draw, add the page, write, read, extract. The typographic characters
// are in the fixture deliberately — they are the ones whose codes lie where
// WinAnsiEncoding and Latin-1 disagree, so a document setting them is the one
// that used to come back wrong.
func TestLaidOutTextExtracts(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	const body = "An em dash — a bullet • curly “quotes” and an ellipsis… all set in a column."

	doc := &Document{Version: "1.7", Objects: map[int]*object.IndirectObject{}}
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	pagesRef := doc.Add(pages)
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", doc.Add(catalog))

	var b content.Builder
	y := 700.0
	for _, line := range wrapGreedy(face, body, 11, 300) {
		codes, missing := face.Encode(line)
		if missing != 0 {
			t.Fatalf("%d characters of %q are outside the encoding", missing, line)
		}
		b.BeginText().SetFont("H", 11).MoveText(72, y).ShowText(codes).EndText()
		y -= 15
	}
	fontRef, err := face.Embed(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.AddPage(Page{
		Width: 595, Height: 842, Content: &b,
		Fonts: map[object.Name]object.Object{"H": fontRef},
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	// Compare with line breaks collapsed: wrapping is what put them there.
	got := strings.Join(strings.Fields(rd.ExtractText()), " ")
	if got != body {
		t.Errorf("extracted %q,\n    want %q", got, body)
	}
}

// TestStandardFaceIsRefusedByPDFA pins the constraint a caller most needs told.
// A standard face embeds no program, and a conforming document may not show a
// font it does not embed — so the validator must say so rather than letting a
// file that cannot conform pass as one that does.
//
// It is reported as a missing /FontDescriptor, which is the mechanism: a
// descriptor is where a font program hangs, and a font that has none cannot
// have one embedded. The rule identifier is what a caller keys on, so that is
// what this asserts rather than the wording.
func TestStandardFaceIsRefusedByPDFA(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	doc := NewPDFADocument(pdfa.PDFA2b)
	codes, _ := face.Encode("Hello")
	var b content.Builder
	b.BeginText().SetFont("H", 12).MoveText(72, 700).ShowText(codes).EndText()
	fontRef, err := face.Embed(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.AddPage(Page{
		Width: 612, Height: 792, Content: &b,
		Fonts: map[object.Name]object.Object{"H": fontRef},
	}); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if e.RuleID() == "6.2.11.4.1" {
			found = true
		}
	}
	if !found {
		t.Error("a page showing a font the document does not embed was reported as conforming")
	}
}
