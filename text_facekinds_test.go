package pdf0

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// Setting text through the glyph path, in each of the three kinds of face.
//
// A face is embedded in one of three ways, and the number GlyphID returns means
// something different in each. For a composite face it is a glyph index, which
// is what the layout tables are keyed by and what a two-byte code names. For a
// simple or standard face it is a *character code* — one byte, WinAnsi — and
// that number names nothing in GSUB or GPOS.
//
// The glyph path was written for the composite case and applied to all three:
// it looked widths up by a number that was a code, applied kerning keyed by a
// number that was a code, and wrote two bytes where a reader expects one. On a
// standard face, which has no program at all, it did not survive the first
// width lookup. These are the tests that would have caught it.

// realFace loads a font from the system to exercise the composite and simple
// paths against something a reader would accept. The test is skipped where none
// is installed rather than asserting on a synthetic font, because what is under
// test is the shape of the *output*, and a real face is what a caller has.
func realFace(t *testing.T, simple bool) *fonts.Face {
	t.Helper()
	candidates := []string{
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/truetype/freefont/FreeSans.ttf",
		"/System/Library/Fonts/Helvetica.ttc",
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var face *fonts.Face
		if simple {
			face, err = fonts.LoadSimple(data)
		} else {
			face, err = fonts.Load(data)
		}
		if err != nil {
			continue
		}
		return face
	}
	t.Skip("no system font available to load")
	return nil
}

// drawnCodes returns the bytes a face writes for a string through the glyph
// path, by drawing into a builder and pulling the shown string back out.
func drawnCodes(t *testing.T, face *fonts.Face, s string) []byte {
	t.Helper()
	var b content.Builder
	b.BeginText().SetFont("F0", 12)
	face.DrawShaped(&b, s, 12)
	b.EndText()
	stream, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	// The shown string is the last parenthesised run before Tj or TJ.
	open := bytes.IndexByte(stream, '(')
	closeAt := bytes.LastIndexByte(stream, ')')
	if open < 0 || closeAt < open {
		t.Fatalf("no string was shown: %q", stream)
	}
	return stream[open+1 : closeAt]
}

// TestStandardFaceDrawsWithoutPanicking is the crash. A standard face carries
// no font program, so asking it for a width by glyph index dereferences
// nothing — and the glyph path asked on the first character of the first
// string.
func TestStandardFaceDrawsWithoutPanicking(t *testing.T) {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	glyphs, missing := face.ShapeGlyphs("Hello")
	if missing != 0 {
		t.Errorf("%d characters could not be set in Helvetica", missing)
	}
	if len(glyphs) != 5 {
		t.Fatalf("got %d glyphs for 5 characters", len(glyphs))
	}
	for i, g := range glyphs {
		if g.XAdvance <= 0 {
			t.Errorf("glyph %d has advance %v; the published metrics were not found", i, g.XAdvance)
		}
	}
	// And the width it reports agrees with the one Measure reports, which comes
	// from the same published table by a different route.
	if got, want := fonts.MeasureGlyphs(glyphs, 12), face.Measure("Hello", 12); got != want {
		t.Errorf("the shaped run measures %v and Measure says %v", got, want)
	}
}

// TestCodeWidthMatchesTheEmbedding is the correctness point underneath the
// crash. A composite font's codes are two bytes; a simple or standard font's
// are one. Writing two where one is expected makes a reader read every pair of
// characters as a single code — a page of nonsense, not a subtle shift.
func TestCodeWidthMatchesTheEmbedding(t *testing.T) {
	standard, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if got := drawnCodes(t, standard, "AB"); !bytes.Equal(got, []byte("AB")) {
		t.Errorf("a standard face wrote %q for \"AB\", want one byte per character", got)
	}

	simple := realFace(t, true)
	if got := drawnCodes(t, simple, "AB"); len(got) != 2 {
		t.Errorf("a simple face wrote %d bytes for 2 characters: %q", len(got), got)
	}

	composite := realFace(t, false)
	if got := drawnCodes(t, composite, "AB"); len(got) != 4 {
		t.Errorf("a composite face wrote %d bytes for 2 characters, want 4: %q", len(got), got)
	}
}

