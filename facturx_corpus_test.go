package pdf0

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/formalis"
)

// TestValidateFacturXInvoiceCorpus is the oracle for the seam between the
// container and the invoice rule engine: for every conforming Factur-X / ZUGFeRD
// sample the XML pdf0 extracted must be readable as XML, and the rule set pdf0
// routes it to must be the one the container's metadata named. Skips when the
// corpus is absent.
//
// It asserts readability and routing rather than zero findings. Which business
// rules fire is formalis's scope decision, ratcheted in TestValidateFacturXCorpus
// (facturxInvoiceRuleFindings); what pdf0 owns is that a container it accepts
// yields XML the engine can read and hands it to the right rule set.
func TestValidateFacturXInvoiceCorpus(t *testing.T) {
	files, _ := filepath.Glob("testdata/facturx/*.pdf")
	if len(files) == 0 {
		t.Skip("Factur-X corpus not present")
	}
	sort.Strings(files)
	seen := 0
	for _, f := range files {
		name := filepath.Base(f)
		if strings.HasPrefix(name, "FAIL") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		res := ValidateFacturX(doc, data)
		if len(res.XML) == 0 {
			continue
		}
		rep, err := res.ValidateInvoiceXML(context.Background())
		if err != nil {
			t.Errorf("%s [%s]: invoice XML could not be read: %v", name, res.Profile, err)
			continue
		}
		// A rule set was chosen and it ran. formalis reports a profile it does not
		// implement as its reserved "profile" rule and validates nothing, so this
		// catches a container whose conformance level pdf0 read but could not route.
		for _, v := range rep.Violations {
			if v.Rule == formalis.RuleProfile || v.Rule == formalis.RuleRoot {
				t.Errorf("%s [%s/%s]: the invoice was not routed to a rule set: %s",
					name, res.Profile, res.CIUS, v)
			}
		}
		// The same report the validator saw: the findings it adopted, and the
		// coverage it recorded on the result.
		if got, want := len(rep.NotEvaluated), len(res.InvoiceNotEvaluated); got != want {
			t.Errorf("%s: result carries %d unevaluated families, the report has %d", name, want, got)
		}
		if rep.Complete() != res.InvoiceComplete {
			t.Errorf("%s: result says complete=%v, the report says %v", name, res.InvoiceComplete, rep.Complete())
		}
		seen++
	}
	// A test that silently exercised nothing prints ok and proves nothing.
	if seen == 0 {
		t.Fatal("no Factur-X invoice XML was validated")
	}
}
