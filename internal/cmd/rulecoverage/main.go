//go:build devtools

// Command rulecoverage reports, rule by rule, which veraPDF PDF/A rules the
// pdf0 validator detects: for every rule in the veraPDF validation profiles, it
// validates the corpus files that must fail that rule and checks that pdf0
// reports a violation under the rule's clause. See internal/rulecov for the
// classification and for why the previous measure (clause strings found in the
// source) had saturated at 181/181 and could no longer find a gap.
//
// It is a developer aid for finding coverage gaps, not a shipped feature. It
// needs the profiles (`make profiles`; CC BY 4.0, veraPDF Consortium) and the
// veraPDF corpus (`make corpus`); run it with `make rule-coverage`.
//
// usage: rulecoverage [-v]
//
// -v also lists every rule the corpus does not test. Exit status: 0 on a
// completed report, 1 when the profiles or the corpus are missing or unusable.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/internal/rulecov"
	"github.com/mgilbir/pdf0/pdfa"
)

// level pairs a PDF/A level with its combined profile and its corpus directory.
type level struct {
	name    string
	level   pdfa.Level
	profile string
	corpus  string
}

var levels = []level{
	{"PDF/A-1b", pdfa.PDFA1b, "PDFA-1B.xml", "PDF_A-1b"},
	{"PDF/A-2b", pdfa.PDFA2b, "PDFA-2B.xml", "PDF_A-2b"},
	{"PDF/A-3b", pdfa.PDFA3b, "PDFA-3B.xml", "PDF_A-3b"},
	{"PDF/A-4", pdfa.PDFA4, "PDFA-4.xml", "PDF_A-4"},
}

func main() {
	verbose := flag.Bool("v", false, "also list the rules the corpus does not test")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: rulecoverage [-v]")
		os.Exit(2)
	}
	profilesDir := envOr("VERAPDF_PROFILES", "spec/verapdf-profiles")
	corpusDir := envOr("VERAPDF_CORPUS", "testdata/verapdf-corpus")
	for _, need := range []struct{ path, fetch string }{
		{filepath.Join(profilesDir, "PDF_A"), "make profiles"},
		{corpusDir, "make corpus"},
	} {
		if fi, err := os.Stat(need.path); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "%s not found; run `%s` first.\n", need.path, need.fetch)
			os.Exit(1)
		}
	}

	var total, tested, detected int
	for _, lv := range levels {
		rules, err := rulecov.LoadProfile(filepath.Join(profilesDir, "PDF_A", lv.profile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", lv.name, err)
			os.Exit(1)
		}
		files, err := rulecov.CorpusFiles(filepath.Join(corpusDir, lv.corpus))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", lv.name, err)
			os.Exit(1)
		}
		if len(files) == 0 {
			fmt.Fprintf(os.Stderr, "%s: no corpus test files under %s\n", lv.name, lv.corpus)
			os.Exit(1)
		}
		rep, err := rulecov.Measure(rules, files, validator(lv.level))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", lv.name, err)
			os.Exit(1)
		}
		printLevel(lv.name, rep, *verbose)
		total += len(rep.Rules)
		tested += len(rep.Rules) - rep.Count(rulecov.Untested)
		detected += rep.Count(rulecov.Detected)
	}
	fmt.Printf("Overall: %d rules; the corpus tests %d, and pdf0 detects %d of those under the rule's own clause.\n",
		total, tested, detected)
	fmt.Println("A rule the corpus does not test is not measured here, in either direction.")
}

// validator validates at one level and returns the clauses reported.
func validator(lvl pdfa.Level) rulecov.Validator {
	return func(data []byte) ([]string, error) {
		doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		var clauses []string
		for _, v := range pdf0.ValidatePDFABytes(doc, lvl, data) {
			clauses = append(clauses, v.Rule)
		}
		return clauses, nil
	}
}

func printLevel(name string, rep rulecov.Report, verbose bool) {
	n := len(rep.Rules)
	untested := rep.Count(rulecov.Untested)
	fmt.Printf("=== %s: %d rules; %d tested by the corpus: %d detected, %d caught only under another clause, %d missed, %d unreadable; %d untested ===\n",
		name, n, n-untested, rep.Count(rulecov.Detected), rep.Count(rulecov.Elsewhere),
		rep.Count(rulecov.Missed), rep.Count(rulecov.Unreadable), untested)
	for _, s := range []rulecov.Status{rulecov.Missed, rulecov.Unreadable, rulecov.Elsewhere} {
		for _, r := range rep.Rules {
			if r.Status() != s {
				continue
			}
			fmt.Printf("  %-10s %-16s %s\n", s, r.ID(), firstSentence(r.Description))
			for _, ex := range r.Examples {
				fmt.Printf("  %-10s %-16s   %s\n", "", "", ex)
			}
		}
	}
	if verbose {
		for _, r := range rep.Rules {
			if r.Status() == rulecov.Untested {
				fmt.Printf("  %-10s %-16s %s\n", "untested", r.ID(), firstSentence(r.Description))
			}
		}
	}
	if len(rep.Orphans) > 0 {
		var names []string
		for _, o := range rep.Orphans {
			names = append(names, filepath.Base(o.Path))
		}
		fmt.Printf("  fail files naming a rule the profile does not define: %s\n", strings.Join(names, ", "))
	}
	fmt.Println()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func firstSentence(s string) string {
	if i := strings.IndexByte(s, '.'); i > 0 && i < 100 {
		return s[:i+1]
	}
	if len(s) > 100 {
		return s[:100] + "…"
	}
	return s
}
