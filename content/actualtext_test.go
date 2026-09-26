package content

import (
	"strings"
	"testing"
)

// TestActualTextSplitsTheArray pins how a marked stretch is written: the TJ
// is cut around it, the stretch is a /Span with the text inline as UTF-16BE,
// and the pieces show exactly the spans they were given.
func TestActualTextSplitsTheArray(t *testing.T) {
	var b Builder
	b.BeginText().SetFont("F1", 12).ShowTextAdjusted(
		TextSpan{Codes: []byte("a")},
		TextSpan{Adjust: 40},
		ActualTextStart("ffi😀"),
		TextSpan{Codes: []byte("X")},
		TextSpan{Adjust: -10},
		ActualTextEnd(),
		TextSpan{Codes: []byte("b")},
	).EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "BT\n/F1 12 Tf\n[(a) 40 ] TJ\n" +
		"/Span <</ActualText <FEFF006600660069D83DDE00>>> BDC\n" +
		"[(X) -10 ] TJ\nEMC\n[(b)] TJ\nET\n"
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

// TestActualTextWithNothingInIt is a stretch with no glyphs: text a
// neighbouring run drew, which must still be in the page's text.
func TestActualTextWithNothingInIt(t *testing.T) {
	var b Builder
	b.BeginText().ShowTextAdjusted(ActualTextStart("fi"), ActualTextEnd()).EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "BT\n/Span <</ActualText <FEFF00660069>>> BDC\nEMC\nET\n" {
		t.Errorf("got %q", got)
	}
}

// TestActualTextMarkersMustPair: an unbalanced marked-content sequence changes
// how the rest of the page is read, so it is refused before anything is
// written rather than left half open.
func TestActualTextMarkersMustPair(t *testing.T) {
	for name, spans := range map[string][]TextSpan{
		"unclosed": {ActualTextStart("x"), {Codes: []byte("a")}},
		"unopened": {{Codes: []byte("a")}, ActualTextEnd()},
		"nested":   {ActualTextStart("x"), ActualTextStart("y"), ActualTextEnd(), ActualTextEnd()},
	} {
		var b Builder
		b.BeginText().ShowTextAdjusted(spans...)
		if b.Err() == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(string(b.buf), "BDC") {
			t.Errorf("%s: wrote a marked-content operator before refusing: %q", name, b.buf)
		}
	}
}

// TestShowTextAdjustedWithoutMarkersIsUnchanged: a span list with no markers
// is the one TJ it always was, including the empty array for spans of zeros.
func TestShowTextAdjustedWithoutMarkersIsUnchanged(t *testing.T) {
	var b Builder
	b.BeginText().ShowTextAdjusted(TextSpan{}).ShowTextAdjusted(
		TextSpan{Codes: []byte("a")}, TextSpan{Adjust: 5}, TextSpan{Codes: []byte("b")}).EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "BT\n[] TJ\n[(a) 5 (b)] TJ\nET\n" {
		t.Errorf("got %q", got)
	}
	var outside Builder
	outside.ShowTextAdjusted(TextSpan{})
	if outside.Err() == nil {
		t.Error("TJ outside a text object was accepted")
	}
}
