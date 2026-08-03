package fonts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
)

// The bundled face.

// TestBundledFontIsTheFileWeDocumented pins the bytes against the provenance
// recorded in notosans/README.md.
//
// A bundled binary is the one thing in a repository that can be swapped without
// anybody reading the diff. The checksum is what makes an upgrade a deliberate
// act: replacing the file without updating the record fails here rather than
// leaving the licence, the version and the coverage in the README describing a
// different font.
func TestBundledFontIsTheFileWeDocumented(t *testing.T) {
	const want = "bfb7bb691513f12e734dc346c03a03f784912432d7e3fa8e56efcf906fe86b3d"
	sum := sha256.Sum256(notoSansRegular)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("the bundled font is %s, and notosans/README.md says %s.\n"+
			"If this was an intentional upgrade, update the version, the checksums and the\n"+
			"coverage in that file — and re-check the licence for a Reserved Font Name.", got, want)
	}
}

// TestTheLicenceTravelsWithTheFont pins the condition the OFL actually imposes:
// the copyright notice and the licence go where the font goes. A caller that
// ships a binary has no file to point at, so it has to be reachable in the
// program.
func TestTheLicenceTravelsWithTheFont(t *testing.T) {
	// The licence is hard-wrapped at about seventy columns, so a clause that
	// reads as one sentence is several lines in the file. Collapsing whitespace
	// is what lets a test assert on what it says rather than on how it is set.
	text := strings.Join(strings.Fields(NotoSansLicense()), " ")
	for _, want := range []string{
		"Copyright 2022 The Noto Project Authors",
		"SIL OPEN FONT LICENSE Version 1.1",
		// The clause that says a PDF made with the font is not itself covered.
		"does not apply to any document created using the Font Software",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the bundled licence text does not contain %q", want)
		}
	}
}

// TestBundledFontDeclaresNoReservedFontName pins the fact the subsetter depends
// on.
//
// Subsetting is a modification, and OFL 1.1 forbids a modified version from
// using a Reserved Font Name. Noto Sans declares none — the copyright line
// carries no "with Reserved Font Name" clause, and the only mention of the term
// is the licence's own definition. If a future version declared one, the subsets
// this module writes would have to be renamed, and this is where that would be
// noticed.
func TestBundledFontDeclaresNoReservedFontName(t *testing.T) {
	text := NotoSansLicense()
	copyright, _, ok := strings.Cut(text, "\n")
	if !ok {
		t.Fatal("the licence has no copyright line")
	}
	if strings.Contains(strings.ToLower(copyright), "reserved font name") {
		t.Errorf("the copyright line now declares a Reserved Font Name: %q\n"+
			"Subsets may no longer carry the family name; the subsetter needs revisiting.", copyright)
	}
}

// TestBundledFontSetsTextInEachScriptItCovers is the capability claim. A face
// that loads is not a face that works, and the point of bundling one is that a
// caller can set text without finding a font first.
func TestBundledFontSetsTextInEachScriptItCovers(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !strings.Contains(f.Name(), "NotoSans") {
		t.Errorf("the face is named %q", f.Name())
	}
	samples := map[string]string{
		"Devanagari":     "नमस्ते",
		"Latin":          "Hamburgefonstiv",
		"Latin accented": "café naïve Ærø",
		"Greek":          "Ωμέγα αβγδ",
		"Cyrillic":       "Привет мир",
		"punctuation":    "“quoted” — dashed…",
	}
	for name, s := range samples {
		glyphs, missing := f.ShapeGlyphs(s)
		if missing != 0 {
			t.Errorf("%s: %d characters of %q have no glyph", name, missing, s)
		}
		if len(glyphs) == 0 {
			t.Errorf("%s: nothing was shaped", name)
			continue
		}
		if w := MeasureGlyphs(glyphs, 12); w <= 0 {
			t.Errorf("%s: measured %v", name, w)
		}
	}
}

// TestBundledFontGivesEachScriptItsOwnRules is the script selection checked
// against a real face rather than a fixture.
//
// Noto Sans declares twenty-one 'locl' substitutions across its scripts and
// languages — the letterform corrections that make Serbian Cyrillic look
// Serbian and Romanian Latin look Romanian — spread over a dozen separate
// features. Taken together, as a reader that ignores the ScriptList must take
// them, a Latin run receives the corrections meant for Serbian. Taken per
// script, each run receives its own set, and the sets differ.
func TestBundledFontGivesEachScriptItsOwnRules(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	const tag = "locl"

	everything := f.layout.single[tag]
	if len(everything) == 0 {
		t.Fatalf("the fixture assumption is gone: the face declares no %q substitutions at all", tag)
	}

	latin := f.layoutFor(runScript("abc")).single[tag]
	cyrillic := f.layoutFor(runScript("абв")).single[tag]
	if sameSubstitutions(latin, cyrillic) {
		t.Errorf("Latin and Cyrillic runs get the same %q substitutions (%d of them); "+
			"the script list was not consulted", tag, len(latin))
	}
	for name, sel := range map[string]map[int]int{"Latin": latin, "Cyrillic": cyrillic} {
		if len(sel) >= len(everything) {
			t.Errorf("%s gets %d %q substitutions and the whole font declares %d; "+
				"a script should get fewer than all of them", name, len(sel), tag, len(everything))
		}
		for from, to := range sel {
			if everything[from] != to {
				t.Errorf("%s substitutes glyph %d with %d, which is not what the font declares anywhere",
					name, from, to)
			}
		}
	}

	// And a language system narrows it further. Romanian is one of the seven
	// Noto Sans declares under 'latn'.
	f.SetLanguage("ROM ")
	romanian := f.layoutFor(runScript("abc")).single[tag]
	if sameSubstitutions(romanian, latin) {
		t.Errorf("Romanian gets the same %d %q substitutions as the default language system",
			len(romanian), tag)
	}
}

