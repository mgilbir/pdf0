package core

import (
	"github.com/mgilbir/pdf0/internal/hostile"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/object"
)

// Reading a CMap, which is how a Type 0 font's bytes become glyph references.
//
// Every test here is about one of the two things a CMap says — where a code
// ends, and which CID it names — because a checker that gets either wrong is
// asking about the wrong glyph and will say so with confidence.

const mixedWidthCMap = `
/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 2 >> def
/CMapName /Test-H def
/CMapType 1 def
2 begincodespacerange
<00> <80>
<8140> <9FFC>
endcodespacerange
2 begincidrange
<20> <7E> 231
<8140> <817E> 633
endcidrange
1 begincidchar
<8180> 700
endcidchar
endcmap
CMapName currentdict /CMap defineresource pop
end
end
`

// TestCMapCutsCodesByTheirFirstByte is §9.7.6.2, and the rule a naive reader
// gets wrong.
//
// The length comes from the first byte alone. A mixed-width CMap has a one-byte
// space and a two-byte one, and which a code belongs to is decided before
// anything asks whether the code is in range.
func TestCMapCutsCodesByTheirFirstByte(t *testing.T) {
	c, r := ParseCMap(mixedWidthCMap)
	if r != ReasonOK {
		t.Fatal("the CMap did not parse")
	}

	// "A" is one byte; 0x81 0x41 is two.
	got := c.Decode([]byte{0x41, 0x81, 0x41, 0x42})
	if len(got) != 3 {
		t.Fatalf("%d codes from four bytes, want 3: %+v", len(got), got)
	}
	if got[0].Bytes != 1 || got[0].Value != 0x41 {
		t.Errorf("the first code is %d bytes of %#x, want 1 byte of 0x41", got[0].Bytes, got[0].Value)
	}
	if got[1].Bytes != 2 || got[1].Value != 0x8141 {
		t.Errorf("the second code is %d bytes of %#x, want 2 bytes of 0x8141", got[1].Bytes, got[1].Value)
	}
	if got[2].Bytes != 1 || got[2].Value != 0x42 {
		t.Errorf("the third code is %d bytes of %#x, want 1 byte of 0x42", got[2].Bytes, got[2].Value)
	}
}

// TestCMapLengthIsNotContainment is the same rule at the point it bites.
//
// 0x81 0x20 begins in the two-byte range's first byte and its value is below
// the range, so it is a two-byte code that maps to nothing. Reading it as one
// byte — which is what deciding by containment does — leaves the 0x20 to be
// read as a code of its own, and every code after it in the string is wrong.
func TestCMapLengthIsNotContainment(t *testing.T) {
	c, _ := ParseCMap(mixedWidthCMap)
	got := c.Decode([]byte{0x81, 0x20, 0x41})
	if len(got) != 2 {
		t.Fatalf("%d codes, want 2 — the invalid two-byte code and the 'A' after "+
			"it: %+v", len(got), got)
	}
	if got[0].Bytes != 2 {
		t.Errorf("0x8120 was read as %d bytes, want 2; the string desynchronises "+
			"from here", got[0].Bytes)
	}
	if got[0].Mapped {
		t.Errorf("0x8120 mapped to CID %d; it is outside every cidrange", got[0].CID)
	}
	if got[1].Value != 0x41 || got[1].Bytes != 1 {
		t.Errorf("the code after it is %#x of %d bytes, want 0x41 of 1", got[1].Value, got[1].Bytes)
	}
}

// TestCMapMapsRangesAndSingles: a cidrange counts up from its start, and a
// cidchar names one code.
func TestCMapMapsRangesAndSingles(t *testing.T) {
	c, _ := ParseCMap(mixedWidthCMap)
	for _, tc := range []struct {
		in   []byte
		cid  int
		what string
	}{
		{[]byte{0x20}, 231, "the start of a range"},
		{[]byte{0x41}, 231 + 0x41 - 0x20, "the middle of a range"},
		{[]byte{0x7E}, 231 + 0x7E - 0x20, "the end of a range"},
		{[]byte{0x81, 0x40}, 633, "the start of the two-byte range"},
		{[]byte{0x81, 0x7E}, 633 + 0x3E, "the end of the two-byte range"},
		{[]byte{0x81, 0x80}, 700, "a cidchar"},
	} {
		got := c.Decode(tc.in)
		if len(got) != 1 {
			t.Errorf("%s: %d codes, want 1", tc.what, len(got))
			continue
		}
		if !got[0].Mapped || got[0].CID != tc.cid {
			t.Errorf("%s: % x mapped to %d (mapped=%v), want %d",
				tc.what, tc.in, got[0].CID, got[0].Mapped, tc.cid)
		}
	}
	// Just past the one-byte range's end, and inside the codespace: a code the
	// document may write and the CMap does not define.
	if got := c.Decode([]byte{0x1F}); len(got) != 1 || got[0].Mapped {
		t.Errorf("0x1F mapped to %+v; it is in the codespace and in no cidrange", got)
	}
}

