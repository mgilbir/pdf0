package pdfua

import (
	"encoding/binary"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// cidSetFont is a subset CIDFontType2 over a three-glyph TrueType program —
// glyphs 1 and 3 have outlines, glyph 2 is blank — whose CIDToGIDMap is cgm
// and whose FontDescriptor's /CIDSet lists exactly the CIDs in listed.
func cidSetFont(t *testing.T, cgm object.Object, listed ...int) (core.View, *object.Dictionary) {
	t.Helper()
	program := fonttest.SFNT(fonttest.SFNTOptions{Glyphs: []fonttest.Glyph{
		{Rune: 'A', Advance: 500, HasShape: true},
		{Rune: ' ', Advance: 250},
		{Rune: 'B', Advance: 600, HasShape: true},
	}})
	set := make([]byte, 16)
	for _, cid := range listed {
		set[cid/8] |= 0x80 >> (cid % 8)
	}
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("ABCDEF+Test")},
		object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 21}},
		object.Entry{Key: "CIDSet", Value: object.IndirectRef{Number: 22}},
	)
	desc := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "BaseFont", Value: object.Name("ABCDEF+Test")},
		object.Entry{Key: "FontDescriptor", Value: fd},
		object.Entry{Key: "CIDToGIDMap", Value: cgm},
	)
	font := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "BaseFont", Value: object.Name("ABCDEF+Test")},
		object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
		object.Entry{Key: "DescendantFonts", Value: object.Array{desc}},
	)
	doc := mkView(map[int]*object.IndirectObject{
		20: {Number: 20, Value: font},
		21: {Number: 21, Value: object.NewStream(&object.Dictionary{}, program)},
		22: {Number: 22, Value: object.NewStream(&object.Dictionary{}, set)},
	}, nil)
	return doc, font
}

// cidToGID is a CIDToGIDMap stream sending each CID in m to its glyph, and
// every other CID up to the highest to glyph 0.
func cidToGID(m map[int]int) *object.Stream {
	hi := 0
	for cid := range m {
		hi = max(hi, cid)
	}
	data := make([]byte, 2*(hi+1))
	for cid, gid := range m {
		binary.BigEndian.PutUint16(data[2*cid:], uint16(gid))
	}
	return object.NewStream(&object.Dictionary{}, data)
}

// TestUACIDSetFollowsTheCIDToGIDMap: a subset CIDFontType2's /CIDSet must list
// every CID whose glyph the program has (PDF/UA-1 7.21.4.2). A CID's glyph is
// the one its CIDToGIDMap selects, so for a stream map those are the CIDs the
// map sends to a glyph with an outline — not the glyph indices themselves. The
// check used to decline every stream-mapped font, which PDF/A's CIDSet rule
// had stopped doing (audit 2026-09-22 C68, the sibling its fix left open).
func TestUACIDSetFollowsTheCIDToGIDMap(t *testing.T) {
	const msg = "FontDescriptor /CIDSet does not list all CIDs present in the embedded font program"
	has := func(vs []Violation) bool {
		for _, v := range vs {
			if v.Message == msg {
				return true
			}
		}
		return false
	}
	// The map sends CID 5 to glyph 1 and CID 9 to glyph 3, both with
	// outlines, and CID 7 to the blank glyph 2.
	mapped := func() object.Object { return cidToGID(map[int]int{5: 1, 7: 2, 9: 3}) }
	cases := []struct {
		name   string
		cgm    func() object.Object
		listed []int
		want   bool
	}{
		{"a stream map, CIDSet lists the mapped CIDs", mapped, []int{5, 9}, false},
		{"a stream map, CIDSet lists the glyph indices instead", mapped, []int{1, 3}, true},
		{"a stream map, CIDSet misses a mapped CID", mapped, []int{5}, true},
		{"a stream map, a blank glyph's CID need not be listed", mapped, []int{5, 9}, false},
		{"Identity, CIDSet lists every glyph with an outline", func() object.Object { return object.Name("Identity") }, []int{1, 3}, false},
		{"Identity, CIDSet misses one", func() object.Object { return object.Name("Identity") }, []int{1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, font := cidSetFont(t, c.cgm(), c.listed...)
			if got := has(checkCIDFontCIDSet(doc, font)); got != c.want {
				t.Errorf("reported = %v, want %v", got, c.want)
			}
		})
	}

	// A map pdf0 did not read names no glyph, and nothing is said about it.
	t.Run("an unreadable stream map declines", func(t *testing.T) {
		bad := object.NewStream(object.NewDictionary(object.Entry{Key: "Filter", Value: object.Name("NoSuchFilter")}), []byte{0, 1})
		doc, font := cidSetFont(t, bad)
		if got := checkCIDFontCIDSet(doc, font); len(got) != 0 {
			t.Errorf("a CIDSet was judged against a map pdf0 did not read: %v", got)
		}
	})
}
