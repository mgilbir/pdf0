package pdfa

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// A skipped check has to say so.
//
// Three font rules need a code-to-CID mapping — whether every code shown has a
// glyph, whether .notdef is drawn, and whether /W agrees with the program's own
// advances. For a font whose /Encoding names a predefined CJK CMap, this module
// has no such mapping and the rules cannot run. That is the right answer;
// reading UniJIS-UCS2-H as Identity would report every glyph on the page as
// missing.
//
// What was wrong is that it happened silently. A caller got no violations for
// the font and no way to tell "checked and clean" from "not checked", which is
// the one thing a validator must never blur. The sibling case — a /W range too
// wide to expand — has reported itself through the guard mechanism all along.

// predefinedCMapFont builds a Type 0 font whose /Encoding is enc.
func predefinedCMapFont(enc object.Object) (*object.Dictionary, core.View, *core.FontTextUsage) {
	fd := &object.Dictionary{}
	fd.Set("Flags", object.Integer(4))
	fd.Set("FontFile2", object.IndirectRef{Number: 9})
	desc := &object.Dictionary{}
	desc.Set("Subtype", object.Name("CIDFontType2"))
	desc.Set("FontDescriptor", fd)
	desc.Set("CIDToGIDMap", object.Name("Identity"))
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("Type0"))
	font.Set("Encoding", enc)
	font.Set("DescendantFonts", object.Array{desc})

	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: &object.Stream{Dict: object.Dictionary{}, Data: cidTestProgram()}},
	}})
	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{{0x30, 0x42}}, Modes: map[int]bool{0: true}}
	return font, doc, u
}

func guardsRaised(doc core.View) []string {
	var out []string
	for _, t := range doc.Run.Trips.Snapshot() {
		out = append(out, t.Guard())
	}
	return out
}

func raised(doc core.View, guard string) bool {
	for _, g := range guardsRaised(doc) {
		if g == guard {
			return true
		}
	}
	return false
}

func TestAPredefinedCMapSkipIsReported(t *testing.T) {
	font, doc, u := predefinedCMapFont(object.Name("UniJIS-UCS2-H"))
	checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)

	if !raised(doc, core.GuardPredefinedCMap) {
		t.Fatalf("a font the checks could not run on reported nothing: %v", guardsRaised(doc))
	}

	// The report has to name the CMap and say which checks did not run, or it
	// tells a reader that something is wrong without saying what is unknown.
	var msg string
	for _, tr := range doc.Run.Trips.Snapshot() {
		if tr.Guard() == core.GuardPredefinedCMap {
			msg = tr.Message()
		}
	}
	for _, want := range []string{"UniJIS-UCS2-H", "skipped", "neither confirmed conformant nor non-conformant"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the report does not mention %q: %s", want, msg)
		}
	}
	// And it must not claim a budget was reached, which would send a reader to
	// raise a limit that does not exist.
	if strings.Contains(msg, "resource limit reached") {
		t.Errorf("a missing-data skip reports itself as a resource limit: %s", msg)
	}
	if !strings.Contains(msg, "data not carried") {
		t.Errorf("the report does not say what kind of gap it is: %s", msg)
	}
}

// TestTheCMapsThatAreAnsweredReportNothing is the other half. A guard that
// fires when nothing was skipped is worse than one that never fires: it trains
// a reader to ignore it.
func TestTheCMapsThatAreAnsweredReportNothing(t *testing.T) {
	// Identity-H is answered by LoadCMap, so nothing is skipped.
	font, doc, u := predefinedCMapFont(object.Name("Identity-H"))
	checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)
	if raised(doc, core.GuardPredefinedCMap) {
		t.Error("Identity-H was reported as a CMap the module cannot read")
	}

	// An embedded CMap stream is read, so nothing is skipped either.
	body := `/CIDInit /ProcSet findresource begin
begincmap
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0000> <FFFF> 0
endcidrange
endcmap`
	st := &object.Stream{Dict: object.Dictionary{}, Data: []byte(body)}
	st.Dict.Set("Length", object.Integer(len(body)))
	font, doc, u = predefinedCMapFont(object.IndirectRef{Number: 20})
	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: st}
	checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)
	if raised(doc, core.GuardPredefinedCMap) {
		t.Error("an embedded CMap was reported as data the module does not carry")
	}

	// A name that is not a predefined CMap at all is a violation of Table 118,
	// which the CMap-legality rule reports. Saying "not checked" as well would
	// be a weaker claim about a file already known to be wrong.
	font, doc, u = predefinedCMapFont(object.Name("Not-A-Real-CMap"))
	checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)
	if raised(doc, core.GuardPredefinedCMap) {
		t.Error("an illegal CMap name was reported as an unreadable one")
	}
}
