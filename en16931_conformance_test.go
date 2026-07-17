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

// This test uses the official CEN/TC 434 EN 16931 unit-test suite as a
// differential oracle for the syntax-neutral rule engine (validateEN16931). The
// suite (ConnectingEurope/eInvoicing-EN16931, EUPL-1.2) is a set of per-rule
// testSets: each carries minimal UBL fragments tagged <success>BR-XX</success>
// (the fragment satisfies rule BR-XX) or <error>BR-XX</error> (the fragment
// violates it). We feed each fragment through ValidateFacturXInvoice and check
// our verdict for that one rule against the tag.
//
// The suite is not vendored; clone it with `make en16931-artefacts` (gitignored).
// The test skips when the directory is absent.

// en16931SuiteDir returns the artefact directory, or "" if it is not present.
func en16931SuiteDir() string {
	if d := os.Getenv("EN16931_ARTEFACTS"); d != "" {
		return d
	}
	d := filepath.Join("testdata", "en16931-artefacts")
	if _, err := os.Stat(filepath.Join(d, "test", "Invoice-unit-UBL")); err == nil {
		return d
	}
	return ""
}

type en16931TestSet struct {
	Tests []struct {
		Assert struct {
			Success string `xml:"success"`
			Error   string `xml:"error"`
		} `xml:"assert"`
		Inner string `xml:",innerxml"`
	} `xml:"test"`
}

// documentFragment extracts the embedded UBL document element (an Invoice or
// CreditNote, each self-contained with its namespace declarations) from a
// <test> element's inner XML. The root may carry a namespace prefix (some
// fragments deliberately test prefixed namespaces, e.g. <ns:CreditNote>).
var documentRootRE = regexp.MustCompile(`<(?:[\w.-]+:)?(?:Invoice|CreditNote)[\s>]`)

func documentFragment(inner string) string {
	if loc := documentRootRE.FindStringIndex(inner); loc != nil {
		return inner[loc[0]:]
	}
	return ""
}

func hasFacturXRule(vs []FacturXViolation, rule string) bool {
	for _, v := range vs {
		if v.Rule == rule {
			return true
		}
	}
	return false
}

// TestEN16931ConformanceSuite runs the CEN unit-test fragments as a per-rule
// oracle: on a <success> fragment our engine must NOT report the rule (a false
// positive if it does); on an <error> fragment reporting the rule counts as
// caught. False positives must be zero; caught rules are ratcheted.
func TestEN16931ConformanceSuite(t *testing.T) {
	dir := en16931SuiteDir()
	if dir == "" {
		t.Skip("EN 16931 artefact suite not present; run `make en16931-artefacts`")
	}
	var files []string
	for _, sub := range []string{"Invoice-unit-UBL", "CreditNote-unit-UBL"} {
		fs, _ := filepath.Glob(filepath.Join(dir, "test", sub, "*.xml"))
		files = append(files, fs...)
	}
	if len(files) == 0 {
		t.Fatalf("no unit-test files found under %s/test", dir)
	}

	var falsePositives []string
	caught := map[string]bool{}
	errorSeen := map[string]bool{}
	var harnessErr int

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var ts en16931TestSet
		if err := xml.Unmarshal(data, &ts); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, tc := range ts.Tests {
			rule := strings.TrimSpace(tc.Assert.Success)
			isError := false
			if rule == "" {
				rule = strings.TrimSpace(tc.Assert.Error)
				isError = true
			}
			if rule == "" {
				continue
			}
			doc := documentFragment(tc.Inner)
			if doc == "" {
				harnessErr++
				continue
			}
			vs := ValidateFacturXInvoice([]byte(doc), FacturXEN16931)
			reports := hasFacturXRule(vs, rule)
			if isError {
				errorSeen[rule] = true
				if reports {
					caught[rule] = true
				}
			} else if reports {
				falsePositives = append(falsePositives,
					filepath.Base(f)+" ["+rule+"]")
			}
		}
	}

	sort.Strings(falsePositives)

	// False positives are non-negotiable: on a fragment that satisfies a rule,
	// our engine must not report that rule.
	if len(falsePositives) != 0 {
		t.Errorf("EN 16931 conformance false positives: %d", len(falsePositives))
		for _, fp := range falsePositives {
			t.Errorf("  FP %s", fp)
		}
	}

	// The extractor must handle every fragment; a failure is a harness bug.
	if harnessErr != 0 {
		t.Errorf("failed to extract the document fragment from %d test cases", harnessErr)
	}

	// Ratchet the number of rules whose error fragments we catch. It only goes
	// up as rule coverage grows; a drop means a regression.
	const caughtBaseline = 88
	if len(caught) < caughtBaseline {
		var caughtList []string
		for r := range caught {
			caughtList = append(caughtList, r)
		}
		sort.Strings(caughtList)
		t.Errorf("caught %d/%d rules, below baseline %d; coverage regressed: %v",
			len(caught), len(errorSeen), caughtBaseline, caughtList)
	}
	t.Logf("EN 16931 conformance: 0 false positives, %d/%d rules caught (baseline %d)",
		len(caught), len(errorSeen), caughtBaseline)
}