// TestTextRoundTripsThroughEveryFaceKind is the end-to-end oracle. Text is set
// through the glyph path, the document is written and reparsed, and the text is
// extracted back. Nothing about the codes, the widths or the encoding can be
// wrong and still survive that.
func TestTextRoundTripsThroughEveryFaceKind(t *testing.T) {
	const text = "Hamburgefonstiv"
	cases := []struct {
		name string
		face func(t *testing.T) *fonts.Face
	}{
		{"standard", func(t *testing.T) *fonts.Face {
			f, err := fonts.Standard("Helvetica")
			if err != nil {
				t.Fatalf("loading: %v", err)
			}
			return f
		}},
		{"simple", func(t *testing.T) *fonts.Face { return realFace(t, true) }},
		{"composite", func(t *testing.T) *fonts.Face { return realFace(t, false) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			face := tc.face(t)
			doc := NewDocument()
			var b content.Builder
			b.BeginText().SetFont("F0", 12).MoveText(50, 100)
			if missing := face.DrawShaped(&b, text, 12); missing != 0 {
				t.Fatalf("%d characters could not be set", missing)
			}
			b.EndText()

			if _, err := doc.AddPage(Page{
				Width: 300, Height: 200, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F0": face},
			}); err != nil {
				t.Fatalf("adding the page: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Save(&buf); err != nil {
				t.Fatalf("save: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			got := strings.TrimSpace(rd.ExtractText())
			if got != text {
				t.Errorf("extracted %q, want %q", got, text)
			}
		})
	}
}

// TestSpanPathAgreesWithTheGlyphPathOnCodes pins that the older span API was
// fixed too. It had the same fault — two-byte codes and glyph-index kerning for
// every kind of face — and a caller using Shape rather than ShapeGlyphs would
// otherwise still produce the scrambled page.
func TestSpanPathAgreesWithTheGlyphPathOnCodes(t *testing.T) {
	standard, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	spans, missing := standard.Shape("AB")
	if missing != 0 {
		t.Fatalf("%d characters missing", missing)
	}
	var got []byte
	for _, s := range spans {
		got = append(got, s.Codes...)
	}
	if !bytes.Equal(got, []byte("AB")) {
		t.Errorf("the span path wrote %q for a standard face, want one byte per character", got)
	}

	simple := realFace(t, true)
	spans, _ = simple.Shape("AB")
	got = nil
	for _, s := range spans {
		got = append(got, s.Codes...)
	}
	if len(got) != 2 {
		t.Errorf("the span path wrote %d bytes for 2 characters in a simple face: %q", len(got), got)
	}
}

// TestFallbackWorksAcrossFaceKinds pins that the fallback stack does not have to
// know which kind of face it was handed, which is the whole reason the glyph
// path has to behave the same for all three.
func TestFallbackWorksAcrossFaceKinds(t *testing.T) {
	standard, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	composite := realFace(t, false)

	// Helvetica first: it covers Latin, and the composite face is there for
	// whatever WinAnsi does not have. Greek omega is outside WinAnsi, so it is
	// the character that must fall through — the em dash and the accented
	// Latin letters would not, because WinAnsi has them.
	const outsideWinAnsi = 'Ω'
	if _, ok := standard.GlyphID(outsideWinAnsi); ok {
		t.Fatal("the fixture is wrong: WinAnsi is not supposed to cover omega")
	}
	if _, ok := composite.GlyphID(outsideWinAnsi); !ok {
		t.Skip("the available system font does not carry Greek")
	}

	s := fonts.NewStack(standard, composite)
	runs, missing := s.ShapeRuns("Hi Ω")
	if missing != 0 {
		t.Errorf("%d characters no face covers, though between them they do", missing)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs; the text needs both faces", len(runs))
	}
	if runs[0].Face != standard.Face {
		t.Errorf("the Latin run was set in the wrong face")
	}
	if runs[1].Face != composite.Face {
		t.Errorf("the Greek run was set in the wrong face")
	}
	for i, r := range runs {
		for j, g := range r.Glyphs {
			if g.XAdvance <= 0 && r.Face == standard.Face {
				t.Errorf("run %d glyph %d has advance %v", i, j, g.XAdvance)
			}
		}
	}
}
