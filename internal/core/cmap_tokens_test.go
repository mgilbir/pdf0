package core

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// A CMap program is PostScript, and it is read as tokens (audit 2026-09-22
// C75, C76, C78). Each test here is a layout the text-searching readers got
// wrong: several entries on a line, CR alone between lines, single codes of
// different widths, keywords inside comments, and array-form bfrange.

// TestCMapEntriesAreTokensNotLines is C78. The line-based reader took one
// cidrange or cidchar entry per LF-separated line: two entries on a line came
// back as one range from the first's low code to the second's high code mapped
// to the last number on the line, and a file with CR line ends was one line.
func TestCMapEntriesAreTokensNotLines(t *testing.T) {
	for name, src := range map[string]string{
		"two ranges on a line": "1 begincodespacerange <00> <FF> endcodespacerange\n" +
			"2 begincidrange\n<00> <7F> 0 <80> <FF> 200\nendcidrange\n",
		"CR line ends": "1 begincodespacerange\r<00> <FF>\rendcodespacerange\r" +
			"2 begincidrange\r<00> <7F> 0\r<80> <FF> 200\rendcidrange\r",
	} {
		c, r := ParseCMap(src)
		if r != ReasonOK {
			t.Errorf("%s: the CMap did not parse", name)
			continue
		}
		for _, tc := range []struct {
			b   byte
			cid int
		}{{0x00, 0}, {0x7F, 127}, {0x80, 200}, {0xFF, 327}} {
			got := c.Decode([]byte{tc.b})
			if len(got) != 1 || !got[0].Mapped || got[0].CID != tc.cid {
				t.Errorf("%s: code %#02x decoded to %+v, want CID %d", name, tc.b, got, tc.cid)
			}
		}
	}
}

// TestCMapKeywordsRunTogether: a producer in the veraPDF corpus writes its
// CMaps with no white space after a keyword ("endcodespacerange32
// begincidrange", "endcidrangeendcmap"). PostScript would read each run as one
// unknown name; the reader splits a run that begins with a CMap keyword into
// its keywords and the count that follows, as the text search it replaces
// effectively did.
func TestCMapKeywordsRunTogether(t *testing.T) {
	c, r := ParseCMap("/CIDInit /ProcSet findresourcebegin12 dictbeginbegincmap/WMode 1 def" +
		"1 begincodespacerange<0000> <47ff> endcodespacerange2 begincidrange<0000> <00ff> 0 <3f00> <3fff> 16536 " +
		"endcidrangeendcmapCMapNamecurrentdict/CMap defineresourcepopendend")
	if r != ReasonOK {
		t.Fatal("the CMap did not parse")
	}
	if got := c.Decode([]byte{0x3F, 0x29}); len(got) != 1 || !got[0].Mapped || got[0].CID != 16577 {
		t.Errorf("<3f29> decoded to %+v, want CID 16577", got)
	}
	if m, found := CMapWMode(Canceler{}, []byte("/WMode 1 def1 begincodespacerange")); !found || m != 1 {
		t.Errorf("WMode = %d, %v; want 1", m, found)
	}
}

// TestCMapNotdefMappings: a code the CID mappings leave unmapped stands for
// the CID its notdef mapping names (9.7.6.3), and a notdef range maps every
// code in it to that one CID. KSCms-UHC-H, embedded by a TWG pass file, maps
// its control codes <00>-<1F> this way.
func TestCMapNotdefMappings(t *testing.T) {
	c, r := ParseCMap("2 begincodespacerange <00> <80> <8141> <FEFE> endcodespacerange\n" +
		"1 beginnotdefrange <00> <1f> 1 endnotdefrange\n1 beginnotdefchar <7f> 2 endnotdefchar\n" +
		"1 begincidrange <20> <7e> 1 endcidrange\n")
	if r != ReasonOK {
		t.Fatal("the CMap did not parse")
	}
	for b, want := range map[byte]int{0x01: 1, 0x1f: 1, 0x21: 2, 0x7f: 2} {
		if got := c.Decode([]byte{b}); len(got) != 1 || !got[0].Mapped || got[0].CID != want {
			t.Errorf("code %#02x decoded to %+v, want CID %d", b, got, want)
		}
	}
}

