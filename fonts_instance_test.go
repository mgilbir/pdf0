package pdf0

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// What a document embeds for a variable font is the instance it draws.
//
// forme sets a variable face where CSS places it and hands a backend the
// instance (shape.LoadInstance): a static face whose outlines, advances and
// metrics are the ones at that point of the design space, with its own
// program. A document that drew the instance and embedded the default master
// — the outlines a variable font stores, and what a reader that ignores the
// variation tables draws — would show a regular weight where the page was
// laid out bold, with /W widths the glyphs do not have.
//
// The oracle is outside both modules: testdata/fontinstance/oracle.txt, what
// HarfBuzz and fontTools' instancer say each glyph of the instance advances
// and inks, written by oracle.py beside it. The two agree on every line (the
// script refuses a line they disagree on), so each number is one neither this
// module nor forme chose.

// instanceGlyph is one line of the oracle.
type instanceGlyph struct {
	r       rune
	gid     int
	advance float64
	// ink is HarfBuzz's extents at the location: x bearing, y bearing,
	// width and height, in font units, y up; all zero for a glyph with none.
	ink [4]int
	// bounds is the outline's box as fontTools draws the instance: xMin,
	// yMin, xMax, yMax.
	bounds [4]int
}

// instanceCase is one font at one location, and the glyphs the oracle states
// for it.
type instanceCase struct {
	font, file string
	location   map[string]float64
	locText    string
	glyphs     []instanceGlyph
}

// instanceFonts are the oracle's fonts, in the forme module this build uses.
var instanceFonts = map[string]string{
	"notosans":  "fonts/notosans/NotoSans-Variable.ttf",
	"cff2blend": "testdata/harfbuzz/fonts/CFF2Blend.otf",
}

// instanceOracle reads testdata/fontinstance/oracle.txt.
func instanceOracle(t *testing.T) []instanceCase {
	t.Helper()
	f, err := os.Open(testfiles.Committed(t, "testdata/fontinstance/oracle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []instanceCase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) != 14 {
			t.Fatalf("oracle line %q has %d fields, want 14", line, len(fs))
		}
		n := make([]int, 0, 11)
		for _, s := range fs[3:] {
			v, err := strconv.Atoi(s)
			if err != nil {
				t.Fatalf("oracle line %q: %v", line, err)
			}
			n = append(n, v)
		}
		cp, err := strconv.ParseInt(strings.TrimPrefix(fs[2], "U+"), 16, 32)
		if err != nil {
			t.Fatalf("oracle line %q: %v", line, err)
		}
		if n[1] != n[2] {
			t.Fatalf("oracle line %q: HarfBuzz and fontTools disagree on the advance", line)
		}
		g := instanceGlyph{
			r: rune(cp), gid: n[0], advance: float64(n[1]),
			ink:    [4]int{n[3], n[4], n[5], n[6]},
			bounds: [4]int{n[7], n[8], n[9], n[10]},
		}
		if k := len(out); k == 0 || out[k-1].font != fs[0] || out[k-1].locText != fs[1] {
			file, ok := instanceFonts[fs[0]]
			if !ok {
				t.Fatalf("oracle line %q names a font this test does not know", line)
			}
			loc := map[string]float64{}
			for _, kv := range strings.Split(fs[1], ",") {
				tag, v, _ := strings.Cut(kv, "=")
				x, err := strconv.ParseFloat(v, 64)
				if err != nil {
					t.Fatalf("oracle line %q: location %v", line, err)
				}
				loc[tag] = x
			}
			out = append(out, instanceCase{font: fs[0], file: file, location: loc, locText: fs[1]})
		}
		out[len(out)-1].glyphs = append(out[len(out)-1].glyphs, g)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("the oracle states nothing")
	}
	return out
}

// formeFile reads a file of the forme module this build uses.
func formeFile(t *testing.T, rel string) []byte {
	t.Helper()
	dir, err := formeDir()
	if err != nil || dir == "" {
		t.Fatalf("locating the forme module: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading forme's %s: %v", rel, err)
	}
	return data
}

func (c instanceCase) text() string {
	var b strings.Builder
	for _, g := range c.glyphs {
		b.WriteRune(g.r)
	}
	return b.String()
}

// TestAnInstanceIsEmbeddedAsItIsDrawn draws each oracle location's text in
// the instance forme makes for it, writes the document at every PDF/A level,
// and reads back what a reader gets: the /W width of every glyph drawn, the
// embedded program's own advance and ink for it, the font file's kind, and
// this module's PDF/A validator on the whole. Each must be the instance's, as
// HarfBuzz and fontTools state it.
//
// The CFF2 font is embedded as the CFF it draws (forme 6f4fa65): FontFile3
// with /Subtype /OpenType, an sfnt whose outlines are a 'CFF ' table and which
// carries no CFF2, which PDF has no font file type for (ISO 32000-2 9.9).
func TestAnInstanceIsEmbeddedAsItIsDrawn(t *testing.T) {
	levels := []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4}
	var draw drawPath
	for _, p := range drawPaths {
		if p.name == "Draw" {
			draw = p
		}
	}
	for _, c := range instanceOracle(t) {
		t.Run(c.font+"@"+c.locText, func(t *testing.T) {
			data := formeFile(t, c.file)
			inst, err := shape.LoadInstance(data, c.location)
			if err != nil {
				t.Fatalf("instancing: %v", err)
			}
			master, err := shape.Load(data)
			if err != nil {
				t.Fatalf("loading the default master: %v", err)
			}
			// The case has to be able to tell the instance from the default
			// master, or it asserts nothing about which was embedded.
			differs := false
			for _, g := range c.glyphs {
				xb, yb, w, h, _ := master.GlyphExtents(g.gid)
				if master.GlyphAdvance(g.gid) != g.advance || [4]int{xb, yb, w, h} != g.ink {
					differs = true
				}
			}
			if !differs {
				t.Fatal("the default master draws every glyph as the instance does; the case cannot tell them apart")
			}
			text := c.text()
			for _, level := range levels {
				face := fonts.Adopt(inst.Clone())
				back, _ := drawnDocument(t, face, draw, text, level, true)
				for _, v := range ValidatePDFA(back, level) {
					t.Errorf("%s: %s", level, v.Error())
				}
				if got := strings.TrimSpace(mustExtractText(t, back)); got != text {
					t.Errorf("%s: extracted %q, want %q", level, got, text)
				}
				checkEmbeddedInstance(t, back, inst, c)
			}
		})
	}
}

