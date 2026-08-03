package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Font embedding judged by this module's own font rules.
//
// pdfa/fonts.go is the strictest reader of an embedded font in this repository:
// it checks that /W agrees with the embedded program's own metrics, that
// /CIDSet lists exactly the glyphs the program has, that a Type0 font carries a
// ToUnicode CMap, and that every glyph shown is one the program defines. That
// makes it the specification for the writer, already executable and already
// hardened against the corpus — so these tests point it back at the output.

// testFace is a small synthetic font. Its metrics are chosen so an assertion
// can name an exact width instead of whatever a real face happens to use, and
// so that not every glyph shares one advance — a font where they did would hide
// a /W that was written wrongly.
func testFace(t *testing.T) *fonts.Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Probe-Regular",
		Glyphs: []fonttest.Glyph{
			{Rune: 'H', Advance: 722, HasShape: true},
			{Rune: 'e', Advance: 556, HasShape: true},
			{Rune: 'l', Advance: 222, HasShape: true},
			{Rune: 'o', Advance: 556, HasShape: true},
			{Rune: ' ', Advance: 278, HasShape: false},
			{Rune: 'W', Advance: 944, HasShape: true},
			{Rune: 'r', Advance: 333, HasShape: true},
			{Rune: 'd', Advance: 556, HasShape: true},
			{Rune: '!', Advance: 333, HasShape: true},
			{Rune: 'é', Advance: 556, HasShape: true},
		},
	})
	f, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading the synthetic font: %v", err)
	}
	return f
}

// typesetDoc builds a PDF/A-2b document whose single page shows text in an
// embedded font, and returns it with the face.
func typesetDoc(t *testing.T, text string) (*Document, *fonts.Face) {
	return typesetDocAt(t, pdfa.PDFA2b, text)
}

func typesetDocAt(t *testing.T, level pdfa.Level, text string) (*Document, *fonts.Face) {
	t.Helper()
	doc := NewPDFADocument(level)
	face := testFace(t)

	// Encode first, embed last: the subset carries the glyphs the face has been
	// asked for, so a font embedded before the text is drawn would carry none.
	codes, missing := face.Encode(text)
	if missing != 0 {
		t.Fatalf("the fixture font is missing %d runes of %q", missing, text)
	}

	var b content.Builder
	b.BeginText().SetFont("F1", 24).MoveText(72, 700).ShowText(codes).EndText()
	data, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}

	fontRef, err := face.Embed(doc)
	if err != nil {
		t.Fatalf("embedding: %v", err)
	}

	stream := &object.Stream{Dict: object.Dictionary{}, Data: data}
	stream.Dict.Set("Length", object.Integer(len(data)))
	contentRef := doc.Add(stream)

	fontDict := &object.Dictionary{}
	for _, name := range b.Resources().Fonts {
		fontDict.Set(name, fontRef)
	}
	resources := &object.Dictionary{}
	resources.Set("Font", fontDict)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792),
	})
	page.Set("Resources", resources)
	page.Set("Contents", contentRef)
	pageRef := doc.Add(page)

	pages := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
	pages.Set("Kids", object.Array{pageRef})
	pages.Set("Count", object.Integer(1))
	return doc, face
}

// TestEmbeddedTextValidatesAsPDFA is the end-to-end oracle. A page of text in an
// embedded font, written, re-read and validated, must raise nothing — which
// means /W matched the program, /CIDSet matched it, ToUnicode was present and
// well formed, every glyph shown was one the font defines, and the descriptor
// said what the program says.
func TestEmbeddedTextValidatesAsPDFA(t *testing.T) {
	doc, _ := typesetDoc(t, "Hello World!")

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation on a page of embedded text: %s", e.Error())
	}
}

// TestEmbeddedTextValidatesAtEveryLevel runs the same page at each conformance
// level, because the font rules are not the same at all of them. /CIDSet is the
// clearest case: whether it is required, and whether its contents are checked
// against the embedded program, depends on the level and on whether the font is
// a subset — so a writer verified only at PDF/A-2b has not been verified where
// the strictest of those rules lives.
func TestEmbeddedTextValidatesAtEveryLevel(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc, _ := typesetDocAt(t, level, "Hello World!")
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			for _, e := range ValidatePDFABytes(rd, level, buf.Bytes()) {
				t.Errorf("violation: %s", e.Error())
			}
		})
	}
}

