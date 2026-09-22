package rulecov

import (
	"errors"
	"os"
	"path/filepath"
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
