package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/pdfa"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLevelACorpus is the FP=0 guard for Level A: validating a Level A corpus
// file at its Level A conformance level must not add any finding beyond what the
// corresponding Level B validation already reports (the Level A rule families
// must not false-positive on conforming files). Gated on the veraPDF corpus.
func TestLevelACorpus(t *testing.T) {
	cases := []struct {
		dir  string
		a, b pdfa.Level
	}{
		{"PDF_A-1a", pdfa.PDFA1a, pdfa.PDFA1b},
		{"PDF_A-2a", pdfa.PDFA2a, pdfa.PDFA2b},
	}
	for _, tc := range cases {
		// Every file in the suite, at any depth. This used to glob
		// "**/**/*.pdf", which in Go matches exactly two directory levels and
		// silently left out the 25 of 61 files that sit deeper; and a suite
		// missing from a present corpus used to be skipped.
		files := corpusTestFiles(t, tc.dir)
		aFP, checked := 0, 0
		for _, f := range files {
			base := filepath.Base(f)
			if !strings.Contains(base, "-pass-") {
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", base, err)
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Errorf("%s: a conforming corpus file failed to parse: %v", base, err)
				continue
			}
			checked++
			bmsgs := map[string]bool{}
			for _, e := range ValidatePDFA(doc, tc.b) {
				bmsgs[e.Rule+e.Message] = true
			}
			for _, e := range ValidatePDFA(doc, tc.a) {
				if !bmsgs[e.Rule+e.Message] {
					aFP++
					t.Errorf("%s [%s]: Level A false positive: %s %s", base, tc.a, e.Rule, e.Message)
				}
			}
		}
		if checked == 0 {
			t.Fatalf("%s: no conforming (-pass-) file was checked", tc.dir)
		}
		if aFP == 0 {
			t.Logf("%s: Level A adds no false positives on %d conforming files", tc.dir, checked)
		}
	}
}

// TestLevelAString checks the new level constants stringify correctly.
func TestLevelAString(t *testing.T) {
	for lvl, want := range map[pdfa.Level]string{pdfa.PDFA1a: "PDF/A-1a", pdfa.PDFA2a: "PDF/A-2a", pdfa.PDFA3a: "PDF/A-3a"} {
		if got := lvl.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", lvl, got, want)
		}
		if !lvl.IsA() {
			t.Errorf("%s should be a Level A level", want)
		}
	}
}
