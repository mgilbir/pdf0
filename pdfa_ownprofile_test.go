package pdf0

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A caller bringing their own output-intent profile.
//
// pdf0 embeds an sRGB profile so the common case needs no colour management
// from the caller. A caller who has a press profile, a house sRGB variant, or a
// requirement that the bytes be exactly the ones they audited, has to be able
// to put theirs in and get none of pdf0's.

// syntheticProfile builds a minimal well-formed ICC profile header in the given
// colour space and major version, with its declared length correct. It is not a
// usable profile — it has no tags — but every property the output-intent code
// reads is a header field, and building one by hand is what lets the CMYK and
// ICC-v4 cases be tested without shipping two more profiles.
func syntheticProfile(space string, major byte, size int) []byte {
	if size < 128 {
		size = 128
	}
	p := make([]byte, size)
	p[0] = byte(size >> 24)
	p[1] = byte(size >> 16)
	p[2] = byte(size >> 8)
	p[3] = byte(size)
	p[8] = major
	copy(p[16:20], space)
	return p
}

func TestACallersOwnProfileIsTheOneEmbedded(t *testing.T) {
	mine := syntheticProfile("CMYK", 2, 300)
	doc, err := NewPDFADocumentWith(PDFAOptions{
		Level: pdfa.PDFA4,
		OutputIntent: PDFAOutputIntent{
			ICCProfile:                mine,
			OutputConditionIdentifier: "FOGRA51",
		},
	})
	if err != nil {
		t.Fatalf("a caller's own profile was refused: %v", err)
	}

	// Round-tripped, so this is about the document and not the struct.
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	v := back.view()
	intents, _ := v.Resolve(v.Catalog().Get("OutputIntents")).(object.Array)
	if len(intents) != 1 {
		t.Fatalf("%d output intents, want 1", len(intents))
	}
	oi := v.ResolveDict(intents[0])
	if oi == nil {
		t.Fatal("the output intent did not survive the round trip")
	}

	id, _ := v.Resolve(oi.Get("OutputConditionIdentifier")).(object.String)
	if string(id.Value) != "FOGRA51" {
		t.Errorf("the identifier is %q, want FOGRA51 — pdf0 described the caller's "+
			"profile as its own", id.Value)
	}
	// Info defaults to the identifier rather than to a claim about sRGB.
	info, _ := v.Resolve(oi.Get("Info")).(object.String)
	if strings.Contains(string(info.Value), "sRGB") {
		t.Errorf("/Info says %q about a CMYK profile", info.Value)
	}

	prof, ok := v.Resolve(oi.Get("DestOutputProfile")).(*object.Stream)
	if !ok {
		t.Fatal("no destination profile stream")
	}
	got, err := back.StreamData(prof)
	if err != nil {
		t.Fatalf("decoding the profile stream: %v", err)
	}
	if !bytes.Equal(got, mine) {
		t.Error("the embedded profile is not the one supplied")
	}
	// /N follows the profile, not a hardcoded 3.
	if n, _ := v.Resolve(prof.Dict.Get("N")).(object.Integer); n != 4 {
		t.Errorf("/N is %v for a CMYK profile, want 4", n)
	}
	// And nothing of pdf0's sRGB is in the file.
	if bytes.Contains(buf.Bytes(), []byte("sRGB IEC61966-2.1")) {
		t.Error("the document still names pdf0's sRGB profile")
	}
}

// TestTheDefaultIsStillPdf0sProfile, so the common case is unchanged.
func TestTheDefaultIsStillPdf0sProfile(t *testing.T) {
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("sRGB IEC61966-2.1")) {
		t.Error("the default document no longer carries the sRGB output intent")
	}
	if errs := ValidatePDFA(doc, pdfa.PDFA2b); len(errs) != 0 {
		t.Errorf("the default document stopped validating: %v", errs)
	}
}

