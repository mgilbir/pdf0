package fonts

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Shaping checked against HarfBuzz.
//
// Everything else in this package tests shaping against itself: a fixture built
// by fonttest, read by this package, asserted by this package. That catches a
// reader that contradicts itself and cannot catch a reader that is consistently
// wrong — and consistently wrong is what a misread of a font table looks like,
// because the same misreading writes the fixture and the expectation.
//
// HarfBuzz is outside. It is what browsers, terminals and typesetters actually
// shape with, so where the two differ, the page a reader sees is HarfBuzz's and
// not this package's. That is what makes it ground truth rather than a second
// opinion.
//
// # What it found
//
// It was written after three defects had already reached the tree, all three
// invisible to a test built from this package's own reading:
//
//   - mark attachment took anchors from whichever subtable was read last, so a
//     third of the sample had its accents in the wrong place;
//   - a ligature deleted the marks it stepped over;
//   - pair adjustments were read from 'kern' alone, so every Devanagari conjunct
//     was set at its nominal width, because this font states Devanagari spacing
//     under 'dist' and declares no 'kern' for it at all.
//
// Each was a plausible reading of the specification that no self-consistent test
// would ever have contradicted.
//
// # Why a checked-in file
//
// Regenerating it needs Python, uharfbuzz and the font (make hbshaping); running
// it needs a Go toolchain. An oracle only consultable on a machine with the
// right Python on it is an oracle nobody consults, and this one has to run on
// every change to the shaper.

const harfbuzzDir = "../testdata/harfbuzz"

// harfbuzzCases are the fonts compared and the corpus each is compared over.
//
// One font cannot cover this. The bundled face has no Arabic and no Khmer in it,
// and those are the two shapers with the most to get wrong — cursive joining
// picks one of four forms for every letter from its neighbours, and the Khmer
// model draws a syllable in an order the characters are not written in. Until
// these two arrived neither had ever been compared against anything outside this
// repository.
//
// The extra fonts are Google's Noto builds under the SIL Open Font License, the
// same licence and the same publisher as the bundled face, with their copyright
// notices beside them as that licence requires. They are test data and are not
// embedded in anything this package ships.
var harfbuzzCases = []struct {
	name string
	// font is a path under harfbuzzDir, or empty for the bundled face.
	font, corpus, expected string
}{
	{"latin", "", "corpus.txt", "expected.txt"},
	{"arabic", "fonts/NotoSansArabic.ttf", "arabic.txt", "arabic.expected.txt"},
	{"khmer", "fonts/NotoSansKhmer.ttf", "khmer.txt", "khmer.expected.txt"},
}

// TestShapingAgreesWithHarfBuzz compares every case in every corpus.
func TestShapingAgreesWithHarfBuzz(t *testing.T) {
	for _, tc := range harfbuzzCases {
		t.Run(tc.name, func(t *testing.T) {
			corpus, expected, header := readHarfBuzzGolden(t, tc.corpus, tc.expected)
			f := harfbuzzFace(t, tc.font, header)

			if len(corpus) != len(expected) {
				t.Fatalf("%d strings in %s and %d lines of expectations", len(corpus), tc.corpus, len(expected))
			}
			if len(corpus) == 0 {
				t.Fatalf("%s is empty, so this proves nothing", tc.corpus)
			}

			known := deliberateDifferences[tc.name]
			unseen := map[string]bool{}
			for s := range known {
				unseen[s] = true
			}
			var differing, expectedDiffs int
			for i, s := range corpus {
				glyphs, _ := f.ShapeGlyphs(s)
				same, why := sameAsHarfBuzz(f, glyphs, expected[i])
				if _, listed := known[s]; listed {
					delete(unseen, s)
					if same {
						t.Errorf("%s is listed as a deliberate difference and now agrees with "+
							"HarfBuzz.\nRemove it: a stale exception is a hole in this test.",
							describeRunes(s))
						continue
					}
					expectedDiffs++
					continue
				}
				if !same {
					differing++
					if differing <= 40 {
						t.Errorf("%s\n  %s\n  pdf0     %s\n  harfbuzz %s",
							describeRunes(s), why, describeGlyphs(glyphs), describeExpected(f, expected[i]))
					}
				}
			}
			if differing > 40 {
				t.Errorf("... and %d more", differing-40)
			}
			for s := range unseen {
				t.Errorf("%s is listed as a deliberate difference but is not in the corpus", describeRunes(s))
			}
			t.Logf("%d of %d agree, %d deliberate differences (harfbuzz %s)",
				len(corpus)-differing-expectedDiffs, len(corpus), expectedDiffs, header["harfbuzz"])
		})
	}
}