// TestEmbeddedTextIsExtractable pins the other half of what ToUnicode is for.
// A page whose text cannot be read back is a page whose content is a picture of
// words, and the CMap is the only thing standing between the two.
func TestEmbeddedTextIsExtractable(t *testing.T) {
	const want = "Hello World!"
	doc, _ := typesetDoc(t, want)

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	got := rd.ExtractText()
	if !strings.Contains(got, want) {
		t.Errorf("extracted %q, which does not contain %q", got, want)
	}
}

// TestEncodeIsIdentityH pins the encoding contract: two bytes per glyph,
// big-endian, equal to the glyph index. Everything about a Type0/Identity-H
// font rests on that — /CIDToGIDMap /Identity, the CIDSet indices and the
// ToUnicode keys are all the same numbers — so it is worth stating once
// exactly.
func TestEncodeIsIdentityH(t *testing.T) {
	face := testFace(t)
	codes, missing := face.Encode("He")
	if missing != 0 {
		t.Fatalf("%d runes missing from the fixture font", missing)
	}
	if len(codes) != 4 {
		t.Fatalf("Encode(%q) produced %d bytes, want 4 (two per glyph)", "He", len(codes))
	}
	for i, r := range []rune{'H', 'e'} {
		gid, ok := face.GlyphID(r)
		if !ok {
			t.Fatalf("the fixture font has no glyph for %q", r)
		}
		got := int(codes[2*i])<<8 | int(codes[2*i+1])
		if got != gid {
			t.Errorf("code for %q = %d, want the glyph index %d", r, got, gid)
		}
	}
}

// TestUnmappedRuneEncodesToNotdef pins the deliberate choice not to fail. A
// caller laying out a document cannot have one stray character abort the page,
// and dropping it silently would lose content; .notdef renders as the visible
// box a reader expects, and the count says how many there were.
func TestUnmappedRuneEncodesToNotdef(t *testing.T) {
	face := testFace(t)
	codes, missing := face.Encode("H中o") // a CJK ideograph the fixture lacks
	if missing != 1 {
		t.Errorf("missing = %d, want 1", missing)
	}
	if len(codes) != 6 {
		t.Fatalf("got %d bytes, want 6", len(codes))
	}
	if codes[2] != 0 || codes[3] != 0 {
		t.Errorf("the unmapped rune encoded to %d, want glyph 0 (.notdef)", int(codes[2])<<8|int(codes[3]))
	}
}

// TestMeasureUsesTheProgramsOwnMetrics pins that measurement and the /W array
// come from one source. If they could disagree, text would be laid out to one
// set of widths and rendered to another — the classic cause of a line that
// overflows its box in a viewer but not in the engine that produced it.
func TestMeasureUsesTheProgramsOwnMetrics(t *testing.T) {
	face := testFace(t)
	// H=722, e=556, l=222, l=222, o=556 → 2278/1000 em at 24pt.
	const want = 2278.0 / 1000 * 24
	if got := face.Measure("Hello", 24); got != want {
		t.Errorf("Measure(\"Hello\", 24) = %v, want %v", got, want)
	}
	if w, ok := face.Advance('W'); !ok || w != 944 {
		t.Errorf("Advance('W') = %v, %v; want 944, true", w, ok)
	}
	// And the same numbers reach the file.
	doc, _ := typesetDoc(t, "Hello")
	var found bool
	for _, iobj := range doc.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok || d.Get("Subtype") != object.Name("CIDFontType2") {
			continue
		}
		found = true
		widths, ok := d.Get("W").(object.Array)
		if !ok {
			t.Fatal("the CIDFont has no /W array")
		}
		gid, _ := face.GlyphID('W')
		if got := widthFromW(widths, gid); got != 944 {
			t.Errorf("/W gives glyph %d a width of %v, want 944", gid, got)
		}
	}
	if !found {
		t.Fatal("no CIDFontType2 was written")
	}
}

