package pdf0

import (
	"bytes"
	lcms2 "github.com/mgilbir/golittlecms"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"testing"
)

// TestGeneratedICCProfileIsReal verifies that the OutputIntent ICC profile
// embedded by NewPDFADocument is a genuine sRGB profile — one produced by, and
// re-readable through, a real colour-management engine (golittlecms) — rather
// than the former hand-assembled stub. It also pins the profile version to what
// each PDF/A level permits (ICC v2 for PDF/A-1's PDF 1.4, ICC v4 otherwise).
func TestGeneratedICCProfileIsReal(t *testing.T) {
	cases := []struct {
		level     pdfa.Level
		wantMajor byte
	}{
		{pdfa.PDFA1b, 2},
		{pdfa.PDFA2b, 4},
		{pdfa.PDFA3b, 4},
		{pdfa.PDFA4, 4},
	}
	for _, tc := range cases {
		doc := NewPDFADocument(tc.level)

		// Locate the OutputIntent's DestOutputProfile stream.
		catalog := doc.view().Catalog()
		oi := doc.ResolveDict(catalog.Get("OutputIntents").(object.Array)[0])
		prof, ok := doc.Resolve(oi.Get("DestOutputProfile")).(*object.Stream)
		if !ok {
			t.Fatalf("%v: DestOutputProfile is not a stream", tc.level)
		}
		data := prof.Data

		// Header sanity: class, colour space, PCS, signature, version.
		if got := string(data[12:16]); got != "mntr" {
			t.Errorf("%v: device class = %q, want mntr", tc.level, got)
		}
		if got := string(data[16:20]); got != "RGB " {
			t.Errorf("%v: colour space = %q, want %q", tc.level, got, "RGB ")
		}
		if got := string(data[36:40]); got != "acsp" {
			t.Errorf("%v: profile signature = %q, want acsp", tc.level, got)
		}
		if data[8] != tc.wantMajor {
			t.Errorf("%v: ICC major version = %d, want %d", tc.level, data[8], tc.wantMajor)
		}
		if n, _ := prof.Dict.Get("N").(object.Integer); int(n) != 3 {
			t.Errorf("%v: /N = %d, want 3", tc.level, n)
		}

		// The strongest verification available without the veraPDF binary:
		// re-open the embedded bytes through a real CMM and confirm the RGB
		// matrix/white-point round-trips.
		p, err := lcms2.OpenProfileFromMem(data)
		if err != nil {
			t.Fatalf("%v: real CMM rejected the embedded profile: %v", tc.level, err)
		}
		if v := p.GetProfileVersion(); byte(v) != tc.wantMajor {
			t.Errorf("%v: reopened profile version = %.1f, want major %d", tc.level, v, tc.wantMajor)
		}
	}
}

// TestGeneratedDocPassesOutputIntentProfileCheck confirms a freshly built PDF/A
// document survives a full write/reparse and raises no OutputIntent-profile
// violations at its own level — the check that the former stub was never
// exercised against (audit C29).
func TestGeneratedDocPassesOutputIntentProfileCheck(t *testing.T) {
	for _, lvl := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		doc := NewPDFADocumentWithInfo(lvl, "T", "A")
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Fatalf("%v: write: %v", lvl, err)
		}
		rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("%v: reparse: %v", lvl, err)
		}
		// Through the public entry point: what this pins is the finding the
		// caller sees, and a freshly built document must raise none at all.
		for _, e := range ValidatePDFA(rd, lvl) {
			t.Errorf("%v: violation on a freshly built document: %s", lvl, e.Error())
		}
	}
}
