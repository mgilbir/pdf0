package fonts

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/testfiles"
)

// The one glyph-to-code path, from inside the package.
//
// The round trip through a written document is fonts_drawpaths_test.go in the
// module root, which draws through every public entry point in every kind of
// face. These are the properties underneath it that a document cannot show
// directly: that the codes are forme's, that each ToUnicode entry says what the
// glyph was drawn for, and that the CMap covers the glyphs drawn and no others.

// cjkFace is the CID-keyed CFF the round-trip tests use, or a skip.
func cjkFace(t *testing.T) *Face {
	t.Helper()
	data, err := os.ReadFile(testfiles.NotoCJK.File(t, "NotoSansJP-Regular.otf"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// everyFaceKind is one face of each kind this package embeds.
func everyFaceKind(t *testing.T) map[string]*Face {
	t.Helper()
	out := map[string]*Face{}
	var err error
	if out["composite"], err = NotoSans(); err != nil {
		t.Fatal(err)
	}
	if out["simple"], err = NotoSansSimple(); err != nil {
		t.Fatal(err)
	}
	if out["standard"], err = Standard("Helvetica"); err != nil {
		t.Fatal(err)
	}
	out["cid-keyed"] = cjkFace(t)
	return out
}

// TestEncodeAgreesWithFormesEncode holds the bytes Encode writes through
// appendCode to the bytes forme's own Encode computes. Encode calls forme's
// for the glyphs it records and writes its own codes, so that one function
// writes every code this package emits; this is what keeps the two from
// drifting apart.
func TestEncodeAgreesWithFormesEncode(t *testing.T) {
	texts := []string{
		"Hello, world!", "Café — “quoted” …", "a‍b­c", "日本語ｱ", "Ωμέγα",
		"क्षत्रिय", "a中b", "", "‍",
	}
	for name, face := range everyFaceKind(t) {
		for _, s := range texts {
			want, wantMissing := face.Clone().Face.Encode(s)
			got, gotMissing := face.Clone().Encode(s)
			if !bytes.Equal(got, want) || gotMissing != wantMissing {
				t.Errorf("%s, %q: Encode wrote % X (%d missing), forme's Encode % X (%d missing)",
					name, s, got, gotMissing, want, wantMissing)
			}
		}
	}
}

// TestEveryPathWritesTheCodeTheFontIsAddressedBy is the C24/C111 defect as
// codes rather than as a document: the CID for a CID-keyed CFF, one byte for a
// simple or standard face, whichever public path drew the text.
func TestEveryPathWritesTheCodeTheFontIsAddressedBy(t *testing.T) {
	const katakana = "ｱ" // CID 59158, glyph 15435 in the fixture face
	face := cjkFace(t)
	gid, ok := face.GlyphID('ｱ')
	if !ok {
		t.Fatal("the fixture face has no ｱ")
	}
	cid := face.GlyphCode(gid)
	if cid == gid {
		t.Fatal("the fixture is wrong: ｱ's CID and glyph index must differ")
	}
	want := []byte{byte(cid >> 8), byte(cid)}
	for name, codes := range shownByEveryPath(t, face, katakana) {
		if !bytes.Contains(codes, want) {
			t.Errorf("%s wrote % X for ｱ; the font is addressed by CID %d (% X)",
				name, codes, cid, want)
		}
	}

	simple, err := NotoSansSimple()
	if err != nil {
		t.Fatal(err)
	}
	for name, codes := range shownByEveryPath(t, simple, "office") {
		if string(codes) != "office" {
			t.Errorf("%s wrote % X for \"office\" in a simple face; want one WinAnsi byte "+
				"per character", name, codes)
		}
	}
}

// shownByEveryPath draws s through each public entry point, each with a fresh
// clone of the face, and returns the bytes each one showed.
func shownByEveryPath(t *testing.T, face *Face, s string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	codes, _ := face.Clone().Encode(s)
	out["Encode"] = codes
	for name, spansOf := range map[string]func(*Face) []content.TextSpan{
		"Shape":     func(f *Face) []content.TextSpan { sp, _ := f.Shape(s); return sp },
		"ShapeWith": func(f *Face) []content.TextSpan { sp, _ := f.ShapeWith(s, "smcp"); return sp },
	} {
		var shown []byte
		for _, sp := range spansOf(face.Clone()) {
			shown = append(shown, sp.Codes...)
		}
		out[name] = shown
	}
	for name, draw := range map[string]func(*Face, *content.Builder){
		"Draw": func(f *Face, b *content.Builder) {
			glyphs, _ := f.ShapeGlyphs(s)
			f.Draw(b, s, glyphs, 12)
		},
		"DrawShaped": func(f *Face, b *content.Builder) { f.DrawShaped(b, s, 12) },
	} {
		var b content.Builder
		b.BeginText().SetFont("F1", 12)
		draw(face.Clone(), &b)
		b.EndText()
		stream, err := b.Bytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = shownStrings(stream)
	}
	return out
}

// shownStrings concatenates the literal strings a content stream shows, which
// is what this package writes codes as.
func shownStrings(stream []byte) []byte {
	var out []byte
	for i := 0; i < len(stream); i++ {
		if stream[i] != '(' {
			continue
		}
		for i++; i < len(stream) && stream[i] != ')'; i++ {
			c := stream[i]
			if c == '\\' && i+1 < len(stream) {
				i++
				switch stream[i] {
				case 'r':
					c = '\r'
				default:
					c = stream[i]
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// TestToUnicodeSaysWhatEachGlyphWasDrawnFor is C25 at the CMap: a ligature's
// entry is the characters it replaced, a conjunct's is its cluster, and a
// glyph the font's cmap names is named as the cmap names it.
func TestToUnicodeSaysWhatEachGlyphWasDrawnFor(t *testing.T) {
	face, err := NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12)
	for _, s := range []string{"office", "क्षत्रिय", "नमस्ते"} {
		face.DrawShaped(&b, s, 12)
	}
	b.EndText()

	glyphs, _ := face.ShapeGlyphs("office")
	ffi := glyphs[1]
	if ffi.Cluster != 1 || glyphs[2].Cluster != 4 {
		t.Fatalf("the fixture shapes differently: %v", glyphs)
	}
	conjunct, _ := face.ShapeGlyphs("क्ष")
	if len(conjunct) != 1 {
		t.Fatalf("क्ष shaped as %d glyphs; the fixture needs the conjunct", len(conjunct))
	}
	// स्ते is three glyphs in one cluster: a half form no character maps to,
	// the letter and the vowel sign. The two the cmap names are those
	// characters, and the half form takes what they leave, which is स्.
	ste, _ := face.ShapeGlyphs("नमस्ते")
	if len(ste) != 5 || ste[2].Cluster != ste[3].Cluster {
		t.Fatalf("नमस्ते shaped as %v; the fixture needs the half form", ste)
	}
	cmap := string(face.toUnicodeCMap())
	for _, tc := range []struct {
		gid  int
		text string
	}{
		{ffi.GID, "ffi"},
		{conjunct[0].GID, "क्ष"},
		{glyphs[0].GID, "o"},
		{ste[2].GID, "स्"},
		{ste[3].GID, "त"},
	} {
		var entry bytes.Buffer
		entry.WriteByte('<')
		appendHex(&entry, face.GlyphCode(tc.gid), 4)
		entry.WriteString("> <")
		for _, r := range tc.text {
			appendUTF16BE(&entry, r)
		}
		entry.WriteString(">")
		if !strings.Contains(cmap, entry.String()) {
			t.Errorf("the ToUnicode CMap has no entry %s for glyph %d (%q)", entry.String(), tc.gid, tc.text)
		}
	}
}

// TestToUnicodeCoversOnlyTheGlyphsDrawn is the other half of C110: the CMap
// is one entry per glyph the page shows, not the font's whole cmap inverted.
func TestToUnicodeCoversOnlyTheGlyphsDrawn(t *testing.T) {
	face := cjkFace(t)
	codes, _ := face.Encode("ｱ日本")
	cmap := string(face.toUnicodeCMap())
	// Every entry line opens with "<"; so does the one codespace range line.
	if n := strings.Count(cmap, "\n<") - 1; n != len(codes)/2 {
		t.Errorf("the ToUnicode CMap has %d entries for a page showing %d glyphs", n, len(codes)/2)
	}
	if len(cmap) > 1024 {
		t.Errorf("the ToUnicode CMap for three glyphs is %d bytes", len(cmap))
	}
}

// TestAGlyphDrawnForTwoTextsCarriesBoth is the reuse case. 日 and the Kangxi
// radical ⽇ are one glyph in a CJK face; its ToUnicode entry can name one of
// them, and the page has to say the other some other way.
func TestAGlyphDrawnForTwoTextsCarriesBoth(t *testing.T) {
	face := cjkFace(t)
	sun, _ := face.GlyphID('日')
	radical, _ := face.GlyphID('⽇')
	if sun != radical {
		t.Skip("the fixture face draws 日 and ⽇ with different glyphs")
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12)
	face.DrawShaped(&b, "⽇", 12)
	face.DrawShaped(&b, "日", 12)
	b.EndText()
	stream, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// The glyph is named by the ideograph whichever came first: a document
	// sets 日, and the radical is the rarer reading of the shape.
	if got := face.record().byGID[sun]; got != "日" {
		t.Errorf("the glyph's ToUnicode text is %q, want 日", got)
	}
	// So the radical carries itself as an /ActualText: FEFF 2F47.
	if !bytes.Contains(stream, []byte("/ActualText <FEFF2F47>")) {
		t.Errorf("the radical was drawn with no /ActualText:\n%s", stream)
	}
	// And the ideograph needs none.
	if bytes.Count(stream, []byte("/ActualText")) != 1 {
		t.Errorf("expected exactly one /ActualText:\n%s", stream)
	}
}

// TestASimpleFaceDrawnByGlyphKeepsItsLetters is the subset of a simple face
// drawn through the glyph path. forme's one-code-per-character shaping records
// the code as the glyph used, so the subset kept glyphs 65 and 66 for "AB"
// rather than the glyphs A and B are.
func TestASimpleFaceDrawnByGlyphKeepsItsLetters(t *testing.T) {
	face, err := NotoSansSimple()
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12)
	face.DrawShaped(&b, "AB", 12)
	b.EndText()
	used := map[int]bool{}
	for _, g := range face.Used() {
		used[g] = true
	}
	for _, r := range "AB" {
		if gid := face.Cmap()[r]; !used[gid] {
			t.Errorf("the glyph for %q (%d) is not in the used set %v", r, gid, face.Used())
		}
	}
}

// TestPlanMarksWhatTheMappingCannotSay pins which stretches get an
// /ActualText: none for plain text, the reordered cluster for a Devanagari
// vowel sign, and the whole run for right-to-left text.
func TestPlanMarksWhatTheMappingCannotSay(t *testing.T) {
	face, err := NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	marked := func(s string) []string {
		glyphs, _ := face.ShapeGlyphs(s)
		var out []string
		for _, seg := range face.plan(glyphs, s) {
			if seg.marked {
				out = append(out, seg.actual)
			}
		}
		return out
	}
	if got := marked("office"); len(got) != 0 {
		t.Errorf("plain Latin with a ligature was marked: %q", got)
	}
	// A half form, a letter and a vowel sign in drawing order spell their
	// cluster once the half form has taken what the other two leave.
	if got := marked("नमस्ते"); len(got) != 0 {
		t.Errorf("a cluster its glyphs spell in order was marked: %q", got)
	}
	if got := marked("क्षत्रिय"); len(got) != 1 || got[0] != "त्रि" {
		t.Errorf("the reordered cluster was marked as %q, want [त्रि]", got)
	}
	// A right-to-left override: the glyphs are drawn in the other order.
	if got := marked("‮abc"); len(got) != 1 || got[0] != "abc" {
		t.Errorf("the right-to-left run was marked as %q, want [abc]", got)
	}
	// Glyphs shaped from some other string: the whole run says the text.
	glyphs, _ := face.ShapeGlyphs("abcdef")
	segs := face.plan(glyphs, "ab")
	if len(segs) != 1 || !segs[0].marked || segs[0].actual != "ab" {
		t.Errorf("clusters past the end of the text were trusted: %+v", segs)
	}
}
