package fonttest

import "encoding/binary"

// Fixtures for contextual substitution.
//
// These differ from the other layout fixtures in one way that shapes the whole
// file: a contextual lookup does not carry a substitution, it carries the
// *index* of another lookup. So a fixture cannot be one subtable wrapped in
// scaffolding — it has to be a lookup list with the acting lookup at a known
// index and the rule naming it. GSUBLookups is that general form, and the six
// subtable builders below fill it.

// SeqLookup is one record of a contextual rule: apply lookup number Lookup at
// matched position At.
//
// At counts matched positions, not glyphs. Where the lookup skips marks, the
// two differ, and a fixture that conflates them tests the wrong thing.
type SeqLookup struct{ At, Lookup int }

// ContextRule is one rule of a sequence context.
type ContextRule struct {
	// Input is the whole matched sequence including the first item. In a
	// glyph-based context the items are glyph indices; in a class-based one they
	// are classes. The format stores everything after the first, because the
	// first is what the coverage table already selected on.
	Input   []int
	Lookups []SeqLookup
}

// ChainRule is one rule of a chained sequence context.
type ChainRule struct {
	// Backtrack is what must precede the match, nearest first — the order the
	// format stores it in, which is the reverse of how it reads on the page.
	Backtrack []int
	Input     []int
	Lookahead []int
	Lookups   []SeqLookup
}

// Lookup is one entry of a lookup list.
type Lookup struct {
	Type      int
	Flag      int
	Subtables [][]byte
}

// GSUBLookups builds a GSUB table from an explicit lookup list and a map of
// feature tag to the lookup indices it names.
//
// The indices are the point: a contextual subtable refers to a lookup by its
// position in this list, so a fixture controls both ends of that reference.
func GSUBLookups(lookups []Lookup, features map[string][]int) []byte {
	return layoutLookups(lookups, features)
}

// GPOSLookups is the same for a positioning table. The scaffolding is identical
// — the two tables differ only in what their lookup types mean — and a fixture
// needs it whenever one font must carry two kinds of positioning at once.
func GPOSLookups(lookups []Lookup, features map[string][]int) []byte {
	return layoutLookups(lookups, features)
}

func layoutLookups(lookups []Lookup, features map[string][]int) []byte {
	lookupList := make([]byte, 2+2*len(lookups))
	binary.BigEndian.PutUint16(lookupList[0:], uint16(len(lookups)))
	for i, lk := range lookups {
		body := make([]byte, 6+2*len(lk.Subtables))
		binary.BigEndian.PutUint16(body[0:], uint16(lk.Type))
		binary.BigEndian.PutUint16(body[2:], uint16(lk.Flag))
		binary.BigEndian.PutUint16(body[4:], uint16(len(lk.Subtables)))
		for j, sub := range lk.Subtables {
			binary.BigEndian.PutUint16(body[6+2*j:], uint16(len(body)))
			body = append(body, sub...)
		}
		binary.BigEndian.PutUint16(lookupList[2+2*i:], uint16(len(lookupList)))
		lookupList = append(lookupList, body...)
	}

	tags := make([]string, 0, len(features))
	for tag := range features {
		tags = append(tags, tag)
	}
	sortStrings(tags)

	featureList := make([]byte, 2+6*len(tags))
	binary.BigEndian.PutUint16(featureList[0:], uint16(len(tags)))
	for i, tag := range tags {
		idx := features[tag]
		feat := make([]byte, 4+2*len(idx))
		binary.BigEndian.PutUint16(feat[2:], uint16(len(idx)))
		for j, v := range idx {
			binary.BigEndian.PutUint16(feat[4+2*j:], uint16(v))
		}
		rec := 2 + 6*i
		copy(featureList[rec:], tag)
		binary.BigEndian.PutUint16(featureList[rec+4:], uint16(len(featureList)))
		featureList = append(featureList, feat...)
	}

	header := make([]byte, 10)
	binary.BigEndian.PutUint32(header[0:], 0x00010000)
	out := append([]byte(nil), header...)
	binary.BigEndian.PutUint16(out[4:], uint16(len(out)))
	out = append(out, 0, 0) // an empty ScriptList
	binary.BigEndian.PutUint16(out[6:], uint16(len(out)))
	out = append(out, featureList...)
	binary.BigEndian.PutUint16(out[8:], uint16(len(out)))
	out = append(out, lookupList...)
	return out
}

// SingleSubst is a lookup type 1 subtable replacing each glyph in from with the
// one at the same position in to. It is what a contextual rule usually invokes.
func SingleSubst(from, to []int) []byte { return singleSubstSubtable(from, to) }

