package pdf0

import (
	"encoding/binary"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestEmbeddedTrueTypeCarriesOnlyWhatItDraws holds the TrueType program a
// composite face embeds to the glyphs drawn, in a font far larger than the
// text: the twenty characters of a label, of Noto Sans's 4,515 glyphs.
//
// A CIDFontType2 shown by glyph index through /Identity reads the outlines and
// their metrics, and nothing else. forme v0.14.0's subset carried every glyph's
// name in post, the font's whole cmap, and a long metric per glyph in hmtx,
// which made the program many times what the glyphs need (#425, forme #979);
// v0.14.1 cuts each to what the subset keeps. loca stays at full length — four
// bytes a glyph is what keeping the numbering costs — and compresses to little,
// so the program in the file is bounded by a fixed allowance plus a per-glyph
// one.
//
// The trimmed hmtx is also what makes TestTheCIDSetIsWhatTheProgramHas's
// TrueType case mean something: its long metrics end before maxp's glyph count,
// the case veraPDF reads a TrueType program's CIDs from (see fonts.cidSet).
func TestEmbeddedTrueTypeCarriesOnlyWhatItDraws(t *testing.T) {
	s, err := notosans.Face()
	if err != nil {
		t.Fatal(err)
	}
	face := fonts.Adopt(s)
	const text = "Measurement 0123456789"
	back, _ := writeAndRead(t, textDocument(t, face, text, pdfa.PDFA2b))
	file, ok := back.Resolve(cidFontDescriptor(t, back).Get("FontFile2")).(*object.Stream)
	if !ok {
		t.Fatal("the face was not embedded as a /FontFile2")
	}
	program, err := back.StreamData(file)
	if err != nil {
		t.Fatal(err)
	}
	tables := font.SFNTTables(program)
	numGlyphs := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	longMetrics := int(binary.BigEndian.Uint16(tables["hhea"][34:]))

	highest, kept := 0, map[int]bool{}
	for _, r := range text {
		gid, ok := face.GlyphID(r)
		if !ok {
			t.Fatalf("Noto Sans has no glyph for %q", r)
		}
		kept[gid] = true
		highest = max(highest, gid)
	}

	// post format 3 is the 32-byte header and names no glyph.
	if post := tables["post"]; len(post) != 32 || binary.BigEndian.Uint32(post) != 0x00030000 {
		t.Errorf("post is %d bytes, version %#x; want the 32-byte format 3 header", len(post), binary.BigEndian.Uint32(post))
	}
	// hmtx: a long metric for no glyph past the last one drawn.
	if longMetrics > highest+1 || longMetrics >= numGlyphs {
		t.Errorf("hmtx has %d long metrics for a program of %d glyphs whose highest drawn is %d; want at most %d",
			longMetrics, numGlyphs, highest, highest+1)
	}
	if want := 4*longMetrics + 2*(numGlyphs-longMetrics); len(tables["hmtx"]) != want {
		t.Errorf("hmtx is %d bytes; %d long metrics over %d glyphs is %d", len(tables["hmtx"]), longMetrics, numGlyphs, want)
	}
	// cmap: a format 4 subtable for a few characters is tens of bytes, the
	// whole of Noto Sans's is kilobytes.
	if n := len(tables["cmap"]); n > 256 {
		t.Errorf("cmap is %d bytes; a map of the %d characters drawn is far less", n, len(kept))
	}

	// What reaches the file, compressed. Measured at forme v0.14.1: 3,459
	// bytes (32,516 decoded). At v0.14.0 the same text was 42,754 (109,562).
	const fixed, perGlyph = 3000, 120
	if limit := fixed + perGlyph*len(kept); len(file.Data) > limit {
		t.Errorf("the /FontFile2 of %d glyphs is %d bytes in the file (%d decoded); want at most %d",
			len(kept), len(file.Data), len(program), limit)
	}
}
