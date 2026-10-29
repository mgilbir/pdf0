package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/internal/rulecov"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfua"
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
	{"PDF/A-1b", "PDFA-1B.xml", "PDF_A-1b", pdfa.PDFA1b, 49, 1},
	// 2b and 4 lost 6.2.11.4.1-t02 / 6.2.10.4.1-t02 by name when a TrueType
	// code selecting glyph 0 stopped being reported as a missing glyph: the
	// file named for it, 6-2-11-4-1-t02-fail-c (-4 6-2-10-4-1-t02-fail-c),
	// fails on width and .notdef by veraPDF's verdict, which pdf0 now agrees
	// with exactly (TestRuleCoverageByVeraPDF).
	{"PDF/A-2b", "PDFA-2B.xml", "PDF_A-2b", pdfa.PDFA2b, 65, 9},
	{"PDF/A-3b", "PDFA-3B.xml", "PDF_A-3b", pdfa.PDFA3b, 2, 0},
	{"PDF/A-4", "PDFA-4.xml", "PDF_A-4", pdfa.PDFA4, 75, 4},
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

// ruleVerdictBaselines ratchet TestRuleCoverageByVeraPDF as
// ruleCoverageBaselines ratchet TestRuleCoverage, and add one that must stay
// zero: a file veraPDF passes that pdf0 flags.
var ruleVerdictBaselines = []struct {
	name, profile, corpus string
	level                 pdfa.Level
	minDetected           int
	maxNotDetected        int
}{
	// Every rule veraPDF fails a corpus file on is detected under its own
	// clause, at every level: nothing tested is left to rise.
	{"PDF/A-1b", "PDFA-1B.xml", "PDF_A-1b", pdfa.PDFA1b, 70, 0},
	{"PDF/A-2b", "PDFA-2B.xml", "PDF_A-2b", pdfa.PDFA2b, 104, 0},
	{"PDF/A-3b", "PDFA-3B.xml", "PDF_A-3b", pdfa.PDFA3b, 2, 0},
	{"PDF/A-4", "PDFA-4.xml", "PDF_A-4", pdfa.PDFA4, 91, 0},
	{"PDF/A-1a", "PDFA-1A.xml", "PDF_A-1a", pdfa.PDFA1a, 7, 0},
	{"PDF/A-2a", "PDFA-2A.xml", "PDF_A-2a", pdfa.PDFA2a, 7, 0},
	{"PDF/A-2u", "PDFA-2U.xml", "PDF_A-2u", pdfa.PDFA2u, 4, 0},
	{"PDF/A-4e", "PDFA-4E.xml", "PDF_A-4e", pdfa.PDFA4E, 5, 0},
	{"PDF/A-4f", "PDFA-4F.xml", "PDF_A-4f", pdfa.PDFA4F, 4, 0},
}