// MultipleSubst is a lookup type 2 subtable: each glyph in from becomes the
// whole sequence at the same position in to. This is what a decomposition looks
// like — one character's glyph split into the pieces the rest of the rules are
// written against.
func MultipleSubst(from []int, to [][]int) []byte {
	order := append([]int(nil), from...)
	sortInts(order)
	seq := map[int][]int{}
	for i, g := range from {
		seq[g] = to[i]
	}
	body := make([]byte, 6+2*len(order))
	binary.BigEndian.PutUint16(body[0:], 1)
	binary.BigEndian.PutUint16(body[4:], uint16(len(order)))
	covOff := len(body)
	body = append(body, coverageFormat1(order)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	for i, g := range order {
		glyphs := seq[g]
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(len(body)))
		s := make([]byte, 2+2*len(glyphs))
		binary.BigEndian.PutUint16(s[0:], uint16(len(glyphs)))
		for k, v := range glyphs {
			binary.BigEndian.PutUint16(s[2+2*k:], uint16(v))
		}
		body = append(body, s...)
	}
	return body
}

// AlternateSubst is a lookup type 3 subtable: each glyph in from is offered the
// choices at the same position in alts, of which a shaper with no one to ask
// takes the first.
func AlternateSubst(from []int, alts [][]int) []byte {
	// The two have the same shape on the wire — a count and a list of glyphs
	// per covered glyph — and differ only in what the list means.
	return MultipleSubst(from, alts)
}

// ExtensionSubst wraps a subtable in the indirection a large font uses to place
// its subtables beyond the reach of a 16-bit offset: the lookup's own type
// becomes 7, and each subtable states the real type and a 32-bit offset to it.
func ExtensionSubst(realType int, sub []byte) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint16(out[0:], 1) // extensionFormat
	binary.BigEndian.PutUint16(out[2:], uint16(realType))
	binary.BigEndian.PutUint32(out[4:], 8) // the real subtable follows immediately
	return append(out, sub...)
}

// SequenceContext1 is a lookup type 5 format 1 subtable: rules selected by the
// first glyph, matching the rest by glyph.
func SequenceContext1(rules map[int][]ContextRule) []byte {
	firsts := intKeys(rules)
	body := make([]byte, 6+2*len(firsts))
	binary.BigEndian.PutUint16(body[0:], 1)
	binary.BigEndian.PutUint16(body[4:], uint16(len(firsts)))
	for i, g := range firsts {
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(len(body)))
		body = append(body, ruleSet(encodeSeqRules(rules[g]))...)
	}
	covOff := len(body)
	body = append(body, coverageFormat1(firsts)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	return body
}

// SequenceContext2 is a lookup type 5 format 2 subtable: rules selected by the
// *class* of the first glyph, matching the rest by class. This is how a font
// states one rule for a whole group of glyphs.
func SequenceContext2(coverage []int, classes map[int]int, rules map[int][]ContextRule) []byte {
	n := 1
	for c := range rules {
		if c+1 > n {
			n = c + 1
		}
	}
	body := make([]byte, 8+2*n)
	binary.BigEndian.PutUint16(body[0:], 2)
	binary.BigEndian.PutUint16(body[6:], uint16(n))
	for c := 0; c < n; c++ {
		if len(rules[c]) == 0 {
			continue // a zero offset means this class has no rules
		}
		binary.BigEndian.PutUint16(body[8+2*c:], uint16(len(body)))
		body = append(body, ruleSet(encodeSeqRules(rules[c]))...)
	}
	cov := append([]int(nil), coverage...)
	sortInts(cov)
	covOff := len(body)
	body = append(body, coverageFormat1(cov)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	clsOff := len(body)
	body = append(body, classDefFormat2(classes)...)
	binary.BigEndian.PutUint16(body[4:], uint16(clsOff))
	return body
}

// SequenceContext3 is a lookup type 5 format 3 subtable: one coverage table per
// matched position, and a single set of lookups.
func SequenceContext3(coverages [][]int, lookups []SeqLookup) []byte {
	body := make([]byte, 6+2*len(coverages))
	binary.BigEndian.PutUint16(body[0:], 3)
	binary.BigEndian.PutUint16(body[2:], uint16(len(coverages)))
	binary.BigEndian.PutUint16(body[4:], uint16(len(lookups)))
	body = append(body, seqLookupRecords(lookups)...)
	for i, cov := range coverages {
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(len(body)))
		body = append(body, sortedCoverage(cov)...)
	}
	return body
}

