package fonts

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/mgilbir/pdf0/internal/font"
)

// Subsetting: writing a font program that carries only the glyphs a document
// uses. For a large face this is the difference between a file of tens of
// megabytes and one of tens of kilobytes, and it is the reason a PDF may embed
// a CJK font at all.
//
// # Glyph indices are retained, not renumbered
//
// The subset keeps every glyph index the original had. A glyph that is kept
// occupies its original index; one that is dropped becomes an empty entry at
// that index. The alternative — packing the kept glyphs down into a dense range
// — produces a smaller file, and this does not do it, for three reasons that
// all point the same way:
//
//   - The character codes are already written. Encode produced glyph indices
//     into the content stream before anything knew which glyphs would survive,
//     and with Identity-H those codes are CIDs. Renumbering would invalidate
//     every stream already drawn, or require a CIDToGIDMap stream to undo the
//     renumbering — which costs two bytes per CID and gives much of the saving
//     back.
//   - Composite glyphs reference their components by index. Retaining indices
//     means the glyf data of a composite needs no rewriting at all; renumbering
//     means rewriting the interior of every composite glyph, which is where a
//     subsetter's subtle bugs live.
//   - The CID is the glyph index throughout — in /CIDToGIDMap /Identity, in
//     /CIDSet, in /W. One numbering end to end is a property worth paying for.
//
// What is left on the table is loca and hmtx, which stay at full length: four
// and four bytes per glyph in the original font. For a 20,000-glyph CJK face
// that is 160 KB against the megabytes of outline data that glyf sheds. The
// trade is deliberate, and it is the thing to revisit if that 160 KB ever
// matters more than the correctness it buys.
//
// # What is dropped
//
// Everything not needed to render the kept glyphs: layout tables (GSUB, GPOS,
// GDEF), kerning, hinting programs, and any vendor table. What remains is the
// set a conforming reader requires, plus OS/2 where the original had one.

// requiredTables are the tables a subset font keeps. Hinting (cvt, fpgm, prep)
// is dropped with the rest: it improves rendering at small sizes and costs
// bytes, and a subset that keeps it must keep all of it to stay coherent.
var requiredTables = []string{"cmap", "glyf", "head", "hhea", "hmtx", "loca", "maxp", "name", "post", "OS/2"}

// Subset returns a font program carrying only the glyphs this face has encoded,
// together with .notdef and the components any kept composite glyph needs.
//
// It reports an error when the original font cannot be taken apart — a
// truncated loca, a glyf that does not agree with it — rather than emitting a
// program that claims glyphs it does not have.
func (f *Face) Subset() ([]byte, error) {
	data, _, err := f.subset()
	return data, err
}

// subset is Subset, also returning the glyph indices it kept. Embed needs both,
// and they must be the same set: /CIDSet describes exactly the glyphs the
// program carries, and computing that twice is how the two come to disagree.
func (f *Face) subset() ([]byte, []int, error) {
	tables := font.SFNTTables(f.data)
	if tables == nil {
		return nil, nil, errors.New("fonts: the font program is no longer an sfnt")
	}
	head, loca, glyf := tables["head"], tables["loca"], tables["glyf"]
	if len(head) < 54 || loca == nil || glyf == nil {
		return nil, nil, errors.New("fonts: the font program lacks head, loca or glyf")
	}
	longLoca := binary.BigEndian.Uint16(head[50:]) == 1
	n := f.prog.NumGlyphs

	offsets, err := parseLoca(loca, n, longLoca)
	if err != nil {
		return nil, nil, err
	}

	keep := f.keepSet(offsets, glyf, n)

	// Rebuild glyf and loca. A dropped glyph gets a zero-length entry at its own
	// index, which is what makes this a retained-index subset: the numbering is
	// unchanged and a composite's component references stay valid untouched.
	newGlyf := make([]byte, 0, len(glyf))
	newLoca := make([]uint32, n+1)
	for gid := 0; gid < n; gid++ {
		newLoca[gid] = uint32(len(newGlyf))
		if !keep[gid] {
			continue
		}
		start, end := offsets[gid], offsets[gid+1]
		if start > end || int(end) > len(glyf) {
			return nil, nil, fmt.Errorf("fonts: glyph %d lies outside the glyf table", gid)
		}
		newGlyf = append(newGlyf, glyf[start:end]...)
		for len(newGlyf)%4 != 0 { // glyf entries are long-aligned
			newGlyf = append(newGlyf, 0)
		}
	}
	newLoca[n] = uint32(len(newGlyf))

	out := map[string][]byte{}
	for _, tag := range requiredTables {
		if b, ok := tables[tag]; ok {
			out[tag] = b
		}
	}
	out["glyf"] = newGlyf
	// Always write the long loca form: the short form stores offsets halved, so
	// it cannot represent an odd offset, and choosing between them is one more
	// thing to get wrong for no benefit at this size.
	locaBytes := make([]byte, 4*(n+1))
	for i, off := range newLoca {
		binary.BigEndian.PutUint32(locaBytes[4*i:], off)
	}
	out["loca"] = locaBytes

	newHead := append([]byte(nil), head...)
	binary.BigEndian.PutUint16(newHead[50:], 1) // indexToLocFormat: long
	out["head"] = newHead

	keptList := make([]int, 0, countTrue(keep))
	for gid, k := range keep {
		if k {
			keptList = append(keptList, gid)
		}
	}
	return assembleSFNT(out), keptList, nil
}

