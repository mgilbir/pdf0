package pdfa

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// type3WidthDoc is a PDF/A-2b page showing one glyph of a Type 3 font whose
// FontMatrix is fm, whose /Widths entry for the glyph is width and whose
// CharProc starts "advance 0 d0".
func type3WidthDoc(t *testing.T, fm object.Array, width, advance float64) core.View {
	t.Helper()
	doc := mkPDFAViewT(t, PDFA2b)
	page := addTestPage(doc)
	cp := []byte(fmt.Sprintf("%g 0 d0 0 0 m 10 0 l 0 10 l f", advance))
	doc.Objects[30] = &object.IndirectObject{Number: 30, Value: object.NewStream(&object.Dictionary{}, cp)}
	font := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type3")},
		object.Entry{Key: "FontBBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
		object.Entry{Key: "FontMatrix", Value: fm},
		object.Entry{Key: "CharProcs", Value: dictWith("A", object.IndirectRef{Number: 30})},
		object.Entry{Key: "Encoding", Value: object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Encoding")},
			object.Entry{Key: "Differences", Value: object.Array{object.Integer(65), object.Name("A")}},
		)},
		object.Entry{Key: "FirstChar", Value: object.Integer(65)},
		object.Entry{Key: "LastChar", Value: object.Integer(65)},
		object.Entry{Key: "Widths", Value: object.Array{object.Real(width)}},
		object.Entry{Key: "Resources", Value: &object.Dictionary{}},
	)
	doc.Objects[31] = &object.IndirectObject{Number: 31, Value: font}
	content := []byte("BT /T3 12 Tf 10 10 Td (A) Tj ET")
	doc.Objects[32] = &object.IndirectObject{Number: 32, Value: object.NewStream(&object.Dictionary{}, content)}
	page.Set("Resources", dictWith("Font", dictWith("T3", object.IndirectRef{Number: 31})))
	page.Set("Contents", object.IndirectRef{Number: 32})
	return doc
}

func type3WidthFindings(vs []Violation) []Violation {
	var out []Violation
	for _, v := range vs {
		if strings.Contains(v.Message, "inconsistent in Type3 font") {
			out = append(out, v)
		}
	}
	return out
}

// TestType3WidthsAreComparedInGlyphSpace: a Type 3 font's /Widths are in glyph
// space (ISO 32000-1 Table 112), as is the advance of its d0/d1 operator, so
// the two are compared as they are written. The check used to scale the
// advance by the FontMatrix into thousandths of text space and compare that
// with the glyph-space /Widths, which agreed only when the FontMatrix scale was
// 0.001: a matplotlib-style [1/2048 0 0 1/2048 0 0] font with matching widths
// was reported, and a rotated FontMatrix (a = 0) always was (audit 2026-09-22
// C67).
func TestType3WidthsAreComparedInGlyphSpace(t *testing.T) {
	scale := func(a, b, c, d float64) object.Array {
		return object.Array{object.Real(a), object.Real(b), object.Real(c), object.Real(d), object.Integer(0), object.Integer(0)}
	}
	cases := []struct {
		name            string
		fm              object.Array
		width, advance  float64
		wantInconsisent bool
	}{
		{"FontMatrix 0.001, agreeing", scale(0.001, 0, 0, 0.001), 600, 600, false},
		{"FontMatrix 1/2048, agreeing", scale(1.0/2048, 0, 0, 1.0/2048), 1200, 1200, false},
		{"FontMatrix rotated 90 degrees, agreeing", scale(0, 0.001, -0.001, 0), 500, 500, false},
		{"FontMatrix 1, agreeing", scale(1, 0, 0, 1), 0.6, 0.6, false},
		// The disagreeing controls: each is off by more than a thousandth of
		// text space in the advance, whatever the scale.
		{"FontMatrix 0.001, disagreeing", scale(0.001, 0, 0, 0.001), 600, 700, true},
		{"FontMatrix 1/2048, disagreeing", scale(1.0/2048, 0, 0, 1.0/2048), 1200, 1300, true},
		{"FontMatrix rotated 90 degrees, disagreeing", scale(0, 0.001, -0.001, 0), 500, 600, true},
		// A tenth of a glyph unit is a hundred thousandths of text space at
		// scale 1: well past the tolerance, though the numbers differ by
		// less than one.
		{"FontMatrix 1, disagreeing by 0.1", scale(1, 0, 0, 1), 0.6, 0.5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := type3WidthDoc(t, c.fm, c.width, c.advance)
			got := type3WidthFindings(validateView(doc, PDFA2b))
			if c.wantInconsisent && len(got) != 1 {
				t.Errorf("want one Type 3 width finding, got %v", got)
			}
			if !c.wantInconsisent && len(got) != 0 {
				t.Errorf("want no Type 3 width finding, got %v", got)
			}
		})
	}
}
