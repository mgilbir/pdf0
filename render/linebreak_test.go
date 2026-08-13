package render

import "testing"

// line-break, CSS Text §5.3.
//
// One of its four values is implemented and the other three are not, and the
// tests below are about both halves: what "anywhere" adds, and that reading the
// other three as auto stays quiet over text they could not have changed.

// TestLineBreakAnywhereBreaksBeforeAPreservedSpace is the value at the point it
// differs from everything else that adds opportunities.
//
// §5.3: "There is a soft wrap opportunity around every typographic character
// unit, including around any punctuation character or preserved white spaces."
// Around, so before as well as after — and before a *space* is the part neither
// break-all nor break-spaces will give, since UAX #14's LB7 keeps a space with
// the word in front of it.
//
// The three-way comparison is the assertion. The same six characters in the same
// four characters of room break in three different places, so an engine that
// read "anywhere" as either of its neighbours would give one of the other two
// answers here.
func TestLineBreakAnywhereBreaksBeforeAPreservedSpace(t *testing.T) {
	const src = `<p id="p">X XX X</p>`

	// break-spaces alone: only after a space, so the first line stops after the
	// first one and the rest fits exactly.
	root := layoutOf(t, 10000, src, widthCSS(4, "white-space: break-spaces"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "X " || got[1] != "XX X" {
		t.Errorf("break-spaces gave %q, want [\"X \" \"XX X\"]", got)
	}

	// word-break: break-all adds opportunities inside the word but still none
	// before a space, so the line stops one character short of full: it may end
	// between the two X's and not between the second and the space after them.
	root = layoutOf(t, 10000, src,
		widthCSS(4, "white-space: break-spaces; word-break: break-all"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "X X" || got[1] != "X X" {
		t.Errorf("break-all gave %q, want [\"X X\" \"X X\"]", got)
	}

	// line-break: anywhere does allow it, so the line takes all four characters
	// it has room for and the space that follows starts the next one.
	root = layoutOf(t, 10000, src,
		widthCSS(4, "white-space: break-spaces; line-break: anywhere"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "X XX" || got[1] != " X" {
		t.Errorf("line-break: anywhere gave %q, want [\"X XX\" \" X\"]", got)
	}
}

// TestLineBreakAnywhereSplitsARunOfPreservedSpaces: "around every typographic
// character unit" reaches inside a run of them too.
//
// Under pre-wrap a run of preserved spaces is one unit — it hangs or wraps
// whole — and that is the thing "anywhere" takes apart.
func TestLineBreakAnywhereSplitsARunOfPreservedSpaces(t *testing.T) {
	pieces, _ := splitAtBreaks("a    b", whiteSpaceOf("preserve"), wordBreak{},
		lineBreak{anywhere: true})
	var spaces int
	for _, p := range pieces {
		if p.space {
			spaces++
			if p.text != " " {
				t.Errorf("a space piece is %q, want a single space", p.text)
			}
		}
	}
	if spaces != 4 {
		t.Errorf("four preserved spaces came to %d pieces, want 4", spaces)
	}

	// Without it they are one piece, which is what pre-wrap means.
	pieces, _ = splitAtBreaks("a    b", whiteSpaceOf("preserve"), wordBreak{},
		lineBreak{})
	for _, p := range pieces {
		if p.space && p.text != "    " {
			t.Errorf("pre-wrap gathered the run into %q, want all four", p.text)
		}
	}
}

// TestLineBreakAnywhereBreaksMidWord: the "or in the middle of words" half,
// which is where it overlaps break-all — and unlike break-all it needs no help
// from overflow-wrap.
func TestLineBreakAnywhereBreaksMidWord(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p">abcdef</p>`,
		widthCSS(3, "line-break: anywhere"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "abc" || got[1] != "def" {
		t.Errorf("line-break: anywhere gave %q, want [\"abc\" \"def\"]", got)
	}
}

// TestTheOtherLineBreakValuesAreReportedOnlyOverCJK is the reporting rule, and
// the quiet half is the one worth pinning.
//
// loose, normal and strict are read as auto. Over Latin text that is not an
// approximation but an identity — the three differ from auto only in CJK
// strictness — and the suite has three tests (pre-wrap-004, -005 and -006) whose
// whole assertion is that "XX    XX" wraps the same under loose as under auto.
// A warning there would be a false one, and a false warning keeps a correct
// document out of the clean-pass count.
func TestTheOtherLineBreakValuesAreReportedOnlyOverCJK(t *testing.T) {
	reported := func(text, value string) string {
		t.Helper()
		rec := NewRecorder(nil)
		built := Build(Input{
			HTML: `<div id="p">` + text + `</div>`,
			CSS:  []Stylesheet{{Source: `#p { width: 40px; line-break: ` + value + ` }`}},
		})
		Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, StandardFonts(), rec)
		for _, f := range rec.Findings() {
			if f.Property == "line-break" {
				return f.Message
			}
		}
		return ""
	}
	for _, value := range []string{"loose", "normal", "strict"} {
		if got := reported("XX    XX", value); got != "" {
			t.Errorf("line-break: %s reported %q over Latin text, want silence",
				value, got)
		}
		if got := reported("\u65e5\u672c\u8a9e\u306e\u30c6\u30ad\u30b9\u30c8", value); got == "" {
			t.Errorf("line-break: %s said nothing over CJK text, want a report",
				value)
		}
	}
	// anywhere is implemented, so no text makes it worth reporting.
	for _, text := range []string{"XX    XX", "\u65e5\u672c\u8a9e\u306e\u30c6\u30ad\u30b9\u30c8"} {
		if got := reported(text, "anywhere"); got != "" {
			t.Errorf("line-break: anywhere was reported as unsupported: %s", got)
		}
	}
}
