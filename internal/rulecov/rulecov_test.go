package rulecov

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseName(t *testing.T) {
	cases := []struct {
		name   string
		clause string
		test   int
		pass   bool
		ok     bool
	}{
		{"veraPDF test suite 6-2-11-4-1-t02-fail-e.pdf", "6.2.11.4.1", 2, false, true},
		{"6-3-5-t03-pass-b.pdf", "6.3.5", 3, true, true},
		{"veraPDF test suite 6-9-t01-fail-a.pdf", "6.9", 1, false, true},
		{"isartor-6-1-2-t01-fail-a.pdf", "", 0, false, false},
		{"README.md", "", 0, false, false},
		{"veraPDF test suite 6-1-2-t01-fail-a.pdf.bak", "", 0, false, false},
	}
	for _, c := range cases {
		clause, test, pass, ok := ParseName(c.name)
		if clause != c.clause || test != c.test || pass != c.pass || ok != c.ok {
			t.Errorf("ParseName(%q) = %q, %d, %v, %v; want %q, %d, %v, %v",
				c.name, clause, test, pass, ok, c.clause, c.test, c.pass, c.ok)
		}
	}
}

// TestMeasure: each status arises from exactly the outcome it names, pass
// files are ignored, a rule with no fail file is untested, and a fail file for
// a rule the profile lacks is an orphan.
func TestMeasure(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) CorpusFile {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		clause, test, pass, ok := ParseName(name)
		if !ok {
			t.Fatalf("bad test name %q", name)
		}
		return CorpusFile{Path: p, Clause: clause, Test: test, Pass: pass}
	}
	rules := []Rule{
		{Clause: "6.1.1", Test: 1}, // detected
		{Clause: "6.1.2", Test: 1}, // elsewhere
		{Clause: "6.1.3", Test: 1}, // missed
		{Clause: "6.1.4", Test: 1}, // unreadable
		{Clause: "6.1.5", Test: 1}, // untested
	}
	files := []CorpusFile{
		write("6-1-1-t01-fail-a.pdf"),
		write("6-1-1-t01-fail-b.pdf"),
		write("6-1-1-t01-pass-a.pdf"),
		write("6-1-2-t01-fail-a.pdf"),
		write("6-1-2-t01-fail-b.pdf"),
		write("6-1-3-t01-fail-a.pdf"),
		write("6-1-4-t01-fail-a.pdf"),
		write("6-1-9-t01-fail-a.pdf"),
	}
	validate := func(data []byte) ([]string, error) {
		switch string(data) {
		case "6-1-1-t01-fail-a.pdf":
			return []string{"6.9", "6.1.1"}, nil
		case "6-1-1-t01-fail-b.pdf":
			return []string{"6.1.1"}, nil
		case "6-1-1-t01-pass-a.pdf":
			t.Error("a pass file was validated")
			return nil, nil
		case "6-1-2-t01-fail-a.pdf":
			return []string{"6.1.2"}, nil
		case "6-1-2-t01-fail-b.pdf":
			return []string{"6.1.20"}, nil // a prefix is not the clause
		case "6-1-3-t01-fail-a.pdf":
			return nil, nil
		case "6-1-4-t01-fail-a.pdf":
			return nil, errors.New("header not found")
		}
		return []string{"6.1.9"}, nil
	}
	rep, err := Measure(rules, files, validate)
	if err != nil {
		t.Fatal(err)
	}
	want := []Status{Detected, Elsewhere, Missed, Unreadable, Untested}
	for i, r := range rep.Rules {
		if r.Status() != want[i] {
			t.Errorf("%s: %v, want %v (%+v)", r.ID(), r.Status(), want[i], r)
		}
	}
	if rep.Rules[0].FailFiles != 2 || rep.Rules[0].Detected != 2 {
		t.Errorf("6.1.1: %+v, want 2 fail files, both detected", rep.Rules[0])
	}
	if len(rep.Orphans) != 1 || rep.Orphans[0].Clause != "6.1.9" {
		t.Errorf("orphans = %+v, want the 6.1.9 file", rep.Orphans)
	}
	for _, s := range want {
		if rep.Count(s) != 1 {
			t.Errorf("Count(%v) = %d, want 1", s, rep.Count(s))
		}
	}
}

