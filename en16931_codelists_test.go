package pdf0

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// These tests keep the committed EN 16931 code-list tables faithful to the
// official CEN/TC 434 Schematron by re-extracting the code sets from it and
// asserting the committed maps match. They skip when the artefact suite is
// absent (run `make en16931-artefacts`), so the tables remain the runtime
// source and the Schematron is only the verification oracle.

// containsList pulls the space-delimited value set out of the first
// contains(' ... ', ...) test whose surrounding rule mentions anchor.
func containsListAfter(sch, anchor, charset string) []string {
	re := regexp.MustCompile(`(?s)` + anchor + `.*?contains\(' ([` + charset + ` ]+?) ',`)
	m := re.FindStringSubmatch(sch)
	if m == nil {
		return nil
	}
	return regexp.MustCompile(`\s+`).Split(trimSpace(m[1]), -1)
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 {
		if c := s[len(s)-1]; c == ' ' || c == '\n' || c == '\t' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

func readSchematron(t *testing.T, rel string) string {
	dir := en16931SuiteDir()
	if dir == "" {
		t.Skip("EN 16931 artefact suite not present; run `make en16931-artefacts`")
	}
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Skipf("schematron %s not present: %v", rel, err)
	}
	return string(b)
}

func TestEN16931CurrenciesFaithful(t *testing.T) {
	sch := readSchematron(t, "ubl/schematron/codelist/EN16931-UBL-codes.sch")
	got := containsListAfter(sch, `DocumentCurrencyCode`, `A-Z0-9`)
	assertSetMatches(t, "en16931Currencies", en16931Currencies, got)
}

func TestEN16931TypeCodesFaithful(t *testing.T) {
	// The CII code list carries the combined invoice + credit-note type-code set
	// (the same union the UBL invoice and credit-note lists form together).
	sch := readSchematron(t, "cii/schematron/codelist/EN16931-CII-codes.sch")
	got := containsListAfter(sch, `TypeCode`, `0-9`)
	assertSetMatches(t, "en16931TypeCodes", en16931TypeCodes, got)
}

func assertSetMatches(t *testing.T, name string, have map[string]bool, want []string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatalf("%s: could not extract the code set from the Schematron", name)
	}
	wantSet := map[string]bool{}
	for _, c := range want {
		wantSet[c] = true
		if !have[c] {
			t.Errorf("%s is missing %q, which the Schematron permits", name, c)
		}
	}
	for c := range have {
		if !wantSet[c] {
			t.Errorf("%s contains %q, which the Schematron does not list", name, c)
		}
	}
}
