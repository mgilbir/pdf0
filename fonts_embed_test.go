package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
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

// corpusOpenTypeCFF returns a real CFF-flavoured OpenType program from the
// veraPDF corpus that this package accepts, or skips.
//
// It searches for one that loads rather than taking the first it finds, because
// most CFF programs in the wild are CID-keyed and those are deliberately
// refused: their CIDs are not glyph indices, and everything here assumes they
// are.
//
// The synthetic fixtures elsewhere in this file cannot serve here: building a
// valid CFF means emitting INDEX structures, DICT operators and Type 2
// charstrings, which is a font compiler. Borrowing a real one from the corpus
// is the honest alternative, and it follows what the rest of this repository
// already does — the corpus is fetched, not vendored, so the test skips when it
// is absent rather than failing.
func corpusOpenTypeCFF(t *testing.T) []byte {
	t.Helper()
	root := "testdata/verapdf-corpus"
	if _, err := os.Stat(root); err != nil {
		t.Skip("veraPDF corpus not present; run `make corpus`")
	}
	var found []byte
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if found != nil || err != nil || info.IsDir() || filepath.Ext(p) != ".pdf" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		defer func() { _ = recover() }()
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil
		}
		for _, iobj := range doc.Objects {
			s, ok := iobj.Value.(*object.Stream)
			if !ok || s.Dict.Get("Subtype") != object.Name("OpenType") {
				continue
			}
			raw, err := doc.StreamData(s)
			if err != nil || len(raw) < 4 || string(raw[:4]) != "OTTO" {
				continue
			}
			if _, err := fonts.Load(raw); err != nil {
				continue // CID-keyed, or otherwise not one this package takes
			}
			if !selfConsistentWidths(raw) {
				continue
			}
			found = raw
			return filepath.SkipAll
		}
		return nil
	})
	if found == nil {
		t.Skip("no CFF-flavoured OpenType program found in the corpus")
	}
	return found
}

// selfConsistentWidths reports whether an OpenType CFF program's hmtx table and
// its own charstring widths agree.
//
// They must, and in a well-made font they do — but many of the programs in this
// corpus were extracted from files built to fail a conformance rule, and a font
// whose two width tables contradict each other cannot be embedded conformantly
// by anything: whichever a writer copies into /W, a reader consulting the other
// sees a mismatch. Selecting for consistency is therefore choosing a valid
// fixture, not tuning the test until it passes.
func selfConsistentWidths(program []byte) bool {
	sfnt := font.ParseSFNT(program, 1<<22)
	cff := font.ParseCFF(font.SFNTTables(program)["CFF "])
	if sfnt == nil || cff == nil || len(sfnt.WidthByGID) == 0 {
		return false
	}
	for gid := 0; gid < sfnt.NumGlyphs && gid < len(cff.WidthByGID); gid++ {
		if sfnt.WidthByGID[gid] != cff.WidthByGID[gid] {
			return false
		}
	}
	return true
}

// TestOpenTypeCFFEmbedsAndValidates exercises the CFF path end to end on a real
// font. It is the only way to know the path works: a CFF program cannot be
// synthesised here, and an untested branch that writes a font descriptor is
// exactly the kind of code that is wrong in a way nobody notices.
func TestOpenTypeCFFEmbedsAndValidates(t *testing.T) {
	program := corpusOpenTypeCFF(t)
	face, err := fonts.Load(program)
	if err != nil {
		t.Fatalf("a program the helper already loaded was refused: %v", err)
	}

	// Something the font actually covers.
	var text string
	for r := rune('A'); r <= 'z' && len(text) < 4; r++ {
		if _, ok := face.GlyphID(r); ok {
			text += string(r)
		}
	}
	if text == "" {
		t.Skip("the corpus program maps none of the ASCII letters")
	}
	codes, missing := face.Encode(text)
	if missing != 0 {
		t.Fatalf("%d runes missing from a font that reported them present", missing)
	}

	doc := NewPDFADocument(pdfa.PDFA2b)
	var b content.Builder
	b.BeginText().SetFont("F1", 18).MoveText(72, 700).ShowText(codes).EndText()
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
		t.Errorf("violation on a page set in a CFF-flavoured OpenType face: %s", e.Error())
	}

	// The descriptor must describe what was actually written: FontFile3 with an
	// OpenType subtype and a CIDFontType0 descendant, not the TrueType shape.
	var sawFontFile3, sawCIDFontType0 bool
	for _, iobj := range rd.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		if d.Get("FontFile3") != nil {
			sawFontFile3 = true
			if d.Get("FontFile2") != nil {
				t.Error("the descriptor carries both FontFile2 and FontFile3")
			}
		}
		if d.Get("Subtype") == object.Name("CIDFontType0") {
			sawCIDFontType0 = true
			if d.Get("CIDToGIDMap") != nil {
				t.Error("a CIDFontType0 descendant carries /CIDToGIDMap, which belongs to CIDFontType2")
			}
		}
	}
	if !sawFontFile3 || !sawCIDFontType0 {
		t.Errorf("FontFile3=%v CIDFontType0=%v; the CFF path did not write its own shape",
			sawFontFile3, sawCIDFontType0)
	}
}