// harfbuzzFace loads the face a case is compared over, and refuses to run
// against a font the expectations were not generated from.
//
// That is a hard stop rather than a skip: every expectation is about a glyph
// index, so a different font would leave this asserting yesterday's answers
// about today's glyphs and every one of them would be about the wrong glyph.
func harfbuzzFace(t *testing.T, path string, header map[string]string) *Face {
	t.Helper()
	data := notoSansRegular
	if path != "" {
		var err error
		data, err = os.ReadFile(filepath.Join(harfbuzzDir, path))
		if err != nil {
			t.Fatalf("reading the font: %v\nRun `make hbshaping` after fetching it.", err)
		}
	}
	sum := sha256.Sum256(data)
	if got, want := hex.EncodeToString(sum[:]), header["font-sha256"]; got != want {
		t.Fatalf("the expectations were generated against font %s and this one is %s.\n"+
			"Run `make hbshaping` to regenerate them.", want, got)
	}
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading the font: %v", err)
	}
	return f
}

// deliberateDifferences are the cases where this package answers something other
// than HarfBuzz on purpose, per corpus, each with the reason.
//
// The list is checked in both directions: a case in it that agrees with HarfBuzz
// fails, and so does one that is not in the corpus. An exception that cannot go
// stale is a documented decision; one that can is a hole.
//
// # The Latin entries: one thing, thirteen times
//
// A character nothing is drawn for, written between a consonant and its virama.
//
// This package removes such a character before the shaper sees it, so the
// syllable is whole and the conjunct forms. HarfBuzz keeps it until after
// shaping, so it breaks the syllable, and the orphaned virama gets a dotted
// circle — the placeholder that says "this text is malformed".
//
// Both readings are defensible and they differ only on malformed text. Unicode
// defines the property as characters that "should be ignored in rendering",
// which is what this package does; HarfBuzz gives the syllable model the last
// word. The choice here is the one that puts a well-formed conjunct on the page
// rather than a dotted circle, because a document is written once and read many
// times, and a reader cannot fix the text.
var deliberateDifferences = map[string]map[string]string{
	"latin": {
		"\u0915\u00AD\u094D\u0937":     "soft hyphen inside a Devanagari cluster",
		"\u0915\u034F\u094D\u0937":     "combining grapheme joiner inside a Devanagari cluster",
		"\u0915\u200B\u094D\u0937":     "zero width space inside a Devanagari cluster",
		"\u0915\u200E\u094D\u0937":     "left-to-right mark inside a Devanagari cluster",
		"\u0915\u202C\u094D\u0937":     "pop directional formatting inside a Devanagari cluster",
		"\u0915\u2060\u094D\u0937":     "word joiner inside a Devanagari cluster",
		"\u0915\u2064\u094D\u0937":     "invisible plus inside a Devanagari cluster",
		"\u0915\u2069\u094D\u0937":     "pop directional isolate inside a Devanagari cluster",
		"\u0915\uFE00\u094D\u0937":     "variation selector 1 inside a Devanagari cluster",
		"\u0915\uFE0F\u094D\u0937":     "variation selector 16 inside a Devanagari cluster",
		"\u0915\uFEFF\u094D\u0937":     "byte order mark inside a Devanagari cluster",
		"\u0915\U0001D173\u094D\u0937": "musical begin beam inside a Devanagari cluster",
		"\u0915\U000E0041\u094D\u0937": "tag letter inside a Devanagari cluster",
	},
	"arabic": {},
	"khmer":  {},
}

// TestTheHarfBuzzOracleHasTeeth is the guard on the guard.
//
// A comparison that reads no cases, or compares nothing about them, passes
// exactly as loudly as one that checks five thousand. This shapes a string with
// a deliberately wrong answer and requires the comparison to say so.
func TestTheHarfBuzzOracleHasTeeth(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	glyphs, _ := f.ShapeGlyphs("AV")
	if len(glyphs) != 2 {
		t.Fatalf("the fixture shaped to %d glyphs", len(glyphs))
	}
	right := []hbGlyph{
		{gid: glyphs[0].GID, adv: int(glyphs[0].XAdvance)},
		{gid: glyphs[1].GID, adv: int(glyphs[1].XAdvance)},
	}
	if same, why := sameAsHarfBuzz(f, glyphs, right); !same {
		t.Fatalf("the comparison rejects a correct answer: %s", why)
	}
	for _, tc := range []struct {
		mutate func([]hbGlyph) []hbGlyph
		why    string
	}{
		{func(g []hbGlyph) []hbGlyph { g[0].gid++; return g }, "a different glyph"},
		{func(g []hbGlyph) []hbGlyph { g[0].adv++; return g }, "a different advance"},
		{func(g []hbGlyph) []hbGlyph { g[1].dx = 5; return g }, "a different offset"},
		{func(g []hbGlyph) []hbGlyph { g[1].dy = -5; return g }, "a different vertical offset"},
		{func(g []hbGlyph) []hbGlyph { return g[:1] }, "one glyph too few"},
		{func(g []hbGlyph) []hbGlyph { return append(g, hbGlyph{gid: 1}) }, "one glyph too many"},
	} {
		wrong := append([]hbGlyph(nil), right...)
		if same, _ := sameAsHarfBuzz(f, glyphs, tc.mutate(wrong)); same {
			t.Errorf("the comparison accepts %s", tc.why)
		}
	}
}