// TestParseVerdicts: veraPDF's text report, with a path holding spaces, the
// mount prefix removed, a passing file, and a failing one's rules in ID form.
func TestParseVerdicts(t *testing.T) {
	report := `FAIL /data/6.2 Graphics/veraPDF test suite 6-2-10-t01-fail-f.pdf 2b
  FAIL 6.2.4.3-2
  FAIL 6.2.10-1
PASS /data/top.pdf 2b
Oct 29, 2026 WARNING: noise
`
	got, err := ParseVerdicts(strings.NewReader(report), "/data/")
	if err != nil {
		t.Fatal(err)
	}
	fail := got["6.2 Graphics/veraPDF test suite 6-2-10-t01-fail-f.pdf"]
	if fail.Pass || !slices.Equal(fail.Rules, []string{"6.2.4.3-t02", "6.2.10-t01"}) {
		t.Errorf("the failing file: %+v", fail)
	}
	if v, ok := got["top.pdf"]; !ok || !v.Pass || len(v.Rules) != 0 {
		t.Errorf("the passing file: %+v, %v", v, ok)
	}
	if len(got) != 2 {
		t.Errorf("%d verdicts, want 2: %v", len(got), got)
	}
	if _, err := ParseVerdicts(strings.NewReader("  FAIL 6.1-1\n")); err == nil {
		t.Error("a rule before any file was accepted")
	}
}

// TestMeasureVerdicts: each rule veraPDF fails a file on is tested by that
// file, whatever the file is named; a file veraPDF passes and pdf0 flags is a
// false positive; a file named for a rule veraPDF does not fail it on is
// counted as renamed; a file with no verdict is an error.
func TestMeasureVerdicts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"6-2-10-t01-fail-f.pdf", "6-1-2-t01-fail-a.pdf", "6-1-2-t01-pass-a.pdf", "6-1-3-t01-fail-a.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rules := []Rule{{Clause: "6.2.4.3", Test: 2}, {Clause: "6.2.10", Test: 1}, {Clause: "6.1.2", Test: 1}, {Clause: "6.1.3", Test: 1}}
	verdicts := map[string]Verdict{
		"6-2-10-t01-fail-f.pdf": {Rules: []string{"6.2.4.3-t02"}}, // named for 6.2.10-1, fails 6.2.4.3-2
		"6-1-2-t01-fail-a.pdf":  {Rules: []string{"6.1.2-t01"}},
		"6-1-2-t01-pass-a.pdf":  {Pass: true},
		"6-1-3-t01-fail-a.pdf":  {Rules: []string{"6.1.3-t01"}},
	}
	// pdf0, as this test has it: 6.2.4.3 for the renamed file, 6.1.2 for both
	// 6.1.2 files (a false positive on the passing one), nothing for 6.1.3.
	validate := func(data []byte) ([]string, error) {
		switch string(data) {
		case "6-2-10-t01-fail-f.pdf":
			return []string{"6.2.4.3"}, nil
		case "6-1-2-t01-fail-a.pdf", "6-1-2-t01-pass-a.pdf":
			return []string{"6.1.2"}, nil
		}
		return nil, nil
	}
	rep, err := MeasureVerdicts(rules, dir, verdicts, validate)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]Status{}
	for _, r := range rep.Rules {
		status[r.ID()] = r.Status()
	}
	want := map[string]Status{"6.2.4.3-t02": Detected, "6.2.10-t01": Untested, "6.1.2-t01": Detected, "6.1.3-t01": Missed}
	for id, s := range want {
		if status[id] != s {
			t.Errorf("%s: %v, want %v", id, status[id], s)
		}
	}
	if len(rep.FalsePositives) != 1 || rep.Renamed != 1 || rep.Files != 4 {
		t.Errorf("false positives %v, renamed %d, files %d; want 1, 1, 4", rep.FalsePositives, rep.Renamed, rep.Files)
	}
	delete(verdicts, "6-1-3-t01-fail-a.pdf")
	if _, err := MeasureVerdicts(rules, dir, verdicts, validate); err == nil {
		t.Error("a file with no verdict was measured")
	}
}