// TestIdentityCMapIsTwoBytesAndItself, since it is the case every other
// document uses and the one a mistake here would break.
func TestIdentityCMapIsTwoBytesAndItself(t *testing.T) {
	c := IdentityCMap()
	if !c.Identity() {
		t.Error("the identity CMap does not say it is one")
	}
	got := c.Decode([]byte{0x00, 0x41, 0xFF, 0xFE})
	if len(got) != 2 {
		t.Fatalf("%d codes from four bytes, want 2", len(got))
	}
	if got[0].CID != 0x0041 || got[1].CID != 0xFFFE {
		t.Errorf("CIDs are %d and %d, want 65 and 65534", got[0].CID, got[1].CID)
	}
	for _, g := range got {
		if !g.Mapped || g.Bytes != 2 {
			t.Errorf("code %#x: mapped=%v bytes=%d, want true and 2", g.Value, g.Mapped, g.Bytes)
		}
	}
	// An odd trailing byte is still reported rather than dropped, so a caller
	// counting codes sees the incomplete one.
	if got := c.Decode([]byte{0x00, 0x41, 0x00}); len(got) != 2 {
		t.Errorf("three bytes gave %d codes, want 2 — the pair and the stray", len(got))
	}
}

// TestACMapWithNoCodespaceIsRefused. Without one nothing can be cut into
// codes, and a map that answers for no code is worse than no map: it reports
// every string as defining nothing.
func TestACMapWithNoCodespaceIsRefused(t *testing.T) {
	if _, r := ParseCMap("begincmap\n1 begincidrange\n<20> <7E> 1\nendcidrange\nendcmap"); r != ReasonMalformed {
		t.Error("a CMap with no codespacerange was accepted")
	}
	if _, r := ParseCMap(""); r != ReasonMalformed {
		t.Error("an empty CMap was accepted")
	}
	if _, r := ParseCMap("not a cmap at all"); r != ReasonMalformed {
		t.Error("a non-CMap was accepted")
	}
}

// TestACMapThatDefersToAnotherKnowsWhatItDoesNotKnow: usecmap names a
// predefined CMap, which is data this module does not carry. The CMap used to
// be refused whole, because the result would be a map with holes and no way to
// tell a hole from a code the document really left undefined. That way now
// exists: a code the CMap's own entries define is mapped, and one it leaves to
// the base is Unknown — never "undefined".
func TestACMapThatDefersToAnotherKnowsWhatItDoesNotKnow(t *testing.T) {
	src := `begincmap
/UniJIS-UCS2-H usecmap
1 begincodespacerange
<0000> <7FFF>
endcodespacerange
1 begincidchar
<0041> 34
endcidchar
endcmap`
	c, r := ParseCMap(src)
	if r != ReasonOK {
		t.Fatal("the CMap was refused")
	}
	got := c.Decode([]byte{0x00, 0x41, 0x00, 0x42, 0x90, 0x00})
	if len(got) != 3 {
		t.Fatalf("%d codes, want 3: %+v", len(got), got)
	}
	if !got[0].Mapped || got[0].CID != 34 || got[0].Unknown {
		t.Errorf("<0041>, which the CMap defines, decoded to %+v", got[0])
	}
	if got[1].Mapped || !got[1].Unknown {
		t.Errorf("<0042>, which it leaves to UniJIS-UCS2-H, decoded to %+v; want Unknown, not undefined", got[1])
	}
	// 90 is outside this CMap's codespace and may be inside the base's, so
	// neither its length nor anything after it can be known.
	if !got[2].Unknown || got[2].Bytes != 2 {
		t.Errorf("a byte outside the known codespace decoded to %+v; want one Unknown code for the rest", got[2])
	}
}

