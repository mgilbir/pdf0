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
func GPOS(pairs []KernPair) []byte { return GPOSPairsUnder("kern", pairs) }

// GPOSPairsUnder is GPOS with the feature named, for a fixture that needs to
// know which feature a reader took its pairs from. 'dist' is the one that
// matters: it is where the complex scripts state their spacing, and a font may
// declare it and no 'kern' at all.
func GPOSPairsUnder(feature string, pairs []KernPair) []byte {
	return layoutTable(feature, 2, PairPosSubtable(pairs)) // 2 = pair adjustment
}

// PairPosSubtable is the bare type 2 subtable, for a caller placing it in a
// lookup list of its own.
func PairPosSubtable(pairs []KernPair) []byte {
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
	return body
}

// GSUB builds a GSUB table with a single 'liga' feature whose one lookup is a
// LigatureSubst subtable carrying the given ligatures.
func GSUB(ligs []Ligature) []byte {
	return layoutTable("liga", 4, LigatureSubst(ligs)) // 4 = ligature substitution
}

// LigatureSubst is the bare lookup type 4 subtable, for a caller placing it in a
// lookup list of its own — a contextual rule invoking a ligature, say.
func LigatureSubst(ligs []Ligature) []byte {
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
	return body
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
// LookupList scaffolding a GSUB or GPOS table needs, under a 'DFLT' script that
// selects the one feature — a font covering one script, which is what a fixture
// about kerning or ligatures means to be.
func layoutTable(feature string, lookupType int, subtable []byte) []byte {
	return layoutTableFull(
		[]Lookup{{Type: lookupType, Subtables: [][]byte{subtable}}},
		[]Feature{{Tag: feature, Lookups: []int{0}}},
		nil,
	)
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

// GDEF builds a glyph definition table classifying the given glyphs. A glyph
// absent from the map is left unclassified, which is what a real GDEF does for
// glyphs whose class does not matter.
func GDEF(classes map[int]int) []byte {
	// Header: version 1.0, then offsets to glyph class def, attach list, lig
	// caret list, mark attach class def.
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint32(hdr[0:], 0x00010000)
	body := append([]byte(nil), hdr...)
	binary.BigEndian.PutUint16(body[4:], uint16(len(body)))
	body = append(body, classDefFormat2(classes)...)
	return body
}

// GPOSWithFlag is GPOS with a lookup flag set, so a test can exercise the
// glyphs a lookup declares it ignores.
func GPOSWithFlag(pairs []KernPair, flag int) []byte {
	out := GPOS(pairs)
	// The lookup sits at the end, after the LookupList count and offset; its
	// flag is the second field of its header.
	lookupListOff := int(binary.BigEndian.Uint16(out[8:]))
	lookupOff := lookupListOff + int(binary.BigEndian.Uint16(out[lookupListOff+2:]))
	binary.BigEndian.PutUint16(out[lookupOff+2:], uint16(flag))
	return out
}

// GSUBSingle builds a GSUB table with one feature whose lookup replaces each
// glyph in from with the one at the same position in to.
func GSUBSingle(feature string, from, to []int) []byte {
	order := append([]int(nil), from...)
	sortInts(order)
	pos := map[int]int{}
	for i, g := range from {
		pos[g] = to[i]
	}
	sub := make([]byte, 6+2*len(order))
	binary.BigEndian.PutUint16(sub[0:], 2) // substFormat 2: explicit list
	binary.BigEndian.PutUint16(sub[4:], uint16(len(order)))
	body := append([]byte(nil), sub...)
	covOff := len(body)
	body = append(body, coverageFormat1(order)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	for i, g := range order {
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(pos[g]))
	}
	return layoutTable(feature, 1, body) // 1 = single substitution
}

// classDefFormat2 lists class ranges, one per glyph, which is the simplest
// correct encoding for a handful of glyphs.
func classDefFormat2(classes map[int]int) []byte {
	gids := make([]int, 0, len(classes))
	for g := range classes {
		gids = append(gids, g)
	}
	sortInts(gids)
	out := make([]byte, 4+6*len(gids))
	binary.BigEndian.PutUint16(out[0:], 2)
	binary.BigEndian.PutUint16(out[2:], uint16(len(gids)))
	for i, g := range gids {
		rec := 4 + 6*i
		binary.BigEndian.PutUint16(out[rec:], uint16(g))
		binary.BigEndian.PutUint16(out[rec+2:], uint16(g))
		binary.BigEndian.PutUint16(out[rec+4:], uint16(classes[g]))
	}
	return out
}

// Anchor is an attachment point in a glyph's own coordinate space.
type Anchor struct{ X, Y int }

// MarkAttachment describes one mark: the glyph, its class, and its own anchor.
type MarkAttachment struct {
	Glyph  int
	Class  int
	Anchor Anchor
}

// BaseAttachment describes where one base receives marks: the glyph, and an
// anchor per mark class.
type BaseAttachment struct {
	Glyph   int
	Anchors map[int]Anchor
}

// GPOSMarkToBase builds a GPOS table whose single lookup is a mark-to-base
// attachment (type 4), under the 'mark' feature fonts conventionally use.
//
// kind is 4 for mark-to-base and 6 for mark-to-mark; the two have the same
// shape and differ only in what the second coverage covers.
func GPOSMarkToBase(kind int, marks []MarkAttachment, bases []BaseAttachment) []byte {
	return layoutTable("mark", kind, MarkAttachSubtable(marks, bases))
}

// MarkAttachSubtable is the bare mark attachment subtable, whose shape is the
// same for mark-to-base and mark-to-mark.
func MarkAttachSubtable(marks []MarkAttachment, bases []BaseAttachment) []byte {
	classCount := 0
	for _, m := range marks {
		if m.Class+1 > classCount {
			classCount = m.Class + 1
		}
	}
	if classCount == 0 {
		classCount = 1
	}

	markGlyphs := make([]int, 0, len(marks))
	for _, m := range marks {
		markGlyphs = append(markGlyphs, m.Glyph)
	}
	sortInts(markGlyphs)
	baseGlyphs := make([]int, 0, len(bases))
	for _, b := range bases {
		baseGlyphs = append(baseGlyphs, b.Glyph)
	}
	sortInts(baseGlyphs)

	// Header: format, mark coverage, base coverage, class count, mark array,
	// base array. The offsets are filled in as the pieces are appended.
	body := make([]byte, 12)
	binary.BigEndian.PutUint16(body[0:], 1)
	binary.BigEndian.PutUint16(body[6:], uint16(classCount))

	binary.BigEndian.PutUint16(body[2:], uint16(len(body)))
	body = append(body, coverageFormat1(markGlyphs)...)
	binary.BigEndian.PutUint16(body[4:], uint16(len(body)))
	body = append(body, coverageFormat1(baseGlyphs)...)

	// Mark array: a class and an anchor offset per covered mark, with the
	// anchors following it.
	markArrayAt := len(body)
	binary.BigEndian.PutUint16(body[8:], uint16(markArrayAt))
	markArray := make([]byte, 2+4*len(markGlyphs))
	binary.BigEndian.PutUint16(markArray[0:], uint16(len(markGlyphs)))
	anchors := []byte{}
	for i, g := range markGlyphs {
		var m MarkAttachment
		for _, cand := range marks {
			if cand.Glyph == g {
				m = cand
				break
			}
		}
		binary.BigEndian.PutUint16(markArray[2+4*i:], uint16(m.Class))
		binary.BigEndian.PutUint16(markArray[2+4*i+2:], uint16(len(markArray)+len(anchors)))
		anchors = append(anchors, anchorTable(m.Anchor)...)
	}
	body = append(body, append(markArray, anchors...)...)

	// Base array: an anchor offset per class for each covered base.
	baseArrayAt := len(body)
	binary.BigEndian.PutUint16(body[10:], uint16(baseArrayAt))
	baseArray := make([]byte, 2+2*len(baseGlyphs)*classCount)
	binary.BigEndian.PutUint16(baseArray[0:], uint16(len(baseGlyphs)))
	baseAnchors := []byte{}
	for i, g := range baseGlyphs {
		var b BaseAttachment
		for _, cand := range bases {
			if cand.Glyph == g {
				b = cand
				break
			}
		}
		for c := 0; c < classCount; c++ {
			a, ok := b.Anchors[c]
			rec := 2 + (i*classCount+c)*2
			if !ok {
				binary.BigEndian.PutUint16(baseArray[rec:], 0) // no anchor
				continue
			}
			binary.BigEndian.PutUint16(baseArray[rec:], uint16(len(baseArray)+len(baseAnchors)))
			baseAnchors = append(baseAnchors, anchorTable(a)...)
		}
	}
	body = append(body, append(baseArray, baseAnchors...)...)

	return body
}

// CursiveAnchor is a glyph's connecting stroke: where it arrives and where it
// leaves. Either may be absent — a letter that begins a word joins forwards
// only.
type CursiveAnchor struct {
	Glyph             int
	Entry, Exit       Anchor
	HasEntry, HasExit bool
}

// GPOSCursive builds a GPOS table whose single lookup is a cursive attachment
// (type 3), under the 'curs' feature, with the given lookup flag — whose
// RightToLeft bit is what decides which end of a joined run stays on the
// baseline.
func GPOSCursive(anchors []CursiveAnchor, flag int) []byte {
	out := layoutTable("curs", 3, CursivePosSubtable(anchors))
	// layoutTable writes no flag; patch the lookup's second field.
	lookupListOff := int(binary.BigEndian.Uint16(out[8:]))
	lookupOff := lookupListOff + int(binary.BigEndian.Uint16(out[lookupListOff+2:]))
	binary.BigEndian.PutUint16(out[lookupOff+2:], uint16(flag))
	return out
}

// CursivePosSubtable is the bare lookup type 3 subtable, for a caller placing it
// in a lookup list of its own alongside other positioning.
func CursivePosSubtable(anchors []CursiveAnchor) []byte {
	glyphs := make([]int, 0, len(anchors))
	for _, a := range anchors {
		glyphs = append(glyphs, a.Glyph)
	}
	sortInts(glyphs)

	body := make([]byte, 6+4*len(glyphs))
	binary.BigEndian.PutUint16(body[0:], 1)
	binary.BigEndian.PutUint16(body[4:], uint16(len(glyphs)))
	covOff := len(body)
	body = append(body, coverageFormat1(glyphs)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))

	for i, g := range glyphs {
		var a CursiveAnchor
		for _, cand := range anchors {
			if cand.Glyph == g {
				a = cand
				break
			}
		}
		rec := 6 + 4*i
		if a.HasEntry {
			binary.BigEndian.PutUint16(body[rec:], uint16(len(body)))
			body = append(body, anchorTable(a.Entry)...)
		}
		if a.HasExit {
			binary.BigEndian.PutUint16(body[rec+2:], uint16(len(body)))
			body = append(body, anchorTable(a.Exit)...)
		}
	}

	return body
}

// GPOSSingle builds a GPOS table whose lookup nudges each given glyph.
func GPOSSingle(glyph, xPlacement, yPlacement, xAdvance int) []byte {
	return layoutTable("kern", 1, SinglePosSubtable(glyph, xPlacement, yPlacement, xAdvance))
}

// SinglePosSubtable is the bare type 1 subtable, for a caller placing it in a
// lookup list of its own — a contextual positioning rule naming it, say.
func SinglePosSubtable(glyph, xPlacement, yPlacement, xAdvance int) []byte {
	sub := make([]byte, 6+6)
	binary.BigEndian.PutUint16(sub[0:], 1)      // posFormat 1: one value for all
	binary.BigEndian.PutUint16(sub[4:], 0x0007) // XPlacement|YPlacement|XAdvance
	body := append([]byte(nil), sub[:6]...)
	covOff := len(body) + 6
	rec := make([]byte, 6)
	binary.BigEndian.PutUint16(rec[0:], uint16(int16(xPlacement)))
	binary.BigEndian.PutUint16(rec[2:], uint16(int16(yPlacement)))
	binary.BigEndian.PutUint16(rec[4:], uint16(int16(xAdvance)))
	body = append(body, rec...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	body = append(body, coverageFormat1([]int{glyph})...)
	return body
}

// anchorTable writes a format-1 anchor: the two coordinates and nothing else.
func anchorTable(a Anchor) []byte {
	out := make([]byte, 6)
	binary.BigEndian.PutUint16(out[0:], 1)
	binary.BigEndian.PutUint16(out[2:], uint16(int16(a.X)))
	binary.BigEndian.PutUint16(out[4:], uint16(int16(a.Y)))
	return out
}

// GSUBForms builds a GSUB table carrying one single-substitution lookup per
// named feature, which is how a font declares the positional shapes of a
// cursive script: 'init', 'medi' and 'fina' each map a letter to a different
// glyph.
//
// The value of each entry is a pair of parallel slices, from and to.
func GSUBForms(features map[string][2][]int) []byte {
	return GSUBFormsIn(features, nil)
}

// GSUBFormsIn is GSUBForms with an explicit script list, so a fixture can say
// that the positional forms belong to one script and not another. The features
// are indexed in tag order, which is the order GSUBForms sorts them into.
func GSUBFormsIn(features map[string][2][]int, scripts map[string]Script) []byte {
	tags := make([]string, 0, len(features))
	for tag := range features {
		tags = append(tags, tag)
	}
	sortStrings(tags)

	var lookups [][]byte
	for _, tag := range tags {
		pair := features[tag]
		lookups = append(lookups, singleSubstSubtable(pair[0], pair[1]))
	}
	return layoutTableMulti(tags, 1, lookups, scripts)
}

// singleSubstSubtable is a format-2 single substitution: an explicit
// replacement for each covered glyph.
func singleSubstSubtable(from, to []int) []byte {
	order := append([]int(nil), from...)
	sortInts(order)
	at := map[int]int{}
	for i, g := range from {
		at[g] = to[i]
	}
	head := make([]byte, 6+2*len(order))
	binary.BigEndian.PutUint16(head[0:], 2)
	binary.BigEndian.PutUint16(head[4:], uint16(len(order)))
	body := append([]byte(nil), head...)
	covOff := len(body)
	body = append(body, coverageFormat1(order)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	for i, g := range order {
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(at[g]))
	}
	return body
}

// layoutTableMulti wraps several lookups, one per feature, in the scaffolding a
// GSUB or GPOS table needs, under a 'DFLT' script selecting all of them.
//
// scripts, when not nil, replaces that default — which is how a fixture states
// that one script gets the positional forms and another does not. The features
// are in the order the tags are given, so a script names them by that index.
func layoutTableMulti(tags []string, lookupType int, subtables [][]byte, scripts map[string]Script) []byte {
	lookups := make([]Lookup, 0, len(subtables))
	features := make([]Feature, 0, len(tags))
	for i, sub := range subtables {
		lookups = append(lookups, Lookup{Type: lookupType, Subtables: [][]byte{sub}})
		if i < len(tags) {
			features = append(features, Feature{Tag: tags[i], Lookups: []int{i}})
		}
	}
	return layoutTableFull(lookups, features, scripts)
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
