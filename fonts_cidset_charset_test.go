package pdf0

import (
	"encoding/binary"
	"sort"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// /CIDSet against the program it describes.
//
// ISO 19005-1 6.3.5 and 19005-2 6.2.11.4.2: the set "shall identify all CIDs
// which are present in the font program, regardless of whether a CID in the
// font is referenced or used by the PDF or not". For a CID-keyed CFF the CIDs
// present are the ones its charset lists, so the set and the charset have to
// be the same set of numbers — neither more nor fewer.
//
// fonts writes the set from the glyphs the subset kept, and nothing else.
// That is right because forme's subset of a CID-keyed CFF holds exactly those
// glyphs, each under its own CID (forme 462f3b5). Before it, the subset kept
// the whole charset with the unused glyphs emptied, and fonts read the charset
// back out of the program to match it. This test is what the simpler rule
// rests on: it reads the charset out of the program in the file, by the font
// package's own parser, and holds the set to it.

// TestTheCIDSetIsTheEmbeddedProgramsCharset draws the CJK face through every
// public path, subsetted and (under a no-subsetting licence) whole, and
// compares /CIDSet with the charset of the program embedded beside it.
func TestTheCIDSetIsTheEmbeddedProgramsCharset(t *testing.T) {
	for _, tc := range []struct {
		name    string
		program []byte
		subset  bool
	}{
		{"subset", cidKeyedFace(t), true},
		{"whole", withOS2FSType(t, cidKeyedFace(t), 0x0100), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := fonts.Load(tc.program)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range drawPaths {
				const text = "ｱ日本 日本語のテキスト"
				back, _ := drawnDocument(t, base.Clone(), path, text, pdfa.PDFA1b, true)
				set, charset := cidSetAndCharset(t, back)
				if missing, extra := setDifference(charset, set); len(missing) > 0 || len(extra) > 0 {
					t.Errorf("%s: /CIDSet lacks the program's CIDs %v and names %v, which the "+
						"program does not have", path.name, head(missing), head(extra))
				}
				// The subset is the glyphs drawn, which is what made a
				// three-character page a megabyte when it was not.
				if tc.subset && len(charset) > len([]rune(text))+1 {
					t.Errorf("%s: the subset has %d CIDs for %d characters", path.name,
						len(charset), len([]rune(text)))
				}
				if !tc.subset && len(charset) != base.NumGlyphs() {
					t.Errorf("%s: the whole program has %d CIDs; the face has %d glyphs",
						path.name, len(charset), base.NumGlyphs())
				}
			}
		})
	}
}

// cidSetAndCharset reads the one CID-keyed font of a document: the CIDs its
// /CIDSet names, and the CIDs the charset of its embedded program lists.
func cidSetAndCharset(t *testing.T, doc *Document) (set, charset map[int]bool) {
	t.Helper()
	var fd *object.Dictionary
	for _, o := range doc.Objects {
		if d, ok := o.Value.(*object.Dictionary); ok && d.Get("FontFile3") != nil {
			fd = d
		}
	}
	if fd == nil {
		t.Fatal("no font descriptor with a FontFile3")
	}
	cidSet, _ := doc.Resolve(fd.Get("CIDSet")).(*object.Stream)
	file, _ := doc.Resolve(fd.Get("FontFile3")).(*object.Stream)
	if cidSet == nil || file == nil {
		t.Fatalf("the descriptor has /CIDSet %v and /FontFile3 %v", fd.Get("CIDSet"), fd.Get("FontFile3"))
	}
	bits, err := doc.StreamData(cidSet)
	if err != nil {
		t.Fatal(err)
	}
	program, err := doc.StreamData(file)
	if err != nil {
		t.Fatal(err)
	}
	set = map[int]bool{}
	for i := 0; i < 8*len(bits); i++ {
		if bitSet(bits, i) {
			set[i] = true
		}
	}
	// Bare under PDF/A-1, whose PDF 1.4 has no OpenType font file.
	raw := program
	if file.Dict.Get("Subtype") != object.Name("CIDFontType0C") {
		raw = font.SFNTTables(program)["CFF "]
	}
	cff := font.ParseCFF(raw)
	if cff == nil || cff.GIDToCID == nil {
		t.Fatal("the embedded program is not a CID-keyed CFF")
	}
	charset = map[int]bool{}
	for _, cid := range cff.GIDToCID {
		charset[cid] = true
	}
	return set, charset
}

// setDifference is what want has that got lacks, and what got has that want
// does not, each sorted.
func setDifference(want, got map[int]bool) (missing, extra []int) {
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Ints(missing)
	sort.Ints(extra)
	return missing, extra
}

// withOS2FSType is an sfnt with its OS/2 fsType set: a font with any licence,
// made from one whose licence permits everything.
func withOS2FSType(t *testing.T, program []byte, fsType uint16) []byte {
	t.Helper()
	data := append([]byte(nil), program...)
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		if string(rec[:4]) == "OS/2" {
			off := binary.BigEndian.Uint32(rec[8:])
			binary.BigEndian.PutUint16(data[off+8:], fsType)
			return data
		}
	}
	t.Fatal("the program has no OS/2 table")
	return nil
}