// TestCFFSubsetIsSmallerAndStillParses pins that subsetting a CFF pays and
// that what comes out is still a font. The structures a CFF holds are reached
// by absolute offset, so changing the size of any one of them moves every
// offset that names it — a subsetter that got that wrong produces bytes that
// look plausible and parse as nothing.
func TestCFFSubsetIsSmallerAndStillParses(t *testing.T) {
	program := corpusOpenTypeCFF(t)
	face, err := fonts.Load(program)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	var text string
	for r := rune('A'); r <= 'z' && len(text) < 3; r++ {
		if _, ok := face.GlyphID(r); ok {
			text += string(r)
		}
	}
	if text == "" {
		t.Skip("the corpus program maps none of the ASCII letters")
	}
	face.Encode(text)

	sub, err := face.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	if len(sub) >= len(program) {
		t.Errorf("the subset is %d bytes against the original's %d", len(sub), len(program))
	}
	parsed := font.ParseSFNT(sub, 1<<22)
	if parsed == nil {
		t.Fatal("this module's own reader rejected the subset font")
	}
	if parsed.NumGlyphs != face.NumGlyphs() {
		t.Errorf("NumGlyphs = %d, want %d unchanged: indices are retained", parsed.NumGlyphs, face.NumGlyphs())
	}
	cff := font.ParseCFF(font.SFNTTables(sub)["CFF "])
	if cff == nil {
		t.Fatal("the subsetted CFF table did not parse")
	}
	// The Private DICT must survive the move intact: it carries the default and
	// nominal widths every charstring's width is expressed against, so a subset
	// that relocated it wrongly would give every glyph a different width
	// without changing a single charstring.
	origPriv, err := shape.PrivateDictForTest(font.SFNTTables(program)["CFF "])
	if err != nil {
		t.Fatalf("reading the original Private DICT: %v", err)
	}
	subPriv, err := shape.PrivateDictForTest(font.SFNTTables(sub)["CFF "])
	if err != nil {
		t.Fatalf("reading the subset Private DICT: %v", err)
	}
	if !bytes.Equal(origPriv, subPriv) {
		t.Errorf("the Private DICT changed across subsetting: %d bytes became %d", len(origPriv), len(subPriv))
	}

	// A kept glyph keeps its width; the widths are what /W is written from, so
	// a subset that disturbed them would produce a document the validator
	// rejects.
	for _, r := range text {
		gid, _ := face.GlyphID(r)
		if gid >= len(cff.WidthByGID) {
			t.Fatalf("glyph %d is missing from the subset", gid)
		}
		want, _ := face.Advance(r)
		if cff.WidthByGID[gid] != want {
			t.Errorf("glyph %d width = %v after subsetting, want %v", gid, cff.WidthByGID[gid], want)
		}
	}
}

