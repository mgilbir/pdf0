package pdfa

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// cidMapProgram is a four-glyph TrueType program: glyphs 1 and 3 have
// outlines, glyph 2 is empty, and glyph 3 advances 900 where the others
// advance 500.
func cidMapProgram() []byte {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000)
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:], 4)
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 4)
	hmtx := make([]byte, 16)
	for gid, adv := range []uint16{500, 500, 500, 900} {
		binary.BigEndian.PutUint16(hmtx[4*gid:], adv)
	}
	// Short loca, half-offsets: glyph 1 is bytes 0..10, glyph 2 is empty,
	// glyph 3 is bytes 10..20.
	loca := make([]byte, 2*5)
	for i, half := range []uint16{0, 0, 5, 5, 10} {
		binary.BigEndian.PutUint16(loca[2*i:], half)
	}
	return buildTestSFNT([]sfntTable{
		{"head", head}, {"maxp", maxp}, {"hhea", hhea}, {"hmtx", hmtx},
		{"loca", loca}, {"glyf", make([]byte, 20)},
	})
}

// cidToGIDStream is a CIDToGIDMap stream sending each CID in m to its glyph,
// long enough to cover the highest CID in m and no more.
func cidToGIDStream(m map[int]int) *object.Stream {
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

// cidMapFont is an Identity-H Type 0 font over cidMapProgram whose
// CIDToGIDMap is cgm, showing the two-byte codes in shown, with a ToUnicode
// map that makes every code a letter.
func cidMapFont(cgm object.Object, shown []byte) (core.View, *object.Dictionary, *core.FontTextUsage) {
	fd := object.NewDictionary(
		object.Entry{Key: "Flags", Value: object.Integer(4)},
		object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 9}},
	)
	desc := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "FontDescriptor", Value: fd},
		object.Entry{Key: "CIDToGIDMap", Value: cgm},
		object.Entry{Key: "DW", Value: object.Integer(500)},
	)
	tu := []byte("begincmap 1 begincodespacerange <0000> <FFFF> endcodespacerange 1 beginbfrange <0000> <FFFF> <0041> endbfrange endcmap")
	font := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
		object.Entry{Key: "DescendantFonts", Value: object.Array{desc}},
		object.Entry{Key: "ToUnicode", Value: object.IndirectRef{Number: 13}},
	)
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1:  {Number: 1, Value: font},
		9:  {Number: 9, Value: object.NewStream(&object.Dictionary{}, cidMapProgram())},
		13: {Number: 13, Value: object.NewStream(&object.Dictionary{}, tu)},
	}})
	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{shown}, Modes: map[int]bool{0: true}}
	return doc, font, u
}

// TestCIDToGIDMapNamesTheGlyphForEveryCheck: a CIDFontType2's CIDs select
// glyphs through its CIDToGIDMap (ISO 32000-1 9.7.4.2), and every check on a
// shown CID — does the glyph exist, is its outline empty, is its width
// consistent — is about that glyph. The width check mapped; the existence and
// emptiness checks read the CID as a glyph index, so a map sending CID 0x9000
// to glyph 1 was reported as "does not define a glyph (CID 36864)" (audit
// 2026-09-22 C68).
func TestCIDToGIDMapNamesTheGlyphForEveryCheck(t *testing.T) {
	unreadable := cidToGIDStream(map[int]int{0x9000: 1})
	unreadable.Dict.Set("Filter", object.Name("NoSuchDecode"))
	cases := []struct {
		name  string
		cgm   object.Object
		shown []byte
		want  []string // substrings of the findings expected, in any order
	}{
		{
			// The audit's case: glyph 1 exists and is 500 wide, as /DW says.
			name:  "a mapped CID past the glyph count names an existing glyph",
			cgm:   cidToGIDStream(map[int]int{0x9000: 1}),
			shown: []byte{0x90, 0x00},
		},
		{
			// CID 3 is glyph 3 by number, which has an outline; the map sends it
			// to glyph 2, which has none, and ToUnicode says it is a letter.
			name:  "emptiness is judged on the mapped glyph",
			cgm:   cidToGIDStream(map[int]int{3: 2}),
			shown: []byte{0x00, 0x03},
			want:  []string{"does not define a glyph referenced for rendering (CID 3)"},
		},
		{
			// CID 1 read as a glyph is 500 wide; mapped, it is glyph 3, 900 wide.
			name:  "width is judged on the mapped glyph",
			cgm:   cidToGIDStream(map[int]int{1: 3}),
			shown: []byte{0x00, 0x01},
			want:  []string{"width information for glyphs used for rendering is inconsistent"},
		},
		{
			// The map covers CIDs 0 and 1. CID 3 has no entry, so it selects
			// no glyph — even though glyph 3 exists.
			name:  "a CID past the end of the map selects no glyph",
			cgm:   cidToGIDStream(map[int]int{1: 1}),
			shown: []byte{0x00, 0x03},
			want:  []string{"does not define a glyph referenced for rendering (CID 3)"},
		},
		{
			// pdf0 cannot read the map, so it cannot name the glyph: nothing is
			// asserted about it, rather than something about glyph 0x9000.
			name:  "an unreadable map asserts nothing",
			cgm:   unreadable,
			shown: []byte{0x90, 0x00},
		},
		{
			name:  "Identity is the CID itself",
			cgm:   object.Name("Identity"),
			shown: []byte{0x00, 0x02, 0x00, 0x03},
			want: []string{
				"does not define a glyph referenced for rendering (CID 2)",
				"width information for glyphs used for rendering is inconsistent",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, font, u := cidMapFont(c.cgm, c.shown)
			got := errMessages(checkCIDFontConsistency(doc, PDFA2b, "6.2.11", font, u))
			if len(got) != len(c.want) {
				t.Fatalf("got %d findings %q, want %d matching %q", len(got), got, len(c.want), c.want)
			}
			for _, w := range c.want {
				found := false
				for _, g := range got {
					found = found || strings.Contains(g, w)
				}
				if !found {
					t.Errorf("no finding matches %q; got %q", w, got)
				}
			}
		})
	}
}