// keepSet decides which glyph indices survive: .notdef, every glyph this face
// encoded, and — transitively — every component those glyphs are built from.
//
// The closure matters. An accented letter is usually a composite referring to a
// base letter and a mark, and dropping either leaves a glyph that renders as
// nothing. It is iterated to a fixed point because a component may itself be
// composite.
func (f *Face) keepSet(offsets []uint32, glyf []byte, n int) []bool {
	keep := make([]bool, n)
	keep[0] = true // .notdef is always present
	for gid := range f.used {
		if gid >= 0 && gid < n {
			keep[gid] = true
		}
	}
	for {
		before := countTrue(keep)
		components := make([]bool, n)
		for gid := 0; gid < n; gid++ {
			if !keep[gid] {
				continue
			}
			start, end := offsets[gid], offsets[gid+1]
			if start >= end || int(end) > len(glyf) {
				continue
			}
			font.MarkComposite(glyf[start:end], n, components)
		}
		for gid, isComponent := range components {
			if isComponent {
				keep[gid] = true
			}
		}
		if countTrue(keep) == before {
			return keep
		}
	}
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

// parseLoca reads the glyph offset table. Entry i is where glyph i starts and
// entry i+1 where it ends, so a font with n glyphs has n+1 entries; the short
// form stores each offset halved.
func parseLoca(loca []byte, n int, long bool) ([]uint32, error) {
	need := 2 * (n + 1)
	if long {
		need = 4 * (n + 1)
	}
	if len(loca) < need {
		return nil, fmt.Errorf("fonts: loca holds %d bytes, too few for %d glyphs", len(loca), n)
	}
	out := make([]uint32, n+1)
	for i := 0; i <= n; i++ {
		if long {
			out[i] = binary.BigEndian.Uint32(loca[4*i:])
		} else {
			out[i] = uint32(binary.BigEndian.Uint16(loca[2*i:])) * 2
		}
	}
	return out, nil
}

// assembleSFNT writes a table directory and the tables, four-byte aligned with
// the checksums the format specifies.
func assembleSFNT(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags) // the directory is ordered by tag

	n := len(tags)
	searchRange, entrySelector := 16, 0
	for searchRange*2 <= 16*n {
		searchRange *= 2
		entrySelector++
	}
	dir := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(dir[0:], 0x00010000)
	binary.BigEndian.PutUint16(dir[4:], uint16(n))
	binary.BigEndian.PutUint16(dir[6:], uint16(searchRange))
	binary.BigEndian.PutUint16(dir[8:], uint16(entrySelector))
	binary.BigEndian.PutUint16(dir[10:], uint16(16*n-searchRange))

	out := append([]byte(nil), dir...)
	for i, tag := range tags {
		body := tables[tag]
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		rec := 12 + 16*i
		copy(dir[rec:], tag)
		binary.BigEndian.PutUint32(dir[rec+4:], sfntChecksum(body))
		binary.BigEndian.PutUint32(dir[rec+8:], uint32(len(out)))
		binary.BigEndian.PutUint32(dir[rec+12:], uint32(len(body)))
		out = append(out, body...)
	}
	copy(out, dir)
	return out
}

func sfntChecksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(b); i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	if r := len(b) % 4; r != 0 {
		var last [4]byte
		copy(last[:], b[len(b)-r:])
		sum += binary.BigEndian.Uint32(last[:])
	}
	return sum
}

// subsetTag is the six uppercase letters ISO 32000-2 9.6.4 requires in front of
// a subset font's name, as in "ABCDEF+Probe-Regular". A reader uses it to tell
// two subsets of the same face apart, so it must differ when the glyph sets do
// and match when they do not — which makes it a function of the kept glyphs
// rather than a random draw.
func subsetTag(kept []int) string {
	// FNV-1a over the kept indices: cheap, and deterministic, so the same
	// document produces the same file twice.
	var h uint64 = 14695981039346656037
	for _, gid := range kept {
		for shift := 0; shift < 32; shift += 8 {
			h ^= uint64(byte(gid >> shift))
			h *= 1099511628211
		}
	}
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = byte('A' + h%26)
		h /= 26
	}
	return string(tag)
}