// TestCMapSingleCodesKeepTheirWidth is C78's other half: <41> and <0041> are
// different codes, one byte and two. Keyed by value alone, the second cidchar
// overwrote the first and the one-byte code read as the two-byte one's CID.
func TestCMapSingleCodesKeepTheirWidth(t *testing.T) {
	c, r := ParseCMap("2 begincodespacerange <00> <7F> <8000> <FFFF> endcodespacerange\n" +
		"2 begincidchar <41> 10 <0041> 20 endcidchar\n" +
		"1 begincidchar <8041> 30 endcidchar\n")
	if r != ReasonOK {
		t.Fatal("the CMap did not parse")
	}
	if got := c.Decode([]byte{0x41}); len(got) != 1 || got[0].CID != 10 {
		t.Errorf("one-byte <41> decoded to %+v, want CID 10", got)
	}
	if got := c.Decode([]byte{0x80, 0x41}); len(got) != 1 || got[0].CID != 30 {
		t.Errorf("two-byte <8041> decoded to %+v, want CID 30", got)
	}
}

// TestCMapKeywordsInCommentsAreComments is C75. A comment that mentions
// usecmap refused the whole CMap — silently, so the CIDSet and coverage checks
// that needed it never ran — and a comment naming a section keyword opened a
// section that swallowed what followed.
func TestCMapKeywordsInCommentsAreComments(t *testing.T) {
	src := "% this CMap does not usecmap anything\n" +
		"% begincidrange <00> <FF> 999 endcidrange\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"1 begincidrange <00> <FF> 1 endcidrange\n"
	c, r := ParseCMap(src)
	if r != ReasonOK {
		t.Fatal("a comment mentioning usecmap refused the CMap")
	}
	if got := c.Decode([]byte{0x05}); len(got) != 1 || got[0].CID != 6 {
		t.Errorf("code 05 decoded to %+v, want CID 6 (the commented-out range is not a range)", got)
	}
}

// TestToUnicodeArrayBFRangeKeepsStep is C76. An array-form bfrange entry is
// one operand; counting hex strings in threes made the next entry's source
// code read as a destination, and <0000> — a code, not a target — was reported
// as a mapping to U+0000.
func TestToUnicodeArrayBFRangeKeepsStep(t *testing.T) {
	body := "2 beginbfrange\n<0001> <0002> [<0041> <0042>]\n<0000> <0000> <0020>\nendbfrange\n"
	v, st := toUnicodeView(body)
	if HasForbiddenUnicodeTargets(v, st) {
		t.Error("an array-form bfrange followed by a <0000> source code was reported as a mapping to U+0000")
	}
	runes, _ := ParseToUnicodeRunes(v, toUnicodeFont()) // reason: an unfiltered stream in a test; the map is what is asserted
	for code, want := range map[int]string{0: " ", 1: "A", 2: "B"} {
		if string(runes[code]) != want {
			t.Errorf("code %d maps to %q, want %q", code, string(runes[code]), want)
		}
	}
	// And a real forbidden target is still found, in an array too.
	v, st = toUnicodeView("1 beginbfrange <0001> <0002> [<0041> <FEFF>] endbfrange")
	if !HasForbiddenUnicodeTargets(v, st) {
		t.Error("a U+FEFF destination inside an array was not found")
	}
}

func toUnicodeFont() *object.Dictionary {
	f := &object.Dictionary{}
	f.Set("ToUnicode", object.IndirectRef{Number: 1})
	return f
}

func toUnicodeView(body string) (View, *object.Stream) {
	st := &object.Stream{Dict: object.Dictionary{}, Data: []byte(body)}
	st.Dict.Set("Length", object.Integer(len(body)))
	return View{
		Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: st}},
		Limits:  DefaultLimits(),
		Run:     NewRun(&Recorder{}),
	}, st
}

