// Command rulecoverage reports how the pdf0 PDF/A validator's rule coverage
// compares to the veraPDF validation profiles — the machine-readable inventory
// of every PDF/A rule the reference validator checks.
//
// It is a developer aid for finding coverage gaps, not a shipped feature. Fetch
// the profiles with `make profiles` (they are CC BY 4.0, veraPDF Consortium, and
// live under spec/ which is gitignored), then run `make rule-coverage`.
//
// Coverage is matched by ISO clause. The comparison is approximate: pdf0 emits
// some rule IDs with ISO 19005-2 numbering even at PDF/A-1 (see the audit notes),
// so a clause listed as "not covered" may be implemented under a different
// number — the printed description tells you which to check by hand.
package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// veraProfile is the subset of the veraPDF validation-profile schema we read.
type veraProfile struct {
	Rules []veraRule `xml:"rules>rule"`
}

type veraRule struct {
	Object string `xml:"object,attr"`
	ID     struct {
		Specification string `xml:"specification,attr"`
		Clause        string `xml:"clause,attr"`
		TestNumber    string `xml:"testNumber,attr"`
	} `xml:"id"`
	Description string `xml:"description"`
	Test        string `xml:"test"`
}

// level pairs a display name with its combined-profile filename.
type level struct {
	name string
	file string
}

var levels = []level{
	{"PDF/A-1b", "PDFA-1B.xml"},
	{"PDF/A-2b", "PDFA-2B.xml"},
	{"PDF/A-3b", "PDFA-3B.xml"},
	{"PDF/A-4", "PDFA-4.xml"},
}

func main() {
	profilesDir := os.Getenv("VERAPDF_PROFILES")
	if profilesDir == "" {
		profilesDir = "spec/verapdf-profiles"
	}
	srcDir := "."
	if len(os.Args) > 1 {
		srcDir = os.Args[1]
	}

	if _, err := os.Stat(filepath.Join(profilesDir, "PDF_A")); err != nil {
		fmt.Fprintf(os.Stderr, "veraPDF profiles not found at %s\nRun `make profiles` first.\n", profilesDir)
		os.Exit(1)
	}

	implemented, err := scanImplementedRules(srcDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scanning source: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pdf0 emits %d distinct rule clauses across the source.\n\n", len(implemented))

	var totalVera, totalCovered int
	for _, lv := range levels {
		rules, err := loadProfile(filepath.Join(profilesDir, "PDF_A", lv.file))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", lv.name, err)
			continue
		}
		// Group by clause; keep the first description per clause.
		clauseDesc := map[string]string{}
		clauseTests := map[string]int{}
		for _, r := range rules {
			if _, ok := clauseDesc[r.ID.Clause]; !ok {
				clauseDesc[r.ID.Clause] = firstSentence(r.Description)
			}
			clauseTests[r.ID.Clause]++
		}
		clauses := sortedClauses(clauseDesc)

		var covered, missing []string
		for _, c := range clauses {
			if implemented[c] {
				covered = append(covered, c)
			} else {
				missing = append(missing, c)
			}
		}
		totalVera += len(clauses)
		totalCovered += len(covered)

		fmt.Printf("=== %s: %d rules across %d clauses — %d/%d clauses covered ===\n",
			lv.name, len(rules), len(clauses), len(covered), len(clauses))
		if len(missing) > 0 {
			fmt.Printf("  clauses with no matching pdf0 rule ID (%d):\n", len(missing))
			for _, c := range missing {
				fmt.Printf("    %-10s (%d test%s)  %s\n", c, clauseTests[c], plural(clauseTests[c]), clauseDesc[c])
			}
		}
		fmt.Println()
	}
	fmt.Printf("Overall: %d/%d veraPDF clauses matched by a pdf0 rule ID (clause-string match; see caveat).\n",
		totalCovered, totalVera)
}

func loadProfile(path string) ([]veraRule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p veraProfile
	if err := xml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return p.Rules, nil
}

// ruleLiteral matches any quoted ISO clause number (e.g. "6.7.8", "6.1.7").
// This catches rule IDs however they reach a ValidationError — inline
// `Rule: "6.1.4"`, via `rule := "6.1.7"`, or in a multi-assignment like
// `attrRule, wfRule = "6.7.5", "6.7.9"` — which a `Rule:`-anchored pattern misses.
var ruleLiteral = regexp.MustCompile(`"(6(?:\.\d+)+)"`)

// scanImplementedRules returns the set of ISO clause strings that appear as
// quoted literals in the non-test Go source (the rule IDs the validator emits).
func scanImplementedRules(root string) (map[string]bool, error) {
	set := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, "/cmd/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range ruleLiteral.FindAllStringSubmatch(string(data), -1) {
			set[m[1]] = true
		}
		return nil
	})
	return set, err
}

func sortedClauses(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return clauseLess(out[i], out[j]) })
	return out
}

// clauseLess orders dotted clause numbers numerically ("6.2.10" after "6.2.9").
func clauseLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := atoiSafe(as[i]), atoiSafe(bs[i])
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.IndexByte(s, '.'); i > 0 && i < 100 {
		return s[:i+1]
	}
	if len(s) > 100 {
		return s[:100] + "…"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
