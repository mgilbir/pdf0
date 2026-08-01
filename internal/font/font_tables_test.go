package font

import (
	"testing"
)

// The CFF standard-strings table and the Annex D encoding tables were
// transcribed from their specifications by hand: unlike the spec-example JSON
// (cmd/extract_spec_examples) there is no committed generator, and unlike the
// XMP schema tables (xmp_rng_test.go) there is no external schema to diff them
// against. Nothing else in the suite would notice if an entry were edited,
// deleted or shifted — and a silent shift is the dangerous one, because every
// glyph name after the edit changes meaning while the code keeps working.
//
// These tests pin the failure modes that a shift produces: the table lengths,
// the values at both ends, and the handful of interior codes where the
// encodings genuinely disagree with each other. They are not a substitute for
// the specification, only a tripwire.

// TestCFFStandardStringsShape pins the 391 predefined CFF strings (Adobe
// Technical Note #5176, Appendix A). SID 0 and SID 390 bracket the table, and
// the interior anchors catch a shift that leaves both ends intact.
func TestCFFStandardStringsShape(t *testing.T) {
	if got, want := len(cffStandardStrings), 391; got != want {
		t.Fatalf("cffStandardStrings has %d entries, want %d (TN #5176 Appendix A)", got, want)
	}
	for _, tc := range []struct {
		sid  int
		want string
	}{
		{0, ".notdef"},
		{1, "space"},
		{5, "dollar"},
		{95, "asciitilde"}, // SIDs 1..95 are codes 32..126, so this ends the ASCII run
		{229, "exclamsmall"},
		{390, "Semibold"}, // last standard string
	} {
		if got := cffStandardStrings[tc.sid]; got != tc.want {
			t.Errorf("cffStandardStrings[%d] = %q, want %q", tc.sid, got, tc.want)
		}
	}
}

// TestEncodingTablesDisagreeWhereTheyShould pins the codes where the Annex D.2
// encodings differ from one another. These are exactly the entries a
// copy-paste between tables would flatten, and the difference is semantic: a
// font declaring StandardEncoding maps 39 to a right single quote, while
// WinAnsiEncoding maps it to a vertical apostrophe.
func TestEncodingTablesDisagreeWhereTheyShould(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table map[byte]string
		code  byte
		want  string
	}{
		{"standard", StandardEncodingNames, 39, "quoteright"},
		{"standard", StandardEncodingNames, 96, "quoteleft"},
		{"winansi", WinAnsiEncodingNames, 39, "quotesingle"},
		{"winansi", WinAnsiEncodingNames, 96, "grave"},
		{"winansi", WinAnsiEncodingNames, 128, "Euro"},
		{"macroman", MacRomanEncodingNames, 39, "quotesingle"},
		{"macroman", MacRomanEncodingNames, 96, "grave"},
	} {
		if got := tc.table[tc.code]; got != tc.want {
			t.Errorf("%sEncodingNames[%d] = %q, want %q", tc.name, tc.code, got, tc.want)
		}
	}

	// Every table must agree on the unambiguous ASCII core; a shift shows up
	// here immediately.
	for _, tbl := range []struct {
		name  string
		table map[byte]string
	}{
		{"standard", StandardEncodingNames},
		{"winansi", WinAnsiEncodingNames},
		{"macroman", MacRomanEncodingNames},
	} {
		for code, want := range map[byte]string{
			32: "space", 48: "zero", 65: "A", 90: "Z", 97: "a", 122: "z",
		} {
			if got := tbl.table[code]; got != want {
				t.Errorf("%sEncodingNames[%d] = %q, want %q", tbl.name, code, got, want)
			}
		}
	}
}
