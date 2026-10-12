package fontcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/object"
)

// TestGlyphListIsThePinnedFile holds the embedded list to the bytes of
// glyphlist.txt at agl-aglfn commit 4036a9ca80a62f64f9de4f7321a9a045ad0ecfd6,
// so an edit to it — by hand, or by a tool normalising line endings — fails
// here rather than changing which names are listed.
func TestGlyphListIsThePinnedFile(t *testing.T) {
	const pinned = "a3b2f61ced9f3644cc0d4ecde5c59df34ca286c689d9484a43a710a81c466789"
	sum := sha256.Sum256([]byte(glyphList))
	if got := hex.EncodeToString(sum[:]); got != pinned {
		t.Fatalf("glyphlist.txt SHA-256 = %s, want %s", got, pinned)
	}
}

// TestAGLListed pins what "listed in the Adobe Glyph List" admits: the list's
// own 4,281 names and .notdef — veraPDF's copy of the list, which is the
// oracle here, is Adobe's plus a first line ".notdef 0000" — and nothing a
// naming convention derives.
func TestAGLListed(t *testing.T) {
	n := 0
	for _, line := range strings.Split(glyphList, "\n") {
		if line != "" && line[0] != '#' {
			n++
		}
	}
	if n != 4281 {
		t.Errorf("the list has %d entries, want the 4,281 of AGL 2.0", n)
	}
	for _, name := range []string{
		".notdef", "A", "space", "grave", "Euro", "afii10017", "dalethatafpatah",
		"H18533", "SF010000", "commaaccent",
	} {
		if !AGLListed(name) {
			t.Errorf("%q is in the list", name)
		}
	}
	for _, name := range []string{
		"", "gravee", "uni0041", "u1F600", "a1", "bracket", "isinferior", "A.sc", "Space",
	} {
		if AGLListed(name) {
			t.Errorf("%q is not in the list", name)
		}
	}
}

// differencesFont is a TrueType font dictionary whose encoding carries diffs
// (or, when diffs is nil, no /Differences key at all), over the program in
// object 7, which is absent when program is nil.
func differencesFont(diffs object.Object, program []byte) (map[int]*object.IndirectObject, *object.Dictionary) {
	objs := map[int]*object.IndirectObject{}
	fd := object.NewDictionary(object.Entry{Key: "Flags", Value: object.Integer(32)})
	if program != nil {
		fd.Set("FontFile2", object.IndirectRef{Number: 7})
		objs[7] = &object.IndirectObject{Number: 7, Value: object.NewStream(&object.Dictionary{}, program)}
	}
	enc := object.NewDictionary(object.Entry{Key: "BaseEncoding", Value: object.Name("WinAnsiEncoding")})
	if diffs != nil {
		enc.Set("Differences", diffs)
	}
	font := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("TrueType")},
		object.Entry{Key: "Encoding", Value: enc},
		object.Entry{Key: "FontDescriptor", Value: fd},
	)
	return objs, font
}

// TestDifferencesNotInAGL reads the rule as veraPDF's PDTrueTypeFont does.
func TestDifferencesNotInAGL(t *testing.T) {
	unicode := fonttest.SFNT(fonttest.SFNTOptions{Glyphs: []fonttest.Glyph{{Rune: 'A', Advance: 500, HasShape: true}}})
	format0 := make([]byte, 6+256)
	format0[3] = byte(len(format0))
	macOnly := fonttest.SFNTWithCmapSubtables([]fonttest.CmapSub{{Plat: 1, Enc: 0, Data: format0}})
	symbolOnly := fonttest.SFNTWithCmapSubtables([]fonttest.CmapSub{{Plat: 3, Enc: 0, Data: format0}})
	bothMS := fonttest.SFNTWithCmapSubtables([]fonttest.CmapSub{{Plat: 3, Enc: 0, Data: format0}, {Plat: 3, Enc: 1, Data: format0}})
	names := func(ns ...string) object.Array {
		a := object.Array{object.Integer(96)}
		for _, n := range ns {
			a = append(a, object.Name(n))
		}
		return a
	}
	cases := []struct {
		name     string
		diffs    object.Object
		program  []byte
		wantName string
		wantCmap bool
	}{
		{"no /Differences asks nothing", nil, macOnly, "", false},
		{"listed names over a (3,1) cmap", names("grave", "Euro"), unicode, "", false},
		{"an unlisted name", names("grave", "gravee", "zzz"), unicode, "gravee", false},
		{"a (3,1) subtable after a (3,0) one counts", names("grave"), bothMS, "", false},
		{"listed names, only a (1,0) cmap", names("grave"), macOnly, "", true},
		{"listed names, only a (3,0) cmap", names("grave"), symbolOnly, "", true},
		{"an empty array still needs the (3,1) cmap", object.Array{}, macOnly, "", true},
		{"no program: compliant whatever the names", names("gravee"), nil, "", false},
		{"a program pdf0 cannot read says nothing", names("gravee"), []byte("not a font"), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			objs, font := differencesFont(c.diffs, c.program)
			doc := mkView(objs)
			name, noCmap := DifferencesNotInAGL(doc, font)
			if name != c.wantName || noCmap != c.wantCmap {
				t.Errorf("DifferencesNotInAGL = %q, %v; want %q, %v", name, noCmap, c.wantName, c.wantCmap)
			}
		})
	}
}
