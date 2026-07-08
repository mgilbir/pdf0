package pdf0

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ruleCoverageMaxUncovered is a ratchet, like the corpus baselines: the number
// of veraPDF profile clauses (summed over PDF/A-1b/2b/3b/4) for which this
// validator emits no matching rule ID. It is 0 — every clause the profiles
// define is covered — and must never rise. If a rule-ID edit unmatches a clause,
// TestRuleCoverage fails; lower nothing, fix the rule ID (or, when veraPDF adds
// genuinely new rules, implement them).
//
// Matching is by ISO clause string, so this also pins the per-level numbering
// the reconciliation established. See cmd/rulecoverage for the human-readable
// report and CONTRIBUTING.md for the caveat.
const ruleCoverageMaxUncovered = 0

// TestRuleCoverage cross-references the veraPDF validation profiles against the
// rule IDs this package emits and ratchets the number of unmatched clauses. It
// self-skips when the profiles are absent (fetch them with `make profiles`),
// mirroring the corpus tests.
func TestRuleCoverage(t *testing.T) {
	profilesDir := os.Getenv("VERAPDF_PROFILES")
	if profilesDir == "" {
		profilesDir = "spec/verapdf-profiles"
	}
	if _, err := os.Stat(filepath.Join(profilesDir, "PDF_A")); err != nil {
		t.Skip("veraPDF profiles not found; run `make profiles` to download")
	}

	emitted, err := scanEmittedRuleClauses(".")
	if err != nil {
		t.Fatalf("scanning source: %v", err)
	}

	levels := []struct{ name, file string }{
		{"PDF/A-1b", "PDFA-1B.xml"},
		{"PDF/A-2b", "PDFA-2B.xml"},
		{"PDF/A-3b", "PDFA-3B.xml"},
		{"PDF/A-4", "PDFA-4.xml"},
	}

	var totalUncovered int
	for _, lv := range levels {
		clauses, err := profileClauses(filepath.Join(profilesDir, "PDF_A", lv.file))
		if err != nil {
			t.Fatalf("%s: %v", lv.name, err)
		}
		var uncovered []string
		for _, c := range clauses {
			if !emitted[c] {
				uncovered = append(uncovered, c)
			}
		}
		totalUncovered += len(uncovered)
		t.Logf("%-9s %d/%d clauses covered", lv.name, len(clauses)-len(uncovered), len(clauses))
		if len(uncovered) > 0 {
			t.Logf("   uncovered: %s", strings.Join(uncovered, ", "))
		}
	}

	if totalUncovered > ruleCoverageMaxUncovered {
		t.Errorf("veraPDF clause coverage regressed: %d clauses have no matching pdf0 rule ID "+
			"(baseline %d). See the per-level list above and `make rule-coverage`.",
			totalUncovered, ruleCoverageMaxUncovered)
	}
}

// clauseLiteral matches a quoted ISO clause number, e.g. "6.7.8" or "6.2.11.6".
var clauseLiteral = regexp.MustCompile(`"(6(?:\.\d+)+)"`)

// scanEmittedRuleClauses returns the set of ISO clause strings that appear as
// quoted literals in the package's non-test Go source — the rule IDs the
// validator can emit, whether inline or via a clause-helper table.
func scanEmittedRuleClauses(root string) (map[string]bool, error) {
	set := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"cmd"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range clauseLiteral.FindAllStringSubmatch(string(data), -1) {
			set[m[1]] = true
		}
		return nil
	})
	return set, err
}

// profileClauses returns the distinct ISO clauses a combined veraPDF profile
// defines, sorted numerically.
func profileClauses(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p struct {
		Rules []struct {
			ID struct {
				Clause string `xml:"clause,attr"`
			} `xml:"id"`
		} `xml:"rules>rule"`
	}
	if err := xml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range p.Rules {
		if c := r.ID.Clause; c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return clauseLess(out[i], out[j]) })
	return out, nil
}

// clauseLess orders dotted clause numbers numerically ("6.2.10" after "6.2.9").
func clauseLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := atoiClause(as[i]), atoiClause(bs[i])
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

func atoiClause(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
