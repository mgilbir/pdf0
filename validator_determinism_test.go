package pdf0

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"strings"
	"testing"
)

// A validator report must be a function of the file alone. Several rules quote
// one representative example — a glyph name, an object number — chosen out of a
// Go map, and Go randomises map iteration order on every range statement in
// every run. Picking "whichever the range yielded first" therefore produced a
// different message for the same input from one run to the next, which shows up
// as spurious churn whenever two reports are diffed.
//
// Validating once proves nothing here: a single run has nothing to disagree
// with. The tests below validate the same document many times inside one
// process — Go reseeds map iteration per range statement, so repeated iteration
// within a single run does vary — and require every run to agree exactly.

// determinismReps is the number of times a fixture is validated. See
// TestCharSetGlyphChoiceIsDeterministic for the false-pass arithmetic.
const determinismReps = 64

// charSetDeterminismGlyphs is the number of colliding candidates each direction
// of the /CharSet rule is given. Both must be large enough that an unfixed,
// map-order-driven choice is overwhelmingly unlikely to agree across all
// determinismReps runs.
const charSetDeterminismGlyphs = 64

// charSetDeterminismDoc builds a document whose single rendered, subset Type 1
// font breaches BOTH directions of clause 7.21.4.2 many times over: the
// embedded program defines nGlyphs glyphs that /CharSet does not list, and
// /CharSet lists nGlyphs glyphs the program does not define. Every one of those
// is an equally valid example for the rule to quote, so an implementation that
// quotes whichever the map handed back first has nGlyphs answers to choose
// between in each direction.
func charSetDeterminismDoc(nGlyphs int) *Document {
	present := make([]string, 0, nGlyphs)
	var listed strings.Builder
	for i := 0; i < nGlyphs; i++ {
		// Fixed-width names so lexicographic order is unambiguous.
		present = append(present, fmt.Sprintf("inprog%03d", i))
		fmt.Fprintf(&listed, "/incharset%03d", i)
	}

	doc := &Document{Objects: map[int]*IndirectObject{}}
	put := func(num int, v Object) { doc.Objects[num] = &IndirectObject{Number: num, Value: v} }

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	put(1, cat)

	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	put(2, pages)

	fontRes := &Dictionary{}
	fontRes.Set("F1", IndirectRef{Number: 4})
	res := &Dictionary{}
	res.Set("Font", fontRes)
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	page.Set("Resources", res)
	page.Set("Contents", IndirectRef{Number: 5})
	put(3, page)

	font := &Dictionary{}
	font.Set("Type", Name("Font"))
	font.Set("Subtype", Name("Type1"))
	font.Set("BaseFont", Name("ABCDEF+Determinism"))
	font.Set("FontDescriptor", IndirectRef{Number: 6})
	put(4, font)

	put(5, &Stream{Data: []byte("BT /F1 12 Tf (A) Tj ET\n")})

	fd := &Dictionary{}
	fd.Set("CharSet", String{Value: []byte(listed.String())})
	fd.Set("FontFile", IndirectRef{Number: 7})
	put(6, fd)

	put(7, &Stream{Data: fonttest.Type1Program(present)})

	doc.Trailer.Set("Root", IndirectRef{Number: 1})
	return doc
}

// charSetFindings returns the clause 7.21.4.2 findings of one validation run.
func charSetFindings(doc *Document) []string {
	var out []string
	for _, v := range ValidatePDFUA(doc) {
		if v.Clause == "7.21.4.2" {
			out = append(out, v.Error())
		}
	}
	return out
}

// TestCharSetGlyphChoiceIsDeterministic pins the example glyph the /CharSet rule
// quotes. Before the fix the rule broke out of a range over fp.glyphNames (and
// over the parsed /CharSet) at the first offender, so the glyph named was
// whatever Go's randomised map iteration produced.
//
// False-pass arithmetic. Each direction has charSetDeterminismGlyphs = 64
// equally eligible offenders, and an unfixed implementation names essentially
// one drawn at random per run. The test passes spuriously only if all
// determinismReps = 64 runs happen to draw the same glyph, in both directions.
// For one direction under a uniform draw that is
//
//	sum over the 64 glyphs of (1/64)^64  =  64 * 64^-64  =  64^-63  ~=  1e-114.
//
// Even under a pessimistic non-uniform model where some single glyph is drawn
// half the time, it is 0.5^63 ~= 1e-19. Both are far below the 1e-6 target, and
// the two directions must agree simultaneously.
func TestCharSetGlyphChoiceIsDeterministic(t *testing.T) {
	doc := charSetDeterminismDoc(charSetDeterminismGlyphs)

	want := charSetFindings(doc)
	if len(want) != 2 {
		t.Fatalf("fixture should breach both directions of 7.21.4.2 exactly once each; got %d findings: %v", len(want), want)
	}
	// The defined choice is the lexicographically smallest offender, which for
	// this fixture is the ...000 name in each direction. Pinning the value —
	// not merely its stability — is what stops a future "fix" that simply
	// reports nothing, or that picks a different-but-also-stable element.
	joined := strings.Join(want, "\n")
	for _, wantGlyph := range []string{"glyph inprog000 present", "glyph incharset000 that is not present"} {
		if !strings.Contains(joined, wantGlyph) {
			t.Errorf("expected the lexicographically smallest offender (%q) to be named; got:\n%s", wantGlyph, joined)
		}
	}

	for i := 1; i < determinismReps; i++ {
		got := charSetFindings(doc)
		if len(got) != len(want) {
			t.Fatalf("run %d reported %d findings, run 0 reported %d", i, len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("run %d disagrees with run 0 on finding %d:\n run 0: %s\n run %d: %s", i, j, want[j], i, got[j])
			}
		}
	}
}

// TestValidatePDFUAOutputIsStableAcrossRuns is the whole-report version: not one
// rule's example but every finding ValidatePDFUA makes, in order, over a
// document that also trips the catalog, metadata and structure rules. It guards
// against a new rule reintroducing a map-order-dependent message.
func TestValidatePDFUAOutputIsStableAcrossRuns(t *testing.T) {
	doc := charSetDeterminismDoc(charSetDeterminismGlyphs)

	render := func() string {
		var b strings.Builder
		for _, v := range ValidatePDFUA(doc) {
			b.WriteString(v.Error())
			b.WriteByte('\n')
		}
		return b.String()
	}

	want := render()
	if !strings.Contains(want, "7.21.4.2") {
		t.Fatalf("fixture no longer trips the /CharSet rule:\n%s", want)
	}
	for i := 1; i < determinismReps; i++ {
		if got := render(); got != want {
			t.Fatalf("ValidatePDFUA run %d differs from run 0.\n--- run 0 ---\n%s--- run %d ---\n%s", i, want, i, got)
		}
	}
}