// TestCFFSubsetDropsUnusedOutlines pins where the saving comes from. A dropped
// glyph becomes a charstring of one endchar operator — a glyph that draws
// nothing — rather than a zero-length entry, which is not a charstring at all
// and which a renderer may reject.
func TestCFFSubsetDropsUnusedOutlines(t *testing.T) {
	program := corpusOpenTypeCFF(t)
	face, err := fonts.Load(program)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	var keptRune rune
	for r := rune('A'); r <= 'z'; r++ {
		if _, ok := face.GlyphID(r); ok {
			keptRune = r
			break
		}
	}
	if keptRune == 0 {
		t.Skip("the corpus program maps none of the ASCII letters")
	}
	face.Encode(string(keptRune))
	keptGID, _ := face.GlyphID(keptRune)

	sub, err := face.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	strings := charStringsOf(t, font.SFNTTables(sub)["CFF "])
	if len(strings) != face.NumGlyphs() {
		t.Fatalf("the subset holds %d charstrings, want %d", len(strings), face.NumGlyphs())
	}
	if len(strings[keptGID]) <= 1 {
		t.Errorf("the kept glyph's charstring is %d bytes; its outline was dropped", len(strings[keptGID]))
	}
	dropped, emptied := 0, 0
	for gid, cs := range strings {
		if gid == 0 || gid == keptGID {
			continue
		}
		dropped++
		if len(cs) == 1 && cs[0] == 14 { // endchar
			emptied++
		}
	}
	if dropped == 0 {
		t.Fatal("the fixture font has nothing to drop")
	}
	if emptied != dropped {
		t.Errorf("%d of %d unused glyphs kept their outlines", dropped-emptied, dropped)
	}
}

// TestCFFSubsetValidatesAtEveryLevel runs a page set in a subsetted CFF face
// past the validator at each conformance level. The font is a subset now — its
// /BaseFont carries a tag saying so — and the rules that apply to a subset are
// not the same at every level.
func TestCFFSubsetValidatesAtEveryLevel(t *testing.T) {
	program := corpusOpenTypeCFF(t)
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			face, err := fonts.Load(program)
			if err != nil {
				t.Fatalf("loading: %v", err)
			}
			var text string
			for r := rune('A'); r <= 'z' && len(text) < 4; r++ {
				if _, ok := face.GlyphID(r); ok {
					text += string(r)
				}
			}
			if text == "" {
				t.Skip("the corpus program maps none of the ASCII letters")
			}
			codes, _ := face.Encode(text)

			doc := NewPDFADocument(level)
			var b content.Builder
			b.BeginText().SetFont("F1", 18).MoveText(72, 700).ShowText(codes).EndText()
			drawn, err := b.Bytes()
			if err != nil {
				t.Fatalf("drawing: %v", err)
			}
			fontRef, err := face.Embed(doc)
			if err != nil {
				t.Fatalf("embedding: %v", err)
			}
			attachPage(doc, drawn, fontRef)

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

// charStringsOf pulls the CharStrings INDEX out of a CFF table, so a test can
// look at what the subsetter actually wrote.
func charStringsOf(t *testing.T, cff []byte) [][]byte {
	t.Helper()
	items, err := shape.CharStringsForTest(cff)
	if err != nil {
		t.Fatalf("reading the subsetted CharStrings: %v", err)
	}
	return items
}

// attachPage gives doc a single page showing drawn with one font.
func attachPage(doc *Document, drawn []byte, fontRef object.IndirectRef) {
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
}

// TestCIDKeyedCFFIsRefused pins the other half of CFF support: the fonts this
// package will not take. A CID-keyed CFF numbers its glyphs by CID and maps CID
// to glyph index through its charset, so the two are different numberings —
// while everything here assumes they are the same, because Encode emits glyph
// indices as character codes.
//
// Embedding one anyway produces /W keyed by one numbering and codes by the
// other, which this module's own validator reports. Refusing is the honest
// answer until the charset is read, and this is the test that says the refusal
// happens rather than being an intention in a comment.
//
// The corpus carries CID-keyed CFF programs but none inside an OpenType
// wrapper, so the fixture wraps a real one — writing a CID-keyed CFF from
// scratch is a font compiler, and a synthetic one would not exercise the
// detection that matters.
func TestCIDKeyedCFFIsRefused(t *testing.T) {
	cff := corpusCIDKeyedCFF(t)
	program := fonttest.OTTO(cff, fonttest.SFNTOptions{
		Name: "CIDKeyed",
		Glyphs: []fonttest.Glyph{
			{Rune: 'A', Advance: 500, HasShape: true},
			{Rune: 'B', Advance: 500, HasShape: true},
		},
	})
	if _, err := fonts.Load(program); err == nil {
		t.Error("a CID-keyed CFF font was accepted; its CIDs are not glyph indices")
	} else if !strings.Contains(err.Error(), "CID-keyed") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

// corpusCIDKeyedCFF returns a bare CID-keyed CFF program from the corpus.
func corpusCIDKeyedCFF(t *testing.T) []byte {
	t.Helper()
	root := "testdata/verapdf-corpus"
	if _, err := os.Stat(root); err != nil {
		t.Skip("veraPDF corpus not present; run `make corpus`")
	}
	var found []byte
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if found != nil || err != nil || info.IsDir() || filepath.Ext(p) != ".pdf" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		defer func() { _ = recover() }()
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil
		}
		for _, iobj := range doc.Objects {
			s, ok := iobj.Value.(*object.Stream)
			if !ok || s.Dict.Get("Subtype") != object.Name("CIDFontType0C") {
				continue
			}
			raw, err := doc.StreamData(s)
			if err != nil {
				continue
			}
			if prog := font.ParseCFF(raw); prog != nil && prog.WidthByCID != nil {
				found = raw
				return filepath.SkipAll
			}
		}
		return nil
	})
	if found == nil {
		t.Skip("no CID-keyed CFF program found in the corpus")
	}
	return found
}

