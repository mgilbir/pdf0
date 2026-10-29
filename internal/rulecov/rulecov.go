// Package rulecov measures the PDF/A validator's coverage of the veraPDF rule
// inventory by what the validator detects, rule by rule, not by which clause
// numbers appear in its source.
//
// # Why not the source scan
//
// Coverage used to be measured by collecting every quoted ISO clause literal
// ("6.2.11", …) in the module's non-test source and checking each veraPDF
// profile clause against that set. It reported 181/181 at every level and could
// no longer find a gap (audit 2026-09-22 C156), for three reasons that all push
// the same way:
//
//   - The set was one for all levels. A literal written for one level covered
//     the same clause number at every level, so "covered at 1b" meant "the
//     string occurs somewhere in the source".
//   - It was at clause granularity. The profiles define 528 rules (testNumber
//     within clause) across those 181 clauses; one emitted clause marks all of
//     a clause's rules covered.
//   - It never ran the validator. A clause the source mentions but no check
//     emits for the input it is about was still covered.
//
// Once every clause string had been written somewhere, the number could only
// read 181/181.
//
// # What is measured instead
//
// The veraPDF corpus names each test file after the rule it exercises:
// "veraPDF test suite 6-2-11-4-1-t02-fail-a.pdf" is a file that must fail rule
// (6.2.11.4.1, test 2). For each profile rule at a level, Measure validates
// every fail file for that rule at that level and asks whether pdf0 reports a
// violation under that rule's clause:
//
//   - Detected: every fail file is flagged under the rule's clause.
//   - Elsewhere: every fail file is flagged, but at least one only under other
//     clauses. The file is rejected, so TestCorpus counts it as caught, but
//     not for the reason the rule describes — either the rule is not
//     implemented and the file trips another check, or it is implemented under
//     a different clause number. Either way it is a gap to look at.
//   - Missed: some fail file is not flagged at all (TestCorpus's "missed").
//   - Unreadable: some fail file does not parse.
//   - Untested: the corpus has no fail file for the rule, so nothing here can
//     say whether pdf0 implements it.
//
// The measure is only as wide as the corpus, and a clause match is still not
// proof the right check fired; but it can find a gap, which the source scan no
// longer could.
package rulecov

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Rule is one veraPDF profile rule.
type Rule struct {
	Clause      string // ISO 19005 clause, e.g. "6.2.11.4.1"
	Test        int    // the profile's testNumber within the clause
	Description string
}

// ID is the rule's name in the corpus file-name form, e.g. "6.2.11.4.1-t02".
func (r Rule) ID() string { return fmt.Sprintf("%s-t%02d", r.Clause, r.Test) }

// LoadProfile reads the rules of a combined veraPDF validation profile.
func LoadProfile(path string) ([]Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p struct {
		Rules []struct {
			ID struct {
				Clause     string `xml:"clause,attr"`
				TestNumber string `xml:"testNumber,attr"`
			} `xml:"id"`
			Description string `xml:"description"`
		} `xml:"rules>rule"`
	}
	if err := xml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var rules []Rule
	for _, r := range p.Rules {
		n, err := strconv.Atoi(r.ID.TestNumber)
		if err != nil || r.ID.Clause == "" {
			return nil, fmt.Errorf("%s: rule with clause %q testNumber %q", path, r.ID.Clause, r.ID.TestNumber)
		}
		rules = append(rules, Rule{Clause: r.ID.Clause, Test: n, Description: strings.Join(strings.Fields(r.Description), " ")})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%s: no rules", path)
	}
	return rules, nil
}

// CorpusFile is one veraPDF test file and the rule its name says it tests.
type CorpusFile struct {
	Path   string
	Clause string
	Test   int
	Pass   bool
}

// testFileName matches the veraPDF corpus naming: an optional "veraPDF test
// suite " prefix, the clause with dashes, the test number, pass or fail, and a
// variant letter.
var testFileName = regexp.MustCompile(`^(?:veraPDF test suite )?(6(?:-\d+)+)-t(\d+)-(pass|fail)-[a-z]+\.pdf$`)

// ParseName reports the rule a corpus file name tests.
func ParseName(base string) (clause string, test int, pass bool, ok bool) {
	m := testFileName.FindStringSubmatch(base)
	if m == nil {
		return "", 0, false, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false, false
	}
	return strings.ReplaceAll(m[1], "-", "."), n, m[3] == "pass", true
}

