package fonts

import (
	"testing"

	"github.com/mgilbir/forme/fonttest"
)

// The two questions asked of a font program when the face was adopted rather
// than loaded here: is it CID-keyed, and does it say which collection its CIDs
// belong to.
//
// These are tested against the program directly because the path that uses
// them cannot be reached with any fixture available. It needs a font that is
// adopted, CID-keyed, unable to name its collection, *and* subsettable — and
// fonttest's CID-keyed CFF writes no FDSelect, so the subsetter refuses it
// before the question is asked. The corpus's Adobe-Japan1 font subsets and does
// name its collection. Writing a CID-keyed CFF with a valid FDSelect from
// scratch is a font compiler.
//
// So the predicates are pinned here and the wiring above them is pinned by
// TestAnAdoptedCIDFaceGetsItsOwnCollection, which exercises the same two calls
// on a font that can be subsetted. What is not covered is the refusal for an
// adopted font that is CID-keyed and unnameable, and that is stated rather than
// implied.

func cidProgram(t *testing.T, opts fonttest.CFFOptions) []byte {
	t.Helper()
	glyphs := make([]fonttest.Glyph, 0, opts.Glyphs-1)
	for i := 0; i < opts.Glyphs-1; i++ {
		glyphs = append(glyphs, fonttest.Glyph{Rune: rune('A' + i), Advance: 500, HasShape: true})
	}
	return fonttest.OTTO(fonttest.CFF(opts), fonttest.SFNTOptions{Name: "T", Glyphs: glyphs})
}

// TestProgramIsCIDKeyed is the question that decides whether a missing
// collection is a refusal or a font that simply has none.
func TestProgramIsCIDKeyed(t *testing.T) {
	cid := cidProgram(t, fonttest.CFFOptions{Glyphs: 4, CIDKeyed: true})
	if !programIsCIDKeyed(cid) {
		t.Error("a CID-keyed CFF was not recognised as one")
	}
	plain := cidProgram(t, fonttest.CFFOptions{Glyphs: 4})
	if programIsCIDKeyed(plain) {
		t.Error("a CFF that is not CID-keyed was called one, which would refuse " +
			"every font whose glyph names it could not read")
	}
	// A font with no CFF at all — every TrueType — is not CID-keyed, and asking
	// must not panic on the missing table.
	if programIsCIDKeyed(nil) {
		t.Error("nil was called CID-keyed")
	}
	if programIsCIDKeyed([]byte("not a font")) {
		t.Error("nonsense was called CID-keyed")
	}
}

// TestCollectionOfProgram is the other half: what to write, and whether there
// is anything to write.
func TestCollectionOfProgram(t *testing.T) {
	got := func(opts fonttest.CFFOptions) (string, string, int, bool) {
		return collectionOfProgram(cidProgram(t, opts))
	}

	r, o, sup, ok := got(fonttest.CFFOptions{
		Glyphs: 4, CIDKeyed: true, Registry: "Adobe", Ordering: "Japan1", Supplement: 6,
	})
	if !ok || r != "Adobe" || o != "Japan1" || sup != 6 {
		t.Errorf("a named collection read as %q-%q-%d (ok=%v), want Adobe-Japan1-6",
			r, o, sup, ok)
	}

	// The two malformations that parse, which is what makes them worth a check.
	// Both must answer "no collection" rather than half of one: a caller that
	// got Registry and no Ordering would write a dictionary naming neither.
	for _, c := range []struct {
		name string
		opts fonttest.CFFOptions
	}{
		{"a ROS naming strings the font does not carry",
			fonttest.CFFOptions{Glyphs: 4, CIDKeyed: true, UnnamedCollection: true}},
		{"a supplement below zero",
			fonttest.CFFOptions{Glyphs: 4, CIDKeyed: true, NegativeSupplement: true}},
	} {
		if r, o, sup, ok := got(c.opts); ok {
			t.Errorf("%s reported the collection %q-%q-%d; it has none to report",
				c.name, r, o, sup)
		}
	}

	// And a font that is not CID-keyed has no collection either — the caller
	// writes Adobe-Identity-0 for it, which is a different decision and must
	// not come from here.
	if _, _, _, ok := got(fonttest.CFFOptions{Glyphs: 4}); ok {
		t.Error("a CFF that is not CID-keyed reported a collection")
	}
	if _, _, _, ok := collectionOfProgram(nil); ok {
		t.Error("nil reported a collection")
	}
}