// ChainedContext1 is a lookup type 6 format 1 subtable: rules selected by the
// first matched glyph, with what precedes and follows matched by glyph.
func ChainedContext1(rules map[int][]ChainRule) []byte {
	firsts := intKeys(rules)
	body := make([]byte, 6+2*len(firsts))
	binary.BigEndian.PutUint16(body[0:], 1)
	binary.BigEndian.PutUint16(body[4:], uint16(len(firsts)))
	for i, g := range firsts {
		binary.BigEndian.PutUint16(body[6+2*i:], uint16(len(body)))
		body = append(body, ruleSet(encodeChainRules(rules[g]))...)
	}
	covOff := len(body)
	body = append(body, coverageFormat1(firsts)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	return body
}

// ChainedContext2 is a lookup type 6 format 2 subtable. It carries three class
// definitions — one for the backtrack, one for the input, one for the lookahead
// — because a glyph may belong to a different group in each role.
func ChainedContext2(coverage []int, back, input, ahead map[int]int, rules map[int][]ChainRule) []byte {
	n := 1
	for c := range rules {
		if c+1 > n {
			n = c + 1
		}
	}
	body := make([]byte, 12+2*n)
	binary.BigEndian.PutUint16(body[0:], 2)
	binary.BigEndian.PutUint16(body[10:], uint16(n))
	for c := 0; c < n; c++ {
		if len(rules[c]) == 0 {
			continue
		}
		binary.BigEndian.PutUint16(body[12+2*c:], uint16(len(body)))
		body = append(body, ruleSet(encodeChainRules(rules[c]))...)
	}
	cov := append([]int(nil), coverage...)
	sortInts(cov)
	covOff := len(body)
	body = append(body, coverageFormat1(cov)...)
	binary.BigEndian.PutUint16(body[2:], uint16(covOff))
	// In header order, so the bytes a fixture produces do not depend on map
	// iteration: backtrack, input, lookahead.
	for i, classes := range []map[int]int{back, input, ahead} {
		off := len(body)
		body = append(body, classDefFormat2(classes)...)
		binary.BigEndian.PutUint16(body[4+2*i:], uint16(off))
	}
	return body
}

// ChainedContext3 is a lookup type 6 format 3 subtable: three lists of coverage
// tables. It is the form a modern font reaches for most.
func ChainedContext3(back, input, ahead [][]int, lookups []SeqLookup) []byte {
	body := make([]byte, 2)
	binary.BigEndian.PutUint16(body[0:], 3)
	// Each list is a count followed by its offsets; the offsets are patched once
	// the coverage tables have been placed at the end.
	type slot struct{ at, index int }
	var slots []slot
	var all [][]int
	for _, list := range [][][]int{back, input, ahead} {
		count := make([]byte, 2)
		binary.BigEndian.PutUint16(count, uint16(len(list)))
		body = append(body, count...)
		for _, cov := range list {
			slots = append(slots, slot{at: len(body), index: len(all)})
			all = append(all, cov)
			body = append(body, 0, 0)
		}
	}
	recCount := make([]byte, 2)
	binary.BigEndian.PutUint16(recCount, uint16(len(lookups)))
	body = append(body, recCount...)
	body = append(body, seqLookupRecords(lookups)...)

	offsets := make([]int, len(all))
	for i, cov := range all {
		offsets[i] = len(body)
		body = append(body, sortedCoverage(cov)...)
	}
	for _, s := range slots {
		binary.BigEndian.PutUint16(body[s.at:], uint16(offsets[s.index]))
	}
	return body
}

// encodeSeqRules encodes the rules of a sequence context.
func encodeSeqRules(rules []ContextRule) [][]byte {
	out := make([][]byte, 0, len(rules))
	for _, r := range rules {
		rule := make([]byte, 4+2*(len(r.Input)-1))
		binary.BigEndian.PutUint16(rule[0:], uint16(len(r.Input)))
		binary.BigEndian.PutUint16(rule[2:], uint16(len(r.Lookups)))
		for i, v := range r.Input[1:] {
			binary.BigEndian.PutUint16(rule[4+2*i:], uint16(v))
		}
		out = append(out, append(rule, seqLookupRecords(r.Lookups)...))
	}
	return out
}

// encodeChainRules encodes the rules of a chained sequence context, which are
// three sequences and a set of lookups laid out end to end with no offsets.
func encodeChainRules(rules []ChainRule) [][]byte {
	out := make([][]byte, 0, len(rules))
	for _, r := range rules {
		var rule []byte
		add := func(v int) {
			b := make([]byte, 2)
			binary.BigEndian.PutUint16(b, uint16(v))
			rule = append(rule, b...)
		}
		add(len(r.Backtrack))
		for _, v := range r.Backtrack {
			add(v)
		}
		add(len(r.Input))
		for _, v := range r.Input[1:] {
			add(v)
		}
		add(len(r.Lookahead))
		for _, v := range r.Lookahead {
			add(v)
		}
		add(len(r.Lookups))
		out = append(out, append(rule, seqLookupRecords(r.Lookups)...))
	}
	return out
}

// ruleSet wraps encoded rules in the count-and-offsets table that holds them.
func ruleSet(rules [][]byte) []byte {
	out := make([]byte, 2+2*len(rules))
	binary.BigEndian.PutUint16(out[0:], uint16(len(rules)))
	for i, r := range rules {
		binary.BigEndian.PutUint16(out[2+2*i:], uint16(len(out)))
		out = append(out, r...)
	}
	return out
}

func seqLookupRecords(recs []SeqLookup) []byte {
	out := make([]byte, 4*len(recs))
	for i, r := range recs {
		binary.BigEndian.PutUint16(out[4*i:], uint16(r.At))
		binary.BigEndian.PutUint16(out[4*i+2:], uint16(r.Lookup))
	}
	return out
}

func sortedCoverage(glyphs []int) []byte {
	g := append([]int(nil), glyphs...)
	sortInts(g)
	return coverageFormat1(g)
}

func intKeys[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortInts(out)
	return out
}