func sameSubstitutions(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for from, to := range a {
		if b[from] != to {
			return false
		}
	}
	return true
}

// TestBundledFontShapesRatherThanJustEncodes pins that the composite form is
// the one with the layout tables, which is why it is the default.
func TestBundledFontShapesRatherThanJustEncodes(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !f.HasKerning() {
		t.Error("the bundled face reports no kerning; its GPOS was not read")
	}
	if !f.HasLigatures() {
		t.Error("the bundled face reports no ligatures; its GSUB was not read")
	}
	// "ffi" is one glyph in a face that has the ligature.
	glyphs, _ := f.ShapeGlyphs("ffi")
	if len(glyphs) >= 3 {
		t.Errorf("ffi produced %d glyphs; the ligature did not apply", len(glyphs))
	}
}

// TestEachCallGetsItsOwnFace pins the isolation subsetting depends on. A face
// records the glyphs it was asked to set, so a shared one would put each
// document's glyphs into the other's font — which is not a leak of anything
// secret, but is a font that carries glyphs the document never uses and, worse,
// a subset computed from the wrong set.
func TestEachCallGetsItsOwnFace(t *testing.T) {
	first, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	second, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if first == second {
		t.Fatal("two calls returned the same face")
	}
	first.ShapeGlyphs("Hamburgefonstiv")
	if len(second.Used()) != 0 {
		t.Errorf("the second face recorded %d glyphs the first one set", len(second.Used()))
	}
}

// TestTheSimpleFormIsSmallerAndNarrower pins what the two forms trade. The
// simple form writes one byte per character and cannot reach past WinAnsi;
// nothing about that is a bug, and a caller choosing it should get what it says.
func TestTheSimpleFormIsSmallerAndNarrower(t *testing.T) {
	simple, err := NotoSansSimple()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !simple.IsSimple() {
		t.Fatal("NotoSansSimple did not return a simple face")
	}
	// Latin works.
	if _, missing := simple.ShapeGlyphs("café"); missing != 0 {
		t.Errorf("%d Latin characters could not be set in the simple form", missing)
	}
	// Greek does not: it is outside WinAnsi.
	if _, missing := simple.ShapeGlyphs("Ωμέγα"); missing == 0 {
		t.Error("the simple form set Greek; WinAnsi does not cover it")
	}
	// One byte per character.
	var b content.Builder
	b.BeginText().SetFont("F0", 12)
	simple.DrawShaped(&b, "AB", 12)
	b.EndText()
	stream, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	if !strings.Contains(string(stream), "(AB)") {
		t.Errorf("the simple form did not write one byte per character: %q", stream)
	}
}

// TestTheBundledFontSubsetsToWhatWasUsed pins the size claim. Bundling 600 kB
// is only defensible because what reaches a document is a fraction of it.
func TestTheBundledFontSubsetsToWhatWasUsed(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	f.ShapeGlyphs("Hello")
	sub, err := f.Subset()
	if err != nil {
		t.Fatalf("subsetting: %v", err)
	}
	if len(sub) >= len(notoSansRegular) {
		t.Errorf("the subset is %d bytes and the font is %d", len(sub), len(notoSansRegular))
	}
	// A handful of glyphs should not carry most of the file.
	if ratio := float64(len(sub)) / float64(len(notoSansRegular)); ratio > 0.5 {
		t.Errorf("a five-character subset kept %.0f%% of the font", ratio*100)
	}
}

// TestBundledFontCoversTheScriptsWeClaim is the guard that was missing when this
// bundled the wrong file.
//
// The first font committed here was NotoSans-Regular from the per-script
// upstream, which carries Latin, Greek and Cyrillic and *no Devanagari at all* —
// while Google Fonts ships a "Noto Sans" that has it. Nothing noticed, because
// every test asked only about scripts that font happened to have. A coverage
// claim in the README with no test behind it is a claim that rots.
func TestBundledFontCoversTheScriptsWeClaim(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	blocks := []struct {
		name   string
		lo, hi rune
	}{
		{"Basic Latin", 0x0020, 0x007E},
		{"Latin-1 Supplement", 0x00A0, 0x00FF},
		{"Cyrillic", 0x0400, 0x04FF},
		{"Devanagari", 0x0900, 0x097F},
	}
	for _, b := range blocks {
		var have, total int
		for r := b.lo; r <= b.hi; r++ {
			total++
			if _, ok := f.GlyphID(r); ok {
				have++
			}
		}
		if have == 0 {
			t.Errorf("%s: the bundled font covers none of it, and notosans/README.md claims it does", b.name)
			continue
		}
		// Greek is deliberately absent from this list: the face covers most of
		// the block but not the archaic letters, so a "most of it" threshold
		// there would be arbitrary. These four are covered in full.
		if have != total {
			t.Errorf("%s: %d of %d code points", b.name, have, total)
		}
	}
}

// TestBundledFontDeclaresTheShapingItNeeds pins that the face carries the layout
// tables for the scripts it covers. Glyphs alone do not set Devanagari: the
// reordering and conjunct formation are declared under the deva and dev2 script
// tags, and a font with the glyphs and not the tables produces a row of
// unjoined letters.
func TestBundledFontDeclaresTheShapingItNeeds(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	for _, tag := range []string{"latn", "cyrl", "grek", "deva", "dev2"} {
		if !f.HasScript(tag) {
			t.Errorf("the bundled font declares no %q script in its layout tables", tag)
		}
	}
}
