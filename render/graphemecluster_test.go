package render

import (
	"sort"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
)

// Why word-break and overflow-wrap are refused, checked rather than asserted.
//
// Both properties ask for a break *between two characters*, and CSS Text is
// exact about which positions those are: §2 defines the typographic character
// unit as the (tailored) grapheme cluster, and a soft wrap opportunity may only
// fall between two of them. Breaking inside one splits a letter from its accent,
// a flag from its other half, or a syllable from its final jamo — a failure a
// reader sees at once, and a worse outcome than the honest refusal.
//
// The obvious source of those positions is the shaper: forme returns a
// Glyph.Cluster per glyph, "the byte offset of the first character this glyph
// came from", and a change in it looks like a boundary. It is not one. A shaping
// cluster and a grapheme cluster answer different questions — one is "what did
// the font draw together", the other is "what does a reader treat as a
// character" — and this file is the measurement that settles it, because the
// question is about a dependency's behaviour and not about a specification.
//
// Each case below is a string whose grapheme cluster segmentation UAX #29 states
// outright, shaped through the bundled face. The clusters that come back are
// finer than the grapheme clusters in every one of them, so a break taken at a
// cluster change would land inside a grapheme cluster.
//
// # What would be needed instead
//
// A UAX #29 extended grapheme cluster segmenter over the text — which is where
// the question belongs anyway, since splitAtBreaks works on characters and not
// on glyphs. That needs the Grapheme_Cluster_Break property, Extended_Pictographic
// for GB11, and Indic_Conjunct_Break for GB9c. Go's unicode package ships none of
// the three: it has general categories and scripts, which are a different
// classification — unicode.M is close to Extend and is not it, and there is no
// stdlib spelling of Regional_Indicator, Prepend or the Hangul jamo classes at
// all. So it is a generated table, and it belongs in forme beside the canonical
// and Indic tables that are already generated from the UCD, rather than in a
// renderer.
//
// # If this test fails
//
// It is a tripwire, not a regression. A failure means forme's clusters have
// moved, and the refusal above should be re-derived rather than the expectation
// here edited to match.

// shapedClusterBoundaries is the set of byte offsets a break "where the cluster
// changes" could fall at: every distinct Glyph.Cluster in the shaped output.
//
// They are gathered as a set and sorted rather than read off in glyph order,
// which is itself part of the finding: in a right-to-left run the glyphs come
// back in the order they are drawn, so the clusters run backwards — see
// TestShapingClustersAreNotOrderedInARightToLeftRun.
func shapedClusterBoundaries(t *testing.T, text string) []int {
	t.Helper()
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatalf("loading the bundled face: %v", err)
	}
	runs, _ := fonts.NewStack(face).ShapeRuns(text)
	seen := map[int]bool{}
	for _, r := range runs {
		for _, g := range r.Glyphs {
			seen[g.Cluster] = true
		}
	}
	out := make([]int, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}

func TestShapingClustersAreFinerThanGraphemeClusters(t *testing.T) {
	cases := []struct {
		name string
		text string
		// clusters is what the shaper returns, and is checked live.
		clusters []int
		// graphemes is where UAX #29 puts the boundaries of this string, as
		// byte offsets of the start of each cluster. It is a literal because it
		// is the specification's answer and not this engine's.
		graphemes []int
		// rule names the UAX #29 rule that forbids the break the shaper's extra
		// boundary would have allowed.
		rule string
	}{{
		// A base with two combining marks. The first composes into the base and
		// the second cannot, so the shaper draws two glyphs and gives the second
		// the offset of the mark it came from — a boundary between a letter and
		// its own accent.
		name: "base with two combining marks", text: "á̈b",
		clusters: []int{0, 3, 5}, graphemes: []int{0, 5},
		rule: "GB9, × Extend",
	}, {
		// A digit and a combining enclosing keycap, which is what a keycap emoji
		// is made of. U+20E3 is Grapheme_Extend.
		name: "combining enclosing keycap", text: "1⃣",
		clusters: []int{0, 1}, graphemes: []int{0},
		rule: "GB9, × Extend",
	}, {
		// Hangul written as conjoining jamo rather than as a precomposed
		// syllable: lead, vowel and trail are one grapheme cluster and three
		// characters, and the shaper gives each its own offset.
		name: "conjoining Hangul jamo", text: "각",
		clusters: []int{0, 3, 6}, graphemes: []int{0},
		rule: "GB6 and GB7, L × V and V × T",
	}, {
		// A flag: two regional indicator symbols, one grapheme cluster. Nothing
		// in shaping pairs them, so a break between them turns a flag into two
		// letters in boxes.
		name: "regional indicator pair", text: "\U0001F1EF\U0001F1F5",
		clusters: []int{0, 4}, graphemes: []int{0},
		rule: "GB12 and GB13, the regional indicator pair",
	}, {
		// U+0E33 THAI CHARACTER SARA AM is GCB=SpacingMark: it takes width of
		// its own, so it is not a combining mark to a shaper, and it is still
		// part of the cluster its consonant begins.
		name: "Thai spacing mark", text: "กำ",
		clusters: []int{0, 3}, graphemes: []int{0},
		rule: "GB9a, × SpacingMark",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shapedClusterBoundaries(t, c.text)
			if !sameInts(got, c.clusters) {
				t.Fatalf("the shaper returned clusters %v for %+q, not %v — forme's "+
					"clustering has changed, so re-derive whether a cluster change is "+
					"a safe break position rather than editing this expectation",
					got, c.text, c.clusters)
			}
			if sameInts(got, c.graphemes) {
				t.Fatalf("clusters %v now match the grapheme clusters %v for %+q; "+
					"the refusal in inline.go rests on their not matching",
					got, c.graphemes, c.text)
			}
			// And the extra boundaries are inside a grapheme cluster, which is
			// the thing that would be broken. Naming them makes the failure
			// message say which character would have been stranded.
			for _, at := range got {
				if !containsInt(c.graphemes, at) {
					t.Logf("a break at byte %d of %+q is inside a grapheme cluster "+
						"(%s)", at, c.text, c.rule)
				}
			}
		})
	}
}

// TestShapingClustersAreNotOrderedInARightToLeftRun is the second half of the
// finding, and it is structural rather than about any one string.
//
// A run comes back in the order its glyphs are *drawn*. In a right-to-left run
// that is the reverse of the order the characters were written, so the clusters
// descend — and "the cluster changed between glyph i and glyph i+1" does not
// name a position in the text at all, let alone a safe one. Arabic makes it
// plainer still: a base and its marks are reordered among themselves, so two
// adjacent glyphs can carry clusters that are neither ascending nor descending.
func TestShapingClustersAreNotOrderedInARightToLeftRun(t *testing.T) {
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatalf("loading the bundled face: %v", err)
	}
	// Arabic lam followed by alef, which the font draws as one ligature or as
	// two joined letters; either way the second character is drawn first.
	runs, _ := fonts.NewStack(face).ShapeRuns("لا")
	var clusters []int
	for _, r := range runs {
		for _, g := range r.Glyphs {
			clusters = append(clusters, g.Cluster)
		}
	}
	if len(clusters) < 2 {
		t.Skipf("the bundled face drew %+q as one glyph, so there is no pair to "+
			"order; the point stands but this string cannot show it", "لا")
	}
	ascending := true
	for i := 1; i < len(clusters); i++ {
		if clusters[i] < clusters[i-1] {
			ascending = false
		}
	}
	if ascending {
		t.Errorf("the clusters of a right-to-left run came back ascending (%v); "+
			"they are in drawing order, and inline.go's refusal cites that they "+
			"are not a walk through the text", clusters)
	}
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