// widthFromW reads one glyph's width out of a /W array in the
// "start [w1 w2 …]" form this writer emits.
func widthFromW(w object.Array, gid int) float64 {
	for i := 0; i+1 < len(w); i += 2 {
		start, ok := w[i].(object.Integer)
		if !ok {
			continue
		}
		run, ok := w[i+1].(object.Array)
		if !ok {
			continue
		}
		if gid >= int(start) && gid < int(start)+len(run) {
			return object.Float(run[gid-int(start)])
		}
	}
	return -1
}

// TestEmbeddedFontOracleHasTeeth proves the end-to-end check can fail, by
// breaking the one invariant the whole design rests on: /W must agree with the
// embedded program. A writer whose widths drift from its font produces a
// document that renders differently everywhere, and if this passes with a
// planted defect the tests above prove nothing.
func TestEmbeddedFontOracleHasTeeth(t *testing.T) {
	doc, _ := typesetDoc(t, "Hello World!")
	for _, iobj := range doc.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok || d.Get("Subtype") != object.Name("CIDFontType2") {
			continue
		}
		// Claim every glyph is 100 units wide, which the program contradicts.
		d.Set("W", object.Array{object.Integer(0), object.Array{
			object.Integer(100), object.Integer(100), object.Integer(100), object.Integer(100),
			object.Integer(100), object.Integer(100), object.Integer(100), object.Integer(100),
			object.Integer(100), object.Integer(100), object.Integer(100),
		}})
	}
	var found bool
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if strings.Contains(e.Error(), "width") {
			found = true
		}
	}
	if !found {
		t.Error("a /W array contradicting the embedded program was not reported, so the checks above could not fail either")
	}
}

// TestShapedTextValidatesAndKeepsItsLigature closes the loop between the three
// pieces: shaping picks a glyph no Encode call ever named, the subsetter must
// keep it, and the document must still be conforming. A subset that dropped the
// ligature would leave the page blank exactly where it was.
func TestShapedTextValidatesAndKeepsItsLigature(t *testing.T) {
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Ligature-Regular",
		Glyphs: []fonttest.Glyph{
			{Rune: 'f', Advance: 300, HasShape: true},
			{Rune: 'i', Advance: 250, HasShape: true},
			{Rune: 'ﬁ', Advance: 520, HasShape: true},
			{Rune: 'x', Advance: 400, HasShape: true},
		},
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUB([]fonttest.Ligature{{Components: []int{1, 2}, Glyph: 3}}),
		},
	})
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !face.HasLigatures() {
		t.Fatal("the fixture's GSUB was not read")
	}

	spans, missing := face.Shape("fix")
	if missing != 0 {
		t.Fatalf("%d runes missing", missing)
	}

	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.BeginText().SetFont("F1", 24).MoveText(72, 700).ShowTextAdjusted(spans...).EndText()
	drawn, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	fontRef, err := face.Embed(doc)
	if err != nil {
		t.Fatalf("embedding: %v", err)
	}

	stream := &object.Stream{Dict: object.Dictionary{}, Data: drawn}
	stream.Dict.Set("Length", object.Integer(len(drawn)))
	contentRef := doc.Add(stream)
	fontDict := &object.Dictionary{}
	fontDict.Set("F1", fontRef)
	resources := &object.Dictionary{}
	resources.Set("Font", fontDict)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792),
	})
	page.Set("Resources", resources)
	page.Set("Contents", contentRef)
	pageRef := doc.Add(page)
	pages := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
	pages.Set("Kids", object.Array{pageRef})
	pages.Set("Count", object.Integer(1))

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation on a page of shaped text: %s", e.Error())
	}
	// The ligature glyph reached the file with an outline.
	if got := rd.ExtractText(); !strings.Contains(got, "ﬁ") {
		t.Errorf("extracted %q, which does not contain the fi ligature", got)
	}
}