// checkEmbeddedInstance holds the font on the document's page to the oracle.
func checkEmbeddedInstance(t *testing.T, doc *Document, inst *shape.Face, c instanceCase) {
	t.Helper()
	res := doc.ResolveDict(doc.PageList()[0].Get("Resources"))
	top := doc.ResolveDict(res.Get("Font")).Get("F1")
	cid := descendantOf(t, doc, top)
	desc := doc.ResolveDict(cid.Get("FontDescriptor"))
	cff := inst.IsCFF()
	key := object.Name("FontFile2")
	if cff {
		key = "FontFile3"
	}
	st, ok := doc.Resolve(desc.Get(key)).(*object.Stream)
	if !ok {
		t.Fatalf("the descriptor has no %s", key)
	}
	program, err := doc.StreamData(st)
	if err != nil {
		t.Fatal(err)
	}
	if cff {
		if sub := st.Dict.Get("Subtype"); sub != object.Name("OpenType") {
			t.Errorf("the CFF program's /Subtype is %v, want /OpenType", sub)
		}
		if wantSub := object.Name("CIDFontType0"); cid.Get("Subtype") != wantSub {
			t.Errorf("the CIDFont is %v, want %v", cid.Get("Subtype"), wantSub)
		}
		tables := font.SFNTTables(program)
		if string(program[:4]) != "OTTO" || tables["CFF "] == nil || tables["CFF2"] != nil {
			t.Errorf("the embedded program is not an OpenType font with CFF outlines and no CFF2 (tag %q)", program[:4])
		}
	}
	emb, err := shape.Load(program)
	if err != nil {
		t.Fatalf("forme cannot read the embedded program: %v", err)
	}
	// The embedded program addresses a glyph by CID in a CID-keyed CFF
	// (renumbered in the subset, each keeping its original index as its CID)
	// and by its original index in a TrueType subset.
	byCode := map[int]int{}
	for g := 0; g < emb.NumGlyphs(); g++ {
		byCode[emb.GlyphCode(g)] = g
	}
	dw := 1000.0
	switch v := doc.Resolve(cid.Get("DW")).(type) {
	case object.Integer:
		dw = float64(v)
	case object.Real:
		dw = float64(v)
	}
	bits := func() []byte {
		st, ok := doc.Resolve(desc.Get("CIDSet")).(*object.Stream)
		if !ok {
			t.Fatal("the descriptor has no /CIDSet")
		}
		b, err := doc.StreamData(st)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}()
	for _, g := range c.glyphs {
		code := inst.GlyphCode(g.gid)
		w, ok := widthOfCID(t, doc, cid, code)
		if !ok {
			w = dw
		}
		if w != g.advance {
			t.Errorf("%q: /W says %v; the instance advances %v", g.r, w, g.advance)
		}
		if !bitSet(bits, code) {
			t.Errorf("%q: /CIDSet does not list CID %d, which the page shows", g.r, code)
		}
		eg, ok := byCode[code]
		if !ok {
			t.Errorf("%q: the embedded program has no glyph for code %d", g.r, code)
			continue
		}
		if a := emb.GlyphAdvance(eg); a != g.advance {
			t.Errorf("%q: the embedded program advances %v; the instance %v", g.r, a, g.advance)
		}
		xb, yb, wd, h, _ := emb.GlyphExtents(eg)
		got := [4]int{xb, yb, wd, h}
		if !within(got, g.ink, 1) {
			t.Errorf("%q: the embedded program inks %v; HarfBuzz inks the instance's glyph %v", g.r, got, g.ink)
		}
		box := [4]int{xb, yb + h, xb + wd, yb}
		if (wd != 0 || h != 0) && !within(box, g.bounds, 1) {
			t.Errorf("%q: the embedded outline's box is %v; fontTools' instance has %v", g.r, box, g.bounds)
		}
	}
}

// within reports whether two boxes agree to a font unit: HarfBuzz rounds a
// blended CFF2 outline's extents outwards where fontTools rounds the points.
func within(a, b [4]int, tol int) bool {
	for i := range a {
		if d := a[i] - b[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

// TestTheInstanceOracleCoversWhatItClaims keeps the oracle honest about its
// own reach: both fonts, two locations each, and every glyph of each text.
func TestTheInstanceOracleCoversWhatItClaims(t *testing.T) {
	seen := map[string][]string{}
	for _, c := range instanceOracle(t) {
		seen[c.font] = append(seen[c.font], c.locText)
		if len(c.glyphs) < 10 {
			t.Errorf("%s@%s states %d glyphs", c.font, c.locText, len(c.glyphs))
		}
	}
	var fonts []string
	for f, locs := range seen {
		fonts = append(fonts, f)
		if len(locs) != 2 {
			t.Errorf("%s is stated at %v", f, locs)
		}
	}
	sort.Strings(fonts)
	if strings.Join(fonts, ",") != "cff2blend,notosans" {
		t.Errorf("the oracle states %v", fonts)
	}
}
