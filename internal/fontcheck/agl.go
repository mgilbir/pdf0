package fontcheck

import (
	_ "embed"
	"slices"
	"strings"
	"sync"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the Adobe Glyph List and the rule that reads it: a
// non-symbolic TrueType font's /Differences may name only glyphs the list has,
// and needs a (3,1) cmap subtable to look them up in. CmapEncodings, which
// reads that cmap's records, is here too; PDF/A's symbolic-font cmap rule
// reads it as well.

// glyphList is Adobe's glyphlist.txt, verbatim, from
// https://github.com/adobe-type-tools/agl-aglfn at commit
// 4036a9ca80a62f64f9de4f7321a9a045ad0ecfd6 — the commit forme's glyph-name
// table is generated from. Its licence is the BSD-style notice at its head,
// which travels with it. TestGlyphListIsThePinnedFile holds the bytes to that
// commit's.
//
//go:embed glyphlist.txt
var glyphList string

var (
	aglOnce  sync.Once
	aglNames map[string]bool
)

// AGLListed reports whether name is in the Adobe Glyph List, as veraPDF reads
// "listed in the Adobe Glyph List": membership of the list's 4,281 names, and
// .notdef, which veraPDF's copy of the list carries as its first entry. A name
// that the Adobe Glyph Naming convention would resolve without the list —
// uni0041, u1F600 — is not in it, and veraPDF does not accept one.
func AGLListed(name string) bool {
	aglOnce.Do(func() {
		aglNames = make(map[string]bool, 4282)
		aglNames[".notdef"] = true
		for _, line := range strings.Split(glyphList, "\n") {
			if line == "" || line[0] == '#' {
				continue
			}
			if n, _, ok := strings.Cut(line, ";"); ok {
				aglNames[n] = true
			}
		}
	})
	return aglNames[name]
}

// DifferencesNotInAGL checks the second half of the TrueType encoding rule
// shared by ISO 19005-2 6.2.11.6, ISO 14289-1 7.21.6 and ISO 14289-2 8.4.5.7:
// "no non-symbolic TrueType font shall define a Differences array unless all
// of the glyph names in the Differences array are listed in the Adobe Glyph
// List and, if the font program is embedded, it shall contain at least the
// Microsoft Unicode (3,1 – Platform ID = 3, Encoding ID = 1) encoding in the
// 'cmap' table".
//
// It reads the rule as veraPDF does (its PDTrueTypeFont's test of whether the
// differences are Unicode-compliant, consulted only when the /Encoding
// dictionary has a /Differences key): a font with no embedded program is compliant; one
// with a program needs the (3,1) subtable whatever the array holds, an empty
// one included; and every name in the array must be AGLListed. It returns the
// first name not listed, or noCmap31 when the program has no (3,1) subtable.
// A program pdf0 could not read is not evidence either way, and is not
// reported — veraPDF reports it.
//
// The symbolic flag, and the requirement on the base encoding, are the
// caller's.
func DifferencesNotInAGL(doc core.View, fontDict *object.Dictionary) (badName string, noCmap31 bool) {
	enc := doc.ResolveDict(fontDict.Get("Encoding"))
	if enc == nil || !enc.Has("Differences") {
		return "", false
	}
	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	if fd == nil {
		return "", false
	}
	present, known := hasCmap31(doc, fd)
	if !known {
		return "", false
	}
	if !present {
		return "", true
	}
	diffs, _ := doc.Resolve(enc.Get("Differences")).(object.Array)
	doc.Charge(len(diffs))
	for _, el := range diffs {
		if name, ok := doc.ResolveName(el); ok && !AGLListed(string(name)) {
			return string(name), false
		}
	}
	return "", false
}

// hasCmap31 reports whether a TrueType font descriptor's embedded program has
// a Microsoft Unicode (platform 3, encoding 1) cmap subtable, with
// CmapEncodings' known.
func hasCmap31(doc core.View, fd *object.Dictionary) (present, known bool) {
	encs, known := CmapEncodings(doc, fd)
	return slices.Contains(encs, CmapEncoding{3, 1}), known
}

// CmapEncoding is one encoding record of an sfnt cmap table: a platform ID and
// a platform-specific encoding ID, such as (3, 1) for Microsoft Unicode.
type CmapEncoding struct {
	Platform, Encoding int
}

// CmapEncodings lists the encoding records of the cmap table in a TrueType
// font descriptor's embedded program, in the order the table declares them.
// known is false when there is no embedded program, or pdf0 could not read its
// bytes or its sfnt table directory — nothing is then said about the cmap. A
// readable program with no cmap table has no records.
//
// The program is FontFile2 or, for an OpenType program, FontFile3; the records
// are read straight from the table, so a subtable the font declares counts
// whatever its format, and whether or not it could be read. Records that would
// lie past the end of the table are not listed.
func CmapEncodings(doc core.View, fd *object.Dictionary) (encs []CmapEncoding, known bool) {
	var s *object.Stream
	for _, k := range []object.Name{"FontFile2", "FontFile3"} {
		if st, ok := doc.Resolve(fd.Get(k)).(*object.Stream); ok {
			s = st
			break
		}
	}
	if s == nil {
		return nil, false
	}
	data, r := doc.Content(s)
	if r != core.ReasonOK {
		return nil, false
	}
	tables := font.SFNTTables(data)
	if tables == nil {
		return nil, false
	}
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return nil, true
	}
	n := int(cmap[2])<<8 | int(cmap[3])
	doc.Charge(n)
	for i := 0; i < n; i++ {
		rec := 4 + 8*i
		if rec+4 > len(cmap) {
			break
		}
		encs = append(encs, CmapEncoding{
			Platform: int(cmap[rec])<<8 | int(cmap[rec+1]),
			Encoding: int(cmap[rec+2])<<8 | int(cmap[rec+3]),
		})
	}
	return encs, true
}
