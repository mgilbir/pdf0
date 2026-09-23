package pdfa

import (
	"bytes"
	"testing"
)

// The ICC header's creation date, the only field that is not a function of the
// sRGB definition (ICC.1:2010, 7.2.8: bytes 24..35, a dateTimeNumber).
const (
	iccCreatedAt    = 24
	iccCreatedAtEnd = 36
)

// The embedded sRGB profiles have to be what the colour engine would build.
//
// They are committed rather than generated per document, which removed the one
// operation in document creation that could fail and the panic that stood in
// for handling it. The trade is that committed bytes can drift from the engine
// that produced them — a golittlecms change, or a regeneration someone forgot.
// This is the guard against that: it rebuilds both through golittlecms and
// compares, so drift is a test failure rather than a profile nobody notices is
// stale.
//
// If it fails after a dependency bump, regenerate:
//
//	go run -tags devtools ./internal/cmd/genicc
func TestTheEmbeddedProfilesAreWhatTheEngineBuilds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  float64
		embedded []byte
	}{
		{"ICC v2.1, for PDF/A-1", 2.1, srgbV21},
		{"ICC v4.3, for PDF/A-2 and later", 4.3, srgbV43},
	} {
		built, err := buildSRGBProfile(tc.version)
		if err != nil {
			t.Fatalf("%s: the engine could not build it at all: %v", tc.name, err)
		}
		if len(tc.embedded) == 0 {
			t.Fatalf("%s: the embedded profile is empty; //go:embed found nothing", tc.name)
		}
		if len(built) != len(tc.embedded) {
			t.Fatalf("%s: the embedded profile is %d bytes and the engine now builds "+
				"%d; regenerate with `go run -tags devtools ./internal/cmd/genicc`",
				tc.name, len(tc.embedded), len(built))
		}

		// Everything except the creation date has to match exactly. The date
		// is the one field that is not a function of the sRGB definition: ICC
		// header bytes 24..35 are the time the profile was made, so two runs a
		// second apart differ there and nowhere else. That is also why
		// embedding is worth doing beyond the panic — every document pdf0
		// generated used to carry a profile stamped with the moment it ran.
		a, b := copyProfile(built), copyProfile(tc.embedded)
		for i := iccCreatedAt; i < iccCreatedAtEnd; i++ {
			a[i], b[i] = 0, 0
		}
		if !bytes.Equal(a, b) {
			var at []int
			for i := range a {
				if a[i] != b[i] {
					at = append(at, i)
				}
			}
			t.Errorf("%s: the embedded profile differs from what the engine builds "+
				"at %d byte(s) outside the creation date (offsets %v); regenerate "+
				"with `go run -tags devtools ./internal/cmd/genicc`", tc.name, len(at), at)
		}

		// And the difference really is confined to the date, rather than the
		// comparison above having been made vacuous by zeroing too much.
		if bytes.Equal(built, tc.embedded) {
			t.Logf("%s: byte-identical, including the creation date", tc.name)
		} else {
			differs := false
			for i := iccCreatedAt; i < iccCreatedAtEnd; i++ {
				if built[i] != tc.embedded[i] {
					differs = true
				}
			}
			if !differs {
				t.Errorf("%s: the profiles differ somewhere the zeroing hid", tc.name)
			}
		}
	}
}

// TestTheProfileForALevelIsTheOneThatLevelAllows.
//
// PDF/A-1 is based on PDF 1.4, which permits only ICC v2. Handing it the v4
// profile would produce a document that fails the rule the profile is there to
// satisfy.
func TestTheProfileForALevelIsTheOneThatLevelAllows(t *testing.T) {
	if got := sRGBProfile(PDFA1b); !bytes.Equal(got, srgbV21) {
		t.Error("PDF/A-1b did not get the ICC v2.1 profile")
	}
	if got := sRGBProfile(PDFA1a); !bytes.Equal(got, srgbV21) {
		t.Error("PDF/A-1a did not get the ICC v2.1 profile; it is part 1, like 1b")
	}
	for _, l := range []Level{PDFA2b, PDFA3b, PDFA4, PDFA2a, PDFA3a} {
		if got := sRGBProfile(l); !bytes.Equal(got, srgbV43) {
			t.Errorf("%s did not get the ICC v4.3 profile", l)
		}
	}
}

// TestACopiedProfileDoesNotShareTheEmbeddedArray, since an embedded slice is
// shared by every caller in the process and a single mutation would reach all
// of them.
func TestACopiedProfileDoesNotShareTheEmbeddedArray(t *testing.T) {
	c := copyProfile(srgbV43)
	if !bytes.Equal(c, srgbV43) {
		t.Fatal("the copy is not equal to the original")
	}
	c[0] ^= 0xFF
	if bytes.Equal(c, srgbV43) {
		t.Fatal("the copy did not change, so this test proves nothing")
	}
	if srgbV43[0] == c[0] {
		t.Error("writing to the copy reached the embedded profile")
	}
}
