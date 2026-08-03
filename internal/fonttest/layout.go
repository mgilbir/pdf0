package fonttest

import "encoding/binary"

// Synthetic GPOS and GSUB tables, so that kerning and ligature reading can be
// tested against something whose contents the test states rather than against
// whatever a real face happens to contain.

// KernPair is one pair adjustment, in font units. Negative closes the gap,
// which is what kerning almost always does.
type KernPair struct {
	Left, Right int // glyph indices
	Adjust      int
}

// Ligature is one substitution: a run of glyph indices replaced by one.
type Ligature struct {
	Components []int // two or more glyphs
	Glyph      int   // what they become
}

// GPOS builds a GPOS table with a single 'kern' feature whose one lookup is a
// PairPos format 1 subtable carrying the given pairs.
func GPOS(pairs []KernPair) []byte {
	// Group by left glyph, which is how PairPos format 1 is organised.
	order := []int{}
	byLeft := map[int][]KernPair{}
	for _, p := range pairs {
		if _, seen := byLeft[p.Left]; !seen {
			order = append(order, p.Left)
		}
		byLeft[p.Left] = append(byLeft[p.Left], p)
	}
	sortInts(order)

	// PairSets, each a count followed by (secondGlyph, xAdvance) records.
	var sets [][]byte
	for _, left := range order {
		ps := byLeft[left]
		set := make([]byte, 2+4*len(ps))
		binary.BigEndian.PutUint16(set, uint16(len(ps)))
		for i, p := range ps {
			binary.BigEndian.PutUint16(set[2+4*i:], uint16(p.Right))
			binary.BigEndian.PutUint16(set[2+4*i+2:], uint16(int16(p.Adjust)))
		}
		sets = append(sets, set)
	}

	coverage := coverageFormat1(order)
	// PairPos format 1: format, coverage, valueFormat1, valueFormat2,
	// pairSetCount, pairSetOffsets.
	head := make([]byte, 10+2*len(sets))
	binary.BigEndian.PutUint16(head[0:], 1)
	binary.BigEndian.PutUint16(head[4:], 0x0004) // valueFormat1: XAdvance
	binary.BigEndian.PutUint16(head[6:], 0)      // valueFormat2: nothing
	binary.BigEndian.PutUint16(head[8:], uint16(len(sets)))
	body := append([]byte(nil), head...)
	covOff := len(body)
	body = append(body, coverage...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	for i, set := range sets {
		binary.BigEndian.PutUint16(body[10+2*i:], uint16(len(body)))
		body = append(body, set...)
	}
	return layoutTable("kern", 2, body) // 2 = pair adjustment
}

// GSUB builds a GSUB table with a single 'liga' feature whose one lookup is a
// LigatureSubst subtable carrying the given ligatures.
func GSUB(ligs []Ligature) []byte {
	order := []int{}
	byFirst := map[int][]Ligature{}
	for _, l := range ligs {
		if len(l.Components) < 2 {
			continue
		}
		first := l.Components[0]
		if _, seen := byFirst[first]; !seen {
			order = append(order, first)
		}
		byFirst[first] = append(byFirst[first], l)
	}
	sortInts(order)

	var sets [][]byte
	for _, first := range order {
		ls := byFirst[first]
		set := make([]byte, 2+2*len(ls))
		binary.BigEndian.PutUint16(set, uint16(len(ls)))
		for _, l := range ls {
			rest := l.Components[1:]
			lig := make([]byte, 4+2*len(rest))
			binary.BigEndian.PutUint16(lig[0:], uint16(l.Glyph))
			binary.BigEndian.PutUint16(lig[2:], uint16(len(l.Components)))
			for k, c := range rest {
				binary.BigEndian.PutUint16(lig[4+2*k:], uint16(c))
			}
			binary.BigEndian.PutUint16(set[2+2*indexOfLig(ls, l):], uint16(len(set)))
			set = append(set, lig...)
		}
		sets = append(sets, set)
	}

	coverage := coverageFormat1(order)
	head := make([]byte, 6+2*len(sets))
	binary.BigEndian.PutUint16(head[0:], 1) // substFormat
	binary.BigEndian.PutUint16(head[4:], uint16(len(sets)))
	body := append([]byte(nil), head...)
	covOff := len(body)
	body = append(body, coverage...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	for i, set := range sets {
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(len(body)))
		body = append(body, set...)
	}
	return layoutTable("liga", 4, body) // 4 = ligature substitution
}

func indexOfLig(ls []Ligature, want Ligature) int {
	for i, l := range ls {
		if l.Glyph == want.Glyph && len(l.Components) == len(want.Components) {
			return i
		}
	}
	return 0
}

// layoutTable wraps one lookup subtable in the ScriptList / FeatureList /
// LookupList scaffolding a GSUB or GPOS table needs.
func layoutTable(feature string, lookupType int, subtable []byte) []byte {
	// Lookup: type, flag, subtable count, one offset.
	lookup := make([]byte, 8)
	binary.BigEndian.PutUint16(lookup[0:], uint16(lookupType))
	binary.BigEndian.PutUint16(lookup[4:], 1)
	binary.BigEndian.PutUint16(lookup[6:], 8) // the subtable follows immediately
	lookup = append(lookup, subtable...)

	// LookupList: count, one offset.
	lookupList := make([]byte, 4)
	binary.BigEndian.PutUint16(lookupList[0:], 1)
	binary.BigEndian.PutUint16(lookupList[2:], 4)
	lookupList = append(lookupList, lookup...)

	// Feature: featureParams, lookupIndexCount, index 0.
	feat := make([]byte, 6)
	binary.BigEndian.PutUint16(feat[2:], 1)
	binary.BigEndian.PutUint16(feat[4:], 0)

	// FeatureList: count, record{tag, offset}.
	featureList := make([]byte, 8)
	binary.BigEndian.PutUint16(featureList[0:], 1)
	copy(featureList[2:], feature)
	binary.BigEndian.PutUint16(featureList[6:], 8)
	featureList = append(featureList, feat...)

	// ScriptList: empty. Nothing here reads it — script and language selection
	// is exactly what the reader does not implement — but a well-formed table
	// has one.
	scriptList := []byte{0, 0}

	header := make([]byte, 10)
	binary.BigEndian.PutUint32(header[0:], 0x00010000)
	out := append([]byte(nil), header...)
	binary.BigEndian.PutUint16(out[4:], uint16(len(out)))
	out = append(out, scriptList...)
	binary.BigEndian.PutUint16(out[6:], uint16(len(out)))
	out = append(out, featureList...)
	binary.BigEndian.PutUint16(out[8:], uint16(len(out)))
	out = append(out, lookupList...)
	return out
}

// coverageFormat1 lists glyphs explicitly, in the ascending order the format
// requires; the index of a glyph here is its coverage index.
func coverageFormat1(glyphs []int) []byte {
	out := make([]byte, 4+2*len(glyphs))
	binary.BigEndian.PutUint16(out[0:], 1)
	binary.BigEndian.PutUint16(out[2:], uint16(len(glyphs)))
	for i, g := range glyphs {
		binary.BigEndian.PutUint16(out[4+2*i:], uint16(g))
	}
	return out
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