// simpleFace is a synthetic face covering enough of WinAnsiEncoding to be
// embedded as a simple font, including characters from the 0x80-0x9F band
// where the encoding and Latin-1 disagree.
func simpleFace(t *testing.T) *fonts.Face {
	t.Helper()
	var glyphs []fonttest.Glyph
	for r := rune(' '); r <= '~'; r++ {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 400 + int(r)%200, HasShape: r != ' '})
	}
	for _, r := range []rune{'—', '“', '”', '•', 'é', 'ü'} {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: true})
	}
	f, err := fonts.LoadSimple(fonttest.SFNT(fonttest.SFNTOptions{Name: "Simple-Regular", Glyphs: glyphs}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestSimpleFontEncodesOneBytePerCharacter pins the difference from a composite
// font. The codes of a simple font *are* the text, which is the property that
// makes such a document searchable by a reader that consults no CMap at all.
func TestSimpleFontEncodesOneBytePerCharacter(t *testing.T) {
	f := simpleFace(t)
	if !f.IsSimple() {
		t.Fatal("LoadSimple returned a face that is not simple")
	}
	codes, missing := f.Encode("Hi!")
	if missing != 0 {
		t.Fatalf("missing = %d", missing)
	}
	if string(codes) != "Hi!" {
		t.Errorf("Encode = %q, want %q: WinAnsi is ASCII here", codes, "Hi!")
	}
	// A character the encoding cannot name is reported, not substituted.
	if _, missing := f.Encode("a中b"); missing != 1 {
		t.Errorf("missing = %d, want 1", missing)
	}
}

// TestSimpleFontValidatesAtEveryLevel is the oracle. A simple font has its own
// shape — /FirstChar, /LastChar, /Widths indexed by code, a nonsymbolic
// descriptor, a named encoding — and each of those is a rule the validator
// checks, differently at different levels.
func TestSimpleFontValidatesAtEveryLevel(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			face := simpleFace(t)
			const text = "Hello — “world” • café"
			codes, missing := face.Encode(text)
			if missing != 0 {
				t.Fatalf("%d characters outside the encoding", missing)
			}
			doc := NewPDFADocument(level)
			var b content.Builder
			b.BeginText().SetFont("F1", 14).MoveText(72, 700).ShowText(codes).EndText()
			drawn, err := b.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			fontRef, err := face.Embed(doc)
			if err != nil {
				t.Fatalf("embedding: %v", err)
			}
			attachPage(doc, drawn, fontRef)

			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatal(err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range ValidatePDFABytes(rd, level, buf.Bytes()) {
				t.Errorf("violation: %s", e.Error())
			}
			// And the text reads back, including the characters whose codes lie
			// where WinAnsiEncoding and Latin-1 disagree.
			if got := rd.ExtractText(); !strings.Contains(got, "—") || !strings.Contains(got, "café") {
				t.Errorf("extracted %q, which is missing the typographic characters", got)
			}
		})
	}
}