// TestAProfileThatCannotBeUsedIsRefusedRatherThanEmbedded.
//
// This is what the error return on the constructors is for. Each of these is a
// document that should not be built, not one built wrongly.
func TestAProfileThatCannotBeUsedIsRefusedRatherThanEmbedded(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts PDFAOptions
		want string
	}{
		{
			"too short to be a profile",
			PDFAOptions{Level: pdfa.PDFA4, OutputIntent: PDFAOutputIntent{
				ICCProfile: []byte("not a profile"), OutputConditionIdentifier: "x"}},
			"shorter than the 128-byte header",
		},
		{
			"header disagrees with its own length",
			PDFAOptions{Level: pdfa.PDFA4, OutputIntent: PDFAOutputIntent{
				ICCProfile:                func() []byte { p := syntheticProfile("RGB ", 2, 200); return p[:180] }(),
				OutputConditionIdentifier: "x"}},
			"the header declares",
		},
		{
			"a colour space an output intent may not use",
			PDFAOptions{Level: pdfa.PDFA4, OutputIntent: PDFAOutputIntent{
				ICCProfile: syntheticProfile("Lab ", 2, 200), OutputConditionIdentifier: "x"}},
			"not one of",
		},
		{
			"ICC v4 at a level based on PDF 1.4",
			PDFAOptions{Level: pdfa.PDFA1b, OutputIntent: PDFAOutputIntent{
				ICCProfile: syntheticProfile("RGB ", 4, 200), OutputConditionIdentifier: "x"}},
			"permits only ICC v2",
		},
		{
			"a profile with nothing naming it",
			PDFAOptions{Level: pdfa.PDFA4, OutputIntent: PDFAOutputIntent{
				ICCProfile: syntheticProfile("RGB ", 2, 200)}},
			"no OutputConditionIdentifier",
		},
		{
			"a name with no profile to name",
			PDFAOptions{Level: pdfa.PDFA4, OutputIntent: PDFAOutputIntent{
				OutputConditionIdentifier: "FOGRA51"}},
			"no ICCProfile",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := NewPDFADocumentWith(tc.opts)
			if err == nil {
				t.Fatalf("accepted, and built a document: %v", doc != nil)
			}
			if doc != nil {
				t.Error("an error came back with a document beside it")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error is %q, want it to mention %q", err, tc.want)
			}
		})
	}

	// The not-a-profile cases are one sentinel, so a caller can tell "these
	// bytes are not an ICC profile" from "this profile will not do here".
	_, err := NewPDFADocumentWith(PDFAOptions{Level: pdfa.PDFA4,
		OutputIntent: PDFAOutputIntent{ICCProfile: []byte("short"), OutputConditionIdentifier: "x"}})
	if !errors.Is(err, pdfa.ErrNotAProfile) {
		t.Errorf("short bytes gave %v, which does not match ErrNotAProfile", err)
	}
	_, err = NewPDFADocumentWith(PDFAOptions{Level: pdfa.PDFA1b,
		OutputIntent: PDFAOutputIntent{ICCProfile: syntheticProfile("RGB ", 4, 200),
			OutputConditionIdentifier: "x"}})
	if errors.Is(err, pdfa.ErrNotAProfile) {
		t.Error("a valid profile refused for its ICC version matched ErrNotAProfile")
	}
}

// TestICCComponentsReadsTheHeader, exported because a caller assembling their
// own ICCBased colour space needs the same number.
func TestICCComponentsReadsTheHeader(t *testing.T) {
	for space, want := range map[string]int{"GRAY": 1, "RGB ": 3, "CMYK": 4} {
		got, err := pdfa.ICCComponents(syntheticProfile(space, 2, 200))
		if err != nil || got != want {
			t.Errorf("%q gave (%d, %v), want (%d, nil)", space, got, err, want)
		}
	}
	// And pdf0's own embedded profile, through the public door.
	p, err := DefaultSRGBProfile()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := pdfa.ICCComponents(p); err != nil || n != 3 {
		t.Errorf("pdf0's own sRGB profile gave (%d, %v), want (3, nil)", n, err)
	}
}
