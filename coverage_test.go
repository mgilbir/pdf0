package pdf0

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/internal/rulecov"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/pdfa"
)

// ruleCoverageBaselines ratchet the per-rule coverage internal/rulecov
// measures: for every veraPDF profile rule the corpus tests, whether pdf0
// flags the rule's fail files under the rule's own clause.
//
//   - minDetected must never fall: a rule that was detected under its own
//     clause and no longer is has regressed, even when the file is still
//     rejected for some other reason (which TestCorpus alone cannot see).
//   - maxNotDetected (caught only under another clause, missed, or unreadable)
//     must never rise. Lower it when a rule is fixed; `make rule-coverage`
//     lists the rules behind it.
//
// This replaced a clause-string scan of the source that read 181/181 at every
// level and so could not fail on anything (audit 2026-09-22 C156); see the
// rulecov package documentation.
var ruleCoverageBaselines = []struct {
	name, profile, corpus string
	level                 pdfa.Level
	minDetected           int
	maxNotDetected        int
}{
	{"PDF/A-1b", "PDFA-1B.xml", "PDF_A-1b", pdfa.PDFA1b, 46, 4},
	{"PDF/A-2b", "PDFA-2B.xml", "PDF_A-2b", pdfa.PDFA2b, 63, 11},
	{"PDF/A-3b", "PDFA-3B.xml", "PDF_A-3b", pdfa.PDFA3b, 2, 0},
	{"PDF/A-4", "PDFA-4.xml", "PDF_A-4", pdfa.PDFA4, 68, 11},
}

// TestRuleCoverage measures, per level and per veraPDF rule, whether pdf0
// detects the corpus's fail files for that rule under the rule's clause, and
// ratchets the result. It skips when the profiles (`make profiles`) or the
// corpus (`make corpus`) were never fetched.
func TestRuleCoverage(t *testing.T) {
	profilesDir := filepath.Dir(testfiles.VeraPDFProfiles.File(t, "PDF_A"))
	corpusDir := corpusRoot(t)

	for _, lv := range ruleCoverageBaselines {
		rules, err := rulecov.LoadProfile(filepath.Join(profilesDir, "PDF_A", lv.profile))
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		files, err := rulecov.CorpusFiles(filepath.Join(corpusDir, lv.corpus))
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s: no corpus test files under %s; the corpus layout changed and nothing would be measured", lv.name, lv.corpus)
		}
		level := lv.level
		rep, err := rulecov.Measure(rules, files, func(data []byte) ([]string, error) {
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return nil, err
			}
			var clauses []string
			for _, v := range ValidatePDFA(doc, level) {
				clauses = append(clauses, v.Rule)
			}
			return clauses, nil
		})
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}

		detected := rep.Count(rulecov.Detected)
		untested := rep.Count(rulecov.Untested)
		notDetected := len(rep.Rules) - detected - untested
		t.Logf("%-9s %d rules, %d tested: %d detected under their clause, %d not (elsewhere %d, missed %d, unreadable %d); %d untested",
			lv.name, len(rep.Rules), len(rep.Rules)-untested, detected, notDetected,
			rep.Count(rulecov.Elsewhere), rep.Count(rulecov.Missed), rep.Count(rulecov.Unreadable), untested)
		for _, r := range rep.Rules {
			if s := r.Status(); s != rulecov.Detected && s != rulecov.Untested {
				t.Logf("   %-10s %s %v", s, r.ID(), r.Examples)
			}
		}
		if detected < lv.minDetected {
			t.Errorf("%s: %d rules detected under their own clause, below the baseline %d (a regression; see the list above)",
				lv.name, detected, lv.minDetected)
		}
		if notDetected > lv.maxNotDetected {
			t.Errorf("%s: %d tested rules not detected under their own clause, above the baseline %d (a regression; see the list above)",
				lv.name, notDetected, lv.maxNotDetected)
		}
	}
}