// TestSimpleFontWidthsAreIndexedByCode pins the difference that is easiest to
// get wrong. A composite font's /W is keyed by glyph index and a simple font's
// /Widths by character code; writing one where the other belongs produces a
// document whose every advance is some other glyph's.
func TestSimpleFontWidthsAreIndexedByCode(t *testing.T) {
	face := simpleFace(t)
	face.Encode("A")
	doc := NewPDFADocument(pdfa.PDFA2b)
	if _, err := face.Embed(doc); err != nil {
		t.Fatal(err)
	}
	for _, iobj := range doc.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok || d.Get("Subtype") != object.Name("TrueType") {
			continue
		}
		first, _ := d.Get("FirstChar").(object.Integer)
		widths, ok := d.Get("Widths").(object.Array)
		if !ok {
			t.Fatal("the font has no /Widths array")
		}
		want, _ := face.Advance('A')
		got := object.Float(widths[int('A')-int(first)])
		if got != want {
			t.Errorf("/Widths for code %d ('A') = %v, want %v", 'A', got, want)
		}
		return
	}
	t.Fatal("no simple font was written")
}

// TestSimpleFontIsSubsetted pins that the narrow form still pays its way: only
// the glyphs the document showed are carried.
func TestSimpleFontIsSubsetted(t *testing.T) {
	face := simpleFace(t)
	face.Encode("A")
	sub, err := face.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	parsed := font.ParseSFNT(sub, 1<<20)
	if parsed == nil {
		t.Fatal("the subset did not parse")
	}
	kept, _ := face.GlyphIDForTest('A')
	if !parsed.GlyphNonEmpty[kept] {
		t.Error("the glyph that was shown lost its outline")
	}
	dropped, _ := face.GlyphIDForTest('Z')
	if parsed.GlyphNonEmpty[dropped] {
		t.Error("a glyph that was never shown kept its outline")
	}
}

// TestLoadSimpleRefusesWhatCannotBeOne pins the check at the door. A face that
// covers almost none of the encoding would produce a document of blanks, and
// a CFF program is not a TrueType font however it is dressed.
func TestLoadSimpleRefusesWhatCannotBeOne(t *testing.T) {
	sparse := fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Sparse",
		Glyphs: []fonttest.Glyph{{Rune: 'A', Advance: 500, HasShape: true}},
	})
	if _, err := fonts.LoadSimple(sparse); err == nil {
		t.Error("a face covering one character was accepted as a simple font")
	}
	if cff := corpusOpenTypeCFF(t); cff != nil {
		if _, err := fonts.LoadSimple(cff); err == nil {
			t.Error("a CFF program was accepted as a simple TrueType font")
		}
	}
}

// TestSimpleFontCarriesAToUnicodeCMap pins that the CMap is written even though
// a simple font with a standard encoding does not strictly need one: a reader
// could work the characters out from /Encoding, but only one that knows the
// encoding table. Saying it outright is what makes the text extractable by
// everything, and it is what a producer does.
//
// It has its own test because the extraction check cannot see it — this
// module's own extractor reads /Encoding too, so text comes back either way.
func TestSimpleFontCarriesAToUnicodeCMap(t *testing.T) {
	face := simpleFace(t)
	face.Encode("A")
	doc := NewPDFADocument(pdfa.PDFA2b)
	if _, err := face.Embed(doc); err != nil {
		t.Fatal(err)
	}
	for _, iobj := range doc.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok || d.Get("Subtype") != object.Name("TrueType") {
			continue
		}
		stream, ok := doc.Resolve(d.Get("ToUnicode")).(*object.Stream)
		if !ok {
			t.Fatal("the simple font carries no /ToUnicode CMap")
		}
		cmap, err := doc.StreamData(stream)
		if err != nil {
			t.Fatal(err)
		}
		// Code 0x41 is 'A', U+0041; the codespace is one byte.
		if !bytes.Contains(cmap, []byte("<41> <0041>")) {
			t.Errorf("the CMap does not map code 0x41 to U+0041:\n%s", cmap)
		}
		if !bytes.Contains(cmap, []byte("<00> <FF>")) {
			t.Error("the codespace range is not the single byte a simple font uses")
		}
		return
	}
	t.Fatal("no simple font was written")
}