// TestRuleCoverageByVeraPDF is TestRuleCoverage judged by veraPDF instead of
// by the corpus file names: every rule veraPDF fails a corpus file on is a
// test of that rule, and pdf0 detects it when it reports the rule's clause for
// the file. A file veraPDF passes and pdf0 flags is a false positive, and there
// must be none. It skips when the verdicts (`make verapdf-verdicts`), the
// profiles or the corpus are absent.
func TestRuleCoverageByVeraPDF(t *testing.T) {
	verdictsDir := testfiles.VeraPDFVerdicts.Path(t)
	profilesDir := filepath.Dir(testfiles.VeraPDFProfiles.File(t, "PDF_A"))
	corpusDir := corpusRoot(t)

	for _, lv := range ruleVerdictBaselines {
		rules, err := rulecov.LoadProfile(filepath.Join(profilesDir, "PDF_A", lv.profile))
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		levelDir := filepath.Join(corpusDir, lv.corpus)
		report, err := os.Open(filepath.Join(verdictsDir, lv.corpus+".txt"))
		if err != nil {
			t.Fatalf("%s: %v (regenerate with `make verapdf-verdicts`)", lv.name, err)
		}
		abs, _ := filepath.Abs(levelDir)
		verdicts, err := rulecov.ParseVerdicts(report, "/data/", abs+"/")
		report.Close()
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		level := lv.level
		rep, err := rulecov.MeasureVerdicts(rules, levelDir, verdicts, func(data []byte) ([]string, error) {
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return nil, err
			}
			var clauses []string
			for _, v := range ValidatePDFA(doc, level) {
				if !IsCheckerFinding(v) {
					clauses = append(clauses, v.Rule)
				}
			}
			return clauses, nil
		})
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}

		detected := rep.Count(rulecov.Detected)
		untested := rep.Count(rulecov.Untested)
		notDetected := len(rep.Rules) - detected - untested
		t.Logf("%-9s %d files (%d failed on rules their names do not say); %d rules, %d tested: %d detected under their clause, %d not (elsewhere %d, missed %d, unreadable %d); %d untested; %d false positives",
			lv.name, rep.Files, rep.Renamed, len(rep.Rules), len(rep.Rules)-untested, detected, notDetected,
			rep.Count(rulecov.Elsewhere), rep.Count(rulecov.Missed), rep.Count(rulecov.Unreadable), untested, len(rep.FalsePositives))
		for _, r := range rep.Rules {
			if s := r.Status(); s != rulecov.Detected && s != rulecov.Untested {
				t.Logf("   %-10s %s %v", s, r.ID(), r.Examples)
			}
		}
		for _, fp := range rep.FalsePositives {
			t.Errorf("%s: veraPDF passes it and pdf0 does not: %s", lv.name, fp)
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

// pdfuaVerdictBaselines ratchet TestPDFUARuleCoverageByVeraPDF as
// ruleVerdictBaselines ratchet the PDF/A one.
var pdfuaVerdictBaselines = []struct {
	name, profile, corpus string
	validate              func(*Document) []pdfua.Violation
	minDetected           int
	maxNotDetected        int
}{
	// Every rule veraPDF fails a PDF/UA corpus file on is reported under its
	// own clause, at both parts.
	{"PDF/UA-1", "PDFUA-1.xml", "PDF_UA-1", ValidatePDFUA, 97, 0},
	{"PDF/UA-2", "PDFUA-2.xml", "PDF_UA-2", ValidatePDFUA2, 48, 0},
}

// TestPDFUARuleCoverageByVeraPDF is TestRuleCoverageByVeraPDF for PDF/UA:
// every rule veraPDF fails a PDF/UA corpus file on is a test of that rule,
// detected when ValidatePDFUA (or ValidatePDFUA2) reports its clause for the
// file; a file veraPDF passes and pdf0 flags is a false positive, and there
// must be none. Rules veraPDF numbers by an ISO 32005 table rather than a
// clause are not measured.
func TestPDFUARuleCoverageByVeraPDF(t *testing.T) {
	verdictsDir := testfiles.VeraPDFVerdicts.Path(t)
	profilesDir := filepath.Dir(testfiles.VeraPDFProfiles.File(t, "PDF_UA"))
	corpusDir := corpusRoot(t)

	for _, lv := range pdfuaVerdictBaselines {
		rules, err := rulecov.LoadProfile(filepath.Join(profilesDir, "PDF_UA", lv.profile))
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		levelDir := filepath.Join(corpusDir, lv.corpus)
		report, err := os.Open(filepath.Join(verdictsDir, lv.corpus+".txt"))
		if err != nil {
			t.Fatalf("%s: %v (regenerate with `make verapdf-verdicts`)", lv.name, err)
		}
		abs, _ := filepath.Abs(levelDir)
		verdicts, err := rulecov.ParseVerdicts(report, "/data/", abs+"/")
		report.Close()
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		validate := lv.validate
		rep, err := rulecov.MeasureVerdicts(rules, levelDir, verdicts, func(data []byte) ([]string, error) {
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return nil, err
			}
			var clauses []string
			for _, v := range validate(doc) {
				if !IsCheckerFinding(v) {
					clauses = append(clauses, v.Clause)
				}
			}
			return clauses, nil
		})
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		detected := rep.Count(rulecov.Detected)
		untested := rep.Count(rulecov.Untested)
		notDetected := len(rep.Rules) - detected - untested
		t.Logf("%-9s %d files (%d failed on rules their names do not say); %d rules, %d tested: %d detected under their clause, %d not (elsewhere %d, missed %d, unreadable %d); %d untested; %d false positives",
			lv.name, rep.Files, rep.Renamed, len(rep.Rules), len(rep.Rules)-untested, detected, notDetected,
			rep.Count(rulecov.Elsewhere), rep.Count(rulecov.Missed), rep.Count(rulecov.Unreadable), untested, len(rep.FalsePositives))
		for _, r := range rep.Rules {
			if s := r.Status(); s != rulecov.Detected && s != rulecov.Untested {
				t.Logf("   %-10s %s %v", s, r.ID(), r.Examples)
			}
		}
		for _, fp := range rep.FalsePositives {
			t.Errorf("%s: veraPDF passes it and pdf0 does not: %s", lv.name, fp)
		}
		if detected < lv.minDetected {
			t.Errorf("%s: %d rules detected under their own clause, below the baseline %d", lv.name, detected, lv.minDetected)
		}
		if notDetected > lv.maxNotDetected {
			t.Errorf("%s: %d tested rules not detected under their own clause, above the baseline %d", lv.name, notDetected, lv.maxNotDetected)
		}
	}
}
