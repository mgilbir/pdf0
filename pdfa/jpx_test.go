package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestJPXClauseIsPinnedPerLevel, because nothing else pins it.
//
// The corpus does not: making jpxClause answer 6.2.8.3 at PDF/A-4 leaves
// falsePositives=0 missed=0, since the corpus comparison is about which files
// are reported and not about the clause each report cites. The clause is what
// a caller reads to look the rule up, so a wrong one sends them to the wrong
// page of the wrong part of the standard.
//
// There is deliberately no PDF/A-1 answer. JPXDecode is not a permitted filter
// at that level, so a JPEG 2000 image is reported by the filter check under
// 6.1.10 and checkJPXImages returns before asking. The clause table used to
// carry 6.2.4 in that slot, which no call could ever reach.
func TestJPXClauseIsPinnedPerLevel(t *testing.T) {
	for level, want := range map[Level]string{
		PDFA2b: "6.2.8.3",
		PDFA3b: "6.2.8.3",
		PDFA4:  "6.2.7.3",
	} {
		if got := jpxClause(level); got != want {
			t.Errorf("%s cites %s for the JPEG 2000 rules, want %s", level, got, want)
		}
	}

	// And the level with no answer never reaches the question. The image has
	// two colour channels, which is a violation of the rule above at every
	// level that has one — so if PDF/A-1 stopped returning early this would
	// have a clause to cite and no correct one to give.
	var jp2 []byte
	ihdr := []byte{
		0, 0, 0, 1, // height
		0, 0, 0, 1, // width
		0, 2, // NC: two channels, which is not 1, 3 or 4
		7, // BPC: eight bits, which is fine
	}
	jp2 = append(jp2, 0, 0, 0, byte(8+len(ihdr)))
	jp2 = append(jp2, "ihdr"...)
	jp2 = append(jp2, ihdr...)

	stream := &object.Stream{Dict: object.Dictionary{}, Data: jp2}
	stream.Dict.Set("Filter", object.Name("JPXDecode"))
	trailer := object.Dictionary{}
	trailer.Set("Root", object.IndirectRef{Number: 1})
	doc := mkView(map[int]*object.IndirectObject{
		2: {Number: 2, Value: stream},
	}, &trailer)

	if errs := checkJPXImages(doc, PDFA2b); !hasMessage(errs, "2 colour channels") {
		t.Fatalf("the fixture does not violate anything at PDF/A-2b, so the "+
			"assertion below would pass on any code at all: %v", errs)
	}
	if errs := checkJPXImages(doc, PDFA1b); len(errs) != 0 {
		t.Errorf("PDF/A-1b reported %d JPEG 2000 content violations; the filter "+
			"check owns that level and there is no image clause to cite: %v",
			len(errs), errs)
	}
}