// TestACMapCannotBeAskedForUnboundedWork is the security bound.
//
// A CMap arrives in a document. "<0000> <FFFFFFFF> 1" is eleven bytes and asks
// for four billion inserts if the range is expanded, and a file containing it
// costs nothing to make. Ranges are kept as ranges, and one wider than the
// bound refuses the whole map rather than being truncated — a truncated map
// answers "undefined" for codes it simply did not reach.
func TestACMapCannotBeAskedForUnboundedWork(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		huge := `begincmap
1 begincodespacerange
<00000000> <FFFFFFFF>
endcodespacerange
1 begincidrange
<00000000> <FFFFFFFF> 1
endcidrange
endcmap`
		if _, r := ParseCMap(huge); r != ReasonLimit {
			t.Error("a cidrange spanning four billion codes was accepted")
		}

		// And the bound is not so tight that a real CMap trips it: Adobe's largest
		// published CMaps run to a few thousand entries.
		var b []byte
		b = append(b, "begincmap\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n"...)
		b = append(b, "begincidrange\n"...)
		for i := 0; i < 3000; i++ {
			b = append(b, []byte("<"+hex4(i*16)+"> <"+hex4(i*16+15)+"> 1\n")...)
		}
		b = append(b, "endcidrange\nendcmap"...)
		if _, r := ParseCMap(string(b)); r != ReasonOK {
			t.Error("a three-thousand-range CMap was refused; the bound is too tight " +
				"for a real font")
		}
	})
}

func hex4(v int) string {
	const d = "0123456789ABCDEF"
	return string([]byte{d[(v>>12)&15], d[(v>>8)&15], d[(v>>4)&15], d[v&15]})
}

// TestToUnicodeReadsEveryEntryNotJustTheFirstOfEachLine.
//
// Nothing requires one mapping per line, and font tools do not write it that
// way. This reader took the first entry of each line and dropped the rest, so a
// CMap with its table on one line — which is how several producers emit them —
// came back with a single mapping in it. Its sibling ParseToUnicodeRunes
// already consumed the body as a flat stream and documented why; the two now
// agree.
//
// It is not a cosmetic difference. The map decides whether an empty glyph is
// allowed to be empty, so a lost entry turns "this code is a space" into "this
// code is unknown" and a conforming file into a reported one.
func TestToUnicodeReadsEveryEntryNotJustTheFirstOfEachLine(t *testing.T) {
	got := parseToUnicode("beginbfchar <0041> <0061> <0042> <0062> <0043> <0063> endbfchar")
	for code, want := range map[int]rune{0x41: 'a', 0x42: 'b', 0x43: 'c'} {
		if got[code] != want {
			t.Errorf("code %#x mapped to %q, want %q — entries after the first on "+
				"a line were dropped", code, got[code], want)
		}
	}
	if len(got) != 3 {
		t.Errorf("%d mappings, want 3: %v", len(got), got)
	}

	// Across lines as well as along them, and ranges the same way.
	got = parseToUnicode("beginbfrange\n<0041> <0043> <0061> <0050> <0051> <0070>\nendbfrange")
	for code, want := range map[int]rune{0x41: 'a', 0x42: 'b', 0x43: 'c', 0x50: 'p', 0x51: 'q'} {
		if got[code] != want {
			t.Errorf("code %#x mapped to %q, want %q", code, got[code], want)
		}
	}

	// And an entry split across a line break, which the flat-stream reading
	// handles and a line-at-a-time one cannot.
	got = parseToUnicode("beginbfchar <0041>\n<0061> endbfchar")
	if got[0x41] != 'a' {
		t.Errorf("a mapping split across a line break was lost: %v", got)
	}
}

// parseToUnicode runs a ToUnicode CMap body through the reader, which needs a
// font dictionary and a document to hang the stream on.
func parseToUnicode(body string) map[int]rune {
	st := &object.Stream{Dict: object.Dictionary{}, Data: []byte(body)}
	st.Dict.Set("Length", object.Integer(len(body)))
	fontDict := &object.Dictionary{}
	fontDict.Set("ToUnicode", object.IndirectRef{Number: 1})
	doc := View{
		Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: st}},
		Limits:  DefaultLimits(),
		Run:     NewRun(nil),
	}
	tu, _ := ParseToUnicode(doc, fontDict) // reason: an unfiltered stream in a test; the map is what is asserted
	return tu.firstMap()
}

// firstMap expands t into the map ParseToUnicodeMap used to return — every
// mapped code to its first rune, U+0000 left out — for tests to assert on.
// Tests only: it is the expansion ToUnicode exists to avoid.
func (t *ToUnicode) firstMap() map[int]rune {
	m := map[int]rune{}
	if t == nil {
		return m
	}
	for _, sp := range t.spans {
		for c := uint64(sp.Lo); c <= uint64(sp.Hi); c++ {
			if r, ok := t.First(int(c)); ok {
				m[int(c)] = r
			}
		}
	}
	return m
}
