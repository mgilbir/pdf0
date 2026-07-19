package pdf0

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNLCIUSConformanceSuite uses the SimplerInvoicing SI-UBL instance test suite
// as the FP=0 oracle. Each file name encodes the rule it exercises and whether the
// instance satisfies it (…_ok_…), violates it (…_error_…) or merely uses a
// discouraged term (…_warning_…). Like the CEN unit tests, each file targets one
// rule and is not otherwise guaranteed clean, so the assertions are per-family:
//   - _error_ : at least one NLCIUS (BR-NL-*) rule must fire (the broken instance
//     is caught);
//   - _ok_ / _warning_ : no BR-NL rule may fire (no false positive; advisory
//     "not recommended" warnings are not emitted).
//
// The suite is not vendored; the test skips when testdata/nlcius is absent (run
// `make cius-oracles`).
func TestNLCIUSConformanceSuite(t *testing.T) {
	files, _ := filepath.Glob("testdata/nlcius/testsuite/*.xml")
	if len(files) == 0 {
		t.Skip("NLCIUS test suite not present (make cius-oracles)")
	}
	brNL := func(vs []FacturXViolation) []string {
		var r []string
		for _, v := range vs {
			if strings.HasPrefix(v.Rule, "BR-NL-") {
				r = append(r, v.Rule)
			}
		}
		return r
	}
	var falsePositives, missed []string
	caught := 0
	for _, f := range files {
		base := filepath.Base(f)
		if !strings.Contains(base, "BR-NL-") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		nl := brNL(ValidateNLCIUS(data))
		switch {
		case strings.Contains(base, "_error"):
			if len(nl) > 0 {
				caught++
			} else {
				missed = append(missed, base)
			}
		default: // _ok_ or _warning_
			if len(nl) > 0 {
				falsePositives = append(falsePositives, base+" -> "+strings.Join(nl, ","))
			}
		}
	}
	for _, fp := range falsePositives {
		t.Errorf("NLCIUS false positive on a valid/advisory instance: %s", fp)
	}
	for _, m := range missed {
		t.Errorf("NLCIUS failed to catch a broken instance: %s", m)
	}
	t.Logf("NLCIUS conformance: %d error instances caught, %d false positives", caught, len(falsePositives))
}