// CorpusFiles walks dir (following a symlinked root) and returns the test
// files whose names follow the corpus convention, sorted by path. Files that do
// not (Isartor and TWG files, READMEs) are skipped.
func CorpusFiles(dir string) ([]CorpusFile, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	var out []CorpusFile
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		clause, test, pass, ok := ParseName(d.Name())
		if ok {
			out = append(out, CorpusFile{Path: p, Clause: clause, Test: test, Pass: pass})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Validator validates one file's bytes at the level being measured and returns
// the clauses of the violations it reports. An error means the file did not
// parse.
type Validator func(data []byte) (clauses []string, err error)

// Status classifies a rule; see the package documentation.
type Status int

const (
	Untested Status = iota
	Detected
	Elsewhere
	Missed
	Unreadable
)

func (s Status) String() string {
	switch s {
	case Untested:
		return "untested"
	case Detected:
		return "detected"
	case Elsewhere:
		return "elsewhere"
	case Missed:
		return "missed"
	case Unreadable:
		return "unreadable"
	}
	return "?"
}

// RuleResult is one rule's outcome over its fail files.
type RuleResult struct {
	Rule
	FailFiles  int
	Detected   int      // fail files flagged under the rule's clause
	Elsewhere  int      // flagged, but only under other clauses
	Missed     int      // not flagged at all
	Unreadable int      // did not parse
	Examples   []string // for Elsewhere/Missed/Unreadable: the files, with what was reported
}

// Status is the rule's classification: the worst outcome over its files.
func (r RuleResult) Status() Status {
	switch {
	case r.FailFiles == 0:
		return Untested
	case r.Unreadable > 0:
		return Unreadable
	case r.Missed > 0:
		return Missed
	case r.Elsewhere > 0:
		return Elsewhere
	}
	return Detected
}

// Report is one level's measurement.
type Report struct {
	Rules []RuleResult
	// Orphans are fail files whose rule the profile does not define.
	Orphans []CorpusFile
}

// Count returns how many rules have the given status.
func (r Report) Count(s Status) int {
	n := 0
	for _, rr := range r.Rules {
		if rr.Status() == s {
			n++
		}
	}
	return n
}

// Measure validates every fail file against the rule it names and classifies
// every profile rule.
func Measure(rules []Rule, files []CorpusFile, validate Validator) (Report, error) {
	index := map[string]int{}
	results := make([]RuleResult, len(rules))
	for i, r := range rules {
		results[i].Rule = r
		// A profile may list the same (clause, test) twice; the first wins.
		if _, dup := index[r.ID()]; !dup {
			index[r.ID()] = i
		}
	}
	var rep Report
	for _, f := range files {
		if f.Pass {
			continue
		}
		i, ok := index[Rule{Clause: f.Clause, Test: f.Test}.ID()]
		if !ok {
			rep.Orphans = append(rep.Orphans, f)
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return Report{}, err
		}
		rr := &results[i]
		rr.FailFiles++
		clauses, err := validate(data)
		base := filepath.Base(f.Path)
		switch {
		case err != nil:
			rr.Unreadable++
			rr.Examples = append(rr.Examples, fmt.Sprintf("%s: %v", base, err))
		case len(clauses) == 0:
			rr.Missed++
			rr.Examples = append(rr.Examples, base+": no violations")
		case contains(clauses, f.Clause):
			rr.Detected++
		default:
			rr.Elsewhere++
			rr.Examples = append(rr.Examples, base+": reported "+strings.Join(dedupe(clauses), ", "))
		}
	}
	rep.Rules = results
	return rep, nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// # Measured against veraPDF's verdicts
//
// A corpus file's name says the rule it was written for, and it is not always
// the rule veraPDF fails it on: a file named for 6.2.10-t01 that veraPDF fails
// under 6.2.4.3-2 counts, by its name, as a 6.2.10 file pdf0 caught only
// "elsewhere", when pdf0 reports exactly what veraPDF does. MeasureVerdicts
// asks the reference validator instead. For every file veraPDF fails, each
// rule it fails the file on is a test of that rule, classified as Measure
// classifies one; every file veraPDF passes that pdf0 flags is a false
// positive. The verdicts are veraPDF's own report (Verdicts), generated from
// the corpus and never committed.

// Verdict is what veraPDF said of one file: whether it passed, and the rules
// it failed, as Rule.ID writes them.
type Verdict struct {
	Pass  bool
	Rules []string
}

// verdictRule is a rule line of veraPDF's text report: "  FAIL 6.2.4.3-2".
var verdictRule = regexp.MustCompile(`^\s+FAIL ((?:\d+\.)*\d+)-(\d+)\s*$`)

// ParseVerdicts reads veraPDF's text report (--format text -v), keyed by each
// file's path with the first of prefixes it starts with removed: the
// directory the report was made over, as veraPDF saw it.
func ParseVerdicts(r io.Reader, prefixes ...string) (map[string]Verdict, error) {
	out := map[string]Verdict{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var current string
	for sc.Scan() {
		line := sc.Text()
		if m := verdictRule.FindStringSubmatch(line); m != nil {
			if current == "" {
				return nil, fmt.Errorf("rulecov: rule %s-%s in veraPDF's report belongs to no file", m[1], m[2])
			}
			n, _ := strconv.Atoi(m[2])
			v := out[current]
			v.Rules = append(v.Rules, Rule{Clause: m[1], Test: n}.ID())
			out[current] = v
			continue
		}
		var pass bool
		switch {
		case strings.HasPrefix(line, "PASS "):
			pass = true
		case strings.HasPrefix(line, "FAIL "):
		default:
			continue
		}
		// "FAIL <path> <flavour>": the path may hold spaces, the flavour not.
		rest := line[len("FAIL "):]
		i := strings.LastIndexByte(rest, ' ')
		if i < 0 {
			return nil, fmt.Errorf("rulecov: %q in veraPDF's report names no flavour", line)
		}
		path := rest[:i]
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				path = strings.TrimPrefix(path, p)
				break
			}
		}
		current = filepath.ToSlash(path)
		out[current] = Verdict{Pass: pass}
	}
	return out, sc.Err()
}

// VerdictReport is one level measured against veraPDF's verdicts.
type VerdictReport struct {
	Report
	// FalsePositives are the files veraPDF passes and pdf0 flags, each with
	// what pdf0 reported.
	FalsePositives []string
	// Renamed counts the files veraPDF fails on rules that do not include the
	// one their name says: what measuring by name got wrong.
	Renamed int
	// Files is how many files were judged.
	Files int
}

// MeasureVerdicts validates every PDF under dir and judges it by veraPDF's
// verdict on it (see the section above). A file veraPDF has no verdict on is
// an error: the verdicts are of another corpus, and nothing measured against
// them would mean anything.
func MeasureVerdicts(rules []Rule, dir string, verdicts map[string]Verdict, validate Validator) (VerdictReport, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return VerdictReport{}, err
	}
	index := map[string]int{}
	results := make([]RuleResult, len(rules))
	for i, r := range rules {
		results[i].Rule = r
		if _, dup := index[r.ID()]; !dup {
			index[r.ID()] = i
		}
	}
	var rep VerdictReport
	var paths []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return VerdictReport{}, err
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return VerdictReport{}, err
		}
		rel = filepath.ToSlash(rel)
		v, ok := verdicts[rel]
		if !ok {
			return VerdictReport{}, fmt.Errorf("rulecov: veraPDF has no verdict on %s; the verdicts are not of this corpus (regenerate them)", rel)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return VerdictReport{}, err
		}
		rep.Files++
		clauses, verr := validate(data)
		base := filepath.Base(p)
		if v.Pass {
			if verr == nil && len(clauses) > 0 {
				rep.FalsePositives = append(rep.FalsePositives, base+": reported "+strings.Join(dedupe(clauses), ", "))
			}
			continue
		}
		if clause, test, _, named := ParseName(base); named && !contains(v.Rules, Rule{Clause: clause, Test: test}.ID()) {
			rep.Renamed++
		}
		for _, id := range dedupe(v.Rules) {
			i, ok := index[id]
			if !ok {
				rep.Orphans = append(rep.Orphans, CorpusFile{Path: p})
				continue
			}
			rr := &results[i]
			rr.FailFiles++
			clause := rr.Clause
			switch {
			case verr != nil:
				rr.Unreadable++
				rr.Examples = append(rr.Examples, fmt.Sprintf("%s: %v", base, verr))
			case len(clauses) == 0:
				rr.Missed++
				rr.Examples = append(rr.Examples, base+": no violations")
			case contains(clauses, clause):
				rr.Detected++
			default:
				rr.Elsewhere++
				rr.Examples = append(rr.Examples, base+": reported "+strings.Join(dedupe(clauses), ", "))
			}
		}
	}
	rep.Rules = results
	return rep, nil
}
