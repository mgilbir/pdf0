package pdfa

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// Output-intent and ICCBased profiles, each judged once, where they are used,
// under one clause per requirement (audit 2026-09-22 C140).

func findingsWith(vs []Violation, sub string) []Violation {
	var out []Violation
	for _, v := range vs {
		if strings.Contains(v.Message, sub) {
			out = append(out, v)
		}
	}
	return out
}

// TestAnOutputIntentProfileIsNotAColourSpace: a PDF/A-2b skeleton at 1b has a
// v4 output-intent profile, which 6.2.2 reports — and no ICCBased colour
// space, so nothing is reported as one.
func TestAnOutputIntentProfileIsNotAColourSpace(t *testing.T) {
	v := mkPDFAViewT(t, PDFA2b)
	errs := ValidateView(v, PDFA1b, nil)
	if got := findingsWith(errs, "ICC profile version 4"); len(got) != 1 || got[0].Rule != "6.2.2" {
		t.Errorf("the v4 output-intent profile at 1b: want one 6.2.2 finding, got %v", got)
	}
	if got := findingsWith(errs, "ICCBased"); len(got) != 0 {
		t.Errorf("an output-intent profile was judged as an ICCBased colour space: %v", got)
	}
}

// TestAnICCBasedProfileIsFoundWhereItIsUsed: the same v4 profile used as an
// image's ICCBased colour space is judged as one, under the ICCBased clause,
// anchored to the profile, once however many arrays name it.
func TestAnICCBasedProfileIsFoundWhereItIsUsed(t *testing.T) {
	v := mkPDFAViewT(t, PDFA2b)
	cs := object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 5}}
	for n := 30; n < 32; n++ {
		v.Objects[n] = &object.IndirectObject{Number: n, Value: object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Image")},
			object.Entry{Key: "ColorSpace", Value: cs},
		), nil)}
	}
	got := findingsWith(checkICCBasedProfiles(v, PDFA1b), "ICCBased profile version 4")
	if len(got) != 1 || got[0].Rule != "6.2.3.2" || got[0].Object != 5 {
		t.Errorf("a v4 ICCBased profile at 1b: want one 6.2.3.2 finding at object 5, got %v", got)
	}
	// The same arrays at 2b: v4 is allowed there.
	if got := checkICCBasedProfiles(v, PDFA2b); len(got) != 0 {
		t.Errorf("a v4 ICCBased profile at 2b: %v", got)
	}
	// A wrong /N, written inside an Indexed base.
	bad := object.NewStream(object.NewDictionary(object.Entry{Key: "N", Value: object.Integer(2)}), nil)
	v.Objects[40] = &object.IndirectObject{Number: 40, Value: bad}
	v.Objects[31].Value.(*object.Stream).Dict.Set("ColorSpace", object.Array{object.Name("Indexed"),
		object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 40}}, object.Integer(1), object.String{}})
	if got := findingsWith(checkICCBasedProfiles(v, PDFA2b), "/N must be 1, 3, or 4"); len(got) != 1 || got[0].Object != 40 || got[0].Rule != "6.2.4.2" {
		t.Errorf("an ICCBased /N of 2 inside Indexed: %v", got)
	}
}

// TestAPDFA1IntentWithNoProfileIsOneFinding: a GTS_PDFA1 output intent with
// neither /DestOutputProfile nor /OutputConditionIdentifier is missing one
// thing, and gets one finding.
func TestAPDFA1IntentWithNoProfileIsOneFinding(t *testing.T) {
	v := mkPDFAViewT(t, PDFA2b)
	oi := v.Objects[4].Value.(*object.Dictionary)
	oi.Delete("DestOutputProfile")
	oi.Delete("OutputConditionIdentifier")
	got := findingsWith(checkOutputIntents(v, PDFA2b), "/OutputIntents[0]")
	if len(got) != 1 || !strings.Contains(got[0].Message, "GTS_PDFA1 must have /DestOutputProfile") {
		t.Errorf("want one finding for the missing profile, got %v", got)
	}
	// Another kind of intent with neither still gets the general one.
	oi.Set("S", object.Name("GTS_PDFX"))
	if got := findingsWith(checkOutputIntents(v, PDFA2b), "must have /DestOutputProfile or /OutputConditionIdentifier"); len(got) != 1 {
		t.Errorf("a GTS_PDFX intent with neither: %v", got)
	}
}

// TestPageOutputIntentProfilesAreJudged: at PDF/A-4 a page may carry its own
// output intent, and its profile is held to the same rules as the
// document's; a profile several intents share is judged once.
func TestPageOutputIntentProfilesAreJudged(t *testing.T) {
	v := mkPDFAViewT(t, PDFA4)
	page := addTestPage(v)
	bad := object.NewStream(object.NewDictionary(object.Entry{Key: "N", Value: object.Integer(3)}), []byte("too short"))
	v.Objects[41] = &object.IndirectObject{Number: 41, Value: bad}
	pageOI := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("OutputIntent")},
		object.Entry{Key: "S", Value: object.Name("GTS_PDFA1")},
		object.Entry{Key: "OutputConditionIdentifier", Value: object.String{Value: []byte("x")}},
		object.Entry{Key: "DestOutputProfile", Value: object.IndirectRef{Number: 41}},
	)
	page.Set("OutputIntents", object.Array{pageOI, pageOI})
	got := findingsWith(checkOutputIntentProfile(v, PDFA4), "too short")
	if len(got) != 1 || got[0].Object != 20 || !strings.HasPrefix(got[0].Message, "page OutputIntents[0]") || got[0].Rule != "6.2.3" {
		t.Errorf("a page output intent's bad profile: want one 6.2.3 finding on page 20, got %v", got)
	}
	// The version bound holds at part 4 as at parts 2 and 3.
	v5 := make([]byte, 128)
	copy(v5[12:], "mntrRGB ")
	v5[8] = 5
	bad.Data = v5
	if got := findingsWith(checkOutputIntentProfile(v, PDFA4), "version 5.0 not allowed"); len(got) != 1 {
		t.Errorf("a v5 output-intent profile at PDF/A-4: %v", got)
	}
	bad.Data = []byte("too short")
	// Before part 4 a page intent is not a PDF/A output intent at all.
	if got := findingsWith(checkOutputIntentProfile(v, PDFA2b), "page OutputIntents"); len(got) != 0 {
		t.Errorf("a page output intent was judged at 2b: %v", got)
	}
}