// hbGlyph is one glyph as the expectations state it: font units throughout,
// which is what HarfBuzz reports and not what this package does.
type hbGlyph struct {
	gid, adv, dx, dy int
}

// sameAsHarfBuzz compares a shaped run against the expectation, converting the
// expectation into this package's units rather than the other way about — a
// comparison that rounded this package's answer could hide a real difference of
// less than a unit.
func sameAsHarfBuzz(f *Face, got []Glyph, want []hbGlyph) (bool, string) {
	if len(got) != len(want) {
		return false, fmt.Sprintf("%d glyphs, want %d", len(got), len(want))
	}
	for i := range got {
		switch {
		case got[i].GID != want[i].gid:
			return false, fmt.Sprintf("glyph %d is %d, want %d", i, got[i].GID, want[i].gid)
		case got[i].XAdvance != f.scale(want[i].adv):
			return false, fmt.Sprintf("glyph %d advances %v, want %v",
				i, got[i].XAdvance, f.scale(want[i].adv))
		case got[i].XOffset != f.scale(want[i].dx) || got[i].YOffset != f.scale(want[i].dy):
			return false, fmt.Sprintf("glyph %d is placed at (%v, %v), want (%v, %v)",
				i, got[i].XOffset, got[i].YOffset, f.scale(want[i].dx), f.scale(want[i].dy))
		}
	}
	return true, ""
}

// readHarfBuzzGolden reads the corpus and the expectations, together with the
// header that says what produced them.
func readHarfBuzzGolden(t *testing.T, corpusName, expectedName string) (corpus []string, expected [][]hbGlyph, header map[string]string) {
	t.Helper()
	corpus = readNonEmptyLines(t, filepath.Join(harfbuzzDir, corpusName))

	path := filepath.Join(harfbuzzDir, expectedName)
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nRun `make hbshaping` to generate it.", path, err)
	}
	defer file.Close()

	header = map[string]string{}
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for line := 0; sc.Scan(); line++ {
		text := sc.Text()
		if strings.HasPrefix(text, "#") {
			if key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(text, "#")), " "); ok {
				header[key] = value
			}
			continue
		}
		glyphs, err := parseExpectedGlyphs(text)
		if err != nil {
			t.Fatalf("%s:%d: %v", path, line+1, err)
		}
		expected = append(expected, glyphs)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if header["font-sha256"] == "" {
		t.Fatalf("%s has no font-sha256 line, so there is nothing tying it to a font", path)
	}
	return corpus, expected, header
}

func parseExpectedGlyphs(line string) ([]hbGlyph, error) {
	if strings.TrimSpace(line) == "" {
		return nil, nil
	}
	var out []hbGlyph
	for _, field := range strings.Fields(line) {
		parts := strings.Split(field, ",")
		if len(parts) != 2 && len(parts) != 4 {
			return nil, fmt.Errorf("%q has %d parts, want 2 or 4", field, len(parts))
		}
		nums := make([]int, len(parts))
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("%q: %v", field, err)
			}
			nums[i] = n
		}
		g := hbGlyph{gid: nums[0], adv: nums[1]}
		if len(nums) == 4 {
			g.dx, g.dy = nums[2], nums[3]
		}
		out = append(out, g)
	}
	return out, nil
}

func readNonEmptyLines(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	defer file.Close()
	var out []string
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return out
}

// describeRunes names a string by its code points as well as its text, because
// most of the corpus is invisible in a terminal and several cases differ only by
// a character that has no shape.
func describeRunes(s string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q [", s)
	for i, r := range []rune(s) {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "U+%04X", r)
	}
	b.WriteByte(']')
	return b.String()
}

func describeGlyphs(glyphs []Glyph) string {
	var parts []string
	for _, g := range glyphs {
		if g.XOffset != 0 || g.YOffset != 0 {
			parts = append(parts, fmt.Sprintf("%d,%v@%v,%v", g.GID, g.XAdvance, g.XOffset, g.YOffset))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d,%v", g.GID, g.XAdvance))
	}
	return strings.Join(parts, " ")
}

func describeExpected(f *Face, glyphs []hbGlyph) string {
	var parts []string
	for _, g := range glyphs {
		if g.dx != 0 || g.dy != 0 {
			parts = append(parts, fmt.Sprintf("%d,%v@%v,%v", g.gid, f.scale(g.adv), f.scale(g.dx), f.scale(g.dy)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d,%v", g.gid, f.scale(g.adv)))
	}
	return strings.Join(parts, " ")
}