// TestRefusedEmbeddedCMapIsReported is the other half of C75: when an embedded
// CMap cannot be read, or leaves a code the document shows to a CMap that
// cannot be, the checks that need it are skipped, and the skip is reported
// rather than taken silently (the producer records it, once). A CMap that is
// read, and a code it defines itself, report nothing — nor does a CMap that is
// malformed, which is the file's fault and a rule's to report, not a skip.
func TestRefusedEmbeddedCMapIsReported(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		reason Reason
		trips  int
		decode []byte
	}{
		"no codespace, malformed":                 {"1 begincidrange <00> <FF> 1 endcidrange", ReasonMalformed, 0, nil},
		"builds on an unknown CMap, no codespace": {"/No-Such-CMap usecmap 1 begincidrange <00> <FF> 1 endcidrange", ReasonUnsupported, 1, nil},
		"builds on predefined, no own codespace":  {"/UniJIS-UCS2-H usecmap 1 begincidrange <0041> <0041> 1 endcidrange", ReasonOK, 1, []byte{0x00, 0x42}},
		"a code left to an unknown CMap":          {"/No-Such-CMap usecmap 1 begincodespacerange <00> <FF> endcodespacerange 1 begincidchar <41> 7 endcidchar", ReasonOK, 1, []byte{0x42}},
		"a code left to a predefined CMap":        {"/UniJIS-UCS2-H usecmap 1 begincodespacerange <00> <FF> endcodespacerange 1 begincidchar <41> 7 endcidchar", ReasonOK, 1, []byte{0x42}},
		"a code the CMap defines itself":          {"/UniJIS-UCS2-H usecmap 1 begincodespacerange <00> <FF> endcodespacerange 1 begincidchar <41> 7 endcidchar", ReasonOK, 0, []byte{0x41}},
	} {
		body := tc.body
		st := &object.Stream{Dict: object.Dictionary{}, Data: []byte(body)}
		st.Dict.Set("Length", object.Integer(len(body)))
		font := &object.Dictionary{}
		font.Set("Subtype", object.Name("Type0"))
		font.Set("Encoding", object.IndirectRef{Number: 2})
		rec := &Recorder{}
		v := View{
			Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: font}, 2: {Number: 2, Value: st}},
			Limits:  DefaultLimits(),
			Run:     NewRun(rec),
		}
		c, r := LoadCMap(v, font)
		if r != tc.reason {
			t.Errorf("%s: reason %v, want %v", name, r, tc.reason)
			continue
		}
		if r == ReasonOK {
			c.Decode(tc.decode)
			c.Decode(tc.decode) // reported once, however often it is met
		}
		trips := rec.Snapshot()
		if len(trips) != tc.trips {
			t.Errorf("%s: %d trips, want %d: %v", name, len(trips), tc.trips, trips)
			continue
		}
		if tc.trips == 0 {
			continue
		}
		if msg := trips[0].Message(); !strings.Contains(msg, "skipped") || trips[0].Obj != 1 {
			t.Errorf("%s: trip on object %d: %s", name, trips[0].Obj, msg)
		}
	}
}

// TestCMapBaseStreamIsRead is the shape of TWG A025-pdfa2-pass-a, a
// conforming file: an embedded CMap whose usecmap names a predefined CMap and
// whose /UseCMap carries that CMap, embedded. The base is read, so every code
// the page shows has a CID — here through the base's notdefrange — and nothing
// is skipped or reported.
func TestCMapBaseStreamIsRead(t *testing.T) {
	baseBody := "/CMapName /KSCms-UHC-H def 2 begincodespacerange <00> <80> <8141> <FEFE> endcodespacerange\n" +
		"1 beginnotdefrange <00> <1f> 1 endnotdefrange 1 begincidrange <20> <7e> 1 endcidrange"
	topBody := "/KSCms-UHC-H usecmap /CMapName /Adobe-Korea1-2 def 1 begincidrange <8141> <815a> 9333 endcidrange"
	base := &object.Stream{Dict: object.Dictionary{}, Data: []byte(baseBody)}
	base.Dict.Set("Length", object.Integer(len(baseBody)))
	top := &object.Stream{Dict: object.Dictionary{}, Data: []byte(topBody)}
	top.Dict.Set("Length", object.Integer(len(topBody)))
	top.Dict.Set("UseCMap", object.IndirectRef{Number: 3})
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("Type0"))
	font.Set("Encoding", object.IndirectRef{Number: 2})
	rec := &Recorder{}
	v := View{
		Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: font}, 2: {Number: 2, Value: top}, 3: {Number: 3, Value: base}},
		Limits:  DefaultLimits(),
		Run:     NewRun(rec),
	}
	c, r := LoadCMap(v, font)
	if r != ReasonOK {
		t.Fatalf("reason %v, want ok", r)
	}
	for _, tc := range []struct {
		in  []byte
		cid int
	}{{[]byte{0x01}, 1}, {[]byte{0x41}, 34}, {[]byte{0x81, 0x42}, 9334}} {
		got := c.Decode(tc.in)
		if len(got) != 1 || !got[0].Mapped || got[0].Unknown || got[0].CID != tc.cid {
			t.Errorf("% x decoded to %+v, want CID %d", tc.in, got, tc.cid)
		}
	}
	if tr := rec.Snapshot(); len(tr) != 0 {
		t.Errorf("trips %v, want none", tr)
	}
}
