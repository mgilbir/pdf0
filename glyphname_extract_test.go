package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// TestWinAnsiTypographyExtracts pins what resolving the standard encodings'
// glyph names is for. WinAnsiEncoding puts the curly quotes, the dashes, the
// bullet, the ellipsis and the euro between 0x80 and 0x9F, where nothing about
// the code implies the character — so before the glyph list was read, a
// document setting a quotation mark in a simple font had a character no reader
// here could name, and it came back as something else or not at all.
func TestWinAnsiTypographyExtracts(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	const want = "“curly” — €5…"
	codes, missing := face.Encode(want)
	if missing != 0 {
		t.Fatalf("%d characters of %q are outside WinAnsiEncoding", missing, want)
	}
	// Every one of these really does live in the awkward band.
	for _, c := range codes {
		if c >= 0x80 && c <= 0x9F {
			goto found
		}
	}
	t.Fatal("the fixture does not exercise the 0x80-0x9F range it is about")
found:

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

	fontRef, err := face.Embed(doc)
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12).MoveText(72, 700).ShowText(codes).EndText()
	if _, err := doc.AddPage(Page{
		Width: 612, Height: 792, Content: &b,
		Fonts: map[object.Name]object.Object{"F1": fontRef},
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
	got := rd.ExtractText()
	for _, r := range []string{"“", "”", "—", "€", "…"} {
		if !strings.Contains(got, r) {
			t.Errorf("extracted %q, which is missing %q", got, r)
		}
	}
}
